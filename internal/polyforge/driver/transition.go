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
	"regexp"
	"sort"
	"strings"
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
	// CauseLightRecheckEscalated is the audit-only attempt cause of the
	// light path's in-place escalation (polyforge#260): a light-path
	// failure — a malformed classification, a broken repair bound, a
	// light recheck that did not pass, or a fresh gate rewind (a
	// structural review FAIL) that abandoned the pending light recheck
	// escalated the verification step to the existing
	// full gate in the same step. It is neither a
	// rewind archive nor model-class infra: it charges no rewind slot
	// and leaves the candidate pointer's count alone. The reason rides
	// Detail; the audit facts (bound, diff base, measured diff, recheck,
	// escalated flag, and the abandoned flag that tells a fresh
	// re-derivation's abandonment from the in-place escalation —
	// S2) ride Result.
	CauseLightRecheckEscalated = "light_recheck_escalated"
	// CauseGateFailVerdict is the audit-only attempt cause of a gate
	// dispatch whose FAIL verdict rewound the workflow: the gate is never
	// archived nor reset (it stays in_progress), so this entry is the one
	// place its own ledger records that dispatch — the verdict output as
	// Result, the window it consumed as ConsumedBudget. RewindGate stays
	// empty: it is no rewind archive, so every rewind-ledger derivation
	// (gateLastVerdictAtLocked, rewindReentryOrder,
	// crashedBuildAfterRewindLocked) reads exactly what it read before.
	CauseGateFailVerdict = "gate_fail_verdict"
	// CauseRootCauseUnaddressed is the rewind archive cause of the
	// diagnosis step when a completed fix honestly reported
	// root_cause_addressed: false: the driver rewound the
	// lane to root-cause once, before review and verification, with the
	// fix's summary as the rewind input (rewind_gate = the fix step).
	CauseRootCauseUnaddressed = "root_cause_unaddressed"
	CauseSupersededByRewind   = "superseded_by_rewind"
	CauseInfraFailure         = "infra_failure"
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
per-unit commits, the lane's own repro bar and authored-tests gate included).
A fix that needs a change OUTSIDE that boundary — code,
tests or assertions, user-facing string literals, workflows/** .md files,
skills/** prompt material — is NOT yours to apply: do not apply it, and
instead deliver your completed result carrying "needs_code_change": true;
the driver rewinds fresh with the same verdict, counting no additional
rewind — but that fresh round uses one of the rewinding gate's per-step
rewinds, like any ordinary FAIL.`

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
and 5 files, summed across repos. A lint or format fix inside a
bound-named code file IS yours to apply — the bound is the boundary, not
the text-only rule. Your step's existing discipline is
unchanged (fast checks per unit, per-unit commits, the lane's own repro
bar and authored-tests gate included). A fix that must leave
that bound, delete or rename a file, or exceed the caps is NOT yours to
apply: do not apply it, and instead deliver your completed result carrying
"needs_code_change": true; the driver rewinds fresh with the same verdict,
counting no additional rewind — but that fresh round uses one of the
rewinding gate's per-step rewinds, like any ordinary FAIL.`

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
	// TransEnvelopeRejected is the in-process retry lane's durable
	// rejection write: ONE consecutive-envelope-rejection count increment
	// for the current dispatch (applyEnvelopeRejectedLocked), so the bound
	// survives a restart and a re-collected archived envelope is counted
	// once. It changes no other state and consumes no derivation ordinal.
	TransEnvelopeRejected TransitionKind = "envelope_rejected"
	// TransLightEscalate is the mechanical fast lane's in-place escalation
	// write (polyforge#260): a light-path failure detected BEFORE any
	// dispatch (a malformed classification or a broken repair bound) —
	// the escalation archives the audit-only light_recheck_escalated
	// attempt on the verification step with the reason, clears the
	// pending light recheck marker and orders the next dispatch fresh —
	// no rewind slot, no rewind input — so the loop re-dispatches the
	// step in full mode (the existing full verification, unchanged). It
	// consumes no derivation ordinal (like TransUnblock).
	TransLightEscalate TransitionKind = "light_recheck_escalate"
	// TransClaimReentry is a claim's re-entry of an in_progress step whose
	// dispatch never settled (cancelled by an operator pause, a shutdown
	// or a crash before this claim): it re-stamps step_started_at, so a
	// later system relaunch books only from the claim and the time before
	// it never counts toward T (applyClaimReentryLocked).
	TransClaimReentry TransitionKind = "claim_reentry"
	// TransBlocked parks the workflow blocked on the driver's own
	// authority before a dispatch: polyforge#33's dispatch-time candidate
	// composition failure and's unchanged-tree gate park
	// (blockUnchangedGateTree). Plumbing only: it emits the existing
	// blocked event and the owner's `pf resume` unblocks it.
	TransBlocked TransitionKind = "blocked"
	// TransStepRevision is the owner's correction of accepted
	// e2e-author binding commands: the same single-writer
	// path as every other state change, so the batch of corrections is
	// persisted atomically and the observe publish appends it to the chain.
	// It consumes no derivation ordinal and changes no step status; it
	// replaces the named commands and appends one audit record per
	// corrected index to the step.
	TransStepRevision TransitionKind = "step_revision"
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
	Result     *StepResult // step_result
	Model      string      // step_start: the pinned candidate this dispatch runs under
	Effort     string      // step_start: that candidate's thinking effort (polyforge#176)
	Candidate  string      // step_start: the dispatch's full `<harness>:<model>[:effort]` candidate ("" = none in effect; polyforge#33)
	Status     string      // terminal: the target workflow_status
	Reason     string      // terminal/rewind reason; answer: the answer text
	QuestionID string      // answer: the question id being answered
	AnsweredBy string      // answer: who answered ("" → "owner")
	// Failure carries a step_failed write's failed-dispatch record.
	Failure *StepFailure
	// WallMs is the finished dispatch's wall time folded into the step's
	// cumulative runtime by a step_result write.
	WallMs int64
	// BookWall (interrupted): the system relaunched the dispatch after a
	// hang, stall or death — book its wall time toward the step's T.
	BookWall bool
	// TreeHash (interrupted,) is the capsule tree hash as of
	// the archive, computed before the transition takes the driver's lock
	// (git I/O never under d.mu) and recorded on the archived attempt.
	// Empty when hashing failed.
	TreeHash string
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
	// ReviewedHeads is the task repos' HEADs a completed
	// review PASS records under reviewed_heads (deliver_facts.go), read
	// before the transition takes the driver's lock (the verifiedHeads
	// pattern). Nil for any other step, outcome or verdict.
	ReviewedHeads map[string]any
	// JudgedHeads (code review) is the task repos' HEADs a
	// VERDICT-BEARING review round judged — a FAIL names the tree it
	// failed, a PASS the tree it covered — recorded on that round's own
	// LastReviewRound so the next re-entry's fix surface has an anchor
	// that survives rewinds (reviewed_heads exists on a PASS alone and a
	// rewind archives it). Read before the transition takes the driver's
	// lock; nil for any non-review step or verdict-less outcome.
	JudgedHeads map[string]string
	// LightDiffBase (polyforge#260) is the task repos' HEADs an armed
	// verification gate-FAIL that carries a mechanical classification
	// settles on — the light recheck's repair-diff base, read before the
	// transition takes the driver's lock (the verifiedHeads pattern: git
	// I/O never under the lock, never step-supplied).
	LightDiffBase map[string]string
	// LightSettle (polyforge#260) is the light recheck's pre-read
	// settlement facts (set for a completed verification result whose
	// step holds the pending light recheck marker — the light pass or
	// its escalation — and for a gate_failed one holding it, the
	// recheck's own FAIL verdict escalation): the re-measured repair
	// diff and the recheck heads, both read before the transition takes
	// the driver's lock. Nil = no light settlement.
	LightSettle *lightSettleFacts
	// LightEscalationDiff (polyforge#260) is the repair diff a
	// pre-dispatch escalation (TransLightEscalate) measured before this
	// request — nil when the classification was malformed and nothing
	// was measurable. It is the audit fact the escalation attempt
	// archives beside its reason, carried on the request because git
	// I/O never runs under the transition's lock.
	LightEscalationDiff *lightDiff
	// ScopeChain is the driver-built three-ring scope audit
	// chain a completed scoped delivery records under `scope_chain`. It is
	// built from the persisted verification/review facts before the
	// transition takes the lock (the verifiedHeads pattern); nil = a full
	// delivery (or an inconsistent chain), and the step-supplied value is
	// always deleted.
	ScopeChain map[string]any
	// ScopeTier is the effective verification tier a
	// verification step_start records: the marker-derived tier when the
	// execution record has none yet. Empty falls back to the marker and
	// the lane.
	ScopeTier string
	// BindingRevision carries a step_revision write's owner corrections of
	// done e2e-author binding commands, applied as one batch. RevisionOutput
	// is the complete revised output ReviseBinding validated OUTSIDE the
	// transition lock (the shell parse is I/O); RevisionBefore holds the
	// commands it replaces (aligned with BindingRevision.Corrections), and
	// the write re-checks them so a concurrent revision cannot be clobbered.
	BindingRevision *BindingRevisionRequest
	RevisionOutput  map[string]any
	RevisionBefore  []string
	// JudgedRepoHash ( criterion 6) is the task-repo tree hash
	// a GATE step judged by delivering this result (PASS or FAIL), read
	// before the transition takes the driver's lock. It is written to
	// Step.JudgedRepoHash in the same settlement write; an empty value
	// (hash error, or a non-gate/needs_input/incomplete result) records no
	// judged tree, so the unchanged-tree guard fails open.
	JudgedRepoHash string
	// ReproHeads are the task repos' HEADs a completed repro result ran on
	// (pre-read outside the lock): the pre-fix base the first accepted
	// repro records.
	ReproHeads map[string]string
	SpawnN     int64
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
	// candidate is the failing dispatch's composed candidate, captured
	// before the failure decision may advance Step.Candidate (the
	// step_failed body names the candidate that died, never its successor).
	candidate string
	extra     []appliedEvent
	// selfRewind marks the needs_code_change self-report rewind
	// (polyforge#222): the completed result was NOT accepted — the same
	// write rewound the step fresh — so the event is the standard rewind
	// (never step_done for a step that is not done); rewindGate names the
	// light gate that rewound it.
	selfRewind bool
	rewindGate string
	// selfRewindExhausted marks a self-report whose escalation found the
	// light gate's edge spent: the same write ended the
	// workflow failed_rewind_exhausted, so the event is the terminal —
	// neither a rewind nor step_done.
	selfRewindExhausted bool
	// escalated marks the light path's in-place escalation
	// (polyforge#260): the light recheck did not settle a light pass — the
	// same write archived the audit-only escalation attempt instead — so
	// the step is not done and the event is the escalation, never
	// step_done; escalateReason carries why.
	escalated      bool
	escalateReason string
	// recheckAbandoned carries why a fresh re-derivation abandoned
	// verification's pending light recheck (
	// abandonLightRecheckLocked): the event after the write's rewind is
	// the escalation's own, naming verification.
	recheckAbandoned string
	// rootCauseRewind marks the fix→root-cause rewind of a first
	// root_cause_addressed: false: the event is the
	// standard rewind, never step_done (the fix is not accepted).
	// rootCauseQuestion marks the second false's owner question: the
	// event is needs_input.
	rootCauseRewind   bool
	rootCauseQuestion bool
	// question marks a driver-authored owner question this write parked
	// on (the review owner exit, a re-ask of it): a needs_input event
	// follows the write's own event.
	question bool
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
	// Every open question offers the reserved cancel answer, stated by
	// the driver in the one write that persists the question.
	if st := d.exec.Steps[d.exec.CurrentStep]; st != nil && st.PendingQuestion != nil &&
		!strings.HasSuffix(st.PendingQuestion.Question, CancelAnswerSentence) {
		st.PendingQuestion.Question = strings.TrimRight(st.PendingQuestion.Question, "\n") + "\n\n" + CancelAnswerSentence
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
		return d.applyAnswerLocked(req, info)
	case TransInterrupted:
		return d.applyInterruptedLocked(req)
	case TransClaimReentry:
		return d.applyClaimReentryLocked()
	case TransStepFailed:
		return d.applyStepFailedLocked(req, info)
	case TransEnvelopeRejected:
		return d.applyEnvelopeRejectedLocked(req)
	case TransBlocked:
		if isTerminalStatus(d.exec.WorkflowStatus) {
			return fmt.Errorf("driver: workflow is terminal (%s)", d.exec.WorkflowStatus)
		}
		d.exec.WorkflowStatus = StatusBlocked
		d.exec.BlockedReason = req.Reason
		// The block names the dispatch that could not be assembled, so the
		// position, the panels/observe faces and the unblock Supervision
		// reset all act on that step — never on the previous completed one
		// ( C5).
		if req.StepID != "" {
			d.exec.CurrentStep = req.StepID
		}
		return nil
	case TransLightEscalate:
		return d.applyLightEscalateLocked(req.StepID, req.Reason, req.LightEscalationDiff, nil, info)
	case TransStepRevision:
		return d.applyBindingRevisionLocked(req)
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
// — the round's rewind verdict stays in pending_rewind_input for the
// whole round,, and the budget window re-opens after the
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
		st.Output = nil                      // stale products never feed a re-run (01 §6.2 step 4)
		st.BudgetConsumed = capsule.Budget{} // the rewind archived the prior window
		st.EnvelopeRejects = 0               // a new re-entry round opens a fresh rejection window
		st.EnvelopeRejectSpawnN = 0
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
	// The model change this dispatch makes against the persisted
	// Step.Candidate is a candidate_switched event: a pending advancement
	// the newest attempt still owes (a chain-end or no-chain death, then an
	// owner list edit that added a later candidate) names its death cause,
	// anything else is a config change. A switch decided at the failure is already persisted
	// and was announced there.
	if st.Candidate != "" && req.Candidate != "" && st.Candidate != req.Candidate {
		cause := "config_changed"
		if n, c := pendingAdvance(st.Attempts, answerEraStart(st.Answers), st.Candidate); n > 0 {
			cause = c
		}
		info.extra = append(info.extra, appliedEvent{"candidate_switched", map[string]any{
			"step": req.StepID, "from": st.Candidate, "to": req.Candidate, "cause": cause,
		}})
	}
	st.Candidate = req.Candidate
	st.Dispatch = req.SpawnN
	st.StepStartedAt = d.stamp() // refreshes on every re-entry (reference.md §7.3)
	// the verification tier is recorded once, on the first
	// verification step_start (from the marker and the lane), and restored
	// unchanged on every later dispatch. A later dispatch never recomputes
	// it — the deliver 「全量重验」 rewind is its only writer besides this.
	if req.StepID == verificationStepID && d.exec.ScopeTier == "" {
		tier := req.ScopeTier
		if tier == "" {
			tier = effectiveTier(d.scopeOptIn, d.wi.BundleName)
		}
		d.exec.ScopeTier = tier
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
	// The owner's explicit resume of a unchanged-tree park IS
	// the override: forget the gate's last-judged tree so this one dispatch
	// may run the gate on the same tree instead of blocking again. A later
	// verdict records a fresh tree. (A plain resume of any other park
	// leaves the record alone.)
	if strings.HasPrefix(d.exec.BlockedReason, gateUnchangedBlockPrefix) {
		// Every gate forgets its last-judged tree: the owner's explicit
		// override covers the whole gate tail it just chose to run, not only
		// the gate that happened to be next (verification would otherwise
		// pass and the review behind it would block on the same facts).
		for _, st := range d.exec.Steps {
			if st != nil {
				st.JudgedRepoHash = ""
			}
		}
	}
	d.exec.WorkflowStatus = StatusRunning
	d.exec.BlockedReason = ""
	// The owner's resume supersedes a reserved cancel answer whose cancel
	// never landed at the hub (review s8#1): a stale intent would cancel
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
		// The owner's continue also opens a fresh envelope-rejection
		// window: the park at the bound was the reason they intervened.
		st.EnvelopeRejects = 0
		st.EnvelopeRejectSpawnN = 0
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
		// the workflow's on_fail (validated at load to name an earlier
		// step) is the one rewind-target authority
		verdict = d.verdictFor(req.StepID, res)
	}

	st.Dispatch = 0
	st.EnvelopeRejects = 0 // an accepted envelope ends the rejection streak
	st.EnvelopeRejectSpawnN = 0
	// criterion 6: a gate's verdict records the task-repo
	// tree it judged, in this same settlement write. A later rewound
	// re-entry compares the tree handed to the gate against it; an
	// unavailable hash clears the record (the guard fails open).
	if spec := d.workflow.step(req.StepID); spec != nil && spec.Gate &&
		(res.Outcome == OutcomeCompleted || res.Outcome == OutcomeGateFailed) {
		st.JudgedRepoHash = req.JudgedRepoHash
	}
	if req.WallMs > 0 {
		// human-wait time is excluded by construction: no process runs
		// while a question is open
		d.addWallLocked(st, req.WallMs)
	}

	switch res.Outcome {
	case OutcomeCompleted:
		// a completed fix that honestly reports its diagnosis
		// was wrong routes before acceptance — once to root-cause, then to
		// the owner. Judged before the light self-report below: a false
		// diagnosis is never a light re-entry.
		if d.rootCauseFalseLocked(req.StepID, out) {
			return d.applyRootCauseFalseLocked(req.StepID, st, res, out, info)
		}
		// A repro that honestly reports the bug does not reproduce is not
		// accepted into the diagnosis: the owner is asked (bug-fix dead
		// end) — re-run repro with more evidence, continue, or cancel.
		if d.notReproducedLocked(req.StepID, out) {
			st.Output = out // the report stays on the step, for the record
			d.parkDeadEndLocked(st, req.StepID, ReproNotReproducedQuestionID, res.Output,
				"`repro` completed reporting `reproduces: false`: the reported failure did not reproduce, so there is nothing to diagnose yet.", info)
			return nil
		}
		// An authored suite whose every scenario is green at baseline is
		// not a completion (the spec and the tree disagree): the owner is
		// asked — rewind to spec, skip with a recorded rationale, or cancel.
		if req.StepID == e2eAuthorStepID && allGreenBaseline(out) {
			st.Output = out // the green baseline stays on the step, for the record
			raw, _ := json.Marshal(out)
			st.PendingQuestion = &capsule.PendingQuestion{
				ID: E2EAllGreenQuestionID, Step: req.StepID, Question: allGreenQuestion(), Context: raw, AskedAt: d.stamp(),
			}
			info.question = true
			return nil
		}
		if req.StepID == reproStepID && d.exec.PreFixHeads == nil && len(req.ReproHeads) > 0 {
			d.exec.PreFixHeads = req.ReproHeads
		}
		// The needs_code_change self-report rewind (polyforge#222): a
		// completed result of a LIGHT re-entry carrying
		// needs_code_change: true is not archived done — the same write
		// rewinds fresh with the same FAIL verdict, charging the light
		// rewind's edge once (no new slot;).
		// Judged before any of the accepted-result handling below (the
		// result is not accepted: no evidence decode, no stamps, no done).
		if lightRewindContext(st.Attempts) {
			if _, value, malformed := needsCodeChangeOf(out); !malformed && value {
				return d.applySelfReportRewindLocked(req.StepID, st, out, info)
			}
		}
		// NB4 (polyforge#261): mechanical_pass is driver-written only,
		// like verifiedHeads/testFilesCommit further down — a step-supplied
		// value would forge the fast-lane audit trail. The delete sits
		// BEFORE the light-settle block below, which writes the driver's
		// own record on a light pass and must not have it eaten.
		if req.StepID == verificationStepID {
			delete(out, mechanicalPassKey)
		}
		// The light recheck's settlement (polyforge#260): a completed
		// verification result whose step holds the pending light recheck
		// marker is the light recheck's own claim. Judged (verdict pass,
		// every check pass, every bound command shown run with exit 0 in
		// its repo — the pure judgments below — plus the pre-read facts'
		// re-measured diff), it either settles the light pass (the
		// mechanical_pass record, no verified_heads, the carried test_bar,
		// the marker cleared) or escalates IN PLACE — the same write, never
		// a rewind. Judged before the accepted-result handling: an
		// escalated recheck is not done.
		if req.LightSettle != nil {
			if st.PendingLightRecheck == nil {
				return fmt.Errorf("driver: step %q light settlement without a pending light recheck marker", req.StepID)
			}
			reason := req.LightSettle.diffBroken
			var recheck []map[string]any
			if reason == "" {
				bound, perr := parseMechBound(st.PendingLightRecheck.Bound)
				switch perr {
				case nil:
					var r string
					recheck, r = lightRecheckEvidence(bound, res.CommandEvidence, d.wi.CapsuleDir)
					if r != "" {
						reason = r
					} else if r = lightRecheckVerdictHolds(out); r != "" {
						reason = r
					}
				default:
					reason = "the mechanical classification is malformed: " + perr.Error()
				}
			}
			if reason != "" {
				var escDiff *lightDiff
				if req.LightSettle.diffMeasured {
					escDiff = &req.LightSettle.diff
				}
				if err := d.applyLightEscalateLocked(req.StepID, reason, escDiff, out["checks"], info); err != nil {
					return err
				}
				st.Output = out // the light recheck's own record stays on the gate (audit)
				return nil
			}
			// The light pass: mechanical_pass built from the marker, the
			// re-measured diff, the recheck evidence and the driver-read
			// recheck heads; test_bar carried verbatim from the FAIL
			// verdict's record (the driver copies — the step agent never
			// re-transcribes it, the #203 lesson); NO verified_heads (a head
			// verified only by a light recheck is never tests-covered); the
			// marker cleared by this same write.
			var boundAny any
			_ = json.Unmarshal(st.PendingLightRecheck.Bound, &boundAny)
			// recheck_heads as the JSON shape (map[string]any): the record is
			// read back from the driver's IN-MEMORY state too (buildResultSpec
			// → lightRecheckHeads for the deliver merge rule), and a
			// map[string]string would not assert as map[string]any there —
			// only the disk round-trip would fix it.
			recheckHeads := make(map[string]any, len(req.LightSettle.heads))
			for repo, sha := range req.LightSettle.heads {
				recheckHeads[repo] = sha
			}
			out[mechanicalPassKey] = map[string]any{
				"bound":         boundAny,
				"diff":          map[string]any{"files": req.LightSettle.diff.Files, "lines": req.LightSettle.diff.Lines},
				"recheck":       recheck,
				"recheck_heads": recheckHeads,
				"escalated":     false,
			}
			if tb, ok := st.Output["test_bar"]; ok {
				out["test_bar"] = tb // carried byte-for-byte from the FAIL verdict's record
			}
			st.PendingLightRecheck = nil // cleared by the settling write
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
		if req.StepID == reviewStepID {
			// the heads the review PASS covered, per task repo
			// — the deliver merge rule's review-coverage anchor
			// (deliver_facts.go); driver-written only, a step-supplied value
			// is never kept
			delete(out, reviewedHeadsKey)
			if len(req.ReviewedHeads) > 0 {
				out[reviewedHeadsKey] = req.ReviewedHeads
			}
		}
		if req.StepID == deliverStepID {
			// the three-ring scope audit chain is driver-
			// written only — a step-supplied value would forge the chain.
			// The driver's record is present exactly for a consistent
			// scoped delivery (nil otherwise); a full delivery keeps none.
			delete(out, scopeChainKey)
			if req.ScopeChain != nil {
				out[scopeChainKey] = req.ScopeChain
			}
		}
		st.Status = capsule.StepDone
		st.Output = out
		// a review round that carries a verdict records its
		// facts (round ordinal, blocking count, judged heads) in this same
		// atomic write — the completed shape is a PASS (a fail verdict is
		// re-bound to gate_failed before the transition), so the blocking
		// count is the honest count of what the output records (zero on a
		// conforming pass).
		if req.StepID == reviewStepID {
			blocking, _, _ := reviewRoundFacts(out)
			d.recordReviewRoundLocked(req.StepID, blocking, req.JudgedHeads)
		}
		// The round ends here: the rewind verdict that every
		// dispatch of it carried is cleared by the acceptance write, so no
		// later dispatch — the next round included — replays a stale verdict.
		st.PendingRewindInput = nil
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
		// A gate is never accepted, so delivering its own verdict is the
		// end of ITS round: a rewind input the gate carried
		// as a rewind target (the deliver 「全量重验」 override reruns
		// verification) is cleared here, never replayed on the next
		// verification round. The verdict this gate routes to its own
		// target is written below, in this same write.
		st.PendingRewindInput = nil
		if st.PendingLightRecheck != nil {
			// The light recheck's own FAIL verdict (polyforge#260): a bound
			// command still red, or a semantic confirmation the agent cannot
			// make. Escalate IN PLACE, in this same write — never a rewind
			// (the light path's one fallback); the loop re-dispatches the
			// step in full mode. The pre-read settlement facts carry the
			// re-measured repair diff for the escalation's audit record.
			var escDiff *lightDiff
			if req.LightSettle != nil && req.LightSettle.diffMeasured {
				escDiff = &req.LightSettle.diff
			}
			if err := d.applyLightEscalateLocked(req.StepID, lightRecheckFailReason(out), escDiff, out["checks"], info); err != nil {
				return err
			}
			return nil
		}
		// NB3 (polyforge#261): the increment sits BELOW the in-place light
		// escalation above — a review round counts a full-gate review
		// evaluation only; the light recheck's own FAIL verdict, which
		// escalates in place and archives an audit-only attempt, is not
		// one.
		if d.exec.ReviewRounds == nil {
			d.exec.ReviewRounds = map[string]int64{}
		}
		d.exec.ReviewRounds[req.StepID]++
		if req.StepID == rootCauseStepID && verdict.TargetStep == reproStepID &&
			countToEdge(d.exec.RewindStats.Rewinds, rootCauseStepID, reproStepID) > 0 {
			// The second inconclusive diagnosis (bug-fix dead end): no
			// second charged rewind — the owner is asked instead.
			raw, _ := json.Marshal(out)
			d.parkDeadEndLocked(st, req.StepID, RootCauseInconclusiveQuestionID, raw,
				"`root-cause` is inconclusive again: the driver already rewound to `repro` once for more evidence.", info)
		} else if req.StepID == reviewStepID && verdict.TargetStep != "" && reviewAllOffGraph(out) {
			// The review owner exit: every blocking finding is off-graph,
			// so the build step cannot settle the verdict — the owner is
			// asked in this same write instead of a rewind (no edge slot,
			// no max_total count).
			d.raiseReviewOwnerExitLocked(st, out, req.SpawnN, info)
		} else if err := d.applyGateFailLocked(req.StepID, verdict, out, req.LightDiffBase, info); err != nil {
			return err
		}
		// the FAIL verdict closes a review round — record its
		// facts in this same atomic write, AFTER applyGateFailLocked consumed
		// the previous round as its convergence baseline. needs_input and
		// incomplete rounds touch neither the record nor the counter.
		if req.StepID == reviewStepID {
			blocking, _, _ := reviewRoundFacts(out)
			d.recordReviewRoundLocked(req.StepID, blocking, req.JudgedHeads)
		}
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

// The bug-fix lane's diagnosis and fix steps: the fix's
// root_cause_addressed: false routes back to the diagnosis step.
const (
	fixStepID          = "fix"
	reproStepID        = "repro"
	rootCauseStepID    = "root-cause"
	rootCauseAddressed = "root_cause_addressed"
	// RootCauseFalseQuestionID is the driver-authored owner question a
	// second root_cause_addressed: false parks the workflow on.
	RootCauseFalseQuestionID = "root_cause_false"
)

// rootCauseFalseLocked reports whether a completed result of stepID is a
// fix reporting root_cause_addressed: false (the literal boolean) on a
// workflow that declares the root-cause step before it — the bug-fix
// lane; a lane without one (autopilot) accepts the report as before.
func (d *Driver) rootCauseFalseLocked(stepID string, out map[string]any) bool {
	if stepID != fixStepID {
		return false
	}
	if v, ok := out[rootCauseAddressed].(bool); !ok || v {
		return false
	}
	rc, ok := d.workflow.indexOf(rootCauseStepID)
	fx, _ := d.workflow.indexOf(fixStepID)
	return ok && rc < fx
}

// applyRootCauseFalseLocked lands the false routing in the fix's own
// result write. The fix is not accepted: it stays
// in_progress holding its false report (a gate holds its verdict the
// same way).
//
//   - First false (no fix→root-cause record in the rewind ledger):
//     root-cause is archived (root_cause_unaddressed, rewind_gate fix)
//     and reset, the position moves to it, one CHARGED fix→root-cause
//     record is appended, and the fix's summary becomes root-cause's
//     pending_rewind_input. The ledger record IS the once-only memory —
//     no new counter. The edge bound is not consulted (once is stricter)
//     and max_total does not refuse it (the spec's unconditional first
//     rewind); the global count still increments, so a later gate FAIL
//     settles exactly as before. The fix re-dispatches fresh after the
//     new diagnosis; root-cause's own fail verdict still rewinds to
//     repro.
//   - A later false: no second rewind. A driver-authored pending
//     question (root_cause_false) parks the loop at needs_input for the
//     owner — continue with corrective guidance, or cancel and re-file;
//     the answer re-dispatches the fix in place (D9). This path never
//     reaches failed_rewind_exhausted.
//
// The first false opens a fresh re-derivation of the fix after the new
// diagnosis, so the same write abandons verification's pending light
// recheck (abandonLightRecheckLocked, S1): the range
// [root-cause, fix) never reaches verification. The later false resumes
// the fix in place and abandons nothing.
func (d *Driver) applyRootCauseFalseLocked(stepID string, st *capsule.Step, res *StepResult, out map[string]any, info *appliedInfo) error {
	st.Output = out // the false report stays on the fix, for the record
	if countToEdge(d.exec.RewindStats.Rewinds, stepID, rootCauseStepID) > 0 {
		st.PendingQuestion = &capsule.PendingQuestion{
			ID:   RootCauseFalseQuestionID,
			Step: stepID,
			Question: "The fix reported root_cause_addressed: false a second time: the driver already rewound to root-cause once " +
				"and the re-diagnosed cause was not addressed either. Decide: continue — answer with corrective guidance " +
				"(the cause to fix, or how to proceed) and the fix step resumes with it — or cancel this work item and re-file it.",
			Context: res.Output,
			AskedAt: d.stamp(),
		}
		info.rootCauseQuestion = true
		return nil
	}
	rcIdx, _ := d.workflow.indexOf(rootCauseStepID)
	fxIdx, _ := d.workflow.indexOf(stepID)
	d.applyRewindRangeLocked(rcIdx, fxIdx, rootCauseStepID, stepID, CauseRootCauseUnaddressed, capsule.RewindCharged, info)
	summary, _ := out["summary"].(string)
	if strings.TrimSpace(summary) == "" {
		summary = "(the fix gave no summary)"
	}
	d.exec.Steps[rootCauseStepID].PendingRewindInput = &capsule.RewindInput{
		ToStep: rootCauseStepID,
		Reason: "`" + stepID + "` completed reporting `root_cause_addressed: false` — the diagnosis did not hold. Its summary:\n\n" + summary,
	}
	// The fix's next dispatch follows the new diagnosis: a fresh
	// conversation and a fresh failure window, like a gate's re-entry.
	st.AnswerBase = len(st.Answers)
	if st.Supervision == nil {
		st.Supervision = &capsule.StepSupervision{}
	}
	st.Supervision.NextDispatch = capsule.NextDispatchFresh
	st.Supervision.FailureCount = 0
	st.Supervision.LongTaskTimeouts = 0
	st.Supervision.SlowModelTimeouts = 0
	st.BudgetConsumed = capsule.Budget{}
	info.rootCauseRewind = true
	info.rewindGate = stepID
	return d.abandonLightRecheckLocked("`"+stepID+"` reported root_cause_addressed: false and rewound the lane to `"+
		rootCauseStepID+"` for a new diagnosis and a fresh fix", info)
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
// the gate is the running current step (the target is the workflow's
// on_fail, validated at load to name an earlier step).
func (d *Driver) applyGateFailLocked(gate string, v *GateVerdict, out map[string]any, diffBase map[string]string, info *appliedInfo) error {
	target := v.TargetStep
	if target == "" { // no rewind edge declared: the gate verdict is terminal
		d.exec.WorkflowStatus = StatusFailedGate
		return nil
	}
	tIdx, _ := d.workflow.indexOf(target)
	gIdx, _ := d.workflow.indexOf(gate)
	// Bounds BEFORE any mutation (01 §6.2 step 2: a hit must not rewind).
	// the classification runs first — an UNCHARGED rewind
	// (light, mechanical, converging) occupies no edge slot, so the edge
	// bound never refuses it; max_total stays the global hard cap over
	// every automatic rewind, charged or not (an OWNER-routed one counts
	// toward neither bound), and max_per_step is counted per
	// gate→target edge over the CHARGED records alone — two gates sharing
	// one target no longer eat each other's slots. A ledger that cannot
	// attribute its own count (a pre-w69 shape with no per-record
	// history) proves no per-edge quota, so this rewind fails closed — no
	// compat path, no guess at the attribution.
	//
	// Transition code (AGENTS.md): only an execution.json written before
	// the per-edge ledger shipped can carry that shape. Delete this check,
	// its rewind_ledger_unattributable event and
	// TestRewindOldLedgerShapeFailsClosed once no capsule claimed before
	// the first release carrying is still in flight (every
	// such work item wrapped, cancelled or re-filed) — and with them the
	// no-record note in applySelfReportRewindLocked, whose case is then
	// unreachable.
	if d.exec.RewindStats.Count > int64(len(d.exec.RewindStats.Rewinds)) {
		d.exec.WorkflowStatus = StatusFailedRewindExhausted
		info.extra = append(info.extra, appliedEvent{"rewind_ledger_unattributable", map[string]any{
			"spawn_n": d.spawnN, "gate": gate, "target": target, "count": d.exec.RewindStats.Count,
			"detail": "the rewind ledger predates per-edge accounting and cannot attribute its count, so no per-edge quota can be proven; the workflow fails closed",
		}})
		return nil
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
	// a CONVERGING review round — a FAIL whose blocking
	// findings are all residuals of the last verdict round and strictly
	// fewer — charges no edge slot either. The baseline is the record the
	// previous verdict round wrote (read before this round's settlement
	// updates it); anything unprovable (no record, a new discovery, a
	// missing marker, a non-decreasing count) keeps the rewind charged —
	// fail closed.
	var lastRound *capsule.ReviewRound
	if st := d.exec.Steps[reviewStepID]; st != nil {
		lastRound = st.LastReviewRound
	}
	converging := gate == reviewStepID && reviewConverging(out, lastRound)
	// An owner-routed rewind — a deliver FAIL delivered after a human
	// answered in its attempt chain is that human's "back to code" or
	// 「全量重验」 — is the owner's route: it charges neither its edge slot
	// nor max_total, so neither bound refuses it. A deliver FAIL no human
	// answered stays charged (fail closed: no unbounded loop without a
	// human in it).
	class := capsule.RewindCharged
	switch {
	case gate == deliverStepID && currentChainAnswers(d.exec.Steps[gate]) > 0:
		class = capsule.RewindOwner
	case light || mech || converging:
		class = capsule.RewindUncharged
	}
	if class != capsule.RewindOwner && (countTowardTotal(d.exec.RewindStats.Rewinds) >= int64(d.workflow.MaxTotal) ||
		(class == capsule.RewindCharged && countToEdge(d.exec.RewindStats.Rewinds, gate, target) >= int64(d.workflow.MaxPerStep))) {
		d.exec.WorkflowStatus = StatusFailedRewindExhausted
		return nil
	}
	targetCause := CauseGateFailRewind
	switch {
	case light:
		targetCause = CauseGateFailRewindTextOnly
	case mech:
		targetCause = CauseGateFailRewindMechanical
	}
	d.applyRewindRangeLocked(tIdx, gIdx, target, gate, targetCause, class, info)
	// the recorded tier returns to full whenever the run
	// re-runs verification — a verification-gate FAIL (the spec's A3: a
	// verification FAIL has no passed prior run to union with, so it
	// re-verifies full) or the deliver 「全量重验」 rewind (target
	// verification). The re-run then renders the full section and a scoped
	// claim is rejected. A review FAIL keeps the tier: the re-entered
	// round re-runs review first, and the re-run verification matches the
	// scope that review recorded (its union with a `scope.prior`
	// included).
	if gate == verificationStepID || target == verificationStepID {
		d.exec.ScopeTier = tierFull
	}
	// a FRESH rewind re-derives the target from scratch, so
	// a pending light recheck its range did not reset — the marker lives
	// on verification, which a review FAIL's range [code, review) no
	// longer reaches now that review runs first — is abandoned in this
	// same write (abandonLightRecheckLocked). A light rewind keeps the
	// marker (its re-entry resumes the repair the marker describes); a
	// mechanical one has just been set below.
	if !light && !mech {
		if err := d.abandonLightRecheckLocked("the "+gate+" gate's FAIL rewound "+target+" to a fresh re-derivation", info); err != nil {
			return err
		}
	}
	d.closeGateRoundLocked(gate, out, "FAIL verdict rewound the workflow to "+target)
	// The FAIL verdict text rides to the target's pending_rewind_input so
	// every dispatch of the round sees it, until the step is accepted or
	// the next rewind/reset replaces it.
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
	if target == reproStepID {
		pri.Note = d.preFixBaseNoteLocked()
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
	return nil
}

// closeGateRoundLocked closes the human round a gate's FAIL verdict
// judged, on the gate itself: the dispatch whose verdict routed the
// workflow is archived (gate_fail_verdict, the verdict output and its
// budget window), its answer chain closes, and its re-entry starts a new
// conversation in a fresh failure and budget window. Shared by the
// rewind of applyGateFailLocked and the review owner exit's settlement.
func (d *Driver) closeGateRoundLocked(gate string, out map[string]any, detail string) {
	// The gate itself stays in_progress, but its verdict closes the human
	// round it judged: answers recorded so far must not satisfy a
	// requires_human_answer gate on re-entry (human-gate bypass).
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
		gst.AnswerBase = len(gst.Answers)
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

// applyLightEscalateLocked lands the light path's one fallback (polyforge#260,
// the in-place escalation to the existing full verification): archive one
// audit-only light_recheck_escalated attempt on the verification step
// with the reason in Detail and the spec's audit facts in Result — the
// bound and diff base the marker carried, the measured repair diff
// (when one was measurable) and the recheck's own record (when the
// recheck ran), archived in this same write because it also clears the
// marker and the full re-run's own output never carries them — clear
// the pending light recheck marker, and order the next dispatch fresh —
// no rewind slot, no rewind input, no budget reset. The loop then
// re-dispatches the step in full mode (no marker ⇒ a full dispatch) and
// its result is processed by today's rules unchanged. The escalation
// attempt is neither a rewind archive nor model-class infra — it leaves
// the candidate pointer's count alone. Two detection points share it:
// the pre-dispatch phase (a malformed classification or a broken repair
// bound, TransLightEscalate — no recheck ran, diff and recheck absent)
// and the light recheck's own settlement write (a recheck that did not
// pass).
func (d *Driver) applyLightEscalateLocked(stepID, reason string, diff *lightDiff, recheck any, info *appliedInfo) error {
	st := d.exec.Steps[stepID]
	if st == nil {
		return fmt.Errorf("driver: light escalation for unknown step %q", stepID)
	}
	if st.PendingLightRecheck == nil {
		return fmt.Errorf("driver: light escalation without a pending light recheck marker on step %q", stepID)
	}
	if isTerminalStatus(d.exec.WorkflowStatus) {
		return fmt.Errorf("driver: workflow is terminal (%s); light escalation refused", d.exec.WorkflowStatus)
	}
	if d.exec.WorkflowStatus == StatusBlocked {
		return fmt.Errorf("driver: workflow is blocked; light escalation refused")
	}
	if err := archiveLightEscalation(st, reason, diff, recheck, d.stamp(), false); err != nil {
		return err
	}
	info.escalated = true
	info.escalateReason = reason
	return nil
}

// abandonLightRecheckLocked abandons verification's pending mechanical
// light recheck in a write that opened a FRESH re-derivation of the build
// step ( G3, S1): the marker's bound and diff base describe a
// light repair that the fresh round replaces, so leaving it would make
// verification's next dispatch a light recheck over a fresh re-derivation
// — skipping the full bar and the authored scenarios. Every write that
// opens such a round calls it: a structural (non-light) gate FAIL whose
// range does not reset verification, a light re-entry's needs_code_change
// self-report (whichever light cause rewound it) and the fix's first
// root_cause_addressed: false. It lands like the light path's own fallback
// (archiveLightEscalation: the audit-only attempt carrying why and the
// marker's facts, the marker cleared, verification's next dispatch fresh)
// and queues one light_recheck_escalated event after the write's own
// rewind event. why opens the recorded reason. No marker: nothing to do.
func (d *Driver) abandonLightRecheckLocked(why string, info *appliedInfo) error {
	vst := d.exec.Steps[verificationStepID]
	if vst == nil || vst.PendingLightRecheck == nil {
		return nil
	}
	reason := why + ", abandoning the mechanical fast lane: the pending light recheck's bound and diff base describe a light repair that no longer exists"
	if err := archiveLightEscalation(vst, reason, nil, nil, d.stamp(), true); err != nil {
		return err
	}
	info.recheckAbandoned = reason
	return nil
}

// archiveLightEscalation is the escalation's own write on the step holding
// the pending light recheck marker: the audit-only light_recheck_escalated
// attempt, the marker cleared and the next dispatch ordered fresh. The
// in-place escalation (applyLightEscalateLocked) and the abandonment by a
// fresh re-derivation (abandonLightRecheckLocked,) share it;
// the audit record is marshaled before anything is mutated. abandoned
// ( S2) marks the second kind in the audit record: the
// unchanged-tree guard exempts only the in-place escalation's full re-run
// (inPlaceEscalationContext), never the full run after a fresh round.
func archiveLightEscalation(st *capsule.Step, reason string, diff *lightDiff, recheck any, now string, abandoned bool) error {
	// the audit record (the spec's complete audit trail): the marker's
	// own facts — the bound copied verbatim from the FAIL verdict's
	// classification, the FAIL-settlement diff base — plus what this
	// escalation measured and ran, the escalation flag and the reason.
	// Archived in the attempt's Result before this same write clears the
	// marker: after the escalation the full re-run's own output is the
	// only thing that changes, and it never carries these.
	var boundAny any
	_ = json.Unmarshal(st.PendingLightRecheck.Bound, &boundAny)
	audit := map[string]any{
		"bound":     boundAny,
		"diff_base": st.PendingLightRecheck.DiffBase,
		"diff":      nil,
		"recheck":   nil,
		"escalated": true,
		"abandoned": abandoned,
		"reason":    reason,
	}
	if diff != nil {
		audit["diff"] = map[string]any{"files": diff.Files, "lines": diff.Lines}
	}
	if recheck != nil {
		audit["recheck"] = recheck
	}
	raw, err := json.Marshal(audit)
	if err != nil {
		return fmt.Errorf("driver: light escalation audit record: %w", err)
	}
	st.Attempts = append(st.Attempts, capsule.Attempt{
		Cause:      CauseLightRecheckEscalated,
		AttemptSeq: nextAttemptSeq(st),
		StartedAt:  st.StepStartedAt,
		ArchivedAt: now,
		Detail:     reason,
		Result:     raw,
	})
	st.PendingLightRecheck = nil
	// the full re-run is a fresh conversation (the light one judged this
	// same FAIL; its transcript is at most read-only reference)
	if st.Supervision == nil {
		st.Supervision = &capsule.StepSupervision{}
	}
	st.Supervision.NextDispatch = capsule.NextDispatchFresh
	return nil
}

// reviewRoundFacts reads one recorded review verdict's
// blocking facts: how many findings carry severity "blocking" and how
// many of those reference their prior-round original via a non-empty
// residual_of. ok is false when the findings record is not a list of
// objects — an unparseable round proves nothing (fail closed).
func reviewRoundFacts(out map[string]any) (blocking, residuals int, ok bool) {
	if out == nil {
		return 0, 0, false
	}
	items, isList := out["findings"].([]any)
	if !isList {
		return 0, 0, false
	}
	for _, it := range items {
		m, isObj := it.(map[string]any)
		if !isObj {
			return 0, 0, false
		}
		if sev, _ := m["severity"].(string); sev != "blocking" {
			continue
		}
		// An off-graph finding rides along a mixed FAIL's rewind but is
		// not the build step's to resolve: it never counts as an
		// unresolved code finding for the convergence rule.
		if offGraphFinding(m) {
			continue
		}
		blocking++
		if ref, _ := m["residual_of"].(string); strings.TrimSpace(ref) != "" {
			residuals++
		}
	}
	return blocking, residuals, true
}

// offGraphFinding answers whether one blocking finding names an
// off-graph repair target (any known class but code). An absent or
// unknown class is never off-graph: validation rejects it before a
// verdict lands, and anything unprovable keeps the build-step route.
func offGraphFinding(m map[string]any) bool {
	c, _ := m[repairTargetKey].(string)
	return c != RepairTargetCode && validRepairTarget(c)
}

// reviewAllOffGraph answers whether one recorded review FAIL verdict is
// an ALL-OFF-GRAPH round: at least one blocking finding, and every
// blocking finding carries an off-graph repair target. A malformed
// findings record answers false (the build-step rewind holds).
func reviewAllOffGraph(out map[string]any) bool {
	items, ok := out["findings"].([]any)
	if !ok {
		return false
	}
	n := 0
	for _, it := range items {
		m, isObj := it.(map[string]any)
		if !isObj {
			return false
		}
		if sev, _ := m["severity"].(string); sev != "blocking" {
			continue
		}
		if !offGraphFinding(m) {
			return false
		}
		n++
	}
	return n > 0
}

// reviewConverging answers whether one recorded review
// FAIL verdict is a CONVERGING round: at least one blocking finding,
// every blocking finding a residual (residual_of set), strictly fewer
// than the last verdict round's blocking count, and that baseline
// exists. Anything else — a new discovery, a missing marker, a missing
// or unparseable baseline, a non-decreasing count — answers false and
// the rewind charges its edge like any fresh one (fail closed).
func reviewConverging(out map[string]any, last *capsule.ReviewRound) bool {
	if last == nil {
		return false
	}
	blocking, residuals, ok := reviewRoundFacts(out)
	if !ok || blocking == 0 || residuals != blocking {
		return false
	}
	return int64(blocking) < last.Blocking
}

// recordReviewRoundLocked writes the review step's last-verdict-round
// facts: the round ordinal, this round's blocking count
// and the heads the round judged (code review), in the settlement write of
// the verdict that closed the round. An unparseable findings record
// still writes the round with its counted blocking (zero) — a baseline
// of zero blocks every later convergence claim by arithmetic (nothing
// is strictly fewer than zero), so a malformed round never subsidizes
// the next one.
func (d *Driver) recordReviewRoundLocked(stepID string, blocking int, heads map[string]string) {
	st := d.exec.Steps[stepID]
	if st == nil {
		return
	}
	st.LastReviewRound = &capsule.ReviewRound{
		Round:    d.exec.ReviewRounds[stepID],
		Blocking: int64(blocking),
		Heads:    heads,
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

// escalatedRecheckContext answers whether a step's latest non-
// infra_failure archived attempt is the light path's in-place escalation
// (light_recheck_escalated): this dispatch is the ESCALATED full re-run
// of the mechanical fast lane (polyforge#260) — the prompt carries the
// escalation note (the report's fast-lane section duty; the light
// recheck that would have written it never settled). The order is that
// ledger fact — archived by the escalation write itself, the light
// re-entry's resume pattern — and the full re-run's own accepted
// attempt ends the context.
func escalatedRecheckContext(attempts []capsule.Attempt) bool {
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i].Cause == CauseInfraFailure {
			continue // audit-only interruptions do not change the dispatch
		}
		return attempts[i].Cause == CauseLightRecheckEscalated
	}
	return false
}

// inPlaceEscalationContext narrows escalatedRecheckContext to the light
// path's IN-PLACE escalation ( S2): the latest non-
// infra_failure attempt is light_recheck_escalated and its audit record
// does not mark it abandoned. Only that full re-run is a designed same-tree
// re-run — the unchanged-tree guard's exemption. A recheck abandoned by a
// fresh re-derivation (abandonLightRecheckLocked) archives the same cause,
// but the full run after it follows a fresh build round, so the guard
// judges it like any gate dispatch. An audit record that does not parse
// keeps the exemption (the in-place records written before the flag
// carry none).
func inPlaceEscalationContext(attempts []capsule.Attempt) bool {
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i].Cause == CauseInfraFailure {
			continue
		}
		if attempts[i].Cause != CauseLightRecheckEscalated {
			return false
		}
		var audit struct {
			Abandoned bool `json:"abandoned"`
		}
		_ = json.Unmarshal(attempts[i].Result, &audit)
		return !audit.Abandoned
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
// re-entry's rewind input (Note empty: a fresh re-derivation). The fresh
// round it opens charges the light rewind's gate→target edge exactly once
// the light rewind counted toward max_total when it
// landed but occupied no edge, so this write checks the edge bound BEFORE
// any mutation — a spent edge ends failed_rewind_exhausted, the report
// left on the step for the record — and otherwise flips the light record
// to charged. No new record and no new max_total count: a classification
// error is not charged twice. Without the charge, "light rewind →
// needs_code_change → fresh round → light FAIL → …" never occupied the
// edge and only max_total stopped it. Position unchanged (the step is the
// current one). The fresh round abandons the light path, so the same
// write also abandons verification's pending light recheck
// (abandonLightRecheckLocked; polyforge#260, S1) whichever
// light cause rewound the step — after a mechanical FAIL a text-only
// review FAIL keeps the marker, so the self-report that follows it is the
// write that opens the fresh round: the marker lives on verification,
// OUTSIDE the [target, gate) range this write archives, and a stale one
// would make verification's next dispatch a light recheck over a fresh
// re-derivation. The caller emits the standard rewind event — never
// step_done for a step that is not done.
func (d *Driver) applySelfReportRewindLocked(stepID string, st *capsule.Step, out map[string]any, info *appliedInfo) error {
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
	// The edge charge, bounds before any mutation. The
	// light record is the edge's latest: nothing else rewinds into the
	// current step between the light rewind and this result. A ledger with
	// no record of it has nothing to flip — and its count then exceeds its
	// records, so the next gate FAIL fails closed (rewind_ledger_
	// unattributable) and the loop cannot run on it.
	rewinds := d.exec.RewindStats.Rewinds
	if i := lastEdgeRecord(rewinds, gate, stepID); i >= 0 && rewinds[i].Class == capsule.RewindUncharged {
		if countToEdge(rewinds, gate, stepID) >= int64(d.workflow.MaxPerStep) {
			st.Output = out // the self-report stays on the step, for the record
			d.exec.WorkflowStatus = StatusFailedRewindExhausted
			info.selfRewindExhausted = true
			info.rewindGate = gate
			return nil
		}
		rewinds[i].Class = capsule.RewindCharged
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
	st.Candidate = "" // the era reset: the pointer returns to the live list head
	st.Output = nil
	st.PendingQuestion = nil
	st.PendingRewindInput = nil
	st.Supervision = nil
	st.AnswerBase = len(st.Answers)
	st.EnvelopeRejects = 0
	st.EnvelopeRejectSpawnN = 0
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
	return d.abandonLightRecheckLocked("`"+stepID+"`'s needs_code_change self-report opened a fresh re-derivation", info)
}

// applyRewindRangeLocked archives every step in [from, to) and resets it
// to pending with output cleared, then moves the position to the target.
// The target (index from) is archived with the targetCause the caller
// chose (the fresh gate_fail_rewind, or gate_fail_rewind_text_only on the
// light path); every later step in the range as superseded_by_rewind.
// rewindGate names the triggering gate; class is the
// record's accounting class — charged occupies its gate→target edge's
// max_per_step bound, uncharged counts toward max_total only, owner
// toward neither (capsule.RewindRecord).
func (d *Driver) applyRewindRangeLocked(from, to int, target, rewindGate, targetCause, class string, info *appliedInfo) {
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
		st.Candidate = "" // the era reset: the pointer returns to the live list head
		st.Output = nil
		st.PendingQuestion = nil
		st.PendingRewindInput = nil
		st.PendingLightRecheck = nil    // a reset step never carries a stale light recheck marker (polyforge#260)
		st.Supervision = nil            // a gate rewind into the step resets its failure ledger and relaxed bounds
		st.AnswerBase = len(st.Answers) // a rewound step starts a new attempt chain: earlier answers no longer count toward the human gate and are not handed to the re-dispatch
		st.EnvelopeRejects = 0          // the rejected envelopes belonged to the round the rewind archived
		st.EnvelopeRejectSpawnN = 0
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
func (d *Driver) applyAnswerLocked(req TransitionRequest, info *appliedInfo) error {
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
	at := d.stamp()
	st.Answers = append(st.Answers, capsule.ArchivedAnswer{
		QuestionID: q.ID,
		Answer:     req.Reason,
		AnsweredBy: by,
		AnsweredAt: at,
	})
	st.PendingQuestion = nil
	if IsCancelAnswer(req.Reason) {
		// The reserved cancel answer (authorized by the hub before it
		// reached the driver): the durable intent and the blocked park in
		// this one write; the daemon's blocked close ends the work item
		// in the hub's canceled status. No ruling, no routing.
		d.exec.CancelRequested = &capsule.CancelIntent{QuestionID: q.ID, Answer: req.Reason, AnsweredBy: by, At: at}
		d.exec.WorkflowStatus = StatusBlocked
		d.exec.BlockedReason = "cancel requested: the owner answered `cancel` to question " + q.ID + "; the work item ends in the hub's canceled status"
		info.extra = append(info.extra, appliedEvent{"blocked", map[string]any{
			"spawn_n": d.spawnN, "step": cur, "reason": d.exec.BlockedReason, "report": false,
		}})
		return nil
	}
	switch {
	case q.ID == ReviewOwnerExitQuestionID && cur == reviewStepID:
		return d.routeReviewOwnerExitLocked(st, q, req.Reason, by, at, info)
	case q.ID == ReproNotReproducedQuestionID || q.ID == RootCauseInconclusiveQuestionID:
		return d.routeDeadEndLocked(cur, st, q, req.Reason, info)
	case q.ID == E2EAllGreenQuestionID && cur == e2eAuthorStepID:
		return d.routeAllGreenLocked(st, q, req.Reason, info)
	}
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

// applyEnvelopeRejectedLocked records ONE consecutive envelope rejection
// durably on the current dispatch ( B3): the count is the
// retry bound's authority, so a restart neither resets it nor counts the
// same archived envelope twice (the ordinal it last counted is kept).
func (d *Driver) applyEnvelopeRejectedLocked(req TransitionRequest) error {
	if err := d.requireRunningStep(req.StepID); err != nil {
		return err
	}
	st := d.exec.Steps[req.StepID]
	if st.EnvelopeRejectSpawnN != req.SpawnN {
		st.EnvelopeRejects++
		st.EnvelopeRejectSpawnN = req.SpawnN
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
			if info.rootCauseQuestion || info.question {
				info.question = false // this event is the needs_input itself
				// the second root_cause_addressed: false: the
				// driver-authored owner question parks the loop
				kind = "needs_input"
				q := d.exec.Steps[req.StepID].PendingQuestion
				d.inputEventFor = q.ID
				body["question_id"], body["question"] = q.ID, q.Question
			} else if info.selfRewindExhausted {
				// the self-report's escalation found its edge spent
				//: the terminal, never a rewind or step_done
				kind = "terminal"
				body["status"] = d.exec.WorkflowStatus
				body["gate"] = info.rewindGate
			} else if info.selfRewind || info.rootCauseRewind {
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
			if info.escalated {
				// the light recheck's in-place escalation (polyforge#260): the
				// same write that would have settled it archived the escalation
				// instead — never step_done for a step that is not done
				kind = EvLightRecheckEscalated
				body["detail"] = info.escalateReason
			}
		case OutcomeGateFailed:
			if info.escalated {
				// the light recheck's own FAIL verdict escalated in place
				// (polyforge#260): one event per escalation, carrying the
				// reason (EvLightRecheckEscalated's own contract) — never a
				// rewind (the escalation charges no slot and routes no
				// verdict) and never a gate_verdict (no gate judgment landed:
				// the light recheck is not a gate dispatch).
				kind = EvLightRecheckEscalated
				body["detail"] = info.escalateReason
			} else {
				kind = "gate_verdict"
				if info.question {
					// the review owner exit: the owner is asked, nothing rewound
				} else if isTerminalStatus(d.exec.WorkflowStatus) {
					info.extra = append(info.extra, appliedEvent{"terminal", map[string]any{
						"spawn_n": d.spawnN, "status": d.exec.WorkflowStatus, "gate": req.StepID,
					}})
				} else {
					info.extra = append(info.extra, appliedEvent{"rewind", map[string]any{
						"spawn_n": d.spawnN, "gate": req.StepID, "target": info.target, "archived": info.archived,
					}})
				}
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
	case TransLightEscalate:
		// the pre-dispatch escalation (polyforge#260): one event per
		// escalation, carrying the reason — the same kind and shape the
		// settlement-side escalation emits (EvLightRecheckEscalated's
		// own contract: one event per escalation).
		kind = EvLightRecheckEscalated
		body["step"] = req.StepID
		body["detail"] = req.Reason
	case TransBlocked:
		kind = "blocked"
		body["step"] = req.StepID
		body["reason"] = req.Reason
		body["report"] = false
	case TransStepFailed:
		kind = "step_failed"
		d.failureEventsLocked(req, info, body)
	case TransStepRevision:
		kind = EvBindingRevised
		body["step"] = req.StepID
		if rev := req.BindingRevision; rev != nil {
			body["by"] = rev.By
			body["reason"] = rev.Reason
			// One durable event per corrected index: the batch is one
			// write, but a reader following the chain sees each command
			// that changed and what it became.
			for i, corr := range rev.Corrections {
				ev := map[string]any{
					"spawn_n":       d.spawnN,
					"step":          req.StepID,
					"binding_index": corr.Index,
					"before":        req.RevisionBefore[i],
					"after":         corr.Command,
					"by":            rev.By,
					"reason":        rev.Reason,
				}
				if i == 0 {
					body["binding_index"] = corr.Index
					body["before"] = req.RevisionBefore[i]
					body["after"] = corr.Command
					continue
				}
				info.extra = append(info.extra, appliedEvent{EvBindingRevised, ev})
			}
		}
	}
	if info.question {
		if st := d.exec.Steps[d.exec.CurrentStep]; st != nil && st.PendingQuestion != nil {
			d.inputEventFor = st.PendingQuestion.ID
			info.extra = append(info.extra, appliedEvent{"needs_input", map[string]any{
				"spawn_n": d.spawnN, "step": d.exec.CurrentStep,
				"question_id": st.PendingQuestion.ID, "question": st.PendingQuestion.Question,
			}})
		}
	}
	if info.recheckAbandoned != "" {
		// the abandonment of verification's pending light recheck by a
		// fresh re-derivation: after the write's own
		// rewind event, whichever write opened the round
		info.extra = append(info.extra, appliedEvent{EvLightRecheckEscalated, map[string]any{
			"spawn_n": d.spawnN, "step": verificationStepID, "detail": info.recheckAbandoned,
		}})
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

// lastEdgeRecord answers the index of the latest rewind record of the
// gate→target edge (-1 = none).
func lastEdgeRecord(rewinds []capsule.RewindRecord, gate, target string) int {
	for i := len(rewinds) - 1; i >= 0; i-- {
		if rewinds[i].Gate == gate && rewinds[i].ToStep == target {
			return i
		}
	}
	return -1
}

// countToEdge counts how many CHARGED rewind records the gate→target
// edge already carries the max_per_step bound's input.
// A light or converging rewind (charged false) counts toward max_total
// only, never toward an edge's bound — unless a light re-entry's
// needs_code_change self-report escalates it to a fresh round, which
// flips its record to charged.
func countToEdge(rewinds []capsule.RewindRecord, gate, target string) int64 {
	var n int64
	for _, r := range rewinds {
		if r.Class == capsule.RewindCharged && r.Gate == gate && r.ToStep == target {
			n++
		}
	}
	return n
}

// countTowardTotal counts the ledger records the max_total bound counts:
// every record but an owner-routed one (charged and uncharged alike —
// max_total is the automatic rewinds' runaway guard).
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

// The review owner exit: a review FAIL whose every blocking finding is
// off-graph (reviewAllOffGraph) cannot be settled by the build step, so
// the settlement write asks the owner instead of rewinding. The answer's
// own transition routes it: amend-upstream records the ruling and
// rewinds, owner-routed, to the step that executes it (the authoring
// step for a suite gap, append-only; otherwise the build step) — spec and
// plan never re-run; waive records the exemption of exactly the named
// findings and re-runs review; cancel is the universal reserved answer;
// anything else is asked again.
const (
	// ReviewOwnerExitQuestionID is the driver-authored owner question of
	// an all-off-graph review FAIL.
	ReviewOwnerExitQuestionID = "review_owner_exit"
	// The owner exit's answer verbs (the trimmed answer's first token,
	// case-insensitive, a trailing ':' ignored).
	OwnerAnswerAmend = "amend-upstream"
	OwnerAnswerWaive = "waive"
	// OwnerAmendSuiteNote rides the authoring step's rewind input of a
	// suite-gap amendment: the append-only duty.
	OwnerAmendSuiteNote = "Owner-approved suite amendment (append-only): keep every existing scenario, test and title byte-identical; append and title only the new scenarios the ruling names, prove them red, and complete with the full title list (old titles first, then the new ones) — the driver takes that list as the new baseline."
)

// ownerExitFinding is one blocking finding the exit question names.
type ownerExitFinding struct {
	Ref          string `json:"ref"`
	RepairTarget string `json:"repair_target"`
	Detail       string `json:"detail"`
}

// ownerExitContext is the exit question's driver-composed context: the
// round's blocking findings with their positional references and the
// verdict output in full.
type ownerExitContext struct {
	Round    int64              `json:"review_round"`
	Blocking []ownerExitFinding `json:"blocking"`
	Verdict  map[string]any     `json:"verdict"`
}

// ownerExitFindings lists one verdict's blocking findings with their
// positional reference s<n>#<k> (n = the verdict dispatch's ordinal,
// k = the 1-based position in its findings list).
func ownerExitFindings(out map[string]any, n int64) []ownerExitFinding {
	items, _ := out["findings"].([]any)
	var fs []ownerExitFinding
	for i, it := range items {
		m, _ := it.(map[string]any)
		if sev, _ := m["severity"].(string); sev != "blocking" {
			continue
		}
		c, _ := m[repairTargetKey].(string)
		detail, _ := m["detail"].(string)
		fs = append(fs, ownerExitFinding{Ref: fmt.Sprintf("s%d#%d", n, i+1), RepairTarget: c, Detail: detail})
	}
	return fs
}

// ownerExitQuestion renders the exit question: the findings and the
// three answers, stated so an answer is unambiguous.
func ownerExitQuestion(fs []ownerExitFinding) string {
	var b strings.Builder
	b.WriteString("The review FAILed with blocking findings that are all off-graph — the build step cannot settle them, so the driver asks you instead of rewinding it:\n")
	for _, f := range fs {
		fmt.Fprintf(&b, "- %s [%s] %s\n", f.Ref, f.RepairTarget, oneLine(f.Detail, 300))
	}
	b.WriteString("Answer with exactly one of:\n")
	b.WriteString("- `amend-upstream: <your words>` — approve the upstream correction. Your words are recorded verbatim in artifacts/grill-qa.md `## Owner rulings`; a suite gap re-runs the authoring step append-only, any other finding is executed by the build step; spec and plan are never re-run.\n")
	b.WriteString("- `waive <ref> [<ref> ...] — <your words>` — exempt exactly the findings you name by their reference (e.g. " + fs[0].Ref + "); an unnamed finding stays blocking, and a waive naming none is asked again.\n")
	b.WriteString("- `cancel` — abandon this work item.\n")
	b.WriteString("Any other answer is treated as a new question and this one is asked again.")
	return b.String()
}

// raiseReviewOwnerExitLocked parks the review gate on the owner exit in
// its FAIL settlement write: the verdict stays on the gate (in_progress),
// nothing is archived or rewound.
func (d *Driver) raiseReviewOwnerExitLocked(st *capsule.Step, out map[string]any, n int64, info *appliedInfo) {
	fs := ownerExitFindings(out, n)
	ctx, _ := json.Marshal(ownerExitContext{Round: n, Blocking: fs, Verdict: out})
	st.PendingQuestion = &capsule.PendingQuestion{
		ID:       ReviewOwnerExitQuestionID,
		Step:     reviewStepID,
		Question: ownerExitQuestion(fs),
		Context:  ctx,
		AskedAt:  d.stamp(),
	}
	info.question = true
}

// answerVerb is an owner answer's routing verb: the trimmed answer's
// first whitespace-separated token, lower-cased, a trailing ':' or ','
// dropped.
func answerVerb(answer string) string {
	f := strings.Fields(strings.TrimSpace(answer))
	if len(f) == 0 {
		return ""
	}
	return strings.TrimRight(strings.ToLower(f[0]), ":,")
}

// findingRefsIn answers the well-formed s<n>#<k> references an answer
// names, in order, de-duplicated.
func findingRefsIn(answer string) []string {
	seen := map[string]bool{}
	var refs []string
	for _, m := range findingRefScanRe.FindAllString(answer, -1) {
		if !seen[m] {
			seen[m] = true
			refs = append(refs, m)
		}
	}
	return refs
}

var findingRefScanRe = regexp.MustCompile(`s[1-9][0-9]*#[1-9][0-9]*`)

// routeReviewOwnerExitLocked routes the owner's answer to the exit, in
// the answer's own write (the pair is already archived).
func (d *Driver) routeReviewOwnerExitLocked(st *capsule.Step, q *capsule.PendingQuestion, answer, by, at string, info *appliedInfo) error {
	var ctx ownerExitContext
	_ = json.Unmarshal(q.Context, &ctx)
	reask := func(why string) error {
		st.PendingQuestion = &capsule.PendingQuestion{
			ID:       q.ID,
			Step:     q.Step,
			Question: why + "\n\n" + ownerExitQuestion(ctx.Blocking),
			Context:  q.Context,
			AskedAt:  at,
		}
		info.question = true
		return nil
	}
	if len(ctx.Blocking) == 0 {
		return reask("The driver could not read the findings this question names.")
	}
	ruling := capsule.OwnerRuling{QuestionID: q.ID, Answer: answer, AnsweredBy: by, AnsweredAt: at}
	switch answerVerb(answer) {
	case OwnerAnswerAmend:
		ruling.Kind = OwnerAnswerAmend
		target := d.workflow.step(reviewStepID).OnFail.Target
		suite := false
		for _, f := range ctx.Blocking {
			ruling.Findings = append(ruling.Findings, f.Ref)
			if f.RepairTarget == RepairTargetAuthoredSuite {
				suite = true
			}
		}
		note := ""
		if suite {
			if a := d.authoringStepBefore(reviewStepID); a != "" {
				target, note = a, OwnerAmendSuiteNote
			}
		}
		ruling.RoutedTo = target
		st.OwnerRulings = append(st.OwnerRulings, ruling)
		tIdx, _ := d.workflow.indexOf(target)
		gIdx, _ := d.workflow.indexOf(reviewStepID)
		d.applyRewindRangeLocked(tIdx, gIdx, target, reviewStepID, CauseGateFailRewind, capsule.RewindOwner, info)
		d.closeGateRoundLocked(reviewStepID, st.Output, "the owner's amend-upstream ruling routed the workflow to "+target)
		verdict, _ := json.Marshal(ctx.Verdict)
		d.exec.Steps[target].PendingRewindInput = &capsule.RewindInput{
			ToStep: target,
			Reason: "The owner answered the review owner exit with an amend-upstream ruling (recorded verbatim in artifacts/grill-qa.md `## Owner rulings`, findings " +
				strings.Join(ruling.Findings, ", ") + "). Execute it — spec and plan are not re-run; the ruling is the upstream correction:\n\n" +
				answer + "\n\nThe review verdict it answers:\n\n" + string(verdict),
			Note: note,
		}
		info.extra = append(info.extra, appliedEvent{"rewind", map[string]any{
			"spawn_n": d.spawnN, "gate": reviewStepID, "target": target, "archived": info.archived, "cause": "owner_ruling",
		}})
		return d.abandonLightRecheckLocked("the owner's amend-upstream ruling rewound "+target+" to a fresh re-derivation", info)
	case OwnerAnswerWaive:
		valid := map[string]bool{}
		for _, f := range ctx.Blocking {
			valid[f.Ref] = true
		}
		for _, r := range findingRefsIn(answer) {
			if valid[r] {
				ruling.Findings = append(ruling.Findings, r)
			}
		}
		if len(ruling.Findings) == 0 {
			return reask("Your waive named no blocking finding of this review round by its reference, so it clears nothing.")
		}
		ruling.Kind = OwnerAnswerWaive
		st.OwnerRulings = append(st.OwnerRulings, ruling)
		// review re-runs on the same tree with the waiver on record: the
		// owner's ruling is the new gate input, so the round closes here
		// and the unchanged-tree record is forgotten
		d.closeGateRoundLocked(reviewStepID, st.Output, "the owner waived "+strings.Join(ruling.Findings, ", ")+"; review re-runs")
		st.JudgedRepoHash = ""
		return nil
	}
	return reask("Your answer is not one of amend-upstream, waive or cancel.")
}

// authoringStepBefore answers the lane's authoring step before stepID —
// the step whose accepted output declares the authored test files
// (feature-full's e2e-author, bug-fix's repro) — or "" when the lane has
// none.
func (d *Driver) authoringStepBefore(stepID string) string {
	idx, _ := d.workflow.indexOf(stepID)
	for i := idx - 1; i >= 0; i-- {
		switch d.workflow.Steps[i].ID {
		case e2eAuthorStepID, reproStepID:
			return d.workflow.Steps[i].ID
		}
	}
	return ""
}

// The bug-fix dead ends: a repro that does not reproduce, and a second
// inconclusive root cause. Both park a driver-authored owner question
// with the same three answers instead of continuing or exhausting the
// rewinds.
const (
	ReproNotReproducedQuestionID    = "repro_not_reproduced"
	RootCauseInconclusiveQuestionID = "root_cause_inconclusive"
	OwnerAnswerRerunRepro           = "re-run-repro"
	OwnerAnswerContinue             = "continue"
)

// notReproducedLocked reports a completed repro result carrying the
// literal reproduces: false on a lane whose root-cause step follows it.
func (d *Driver) notReproducedLocked(stepID string, out map[string]any) bool {
	if stepID != reproStepID {
		return false
	}
	if v, ok := out["reproduces"].(bool); !ok || v {
		return false
	}
	rp, _ := d.workflow.indexOf(reproStepID)
	rc, ok := d.workflow.indexOf(rootCauseStepID)
	return ok && rp < rc
}

// deadEndQuestion renders a dead end's owner question.
func deadEndQuestion(why string) string {
	return why + " Answer with exactly one of:\n" +
		"- `re-run-repro: <evidence>` — supplement the reproduction evidence (environment, trigger, logs) and re-run `repro` with it, against the pre-fix base;\n" +
		"- `continue: <guidance>` — continue on the current diagnosis with your corrective guidance;\n" +
		"- `cancel` — abandon this work item.\n" +
		"Any other answer is treated as a new question and this one is asked again."
}

// parkDeadEndLocked parks the step on a dead end's owner question.
func (d *Driver) parkDeadEndLocked(st *capsule.Step, stepID, qid string, ctx json.RawMessage, why string, info *appliedInfo) {
	st.PendingQuestion = &capsule.PendingQuestion{
		ID: qid, Step: stepID, Question: deadEndQuestion(why), Context: ctx, AskedAt: d.stamp(),
	}
	info.question = true
}

// routeDeadEndLocked routes the owner's dead-end answer in its own write:
//   - re-run-repro: repro re-runs with the answer (in place when the
//     question sits on repro; an owner-routed rewind root-cause → repro
//     otherwise), told to reproduce against the pre-fix base;
//   - continue: on repro the held report is accepted and the lane moves
//     on to root-cause, the guidance in repro's answers; on root-cause
//     the gate re-runs in place with the guidance;
//   - anything else (cancel is the reserved answer) is asked again.
func (d *Driver) routeDeadEndLocked(cur string, st *capsule.Step, q *capsule.PendingQuestion, answer string, info *appliedInfo) error {
	switch answerVerb(answer) {
	case OwnerAnswerRerunRepro:
		reason := "The owner answered the bug-fix dead end with re-run-repro — supplement the reproduction with this evidence and re-run it:\n\n" + answer
		if cur == reproStepID {
			st.Output = nil
			st.PendingRewindInput = &capsule.RewindInput{ToStep: reproStepID, Reason: reason, Note: d.preFixBaseNoteLocked()}
			if st.Supervision == nil {
				st.Supervision = &capsule.StepSupervision{}
			}
			st.Supervision.NextDispatch = capsule.NextDispatchFresh
			return nil
		}
		tIdx, _ := d.workflow.indexOf(reproStepID)
		gIdx, _ := d.workflow.indexOf(cur)
		d.applyRewindRangeLocked(tIdx, gIdx, reproStepID, cur, CauseGateFailRewind, capsule.RewindOwner, info)
		d.closeGateRoundLocked(cur, st.Output, "the owner's re-run-repro answer routed the workflow to "+reproStepID)
		d.exec.Steps[reproStepID].PendingRewindInput = &capsule.RewindInput{ToStep: reproStepID, Reason: reason, Note: d.preFixBaseNoteLocked()}
		info.extra = append(info.extra, appliedEvent{"rewind", map[string]any{
			"spawn_n": d.spawnN, "gate": cur, "target": reproStepID, "archived": info.archived, "cause": "owner_answer",
		}})
		return d.abandonLightRecheckLocked("the owner's re-run-repro answer rewound "+reproStepID+" to a fresh re-derivation", info)
	case OwnerAnswerContinue:
		if cur == reproStepID {
			st.Status = capsule.StepDone
			st.PendingRewindInput = nil
			st.Attempts = append(st.Attempts, capsule.Attempt{
				Cause: "accepted", AttemptSeq: nextAttemptSeq(st), StartedAt: st.StepStartedAt, ArchivedAt: d.stamp(),
			})
			// root-cause reads only repro's artifacts, written before this
			// answer: the guidance rides its next dispatch's rewind input
			// verbatim (review s8#2), cleared when that round ends.
			if rc := d.exec.Steps[rootCauseStepID]; rc != nil {
				rc.PendingRewindInput = &capsule.RewindInput{
					ToStep: rootCauseStepID,
					Reason: "The owner answered `repro`'s reproduces:false dead end with continue — accept the report and diagnose with this guidance:\n\n" + answer,
				}
			}
			return nil
		}
		return nil // root-cause re-runs in place with the guidance in its answers
	}
	st.PendingQuestion = &capsule.PendingQuestion{
		ID: q.ID, Step: q.Step, Context: q.Context, AskedAt: d.stamp(),
		Question: "Your answer is not one of re-run-repro, continue or cancel.\n\n" + q.Question,
	}
	info.question = true
	return nil
}

// preFixBaseNoteLocked is the pre-fix base duty a repro re-run carries
// once a fix has run (a fix → root-cause record exists) and the base is
// recorded; "" otherwise (the tree is still the pre-fix tree).
func (d *Driver) preFixBaseNoteLocked() string {
	if len(d.exec.PreFixHeads) == 0 || countToEdge(d.exec.RewindStats.Rewinds, fixStepID, rootCauseStepID) == 0 {
		return ""
	}
	names := make([]string, 0, len(d.exec.PreFixHeads))
	for name := range d.exec.PreFixHeads {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"@"+d.exec.PreFixHeads[name])
	}
	return "Pre-fix base: a fix has already run on this tree, so reproduce against the pre-fix base " + strings.Join(parts, ", ") +
		" (`execution.json` `pre_fix_heads`; a throwaway worktree at those heads), never against the current tree that carries the fix's mitigation."
}

// The e2e-author all-green baseline: every authored scenario passed on
// the pre-implementation tree, so the spec claims behaviour the tree
// already has. Three owner answers make it completable or rewindable.
const (
	E2EAllGreenQuestionID   = "e2e_all_green"
	OwnerAnswerRewindToSpec = "rewind-to-spec"
	OwnerAnswerSkip         = "skip"
	specStepID              = "spec"
)

// allGreenBaseline reports an e2e-author output whose baseline lists at
// least one scenario and none of them failing.
func allGreenBaseline(out map[string]any) bool {
	rows, _ := out["baseline"].([]any)
	if len(rows) == 0 {
		return false
	}
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if st, _ := m["status"].(string); st != "pass" {
			return false
		}
	}
	return true
}

func allGreenQuestion() string {
	return "`e2e-author`'s authored scenarios all pass on the pre-implementation tree: the behaviour the spec claims is new is already there, so the spec and the tree disagree. Answer with exactly one of:\n" +
		"- `rewind-to-spec: <your words>` — the spec is corrected: the driver rewinds to `spec` (owner-routed) with your words;\n" +
		"- `skip: <your rationale>` — the behaviour is already on main: `e2e-author` records your rationale verbatim in artifacts/grill-qa.md and completes with no authored scenarios (the named discipline-① exception);\n" +
		"- `cancel` — abandon this work item.\n" +
		"Any other answer is treated as a new question and this one is asked again."
}

// routeAllGreenLocked routes the owner's all-green answer in its own
// write: rewind-to-spec is an owner-routed rewind of [spec, e2e-author];
// skip re-dispatches e2e-author fresh with the answer (it records the
// rationale and completes empty); anything else is asked again.
func (d *Driver) routeAllGreenLocked(st *capsule.Step, q *capsule.PendingQuestion, answer string, info *appliedInfo) error {
	switch answerVerb(answer) {
	case OwnerAnswerRewindToSpec:
		if sIdx, ok := d.workflow.indexOf(specStepID); ok {
			eIdx, _ := d.workflow.indexOf(e2eAuthorStepID)
			d.applyRewindRangeLocked(sIdx, eIdx+1, specStepID, e2eAuthorStepID, CauseGateFailRewind, capsule.RewindOwner, info)
			d.exec.Steps[specStepID].PendingRewindInput = &capsule.RewindInput{
				ToStep: specStepID,
				Reason: "The owner answered `e2e-author`'s all-green baseline with rewind-to-spec: every authored scenario already passed on the pre-implementation tree, so the spec claims behaviour the tree already has. Correct the spec. The owner's words:\n\n" +
					answer + "\n\nThe green baseline:\n\n" + string(q.Context),
			}
			info.extra = append(info.extra, appliedEvent{"rewind", map[string]any{
				"spawn_n": d.spawnN, "gate": e2eAuthorStepID, "target": specStepID, "archived": info.archived, "cause": "owner_answer",
			}})
			return d.abandonLightRecheckLocked("the owner's rewind-to-spec answer rewound "+specStepID+" to a fresh re-derivation", info)
		}
	case OwnerAnswerSkip:
		st.Output = nil
		if st.Supervision == nil {
			st.Supervision = &capsule.StepSupervision{}
		}
		st.Supervision.NextDispatch = capsule.NextDispatchFresh
		return nil
	}
	st.PendingQuestion = &capsule.PendingQuestion{
		ID: q.ID, Step: q.Step, Context: q.Context, AskedAt: d.stamp(),
		Question: "Your answer is not one of rewind-to-spec, skip or cancel.\n\n" + allGreenQuestion(),
	}
	info.question = true
	return nil
}

// CancelAnswerSentence is the reserved cancel answer's one statement,
// appended by the driver to every question it persists.
const CancelAnswerSentence = "Answer `cancel` (the whole answer, ignoring case and surrounding whitespace) to abandon this work item."

// IsCancelAnswer reports the reserved cancel answer: the answer text,
// trimmed of surrounding whitespace, equal to "cancel" ignoring case.
// The daemon authorizes it at the hub before relaying it.
func IsCancelAnswer(answer string) bool {
	return strings.EqualFold(strings.TrimSpace(answer), "cancel")
}
