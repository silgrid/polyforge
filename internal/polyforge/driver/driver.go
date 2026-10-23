// Package driver implements the polyforge v2 execution driver: the Go
// replacement for the LLM-driven WI Agent inside the daemon. The driver
// reads runtime-state/execution.json to determine the current state,
// spawns step processes (harness sessions) directly, validates step
// results against the minimal result envelope (outcome + output, plus
// the V4 command-evidence gate; no hashes, no manifest), writes
// execution.json exclusively through the single transition entry point
// (transition.go), and advances the workflow on validated results.
//
// Execution semantics are the frozen v1 workflow contract
// (docs/design/polyforge-vnext/01-workflow-skills.md §6): the
// sequential success path, bounded on_fail rewind on business gate FAIL
// (the gate itself never archived nor reset — INV-1d/INV-1g), in-place
// resume on infrastructure failure (the attempt archive and candidate
// advance belong to the daemon's recovery integration; the core loop
// re-dispatches from the recomputed candidate), incomplete → blocked
// with the owner holding every next move, and §6.5's explicit
// all-done-versus-failed-terminal distinction. execution.json stays the
// single execution authority (INV-1) and the observe lane keeps
// consuming it unchanged: every transition bumps Version, which is the
// observe C3 commit-boundary engine's signal.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/crashpoint"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// WIInfo identifies the work item one Driver drives.
type WIInfo struct {
	ID         string
	CapsuleDir string
	BundleName string
	// SessionHandle is the owning session's PUBLIC fingerprint (B3,
	// never the secret): it names each dispatch's spawn token and
	// capsule transcript.
	SessionHandle string
}

// EventLogger is the driver's event emission face — the shape of
// observe.EventLog.Append, so the daemon binds its durable events log
// directly. Events are news, never state (the observe contract): a
// failing logger is reported on stderr and never fails a committed
// transition.
type EventLogger interface {
	Append(wi, kind string, body any) error
}

// Config is the daemon-supplied driver configuration.
type Config struct {
	WI WIInfo
	// BundleDir is the materialized workflow bundle root (workflow.md
	// lives there). Default: <CapsuleDir>/workflows/<BundleName>.
	BundleDir string
	// ExecPath is the execution.json path. Default:
	// <CapsuleDir>/runtime-state/execution.json (capsule.ExecutionJSON).
	ExecPath string
	// Workflow supplies the execution graph directly (tests, or a
	// daemon that already parsed it); nil → loaded from BundleDir.
	Workflow *Workflow
	// StepCandidates is the per-step candidate lists of this claim
	// (polyforge#79): composed once at claim time over every precedence
	// level, the bundle's own lists included (workspace.ComposeCandidates),
	// and rehydrated from the job record at recovery. It is the driver's
	// only candidate source: a step without an entry, or with an empty
	// one, has no switchable chain and runs on the harness default. Every
	// candidate is `<harness>:<model>` (polyforge#175): the prefix is the
	// harness that candidate's dispatch runs on (New refuses any other
	// shape).
	StepCandidates map[string][]string
	// Interrupted, when set, archives ONE infra_failure attempt on the
	// current in_progress step before the loop re-dispatches (the
	// recovery integration's write, performed by the single
	// execution.json writer). Nil on a fresh start — a claim never
	// archives.
	Interrupted *InterruptedDispatch
	// Spawner derives the step processes. Required.
	Spawner StepSpawner
	// StepEnded runs after each dispatch's process is gone, before its
	// result or failure is written (production: the task-repo WIP push).
	// It never fails the step; nil = nothing to run.
	StepEnded func(context.Context)
	// SpawnN seeds the per-WI monotonic derivation counter (B2): the
	// same sources the runtime uses — state.json's spawn_n, or the
	// capsule spawn-journal max across a fresh clone. Each step process
	// the driver derives is one derivation; the counter advances with
	// every durable step_start and is the transition fence key.
	SpawnN int64
	// Log receives progress events; nil disables emission. Production
	// binds the daemon's per-WI event log (durable, monotonic ids).
	Log EventLogger
	// Link is the per-WI driver <-> supervisor channel (supervise
	// package); nil = unsupervised (no facts out, no verdicts in).
	Link *supervise.Link
	// Limits are the global supervision defaults (X, Y, T); step
	// overrides in workflow.md apply on top. Zero fields take the
	// documented defaults.
	Limits supervise.Defaults
	// MaxStepFailures is the per-step failure counter bound (default 3).
	MaxStepFailures int
	// Now overrides the clock (tests).
	Now func() time.Time
	// Sleep overrides the driver's waits (the D7 rate-limit backoff);
	// nil = a real timer cut short by the context. Tests inject a
	// recorder so the backoff curve is asserted without the wall time.
	Sleep func(ctx context.Context, d time.Duration)
}

// hasUnsettledDispatch reports whether the current step awaits a
// dispatch that never settled.
func (d *Driver) hasUnsettledDispatch() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.unsettledStepLocked() != nil
}

// InterruptedDispatch records one derivation that ended without a
// result: the recovery cause. The driver archives it as an audit-only
// infra_failure attempt (never model-class, so never a candidate
// advance) when the recovery lane re-derives.
type InterruptedDispatch struct {
	Detail string
	// BookWall books the interrupted dispatch's wall time toward the
	// step's T in the same archive write: set for a relaunch the system
	// ordered after a hang, stall or death, never for a daemon restart
	// or an operator resume (the daemon decides from the recovery cause).
	BookWall bool
}

// Driver is the per-WI execution driver. One Driver per work item; Run
// is the only loop and is not re-entrant — the daemon serializes
// starts, and the spawn fence plus the transition mutex keep any stray
// concurrent writer honest anyway.
type Driver struct {
	wi       WIInfo
	workflow *Workflow
	exec     *capsule.Execution
	execPath string

	mu     sync.Mutex // serializes every transition (the single write entry)
	spawnN int64
	// initialized records that execution.json exists on disk (loaded,
	// or written by the init transition).
	initialized bool

	cfg Config
	log EventLogger
	// failed is set when a transition's persist failed: memory may be
	// ahead of execution.json. Every further transition answers it and
	// Run returns it; the restart reloads the file (transition.go).
	failed error

	// wake carries the "state changed, re-plan" signal to a Run loop
	// parked on an open question; one buffered slot, coalescing.
	wake chan struct{}

	// rejects/priorRejection track per-step envelope-rejection state for
	// the in-process re-dispatch lane (the #97/#107 lessons): the count
	// bounds the retries, the string rides the next dispatch's prompt as
	// visible feedback so the author can fix the actual defect.
	rejects        map[string]int
	priorRejection map[string]string
	// unbooked is the wall time of finished dispatches not yet written
	// into the step's cumulative runtime (the T accumulator): folded into
	// the next result/failure write.
	unbooked map[string]int64
	// waitingAnnounced is the question id step_waiting_human was last
	// sent for (one fact per question, however often the loop re-plans).
	waitingAnnounced string
	// inputEventFor is the question id a needs_input event was last
	// emitted for in this driver life (the park transition emits it; a
	// restarted driver re-announces a question it finds parked).
	inputEventFor string

	// bundleDir is the materialized bundle root the step definitions
	// (and their declared output schemas) are read from; "" = none.
	bundleDir string
	// schemas caches each step's declared output schema (nil entry =
	// none declared or unloadable), loaded on first validation.
	schemas map[string]*stepOutputSchema
}

// New builds a Driver: it resolves paths, loads the workflow graph from
// the materialized bundle (or takes the supplied one), and loads
// execution.json — a missing file is a fresh capsule (the skeleton is
// built in memory; Run's init transition persists it), a torn one is a
// hard error (never guess execution state). Step records are ensured
// for every workflow step, and any recorded step outside the workflow
// is refused: the bundle digest pins the graph, so a divergence means
// the wrong bundle.
func New(cfg Config) (*Driver, error) {
	if cfg.WI.ID == "" {
		return nil, fmt.Errorf("driver: config without a work item id")
	}
	if cfg.WI.CapsuleDir == "" {
		return nil, fmt.Errorf("driver: config without a capsule dir")
	}
	if cfg.Spawner == nil {
		return nil, fmt.Errorf("driver: config without a spawner")
	}
	execPath := cfg.ExecPath
	if execPath == "" {
		execPath = filepath.Join(cfg.WI.CapsuleDir, capsule.ExecutionJSON)
	}
	wf := cfg.Workflow
	bundleDir := cfg.BundleDir
	if bundleDir == "" && cfg.WI.BundleName != "" {
		bundleDir = filepath.Join(cfg.WI.CapsuleDir, capsule.DirWorkflows, cfg.WI.BundleName)
	}
	if wf == nil {
		if bundleDir == "" {
			return nil, fmt.Errorf("driver: config without a bundle name or dir")
		}
		loaded, err := LoadWorkflow(bundleDir)
		if err != nil {
			return nil, err
		}
		wf = loaded
	}
	if len(wf.Steps) == 0 {
		return nil, fmt.Errorf("driver: workflow has no steps")
	}
	if wf.MaxTotal <= 0 {
		wf.MaxTotal = defaultMaxRewindTotal
	}
	if wf.MaxPerStep <= 0 {
		wf.MaxPerStep = defaultMaxRewindPerStep
	}

	// polyforge#175: every candidate a step can walk must name its
	// harness; refuse up front rather than mid-chain.
	for _, spec := range wf.Steps {
		for i, c := range cfg.StepCandidates[spec.ID] {
			if _, err := harness.ParseCandidate(c); err != nil {
				return nil, fmt.Errorf("driver: step %s: models[%d] (the claim-time list: yaml, bundle or pf submit --models): %w", spec.ID, i, err)
			}
		}
	}

	exec, existed, err := loadExecution(execPath, cfg.WI, wf)
	if err != nil {
		return nil, err
	}
	// The step_start write lands before its process exists and the spawn
	// journal line only after: a crash between the two leaves the
	// dispatch's ordinal in execution.json alone. The seed never goes
	// below it, so a re-dispatch never reuses the ordinal (and drop path)
	// of a dispatch that ran.
	spawnN := cfg.SpawnN
	if st := exec.Steps[exec.CurrentStep]; st != nil && st.Dispatch > spawnN {
		spawnN = st.Dispatch
	}
	return &Driver{
		wi:             cfg.WI,
		workflow:       wf,
		exec:           exec,
		execPath:       execPath,
		spawnN:         spawnN,
		initialized:    existed,
		cfg:            cfg,
		log:            cfg.Log,
		wake:           make(chan struct{}, 1),
		rejects:        make(map[string]int),
		priorRejection: make(map[string]string),
		unbooked:       make(map[string]int64),
		bundleDir:      bundleDir,
		schemas:        make(map[string]*stepOutputSchema),
	}, nil
}

// Run is the main loop (01 §6.5): determine the current step, start it
// if it is not already in progress, spawn its process, wait for the
// result, validate, transition, and repeat — until the workflow reaches
// a terminal state (nil return), parks blocked on an incomplete result
// (nil return: the owner holds the next move; the durable file says
// why), or an infrastructure failure surfaces (non-nil error: spawn
// failure, persist failure, rejected result, context cancellation — the
// daemon's recovery lane owns re-derivation).
//
// Crash safety: the step_start write lands BEFORE the process exists,
// so a crash between them leaves an in_progress step that this loop's
// resume path re-dispatches on the next Run. A crash after the
// dispatch's envelope was collected but before its result write is
// settled from the collected envelope (unconsumedResult): the step is
// not run again. A crash while a question
// is open is equally recoverable: the pending question is durable, the
// re-invoked loop parks on it again, and Answer() delivers across the
// restart.
func (d *Driver) Run(ctx context.Context) error {
	if !d.isInitialized() {
		if err := d.transition(TransitionRequest{Kind: TransInit, SpawnN: d.currentSpawnN()}); err != nil {
			return fmt.Errorf("driver: execution init failed: %w", err)
		}
	}
	// polyforge#79 era: the recovery integration's interrupted-dispatch
	// archive (the recovery lane triggers it; the single execution.json
	// writer performs it). ONE audit-only infra_failure attempt for the
	// derivation that died without a result, BEFORE the re-derivation
	// dispatches. It is not model-class, so it never moves the candidate
	// pointer; an operator shutdown never reaches this hook (the daemon
	// filters it).
	collected, collectedN := d.unconsumedResult()
	if collected == nil && d.cfg.Interrupted == nil && d.hasUnsettledDispatch() {
		// A claim over a dispatch that never settled — an operator pause
		// cancelled it, or a shutdown or crash ended it before this claim:
		// that time is never booked, even by a later system relaunch.
		if err := d.transition(TransitionRequest{Kind: TransClaimReentry, SpawnN: d.currentSpawnN()}); err != nil {
			return fmt.Errorf("driver: claim re-entry write failed: %w", err)
		}
	}
	if collected == nil && d.cfg.Interrupted != nil {
		if err := d.transition(TransitionRequest{
			Kind:     TransInterrupted,
			Reason:   d.cfg.Interrupted.Detail,
			BookWall: d.cfg.Interrupted.BookWall,
			SpawnN:   d.currentSpawnN(),
		}); err != nil {
			return fmt.Errorf("driver: interrupted-dispatch archive failed: %w", err)
		}
	}
	for {
		if d.takeStalled() != nil {
			return ErrDriverStalled // the supervisor's needs_recovery records the stall
		}
		if collected != nil {
			res := collected
			collected = nil
			if stop, err := d.settle(res.StepID, collectedN, res, nil); stop {
				return err
			}
			continue
		}
		stop, stepID, err := d.plan(ctx)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}

		spawn, n, err := d.prepareDispatch(stepID)
		if err != nil {
			return err
		}
		if spawn == nil {
			continue // an open question is parked; plan again once answered
		}

		if err := d.transition(TransitionRequest{
			Kind:   TransStepStart,
			StepID: stepID,
			Model:  spawn.Model,
			Effort: spawn.Effort,
			SpawnN: n,
		}); err != nil {
			return fmt.Errorf("driver: step %s start failed: %w", stepID, err)
		}

		dispatchedAt := d.now()
		run, err := d.cfg.Spawner.SpawnStep(ctx, *spawn)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("driver: step %s spawn %d failed: %w", stepID, n, err)
			}
			// The spawn itself failed (harness binary missing, journal or
			// prompt I/O, ...): a dispatch that never produced a process is
			// a failed dispatch like any other — classified, counted,
			// retried or blocked.
			cause, code, detail := classifyWaitFailure(fmt.Errorf("driver: step %s spawn %d failed: %w", stepID, n, err))
			if stop, err := d.failDispatch(ctx, stepID, n, dispatchedAt, cause, code, detail); stop {
				return err
			}
			continue
		}
		d.announceStart(spawn, run, dispatchedAt)
		res, werr, verdict := d.awaitDispatch(ctx, run, stepID, n)
		if d.cfg.StepEnded != nil && ctx.Err() == nil {
			d.cfg.StepEnded(ctx)
		}
		if verdict != nil {
			// The supervisor judged the dispatch dead. The process is
			// already torn down; this is the dispatch's one failure record.
			var ed *EnvelopeDefectError
			if errors.As(werr, &ed) {
				// the step DID write an envelope: the retry prompt
				// names the defect instead of "you wrote no envelope"
				d.mu.Lock()
				d.priorRejection[stepID] = fmt.Sprintf("dispatch %d: %v", n, ed.Defect)
				d.mu.Unlock()
			}
			d.emit("step_dead", map[string]any{
				"step": stepID, "spawn_n": n, "cause": verdict.Cause, "evidence": verdict.Evidence,
			})
			if stop, err := d.failDispatch(ctx, stepID, n, dispatchedAt, verdictCause(*verdict), -1, verdict.Evidence); stop {
				return err
			}
			continue
		}
		if werr != nil {
			_ = run.Terminate(5 * time.Second)
			if ctx.Err() != nil {
				// Cancellation (pause, recovery relaunch): not a failure of
				// the step — nothing is counted, the candidate pointer stays
				// and this driver writes nothing. A dispatch the system
				// relaunches after a hang, stall or death is booked by the
				// successor's interrupted archive (Config.Interrupted); a
				// paused one books nothing.
				d.announceEnd(stepID, n, supervise.OutcomeExitedWithoutResult, -1, "cancelled")
				return fmt.Errorf("driver: step %s dispatch %d ended without a result: %w", stepID, n, werr)
			}
			cause, code, detail := classifyWaitFailure(werr)
			if stop, err := d.failDispatch(ctx, stepID, n, dispatchedAt, cause, code, detail); stop {
				return err
			}
			continue
		}
		d.announceEnd(stepID, n, supervise.OutcomeResult, 0, "")
		d.addUnbooked(stepID, dispatchedAt)
		crashpoint.At("driver.collected:" + stepID)
		if stop, err := d.settle(stepID, n, res, run); stop {
			return err
		}
	}
}

// unconsumedResult answers the envelope of the current dispatch that a
// dead predecessor collected — archived under the step's s<n>/ or still
// at the drop path — but never settled: the current step is in_progress
// and awaits exactly the current ordinal (Step.Dispatch). Nil when there
// is none; the loop then dispatches as usual.
func (d *Driver) unconsumedResult() (*StepResult, int64) {
	d.mu.Lock()
	n := d.spawnN
	stepID := d.exec.CurrentStep
	st := d.exec.Steps[stepID]
	awaiting := d.exec.WorkflowStatus == StatusRunning && st != nil && st.Status == capsule.StepInProgress &&
		st.PendingQuestion == nil && n > 0 && st.Dispatch == n
	d.mu.Unlock()
	if !awaiting || d.wi.CapsuleDir == "" {
		return nil, 0
	}
	r := &stepRun{capsuleRoot: d.wi.CapsuleDir, wiID: d.wi.ID, sp: StepSpawn{StepID: stepID, WIID: d.wi.ID, SpawnN: n}}
	r.dropPath = r.archivePath()
	if res, _ := r.collect(); res != nil {
		return res, n
	}
	r.dropPath = PendingResultPath(d.wi.CapsuleDir, n)
	res, _ := r.collect()
	if res == nil || r.archive(res) != nil {
		return nil, 0
	}
	_ = os.Remove(r.dropPath)
	return res, n
}

// settle validates and accepts one collected result envelope of
// dispatch n (run is nil for an envelope a dead predecessor collected:
// there is no process to tear down). stop reports that Run must return
// err; otherwise the loop plans again.
func (d *Driver) settle(stepID string, n int64, res *StepResult, run StepRun) (stop bool, err error) {
	delivered := res.Outcome
	if verr := d.validateResultStrict(res, stepID, n); verr != nil {
		// Envelope-authoring errors are STEP-CONTENT failures, not
		// infrastructure (the #97/#107 lessons: a hand-transcribed digest
		// typo, a missing handoff digest — each used to cost a whole driver
		// restart through the recovery lane, and two rejections inside
		// the storm window bounded-failed the WI outright). The driver
		// re-dispatches the step in-process with the rejection as visible
		// feedback, bounded by maxEnvelopeRejects; only a persistently
		// broken author escalates to the recovery lane.
		if run != nil {
			_ = run.Terminate(5 * time.Second)
		}
		if d.log != nil {
			_ = d.log.Append(d.wi.ID, EvEnvelopeRejected, map[string]any{
				"step": stepID, "spawn_n": n, "detail": verr.Error(),
			})
		}
		d.mu.Lock()
		d.rejects[stepID]++
		count := d.rejects[stepID]
		feedback := fmt.Sprintf("dispatch %d: %v", n, verr)
		if delivered == OutcomeNeedsInput {
			// Keep the pending-question context across the retry: the
			// rejected envelope was a question, and the rewrite must
			// still be one.
			_, q, _ := needsInputQuestion(res)
			feedback += "\nThe rejected envelope was a needs_input (question) report"
			if q != "" {
				feedback += fmt.Sprintf(" asking: %q", q)
			}
			feedback += ". Rewrite it as a needs_input envelope carrying both question_id and question (envelope level or in the output object); do not turn it into a completed result."
		}
		d.priorRejection[stepID] = feedback
		d.mu.Unlock()
		if count > maxEnvelopeRejects {
			d.emit("schema_fault", map[string]any{
				"step": stepID, "spawn_n": n, "rejections": count,
				"detail": "persistent envelope rejection: the result contract is broken, not the model; reported to the owner",
			})
			// the in-process retry bound is spent: the step goes BLOCKED
			// with the result_invalid cause code — no driver relaunch
			// through the recovery lane, no on_fail routing (rewind stays
			// reserved for review FAIL verdicts).
			// The death-cause table always blocks result_invalid, so a
			// landed write is a clean stop: not relaunchable.
			if err := d.transition(TransitionRequest{
				Kind: TransStepFailed, StepID: stepID, SpawnN: n,
				Failure: &StepFailure{Cause: FailResultInvalid, Detail: verr.Error()},
			}); err != nil {
				return true, fmt.Errorf("driver: step %s result-invalid block failed: %w", stepID, err)
			}
			return true, nil
		}
		return false, nil // in-process re-dispatch: the step is still in_progress
	}
	if res.Outcome != delivered && d.log != nil {
		// The verdict binding re-classified the envelope (the #106
		// lesson): a gate's completed envelope carrying a fail verdict
		// routes as the gate-fail report it claims to be. News, not
		// state — the delivered envelope stays archived verbatim, the
		// rewind itself is written by the result transition below.
		verdict, _ := failVerdictInOutput(res.Output)
		_ = d.log.Append(d.wi.ID, EvGateVerdictBound, map[string]any{
			"step": stepID, "spawn_n": n, "verdict": verdict,
			"delivered_outcome": delivered, "bound_outcome": res.Outcome,
		})
	}
	d.mu.Lock()
	delete(d.rejects, stepID)
	delete(d.priorRejection, stepID)
	d.mu.Unlock()
	if err := d.transition(TransitionRequest{
		Kind:                    TransStepResult,
		StepID:                  stepID,
		Result:                  res,
		SpawnN:                  n,
		WallMs:                  d.takeUnbooked(stepID),
		TestFilesCommit:         d.testFilesStamp(res),
		RebaselineAuthoredTests: res.rebaseline,
		VerifiedHeads:           d.verifiedHeads(stepID, res),
		LightDiffBase:           d.lightDiffBase(stepID, res),
	}); err != nil {
		return true, fmt.Errorf("driver: step %s result transition failed: %w", stepID, err)
	}
	crashpoint.At("driver.settled:" + stepID)
	d.noteUnaskedHumanGate(stepID, n, res)
	d.noteAcceptedResultRecords(stepID, n, res)

	// completed: §6.5 step 2, next selection; gate_failed: §6.5 step 3,
	// the same write applied the rewind or terminal policy; needs_input:
	// the plan pass parks on the persisted question until answered;
	// incomplete: §6.5 step 5, blocked is durable and the loop exits.
	return res.Outcome == OutcomeIncomplete, nil
}

// plan is one selection pass: terminal or blocked workflows stop the
// loop (nil error); a current step holding an open question parks the
// loop until its answer (or another transition) wakes it; otherwise the
// earliest non-done step (next_step_by_order, 01 §6.5) is dispatched —
// and an empty selection over a running workflow is the §6.5 step 6
// check: every step done, so the loop writes completed and stops.
func (d *Driver) plan(ctx context.Context) (stop bool, stepID string, err error) {
	for {
		d.mu.Lock()
		if d.failed != nil {
			err := d.failed
			d.mu.Unlock()
			return false, "", err
		}
		status := d.exec.WorkflowStatus
		stop = isTerminalStatus(status) || status == StatusBlocked
		var pending bool
		if !stop && d.exec.CurrentStep != "" {
			if st := d.exec.Steps[d.exec.CurrentStep]; st != nil {
				pending = st.PendingQuestion != nil
			}
		}
		var next string
		if !stop && !pending {
			for _, spec := range d.workflow.Steps {
				st := d.exec.Steps[spec.ID]
				if st == nil || st.Status != capsule.StepDone {
					next = spec.ID
					break
				}
			}
		}
		spawnN := d.spawnN
		d.mu.Unlock()

		if stop {
			return true, "", nil
		}
		if pending {
			// no step process exists while a human is asked: the
			// supervisor runs no dispatch timer and no stall clock
			d.announceWaiting()
			if err := d.awaitAnswer(ctx); err != nil {
				return false, "", err
			}
			continue // re-plan: answered (or otherwise woken)
		}
		if next == "" {
			// §6.5 step 6: not a failure terminal (that exits above),
			// so an empty selection means every step is done.
			if err := d.transition(TransitionRequest{
				Kind:   TransTerminal,
				Status: StatusCompleted,
				SpawnN: spawnN,
			}); err != nil {
				return false, "", fmt.Errorf("driver: workflow completion transition failed: %w", err)
			}
			return true, "", nil
		}
		return false, next, nil
	}
}

// prepareDispatch snapshots everything the spawn needs from under the
// mutex: the resume flag (an in_progress step is a re-dispatch — answer
// continuation, crash recovery, recovery-lane re-derivation; a pending
// light-rewind target is the light re-entry, its resume derived from
// the ledger), the rewind verdict about to be consumed by the start
// write (it must reach the dispatched process's prompt inputs), the
// step's archived answers (a resumed step continues with its Q&A
// history), and the effective model candidate. Parking on an open
// question is plan()'s decision — a pending question can only exist on
// the current in_progress step, which plan sees before dispatching here.
func (d *Driver) prepareDispatch(stepID string) (*StepSpawn, int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.exec.Steps[stepID]
	sp := &StepSpawn{
		StepID: stepID,
		WIID:   d.wi.ID,
		Resume: st != nil && st.Status == capsule.StepInProgress,
	}
	if st != nil {
		sp.RewindInput = st.PendingRewindInput
		if len(st.Answers) > 0 {
			sp.Answers = append([]capsule.ArchivedAnswer(nil), st.Answers...)
		}
		if sp.Resume && st.Supervision != nil && st.Supervision.NextDispatch == capsule.NextDispatchFresh {
			sp.Fresh = true
			sp.Resume = false
		}
		// The light re-entry (polyforge#222): a PENDING step — a gate
		// FAIL's rewound target — whose ledger's latest non-infra_failure
		// archived attempt is the light rewind re-enters as a resume of
		// its previous dispatch's conversation. The order is that ledger
		// fact — the same single gate-FAIL write that routed the verdict
		// archived it, and the same derivation the needs_code_change
		// self-report validation reads (one fact, one place): nothing to
		// write, nothing to consume, equally durable. SpawnStep's existing
		// same-harness journal lookup decides whether the resume is
		// possible and degrades to fresh-with-reference when it is not.
		if !sp.Resume && st.Status == capsule.StepPending && lightRewindContext(st.Attempts) {
			sp.Resume = true
		}
	}
	// the in-process rejection retry lane: the prior dispatch's
	// validation error rides the prompt as visible feedback
	sp.PriorRejection = d.priorRejection[stepID]
	if sp.PriorRejection == "" && st != nil && len(st.Attempts) > 0 && st.Attempts[len(st.Attempts)-1].FailureCause == FailEndedWithoutResult {
		sp.PriorEndedWithoutResult = true
	}
	// the candidate names the harness its dispatch runs on (polyforge#175);
	// no list in effect runs pi's own default model. A candidate on
	// another harness than the previous dispatch's starts a fresh
	// session with the old transcript as reference (spawn adapter).
	sp.Harness = harness.Pi
	if spec := d.workflow.step(stepID); spec != nil {
		if c, err := harness.ParseCandidate(d.modelForLocked(stepID)); err == nil {
			sp.Harness, sp.Model, sp.Effort = c.Harness, c.Model, c.Effort
		}
	}
	sp.Done = map[string]bool{}
	for id, s := range d.exec.Steps {
		if s != nil && s.Status == capsule.StepDone {
			sp.Done[id] = true
		}
	}
	sp.SpawnN = d.spawnN + 1
	return sp, sp.SpawnN, nil
}

// awaitAnswer parks the loop while the current step holds an open
// question. Every successful transition pushes a wake, so the wait
// re-plans on the answer, an operator terminate, or a terminal —
// and exits promptly on ctx.
func (d *Driver) awaitAnswer(ctx context.Context) error {
	for {
		d.mu.Lock()
		status := d.exec.WorkflowStatus
		var pending bool
		if !isTerminalStatus(status) && status != StatusBlocked && d.exec.CurrentStep != "" {
			if st := d.exec.Steps[d.exec.CurrentStep]; st != nil {
				pending = st.PendingQuestion != nil
			}
		}
		failed := d.failed != nil
		d.mu.Unlock()
		if !pending || failed {
			return nil // plan re-reads the state (and answers a failed driver)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-d.wake:
		}
	}
}

// Answer delivers one owner answer for the current step's open
// question: the D9 consumption write (archive + clear in one atomic
// transition), then the loop's wake re-plans and re-dispatches the step
// in place with the answer carried in its spawn. A question-id
// mismatch, or no open question at all, is refused — the same binding
// the daemon's answer relay enforces on its side.
func (d *Driver) Answer(questionID, answer, answeredBy string) error {
	step := ""
	d.mu.Lock()
	step = d.exec.CurrentStep
	d.mu.Unlock()
	if err := d.transition(TransitionRequest{
		Kind:       TransAnswer,
		QuestionID: questionID,
		Reason:     answer,
		AnsweredBy: answeredBy,
		SpawnN:     d.currentSpawnN(),
	}); err != nil {
		return err
	}
	// the human answered: the driver is between steps again (the
	// supervisor's stall clock restarts from here)
	d.mu.Lock()
	d.waitingAnnounced = ""
	d.mu.Unlock()
	d.fact(supervise.StepAnswered{Step: step, SpawnN: d.currentSpawnN(), QuestionID: questionID})
	return nil
}

// ResumeCurrent is the owner's third §6.4 verdict after an incomplete
// result: write running back over the parked workflow (reference.md §7.3: the
// owner's follow-up writes running back) and wake the loop, whose own
// dispatch re-dispatches the frozen in_progress current step in place
// as a fresh derivation. Only a blocked workflow accepts it — while
// running, the loop owns starts, and a stray resume would race the
// loop's own dispatch ordinal.
//
// The verdict consumes NO derivation ordinal (polyforge#116): the fresh
// ordinal is the loop's own step_start write on that dispatch, so the
// spawn ledger records exactly one derivation per dispatch that ran —
// the re-dispatch the owner asked for lands at the next ordinal with no
// phantom gap. The verdict alone re-dispatches nothing: it is a state
// write plus a wake; the caller re-invokes Run (the claim/start path
// does: Start → ResumeCurrent → Run).
func (d *Driver) ResumeCurrent() error {
	d.mu.Lock()
	blocked := d.exec.WorkflowStatus == StatusBlocked
	d.mu.Unlock()
	if !blocked {
		return fmt.Errorf("driver: resume is the owner's blocked-step verdict; workflow is %q", d.Snapshot().WorkflowStatus)
	}
	return d.transition(TransitionRequest{
		Kind:   TransUnblock,
		SpawnN: d.currentSpawnN(),
	})
}

// Snapshot answers a deep copy of the current execution state (the
// daemon's observe/report face may read it freely).
func (d *Driver) Snapshot() *capsule.Execution {
	d.mu.Lock()
	defer d.mu.Unlock()
	return cloneExec(d.exec)
}

// SpawnN answers the current derivation counter (the fence key callers
// must carry in TransitionRequest.SpawnN).
func (d *Driver) SpawnN() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.spawnN
}

func (d *Driver) isInitialized() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.initialized
}

func (d *Driver) currentSpawnN() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.spawnN
}

func (d *Driver) now() time.Time {
	if d.cfg.Now != nil {
		return d.cfg.Now()
	}
	return time.Now()
}

func (d *Driver) stamp() string { return d.now().UTC().Format(time.RFC3339Nano) }

// wakeLoopLocked nudges a parked Run loop; coalescing by design.
func (d *Driver) wakeLoopLocked() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// ---------------------------------------------------------------------------
// Spawn seam. The production adapter (spawn.go, a later lane) bridges
// StepSpawn onto the runtime's SpawnParams (model pin, resume session,
// spawn journal) and assembles the step prompt — the step definition
// plus declared inputs plus, for inherit steps, the producing step's
// handoff artifact and, when present, the consumed rewind verdict.

// StepSpawner derives one step process per SpawnStep call. The driver
// serializes: at most one live step run per Driver at any time.
type StepSpawner interface {
	SpawnStep(ctx context.Context, sp StepSpawn) (StepRun, error)
}

// StepSpawn is the driver's order for one step-process derivation.
type StepSpawn struct {
	StepID string
	// WIID is the parent work item — the result envelope must bind it.
	WIID string
	// SpawnN is this derivation's ordinal — the step process's fence
	// identity; its result envelope must echo it.
	SpawnN int64
	// Model is the resolved candidate ("" = the harness adapter's
	// default; no candidate list is in effect).
	Model string
	// Effort is the resolved candidate's thinking effort (polyforge#176;
	// "" = the harness's own default).
	Effort string
	// Harness is the step's effective harness (polyforge#159; never ""
	// from the driver — pi when nothing chose otherwise).
	Harness string
	// Resume marks a re-dispatch of an in_progress step (answer
	// continuation, crash recovery, recovery-lane re-derivation) or of a
	// pending light-rewind target (polyforge#222: the ledger's light
	// cause derives the resume) — a resume of the previous dispatch's
	// conversation.
	Resume bool
	// Answers is the step's archived Q&A history — a resumed step
	// continues from it, never from a blank slate.
	Answers []capsule.ArchivedAnswer
	// RewindInput is the gate FAIL verdict this dispatch consumes (set
	// on a rewind re-entry; the start transition clears the persisted
	// field in the same write that starts the step).
	RewindInput *capsule.RewindInput
	// Fresh orders a new conversation for a retry (2nd+ failure, or a
	// candidate switch): no resume of the old transcript; the previous
	// transcript, when one is readable, rides the prompt as a read-only
	// reference (v2-converge 2.3 retry shape — a context-overflow style
	// failure must not repeat on every model).
	Fresh bool
	// PriorRejection is the verbatim validation error the PREVIOUS
	// dispatch's envelope was rejected for (the in-process retry lane):
	// the step author sees exactly what to fix — required fields
	// present, evidence carried — instead of blindly
	// re-authoring. Empty on a fresh dispatch.
	PriorRejection string
	// PriorEndedWithoutResult tells the retry that the previous dispatch's
	// turn ENDED without the agent writing the result envelope (cause
	// ended_without_result).
	PriorEndedWithoutResult bool
	// Done is the set of steps whose execution record is done (an
	// inherit step's handoff producer must be one).
	Done map[string]bool
}

// StepRun is one spawned step execution awaiting its result envelope.
type StepRun interface {
	// Wait blocks until the step process returns its result envelope.
	// An error means no business result (infrastructure failure or
	// cancellation): the recovery lane owns what follows.
	Wait(ctx context.Context) (*StepResult, error)
	// Terminate tears the step process down, bounded.
	Terminate(bound time.Duration) error
}

// ---------------------------------------------------------------------------
// Result validation (the core's accept gate on the minimal envelope).

// validateResultStrict is the accept-path check before the step_result
// transition. It builds a ResultSpec from the driver's current state and
// delegates to ValidateStepResult (the staging gates: shape, binding and
// fencing, the needs_input question pair, the verdict binding, the
// human-gate guarantee, the declared output schema, the V4
// command-evidence gate and the committed-test-files gate). A
// failure is an infrastructure-class rejection — Run surfaces it and the
// recovery lane owns re-derivation.
func (d *Driver) validateResultStrict(res *StepResult, stepID string, n int64) error {
	spec := d.buildResultSpec(stepID, n)
	return ValidateStepResult(res, spec)
}

// buildResultSpec assembles the read-only validation snapshot from the
// driver's current durable state (gate policy, human-answer duty, the
// answers of the current attempt chain, the command-evidence duty and the
// task-repo root the committed-test-files gate reads).
func (d *Driver) buildResultSpec(stepID string, n int64) ResultSpec {
	spec := ResultSpec{
		WIID:   d.wi.ID,
		StepID: stepID,
		SpawnN: n,
	}
	d.mu.Lock()
	if st, ok := d.exec.Steps[stepID]; ok {
		spec.HumanAnswers = currentChainAnswers(st)
		// polyforge#222: the light-rewind context — the same ledger-cause
		// fact the transition's self-report rewind reads (one fact, one
		// derivation: lightRewindContext over the archived attempts).
		spec.LightRewind = lightRewindContext(st.Attempts)
	}
	outputs := make(map[string]map[string]any, len(d.exec.Steps))
	for id, st := range d.exec.Steps {
		if st != nil && st.Status == capsule.StepDone {
			outputs[id] = st.Output
		}
	}
	if stepID == deliverStepID {
		spec.ReviewPassed = reviewPassed(d.exec.Steps[reviewStepID])
		spec.HasReview = d.workflow.step(reviewStepID) != nil
		spec.VerifiedHeads, spec.VerifiedTestBar = verificationRecord(d.exec.Steps[verificationStepID])
	}
	d.mu.Unlock()
	spec.OutputSchema = d.outputSchema(stepID)
	spec.PreExisting = preExistingRulesOf(outputs)
	// Declared outputs, gate-ness and duties (gate-ness feeds the
	// verdict binding — the #106 lesson).
	if ss := d.workflow.step(stepID); ss != nil {
		spec.Gate = ss.Gate
		spec.RequiresHumanAnswer = ss.RequiresHumanAnswer
		spec.RequireCommandEvidence = ss.RequiresCommandEvidence
	}
	if d.wi.CapsuleDir != "" {
		spec.ReposDir = filepath.Join(d.wi.CapsuleDir, capsule.DirRepos)
		// deliver is exempt from the authored-tests gate: merging the
		// latest default branch legitimately changes an authored test file
		// upstream also edited, and restoring the author commit's blob
		// would undo upstream's edit. Verification passed on the verified
		// head; anything hand-made since is escalated by deliver_facts.go.
		if stepID != deliverStepID {
			spec.AuthoredTests = authoredTestsBefore(d.workflow, outputs, stepID)
		}
	}
	return spec
}

// outputSchema answers stepID's declared output schema (cached; nil when
// the step definition declares none). A declared schema that does not
// load is reported once on stderr and validates nothing on a record-only
// step; an enforced step then rejects its completed results
// (ValidateStepResult) — the bundle tests pin that every shipped schema
// compiles.
func (d *Driver) outputSchema(stepID string) *jsonschema.Schema {
	if d.bundleDir == "" {
		return nil
	}
	d.mu.Lock()
	if d.schemas == nil {
		d.schemas = make(map[string]*stepOutputSchema)
	}
	s, ok := d.schemas[stepID]
	d.mu.Unlock()
	if !ok {
		var err error
		s, err = loadStepOutputSchema(d.bundleDir, stepID, d.workflow.stepVars(stepID))
		if err != nil {
			// loud, never a silent nil: stderr AND a durable event
			fmt.Fprintf(os.Stderr, "driver: %s: %v (output not schema-validated)\n", d.wi.ID, err)
			d.emit(EvOutputSchemaUnloadable, map[string]any{"step": stepID, "error": err.Error()})
			s = nil
		}
		d.mu.Lock()
		d.schemas[stepID] = s
		d.mu.Unlock()
	}
	if s == nil {
		return nil
	}
	return s.Schema
}

// noteAcceptedResultRecords emits the durable records an accepted result
// owes: an output that violates its declared schema on a step whose
// schema is recorded, not enforced (polyforge#164 H3), and a
// human-approved re-baseline of authored tests naming the files (H4).
func (d *Driver) noteAcceptedResultRecords(stepID string, n int64, res *StepResult) {
	if res.schemaViolation != "" {
		d.emit(EvOutputSchemaViolation, map[string]any{
			"step": stepID, "spawn_n": n,
			"schema": dirStepDefs + "/" + stepID + ".md#/output",
			"errors": res.schemaViolation,
		})
	}
	if res.rebaseline {
		d.emit(EvAuthoredTestsRebaselined, map[string]any{
			"step": stepID, "spawn_n": n, "files": res.rebaselined,
		})
	}
}

// noteUnaskedHumanGate records a requires_human_answer: if_asked step
// that completed with no human answer in its current attempt chain
// (polyforge#164 H11): the completion stands (an unattended lane runs
// on), but the skipped checkpoint is a durable EvHumanGateUnasked event,
// and the watcher turns that event into an owner notification.
func (d *Driver) noteUnaskedHumanGate(stepID string, n int64, res *StepResult) {
	ss := d.workflow.step(stepID)
	if res.Outcome != OutcomeCompleted || ss == nil || !ss.HumanAnswerIfAsked {
		return
	}
	d.mu.Lock()
	answers := 0
	if st := d.exec.Steps[stepID]; st != nil {
		answers = currentChainAnswers(st)
	}
	d.mu.Unlock()
	if answers > 0 {
		return
	}
	d.emit(EvHumanGateUnasked, map[string]any{"step": stepID, "spawn_n": n})
}

// testFilesStamp answers the authored-tests baseline to stamp for an
// accepted completed result (polyforge#164 H4): the task repos' HEADs
// when the output declares test_files (an authoring step) or when the
// step changed authored tests with a human's approval (res.rebaseline:
// the transition moves the stamp onto the authoring steps); nil
// otherwise.
func (d *Driver) testFilesStamp(res *StepResult) map[string]any {
	if res.Outcome != OutcomeCompleted || d.wi.CapsuleDir == "" {
		return nil
	}
	if len(declaredTestFiles(res.Output)) == 0 && !res.rebaseline {
		return nil
	}
	heads := repoHeads(filepath.Join(d.wi.CapsuleDir, capsule.DirRepos))
	if len(heads) == 0 {
		return nil
	}
	return heads
}

// verifiedHeads reads the task repos' HEADs a completed verification
// result records (git I/O, so outside the transition's lock); nil
// otherwise.
func (d *Driver) verifiedHeads(stepID string, res *StepResult) map[string]any {
	if stepID != verificationStepID || res.Outcome != OutcomeCompleted || d.wi.CapsuleDir == "" {
		return nil
	}
	return repoHeads(filepath.Join(d.wi.CapsuleDir, capsule.DirRepos))
}

// lightDiffBase pre-reads the task repos' HEADs the mechanical rewind
// records as the light recheck's repair-diff base (polyforge#260; git
// I/O, so outside the transition's lock — the verifiedHeads pattern):
// each repo's HEAD at this FAIL verdict's settlement, carried in the
// TransitionRequest, never step-supplied. Nil unless this is an armed
// verification gate-FAIL that carries a mechanical classification (the
// mechanical rewind's own precondition — everything else rewinds as
// today, with no marker to feed).
func (d *Driver) lightDiffBase(stepID string, res *StepResult) map[string]string {
	if stepID != verificationStepID || res.Outcome != OutcomeGateFailed || d.wi.CapsuleDir == "" {
		return nil
	}
	if !d.mechanicalArmed() {
		return nil
	}
	out, err := parseOutput(res.Output)
	if err != nil || !mechanicalBoundPresent(out) {
		return nil
	}
	return repoHeadShas(filepath.Join(d.wi.CapsuleDir, capsule.DirRepos))
}

// repoHeadShas answers each task repo's HEAD sha (repos with an unborn or
// unreadable HEAD omitted) — the mechanical rewind's diff base and the
// light pass's recheck heads.
func repoHeadShas(reposDir string) map[string]string {
	heads := map[string]string{}
	for name, v := range repoHeads(reposDir) {
		if sha, ok := v.(string); ok {
			heads[name] = sha
		}
	}
	return heads
}

// parseOutput coerces a result's output payload to a JSON object map
// (the capsule Step.Output shape); an absent payload is an empty
// object.
func parseOutput(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("output is not a JSON object: %w", err)
	}
	if out == nil {
		return map[string]any{}, nil
	}
	return out, nil
}

// verdictFor assembles the gate FAIL policy the transition applies:
// the workflow's on_fail rewind target (absent = no rewind edge, the
// workflow fails on the gate) plus the verdict text — the result's
// output payload in full, or a synthesized note when the gate produced
// none.
func (d *Driver) verdictFor(stepID string, res *StepResult) *GateVerdict {
	v := &GateVerdict{
		GateStepID:  stepID,
		VerdictText: strings.TrimSpace(string(res.Output)),
	}
	if v.VerdictText == "" {
		v.VerdictText = fmt.Sprintf("gate %s FAIL (spawn %d)", stepID, res.SpawnN)
	}
	if spec := d.workflow.step(stepID); spec != nil && spec.OnFail != nil {
		v.TargetStep = spec.OnFail.Target
	}
	return v
}

// ---------------------------------------------------------------------------
// Execution load + candidate resolution.

// loadExecution reads execution.json; a missing file yields a fresh
// skeleton (all steps pending) with existed=false — the init transition
// persists it. A torn file is a hard error (never guess execution
// state). Recorded steps outside the workflow are refused (the bundle
// digest pins the graph); missing step records are ensured pending.
func loadExecution(path string, wi WIInfo, wf *Workflow) (*capsule.Execution, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return skeletonExecution(wi, wf), false, nil
		}
		return nil, false, err
	}
	var exec capsule.Execution
	if err := json.Unmarshal(raw, &exec); err != nil {
		return nil, false, fmt.Errorf("driver: execution.json unparsable: %w", err)
	}
	if exec.Steps == nil {
		exec.Steps = map[string]*capsule.Step{}
	}
	// A step the workflow no longer has means the bundle changed under an
	// in-flight work item (a re-claim materializes the daemon's current
	// checkout, which an upgrade may have restructured). It cannot be
	// continued on this bundle: refuse loudly, naming every such step —
	// the claim lands the WI blocked with this text as its reason.
	var removed []string
	for id := range exec.Steps {
		if _, ok := wf.indexOf(id); !ok {
			removed = append(removed, strconv.Quote(id))
		}
	}
	if len(removed) > 0 {
		sort.Strings(removed)
		return nil, false, fmt.Errorf("driver: execution.json records step(s) %s that workflow bundle %q no longer has — the bundle changed under this in-flight work item (a step was removed or renamed); it cannot continue on the current bundle: cancel it and re-file",
			strings.Join(removed, ", "), wi.BundleName)
	}
	for _, spec := range wf.Steps {
		if _, ok := exec.Steps[spec.ID]; !ok {
			exec.Steps[spec.ID] = &capsule.Step{Status: capsule.StepPending}
		}
	}
	return &exec, true, nil
}

func skeletonExecution(wi WIInfo, wf *Workflow) *capsule.Execution {
	exec := &capsule.Execution{
		CapsuleID:      capsuleIDFor(wi.ID),
		WorkItemID:     wi.ID,
		WorkflowStatus: StatusRunning,
		Steps:          map[string]*capsule.Step{},
	}
	for _, spec := range wf.Steps {
		exec.Steps[spec.ID] = &capsule.Step{Status: capsule.StepPending}
	}
	return exec
}

func capsuleIDFor(wi string) string { return "cap_" + wi }

// effectiveCandidates answers the step's claim-time candidate list (each
// `<harness>:<model>`, validated by New). The pointer recomputation, the
// switch and the exhaustion paths all walk this list — across harnesses.
func (d *Driver) effectiveCandidates(stepID string) []string {
	return d.cfg.StepCandidates[stepID]
}

// modelForLocked resolves the step's effective candidate for the NEXT
// dispatch (the `<harness>:<model>` at the recomputed pointer; "" when
// no candidate list is in effect). The recomputation runs over the
// claim-time list (polyforge#79). Model-class infra failures alone advance (bounded, capped at list end), a gate-FAIL
// rewind resets to the head.
func (d *Driver) modelForLocked(stepID string) string {
	list := d.effectiveCandidates(stepID)
	if len(list) == 0 {
		return ""
	}
	st := d.exec.Steps[stepID]
	if st == nil {
		return list[0]
	}
	return list[CandidatePointer(st.Attempts, list)]
}
