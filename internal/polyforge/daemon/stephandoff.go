package daemon

// stephandoff.go: `pf step handoff` (workflow contract 4, design #483
// §5.1 and §6; stage C1,). The dispatch ends with one status
// and a body; the daemon checks the call's shape on the spot — the
// refusal is the step's to fix, and nothing is written — then stamps the
// section's header from its own records and appends the section to
// artifacts/handoff.md, and the call's result line records the accepted
// hand-off: the ledger, not the document, is the daemon's record of it.
//
// What the daemon does NOT do: judge the body (its shape only: the seven
// template sections, the re-entry section, the length), or read the
// owner's words — --per-owner is checked for existence in the record (an
// owner message to this step in this round, by its stamp) and copied
// verbatim. Reacting to the status (the next step, a rewind, a cancel) is
// the driver's, from workflow contract 4 on; C1 records.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
	"github.com/silgrid/polyforge/internal/polyforge/taskrepo"
)

// handoffVerb checks, stamps and appends one hand-off.
func (sc *stepCall) handoffVerb(w http.ResponseWriter, raw json.RawMessage) {
	var a stepcall.HandoffArgs
	if !decodeArgs(w, raw, &a) {
		return
	}
	j := sc.j
	lines, err := stepcall.Read(j.capsuleDir)
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, "handoff: "+err.Error())
		return
	}
	refuse := func(format string, args ...any) {
		writeErr(w, api.ErrDaemonBadInput, "handoff refused: "+fmt.Sprintf(format, args...))
	}
	if prev := stepcall.HandoffOf(lines, j.step, int64(j.attempt)); prev != nil {
		writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("handoff refused: this dispatch (step %s, spawn %d) already handed off %s (call %d) — a dispatch hands off once: end your turn", j.step, j.attempt, prev.Handoff.Status, prev.Call))
		return
	}
	switch {
	case !stepcall.IsStatus(a.Status):
		refuse("--status %q is none of %s", a.Status, strings.Join(stepcall.Statuses, ", "))
		return
	case strings.ContainsAny(a.Reason, "\r\n"):
		refuse("--reason is one line")
		return
	case a.Status == stepcall.StatusRewind && strings.TrimSpace(a.To) == "":
		refuse("rewind needs --to <step> (the step to run again; this step's own id re-runs it)")
		return
	case a.Status != stepcall.StatusRewind && a.To != "":
		refuse("--to belongs to rewind only")
		return
	case stepcall.NeedsOwner(a.Status) && strings.TrimSpace(a.PerOwner) == "":
		refuse("%s carries out the owner's word: --per-owner <at> names the owner message it rests on (its stamp in runtime-state/asks.jsonl)", a.Status)
		return
	}
	if a.Status != stepcall.StatusBlocked && a.Status != stepcall.StatusCancel {
		why, readable := sc.laneRefusal(a, lines)
		if !readable {
			writeErr(w, api.ErrDaemonInternal, "handoff: the capsule's lane snapshot is unreadable, so "+a.Status+" cannot be checked")
			return
		}
		if why != "" {
			refuse("%s", why)
			return
		}
	}
	var st *capsule.Step
	if sc.ex != nil {
		st = sc.ex.Steps[j.step]
	}
	asks, err := capsule.ReadAsks(j.capsuleDir)
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, "handoff: "+err.Error())
		return
	}
	owner := driver.OwnerMessagesOfRound(asks, j.step, st)
	var perOwner *capsule.AskLine
	if a.PerOwner != "" {
		for i := range owner {
			if owner[i].At == a.PerOwner {
				perOwner = &owner[i]
			}
		}
		if perOwner == nil {
			refuse("--per-owner %s names no owner message to step %s in its current round (runtime-state/asks.jsonl: the reply and message lines of this step since the round opened)", a.PerOwner, j.step)
			return
		}
	}
	if err := stepcall.CheckBody(a.Body, len(stepcall.StepHandoffs(lines, j.step)) > 0, capsule.DispatchLanguage(j.capsuleDir, j.step, int64(j.attempt))); err != nil {
		refuse("%v", err)
		return
	}
	evidence, err := evidencePaths(j.capsuleDir, a.Evidence)
	if err != nil {
		refuse("%v", err)
		return
	}
	h, rec := sc.stamp(lines, st, a, evidence, owner, perOwner)
	if err := stepcall.AppendHandoff(j.capsuleDir, stepcall.RenderSection(h, a.Body)); err != nil {
		writeErr(w, api.ErrDaemonInternal, "handoff: the section could not be appended: "+err.Error())
		return
	}
	sc.handoff = rec
	writeJSON(w, http.StatusOK, map[string]any{
		"call": sc.call.Seq, "step": j.step, "round": h.Round, "spawn": j.attempt,
		"status": a.Status, "to": a.To, "path": capsule.HandoffMD,
	})
}

// laneRefusal checks a status against the capsule's lane snapshot and the
// work item's record: done needs every wrap attr the step declares
// (`wrap_attrs`, owner ruling R7) registered in its round; fail is a
// gate's, and once the lane's rewind budget is spent a fail must cite the
// owner (--per-owner: the gate asks first, design #483 §5.1); waive is a
// waivable gate's; rewind's --to a step of the lane at or before this one.
// why "" = the status stands; readable false = the lane snapshot's
// workflow.yaml cannot be read (nothing can be checked). Only the step graph
// is read: a broken definition of another step is the driver's to meet
// at its dispatch, never a reason to refuse this hand-off.
func (sc *stepCall) laneRefusal(a stepcall.HandoffArgs, lines []stepcall.Line) (why string, readable bool) {
	wf := capsuleLaneGraph(sc.j.capsuleDir)
	if wf == nil {
		return "", false
	}
	self := -1
	for i, s := range wf.Steps {
		if s.ID == sc.j.step {
			self = i
		}
	}
	if self < 0 {
		return fmt.Sprintf("step %s is no step of lane %s", sc.j.step, wf.Name), true
	}
	spec := wf.Steps[self]
	switch a.Status {
	case stepcall.StatusDone:
		registered := map[string]bool{}
		for _, op := range stepcall.PendingOps(lines, spec.ID, sc.call.Round) {
			if op.Verb == stepcall.HubWrapAttrs {
				registered[op.Name] = true
			}
		}
		var missing []string
		for _, name := range spec.WrapAttrs {
			if !registered[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return fmt.Sprintf("step %s registers the wrap attr(s) %s before it hands off done (the lane's wrap_attrs): run `pf step hub wrap-attrs <name> @<file>` for each, then hand off again", spec.ID, strings.Join(missing, ", ")), true
		}
	case stepcall.StatusFail:
		if !spec.Gate {
			return fmt.Sprintf("fail is a gate's verdict and %s is no gate: a step that cannot go on hands off blocked", spec.ID), true
		}
		if spent := driver.RewindBudgetSpent(wf, sc.ex); a.PerOwner == "" && spec.OnFail != nil && spent != "" {
			return spent + ": a fail without the owner's word can rewind no more — ask the owner first (`pf step ask`: one more round, accepting the FAIL on a waivable gate, or cancelling the work item) and hand off as the reply says, citing it with --per-owner", true
		}
	case stepcall.StatusWaive:
		if !spec.Gate || !spec.Waivable {
			return fmt.Sprintf("waive is a waivable gate's and %s is not one (the lane does not declare it waivable)", spec.ID), true
		}
	case stepcall.StatusRewind:
		for i := 0; i <= self; i++ {
			if wf.Steps[i].ID == a.To {
				return "", true
			}
		}
		var ids []string
		for i := 0; i <= self; i++ {
			ids = append(ids, wf.Steps[i].ID)
		}
		return fmt.Sprintf("--to %s names no step at or before %s in lane %s (%s)", a.To, spec.ID, wf.Name, strings.Join(ids, ", ")), true
	}
	return "", true
}

// stamp gathers the header's facts from the daemon's own records — the
// execution record (round, step start, candidate, the round's attempts,
// the HEADs its round started from), the spawn journal (the dispatch's
// start and harness when the record has none), the owner messages of the
// round, the task repos (each repo from its round start commit, to its
// HEAD now; a repo with no recorded start is "start unknown") and the
// language the dispatch was told (its spawn-journal line: the header is
// stamped in it, never the live value) — and answers it with the ledger's
// record of the hand-off.
func (sc *stepCall) stamp(lines []stepcall.Line, st *capsule.Step, a stepcall.HandoffArgs, evidence []string, owner []capsule.AskLine, perOwner *capsule.AskLine) (stepcall.Header, *stepcall.Handoff) {
	j := sc.j
	now := time.Now().UTC()
	h := stepcall.Header{
		Call: sc.call.Seq, Step: j.step, Round: stepRound(sc.ex, j.step), Spawn: int64(j.attempt),
		EndedAt: now.Format(time.RFC3339), Status: a.Status, To: a.To, Reason: a.Reason,
		OwnerMessages: owner, PerOwner: perOwner, Evidence: evidence,
		Language: capsule.DispatchLanguage(j.capsuleDir, j.step, int64(j.attempt)),
	}
	l := stepcall.LabelsOf(h.Language)
	if st != nil {
		h.StartedAt, h.Model = st.StepStartedAt, st.Candidate
	}
	if h.StartedAt == "" || h.Model == "" {
		entries, _ := capsule.ReadSpawnJournal(j.capsuleDir)
		for _, e := range entries {
			if e.StepID == j.step && int64(e.SpawnN) == int64(j.attempt) {
				if h.StartedAt == "" {
					h.StartedAt = e.At
				}
				if h.Model == "" {
					h.Model = e.Harness
				}
			}
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, h.StartedAt); err == nil {
		h.Duration = now.Sub(t).Round(time.Second).String()
	}
	for _, at := range driver.RoundAttempts(st) {
		h.Events = append(h.Events, attemptEvent(at))
	}
	reposDir := filepath.Join(j.capsuleDir, capsule.DirRepos)
	heads := map[string]string{}
	for _, name := range taskrepo.Names(reposDir) {
		out, err := taskrepo.Git(filepath.Join(reposDir, name), "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
		if sha := strings.TrimSpace(out); err == nil && sha != "" {
			heads[name] = sha
		}
	}
	// each repo's start commit is the step round's own (the driver records
	// the HEADs when the round's first dispatch starts): commits made
	// before the round — another step's, a command's between hand-offs —
	// are never this section's
	var starts map[string]string
	if st != nil && st.StartHeads != nil && st.StartHeads.Round == h.Round {
		starts = st.StartHeads.Heads
	}
	names := make([]string, 0, len(heads))
	for n := range heads {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		start, end := starts[n], heads[n]
		h.Repos = append(h.Repos, stepcall.RepoChange{Repo: n, Start: start, End: end, Stat: diffStat(filepath.Join(reposDir, n), start, end, l)})
	}
	rec := &stepcall.Handoff{Status: a.Status, To: a.To, Reason: a.Reason, Evidence: evidence, Heads: heads, Candidate: h.Model, StartedAt: h.StartedAt}
	if perOwner != nil {
		rec.PerOwner = perOwner.At
	}
	return h, rec
}

// attemptEvent renders one archived attempt of the round as an event line.
func attemptEvent(at capsule.Attempt) string {
	stamp := at.ArchivedAt
	if stamp == "" {
		stamp = at.InterruptedAt
	}
	ev := stamp + " " + at.Cause
	if at.FailureCause != "" {
		ev += "/" + at.FailureCause
	}
	if at.Candidate != "" {
		ev += " (" + at.Candidate + ")"
	}
	if d := strings.TrimSpace(strings.SplitN(at.Detail, "\n", 2)[0]); d != "" {
		if len(d) > 160 {
			d = d[:160] + "…"
		}
		ev += ": " + d
	}
	return ev
}

// diffStat answers a repo's change between two commits, as git's
// --shortstat says it (its other answers in the section's labels l).
func diffStat(dir, start, end string, l stepcall.Labels) string {
	switch {
	case start == "":
		return l.StatStartUnknown
	case start == end:
		return l.StatNoCommits
	}
	out, err := taskrepo.Git(dir, "diff", "--shortstat", start, end)
	if err != nil {
		return l.StatUnavailable
	}
	if s := strings.TrimSpace(out); s != "" {
		return s
	}
	return l.StatNoChanges
}

// evidencePaths resolves each --evidence path (absolute, or relative to
// the capsule root) to an existing regular file inside the capsule, and
// answers them capsule-relative.
func evidencePaths(capsuleDir string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	root, err := filepath.EvalSymlinks(capsuleDir)
	if err != nil {
		return nil, fmt.Errorf("the capsule root is unreadable: %v", err)
	}
	var out []string
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			return nil, errors.New("--evidence names an empty path")
		}
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(capsuleDir, abs)
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("--evidence %s: no such file in the capsule", p)
		}
		rel, err := filepath.Rel(root, real)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("--evidence %s is outside the capsule", p)
		}
		if fi, err := os.Stat(real); err != nil || !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("--evidence %s is not a file", p)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}
