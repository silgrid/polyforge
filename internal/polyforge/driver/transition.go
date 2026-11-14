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
	StatusRunning    = capsule.WorkflowStatusRunning
	StatusCompleted  = capsule.WorkflowStatusCompleted
	StatusFailedGate = capsule.WorkflowStatusFailedGate
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
// Every rewind target archive orders the target's
// re-entry as a resume of its previous conversation (the order is that
// ledger cause itself, derived by prepareDispatch/applyStepStartLocked —
// no second carrier); a resuming target keeps its candidate pointer, a
// gate target and the superseded steps reset theirs.
const (
	CauseGateFailRewind = "gate_fail_rewind"
	// CauseGateFailVerdict is the audit-only attempt cause of a gate
	// dispatch whose FAIL verdict rewound the workflow: the gate is never
	// archived nor reset (it stays in_progress), so this entry is the one
	// place its own ledger records that dispatch — the verdict output as
	// Result, the window it consumed as ConsumedBudget. RewindGate stays
	// empty: it is no rewind archive, so every rewind-ledger derivation
	// (gateLastVerdictAtLocked, rewindReentryOrder,
	// crashedBuildAfterRewindLocked) reads exactly what it read before.
	CauseGateFailVerdict    = "gate_fail_verdict"
	CauseSupersededByRewind = "superseded_by_rewind"
	CauseInfraFailure       = "infra_failure"
)

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
	// TransInit is an addition the single-write rule forces: someone must
	// write the first execution.json (that write is a mutation like any
	// other). Every other kind follows the driver spec.
	TransInit TransitionKind = "init"
	// TransInterrupted is the recovery path's write: ONE audit-only
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
	// TransBlocked parks the workflow blocked on the driver's own
	// authority before a dispatch: polyforge#33's dispatch-time candidate
	// composition failure. Plumbing only: it emits the existing blocked
	// event and the owner's `pf resume` unblocks it.
	TransBlocked TransitionKind = "blocked"
	// TransStepSkipped records a step the workflow skips (StepSpec.Skipped)
	// as skipped instead of dispatching it: no derivation,
	// no position move; it emits step_skipped.
	TransStepSkipped TransitionKind = "step_skipped"
)

// TransitionRequest is one submitted execution.json mutation. SpawnN is
// the fencing key: the ordinal of the derivation the request belongs
// to. The driver's own loop fills it from the dispatch it is settling;
// daemon-side callers (Answer/Terminate) snapshot the current
// counter — a mismatch is the honest answer "this request targets a
// superseded spawn", never retried blindly.
type TransitionRequest struct {
	Kind      TransitionKind
	StepID    string
	Result    *StepResult // step_result
	Model     string      // step_start: the pinned candidate this dispatch runs under
	Effort    string      // step_start: that candidate's thinking effort (polyforge#176)
	Candidate string      // step_start: the dispatch's full `<harness>:<model>[:effort]` candidate ("" = none in effect; polyforge#33)
	Status    string      // terminal: the target workflow_status
	Reason    string      // terminal/rewind reason
	// Failure carries a step_failed write's failed-dispatch record.
	Failure *StepFailure
	// WallMs is the finished dispatch's wall time folded into the step's
	// cumulative runtime by a step_result write.
	WallMs int64
	// BookWall (interrupted): the system relaunched the dispatch after a
	// hang, stall or death — book its wall time toward the step's T.
	BookWall bool
	// TreeHash (interrupted) is the capsule tree hash as of
	// the archive, computed before the transition takes the driver's lock
	// (git I/O never under d.mu) and recorded on the archived attempt.
	// Empty when hashing failed.
	TreeHash string
	// OwnerMessage (step_result) is the owner message a hand-off citing
	// the owner rests on (settleHandoff: read from the capsule's asks.jsonl
	// before the write); nil without an intent.
	OwnerMessage *capsule.AskLine
	// OwnerRewind is the rewind a hand-off citing the owner lands
	// (`rewind --to <step> --per-owner`, ownerrewind.go).
	OwnerRewind *capsule.OwnerRewindRequest
	// StartHeads (step_start) are the task repos' HEADs as the dispatch
	// starts, read before the transition takes the driver's lock: recorded
	// as the step's round start commits when this dispatch opens its round.
	StartHeads map[string]string
	SpawnN     int64
}

// GateVerdict is one business gate FAIL judgment (01 §6.2): the policy
// the workflow declared.
type GateVerdict struct {
	GateStepID string
	// TargetStep is the on_fail rewind target: a gate FAIL always
	// rewinds to it. An empty target means the gate declared no rewind
	// edge and the workflow fails on the verdict.
	TargetStep string
	// PerOwner marks a FAIL the gate handed off citing the owner's message
	// (`pf step handoff --status fail --per-owner`, workflow contract 4):
	// its rewind is the owner's, charged to no budget.
	PerOwner bool
}

// appliedInfo records what one apply actually did, for the event body.
type appliedInfo struct {
	archived []string
	target   string
	resume   bool
	failure  *failureDecision
	// candidate is the failing dispatch's composed candidate, captured
	// before the failure decision may advance Step.Candidate (the
	// step_failed body names the candidate that died, never its successor).
	candidate string
	extra     []appliedEvent
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
	case TransInterrupted:
		return d.applyInterruptedLocked(req)
	case TransClaimReentry:
		return d.applyClaimReentryLocked()
	case TransStepFailed:
		return d.applyStepFailedLocked(req, info)
	case TransStepSkipped:
		return d.applyStepSkippedLocked(req)
	case TransBlocked:
		if isTerminalStatus(d.exec.WorkflowStatus) {
			return fmt.Errorf("driver: workflow is terminal (%s)", d.exec.WorkflowStatus)
		}
		d.exec.WorkflowStatus = StatusBlocked
		d.exec.BlockedReason = req.Reason
		// The block names the dispatch that could not be assembled, so the
		// position, the panels/observe faces and the unblock Supervision
		// reset all act on that step — never on the previous completed one
		// (C5).
		if req.StepID != "" {
			d.exec.CurrentStep = req.StepID
		}
		return nil
	default:
		return fmt.Errorf("driver: unknown transition kind %q", req.Kind)
	}
}

// applyStepSkippedLocked records a pending step the workflow skips as
// skipped under a running workflow. A rewind that resets it to pending
// leaves it for the next plan pass to skip again.
func (d *Driver) applyStepSkippedLocked(req TransitionRequest) error {
	if !d.workflow.skipped(req.StepID) {
		return fmt.Errorf("driver: step %q is not skipped by the workflow", req.StepID)
	}
	if d.exec.WorkflowStatus != StatusRunning {
		return fmt.Errorf("driver: a step is skipped under a running workflow, not %s", d.exec.WorkflowStatus)
	}
	st := d.exec.Steps[req.StepID]
	if st == nil {
		st = &capsule.Step{}
		d.exec.Steps[req.StepID] = st
	}
	if st.Status != capsule.StepPending && st.Status != "" {
		return fmt.Errorf("driver: step %q is %s; only a pending step is skipped", req.StepID, st.Status)
	}
	st.Status = capsule.StepSkipped
	return nil
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
// — the round's rewind verdict stays in pending_rewind_input for the
// whole round, and the budget window re-opens after the
// rewind archived it); an IN_PROGRESS step is a resume re-dispatch (answer
// continuation, crash recovery, recovery-lane re-derivation — a new
// measurement window, the prior partial output kept). From StatusBlocked
// only the current step may resume: that is the owner's §6.4 unblock
// action, which also rewrites the status back to running.
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
		st.BudgetConsumed = capsule.Budget{} // the rewind archived the prior window
		st.Round++                           // the round every dispatch until the next reset carries (PF_ROUND)
		st.Status = capsule.StepInProgress
		if d.rewindResumes(req.StepID) && rewindResumeContext(st.Attempts) {
			info.resume = true // the rewind re-entry: a resume of the previous conversation (the ledger cause is the order)
		}
	case st.Status == capsule.StepInProgress:
		info.resume = true
	default:
		return fmt.Errorf("driver: step %q is %s; only pending or in_progress steps start", req.StepID, st.Status)
	}
	if st.Supervision != nil {
		st.Supervision.NextDispatch = "" // this dispatch consumed the shape the last failure ordered
	}
	// The model change this dispatch makes against the persisted
	// Step.Candidate is a candidate_switched event: a pending advancement
	// the newest attempt still owes (a chain-end or no-chain death, then an
	// owner list edit that added a later candidate) names its death cause,
	// anything else is a config change. A switch decided at the failure is already persisted
	// and was announced there.
	if st.Candidate != "" && req.Candidate != "" && st.Candidate != req.Candidate {
		cause := "config_changed"
		if n, c := pendingAdvance(st.Attempts, st.Candidate); n > 0 {
			cause = c
		}
		info.extra = append(info.extra, appliedEvent{"candidate_switched", map[string]any{
			"step": req.StepID, "from": st.Candidate, "to": req.Candidate, "cause": cause,
		}})
	}
	st.Candidate = req.Candidate
	st.Dispatch = req.SpawnN
	st.StepStartedAt = d.stamp() // refreshes on every re-entry (reference.md §7.3)
	if req.StartHeads != nil && (st.StartHeads == nil || st.StartHeads.Round != st.Round) {
		// the round's first dispatch: its start commits (workflow contract 4)
		st.StartHeads = &capsule.StartHeads{Round: st.Round, Heads: req.StartHeads}
	}
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
	d.exec.WorkflowStatus = StatusRunning
	d.exec.BlockedReason = ""
	// The owner's resume supersedes an owner's cancel that never landed at
	// the hub (review s8#1): a stale intent would cancel
	// the resumed work item at its next blocked close.
	d.exec.CancelRequested = nil
	// the human's "continue" resets the step's failure ledger (the
	// relaxed thresholds stay: they are the step's learned bounds) and
	// opens a new streak era (ResumedAt, streakEra): the ledger-derived
	// streaks — the model-class and EWR rotations, the retry pacing —
	// start fresh, and the blocking death's backoff is not owed
	if st := d.exec.Steps[d.exec.CurrentStep]; st != nil {
		if st.Supervision == nil {
			st.Supervision = &capsule.StepSupervision{}
		}
		sup := st.Supervision
		sup.ResumedAt = d.stamp()
		sup.FailureCount = 0
		sup.LongTaskTimeouts = 0
		sup.SlowModelTimeouts = 0
		sup.NextDispatch = ""
	}
	return nil
}

// applyStepResultLocked lands one accepted hand-off of the current step
// (routed by handoffShape) and applies its outcome (01 §6.5 steps 2-5):
//
//   - completed: the step is done — the only accept path;
//   - gate_failed: the gate's fail policy — rewind or terminal — or the
//     owner's waive lands in the same write, so no crash can leave a
//     verdict nobody consumes (the gate is never done on a FAIL);
//   - incomplete: workflow_status=blocked, position frozen (01 §6.4),
//     the hand-off's reason in the blocked reason (StepBlockedReason); a
//     message to the work item wakes the step;
//   - intent: the owner's cancel or rewind (intent.go).
func (d *Driver) applyStepResultLocked(req TransitionRequest, info *appliedInfo) error {
	res := req.Result
	if res == nil {
		return fmt.Errorf("driver: step_result without a result")
	}
	if req.StepID == "" {
		req.StepID = res.StepID
	}
	if req.StepID == "" {
		return fmt.Errorf("driver: step_result without a step id")
	}
	if res.StepID != "" && res.StepID != req.StepID {
		return fmt.Errorf("driver: result names step %q, request says %q", res.StepID, req.StepID)
	}
	if res.SpawnN != req.SpawnN {
		return fmt.Errorf("%w: result spawn %d, request spawn %d", ErrStaleFence, res.SpawnN, req.SpawnN)
	}
	if res.WIID != "" && res.WIID != d.wi.ID {
		return fmt.Errorf("driver: result names work item %q, this driver drives %q", res.WIID, d.wi.ID)
	}
	switch res.Outcome {
	case OutcomeCompleted, OutcomeGateFailed, OutcomeIncomplete, OutcomeIntent:
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
	var verdict *GateVerdict
	if res.Outcome == OutcomeGateFailed {
		var err error
		if out, err = parseOutput(res.Output); err != nil {
			return err
		}
		// the workflow's on_fail (validated at load to name an earlier
		// step) is the one rewind-target authority
		verdict = d.verdictFor(req.StepID, res)
	}

	st.Dispatch = 0
	if req.WallMs > 0 {
		d.addWallLocked(st, req.WallMs)
	}

	switch res.Outcome {
	case OutcomeCompleted:
		st.Status = capsule.StepDone
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
		// the owner's waive lands in place of the plain policy (intent.go)
		handled, err := d.applyGateFailIntentLocked(req.StepID, st, out, res, req.OwnerMessage, info)
		if err != nil {
			return err
		}
		if !handled {
			if err := d.applyGateFailLocked(req.StepID, verdict, out, info); err != nil {
				return err
			}
		}
	case OutcomeIncomplete:
		d.exec.WorkflowStatus = StatusBlocked
		d.exec.BlockedReason = StepBlockedReason(req.StepID, res.Blocker)
	case OutcomeIntent:
		// the owner's cancel or rewind (intent.go)
		return d.applyIntentLocked(req.StepID, st, res, req.OwnerMessage, info)
	}
	return nil
}

// applyGateFailLocked consumes one gate FAIL policy (01 §6.2): a gate
// without on_fail ends the workflow failed_gate (WF-102); a gate with
// on_fail archives every step in [target, gate) (the target as
// gate_fail_rewind, the steps after it as superseded_by_rewind), resets
// each to pending, and moves the position back to target. The gate itself
// is neither archived nor reset: it stays in_progress (its round
// advances), and its candidate pointer is untouched (INV-1g — rewind
// never switches candidates). The rewind is charged to max_rewinds unless
// the gate handed it off citing the owner (--per-owner: the owner's,
// charged to nothing). The budget is held at the call: `pf step handoff`
// refuses a fail not citing the owner once it is spent
// (RewindBudgetSpent), so nothing here parks on it.
//
// It runs inside the gate's step_result write, after that write checked
// the gate is the running current step (the target is the workflow's
// on_fail, validated at load to name an earlier step).
func (d *Driver) applyGateFailLocked(gate string, v *GateVerdict, out map[string]any, info *appliedInfo) error {
	target := v.TargetStep
	if target == "" { // no rewind edge declared: the gate verdict is terminal
		d.exec.WorkflowStatus = StatusFailedGate
		return nil
	}
	tIdx, _ := d.workflow.indexOf(target)
	gIdx, _ := d.workflow.indexOf(gate)
	class := capsule.RewindCharged
	if v.PerOwner {
		class = capsule.RewindOwner
	}
	d.applyRewindRangeLocked(tIdx, gIdx, target, gate, CauseGateFailRewind, class, info)
	d.closeGateRoundLocked(gate, out, "FAIL verdict rewound the workflow to "+target)
	return nil
}

// closeGateRoundLocked closes the human round a gate's FAIL verdict
// judged, on the gate itself: the dispatch whose verdict routed the
// workflow is archived (gate_fail_verdict, the verdict output and its
// budget window), its answer chain closes, and its re-entry starts a new
// conversation in a fresh failure and budget window. Used by the rewind
// of applyGateFailLocked.
func (d *Driver) closeGateRoundLocked(gate string, out map[string]any, detail string) {
	// The gate itself stays in_progress, but its verdict closes the human
	// round it judged: answers recorded so far belong to the judged round.
	if gst := d.exec.Steps[gate]; gst != nil {
		// The rewound dispatch keeps its own record on the gate (captured
		// before the budget window below resets).
		raw, _ := json.Marshal(out)
		consumed := gst.BudgetConsumed
		gst.Attempts = append(gst.Attempts, capsule.Attempt{
			Cause:          CauseGateFailVerdict,
			AttemptSeq:     nextAttemptSeq(gst),
			StartedAt:      gst.StepStartedAt,
			ArchivedAt:     d.stamp(),
			Result:         raw,
			Detail:         detail,
			ConsumedBudget: &consumed,
		})
		// The verdict closes the gate's round: the gate stays in_progress
		// (no step_start of a pending step advances it), so its next
		// dispatch's round (PF_ROUND) is opened here, in the same write.
		gst.Round++
		// Its conversation judged the version now being redone: the
		// re-entry must start a NEW conversation (prior transcript as
		// read-only reference at most), never resume that judgment.
		if gst.Supervision == nil {
			gst.Supervision = &capsule.StepSupervision{}
		}
		gst.Supervision.NextDispatch = capsule.NextDispatchFresh
		// B2: the verdict closes the gate's round, so its
		// re-entry starts a FRESH failure window — the deaths counted
		// before the verdict belonged to the round it already judged. The
		// same reset a rewind gives its target (01 §6.3 item 5).
		gst.Supervision.FailureCount = 0
		gst.Supervision.LongTaskTimeouts = 0
		gst.Supervision.SlowModelTimeouts = 0
		// D6: a rewind resets T — the gate's re-entry judges the redone
		// work in a fresh window, not on the runtime of the verdict it
		// already delivered.
		gst.BudgetConsumed = capsule.Budget{}
	}
}

// rewindResumeContext answers whether a step's latest non-infra_failure
// archived attempt is a rewind target archive (gate_fail_rewind, the
// text-only or the mechanical light cause — whatever the verdict's
// findings): its next dispatch is the rewind re-entry and
// resumes the previous round's conversation (rewindResumes decides which
// steps may). An accepted attempt or a superseded_by_rewind archive
// answers false.
func rewindResumeContext(attempts []capsule.Attempt) bool {
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i].Cause == CauseInfraFailure {
			continue // audit-only interruptions do not change the re-entry
		}
		return rewindTargetCause(attempts[i].Cause)
	}
	return false
}

// rewindResumes answers whether a rewind into stepID re-enters it as a
// resume: every step but a gate. A gate's re-entry is
// always a new conversation — its verdict closed the round it judged
// (closeGateRoundLocked), and a judge never inherits a persuasive context
// — so a rewind that targets a gate re-enters it fresh.
func (d *Driver) rewindResumes(stepID string) bool {
	spec := d.workflow.step(stepID)
	return spec != nil && !spec.Gate
}

// clipRunes cuts s to max runes, marking the cut.
func clipRunes(s string, max int) string {
	if rs := []rune(s); len(rs) > max {
		return string(rs[:max]) + "… (truncated; the full output is in the step's archived attempt)"
	}
	return s
}

// applyRewindRangeLocked archives every step in [from, to) and resets it
// to pending with output cleared, then moves the position to the target.
// The target (index from) is archived with the targetCause the caller
// chose (gate_fail_rewind); every later step in the range as
// superseded_by_rewind. rewindGate names the triggering gate; class
// is the record's accounting class — charged counts toward
// max_rewinds, owner toward nothing (capsule.RewindRecord).
func (d *Driver) applyRewindRangeLocked(from, to int, target, rewindGate, targetCause, class string, info *appliedInfo) {
	for i := from; i < to; i++ {
		sid := d.workflow.Steps[i].ID
		st := d.exec.Steps[sid]
		if st == nil {
			d.exec.Steps[sid] = &capsule.Step{Status: capsule.StepPending}
			continue
		}
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
			ConsumedBudget: &consumed,
		})
		st.Status = capsule.StepPending
		if i != from || !d.rewindResumes(sid) {
			// the era reset: the pointer returns to the live list head. A
			// rewound target that resumes keeps its candidate:
			// a pointer moved back to the head after a failover would put the
			// re-entry on another harness and turn its resume into a new
			// conversation.
			st.Candidate = ""
		}
		st.Supervision = nil // a gate rewind into the step resets its failure ledger and relaxed bounds
		info.archived = append(info.archived, sid)
	}
	d.exec.RewindStats.Count++
	d.exec.RewindStats.Rewinds = append(d.exec.RewindStats.Rewinds, capsule.RewindRecord{
		Gate: rewindGate, ToStep: target, Class: class,
	})
	d.exec.CurrentStep = target
	info.target = target
}

// applyTerminalLocked lands a terminal workflow_status. completed is
// reserved for the all-done path (01 §6.5 step 6: the loop must prove
// every step done — or skipped, — before writing it — an
// empty next_step_by_order alone is NOT completion); the failure statuses
// carry the owner's or the gate policy's verdict.
func (d *Driver) applyTerminalLocked(req TransitionRequest) error {
	switch req.Status {
	case StatusCompleted, StatusFailedGate:
	default:
		return fmt.Errorf("driver: %q is not a terminal workflow_status", req.Status)
	}
	if isTerminalStatus(d.exec.WorkflowStatus) {
		return fmt.Errorf("driver: workflow is already terminal (%s)", d.exec.WorkflowStatus)
	}
	if req.Status == StatusCompleted {
		for _, spec := range d.workflow.Steps {
			st := d.exec.Steps[spec.ID]
			if st == nil || (st.Status != capsule.StepDone && st.Status != capsule.StepSkipped) {
				return fmt.Errorf("driver: completed requires every step done or skipped; %q is %s", spec.ID, stepStatusOf(st))
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

// applyInterruptedLocked archives the interrupted derivation as one
// infra_failure attempt on the current in_progress step (the recovery
// lane's re-derivation input; the recomputation advances on
// provider-class entries alone). A dispatch that ended legitimately
// archives nothing: an open ask means the dispatch DELIVERED, and a step
// that is not in flight means the recovery cause predates the
// step's own resolution. The no-archive shapes are silent, not errors —
// the transition still lands (one version bump, no state change).
func (d *Driver) applyInterruptedLocked(req TransitionRequest) error {
	st := d.unsettledStepLocked()
	if st == nil {
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
		// result or failure write books it otherwise; unsettledStepLocked
		// is the one test of that), and only the time since its start or
		// since the last interruption archived for it — a second relaunch
		// before the next dispatch never re-books.
		if ms := interruptedWallMs(st, now); ms > 0 {
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
		Candidate:     st.Candidate,
		TreeHash:      req.TreeHash,
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
// the step is in_progress and its dispatch never settled (Step.Dispatch
// set) — not by an open ask either; nil otherwise.
func (d *Driver) unsettledStepLocked() *capsule.Step {
	if d.exec.WorkflowStatus != StatusRunning {
		return nil
	}
	st := d.exec.Steps[d.exec.CurrentStep]
	if st == nil || st.Status != capsule.StepInProgress || st.Dispatch == 0 {
		return nil
	}
	if d.parkedAskLocked(st) {
		return nil // settled by its open ask: the step awaits the owner (ask.go)
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
// vocabulary where one exists.
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
		if req.Candidate != "" {
			body["candidate"] = req.Candidate
		}
	case TransStepResult:
		if req.Result == nil {
			return
		}
		body["step"] = req.StepID
		switch req.Result.Outcome {
		case OutcomeCompleted:
			kind = "step_done"
		case OutcomeGateFailed:
			kind = "gate_verdict"
			switch {
			case isTerminalStatus(d.exec.WorkflowStatus):
				info.extra = append(info.extra, appliedEvent{"terminal", map[string]any{
					"spawn_n": d.spawnN, "status": d.exec.WorkflowStatus, "gate": req.StepID,
				}})
			case req.Result.Intent != nil:
				// the waive's own events ride info.extra (intent.go)
			default:
				info.extra = append(info.extra, appliedEvent{"rewind", map[string]any{
					"spawn_n": d.spawnN, "gate": req.StepID, "target": info.target, "archived": info.archived,
				}})
			}
		case OutcomeIncomplete:
			kind = "incomplete"
		case OutcomeIntent:
			// the write's own intent event (applyIntentLocked), its
			// follow-ups after it
			if len(info.extra) > 0 {
				kind, body = info.extra[0].kind, info.extra[0].body
				info.extra = info.extra[1:]
			}
		}
	case TransTerminal:
		kind = "terminal"
		body["status"] = req.Status
		body["reason"] = req.Reason
	case TransInterrupted:
		kind = "interrupted"
		body["step"] = d.exec.CurrentStep
		body["detail"] = req.Reason
	case TransBlocked:
		kind = "blocked"
		body["step"] = req.StepID
		body["reason"] = req.Reason
		body["report"] = false
	case TransStepFailed:
		kind = "step_failed"
		d.failureEventsLocked(req, info, body)
	case TransStepSkipped:
		kind = "step_skipped"
		body["step"] = req.StepID
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
// the owner-facing log carries. The decided switch is already persisted in
// Step.Candidate (applyStepFailedLocked); the step_start event covers only
// a switch the next dispatch makes by itself (an owner list edit, including
// one that adds a candidate after a chain-end or no-chain death).
func (d *Driver) failureEventsLocked(req TransitionRequest, info *appliedInfo, body map[string]any) {
	dec := info.failure
	if dec == nil || req.Failure == nil {
		return
	}
	body["step"] = req.StepID
	body["cause"] = dec.Cause
	if info.candidate != "" {
		body["candidate"] = info.candidate
	}
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
			"note": "the candidate chain is spent: no usable model is left and the step blocks",
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

// countTowardTotal counts the ledger records max_rewinds counts: every
// record but an owner-routed one.
func countTowardTotal(rewinds []capsule.RewindRecord) int64 {
	var n int64
	for _, r := range rewinds {
		if r.Class != capsule.RewindOwner {
			n++
		}
	}
	return n
}

// isTerminalStatus reports whether a workflow_status is terminal
// (current_step frozen; every further transition refused).
func isTerminalStatus(s string) bool {
	switch s {
	case StatusCompleted, StatusFailedGate:
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
