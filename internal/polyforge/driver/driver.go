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

	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/scopetier"
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
	// Compose answers the per-step candidate lists (polyforge#33): the
	// driver calls it at EVERY dispatch (outside its mutex), so a yaml or
	// bundle edit governs the next dispatch. It composes over every
	// precedence level (workspace.ComposeCandidates) and holds the answer
	// for that dispatch and its failure decision only; a failure blocks the
	// workflow (the owner fixes the config and resumes). Nil means no
	// list: the harness default model runs (tests). Every candidate is
	// `<harness>:<model>` (polyforge#175): the prefix is the harness that
	// candidate's dispatch runs on (a malformed one blocks like a compose
	// failure).
	Compose func() (map[string][]string, error)
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
	// LimitsFunc, when set, answers the CURRENT global defaults at every
	// judgement, so a daemon reload of step_timeout/stall_window takes
	// effect without a restart. nil keeps Limits.
	LimitsFunc func() supervise.Defaults
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

	// scopeOptIn records, read once in New from the work item's content
	// (outside every lock), whether the work item carries the explicit
	// `verification: scoped` marker. The effective tier combines it with
	// the lane (effectiveTier) and is recorded on the first verification
	// step_start, then restored unchanged.
	scopeOptIn bool

	mu     sync.Mutex // serializes every transition (the single write entry)
	spawnN int64
	// initialized records that execution.json exists on disk (loaded,
	// or written by the init transition).
	initialized bool

	cfg Config
	log EventLogger
	// cands is the candidate map of the dispatch in flight (or about to
	// start): composed at that dispatch, read by its failure decision,
	// replaced by the next one (polyforge#33; guarded by mu).
	cands map[string][]string
	// failed is set when a transition's persist failed: memory may be
	// ahead of execution.json. Every further transition answers it and
	// Run returns it; the restart reloads the file (transition.go).
	failed error

	// wake carries the "state changed, re-plan" signal to a Run loop
	// parked on an open question; one buffered slot, coalescing.
	wake chan struct{}

	// priorRejection tracks the per-step envelope-rejection feedback for
	// the in-process re-dispatch lane (the #97/#107 lessons): the string
	// rides the next dispatch's prompt as visible feedback so the author
	// can fix the actual defect. The count itself is durable on Step.
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
	// lightSchema caches the light recheck's output schema (the shared
	// fast-lane material's frontmatter, polyforge#260); lightSchemaLoaded
	// marks the one load attempt (nil schema = none declared or
	// unloadable — the full step schema stays in effect).
	lightSchema       *stepOutputSchema
	lightSchemaLoaded bool
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
	// The scoped opt-in marker: read once here, outside every lock (the
	// workflow.md read pattern). A missing work-item.json or no marker is
	// full; the marker only ever matters on a scoped-capable lane.
	scopeOptIn := false
	if doc, err := readWorkItemDoc(cfg.WI.CapsuleDir); err == nil && doc.Content != nil {
		scopeOptIn = scopetier.OptedIn(*doc.Content)
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
		priorRejection: make(map[string]string),
		unbooked:       make(map[string]int64),
		bundleDir:      bundleDir,
		schemas:        make(map[string]*stepOutputSchema),
		scopeOptIn:     scopeOptIn,
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
	// a ruling whose write landed before a crash took its document
	// append is landed now (idempotent: keyed on the ruling)
	d.landOwnerRulings()
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
		req := TransitionRequest{
			Kind:     TransInterrupted,
			Reason:   d.cfg.Interrupted.Detail,
			BookWall: d.cfg.Interrupted.BookWall,
			SpawnN:   d.currentSpawnN(),
		}
		// The tree hash is read before the transition takes d.mu (the
		// VerifiedHeads/LightDiffBase pattern: git I/O never under the
		// driver's lock); a hash error records none (today's behavior).
		if h, herr := treeHashFn(d.wi.CapsuleDir); herr == nil {
			req.TreeHash = h
		} else {
			fmt.Fprintf(os.Stderr, "driver: %s: interrupted-dispatch tree hash unavailable (recording none): %v\n", d.wi.ID, herr)
		}
		if err := d.transition(req); err != nil {
			return fmt.Errorf("driver: interrupted-dispatch archive failed: %w", err)
		}
	}
	// A decided backoff survives this driver's predecessor: the attempts
	// ledger names the death that owes it and when it happened (RetryOwed),
	// so a restart or relaunch during the wait serves only its remainder
	// before the next dispatch instead of re-dispatching into the same
	// outage. Within one Run the failure path waits for itself
	// (failDispatch), so this runs once per Run.
	if collected == nil {
		if err := d.awaitOwedRetry(ctx); err != nil {
			return err
		}
	}
	for {
		if d.takeStalled() != nil {
			return ErrDriverStalled // the supervisor's needs_recovery records the stall
		}
		if collected != nil {
			res := collected
			collected = nil
			// Book the dead dispatch's runtime before its envelope settles
			// ( B4): the measure ends where the dispatch actually
			// ran — at the envelope's daemon stamp — so daemon downtime and a
			// paused period are not counted (the D6 lifecycle ruling).
			d.bookCollectedWall(res)
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

		// polyforge#260: the light recheck's pre-dispatch phase — the
		// marker peek under the lock, the bound coverage validation and
		// the deterministic repair-diff measurement outside it. A malformed
		// classification or a broken repair bound escalates IN PLACE, in
		// the same step, instead of a light dispatch: the loop re-plans
		// and re-dispatches the step in full mode (the existing full
		// verification, unchanged).
		esc, err := d.prepareLightRecheck(stepID)
		if err != nil {
			return err
		}
		if esc {
			continue
		}

		// criterion 6 (widened by D3): a gate whose
		// tree is unchanged since its last verdict, after a rewind of the
		// build step, must not be re-run — crashed build attempt or not. Park
		// the workflow with a human-visible reason (the rewinding verdict, the
		// build step's summary, the crash when there was one) instead of
		// spending a full verification/review and another rewind slot on an
		// identical resubmission.
		if blocked, err := d.blockUnchangedGateTree(stepID); err != nil {
			return err
		} else if blocked {
			continue
		}

		if blocked, err := d.composeDispatch(stepID); err != nil {
			return err
		} else if blocked {
			continue // the workflow is blocked: plan stops the loop
		}

		spawn, n, err := d.prepareDispatch(stepID)
		if err != nil {
			return err
		}
		if spawn == nil {
			continue // an open question is parked; plan again once answered
		}
		// item 6: the first dispatch of a round a rewind reset
		// keeps its dispatch SHAPE across a crash that lands after the
		// step_start but before the spawn journal line. The verdict itself
		// needs no restoration: it stays in pending_rewind_input for the
		// whole round, so prepareDispatch already put it on
		// this spawn.
		if d.rewindReentryOrder(stepID) {
			spawn.Resume = false
		}
		// the gate's plan-conformance facts — git reads, so
		// computed here, outside d.mu (the verifiedHeads pattern).
		spawn.PlanConformance = d.planConformance(stepID)

		if err := d.transition(TransitionRequest{
			Kind:      TransStepStart,
			StepID:    stepID,
			Model:     spawn.Model,
			Effort:    spawn.Effort,
			Candidate: spawn.Candidate,
			ScopeTier: spawn.ScopeTier,
			SpawnN:    n,
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
			if stop, err := d.failDispatch(ctx, stepID, n, dispatchedAt, d.now(), cause, code, detail, nil); stop {
				return err
			}
			continue
		}
		announced := d.announceStart(spawn, run, dispatchedAt)
		res, werr, verdict := d.awaitDispatch(ctx, run, stepID, n)
		// The dispatch's wall ends when its process is known gone. The
		// step-end hooks below are cleanup: charging their cost to T would
		// turn a step-end /proc scan into the next dispatch's timeout.
		endedAt := d.now()
		// R4: the wall booked into T starts after the
		// observation gaps the supervisor declared during this dispatch (a
		// host or daemon freeze it forgave), so the next dispatch of the
		// step does not inherit the freeze as PriorRuntime. Read before
		// step_ended, which drops the dispatch's tracker.
		bookedFrom := dispatchedAt.Add(d.declaredPause(n, announced))
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
			// The verdict record is emitted by failDispatch AFTER the
			// step_failed write, so a reader that sees step_dead is guaranteed
			// to see its step_failed too (the acceptance contract) without
			// delaying the verdict behind the D7 backoff sleep.
			if stop, err := d.failDispatch(ctx, stepID, n, bookedFrom, endedAt, verdictCause(*verdict), -1, verdict.Evidence, verdict); stop {
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
			if stop, err := d.failDispatch(ctx, stepID, n, bookedFrom, endedAt, cause, code, detail, nil); stop {
				return err
			}
			continue
		}
		d.announceEnd(stepID, n, supervise.OutcomeResult, 0, "")
		d.addUnbookedUntil(stepID, bookedFrom, endedAt)
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

// bookCollectedWall books the runtime of a dispatch whose envelope a dead
// predecessor collected ( B4). The measure starts at the
// dispatch's StepStartedAt and ends at the envelope's daemon-stamped
// SubmittedAt: the agent stopped running when it delivered, so the daemon
// downtime before this successor collected the envelope — and any paused
// period, in which no successor re-stamped StepStartedAt — is not counted
// (the D6 lifecycle ruling). Only a synthetic envelope without the stamp
// (no production path) has no better bound than now.
func (d *Driver) bookCollectedWall(res *StepResult) {
	d.mu.Lock()
	st := d.exec.Steps[res.StepID]
	startedAt := ""
	if st != nil {
		startedAt = st.StepStartedAt
	}
	d.mu.Unlock()
	from, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return
	}
	until := res.SubmittedAt
	if until.IsZero() {
		until = d.now()
	}
	d.addUnbookedUntil(res.StepID, from, until)
}

// rewindReentryOrder answers the dispatch SHAPE a rewind still owes the
// first new-round dispatch after a crash landed following that dispatch's
// step_start but before its spawn-journal line ( item 6).
// One rule covers every step the rewind re-enters: a resume never crosses
// a rewind boundary. The boundary is the newest archive that reset or
// re-entered the step — its own gate_fail_rewind* (the target) or
// superseded_by_rewind attempt (a step between target and gate), or, for
// the gate, any archive carrying that gate's rewind_gate.
//
// Until a dispatch of the new round is journaled, such a step starts
// without resume (the light causes keep theirs); a superseded step and
// the gate simply start fresh. Archived infra_failure attempts after the
// boundary (daemon_restart, driver_relaunch, a pre-journal spawn failure's
// step_failed) are audit only and never move it; the spawn journal is the
// bound, after which the ordinary resume is correct.
//
// The verdict is NOT this function's business: it stays in
// steps.<id>.pending_rewind_input for the whole round, so every dispatch
// of the round — this crash re-entry, a post-journal retry, the restart
// after a daemon death — reads it from there and no restoration path
// exists.
func (d *Driver) rewindReentryOrder(stepID string) (forceFresh bool) {
	d.mu.Lock()
	st := d.exec.Steps[stepID]
	if st == nil || st.Status != capsule.StepInProgress {
		d.mu.Unlock()
		return false
	}
	// The step's own archives are the target/superseded boundary; every
	// other step's archive whose rewind_gate names this step is the gate
	// boundary. The newest one wins: an older rewind the step already
	// re-entered no longer matters.
	var boundary capsule.Attempt
	boundaryAt := time.Time{}
	own := false
	found := false
	take := func(a capsule.Attempt, ownArchive bool) {
		if !rewindArchive(a.Cause) {
			return
		}
		at, err := time.Parse(time.RFC3339Nano, a.ArchivedAt)
		if err != nil {
			return
		}
		if !found || at.After(boundaryAt) {
			boundary, boundaryAt, own, found = a, at, ownArchive, true
		}
	}
	for _, a := range st.Attempts {
		take(a, true)
	}
	for _, other := range d.exec.Steps {
		if other == st {
			continue
		}
		for _, a := range other.Attempts {
			if a.RewindGate == stepID {
				take(a, false)
			}
		}
	}
	d.mu.Unlock()
	if !found || d.spawnJournaledSince(stepID, boundaryAt) {
		return false
	}
	// The light causes keep their resume; only the fresh target, a
	// superseded step and the gate start a new conversation.
	return !(own && (boundary.Cause == CauseGateFailRewindTextOnly || boundary.Cause == CauseGateFailRewindMechanical))
}

// gateUnchangedBlockPrefix marks the driver-authored blocked_reason of the
// criterion-6 guard. The owner's resume clears the gate's
// last-judged tree so the explicit override can run the gate once on the
// same tree instead of blocking again.
const gateUnchangedBlockPrefix = "gate tree unchanged: "

// unchangedTreeSummaryRunes bounds the build step's own summary quoted into
// the unchanged-tree block reason (one line, a human-facing excerpt).
const unchangedTreeSummaryRunes = 300

// blockUnchangedGateTree is the criterion-6 pre-dispatch
// guard. It parks the workflow blocked (a human-visible result; the owner's
// `pf resume` unblocks it) when ALL of the following hold:
//
//   - the step about to dispatch is a workflow gate with a recorded
//     last-judged task-repo tree;
//   - a rewind of the lane's BUILD step archived at/after that verdict
//     re-entered this gate (its own range was [build step, gate));
//   - the task-repo tree now is byte-identical to the one the gate last
//     judged: the build round that rewind opened produced no change.
//
// Whether the rewound build step crashed on the way does not matter
// ( D3): re-judging an identical tree can only repeat the
// verdict or pass by chance, so a build agent that received the verdict
// and changed nothing parks exactly like a crashed attempt's retry. The
// reason names the gate that is not re-run, the rewinding gate and its
// verdict round, the crashed build attempt when there was one, and a
// bounded excerpt of the build step's own summary (why it changed nothing).
//
// An identical task-repo tree is not by itself an identical gate input: on
// a lane with a done plan step the gate also judges the driver-computed
// plan-conformance facts (planconformance.go), and the designed repair for
// the "changed but not declared" fact is metadata — the build step cites
// the finding in its `declared_extensions` output and commits nothing
//. After the tree-hash equality, the guard recomputes the
// facts the gate last judged (the same diff, folding the build round the
// FAIL's rewind archived as the live output and the older archives as
// carried) and re-runs the gate only when those judged facts carried a
// blocking entry and the current recomputed facts carry none: the input
// the gate would judge changed. The exemption does not key on a live
// citation, because `declared_extensions` is cumulative and a later
// no-change round re-emits the same citation; that would re-judge an
// already-judged input. A tree that still carries a blocking conformance
// fact, an unreadable diff, or facts the gate already judged clean parks
// as before (fixture B of the repro).
//
// The build-step condition is what keeps the guard off rewinds whose
// target's product is an artifact (spec, repro, the deliver 「全量重验」
// verification re-run): those may legitimately change the artifact while
// the task-repo tree stays put, so they are not a "no progress"
// resubmission. The build step is derived from the lane (buildStep), never
// a hard-coded step id.
//
// It never starts the gate, so no full verification/review runs and no
// rewind slot is charged. A light recheck and its in-place escalation are
// deliberately exempt: they are the designed same-tree fast lanes. A light
// REWIND (text_only, mechanical) is not: its re-entered build step resumes
// to change the tree, and a resumed round that changed nothing parks like
// any other — unless that round is the metadata-only conformance repair
// above. Nor is the full verification after a light recheck a fresh
// re-derivation abandoned ( S2): it archives the same
// light_recheck_escalated cause, but a fresh build round came between, so
// only inPlaceEscalationContext exempts.
func (d *Driver) blockUnchangedGateTree(stepID string) (bool, error) {
	d.mu.Lock()
	spec := d.workflow.step(stepID)
	st := d.exec.Steps[stepID]
	if spec == nil || !spec.Gate || st == nil || st.JudgedRepoHash == "" ||
		st.PendingLightRecheck != nil || inPlaceEscalationContext(st.Attempts) {
		d.mu.Unlock()
		return false, nil
	}
	gateIdx, ok := d.workflow.indexOf(stepID)
	if !ok {
		d.mu.Unlock()
		return false, nil
	}
	buildStep := d.workflow.buildStep()
	if buildStep == "" {
		d.mu.Unlock()
		return false, nil
	}
	judgedAt, ok := d.gateLastVerdictAtLocked(stepID)
	if !ok {
		d.mu.Unlock()
		return false, nil
	}
	target, rewindGate, judgedRound, crash, ok := d.buildRewindSinceLocked(gateIdx, buildStep, judgedAt)
	if !ok {
		d.mu.Unlock()
		return false, nil
	}
	// The review owner exit's amend-upstream ruling is new gate input:
	// the owner approved an upstream correction the build round may
	// legitimately execute without a task-repo change, so a re-entry it
	// opened is never a "no progress" resubmission.
	if rs := d.exec.RewindStats.Rewinds; len(rs) > 0 && rs[len(rs)-1].Gate == reviewStepID && rs[len(rs)-1].Class == capsule.RewindOwner {
		d.mu.Unlock()
		return false, nil
	}
	judgedHash, judgedStamp := st.JudgedRepoHash, judgedAt.UTC().Format(time.RFC3339Nano)
	round := d.exec.ReviewRounds[rewindGate]
	summary := fmt.Sprintf("%q gave no summary.", target)
	if tst := d.exec.Steps[target]; tst != nil {
		if s, _ := tst.Output["summary"].(string); strings.TrimSpace(s) != "" {
			summary = fmt.Sprintf("%q's summary: %q.", target, oneLine(s, unchangedTreeSummaryRunes))
		}
	}
	d.mu.Unlock()

	if d.wi.CapsuleDir == "" {
		return false, nil
	}
	cur, herr := reposTreeHashFn(d.wi.CapsuleDir)
	if herr != nil || cur != judgedHash {
		return false, nil
	}
	if d.gateConformanceRepaired(stepID, judgedRound) {
		return false, nil
	}
	resubmitted, next := "re-submitted the identical tree",
		fmt.Sprintf("Change %q's tree, or resume to have the gates judge this same tree once more.", target)
	if crash != nil {
		resubmitted = fmt.Sprintf("re-submitted the identical tree after its attempt %d (started %s) crashed with %s",
			crash.AttemptSeq, crash.StartedAt, crash.FailureCause)
		next = fmt.Sprintf("Resolve %q's failure (or change its tree) and resume.", target)
	}
	reason := fmt.Sprintf("%sgate %q last judged the task-repo tree %s at %s; the rewound step %q %s: the build round after gate %q's FAIL verdict (verdict round %d) produced no change, so the gate is not re-run and no rewind slot is charged. %s %s",
		gateUnchangedBlockPrefix, stepID, shortTreeHash(judgedHash), judgedStamp, target, resubmitted,
		rewindGate, round, summary, next)
	if err := d.transition(TransitionRequest{
		Kind: TransBlocked, StepID: stepID, Reason: reason, SpawnN: d.currentSpawnN(),
	}); err != nil {
		return false, fmt.Errorf("driver: step %s unchanged-tree block failed: %w", stepID, err)
	}
	return true, nil
}

// gateLastVerdictAtLocked answers when gate last delivered a verdict, from
// the attempts ledger alone: its newest accepted attempt (a PASS) or its
// newest rewind archive naming it in rewind_gate (a FAIL, archived by the
// same write that routed the verdict). Deriving it here keeps the fact in
// one place and makes the unchanged-tree comparison inclusive of a verdict
// archived in the same write as the rewind. The caller holds d.mu.
func (d *Driver) gateLastVerdictAtLocked(gate string) (time.Time, bool) {
	var at time.Time
	var ok bool
	if gst := d.exec.Steps[gate]; gst != nil {
		for i := len(gst.Attempts) - 1; i >= 0; i-- {
			a := &gst.Attempts[i]
			if a.Cause != "accepted" {
				continue
			}
			if t, err := time.Parse(time.RFC3339Nano, a.ArchivedAt); err == nil {
				at, ok = t, true
			}
			break
		}
	}
	for _, other := range d.exec.Steps {
		for i := range other.Attempts {
			a := &other.Attempts[i]
			if a.RewindGate != gate || !rewindTargetCause(a.Cause) {
				continue
			}
			if t, err := time.Parse(time.RFC3339Nano, a.ArchivedAt); err == nil && (!ok || t.After(at)) {
				at, ok = t, true
			}
		}
	}
	return at, ok
}

// buildRewindSinceLocked answers the lane's build step when a rewind of it
// archived at/after the gate's last verdict has this gate in its range,
// together with the gate that rewound it (the newest such rewind), the
// index of that rewind's archive on the build step (its result is the
// output the gate judged), and a copy of the build step's newest
// infra_failure attempt after that rewind (nil when the round did not
// crash — the reason names a crash only when there was one). Everything is
// in the execution ledger; the caller holds d.mu. ok=false: no rewind of
// the build step re-entered this gate.
func (d *Driver) buildRewindSinceLocked(gateIdx int, buildStep string, since time.Time) (target, rewindGate string, judgedRound int, crash *capsule.Attempt, ok bool) {
	tIdx, known := d.workflow.indexOf(buildStep)
	if !known || tIdx >= gateIdx {
		return "", "", -1, nil, false
	}
	tst := d.exec.Steps[buildStep]
	if tst == nil {
		return "", "", -1, nil, false
	}
	var rewindAt time.Time
	for i := range tst.Attempts {
		a := &tst.Attempts[i]
		if !rewindTargetCause(a.Cause) {
			continue
		}
		gIdx, known := d.workflow.indexOf(a.RewindGate)
		if !known || gateIdx > gIdx {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, a.ArchivedAt)
		if err != nil || at.Before(since) {
			continue
		}
		if !ok || at.After(rewindAt) {
			rewindAt, rewindGate, judgedRound, ok = at, a.RewindGate, i, true
		}
	}
	if !ok {
		return "", "", -1, nil, false
	}
	for i := len(tst.Attempts) - 1; i >= 0; i-- {
		a := tst.Attempts[i]
		if a.Cause != CauseInfraFailure {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, a.InterruptedAt)
		if err != nil || at.Before(rewindAt) {
			continue
		}
		crash = &a
		break
	}
	return buildStep, rewindGate, judgedRound, crash, true
}

// rewindTargetCause reports whether an attempt is a rewind archive's target
// record (the step a gate FAIL reset), as opposed to the collateral
// superseded_by_rewind records or an infra_failure.
func rewindTargetCause(cause string) bool {
	switch cause {
	case CauseGateFailRewind, CauseGateFailRewindTextOnly, CauseGateFailRewindMechanical:
		return true
	}
	return false
}

// shortTreeHash shortens a tree hash for a human-facing block reason.
func shortTreeHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// rewindArchive reports whether the attempt cause is a rewind archive
// (01 §6.3b): the target/superseded rollback records. infra_failure and
// the accepted/audit causes are not.
func rewindArchive(cause string) bool {
	switch cause {
	case CauseGateFailRewind, CauseGateFailRewindTextOnly, CauseGateFailRewindMechanical, CauseSupersededByRewind, CauseRootCauseUnaddressed:
		return true
	}
	return false
}

// spawnJournaledSince reports whether a dispatch of this step was journaled
// after the rewind archive: the new round's conversation exists on disk, so
// an ordinary resume is correct and the rewind order no longer needs
// restoring. An unreadable journal or stamp answers false: keeping the
// rewind order is the safe side.
func (d *Driver) spawnJournaledSince(stepID string, after time.Time) bool {
	entries, err := capsule.ReadSpawnJournal(d.wi.CapsuleDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.StepID != stepID || e.Transcript == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if err != nil {
			return false
		}
		if at.After(after) {
			return true
		}
	}
	return false
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
		// The count is durable ( B3): one write per rejection,
		// so a restart keeps the same bound and a re-collected archived
		// envelope is not counted twice.
		if err := d.transition(TransitionRequest{
			Kind: TransEnvelopeRejected, StepID: stepID, SpawnN: n,
		}); err != nil {
			return true, fmt.Errorf("driver: step %s envelope-rejection write failed: %w", stepID, err)
		}
		d.mu.Lock()
		count := d.exec.Steps[stepID].EnvelopeRejects
		d.mu.Unlock()
		if count >= maxEnvelopeRejects {
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
				Failure: &StepFailure{Cause: FailResultInvalid, Detail: verr.Error(), WallMs: d.takeUnbooked(stepID)},
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
	delete(d.priorRejection, stepID)
	d.mu.Unlock()
	// criterion 6: a gate's verdict records the task-repo tree
	// it judged, read before the transition takes the driver's lock (git
	// I/O never under d.mu). A hash error records none: the unchanged-tree
	// guard fails open rather than blocking on an uncertain equality.
	judgedRepoHash := ""
	if spec := d.workflow.step(stepID); spec != nil && spec.Gate &&
		(res.Outcome == OutcomeCompleted || res.Outcome == OutcomeGateFailed) {
		var herr error
		judgedRepoHash, herr = reposTreeHashFn(d.wi.CapsuleDir)
		if herr != nil {
			if !errors.Is(herr, os.ErrNotExist) {
				fmt.Fprintf(os.Stderr, "driver: %s: step %s judged tree hash unavailable (recording none): %v\n", d.wi.ID, stepID, herr)
			}
			judgedRepoHash = ""
		}
	}
	if err := d.transition(TransitionRequest{
		Kind:                    TransStepResult,
		StepID:                  stepID,
		Result:                  res,
		SpawnN:                  n,
		WallMs:                  d.takeUnbooked(stepID),
		TestFilesCommit:         d.testFilesStamp(res),
		RebaselineAuthoredTests: res.rebaseline,
		VerifiedHeads:           d.verifiedHeads(stepID, res),
		ReviewedHeads:           d.reviewedHeads(stepID, res),
		JudgedHeads:             d.judgedHeads(stepID, res),
		LightDiffBase:           d.lightDiffBase(stepID, res),
		LightSettle:             d.lightSettleFactsOf(stepID, res),
		ScopeChain:              d.scopeChainFor(stepID, res),
		JudgedRepoHash:          judgedRepoHash,
		ReproHeads:              d.reproHeads(stepID, res),
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

// prepareLightRecheck is the light recheck's pre-dispatch phase
// (polyforge#260): the marker peek under the lock, then — outside every
// lock — the bound coverage validation (pure over the marker and the
// gate's recorded FAIL verdict) and the deterministic repair-diff
// measurement (git). A malformed classification or a broken repair bound
// escalates IN PLACE: the TransLightEscalate transition instead of a
// light dispatch (no rewind slot, no rewind input — the loop re-plans and
// re-dispatches the step in full mode), answering escalated=true; the
// measured diff (when one was measurable) rides the request as the
// escalation audit's fact. No marker answers (false, nil): an ordinary
// dispatch. Idempotent by construction: the same marker, FAIL verdict
// and repo state always derive the same verdict, so an interrupted light
// recheck re-dispatches and replays (or escalates) with no half state.
func (d *Driver) prepareLightRecheck(stepID string) (escalated bool, err error) {
	d.mu.Lock()
	st := d.exec.Steps[stepID]
	var marker *capsule.LightRecheck
	var failOut map[string]any
	if st != nil {
		marker, failOut = st.PendingLightRecheck, st.Output
	}
	d.mu.Unlock()
	if marker == nil {
		return false, nil
	}
	reason := ""
	if d.wi.CapsuleDir == "" {
		reason = "the capsule carries no task repos to measure the repair diff in"
	}
	reposDir := ""
	if d.wi.CapsuleDir != "" {
		reposDir = filepath.Join(d.wi.CapsuleDir, capsule.DirRepos)
	}
	var bound []mechBound
	if reason == "" {
		var berr error
		bound, berr = parseMechBound(marker.Bound)
		if berr != nil {
			reason = "the mechanical classification is malformed: " + berr.Error()
		}
	}
	if reason == "" {
		if r := validateBoundCoverage(bound, failOut, reposDir); r != "" {
			reason = "the mechanical classification does not cover the FAIL verdict's failed checks: " + r
		}
	}
	var measured *lightDiff
	if reason == "" {
		if diff, r := measureRepairDiff(bound, marker.DiffBase, reposDir); r != "" {
			reason = "the repair diff breaks the bound: " + r
			measured = &diff // the partial measurement is the escalation audit's fact
		}
	}
	if reason == "" {
		return false, nil
	}
	if err := d.transition(TransitionRequest{
		Kind: TransLightEscalate, StepID: stepID, Reason: reason, SpawnN: d.currentSpawnN(),
		LightEscalationDiff: measured,
	}); err != nil {
		return false, fmt.Errorf("driver: step %s light-path escalation failed: %w", stepID, err)
	}
	return true, nil
}

// prepareDispatch snapshots everything the spawn needs from under the
// mutex: the resume flag (an in_progress step is a re-dispatch — answer
// continuation, crash recovery, recovery-lane re-derivation; a pending
// light-rewind target is the light re-entry, its resume derived from
// the ledger), the round's rewind verdict (it must reach the dispatched
// process's prompt inputs, and it stays persisted until the step is
// accepted, so every retry of the round reads it here), the step's
// current attempt chain of answers (a resumed step continues with the
// Q&A of this round, never with an answer a rewind already consumed), and
// the effective model candidate. Parking on an open
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
		if sp.RewindInput != nil && d.workflow.buildStep() == stepID {
			// criterion 5: the tree this round started from is
			// the one the rewinding gate last judged. A retry still sitting
			// on it made no code progress, so the spawn adapter suppresses
			// the continue-in-place wording. Only a rewind of the lane's
			// BUILD step compares task-repo trees: an artifact-producing
			// target (spec, repro, the deliver 「全量重验」 verification
			// re-run) may legitimately change its artifact while the repos
			// tree stays put.
			for i := len(st.Attempts) - 1; i >= 0; i-- {
				if !rewindArchive(st.Attempts[i].Cause) {
					continue
				}
				if gst := d.exec.Steps[st.Attempts[i].RewindGate]; gst != nil {
					sp.RewindRepoHash = gst.JudgedRepoHash
				}
				break
			}
		}
		if n := currentChainAnswers(st); n > 0 {
			sp.Answers = append([]capsule.ArchivedAnswer(nil), st.Answers[len(st.Answers)-n:]...)
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
		// polyforge#260: the pending light recheck marker makes this
		// dispatch the light recheck — the mode switch the prompt (the
		// light section) and the settlement (the light output schema, the
		// mechanical_pass record) both key on. The bound is parsed here
		// (pure); a malformed one never reaches a light dispatch (the
		// pre-dispatch phase escalated instead).
		if st.PendingLightRecheck != nil {
			if bound, err := parseMechBound(st.PendingLightRecheck.Bound); err == nil {
				sp.LightRecheck = &LightRecheckOrder{Bound: bound, DiffBase: st.PendingLightRecheck.DiffBase}
			}
		}
		// polyforge#260: a verification step whose ledger's latest
		// non-infra_failure archived attempt is the light path's in-place
		// escalation is the ESCALATED full re-run — the dispatch carries
		// the escalation order (the prompt's escalation note: the report's
		// fast-lane section duty, which the light recheck that would have
		// written it never settled to write). The order is that ledger
		// fact (the escalation write archived it), the light re-entry's
		// resume pattern; the full re-run's own accepted attempt ends it.
		// The full run after a recheck a fresh re-derivation abandoned
		// carries the note too (escalatedRecheckContext, not the guard's
		// narrower inPlaceEscalationContext — S2): the
		// shipped escalation note (workflows/_shared/mechanical-fix.md)
		// names that abandonment and owes the report's fast-lane section
		// stating that no light recheck ran, so the fast lane's audit trail
		// reaches the report either way.
		if escalatedRecheckContext(st.Attempts) {
			sp.EscalatedRecheck = true
		}
		// the fast-path inputs — the tree hash the step's
		// latest failed attempt archived and whether that attempt is a
		// death rather than an answer continuation. Both come from the
		// existing ledger (no new persisted state); a step with no
		// archived attempt carries neither.
		if len(st.Attempts) > 0 {
			last := st.Attempts[len(st.Attempts)-1]
			sp.RecordedTreeHash = last.TreeHash
			sp.RetryTriggered = attemptAfterAnswer(last, st.Answers)
			sp.RecordedAttemptAt = last.InterruptedAt
			if sp.RecordedAttemptAt == "" {
				sp.RecordedAttemptAt = last.ArchivedAt
			}
		}
	}
	// the in-process rejection retry lane: the prior dispatch's
	// validation error rides the prompt as visible feedback
	sp.PriorRejection = d.priorRejection[stepID]
	// The verification tier: the recorded tier once present,
	// else the effective tier derived from the marker and the lane. A
	// re-dispatch therefore restores the recorded tier, never recomputes it.
	sp.ScopeTier = d.exec.ScopeTier
	if sp.ScopeTier == "" {
		sp.ScopeTier = effectiveTier(d.scopeOptIn, d.wi.BundleName)
	}
	if stepID == reviewStepID {
		// review's scoped obligation keys on the work item's
		// opt-in (the scope_adequate validation does), never on the
		// recorded verification tier a verification FAIL resets to full.
		sp.ScopeTier = effectiveTier(d.scopeOptIn, d.wi.BundleName)
		sp.ReviewReentry = st != nil && st.LastReviewRound != nil
	}
	if sp.PriorRejection == "" && st != nil && len(st.Attempts) > 0 && st.Attempts[len(st.Attempts)-1].FailureCause == FailEndedWithoutResult {
		sp.PriorEndedWithoutResult = true
	}
	// the candidate names the harness its dispatch runs on (polyforge#175);
	// no list in effect runs pi's own default model. A candidate on
	// another harness than the previous dispatch's starts a fresh
	// session with the old transcript as reference (spawn adapter).
	sp.Harness = harness.Pi
	if spec := d.workflow.step(stepID); spec != nil {
		cand := d.modelForLocked(stepID)
		if c, err := harness.ParseCandidate(cand); err == nil {
			sp.Harness, sp.Model, sp.Effort = c.Harness, c.Model, c.Effort
			sp.Candidate = cand
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

// attemptAfterAnswer reports whether the step's latest archived attempt
// is a death-triggered re-dispatch rather than a needs_input answer
// continuation: true when there is no archived answer, or
// the latest answer is no later than the attempt's stamp. The stamps are
// RFC3339Nano; an unparsable stamp falls back to the lexicographic
// comparison (both orders agree on well-formed RFC3339Nano strings). A
// zero-valued attempt (no attempt) is false.
func attemptAfterAnswer(last capsule.Attempt, answers []capsule.ArchivedAnswer) bool {
	if last.Cause == "" {
		return false
	}
	if len(answers) == 0 {
		return true
	}
	answeredAt := answers[len(answers)-1].AnsweredAt
	attemptAt := last.InterruptedAt
	if attemptAt == "" {
		attemptAt = last.ArchivedAt
	}
	if answeredAt == "" || attemptAt == "" {
		return answeredAt <= attemptAt
	}
	a, aerr := time.Parse(time.RFC3339Nano, answeredAt)
	b, berr := time.Parse(time.RFC3339Nano, attemptAt)
	if aerr != nil || berr != nil {
		return answeredAt <= attemptAt
	}
	return !a.After(b)
}

// composeDispatch composes the candidate lists for the dispatch about to
// start (polyforge#33) — file reads, so outside the mutex — and holds them
// for it. A composition that fails (an unreadable or invalid yaml or
// bundle, a malformed candidate) blocks the workflow through TransBlocked:
// the owner fixes the config and `pf resume` re-dispatches from the
// checkpoint. It answers blocked=true when it parked the workflow.
func (d *Driver) composeDispatch(stepID string) (blocked bool, err error) {
	if d.cfg.Compose == nil {
		return false, nil
	}
	m, cerr := d.cfg.Compose()
	if cerr == nil {
		cerr = validateCandidates(m)
	}
	if cerr != nil {
		reason := fmt.Sprintf("step %s: the candidate composition failed at dispatch time: %v", stepID, cerr)
		if err := d.transition(TransitionRequest{Kind: TransBlocked, StepID: stepID, Reason: reason, SpawnN: d.currentSpawnN()}); err != nil {
			return false, fmt.Errorf("driver: step %s blocked write failed: %w", stepID, err)
		}
		return true, nil
	}
	d.mu.Lock()
	d.cands = m
	d.mu.Unlock()
	return false, nil
}

// validateCandidates refuses any candidate that does not name its harness
// (polyforge#175): refused before the dispatch rather than mid-chain.
func validateCandidates(m map[string][]string) error {
	for step, list := range m {
		for i, c := range list {
			if _, err := harness.ParseCandidate(c); err != nil {
				return fmt.Errorf("step %s: models[%d]: %w", step, i, err)
			}
		}
	}
	return nil
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
	// an owner ruling the answer recorded lands in its verbatim document
	// (outside the lock; idempotent, and re-checked at every Run start)
	d.landOwnerRulings()
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
// handoff artifact and, when present, the round's rewind verdict.

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
	// Candidate is the full resolved candidate string ("" = none in
	// effect): the identity the step's attempts are stamped with.
	Candidate string
	// Harness is the step's effective harness (polyforge#159; never ""
	// from the driver — pi when nothing chose otherwise).
	Harness string
	// Resume marks a re-dispatch of an in_progress step (answer
	// continuation, crash recovery, recovery-lane re-derivation) or of a
	// pending light-rewind target (polyforge#222: the ledger's light
	// cause derives the resume) — a resume of the previous dispatch's
	// conversation.
	Resume bool
	// Answers is the step's current attempt-chain Q&A — the answers
	// recorded since AnswerBase last advanced (st.Answers[st.AnswerBase:];
	// a gate-fail rewind, a rewind-range archive or a
	// root_cause_addressed:false report closes the chain) — and a resumed
	// step continues from it, never from a blank slate. The consumed
	// answers before that boundary stay in the durable archive for audit
	// but are not handed to a new dispatch of the round.
	Answers []capsule.ArchivedAnswer
	// RewindInput is the round's gate FAIL verdict (set from the rewind
	// write until the step is accepted; every dispatch of the round — the
	// first, a retry after an infra death, a restart — carries it).
	RewindInput *capsule.RewindInput
	// Fresh orders a new conversation for a retry (2nd+ failure, or a
	// candidate switch): no resume of the old transcript; the previous
	// transcript, when one is readable, rides the prompt as a read-only
	// reference (v2-converge 2.3 retry shape — a context-overflow style
	// failure must not repeat on every model).
	Fresh bool
	// ScopeTier is the verification tier this dispatch runs at
	//: "scoped" or "full". The prompt renders it on the
	// verification step; elsewhere it is inert. The value is the recorded
	// exec.ScopeTier, or the effective tier derived from the marker and
	// lane before it has been recorded.
	ScopeTier string
	// ReviewReentry marks a review dispatch with a prior
	// verdict-bearing round on record (steps.review.last_review_round,
	// which survives rewinds): the prompt then carries the shared
	// re-entry section (the round contract, the two faces, the
	// light-round mis-marking duty). False on every first round and on
	// every other step.
	ReviewReentry bool
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
	// LightRecheck, when set, makes this verification dispatch the light
	// recheck of the mechanical fast lane (polyforge#260): the step's
	// pending light recheck marker is present. The prompt carries the
	// light-recheck section (the bound commands per repo, the shared light
	// contract and the light output schema) beside the untouched step
	// definition, and the output schema shown is the light one. Nil = an
	// ordinary (full) dispatch.
	LightRecheck *LightRecheckOrder
	// EscalatedRecheck, when set, makes this verification dispatch the
	// ESCALATED full re-run of the mechanical fast lane (polyforge#260):
	// the step's ledger's latest non-infra_failure archived attempt is
	// the light path's in-place escalation (light_recheck_escalated). The
	// prompt carries the escalation note (the report's fast-lane section
	// duty) beside the untouched classification duty — an escalated full
	// FAIL may be classified mechanical again. False = an ordinary
	// dispatch.
	EscalatedRecheck bool
	// PlanConformance is the driver-computed two-way plan-conformance
	// fact set (planconformance.go): set on a review
	// dispatch of a lane with a done plan step and on a verification
	// dispatch whose entry declares plan_conformance; nil otherwise.
	PlanConformance *PlanConformance
	// Done is the set of steps whose execution record is done (an
	// inherit step's handoff producer must be one).
	Done map[string]bool
	// RecordedTreeHash is the capsule tree hash the step's latest
	// archived attempt recorded; "" = none (an older
	// capsule, or the latest attempt archived no hash). The re-dispatch
	// fast path requires it non-empty and equal to the current tree.
	RecordedTreeHash string
	// RetryTriggered reports that the latest archived attempt is a
	// death-triggered re-dispatch rather than a needs_input answer
	// continuation. It is derived from the existing
	// Attempts/Answers stamps, never a new persisted fact.
	RetryTriggered bool
	// RecordedAttemptAt is the archive stamp (InterruptedAt, else
	// ArchivedAt) of the step's latest archived attempt.
	// The fast path additionally requires the dispatch to run on the SAME
	// claim as that attempt: a re-claim re-materializes the capsule, which
	// restores only tracked and non-ignored untracked files, so the
	// fast path's "environment is intact" claim would be false. Derived
	// in memory from the existing ledger, never a new persisted fact.
	RecordedAttemptAt string
	// RewindRepoHash is the task-repo tree hash the gate that rewound this
	// round last judged ( criterion 5). A retry whose current
	// repos tree still equals it made no code progress since the rewind, so
	// the continue-in-place fast path must not claim there is work to
	// continue. Empty = unknown (the hash-error / older-record path keeps
	// the pre- wording). Derived from the step's newest rewind
	// archive and the gate's JudgedRepoHash, never a new persisted fact.
	RewindRepoHash string
}

// LightRecheckOrder is the light-mode order (polyforge#260): the
// mechanical bound — per failed check its repo, its exact command and
// the files it named — and the repair-diff base, rendered into the
// light dispatch's prompt.
type LightRecheckOrder struct {
	Bound    []mechBound
	DiffBase map[string]string
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
	// the recorded tier and the scoped-tier facts the scope
	// contract and the merge rule judge. VerificationScoped is the fact
	// that the (done) verification result carried a scope declaration —
	// it drives the review's scope_adequate obligation and the deliver
	// merge condition regardless of the current step.
	spec.ScopeTier = d.exec.ScopeTier
	spec.VerificationScoped = verificationScoped(d.exec.Steps[verificationStepID])
	// the review scope obligation keys on the work item's
	// opt-in (review runs before verification now, so verification's own
	// output cannot be its trigger).
	spec.ScopedWorkItem = d.scopeOptIn && scopedLanes[d.wi.BundleName]
	outputs := make(map[string]map[string]any, len(d.exec.Steps))
	for id, st := range d.exec.Steps {
		if st != nil && st.Status == capsule.StepDone {
			outputs[id] = st.Output
		}
	}
	if stepID == deliverStepID {
		spec.ReviewPassed = reviewPassed(d.exec.Steps[reviewStepID])
		spec.HasReview = d.workflow.step(reviewStepID) != nil
		spec.VerifiedHeads, spec.VerifiedTestBar, spec.VerifiedScenarios = verificationRecord(d.exec.Steps[verificationStepID])
		spec.LightRecheckHeads = lightRecheckHeads(d.exec.Steps[verificationStepID])
		spec.ReviewScopeAdequate = reviewScopeAdequate(d.exec.Steps[reviewStepID])
		spec.ReviewedHeads = reviewedHeadsOf(d.exec.Steps[reviewStepID])
		spec.ReviewScope, spec.ReviewScopePresent = reviewScopeRecord(d.exec.Steps[reviewStepID])
		if ver := d.exec.Steps[verificationStepID]; ver != nil {
			spec.VerificationScope, _ = parseScope(ver.Output["scope"])
		}
	}
	// polyforge#260: a pending light recheck marker mode-selects the LIGHT
	// output schema for this step's completed results — the light
	// recheck's own contract (verdict + checks, no full-bar fields:
	// authored_titles/scenarios/test_bar it must not fabricate) — instead
	// of the full one. The marker is the mode switch on both sides (the
	// dispatch's prompt and the settlement's validation).
	var lightMode bool
	if st := d.exec.Steps[stepID]; st != nil && st.PendingLightRecheck != nil {
		lightMode = true
	}
	d.mu.Unlock()
	spec.OutputSchema = d.outputSchema(stepID)
	if lightMode {
		if s := d.lightOutputSchema(); s != nil {
			spec.OutputSchema = s
		}
	}
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
		// deliver is exempt from the authored-tests gate. A machine-made
		// merge of the default branch passes the gate anyway (an edit
		// upstream made to an authored test is not the agent's —
		// mergedFromDefault, the verification step's own merge); what
		// deliver adds beyond that is a conflict it resolved by hand, after
		// its push and merge already happened, so rejecting its envelope
		// would retry an irreversible step. Verification passed on the
		// verified head; anything hand-made since is escalated to the human
		// by deliver_facts.go.
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

// lightOutputSchema answers the light recheck's output schema (the
// shared fast-lane material's frontmatter output block,
// workflows/_shared/mechanical-fix.md), cached; nil when the material
// declares none or it does not load — the full step schema then stays
// in effect (fail-closed to today's validation: the light envelope's
// violations are recorded, and the settlement's own judgment decides).
func (d *Driver) lightOutputSchema() *jsonschema.Schema {
	if d.bundleDir == "" {
		return nil
	}
	d.mu.Lock()
	if !d.lightSchemaLoaded {
		d.mu.Unlock()
		s, err := loadLightOutputSchema(d.bundleDir)
		if err != nil {
			// loud, never a silent nil: stderr AND a durable event
			fmt.Fprintf(os.Stderr, "driver: %s: %v (the light recheck's output is not schema-validated)\n", d.wi.ID, err)
			d.emit(EvOutputSchemaUnloadable, map[string]any{"step": verificationStepID + " (light)", "error": err.Error()})
			s = nil
		}
		d.mu.Lock()
		d.lightSchema, d.lightSchemaLoaded = s, true
	}
	s := d.lightSchema
	d.mu.Unlock()
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
// otherwise. A LIGHT pass never writes them (polyforge#260): a head
// verified only by a light recheck is never tests-covered — the last
// full pass's heads, if any, stand.
func (d *Driver) verifiedHeads(stepID string, res *StepResult) map[string]any {
	if stepID != verificationStepID || res.Outcome != OutcomeCompleted || d.wi.CapsuleDir == "" {
		return nil
	}
	d.mu.Lock()
	st := d.exec.Steps[stepID]
	light := st != nil && st.PendingLightRecheck != nil
	d.mu.Unlock()
	if light {
		return nil
	}
	return repoHeads(filepath.Join(d.wi.CapsuleDir, capsule.DirRepos))
}

// reviewedHeads reads the task repos' HEADs a completed review PASS
// records (; git I/O, so outside the transition's lock — the
// verifiedHeads pattern); nil otherwise. A FAIL verdict records nothing
// (the pass it would anchor does not exist).
func (d *Driver) reviewedHeads(stepID string, res *StepResult) map[string]any {
	if stepID != reviewStepID || res.Outcome != OutcomeCompleted || d.wi.CapsuleDir == "" {
		return nil
	}
	if v := topVerdict(res.Output); strings.ToLower(strings.TrimSpace(v)) != "pass" {
		return nil
	}
	return repoHeads(filepath.Join(d.wi.CapsuleDir, capsule.DirRepos))
}

// judgedHeads pre-reads the task repos' HEADs a VERDICT-BEARING review
// round judged ( code review; git I/O, so outside the
// transition's lock — the verifiedHeads pattern): recorded on the
// round's own LastReviewRound for every verdict-bearing round — a FAIL
// names the tree it failed, a PASS the tree it covered — so the next
// re-entry's fix surface has an anchor that survives rewinds (a PASS
// round's heads equal its reviewed_heads; a FAIL round has no
// reviewed_heads to point at). Nil for any non-review step or a
// verdict-less outcome (needs_input, incomplete), and for a repo set
// with no readable HEAD (the record then carries no heads at all).
func (d *Driver) judgedHeads(stepID string, res *StepResult) map[string]string {
	if stepID != reviewStepID || d.wi.CapsuleDir == "" {
		return nil
	}
	switch res.Outcome {
	case OutcomeCompleted:
		if v := topVerdict(res.Output); strings.ToLower(strings.TrimSpace(v)) != "pass" {
			return nil
		}
	case OutcomeGateFailed:
	default:
		return nil
	}
	heads := repoHeadShas(filepath.Join(d.wi.CapsuleDir, capsule.DirRepos))
	if len(heads) == 0 {
		return nil
	}
	return heads
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

// lightSettleFactsOf pre-reads the light recheck's settlement facts
// (polyforge#260; git I/O outside the transition's lock): the repair diff
// re-measured at settlement (deterministic — the same measurement the
// pre-dispatch phase made; nothing moves between, and a rogue change
// breaks the bound here, fail-closed) and the task repos' HEADs — the
// recheck heads the light-pass record carries. Answered for a completed
// verification result whose step holds the marker (the light recheck's
// own claim — the light pass or its escalation) and for a gate_failed
// one holding it (the recheck's own FAIL verdict — its escalation's audit
// wants the same re-measured diff). Nil otherwise.
func (d *Driver) lightSettleFactsOf(stepID string, res *StepResult) *lightSettleFacts {
	if stepID != verificationStepID || d.wi.CapsuleDir == "" {
		return nil
	}
	if res.Outcome != OutcomeCompleted && res.Outcome != OutcomeGateFailed {
		return nil
	}
	d.mu.Lock()
	var marker *capsule.LightRecheck
	if st := d.exec.Steps[stepID]; st != nil {
		marker = st.PendingLightRecheck
	}
	d.mu.Unlock()
	if marker == nil {
		return nil
	}
	facts := &lightSettleFacts{}
	bound, err := parseMechBound(marker.Bound)
	if err != nil {
		facts.diffBroken = "the mechanical classification is malformed: " + err.Error()
		return facts
	}
	facts.diffMeasured = true
	reposDir := filepath.Join(d.wi.CapsuleDir, capsule.DirRepos)
	var reason string
	facts.diff, reason = measureRepairDiff(bound, marker.DiffBase, reposDir)
	if reason != "" {
		facts.diffBroken = "the repair diff breaks the bound: " + reason
	}
	facts.heads = repoHeadShas(reposDir)
	return facts
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

// scopeChainFor builds the driver-written three-ring scope audit chain a
// completed scoped delivery must carry (;
// re-composes it): the opt-in marker, review's own scope declaration (the
// scope review judged adequate) and verification's scope — present only
// when the two declared the SAME path set (scopeConsistent, the union
// with a prior included) and review answered scope_adequate. It answers
// nil unless every ring is present and consistent — a full delivery, a
// delivery whose verification was full, one whose review never declared
// a scope, or one whose two scopes disagree carries no scoped chain and
// so no scoped merge credential. Pure over the in-memory
// already-persisted facts; the step-supplied value is deleted and this one
// written by the transition.
func (d *Driver) scopeChainFor(stepID string, res *StepResult) map[string]any {
	if stepID != deliverStepID || res.Outcome != OutcomeCompleted {
		return nil
	}
	d.mu.Lock()
	opted := d.scopeOptIn
	tier := d.exec.ScopeTier
	ver := d.exec.Steps[verificationStepID]
	rev := d.exec.Steps[reviewStepID]
	d.mu.Unlock()
	if !opted || tier != tierScoped {
		return nil
	}
	if ver == nil || ver.Status != capsule.StepDone || rev == nil || rev.Status != capsule.StepDone {
		return nil
	}
	sc, err := parseScope(ver.Output["scope"])
	if err != nil || sc == nil {
		return nil
	}
	revScope, present := reviewScopeRecord(rev)
	if !present || revScope == nil || !scopeConsistent(sc, revScope) {
		return nil
	}
	adequate, ok := rev.Output[scopeAdequateKey].(bool)
	if !ok {
		return nil
	}
	return map[string]any{
		"tier":   tierScoped,
		"marker": scopetier.Marker,
		"scope": map[string]any{
			"mode":           sc.Mode,
			"declared_paths": revScope.DeclaredPaths,
			"rationale":      sc.Rationale,
			"fast_gates_ref": sc.FastGatesRef,
		},
		scopeAdequateKey: adequate,
	}
}

// readWorkItemDoc reads runtime-state/work-item.json (the daemon-
// materialized task definition); a missing or torn file answers the zero
// doc.
func readWorkItemDoc(capsuleDir string) (capsule.WorkItemDoc, error) {
	var doc capsule.WorkItemDoc
	if capsuleDir == "" {
		return doc, fmt.Errorf("driver: no capsule dir")
	}
	raw, err := os.ReadFile(filepath.Join(capsuleDir, filepath.FromSlash(capsule.WorkItemJSON)))
	if err != nil {
		return doc, err
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, err
	}
	return doc, nil
}

// verdictFor assembles the gate FAIL policy the transition applies:
// the workflow's on_fail rewind target (absent = no rewind edge, the
// workflow fails on the gate) plus the verdict text — the result's
// output payload in full, or a synthesized note when the gate produced
// none. The deliver step's optional `rewind_to` overrides
// the declared target when it names an earlier step of the workflow: the
// human's 「全量重验」 answer rides it to re-run verification at full.
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
	// Only the deliver step honors `rewind_to`, and only for the one
	// destination the requirement forces: the human's 「全量重验」 answer
	// re-runs verification at full. It is honored only while the recorded
	// tier is scoped — a full-tier ticket keeps its declared on_fail
	// target. Every other value (code, spec, deliver, an unknown step, or
	// verification on a full ticket) is ignored.
	if stepID == deliverStepID && d.exec.ScopeTier == tierScoped {
		if target := rewindToOf(res.Output); target == verificationStepID {
			if _, ok := d.workflow.indexOf(target); ok {
				v.TargetStep = target
			}
		}
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

// effectiveCandidates answers the step's candidate list of the current
// dispatch (each `<harness>:<model>`, validated at composition): the list
// Config.Compose answered for it. The pointer recomputation, the switch and
// the exhaustion paths all walk this list — across harnesses. Without a
// bound composition no list is in effect.
func (d *Driver) effectiveCandidates(stepID string) []string {
	return d.cands[stepID]
}

// modelForLocked resolves the step's effective candidate for the NEXT
// dispatch (the candidate at the recomputed pointer over the dispatch's
// live list; "" when no candidate list is in effect).
func (d *Driver) modelForLocked(stepID string) string {
	list := d.effectiveCandidates(stepID)
	if len(list) == 0 {
		return ""
	}
	st := d.exec.Steps[stepID]
	if st == nil {
		return list[0]
	}
	// the answer era, not streakEra: the owner's continue starts the
	// death streaks afresh but keeps an advance a chain-end or no-chain
	// death still owes — the "edit the list, then pf resume" switch
	return list[CandidatePointer(st.Candidate, st.Attempts, answerEraStart(st.Answers), list)]
}

// grillQAPath is the capsule-relative document the owner rulings land
// in: the one file spec and grill-review already share.
const grillQAPath = "artifacts/grill-qa.md"

// ownerRulingsHeading opens the append-only owner-rulings section.
const ownerRulingsHeading = "## Owner rulings"

// ownerRulingMarker keys one ruling's block in grill-qa.md (question id
// + answer stamp): the append is idempotent across restarts.
func ownerRulingMarker(r capsule.OwnerRuling) string {
	return "<!-- owner-ruling: " + r.QuestionID + " " + r.AnsweredAt + " -->"
}

// landOwnerRulings appends every recorded owner ruling missing from
// artifacts/grill-qa.md to its `## Owner rulings` section (created at
// the end of the file on first use), the owner's words verbatim in a
// fenced block with the identity, the stamp, the question and the
// findings it addresses. The rulings are snapshotted under d.mu; the
// file I/O runs outside it. A failure is logged and retried at the next
// Run start — execution.json's machine copy stays the routing authority.
func (d *Driver) landOwnerRulings() {
	if d.wi.CapsuleDir == "" {
		return
	}
	d.mu.Lock()
	var rulings []capsule.OwnerRuling
	if st := d.exec.Steps[reviewStepID]; st != nil {
		rulings = append(rulings, st.OwnerRulings...)
	}
	d.mu.Unlock()
	if len(rulings) == 0 {
		return
	}
	path := filepath.Join(d.wi.CapsuleDir, filepath.FromSlash(grillQAPath))
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "driver: %s: owner rulings not landed (read %s): %v\n", d.wi.ID, grillQAPath, err)
		return
	}
	doc := string(raw)
	var blocks strings.Builder
	for _, r := range rulings {
		if strings.Contains(doc, ownerRulingMarker(r)) {
			continue
		}
		fence := "```"
		for strings.Contains(r.Answer, fence) {
			fence += "`"
		}
		fmt.Fprintf(&blocks, "\n### %s — %s, %s (question `%s`)\n\n%s\n\nFindings addressed: %s", r.Kind, r.AnsweredBy, r.AnsweredAt, r.QuestionID, ownerRulingMarker(r), strings.Join(r.Findings, ", "))
		if r.RoutedTo != "" {
			fmt.Fprintf(&blocks, "; routed to `%s`", r.RoutedTo)
		}
		fmt.Fprintf(&blocks, ".\n\n%stext\n%s\n%s\n", fence, r.Answer, fence)
	}
	if blocks.Len() == 0 {
		return
	}
	add := ""
	if !strings.Contains(doc, "\n"+ownerRulingsHeading+"\n") && !strings.HasPrefix(doc, ownerRulingsHeading+"\n") {
		if doc != "" && !strings.HasSuffix(doc, "\n") {
			add = "\n"
		}
		add += "\n" + ownerRulingsHeading + "\n\nAppend-only: the driver records each owner ruling here verbatim; review re-entry and the build step read it.\n"
	}
	add += blocks.String()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		err = atomicfile.Write(path, []byte(doc+add), 0o644)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "driver: %s: owner rulings not landed (write %s): %v\n", d.wi.ID, grillQAPath, err)
	}
}

// reproHeads pre-reads the task repos' HEADs a completed repro result ran
// on (git I/O, so outside the transition's lock — the verifiedHeads
// pattern): the bug-fix lane's pre-fix base candidate. Nil otherwise.
func (d *Driver) reproHeads(stepID string, res *StepResult) map[string]string {
	if stepID != reproStepID || res.Outcome != OutcomeCompleted || d.wi.CapsuleDir == "" {
		return nil
	}
	d.mu.Lock()
	recorded := d.exec.PreFixHeads != nil
	d.mu.Unlock()
	if recorded {
		return nil
	}
	heads := repoHeadShas(filepath.Join(d.wi.CapsuleDir, capsule.DirRepos))
	if len(heads) == 0 {
		return nil
	}
	return heads
}
