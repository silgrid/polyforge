package driver

// handoff.go: the driver's reaction to a step's hand-off (workflow contract
// 4, design #483 §7). A dispatch ends with one `pf step handoff`; the
// daemon checks it on the spot and records it on the capsule's call ledger
// (runtime-state/step-calls.jsonl, stepcall). The ledger's record of the
// dispatch's accepted hand-off is the one result the driver reacts to —
// never artifacts/handoff.md, which the driver only appends its event
// sections to:
//
//   - done: the step round's pending hub writes are made (pendingwrites;
//     a failure parks the work item blocked, kind write_failed, and the
//     resume replays them before anything else), then the step is done and
//     the next one dispatches — the last one's done completes the lane;
//   - fail (a gate): the round's pending writes are dropped, a rewind
//     (回退) event section is appended and the lane rewinds to the gate's
//     on_fail target, charged to max_rewinds unless the hand-off cites the owner
//     (--per-owner); a gate without on_fail ends the work item failed;
//   - blocked: the writes are dropped and the work item parks;
//   - cancel / waive / rewind (each citing the owner): the writes are
//     dropped; the work item is cancelled, the gate's FAIL is accepted and
//     the next step runs, or the lane goes back to the named step (this
//     step's own id runs it again), charged to nothing, with a rewind event.
//
// The daemon never reads the hand-off's body or the owner's words: the
// status, the rewind target and the cited message's stamp are the facts
// it acts on. The reaction rides the existing transitions (the hand-off
// is shaped into the result they route: handoffShape).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/crashpoint"
	"github.com/silgrid/polyforge/internal/polyforge/pendingwrites"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
	"github.com/silgrid/polyforge/internal/polyforge/taskrepo"
)

// BlockerKindWriteFailed is the blocked kind of a pending hub write the
// daemon could not make when its step round handed off done: the writes
// not made stay pending on the ledger and the resume replays them before
// the next step.
const BlockerKindWriteFailed = "write_failed"

// writeFailedPrefix opens the blocked reason of a write_failed park.
const writeFailedPrefix = "write failed: "

// WriteFailedBlocked reports whether a blocked reason is a write_failed
// park.
func WriteFailedBlocked(reason string) bool { return strings.HasPrefix(reason, writeFailedPrefix) }

// WriteFailedRequiredAction is what releases a write_failed park.
const WriteFailedRequiredAction = "fix the cause (aihub's refusal, or its reachability), then resume the work item (`pf resume <wi>`): the daemon makes the writes not made yet before the next step, or cancel it"

// BlockerKindStepBlocked is the blocked kind of a step's own `blocked`
// hand-off (the watch's kind name since workflow contract 2).
const BlockerKindStepBlocked = "incomplete"

// stepBlockedPrefix opens the blocked reason of a step's own `blocked`
// hand-off (StepBlockedReason).
const stepBlockedPrefix = "step blocked: "

// StepBlockedRequiredAction is what releases a step's own park.
const StepBlockedRequiredAction = "read the step's section of artifacts/handoff.md and act on what it needs: a message to the work item wakes the step"

// StepBlockedReason is the blocked reason of step's `blocked` hand-off:
// its reason and where its section is (the blocker handoffShape gives
// it). The blocked projection recognizes it (StepBlocked) and splits it
// back (StepBlockedParts).
func StepBlockedReason(step string, blocker map[string]any) string {
	reason, _ := blocker["reason"].(string)
	evidence, _ := blocker["evidence"].(string)
	if strings.TrimSpace(reason) == "" {
		reason = "step " + step + " handed off blocked"
	}
	return stepBlockedPrefix + reason + stepBlockedEvidence + evidence
}

// stepBlockedEvidence separates a step park's reason from its evidence.
const stepBlockedEvidence = " — evidence: "

// StepBlocked reports whether a blocked reason is a step's own park.
func StepBlocked(reason string) bool { return strings.HasPrefix(reason, stepBlockedPrefix) }

// StepBlockedParts splits a step park's blocked reason into the hand-off's
// reason and its evidence (the section of artifacts/handoff.md).
func StepBlockedParts(reason string) (why, evidence string) {
	rest := strings.TrimPrefix(reason, stepBlockedPrefix)
	if i := strings.LastIndex(rest, stepBlockedEvidence); i >= 0 {
		return rest[:i], rest[i+len(stepBlockedEvidence):]
	}
	return rest, ""
}

// ledgerHandoff answers the dispatch (step, spawn)'s accepted hand-off on
// the capsule's call ledger as the result the driver settles (nil when
// the dispatch has not handed off, or the ledger cannot be read).
func ledgerHandoff(capsuleDir, wi, step string, spawn int64) *StepResult {
	if capsuleDir == "" || spawn <= 0 {
		return nil
	}
	lines, err := stepcall.Read(capsuleDir)
	if err != nil {
		return nil
	}
	l := stepcall.HandoffOf(lines, step, spawn)
	if l == nil {
		return nil
	}
	res := &StepResult{WIID: wi, StepID: step, SpawnN: spawn, Handoff: l.Handoff, HandoffCall: l.Call}
	if t, err := time.Parse(time.RFC3339Nano, l.At); err == nil {
		res.SubmittedAt = t
	}
	return res
}

// handoffShape shapes a hand-off into the result the transitions route:
// done → completed; fail → gate_failed; blocked → incomplete (its reason
// as the blocker); cancel → the cancel intent; waive → the gate_failed
// waive intent; rewind → the rewind intent, or the retry intent when it
// names the step itself. An intent rests on the --per-owner message. A
// blocker the result already carries is kept (a hand-off has none; a test
// double may).
func handoffShape(res *StepResult) {
	h := res.Handoff
	reason := strings.TrimSpace(h.Reason)
	where := fmt.Sprintf("artifacts/handoff.md, the section of step %s's call %d", res.StepID, res.HandoffCall)
	if reason == "" {
		reason = "see " + where
	}
	res.Intent = nil
	failOutput := func() {
		raw, _ := json.Marshal(map[string]any{"verdict": "fail", "fail_reason": reason})
		res.Output = raw
	}
	switch h.Status {
	case stepcall.StatusDone:
		res.Outcome = OutcomeCompleted
	case stepcall.StatusFail:
		res.Outcome = OutcomeGateFailed
		failOutput()
	case stepcall.StatusBlocked:
		res.Outcome = OutcomeIncomplete
		if res.Blocker == nil {
			res.Blocker = map[string]any{
				"reason":          reason,
				"evidence":        where,
				"required_action": StepBlockedRequiredAction,
			}
		}
	case stepcall.StatusCancel:
		res.Outcome = OutcomeIntent
		res.Intent = &StepIntent{Kind: IntentCancel, ReplyAt: h.PerOwner, Reason: reason}
	case stepcall.StatusWaive:
		res.Outcome = OutcomeGateFailed
		res.Intent = &StepIntent{Kind: IntentWaive, ReplyAt: h.PerOwner, Reason: reason}
		failOutput()
	case stepcall.StatusRewind:
		res.Outcome = OutcomeIntent
		if h.To == res.StepID {
			res.Intent = &StepIntent{Kind: IntentRetry, ReplyAt: h.PerOwner, Reason: reason}
		} else {
			res.Intent = &StepIntent{Kind: IntentRewind, ReplyAt: h.PerOwner, To: h.To, Reason: reason}
		}
	}
}

// settleHandoff reacts to one accepted hand-off of dispatch n (run is nil
// for a hand-off a dead predecessor's dispatch left on the ledger). The
// round's pending writes are made (done) or dropped (any other status)
// before the transition that moves the lane: a crash between the two
// settles the hand-off again, and the ledger makes neither twice. stop
// reports that Run must return err.
func (d *Driver) settleHandoff(ctx context.Context, stepID string, n int64, res *StepResult) (stop bool, err error) {
	handoffShape(res)
	h := res.Handoff
	var msg *capsule.AskLine
	if res.Intent != nil {
		if msg = d.ownerMessageAt(stepID, res.Intent.ReplyAt); msg == nil {
			reason := fmt.Sprintf("step %s handed off %s citing the owner message %s, which the record no longer holds for its round (runtime-state/asks.jsonl): %s", stepID, h.Status, res.Intent.ReplyAt, "send the step your word again, or resume or cancel the work item")
			return d.blockHandoff(stepID, n, reason)
		}
	}
	round := d.stepRound(stepID)
	if h.Status == stepcall.StatusDone {
		if _, werr := pendingwrites.Execute(ctx, d.wi.CapsuleDir, stepID, round, d.cfg.HubWrites); werr != nil {
			if ctx.Err() != nil {
				return true, ctx.Err()
			}
			reason := writeFailedPrefix + fmt.Sprintf("step %s handed off done and the daemon could not make its pending hub writes — %v; the writes not made stay pending on the call ledger: %s", stepID, werr, WriteFailedRequiredAction)
			if stop, err := d.blockHandoff(stepID, n, reason); err != nil {
				return stop, err
			}
			d.emit("blocked", map[string]any{"step": stepID, "reason": reason, "kind": BlockerKindWriteFailed, "report": false})
			return true, nil
		}
	} else if dropped, derr := pendingwrites.Drop(d.wi.CapsuleDir, stepID, round, "the step round handed off "+h.Status); derr != nil {
		return true, fmt.Errorf("driver: step %s: the pending writes of round %d could not be dropped: %w", stepID, round, derr)
	} else if len(dropped) > 0 {
		d.emit("writes_dropped", map[string]any{"step": stepID, "round": round, "status": h.Status, "ops": dropped})
	}
	crashpoint.At("driver.writes:" + stepID)
	before := d.Snapshot()
	if err := d.transition(TransitionRequest{
		Kind:         TransStepResult,
		StepID:       stepID,
		Result:       res,
		SpawnN:       n,
		WallMs:       d.takeUnbooked(stepID),
		OwnerMessage: msg,
	}); err != nil {
		return true, fmt.Errorf("driver: step %s hand-off transition failed: %w", stepID, err)
	}
	crashpoint.At("driver.settled:" + stepID)
	d.handoffEvents(stepID, res, before, d.Snapshot())
	return res.Outcome == OutcomeIncomplete, nil
}

// blockHandoff parks the work item blocked on the driver's own authority
// with the hand-off's dispatch still the step's (Step.Dispatch kept): the
// resume settles the same hand-off again.
func (d *Driver) blockHandoff(stepID string, n int64, reason string) (bool, error) {
	if err := d.transition(TransitionRequest{Kind: TransBlocked, StepID: stepID, Reason: reason, SpawnN: n}); err != nil {
		return true, fmt.Errorf("driver: step %s hand-off park failed: %w", stepID, err)
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// artifacts/handoff.md: the daemon's opening and its event sections.

// labels answers the stamp labels of the live agent language now
// (Config.Language; nil = the default): the opening and every event
// section take the value at the moment the daemon writes them.
func (d *Driver) labels() stepcall.Labels {
	lang := ""
	if d.cfg.Language != nil {
		lang = d.cfg.Language()
	}
	return stepcall.LabelsOf(lang)
}

// writeHandoffOpening writes the hand-off document's opening — the work
// item, its goal, its acceptance section verbatim and the lane's step
// order — once, at the claim's first run (a document that exists is left
// as it is: it only ever grows). A failure is reported and never stops
// the lane: the document is the steps' reading, not the daemon's state.
func (d *Driver) writeHandoffOpening() {
	path := stepcall.HandoffPath(d.wi.CapsuleDir)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return
	}
	l := d.labels()
	doc, _ := readWorkItemDoc(d.wi.CapsuleDir)
	ref := doc.Slug
	if ref == "" {
		ref = d.wi.ID
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s · %s\n\n", ref, l.Doc)
	fmt.Fprintf(&b, "<!-- %s -->\n\n", l.OpeningNote)
	fmt.Fprintf(&b, "| %s | %s |\n|---|---|\n", l.Item, l.Value)
	cell := func(k, v string) {
		if v == "" {
			v = "—"
		}
		fmt.Fprintf(&b, "| %s | %s |\n", k, strings.ReplaceAll(strings.ReplaceAll(v, "|", `\|`), "\n", " "))
	}
	cell(l.WorkItem, fmt.Sprintf("%s (`%s`)", ref, d.wi.ID))
	cell(l.Project, doc.Project)
	cell(l.Lane, d.workflow.Name)
	cell(l.Goal, doc.Goal)
	fmt.Fprintf(&b, "\n## %s\n\n", l.Acceptance)
	content := ""
	if doc.Content != nil {
		content = *doc.Content
	}
	if text, ok := workflow.AcceptanceSection(content); ok && strings.TrimSpace(text) != "" {
		b.WriteString(text)
		b.WriteString("\n")
	} else {
		b.WriteString(l.NoAcceptance + "\n")
	}
	fmt.Fprintf(&b, "\n## %s\n\n", l.StepOrder)
	for i, s := range d.workflow.Steps {
		fmt.Fprintf(&b, "%d. `%s`", i+1, s.ID)
		var notes []string
		if s.Gate {
			note := "gate"
			if s.OnFail != nil && s.OnFail.Target != "" {
				note += fmt.Sprintf(l.GateRewindsTo, s.OnFail.Target)
			}
			if s.Waivable {
				note += l.Waivable
			}
			notes = append(notes, note)
		}
		if s.Skipped {
			notes = append(notes, l.Skipped)
		} else if d.workflow.lastAgentStep(s.ID) {
			notes = append(notes, l.LastStep)
		}
		if len(notes) > 0 {
			fmt.Fprintf(&b, l.NoteList, strings.Join(notes, l.NoteSep))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n" + fmt.Sprintf(l.MaxRewinds, d.workflow.MaxRewinds) + "\n")
	if err := stepcall.AppendHandoff(d.wi.CapsuleDir, b.String()); err != nil {
		fmt.Fprintf(os.Stderr, "driver: %s: the hand-off document's opening could not be written: %v\n", d.wi.ID, err)
	}
}

// appendHandoffEvent appends one daemon event section, titled title in
// the labels l, to the hand-off document. A failure is reported, never
// fatal (the ledger and execution.json are the record).
func (d *Driver) appendHandoffEvent(l stepcall.Labels, title string, lines []string) {
	var b strings.Builder
	fmt.Fprintf(&b, "\n---\n\n## %s · %s · %s\n\n<!-- %s -->\n\n", l.Event, title, d.stamp(), l.EventNote)
	for _, line := range lines {
		b.WriteString("- " + line + "\n")
	}
	if err := stepcall.AppendHandoff(d.wi.CapsuleDir, b.String()); err != nil {
		fmt.Fprintf(os.Stderr, "driver: %s: the hand-off event %s could not be appended: %v\n", d.wi.ID, title, err)
	}
}

// handoffEvents appends the event sections a settled hand-off calls for,
// read off the record before and after its transition: a rewind (回退)
// when the lane moved back (a gate's fail, the owner's rewind, a re-run
// of the step), a close (收尾) when the work item ended (a gate without
// on_fail failed, the owner cancelled).
func (d *Driver) handoffEvents(stepID string, res *StepResult, before, after *capsule.Execution) {
	l := d.labels()
	h := res.Handoff
	trigger := fmt.Sprintf(l.TriggerText, stepID, roundOf(before, stepID), h.Status, res.HandoffCall)
	if h.PerOwner != "" {
		trigger += fmt.Sprintf(l.TriggerPerOwner, h.PerOwner)
	}
	reason := strings.TrimSpace(h.Reason)
	if reason == "" {
		reason = l.ReasonSeeBody
	}
	switch {
	case isTerminalStatus(after.WorkflowStatus) && !isTerminalStatus(before.WorkflowStatus):
		d.appendHandoffEvent(l, l.Close, []string{
			fmt.Sprintf(l.Trigger, trigger),
			fmt.Sprintf(l.EventReason, reason),
			fmt.Sprintf(l.ResultEnded, after.WorkflowStatus),
		})
	case after.CancelRequested != nil && before.CancelRequested == nil:
		d.appendHandoffEvent(l, l.Close, []string{
			fmt.Sprintf(l.Trigger, trigger),
			fmt.Sprintf(l.EventReason, reason),
			l.ResultCanceled,
		})
	case len(after.RewindStats.Rewinds) > len(before.RewindStats.Rewinds) || (h.Status == stepcall.StatusRewind && h.To == stepID):
		target := after.CurrentStep
		if h.Status == stepcall.StatusRewind && h.To == stepID {
			target = stepID
		}
		var redo []string
		ti, _ := d.workflow.indexOf(target)
		si, _ := d.workflow.indexOf(stepID)
		for i := ti; i <= si && i < len(d.workflow.Steps); i++ {
			redo = append(redo, "`"+d.workflow.Steps[i].ID+"`")
		}
		count := fmt.Sprintf(l.Rewinds, AgentRewindCount(after.RewindStats.Rewinds), d.workflow.MaxRewinds)
		if own := OwnerRewindCount(after.RewindStats.Rewinds); own > 0 {
			count += fmt.Sprintf(l.OwnerRewinds, own)
		}
		if h.PerOwner != "" || h.Status == stepcall.StatusRewind {
			count += l.PerOwnerUncounted
		}
		d.appendHandoffEvent(l, l.Rewind, []string{
			fmt.Sprintf(l.Trigger, trigger),
			fmt.Sprintf(l.EventReason, reason),
			fmt.Sprintf(l.Redo, strings.Join(redo, " → ")),
			count,
			l.HowToRead,
		})
	}
}

// writeCompletionEvent appends the close (收尾) event of a lane whose
// every step is done (the wrap follows).
func (d *Driver) writeCompletionEvent() {
	l := d.labels()
	d.appendHandoffEvent(l, l.Close, []string{l.ResultCompleted})
}

// roundOf answers step's round on ex (0 when none).
func roundOf(ex *capsule.Execution, step string) int64 {
	if ex == nil || ex.Steps[step] == nil {
		return 0
	}
	return ex.Steps[step].Round
}

// ---------------------------------------------------------------------------
// The round's start commits and the rewind budget the daemon checks.

// repoHeads answers the claim's task repos' HEADs (repo → commit; nil
// when the claim has none or none reads): git I/O, never under d.mu.
func (d *Driver) repoHeads() map[string]string {
	if len(d.claimRepos) == 0 || d.wi.CapsuleDir == "" {
		return nil
	}
	out := map[string]string{}
	for _, name := range d.claimRepos {
		sha, err := taskrepo.Git(filepath.Join(d.wi.CapsuleDir, capsule.DirRepos, name), "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
		if s := strings.TrimSpace(sha); err == nil && s != "" {
			out[name] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RewindBudgetSpent answers why the lane's rewind budget is spent ("" when
// it is not): the automatic rewinds ex records reached wf's max_rewinds.
// The daemon's `pf step handoff` refuses a gate's fail that does not cite
// the owner (--per-owner) once it is spent: the gate asks the owner first
// (workflow contract 4 §5.1), so no hand-off parks on a spent budget.
func RewindBudgetSpent(wf *Workflow, ex *capsule.Execution) string {
	if wf == nil {
		return ""
	}
	var n int64
	if ex != nil {
		n = AgentRewindCount(ex.RewindStats.Rewinds)
	}
	if n >= int64(wf.MaxRewinds) {
		return fmt.Sprintf("the lane's rewind budget is spent: %d of max_rewinds %d automatic rewinds already landed", n, wf.MaxRewinds)
	}
	return ""
}
