// Package driver implements the polyforge v2 execution driver: the Go
// replacement for the LLM-driven WI Agent inside the daemon. The driver
// reads runtime-state/execution.json to determine the current state,
// spawns step processes (harness sessions) directly, reacts to each
// dispatch's accepted `pf step handoff` as the capsule's call ledger
// records it (handoff.go), writes execution.json exclusively through the
// single transition entry point (transition.go), and advances the
// workflow on those hand-offs.
//
// Execution semantics are the frozen v1 workflow contract
// (docs/design/polyforge-vnext/01-workflow-skills.md §6): the
// sequential success path, bounded on_fail rewind on business gate FAIL
// (the gate itself never archived nor reset — INV-1d/INV-1g), in-place
// resume on infrastructure failure (the attempt archive and candidate
// advance belong to the daemon's recovery path; the core loop
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

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/crashpoint"
	"github.com/silgrid/polyforge/internal/polyforge/pendingwrites"
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
	// BundleDir is the materialized workflow bundle root (workflow.yaml
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
	// recovery path's write, performed by the single
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
	// overrides in workflow.yaml apply on top. Zero fields take the
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
	// Admission is the daemon's heavy-run admission queue a dispatch that
	// needs a heavy-run slot asks before it runs (resources.go; a step
	// declaring `admission: heavy`); nil = admitted at once (tests, a
	// daemon without the queue).
	Admission VerifyAdmission
	// Forge is the daemon's binding to the task repos' forge
	// (resources.go): the command environment git and gh run with (the
	// machine's own credentials) and the per-repo merge lock; nil = none,
	// and a step declaring the merge lock blocks naming it.
	Forge Forge
	// OwnerWait tells the runtime whether this work item's step waits on
	// the owner (`pf ask`, ask.go): true while an ask of the current
	// dispatch is open — the daemon's slot accounting then counts no
	// running slot for the job — false before the next dispatch. nil =
	// no accounting (tests).
	OwnerWait func(waiting bool)
	// AskPoll is how often a running dispatch's asks.jsonl is re-read (0 =
	// DefaultAskPoll, 1 s; the owner ruling bounds it at 2 s).
	AskPoll time.Duration
	// HubWrites makes the hub writes a step round registered pending
	// (`pf step hub`, workflow contract 4) when it hands off done: the
	// daemon's aihub binding (pendingwrites.Sink). nil = none: a round
	// with a pending write parks write_failed (tests).
	HubWrites pendingwrites.Sink
	// Language answers the live agent language the daemon's opening and
	// event sections of artifacts/handoff.md are stamped in.
	Language func() string
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

	// claimRepos are the task repos the daemon materialized for the claim
	// (capsule.json repos, read once in New).
	claimRepos []string

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

	// unbooked is the wall time of finished dispatches not yet written
	// into the step's cumulative runtime (the T accumulator): folded into
	// the next result/failure write.
	unbooked map[string]int64
	// waitingAnnounced is the question id step_waiting_human was last
	// sent for (one fact per question, however often the loop re-plans).
	waitingAnnounced string
	// inputEventFor is the stamp of the ask (`pf ask`, ask.go) the
	// awaiting_owner event was last emitted for in this driver life (a
	// restarted driver re-announces an ask it finds open).
	inputEventFor string
	// parked is the open ask of the current dispatch the driver last read
	// from the capsule's asks.jsonl (ask.go): the lock-free fact the
	// settled-dispatch test reads; nil = none.
	parked *capsule.AskLine
	// relay carries owner messages to the live dispatch's wait loop
	// (relay.go: awaitDispatch takes them); liveStep / liveSpawn name
	// that dispatch (0 = none: the plan pass resumes the session to
	// carry the message); messageResume names the step whose dispatch
	// ended awaiting the owner, so its next resume sends only the owner
	// messages.
	relay         chan *relayRequest
	liveStep      string
	liveSpawn     int64
	messageResume string
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
	// the steps the claim fixed as skipped (the capsule's
	// file, never the workspace yaml) that the lane declares skippable.
	wf = wf.WithSkipped(ReadSkippedSteps(cfg.WI.CapsuleDir))

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
	claimRepos := ClaimRepos(cfg.WI.CapsuleDir)
	return &Driver{
		wi:          cfg.WI,
		workflow:    wf,
		exec:        exec,
		execPath:    execPath,
		spawnN:      spawnN,
		initialized: existed,
		cfg:         cfg,
		log:         cfg.Log,
		wake:        make(chan struct{}, 1),
		relay:       make(chan *relayRequest),
		unbooked:    make(map[string]int64),
		claimRepos:  claimRepos,
	}, nil
}

// ClaimRepos answers the task repos the daemon materialized for the
// claim — capsule.json's repos, sorted — nil when the file is unreadable.
// A directory a step created under repos/ is not one of them.
func ClaimRepos(capsuleDir string) []string {
	raw, err := os.ReadFile(filepath.Join(capsuleDir, capsule.CapsuleJSON))
	if err != nil {
		return nil
	}
	var idn capsule.Identity
	if json.Unmarshal(raw, &idn) != nil {
		return nil
	}
	names := make([]string, 0, len(idn.Repos))
	for name := range idn.Repos {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
		// workflow contract 4: the hand-off document opens at the claim
		// (written once; a crash before the init write finds it there)
		d.writeHandoffOpening()
		if err := d.transition(TransitionRequest{Kind: TransInit, SpawnN: d.currentSpawnN()}); err != nil {
			return fmt.Errorf("driver: execution init failed: %w", err)
		}
	}
	// a question the daemon worded itself before the
	// upgrade is withdrawn; its step runs again under the new rules
	// polyforge#79 era: the recovery path's interrupted-dispatch
	// archive (the recovery lane triggers it; the single execution.json
	// writer performs it). ONE audit-only infra_failure attempt for the
	// derivation that died without a result, BEFORE the re-derivation
	// dispatches. It is not model-class, so it never moves the candidate
	// pointer; an operator shutdown never reaches this hook (the daemon
	// filters it).
	collected, collectedN := d.unconsumedResult()
	// A dispatch that asked the owner is settled by its ask (ask.go): the
	// open ask is read before the interruption archive and the claim
	// re-entry below decide what the dispatch they found needs — nothing,
	// when it awaits the owner; the plan pass parks on it (lifecycle (i)).
	d.refreshParkedAsk()
	defer d.ownerWait(false)
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
			if stop, err := d.settle(ctx, res.StepID, collectedN, res, nil); stop {
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
		// step_start but before the spawn journal line.
		if atBoundary, fresh := d.rewindReentryOrder(stepID); atBoundary {
			if fresh {
				spawn.Resume = false
			} else if spawn.Resume {
				// the rewound target's first dispatch of the round, resumed
				// across the crash: still the rewind re-entry
				spawn.RewindResume = true
			}
		}
		// the owner messages no dispatch delivered ride this one's first
		// message (relay.go): a resume of the conversation that ended
		// awaiting the owner sends them alone, any other dispatch the full
		// prompt with them as a section; the dispatch marks them delivered
		// once its first turn starts
		spawn.OwnerMessages = d.undeliveredMessages(stepID)
		spawn.MessageResume = d.takeMessageResume(stepID) && len(spawn.OwnerMessages) > 0

		if err := d.transition(TransitionRequest{
			Kind:      TransStepStart,
			StepID:    stepID,
			Model:     spawn.Model,
			Effort:    spawn.Effort,
			Candidate: spawn.Candidate,
			SpawnN:    n,
			// the task repos' HEADs, read outside the lock: the round's
			// start commits when this dispatch opens the step's round
			StartHeads: d.repoHeads(),
		}); err != nil {
			return fmt.Errorf("driver: step %s start failed: %w", stepID, err)
		}
		spawn.Round = d.stepRound(stepID)

		// contract 2: the step's declared resources are held for the
		// dispatch (resources.go: admission, then the locks; all or nothing)
		releaseResources, err := d.acquireStepResources(ctx, stepID)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			if stop, err := d.blockOnResources(stepID, err); stop {
				return err
			}
			continue
		}
		dispatchedAt := d.now()
		d.ownerWait(false) // the slot is this dispatch's again
		run, err := d.cfg.Spawner.SpawnStep(ctx, *spawn)
		if err != nil {
			releaseResources()
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
		releaseResources() // the dispatch's process is gone: what it held is free
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
		var awaiting *awaitingOwnerEnd
		if errors.As(werr, &awaiting) {
			// The dispatch asked the owner and its process is gone with the
			// ask still open (ask.go): no death, no result. The wall booked
			// into T ends where the wait began, the step-end hook runs (the
			// process is gone), and the plan pass parks on the ask.
			d.addUnbookedUntil(stepID, bookedFrom, awaiting.Since)
			if d.cfg.StepEnded != nil && ctx.Err() == nil {
				d.cfg.StepEnded(ctx)
			}
			d.announceEnd(stepID, n, supervise.OutcomeAwaitingOwner, 0, "")
			// the owner's message resumes this conversation (relay.go):
			// the next dispatch of the step sends only the messages
			d.noteMessageResume(stepID)
			continue
		}
		if d.cfg.StepEnded != nil && ctx.Err() == nil {
			d.cfg.StepEnded(ctx)
		}
		if verdict != nil {
			// The supervisor judged the dispatch dead. The process is
			// already torn down; this is the dispatch's one failure record.
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
		if stop, err := d.settle(ctx, stepID, n, res, run); stop {
			return err
		}
	}
}

// unconsumedResult answers the hand-off of the current dispatch that the
// call ledger records but the driver never settled — a predecessor died
// after the step handed off, or a park (write_failed) left it for the
// resume: the current step is in_progress and awaits exactly the current
// ordinal (Step.Dispatch). Nil when there is none; the loop then
// dispatches as usual.
func (d *Driver) unconsumedResult() (*StepResult, int64) {
	d.mu.Lock()
	n := d.spawnN
	stepID := d.exec.CurrentStep
	st := d.exec.Steps[stepID]
	awaiting := d.exec.WorkflowStatus == StatusRunning && st != nil && st.Status == capsule.StepInProgress &&
		n > 0 && st.Dispatch == n
	d.mu.Unlock()
	if !awaiting || d.wi.CapsuleDir == "" {
		return nil, 0
	}
	if res := ledgerHandoff(d.wi.CapsuleDir, d.wi.ID, stepID, n); res != nil {
		return res, n
	}
	return nil, 0
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
// step_start but before its spawn-journal line ( item 6):
// atBoundary when such a dispatch is owed, forceFresh when its shape is a
// new conversation.
// One rule covers every step the rewind re-enters: a resume never crosses
// a rewind boundary. The boundary is the newest archive that reset or
// re-entered the step — its own gate_fail_rewind* (the target) or
// superseded_by_rewind attempt (a step between target and gate), or, for
// the gate, any archive carrying that gate's rewind_gate.
//
// Until a dispatch of the new round is journaled, a superseded step and
// the gate start fresh; the rewound target keeps its resume of the
// previous round, which is still the step's latest
// journaled conversation. Archived infra_failure attempts after the
// boundary (daemon_restart, driver_relaunch, a pre-journal spawn failure's
// step_failed) are audit only and never move it; the spawn journal is the
// bound, after which the ordinary resume is correct.
//
// The verdict is NOT this function's business: it stays in
// steps.<id>.pending_rewind_input for the whole round, so every dispatch
// of the round — this crash re-entry, a post-journal retry, the restart
// after a daemon death — reads it from there and no restoration path
// exists.
func (d *Driver) rewindReentryOrder(stepID string) (atBoundary, forceFresh bool) {
	d.mu.Lock()
	st := d.exec.Steps[stepID]
	if st == nil || st.Status != capsule.StepInProgress {
		d.mu.Unlock()
		return false, false
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
		return false, false
	}
	// The rewound target keeps its resume (every rewind
	// target that is not a gate resumes); a superseded step and the gate
	// start a new conversation.
	return true, !(own && rewindTargetCause(boundary.Cause) && d.rewindResumes(stepID))
}

// rewindTargetCause reports whether an attempt is a rewind archive's target
// record (the step a gate FAIL reset), as opposed to the collateral
// superseded_by_rewind records or an infra_failure.
func rewindTargetCause(cause string) bool {
	switch cause {
	case CauseGateFailRewind:
		return true
	}
	return false
}

// rewindArchive reports whether the attempt cause is a rewind archive
// (01 §6.3b): the target/superseded rollback records. infra_failure and
// the accepted/audit causes are not.
func rewindArchive(cause string) bool {
	switch cause {
	case CauseGateFailRewind, CauseSupersededByRewind:
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

// settle reacts to one collected hand-off of dispatch n (run is nil for a
// hand-off a dead predecessor's dispatch left on the ledger: there is no
// process to tear down; a live one was torn down when it was collected).
// stop reports that Run must return err; otherwise the loop plans again.
func (d *Driver) settle(ctx context.Context, stepID string, n int64, res *StepResult, _ StepRun) (stop bool, err error) {
	if res.Handoff == nil {
		return true, fmt.Errorf("driver: step %s dispatch %d: a result without its hand-off (the call ledger is the one source of a result)", stepID, n)
	}
	return d.settleHandoff(ctx, stepID, n, res)
}

// plan is one selection pass: terminal or blocked workflows stop the
// loop (nil error); a current dispatch with an open `pf ask` parks the loop until
// the owner's reply (or another transition) wakes it; otherwise the
// earliest step neither done nor skipped (next_step_by_order, 01 §6.5) is
// dispatched — unless the workflow skips it (StepSpec.Skipped):
// then it is recorded skipped and the pass selects again —
// and an empty selection over a running workflow is the §6.5 step 6
// check: every step done or skipped, so the loop writes completed and
// stops.
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
		var next string
		skip := false
		if !stop {
			for _, spec := range d.workflow.Steps {
				st := d.exec.Steps[spec.ID]
				if st == nil || (st.Status != capsule.StepDone && st.Status != capsule.StepSkipped) {
					next = spec.ID
					skip = spec.Skipped && (st == nil || st.Status == capsule.StepPending || st.Status == "")
					break
				}
			}
		}
		spawnN := d.spawnN
		d.mu.Unlock()

		if stop {
			return true, "", nil
		}
		if ask := d.parkedAsk(); ask != nil {
			// the current dispatch asked the owner (`pf ask`, ask.go) and
			// the ask is open: the step awaits the owner — no dispatch, no
			// stall clock, the slot released; a restart lands here too
			d.announceParkedAsk(ask)
			if err := d.awaitParkedAsk(ctx, ask); err != nil {
				return false, "", err
			}
			continue // re-plan: the ask closed (or the state moved on)
		}
		if skip {
			if err := d.transition(TransitionRequest{
				Kind:   TransStepSkipped,
				StepID: next,
				SpawnN: spawnN,
			}); err != nil {
				return false, "", fmt.Errorf("driver: step %s skip transition failed: %w", next, err)
			}
			continue
		}
		if next == "" {
			// §6.5 step 6: not a failure terminal (that exits above),
			// so an empty selection means every step is done or skipped.
			if err := d.transition(TransitionRequest{
				Kind:   TransTerminal,
				Status: StatusCompleted,
				SpawnN: spawnN,
			}); err != nil {
				return false, "", fmt.Errorf("driver: workflow completion transition failed: %w", err)
			}
			d.writeCompletionEvent()
			return true, "", nil
		}
		return false, next, nil
	}
}

// prepareDispatch snapshots everything the spawn needs from under the
// mutex: the resume flag (an in_progress step is a re-dispatch — answer
// continuation, crash recovery, recovery-lane re-derivation; a pending
// rewind target is the rewind re-entry, its resume derived from the
// ledger), the retry's conversation shape and the effective model
// candidate. Parking on an open
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
		if sp.Resume && st.Supervision != nil && st.Supervision.NextDispatch == capsule.NextDispatchFresh {
			sp.Fresh = true
			sp.Resume = false
		}
		// The rewind re-entry (generalizing polyforge#222's
		// light one): a PENDING step — a rewound target — whose ledger's
		// latest non-infra_failure archived attempt is a rewind target
		// archive re-enters as a resume of its previous dispatch's
		// conversation, whatever the verdict's findings were; a gate
		// target never resumes (a judge never continues the judgment its
		// verdict closed). The order is that ledger fact — the same single
		// write that routed the verdict archived it: nothing to write,
		// nothing to consume, equally durable. SpawnStep decides whether
		// the resume is possible and degrades to a new conversation (the
		// previous transcript as reference) when it is not: another
		// harness, a gone session, or a previous dispatch that handed off
		// nothing.
		if !sp.Resume && st.Status == capsule.StepPending && d.rewindResumes(stepID) && rewindResumeContext(st.Attempts) {
			sp.Resume = true
			sp.RewindResume = true
		}
		// the fast-path inputs — the tree hash the step's
		// latest failed attempt archived and whether there is one. Both
		// come from the existing ledger (no new persisted state); a step
		// with no archived attempt carries neither.
		if len(st.Attempts) > 0 {
			last := st.Attempts[len(st.Attempts)-1]
			sp.RecordedTreeHash = last.TreeHash
			sp.RetryTriggered = last.Cause != ""
			sp.RecordedAttemptAt = last.InterruptedAt
			if sp.RecordedAttemptAt == "" {
				sp.RecordedAttemptAt = last.ArchivedAt
			}
		}
	}
	if st != nil && len(st.Attempts) > 0 && st.Attempts[len(st.Attempts)-1].FailureCause == FailEndedWithoutResult {
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
	sp.ToolWindow = d.workflow.step(stepID).toolWindow()
	sp.SpawnN = d.spawnN + 1
	return sp, sp.SpawnN, nil
}

// stepRound answers the step's current round (capsule.Step.Round; 0 before
// its first step_start).
func (d *Driver) stepRound(stepID string) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if st := d.exec.Steps[stepID]; st != nil {
		return st.Round
	}
	return 0
}

// composeDispatch composes the candidate lists for the dispatch about to
// start (polyforge#33) — file reads, so outside the mutex — and holds them
// for it. A composition that fails (an unreadable or invalid yaml or
// bundle, a malformed candidate) blocks the workflow through TransBlocked:
// the owner fixes the config and `pf resume` re-dispatches from the
// checkpoint. It answers blocked=true when it parked the workflow.
func (d *Driver) composeDispatch(stepID string) (blocked bool, err error) {
	var m map[string][]string
	var cerr error
	if d.cfg.Compose != nil {
		m, cerr = d.cfg.Compose()
		if cerr == nil {
			cerr = validateCandidates(m)
		}
		if cerr != nil {
			cerr = fmt.Errorf("the candidate composition failed at dispatch time: %w", cerr)
		}
	}
	if cerr != nil {
		reason := fmt.Sprintf("step %s: %v", stepID, cerr)
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
	// WIID is the parent work item.
	WIID string
	// SpawnN is this derivation's ordinal — the step process's fence
	// identity; its hand-off is recorded under it.
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
	// Round is the step round the dispatch belongs to (capsule.Step.Round
	// after its step_start; PF_ROUND in the step process's environment).
	Round int64
	// ToolWindow is the step's declared tool window (StepSpec.ToolMinutes;
	// zero = none: the daemon's global window), the supervisor's Y for
	// this dispatch and the step process's PF_TOOL_WINDOW_MS.
	ToolWindow time.Duration
	// Resume marks a re-dispatch of an in_progress step (answer
	// continuation, crash recovery, recovery-lane re-derivation) or of a
	// pending rewind target (every rewind into a non-gate
	// step; the ledger's rewind archive derives the resume) — a resume of
	// the previous dispatch's conversation.
	Resume bool
	// RewindResume marks the resume as a rewind re-entry (a pending
	// target, rewindResumeContext): the adapter then also refuses to
	// resume a previous dispatch that handed nothing off (the call ledger
	// records no hand-off of it) — the third degradation condition beside
	// another harness and a gone session.
	RewindResume bool
	// Fresh orders a new conversation for a retry (2nd+ failure, or a
	// candidate switch): no resume of the old transcript; the previous
	// transcript, when one is readable, rides the prompt as a read-only
	// reference (v2-converge 2.3 retry shape — a context-overflow style
	// failure must not repeat on every model).
	Fresh bool
	// PriorEndedWithoutResult tells the retry that the previous dispatch's
	// turn ENDED without `pf step handoff` (cause ended_without_result).
	PriorEndedWithoutResult bool
	// RecordedTreeHash is the capsule tree hash the step's latest
	// archived attempt recorded; "" = none (an older
	// capsule, or the latest attempt archived no hash). The re-dispatch
	// fast path requires it non-empty and equal to the current tree.
	RecordedTreeHash string
	// RetryTriggered reports that the step has an archived attempt (a
	// non-zero one): this dispatch is a death-triggered re-dispatch
	//. It is derived from the existing Attempts ledger,
	// never a new persisted fact.
	RetryTriggered bool
	// RecordedAttemptAt is the archive stamp (InterruptedAt, else
	// ArchivedAt) of the step's latest archived attempt.
	// The fast path additionally requires the dispatch to run on the SAME
	// claim as that attempt: a re-claim re-materializes the capsule, which
	// restores only tracked and non-ignored untracked files, so the
	// fast path's "environment is intact" claim would be false. Derived
	// in memory from the existing ledger, never a new persisted fact.
	RecordedAttemptAt string
	// OwnerMessages are the step's owner messages no dispatch delivered
	// (capsule.Undelivered, relay.go): this dispatch's first message
	// carries them, and the dispatch marks them delivered once its first
	// turn starts. nil = none.
	OwnerMessages []capsule.AskLine
	// MessageResume: the step's previous dispatch ended awaiting the
	// owner with its process gone, so a resume of its conversation sends
	// the owner messages alone (the conversation holds the prompt and the
	// question already); a resume that degrades to a new conversation,
	// or a dispatch after a death, sends the full prompt with them.
	MessageResume bool
}

// StepRun is one spawned step execution awaiting its hand-off.
type StepRun interface {
	// Wait blocks until the step process's hand-off is on the call ledger.
	// An error means no business result (infrastructure failure or
	// cancellation): the recovery lane owns what follows.
	Wait(ctx context.Context) (*StepResult, error)
	// Terminate tears the step process down, bounded.
	Terminate(bound time.Duration) error
}

// parseOutput coerces a routed result's output (StepResult.Output) to a
// JSON object map; an absent payload is an empty object.
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

// repoDir answers a task repo's clone in the capsule.
func (d *Driver) repoDir(repo string) string {
	return filepath.Join(d.wi.CapsuleDir, capsule.DirRepos, repo)
}

// orText answers s, or alt when s is blank.
func orText(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

// containsStr reports whether list holds s.
func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// verdictFor assembles the gate FAIL policy the transition applies: the
// workflow's on_fail rewind target (absent = no rewind edge, the workflow
// fails on the gate) and whether the fail cites the owner.
func (d *Driver) verdictFor(stepID string, res *StepResult) *GateVerdict {
	v := &GateVerdict{
		GateStepID: stepID,
		// workflow contract 4: a fail citing the owner is charged to nothing
		PerOwner: res.Handoff != nil && res.Handoff.PerOwner != "",
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
	// no streak era: the owner's continue starts the death streaks afresh
	// (streakEra) but keeps an advance a chain-end or no-chain death still
	// owes — the "edit the list, then pf resume" switch
	return list[CandidatePointer(st.Candidate, st.Attempts, list)]
}
