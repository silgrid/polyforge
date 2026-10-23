package driver

// transition.go: the SINGLE write entry point for every execution.json
// mutation (the driver spec's R5 single-write rule; reference.md §7.3's
// tmp+fsync+rename contract, plus a parent-directory fsync so the rename
// itself survives a crash). Every state change rides Driver.transition:
//
//	1. serialize (mutex)
//	2. fence (the spawn counter must match; step_start carries the NEW
//	   derivation's ordinal, every other kind the current one)
//	3. validate + apply to the in-memory execution state
//	4. marshal + persist atomically
//	5. emit one event (news, never state)
//
// Every apply validates before it mutates, so a refused request leaves
// memory and file untouched. A failed persist leaves memory ahead of (or
// unknown against) the file: the driver then fails — every further
// transition is refused and Run returns the error — and the restart
// reloads execution.json, the one authority. Nothing is rolled back in
// memory. Event emission is the one best-effort step: once the rename
// lands the transition is committed, and a logging failure is loud on
// stderr instead of reverting state.

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// Workflow status vocabulary (reference.md §7.3, closed set).
// The strings are owned by the SCHEMA package
// (internal/common/capsule) so non-driver lanes can read them without an
// import cycle; these are the driver's write-side aliases.
const (
	StatusRunning               = capsule.WorkflowStatusRunning
	StatusCompleted             = capsule.WorkflowStatusCompleted
	StatusFailedGate            = capsule.WorkflowStatusFailedGate
	StatusFailedRewindExhausted = capsule.WorkflowStatusFailedRewindExhausted
	// StatusBlocked is the NON-terminal waiting-on-owner state (01
	// §6.4): current_step frozen, neither forward nor back; the owner
	// resumes the step, rewinds, or terminates.
	StatusBlocked = capsule.WorkflowStatusBlocked
)

// Attempt causes (01 §6.3b) of archived non-accepted attempts. A gate
// FAIL rewind archives its target as gate_fail_rewind and every step
// strictly between the target and the gate as superseded_by_rewind (it
// failed nothing; a later gate discarded its output). Both carry
// rewind_gate. Q&A history never lands in attempts[].
//
// polyforge#222: a review-gate FAIL whose blocking findings (at least
// one) are all marked text_only archives its target as
// gate_fail_rewind_text_only instead — the light rewind, which re-enters
// the target as a resume of its previous conversation rather than a
// fresh re-derivation (the order is that ledger cause itself, derived
// by prepareDispatch/applyStepStartLocked — no second carrier). The rewind
// accounting is identical (one slot, bounds before mutation); the
// candidate pointer resets on it like on every rewind archive.
//
// polyforge#260: an armed verification gate's FAIL verdict that carries a
// mechanical classification (mechanical_bound) archives its target as
// gate_fail_rewind_mechanical — the mechanical light rewind, the second
// light cause, a sibling of #222's with the same ledger semantics (one
// slot, bounds before mutation, resume re-entry, candidate-pointer
// reset) plus the pending light recheck marker it sets on the gate
// (steps.<gate>.pending_light_recheck — the durable carrier of the
// fast-lane state whose presence makes the gate's next dispatch the
// light recheck).
const (
	CauseGateFailRewind         = "gate_fail_rewind"
	CauseGateFailRewindTextOnly = "gate_fail_rewind_text_only"
	// CauseGateFailRewindMechanical is the mechanical light rewind's
	// target archive cause (polyforge#260): an armed verification gate's
	// FAIL verdict that carried a mechanical classification rewound this
	// step - the second light cause, a sibling of #222's text-only one. It
	// joins the same ledger semantics: the candidate pointer resets on it,
	// the re-entry resumes the previous conversation (lightRewindContext),
	// and the needs_code_change self-report's fresh rewind reuses its slot.
	CauseGateFailRewindMechanical = "gate_fail_rewind_mechanical"
	CauseSupersededByRewind       = "superseded_by_rewind"
	CauseInfraFailure             = "infra_failure"
)

// LightRewindNote is the light re-entry instruction a light rewind routes
// beside the FAIL verdict (the rewind input's Note — rendered by
// assembleStepPrompt as its own section): this re-entry is light, apply
// only text-only fixes, and self-report a structural fix instead of
// applying it, through the completed result's needs_code_change field
// (the build step definitions state the field's schema shape; the boundary
// itself is defined once in workflows/_shared/steps/review.md, which this
// text points at without restating in full).
const LightRewindNote = `This rewind is LIGHT: the verdict's blocking findings are all text-only
(comments and documentation in any file, test files included — the boundary
is defined once in the review step's own material). Apply ONLY text-only
fixes; your step's existing discipline is unchanged (fast checks per unit,
per-unit commits). A fix that needs a change OUTSIDE that boundary — code,
tests or assertions, user-facing string literals, workflows/** .md files,
skills/** prompt material — is NOT yours to apply: do not apply it, and
instead deliver your completed result carrying "needs_code_change": true;
the driver rewinds fresh with the same verdict, charging no additional
rewind slot.`

// MechanicalRewindNote is the mechanical light re-entry instruction a
// mechanical rewind routes beside the FAIL verdict (polyforge#260, the
// rewind input's Note - rendered by assembleStepPrompt as its own
// section): this re-entry is light and bounded to the verdict's
// mechanical_bound - modifications of bound-named files only, inside the
// hard-coded caps - and a fix that must leave the bound self-reports
// needs_code_change instead of being applied.
const MechanicalRewindNote = `This rewind is LIGHT and MECHANICAL: the verification gate's FAIL verdict
carried a mechanical classification (every failed check static, each
reproducible by one deterministic command). Apply ONLY the fix inside the
verdict's mechanical_bound: per failed check — its repo, its exact command
and the files it named — a modification of a bound-named file only; never
add, delete or rename a file; at most 40 changed lines (added + deleted)
and 5 files, summed across repos. Your step's existing discipline is
unchanged (fast checks per unit, per-unit commits). A fix that must leave
that bound, delete or rename a file, or exceed the caps is NOT yours to
apply: do not apply it, and instead deliver your completed result carrying
"needs_code_change": true; the driver rewinds fresh with the same verdict,
charging no additional rewind slot.`

// ErrStaleFence marks a transition refused by the spawn fence: the
// request names a derivation that is no longer current (B2 carried into
// the driver — only the current spawn may mutate execution.json).
var ErrStaleFence = errors.New("driver: stale spawn fence")

// TransitionKind is one execution-state mutation class.
type TransitionKind string

const (
	TransStepStart  TransitionKind = "step_start"
	TransStepResult TransitionKind = "step_result"
	TransTerminal   TransitionKind = "terminal"
	// TransInit and TransAnswer are additions the single-write rule
	// forces: someone must write the first execution.json (that write is
	// a mutation like any other), and the D9 answer-consumption write —
	// archive the pair and clear pending_question in ONE atomic write —
	// needs a kind to ride. Every other kind follows the driver spec.
	TransInit   TransitionKind = "init"
	TransAnswer TransitionKind = "answer"
	// TransInterrupted is the recovery integration's write: ONE audit-only
	// infra_failure attempt for the derivation that died without a result
	// (applyInterruptedLocked).
	TransInterrupted TransitionKind = "interrupted"
	// TransUnblock is the §6.4 owner resume verdict for a workflow parked
	// blocked on an honest incomplete result (polyforge#116): flip
	// workflow_status back to running and leave the re-dispatch to the
	// loop. It consumes NO derivation ordinal — the fresh ordinal is the
	// loop's own step_start write on the next dispatch, so the spawn
	// ledger never records a phantom derivation between the owner's
	// verdict and the re-dispatch it names.
	TransUnblock TransitionKind = "unblock"
	// TransStepFailed is the supervision write (v2-converge 2.3): ONE
	// failed dispatch and its death-cause disposal in one atomic write
	// (applyStepFailedLocked).
	TransStepFailed TransitionKind = "step_failed"
	// TransClaimReentry is a claim's re-entry of an in_progress step whose
	// dispatch never settled (cancelled by an operator pause, a shutdown
	// or a crash before this claim): it re-stamps step_started_at, so a
	// later system relaunch books only from the claim and the time before
	// it never counts toward T (applyClaimReentryLocked).
	TransClaimReentry TransitionKind = "claim_reentry"
)

// TransitionRequest is one submitted execution.json mutation. SpawnN is
// the fencing key: the ordinal of the derivation the request belongs
// to. The driver's own loop fills it from the dispatch it is settling;
// daemon-side callers (Answer/Terminate) snapshot the current
// counter — a mismatch is the honest answer "this request targets a
// superseded spawn", never retried blindly.
type TransitionRequest struct {
	Kind       TransitionKind
	StepID     string
	Result     *StepResult  // step_result
	Gate       *GateVerdict // step_result of a gate_failed outcome: the fail policy (nil = the workflow's on_fail)
	Model      string       // step_start: the pinned candidate this dispatch runs under
	Effort     string       // step_start: that candidate's thinking effort (polyforge#176)
	Status     string       // terminal: the target workflow_status
	Reason     string       // terminal/rewind reason; answer: the answer text
	QuestionID string       // answer: the question id being answered
	AnsweredBy string       // answer: who answered ("" → "owner")
	// Failure carries a step_failed write's failed-dispatch record.
	Failure *StepFailure
	// WallMs is the finished dispatch's wall time folded into the step's
	// cumulative runtime by a step_result write.
	WallMs int64
	// BookWall (interrupted): the system relaunched the dispatch after a
	// hang, stall or death — book its wall time toward the step's T.
	BookWall bool
	// TestFilesCommit is the authored-tests baseline (task repo -> HEAD)
	// a completed step_result stamps under test_files_commit
	// (polyforge#164 H4); nil = no stamp. It lands in the result's own
	// output when that declares test_files (an authoring step), and —
	// with RebaselineAuthoredTests, a human-approved change to authored
	// tests — replaces the stamp of every earlier done step that carries
	// one, so the approved baseline survives a later rewind of the
	// approving step.
	TestFilesCommit         map[string]any
	RebaselineAuthoredTests bool
	// VerifiedHeads is the task repos' HEADs a completed verification
	// result records under verified_heads (deliver_facts.go), read
	// before the transition takes the driver's lock.
	VerifiedHeads map[string]any
	// LightDiffBase (polyforge#260) is the task repos' HEADs an armed
	// verification gate-FAIL that carries a mechanical classification
	// settles on — the light recheck's repair-diff base, read before the
	// transition takes the driver's lock (the verifiedHeads pattern: git
	// I/O never under the lock, never step-supplied).
	LightDiffBase map[string]string
	SpawnN        int64
}

// GateVerdict is one business gate FAIL judgment (01 §6.2): the policy
// the workflow declared plus the verdict text the gate produced.
type GateVerdict struct {
	GateStepID string
	// TargetStep is the on_fail rewind target: a gate FAIL always
	// rewinds to it. An empty target means the gate declared no rewind
	// edge and the workflow fails on the verdict.
	TargetStep string
	// VerdictText is the FAIL judgment in full.
	VerdictText string
}

// appliedInfo records what one apply actually did, for the event body.
type appliedInfo struct {
	archived []string
	target   string
	resume   bool
	failure  *failureDecision
	extra    []appliedEvent
	// selfRewind marks the needs_code_change self-report rewind
	// (polyforge#222): the completed result was NOT accepted — the same
	// write rewound the step fresh — so the event is the standard rewind
	// (never step_done for a step that is not done); rewindGate names the
	// light gate that rewound it.
	selfRewind bool
	rewindGate string
}

// appliedEvent is a follow-up event of one transition.
type appliedEvent struct {
	kind string
	body map[string]any
}

// transition is the SINGLE entry point for all execution.json mutations.
// See the file header for the contract; the sequencing guarantees are:
//
//   - the fence check runs before anything else, so a stale request can
//     never observe — let alone mutate — current state;
//   - apply validates before it mutates the live copy; a persist
//     failure fails the driver (d.failed) instead of restoring memory;
//   - the spawn counter advances only WITH the durable step_start write
//     (crash between persist and counter bump is harmless: the dead
//     derivation's results can no longer arrive, and the re-dispatch
//     simply takes the next ordinal).
func (d *Driver) transition(req TransitionRequest) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	want := d.spawnN
	if req.Kind == TransStepStart {
		want++ // the start write carries the NEW derivation's ordinal
	}
	if req.SpawnN != want {
		return fmt.Errorf("%w: %s carries spawn %d, current derivation is %d",
			ErrStaleFence, req.Kind, req.SpawnN, d.spawnN)
	}

	if d.failed != nil {
		return d.failed
	}

	info := &appliedInfo{}
	if err := d.applyLocked(req, info); err != nil {
		return err
	}
	d.exec.Version++
	d.exec.UpdatedAt = d.stamp()

	blob, err := json.MarshalIndent(d.exec, "", "  ")
	if err == nil {
		err = atomicfile.Write(d.execPath, blob, 0o644)
	}
	if err != nil {
		d.failed = fmt.Errorf("driver: execution.json persist failed; the driver stops and the restart recovers from disk: %w", err)
		d.wakeLoopLocked()
		return d.failed
	}
	d.initialized = true
	if req.Kind == TransStepStart {
		d.spawnN = req.SpawnN
	}
	d.emitLocked(req, info)
	d.wakeLoopLocked()
	if d.cfg.Link != nil {
		// the commit boundary: the supervisor pushes the capsule on this
		// notice (it does not poll execution.json)
		d.cfg.Link.Fact(supervise.ExecCommitted{Version: d.exec.Version, Step: d.exec.CurrentStep})
	}
	return nil
}

func (d *Driver) applyLocked(req TransitionRequest, info *appliedInfo) error {
	switch req.Kind {
	case TransInit:
		return d.applyInitLocked()
	case TransStepStart:
		return d.applyStepStartLocked(req, info)
	case TransUnblock:
		return d.applyUnblockLocked()
	case TransStepResult:
		return d.applyStepResultLocked(req, info)
	case TransTerminal:
		return d.applyTerminalLocked(req)
	case TransAnswer:
		return d.applyAnswerLocked(req)
	case TransInterrupted:
		return d.applyInterruptedLocked(req)
	case TransClaimReentry:
		return d.applyClaimReentryLocked()
	case TransStepFailed:
		return d.applyStepFailedLocked(req, info)
	default:
		return fmt.Errorf("driver: unknown transition kind %q", req.Kind)
	}
}

// applyInitLocked lands the first execution.json: identity, the closed
// running status, and the step records ensured at load — all pending,
// position empty (the first dispatch moves it).
func (d *Driver) applyInitLocked() error {
	if d.initialized {
		return fmt.Errorf("driver: execution.json already initialized (version %d)", d.exec.Version)
	}
	d.exec.CapsuleID = capsuleIDFor(d.wi.ID)
	d.exec.WorkItemID = d.wi.ID
	d.exec.WorkflowStatus = StatusRunning
	d.exec.CurrentStep = ""
	return nil
}

// applyStepStartLocked flips one step to in_progress (01 §6.5 dispatch)
// and moves the execution position onto it. Two entry shapes share the
// kind: a PENDING step is a fresh entry (first dispatch or rewind re-entry
// — the pending rewind verdict is consumed by this very write,
// D8, and the budget window re-opens after the rewind archived it); an
// IN_PROGRESS step is a resume re-dispatch (answer continuation, crash
// recovery, recovery-lane re-derivation — a new measurement window, the
// prior partial output kept). From StatusBlocked only the current step
// may resume: that is the owner's §6.4 unblock action, which also
// rewrites the status back to running.
//
// A pending entry whose ledger's latest non-infra_failure archived
// attempt is the light rewind (polyforge#222) is the light re-entry — a
// resume of the previous dispatch's conversation: the started event's
// resume flag is set for this shape too (the event does not lie about
// the dispatch). The resume order IS that ledger fact — recorded by the
// same gate-FAIL write that archives the range, read here and by
// prepareDispatch — so this write consumes no separate order; after the
// start the step is in_progress and resumes by default.
func (d *Driver) applyStepStartLocked(req TransitionRequest, info *appliedInfo) error {
	if _, ok := d.workflow.indexOf(req.StepID); !ok {
		return fmt.Errorf("driver: step %q is not in the workflow", req.StepID)
	}
	if isTerminalStatus(d.exec.WorkflowStatus) {
		return fmt.Errorf("driver: workflow is terminal (%s); step starts are frozen", d.exec.WorkflowStatus)
	}
	if d.exec.WorkflowStatus == StatusBlocked && req.StepID != d.exec.CurrentStep {
		return fmt.Errorf("driver: workflow is blocked; only the current step %q may resume (rewind or terminate otherwise)", d.exec.CurrentStep)
	}
	st := d.exec.Steps[req.StepID]
	if st == nil {
		st = &capsule.Step{}
		d.exec.Steps[req.StepID] = st
	}
	switch {
	case st.Status == capsule.StepPending || st.Status == "":
		st.PendingRewindInput = nil          // consumed by this dispatch (D8)
		st.Output = nil                      // stale products never feed a re-run (01 §6.2 step 4)
		st.BudgetConsumed = capsule.Budget{} // the rewind archived the prior window
		st.Status = capsule.StepInProgress
		if lightRewindContext(st.Attempts) {
			info.resume = true // the light re-entry: a resume of the previous conversation (the ledger cause is the order)
		}
	case st.Status == capsule.StepInProgress:
		info.resume = true
	default:
		return fmt.Errorf("driver: step %q is %s; only pending or in_progress steps start", req.StepID, st.Status)
	}
	if st.Supervision != nil {
		st.Supervision.NextDispatch = "" // this dispatch consumed the shape the last failure ordered
	}
	st.Model = req.Model
	st.Effort = req.Effort
	st.Dispatch = req.SpawnN
	st.StepStartedAt = d.stamp() // refreshes on every re-entry (reference.md §7.3)
	d.exec.WorkflowStatus = StatusRunning
	d.exec.CurrentStep = req.StepID
	return nil
}

// applyUnblockLocked lands the §6.4 owner resume verdict (polyforge#116):
// only a blocked workflow accepts it (reference.md §7.3: the owner's follow-up
// writes running back), the position stays frozen exactly as the park
// left it (the current step remains in_progress — the loop's own
// step_start write on the next dispatch re-dispatches it as the fresh
// derivation), and no derivation ordinal is consumed here: a spawn
// ledger entry must name a derivation that actually ran.
func (d *Driver) applyUnblockLocked() error {
	if d.exec.WorkflowStatus != StatusBlocked {
		return fmt.Errorf("driver: resume is the owner's blocked-step verdict; workflow is %q", d.exec.WorkflowStatus)
	}
	if d.exec.CurrentStep == "" {
		return fmt.Errorf("driver: blocked workflow without a current step")
	}
	d.exec.WorkflowStatus = StatusRunning
	d.exec.BlockedReason = ""
	// the human's "continue" resets the step's failure ledger (the
	// relaxed thresholds stay: they are the step's learned bounds)
	if st := d.exec.Steps[d.exec.CurrentStep]; st != nil && st.Supervision != nil {
		st.Supervision.FailureCount = 0
		st.Supervision.LongTaskTimeouts = 0
		st.Supervision.NextDispatch = ""
	}
	return nil
}

// applyStepResultLocked accepts one R13 result envelope for the current
// step and applies its outcome (01 §6.5 steps 2-5):
//
//   - completed: the step is done, its output (plus the B10 commands
//     evidence) recorded — the only accept path; envelope/artifact
//     validation happened in the Run loop before this write;
//   - gate_failed: the FAIL verdict is recorded on the gate step (never
//     done) and the gate's fail policy — rewind or terminal — lands in
//     the same write, so no crash can leave a verdict nobody consumes;
//   - needs_input: the open question is persisted in the same write
//     that parks the step (D8/D9: no "asked but unrecorded" window);
//     the workflow stays running — pending_question IS the waiting
//     marker, and the observe face derives needs_input from it;
//   - incomplete: workflow_status=blocked, position frozen (01 §6.4);
//     the owner holds every next move.
func (d *Driver) applyStepResultLocked(req TransitionRequest, info *appliedInfo) error {
	res := req.Result
	if res == nil {
		return fmt.Errorf("driver: step_result without a result envelope")
	}
	if req.StepID == "" {
		req.StepID = res.StepID
	}
	if req.StepID == "" {
		return fmt.Errorf("driver: step_result without a step id")
	}
	if res.StepID != "" && res.StepID != req.StepID {
		return fmt.Errorf("driver: result envelope names step %q, request says %q", res.StepID, req.StepID)
	}
	if res.SpawnN != req.SpawnN {
		return fmt.Errorf("%w: result envelope spawn %d, request spawn %d", ErrStaleFence, res.SpawnN, req.SpawnN)
	}
	if res.WIID != "" && res.WIID != d.wi.ID {
		return fmt.Errorf("driver: result envelope names work item %q, this driver drives %q", res.WIID, d.wi.ID)
	}
	switch res.Outcome {
	case OutcomeCompleted, OutcomeGateFailed, OutcomeNeedsInput, OutcomeIncomplete:
	default:
		return fmt.Errorf("driver: unknown outcome %q", res.Outcome)
	}
	if isTerminalStatus(d.exec.WorkflowStatus) {
		return fmt.Errorf("driver: workflow is terminal (%s); results are frozen", d.exec.WorkflowStatus)
	}
	if d.exec.WorkflowStatus == StatusBlocked {
		return fmt.Errorf("driver: workflow is blocked; a result now is stale (resume, rewind or terminate first)")
	}
	if req.StepID != d.exec.CurrentStep {
		return fmt.Errorf("driver: result for step %q but the current step is %q", req.StepID, d.exec.CurrentStep)
	}
	st := d.exec.Steps[req.StepID]
	if st == nil {
		return fmt.Errorf("driver: result for unknown step %q", req.StepID)
	}
	if st.Status != capsule.StepInProgress {
		return fmt.Errorf("driver: step %q is %s; only an in_progress step accepts results", req.StepID, st.Status)
	}
	// Everything that can refuse the result is judged before the first
	// mutation.
	var out map[string]any
	var qid, qtext string
	var verdict *GateVerdict
	var err error
	if res.Outcome == OutcomeNeedsInput {
		// Shape was validated at staging (ValidateStepResult); the same
		// helper re-derives the pair here and stays a backstop.
		qid, qtext, err = needsInputQuestion(res)
	} else {
		out, err = parseOutput(res.Output)
	}
	if err != nil {
		return err
	}
	if res.Outcome == OutcomeGateFailed {
		verdict = req.Gate
		if verdict == nil {
			verdict = d.verdictFor(req.StepID, res)
		}
		if err := d.checkRewindTarget(req.StepID, verdict.TargetStep); err != nil {
			return err
		}
	}

	st.Dispatch = 0
	if req.WallMs > 0 {
		// human-wait time is excluded by construction: no process runs
		// while a question is open
		d.addWallLocked(st, req.WallMs)
	}

	switch res.Outcome {
	case OutcomeCompleted:
		// The needs_code_change self-report rewind (polyforge#222): a
		// completed result of a LIGHT re-entry carrying
		// needs_code_change: true is not archived done — the same write
		// rewinds fresh with the same FAIL verdict, charging no new slot.
		// Judged before any of the accepted-result handling below (the
		// result is not accepted: no evidence decode, no stamps, no done).
		if lightRewindContext(st.Attempts) {
			if _, value, malformed := needsCodeChangeOf(out); !malformed && value {
				d.applySelfReportRewindLocked(req.StepID, st, out, info)
				return nil
			}
		}
		// B10: the only accepted command evidence, decoded (known fields
		// only; ValidateStepResult already proved it decodes)
		if ev, _ := decodeCommandEvidence(res.CommandEvidence); len(ev) > 0 {
			out["commands"] = ev
		}
		// H4: the authored-tests baseline is driver-written only — a
		// step-supplied value is never kept (it would forge the bar).
		delete(out, testFilesCommitKey)
		if len(req.TestFilesCommit) > 0 && len(declaredTestFiles(res.Output)) > 0 {
			out[testFilesCommitKey] = req.TestFilesCommit
		}
		if len(req.TestFilesCommit) > 0 && req.RebaselineAuthoredTests {
			for _, s := range d.workflow.Steps {
				if s.ID == req.StepID {
					break
				}
				if prev := d.exec.Steps[s.ID]; prev != nil && prev.Status == capsule.StepDone && prev.Output[testFilesCommitKey] != nil {
					prev.Output[testFilesCommitKey] = req.TestFilesCommit
				}
			}
		}
		if req.StepID == verificationStepID {
			// the commit verification passed on, per task repo — the
			// deliver merge rule's anchor (deliver_facts.go); driver-
			// written only, a step-supplied value is never kept
			delete(out, verifiedHeadsKey)
			if len(req.VerifiedHeads) > 0 {
				out[verifiedHeadsKey] = req.VerifiedHeads
			}
		}
		st.Status = capsule.StepDone
		st.Output = out
		// Commit point: archive the accepted attempt (the step's attempt
		// ledger; the candidate pointer recomputes over it), with the
		// injected clock and the max-seq rule every other ledger entry
		// uses.
		st.Attempts = append(st.Attempts, capsule.Attempt{
			Cause:      "accepted",
			AttemptSeq: nextAttemptSeq(st),
			StartedAt:  st.StepStartedAt,
			ArchivedAt: d.stamp(),
		})
	case OutcomeGateFailed:
		st.Output = out // the FAIL verdict stays on the gate; it is never done
		if d.exec.ReviewRounds == nil {
			d.exec.ReviewRounds = map[string]int64{}
		}
		d.exec.ReviewRounds[req.StepID]++
		d.applyGateFailLocked(req.StepID, verdict, out, req.LightDiffBase, info)
	case OutcomeNeedsInput:
		st.PendingQuestion = &capsule.PendingQuestion{
			ID:       qid,
			Step:     req.StepID,
			Question: qtext,
			Context:  res.Output,
			AskedAt:  d.stamp(),
		}
	case OutcomeIncomplete:
		d.exec.WorkflowStatus = StatusBlocked
		st.Output = out
	}
	return nil
}

// applyGateFailLocked consumes one gate FAIL policy (01 §6.2): a gate
// without on_fail ends the workflow failed_gate (WF-102); a gate with
// on_fail: rewind first checks BOTH bounds — a hit ends
// failed_rewind_exhausted (WF-101) before any mutation — then archives
// every step in
// [target, gate) (the target as gate_fail_rewind, the steps after it as
// superseded_by_rewind), resets each to pending
// with output cleared, moves the position back to target and persists
// the FAIL verdict (the full verdict text, unconditionally) as the
// target's pending_rewind_input. The gate itself is neither archived
// nor reset: it stays in_progress holding its verdict output (only its
// AnswerBase advances, so a human gate needs a fresh answer on re-entry),
// and its candidate pointer is untouched (INV-1g — rewind never switches
// candidates).
//
// The LIGHT variant (polyforge#222): the same single write over a
// REVIEW-gate FAIL whose recorded blocking findings (at least one) all
// carry text_only archives the target with the
// gate_fail_rewind_text_only cause and routes the verdict with the
// light re-entry instruction as the rewind input's Note — the
// re-dispatch then resumes the target's previous conversation instead
// of re-deriving fresh, an order derived from the ledger that same write
// already recorded (the light cause on the pending target), not a
// second carrier. Everything else (bounds, slot accounting, the
// superseded range, the gate-side fresh-re-entry windows) is exactly the
// fresh rewind's: the light path saves the re-derivation, never a
// rewind slot.
//
// It runs inside the gate's step_result write, after that write checked
// the gate is the running current step and the target (checkRewindTarget).
func (d *Driver) applyGateFailLocked(gate string, v *GateVerdict, out map[string]any, diffBase map[string]string, info *appliedInfo) {
	target := v.TargetStep
	if target == "" { // no rewind edge declared: the gate verdict is terminal
		d.exec.WorkflowStatus = StatusFailedGate
		return
	}
	tIdx, _ := d.workflow.indexOf(target)
	gIdx, _ := d.workflow.indexOf(gate)
	// Bounds BEFORE any mutation (01 §6.2 step 2: a hit must not rewind).
	if d.exec.RewindStats.Count >= int64(d.workflow.MaxTotal) ||
		countToStep(d.exec.RewindStats.ToStep, target) >= int64(d.workflow.MaxPerStep) {
		d.exec.WorkflowStatus = StatusFailedRewindExhausted
		return
	}
	// The light paths key on the gate id alone and the verdict's own
	// recorded markers — no re-classification, no owner round-trip. The
	// text-only light rewind (#222) keys on the review gate; the mechanical
	// light rewind (polyforge#260) keys on the verification gate of an
	// ARMED lane (the workflow's mechanical_fix switch) whose FAIL verdict
	// carries a mechanical classification (mechanical_bound present — the
	// agent's own classification, never the driver's). Any other gate, or
	// a verdict that is neither all-text-only nor classified mechanical,
	// takes the fresh rewind exactly as before (fail closed on any
	// malformed finding).
	light := gate == reviewStepID && lightRewindEligible(out)
	mech := gate == verificationStepID && d.mechanicalArmed() && mechanicalBoundPresent(out)
	targetCause := CauseGateFailRewind
	switch {
	case light:
		targetCause = CauseGateFailRewindTextOnly
	case mech:
		targetCause = CauseGateFailRewindMechanical
	}
	d.applyRewindRangeLocked(tIdx, gIdx, target, gate, targetCause, info)
	// The gate itself stays in_progress, but its verdict closes the human
	// round it judged: answers recorded so far must not satisfy a
	// requires_human_answer gate on re-entry (human-gate bypass).
	if gst := d.exec.Steps[gate]; gst != nil {
		gst.AnswerBase = len(gst.Answers)
		// Its conversation judged the version now being redone: the
		// re-entry must start a NEW conversation (prior transcript as
		// read-only reference at most), never resume that judgment.
		if gst.Supervision == nil {
			gst.Supervision = &capsule.StepSupervision{}
		}
		gst.Supervision.NextDispatch = capsule.NextDispatchFresh
		// D6: a rewind resets T — the gate's re-entry judges the redone
		// work in a fresh window, not on the runtime of the verdict it
		// already delivered.
		gst.BudgetConsumed = capsule.Budget{}
	}
	// The FAIL verdict text rides to the target's pending_rewind_input
	// (D8) so the rewound step's next dispatch is guaranteed to see it.
	pri := &capsule.RewindInput{
		ToStep: target,
		Reason: v.VerdictText,
	}
	switch {
	case light:
		// The light re-entry instruction rides beside the verdict (the
		// prompt renders it as its own section). The re-dispatch's resume
		// order is NOT a second write: the same single write above already
		// archived the light cause on the target, and the re-dispatch
		// (prepareDispatch / applyStepStartLocked) derives the resume from
		// that ledger fact — the same derivation the needs_code_change
		// self-report validation reads (one fact, one place).
		pri.Note = LightRewindNote
	case mech:
		// The mechanical light re-entry instruction (polyforge#260): the
		// same rewind-note pattern — the resumed session sees the bound and
		// the caps and the needs_code_change self-report channel without
		// re-deriving them from the verdict.
		pri.Note = MechanicalRewindNote
	}
	d.exec.Steps[target].PendingRewindInput = pri
	if mech {
		// The pending light recheck marker (polyforge#260): the durable
		// carrier of the fast-lane state after this FAIL verdict's output is
		// replaced — its presence makes the verification step's next
		// dispatch the light recheck. The bound is copied verbatim from the
		// FAIL verdict's own classification (never re-typed by any step
		// agent); the diff base is the request-carried FAIL-settlement HEADs
		// (driver-recorded, never step-supplied). The same single write
		// (this one) sets it with the rewind — no half state.
		if gst := d.exec.Steps[gate]; gst != nil {
			if raw, err := json.Marshal(out[mechanicalBoundKey]); err == nil {
				gst.PendingLightRecheck = &capsule.LightRecheck{
					Bound:    json.RawMessage(raw),
					DiffBase: diffBase,
				}
			}
		}
	}
}

// lightRewindEligible answers whether one recorded gate output is an
// all-text-only review FAIL: at least one finding with severity
// "blocking" is recorded and every one of them carries text_only ==
// true. Non-blocking findings never enter the rule (marked or not); a
// malformed findings record fails closed — not a list, a non-object
// item, or a non-boolean text_only all answer false (an unparseable
// finding set is not vacuously text-only; the fresh default holds).
func lightRewindEligible(out map[string]any) bool {
	if out == nil {
		return false
	}
	items, ok := out["findings"].([]any)
	if !ok {
		return false
	}
	blocking := 0
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			return false
		}
		if sev, _ := m["severity"].(string); sev != "blocking" {
			continue // non-blocking findings never enter the rule
		}
		only, ok := m["text_only"].(bool) // absent or non-boolean: structural
		if !ok || !only {
			return false
		}
		blocking++
	}
	return blocking > 0
}

// lightRewindContext answers whether a step's latest non-infra_failure
// archived attempt is a light rewind (gate_fail_rewind_text_only or
// gate_fail_rewind_mechanical): the completed result's needs_code_change
// self-report is legal only in that context — the light rewind instructed
// this very re-entry, so the self-report's fresh rewind reuses the light
// rewind's slot (a classification error is not charged twice). An accepted
// attempt or a fresh rewind archive answers false (a crash attempt
// interleaved after the light rewind does not change the re-entry's nature).
func lightRewindContext(attempts []capsule.Attempt) bool {
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i].Cause == CauseInfraFailure {
			continue // audit-only interruptions do not change the re-entry
		}
		switch attempts[i].Cause {
		case CauseGateFailRewindTextOnly, CauseGateFailRewindMechanical:
			return true
		}
		return false
	}
	return false
}

// mechanicalBoundKey is the output key of the verification FAIL verdict's
// mechanical classification (polyforge#260): an array, one entry per
// failed check — {check, repo, command, files} — declared as an optional
// property of the shared verification schema. Its presence on an ARMED
// lane's FAIL verdict IS the classification; an unarmed lane never writes
// it (and the driver ignores it there).
const mechanicalBoundKey = "mechanical_bound"

// mechanicalBoundPresent answers whether one recorded gate output carries
// a mechanical classification: the mechanical_bound key present with a
// non-nil value. Shape (an array of well-formed entries covering the
// failed checks exactly) is NOT judged here — the light recheck's
// preparation validates coverage and escalates on a malformed one
// (lightrecheck.go); this check only routes the rewind.
func mechanicalBoundPresent(out map[string]any) bool {
	if out == nil {
		return false
	}
	v, ok := out[mechanicalBoundKey]
	return ok && v != nil
}

// mechanicalArmed answers whether this workflow's verification step is
// armed for the mechanical fast lane (mechanical_fix: true on its
// workflow.md step entry; absent = off). Every fast-lane branch in the
// driver is gated on it — an unarmed lane's FAIL rewinds exactly as today,
// whatever its verdicts carry.
func (d *Driver) mechanicalArmed() bool {
	spec := d.workflow.step(verificationStepID)
	return spec != nil && spec.MechanicalFix
}

// needsCodeChangeOf answers one parsed completed output's needs_code_change
// self-report: present, and its boolean value. A present non-boolean value
// is malformed (the caller rejects the envelope — the field is the light
// re-entry's structural self-report, not free text); absent or null is
// simply not present.
func needsCodeChangeOf(out map[string]any) (present, value, malformed bool) {
	if out == nil {
		return false, false, false
	}
	v, ok := out["needs_code_change"]
	if !ok || v == nil {
		return false, false, false
	}
	b, isBool := v.(bool)
	if !isBool {
		return true, false, true
	}
	return true, b, false
}

// applySelfReportRewindLocked lands the needs_code_change self-report
// rewind (polyforge#222): a completed result of a light re-entry carrying
// needs_code_change: true is NOT archived done — the same write archives
// the resumed attempt (gate_fail_rewind, rewind_gate = the light gate that
// rewound this step, the delivered result and consumed budget archived
// as the range rewind does), resets the step to pending with output
// cleared, and routes the SAME FAIL verdict — re-marshaled from the gate's
// recorded output, the durable verdict the gate step holds — as the fresh
// re-entry's rewind input (Note empty: a fresh re-derivation). No stats
// mutation, no bounds check (the light rewind's slot already paid; a
// classification error is not charged twice), position unchanged (the
// step is the current one). The caller emits the standard rewind event —
// never step_done for a step that is not done.
func (d *Driver) applySelfReportRewindLocked(stepID string, st *capsule.Step, out map[string]any, info *appliedInfo) {
	gate := ""
	for i := len(st.Attempts) - 1; i >= 0; i-- {
		// the light gate that rewound this step — either light cause (#222's
		// text-only review FAIL, or #260's mechanical verification FAIL)
		if st.Attempts[i].Cause == CauseGateFailRewindTextOnly ||
			st.Attempts[i].Cause == CauseGateFailRewindMechanical {
			gate = st.Attempts[i].RewindGate
			break
		}
	}
	raw, _ := json.Marshal(out)
	consumed := st.BudgetConsumed
	st.Attempts = append(st.Attempts, capsule.Attempt{
		Cause:          CauseGateFailRewind,
		AttemptSeq:     nextAttemptSeq(st),
		StartedAt:      st.StepStartedAt,
		ArchivedAt:     d.stamp(),
		RewindGate:     gate,
		Result:         raw,
		ConsumedBudget: &consumed,
	})
	st.Status = capsule.StepPending
	st.Output = nil
	st.PendingQuestion = nil
	st.PendingRewindInput = nil
	st.Supervision = nil
	st.AnswerBase = len(st.Answers)
	// The same FAIL verdict rides as the fresh re-entry's rewind input,
	// re-derived from the gate's recorded output (the durable verdict the
	// gate step holds; the light Note is NOT carried — the fresh re-entry
	// re-derives from scratch).
	reason := ""
	if gst := d.exec.Steps[gate]; gst != nil && gst.Output != nil {
		if blob, err := json.Marshal(gst.Output); err == nil {
			reason = string(blob)
		}
	}
	st.PendingRewindInput = &capsule.RewindInput{ToStep: stepID, Reason: reason}
	info.selfRewind = true
	info.rewindGate = gate
	info.target = stepID
	info.archived = append(info.archived, stepID)
}

// checkRewindTarget refuses a gate FAIL policy whose rewind target is not
// an earlier workflow step ("" = no rewind edge, always valid).
func (d *Driver) checkRewindTarget(gate, target string) error {
	if target == "" {
		return nil
	}
	tIdx, ok := d.workflow.indexOf(target)
	if !ok {
		return fmt.Errorf("driver: rewind target %q is not in the workflow", target)
	}
	if gIdx, _ := d.workflow.indexOf(gate); tIdx >= gIdx {
		return fmt.Errorf("driver: rewind target %q must be an earlier step than the gate %q", target, gate)
	}
	return nil
}

// applyRewindRangeLocked archives every step in [from, to) and resets it
// to pending with output cleared, then moves the position to the target.
// The target (index from) is archived with the targetCause the caller
// chose (the fresh gate_fail_rewind, or gate_fail_rewind_text_only on the
// light path); every later step in the range as superseded_by_rewind.
// rewindGate names the triggering gate.
func (d *Driver) applyRewindRangeLocked(from, to int, target, rewindGate, targetCause string, info *appliedInfo) {
	for i := from; i < to; i++ {
		sid := d.workflow.Steps[i].ID
		st := d.exec.Steps[sid]
		if st == nil {
			d.exec.Steps[sid] = &capsule.Step{Status: capsule.StepPending}
			continue
		}
		raw, _ := json.Marshal(st.Output)
		consumed := st.BudgetConsumed
		cause := CauseSupersededByRewind
		if i == from {
			cause = targetCause
		}
		st.Attempts = append(st.Attempts, capsule.Attempt{
			Cause:          cause,
			AttemptSeq:     nextAttemptSeq(st),
			StartedAt:      st.StepStartedAt,
			ArchivedAt:     d.stamp(),
			RewindGate:     rewindGate,
			Result:         raw,
			ConsumedBudget: &consumed,
		})
		st.Status = capsule.StepPending
		st.Output = nil
		st.PendingQuestion = nil
		st.PendingRewindInput = nil
		st.PendingLightRecheck = nil    // a reset step never carries a stale light recheck marker (polyforge#260)
		st.Supervision = nil            // a gate rewind into the step resets its failure ledger and relaxed bounds
		st.AnswerBase = len(st.Answers) // a rewound step starts a new attempt chain: earlier answers no longer count toward the human gate
		info.archived = append(info.archived, sid)
	}
	d.exec.RewindStats.Count++
	d.exec.RewindStats.ToStep = append(d.exec.RewindStats.ToStep, target)
	d.exec.CurrentStep = target
	info.target = target
}

// applyTerminalLocked lands a terminal workflow_status. completed is
// reserved for the all-done path (01 §6.5 step 6: the loop must prove
// every step done before writing it — an empty next_step_by_order alone
// is NOT completion); the failure statuses carry the owner's or the
// gate policy's verdict.
func (d *Driver) applyTerminalLocked(req TransitionRequest) error {
	switch req.Status {
	case StatusCompleted, StatusFailedGate, StatusFailedRewindExhausted:
	default:
		return fmt.Errorf("driver: %q is not a terminal workflow_status", req.Status)
	}
	if isTerminalStatus(d.exec.WorkflowStatus) {
		return fmt.Errorf("driver: workflow is already terminal (%s)", d.exec.WorkflowStatus)
	}
	if req.Status == StatusCompleted {
		for _, spec := range d.workflow.Steps {
			st := d.exec.Steps[spec.ID]
			if st == nil || st.Status != capsule.StepDone {
				return fmt.Errorf("driver: completed requires every step done; %q is %s", spec.ID, stepStatusOf(st))
			}
		}
	}
	d.exec.WorkflowStatus = req.Status
	return nil
}

// addWallLocked adds wall time to the step's cumulative runtime (the T
// accumulator, the step-level authority) and the workflow aggregate.
func (d *Driver) addWallLocked(st *capsule.Step, ms int64) {
	st.BudgetConsumed.WallMs += ms
	d.exec.BudgetConsumed.WallMs += ms
}

// applyAnswerLocked is the D9 answer-consumption write: archive the
// pair and clear pending_question in ONE atomic write. The binding is
// by question id against the CURRENT step's open question — a mismatch
// is refused (the DAEMON-409 semantics the observe answer face
// enforces on its side of the relay).
func (d *Driver) applyAnswerLocked(req TransitionRequest) error {
	if isTerminalStatus(d.exec.WorkflowStatus) {
		return fmt.Errorf("driver: workflow is terminal (%s); answers are moot", d.exec.WorkflowStatus)
	}
	if d.exec.WorkflowStatus == StatusBlocked {
		return fmt.Errorf("driver: workflow is blocked; there is no open question to answer")
	}
	cur := d.exec.CurrentStep
	if cur == "" {
		return fmt.Errorf("driver: answer with no current step")
	}
	if req.StepID != "" && req.StepID != cur {
		return fmt.Errorf("driver: answer for step %q but the current step is %q", req.StepID, cur)
	}
	st := d.exec.Steps[cur]
	if st == nil || st.PendingQuestion == nil {
		return fmt.Errorf("driver: no open question on step %q", cur)
	}
	q := st.PendingQuestion
	if q.ID != req.QuestionID {
		return fmt.Errorf("driver: answer for question %q but the open question is %q", req.QuestionID, q.ID)
	}
	by := req.AnsweredBy
	if by == "" {
		by = "owner"
	}
	st.Answers = append(st.Answers, capsule.ArchivedAnswer{
		QuestionID: q.ID,
		Answer:     req.Reason,
		AnsweredBy: by,
		AnsweredAt: d.stamp(),
	})
	st.PendingQuestion = nil
	return nil
}

// applyInterruptedLocked archives the interrupted derivation as one
// infra_failure attempt on the current in_progress step (the recovery
// lane's re-derivation input; the recomputation advances on
// provider-class entries alone). A dispatch that ended legitimately
// archives nothing: a parked question means the dispatch DELIVERED, and
// a step that is not in flight means the recovery cause predates the
// step's own resolution. The no-archive shapes are silent, not errors —
// the transition still lands (one version bump, no state change).
func (d *Driver) applyInterruptedLocked(req TransitionRequest) error {
	if d.exec.WorkflowStatus != StatusRunning {
		return nil
	}
	cur := d.exec.CurrentStep
	if cur == "" {
		return nil
	}
	st := d.exec.Steps[cur]
	if st == nil || st.Status != capsule.StepInProgress || st.PendingQuestion != nil {
		return nil
	}
	// The interruption is audit only: it is NOT a counted failure (the
	// per-step failure counter is untouched — a daemon restart or driver
	// relaunch must not push a step towards blocked) and its cause is not
	// model-class, so it never advances the candidate pointer.
	cause := FailDriverRelaunch
	if req.Reason == FailDaemonRestart {
		cause = FailDaemonRestart
	}
	now := d.stamp()
	if req.BookWall {
		// A system relaunch books the interrupted dispatch's runtime so a
		// hang→relaunch loop reaches T. Only an unsettled dispatch (its
		// result or failure write books it otherwise), and only the time
		// since its start or since the last interruption archived for it
		// — a second relaunch before the next dispatch never re-books.
		if ms := interruptedWallMs(st, now); ms > 0 && st.Dispatch != 0 {
			d.addWallLocked(st, ms)
		}
	}
	st.Attempts = append(st.Attempts, capsule.Attempt{
		Cause:         CauseInfraFailure,
		FailureCause:  cause,
		AttemptSeq:    nextAttemptSeq(st),
		StartedAt:     st.StepStartedAt,
		InterruptedAt: now,
		Detail:        req.Reason,
	})
	return nil
}

// applyClaimReentryLocked re-stamps the current step's start when a
// claim finds its dispatch unsettled; any other state is left as is.
func (d *Driver) applyClaimReentryLocked() error {
	if st := d.unsettledStepLocked(); st != nil {
		st.StepStartedAt = d.stamp()
	}
	return nil
}

// unsettledStepLocked answers the current step when the workflow runs,
// the step is in_progress with no open question and its dispatch never
// settled (Step.Dispatch set); nil otherwise.
func (d *Driver) unsettledStepLocked() *capsule.Step {
	if d.exec.WorkflowStatus != StatusRunning {
		return nil
	}
	st := d.exec.Steps[d.exec.CurrentStep]
	if st == nil || st.Status != capsule.StepInProgress || st.PendingQuestion != nil || st.Dispatch == 0 {
		return nil
	}
	return st
}

// interruptedWallMs answers the wall time from the current dispatch's
// start, or from a later interruption already archived for that same
// dispatch, to now; 0 when the start stamp does not parse.
func interruptedWallMs(st *capsule.Step, now string) int64 {
	from, err := time.Parse(time.RFC3339Nano, st.StepStartedAt)
	if err != nil {
		return 0
	}
	for _, a := range st.Attempts {
		if a.Cause != CauseInfraFailure || a.StartedAt != st.StepStartedAt ||
			(a.FailureCause != FailDriverRelaunch && a.FailureCause != FailDaemonRestart) {
			continue
		}
		if at, err := time.Parse(time.RFC3339Nano, a.InterruptedAt); err == nil && at.After(from) {
			from = at
		}
	}
	to, err := time.Parse(time.RFC3339Nano, now)
	if err != nil {
		return 0
	}
	return to.Sub(from).Milliseconds()
}

// requireRunningStep is the step_failed precondition: a known,
// current, in_progress step under a running (not terminal, not blocked)
// workflow.
func (d *Driver) requireRunningStep(stepID string) error {
	if _, ok := d.workflow.indexOf(stepID); !ok {
		return fmt.Errorf("driver: step %q is not in the workflow", stepID)
	}
	if isTerminalStatus(d.exec.WorkflowStatus) {
		return fmt.Errorf("driver: workflow is terminal (%s)", d.exec.WorkflowStatus)
	}
	if d.exec.WorkflowStatus == StatusBlocked {
		return fmt.Errorf("driver: workflow is blocked; a dispatch verdict needs a running workflow")
	}
	if stepID != d.exec.CurrentStep {
		return fmt.Errorf("driver: step %q is not the current step (%q)", stepID, d.exec.CurrentStep)
	}
	st := d.exec.Steps[stepID]
	if st == nil || st.Status != capsule.StepInProgress {
		return fmt.Errorf("driver: step %q is not in_progress", stepID)
	}
	return nil
}

// emitLocked emits one progress event for a committed transition. The
// bodies carry the fence identity (spawn_n) so events line up with the
// spawn that produced them. Names follow the observe lane's closed
// vocabulary where one exists (needs_input, answered).
func (d *Driver) emitLocked(req TransitionRequest, info *appliedInfo) {
	if d.log == nil {
		return
	}
	var kind string
	body := map[string]any{"spawn_n": d.spawnN}
	switch req.Kind {
	case TransInit:
		kind = "exec_init"
		body["steps"] = len(d.workflow.Steps)
	case TransUnblock:
		kind = "resumed"
		body["step"] = d.exec.CurrentStep
		body["resume"] = true
	case TransStepStart:
		kind = "step_started"
		body["step"] = req.StepID
		body["model"] = req.Model
		if req.Effort != "" {
			body["effort"] = req.Effort
		}
		body["resume"] = info.resume
	case TransStepResult:
		if req.Result == nil {
			return
		}
		body["step"] = req.StepID
		switch req.Result.Outcome {
		case OutcomeCompleted:
			if info.selfRewind {
				// the needs_code_change self-report rewind: the standard rewind
				// event (same kind, same shape — the ledger cause is the
				// distinguishing record), never step_done for a step that is
				// not done (polyforge#222)
				kind = "rewind"
				body["gate"] = info.rewindGate
				body["target"] = info.target
				body["archived"] = info.archived
			} else {
				kind = "step_done"
			}
		case OutcomeGateFailed:
			kind = "gate_verdict"
			if isTerminalStatus(d.exec.WorkflowStatus) {
				info.extra = append(info.extra, appliedEvent{"terminal", map[string]any{
					"spawn_n": d.spawnN, "status": d.exec.WorkflowStatus, "gate": req.StepID,
				}})
			} else {
				info.extra = append(info.extra, appliedEvent{"rewind", map[string]any{
					"spawn_n": d.spawnN, "gate": req.StepID, "target": info.target, "archived": info.archived,
				}})
			}
		case OutcomeNeedsInput:
			kind = "needs_input"
			q := d.exec.Steps[req.StepID].PendingQuestion
			d.inputEventFor = q.ID
			body["question_id"], body["question"] = q.ID, q.Question
		case OutcomeIncomplete:
			kind = "incomplete"
		}
	case TransTerminal:
		kind = "terminal"
		body["status"] = req.Status
		body["reason"] = req.Reason
	case TransAnswer:
		kind = "answered"
		body["step"] = d.exec.CurrentStep
		body["question_id"] = req.QuestionID
		body["answered_by"] = req.AnsweredBy
	case TransInterrupted:
		kind = "interrupted"
		body["step"] = d.exec.CurrentStep
		body["detail"] = req.Reason
	case TransStepFailed:
		kind = "step_failed"
		d.failureEventsLocked(req, info, body)
	}
	if kind == "" {
		return
	}
	d.emit(kind, body)
	for _, x := range info.extra {
		d.emit(x.kind, x.body)
	}
}

// failureEventsLocked fills the step_failed body and queues the
// follow-up events (blocked, candidate_switched, candidates_exhausted)
// the owner-facing log carries.
func (d *Driver) failureEventsLocked(req TransitionRequest, info *appliedInfo, body map[string]any) {
	dec := info.failure
	if dec == nil || req.Failure == nil {
		return
	}
	body["step"] = req.StepID
	body["cause"] = dec.Cause
	body["detail"] = req.Failure.Detail
	body["disposition"] = dec.disposition()
	body["failure_count"] = dec.Count
	body["next_dispatch"] = "resume"
	if dec.Fresh {
		body["next_dispatch"] = "fresh"
	}
	if dec.RelaxToolMs > 0 {
		body["relaxed_tool_ms"] = dec.RelaxToolMs
	}
	if dec.RelaxWallMs > 0 {
		body["relaxed_wall_ms"] = dec.RelaxWallMs
	}
	if dec.Backoff > 0 {
		body["backoff_ms"] = dec.Backoff.Milliseconds()
	}
	if dec.Switch {
		body["from_model"], body["to_model"] = dec.From, dec.To
		info.extra = append(info.extra, appliedEvent{"candidate_switched", map[string]any{
			"step": req.StepID, "from": dec.From, "to": dec.To, "cause": dec.Cause,
		}})
	}
	if dec.Exhausted {
		body["candidates_exhausted"] = true
		info.extra = append(info.extra, appliedEvent{"candidates_exhausted", map[string]any{
			"step": req.StepID, "cause": dec.Cause,
			"note": "the candidate chain is spent: the pointer stays at the end and this counts as a same-model retry",
		}})
	}
	if dec.NoChain {
		body["no_candidate_chain"] = true
	}
	if dec.Block {
		info.extra = append(info.extra, appliedEvent{"blocked", map[string]any{
			"step": req.StepID, "cause": dec.Cause, "reason": dec.BlockReason, "report": dec.Report,
		}})
	}
}

// cloneExec deep-copies an execution state for Snapshot readers (JSON
// round-trip — every field is JSON-serializable, and RawMessage payloads
// survive verbatim).
func cloneExec(e *capsule.Execution) *capsule.Execution {
	blob, err := json.Marshal(e)
	if err != nil {
		return &capsule.Execution{Steps: map[string]*capsule.Step{}} // unreachable: the struct is plain data
	}
	var out capsule.Execution
	if err := json.Unmarshal(blob, &out); err != nil {
		return &capsule.Execution{Steps: map[string]*capsule.Step{}}
	}
	if out.Steps == nil {
		out.Steps = map[string]*capsule.Step{}
	}
	return &out
}

// countToStep counts how many times target appears in the rewind stats'
// to_step log — the max_per_step bound's input.
func countToStep(toStep []string, target string) int64 {
	var n int64
	for _, s := range toStep {
		if s == target {
			n++
		}
	}
	return n
}

// isTerminalStatus reports whether a workflow_status is terminal
// (current_step frozen; every further transition refused).
func isTerminalStatus(s string) bool {
	switch s {
	case StatusCompleted, StatusFailedGate, StatusFailedRewindExhausted:
		return true
	}
	return false
}

// stepStatusOf answers a step's status ("absent" for a nil record —
// used in error messages only).
func stepStatusOf(st *capsule.Step) string {
	if st == nil {
		return "absent"
	}
	return st.Status
}

// nextAttemptSeq is the ledger's next ordinal: one past the highest
// recorded AttemptSeq (never len+1 — the ledger may hold gaps).
func nextAttemptSeq(st *capsule.Step) int {
	seq := 0
	for _, a := range st.Attempts {
		if a.AttemptSeq > seq {
			seq = a.AttemptSeq
		}
	}
	return seq + 1
}
