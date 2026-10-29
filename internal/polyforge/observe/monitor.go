package observe

// monitor.go: the per-WI SUPERVISOR goroutine (v2-converge 2.1-2.3; the
// guessing lanes — hang, idle, probe/ack, wall, turn-duration, the tool
// and question exemptions, no_progress — are gone). ONE goroutine
// supervises ONE work item; its only execution shape is a select loop
// whose cases never block. Every long operation (a push, a session
// verify) runs in a sub-goroutine and reports back through a buffered
// channel.
//
// Input sources drained by the select:
//   - the driver's FACTS (supervise.Link.Facts: step_started,
//     step_waiting_human/answered, step_ended, exec_committed,
//     step_retry_wait) — an unbounded in-process queue, never dropped;
//   - the confirmed driver-exit signal (Handle.Exit) → recovery;
//   - RT messages (RTRecovered rides rtQ; RTFinished rides stopCh);
//   - results of the sub-goroutines (push / verify);
//   - the tick (judging, push retry backoff, backpressure, M4).
//
// JUDGING: per dispatch the supervisor holds the harness Tracker the
// translator folds the step process's records into (only real work
// counts; UI noise does not) and applies the rules of package
// supervise each tick; the first rule that fires becomes ONE step_dead
// verdict on the link, after which the supervisor stays silent for that
// spawn until step_ended. State is per dispatch: created on
// step_started, dropped on step_ended. Between steps (and not waiting on
// a human) the supervisor also times the DRIVER: a driver_stalled
// verdict plus a recovery request.
//
// QUIET: after WINeedsRecovery is sent (driver exit / stall) the monitor
// neither judges nor raises recovery until RTRecovered adopts a new
// derivation; after a daemon-initiated close (MarkClosing) it stays so.
// Pushes go on: the capsule chain keeps being published. A superseded
// session stops the monitor (session.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/sessionjsonl"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// PushFunc is the injectable capsule commit+push operation (the seam the
// tests use; production binds gitops.CommitPush). ctx bounds the call;
// returning ctx.Err() reports the attempt as failed while the underlying
// git keeps running — the per-WI git lock serializes any successor. The
// returned timings are the observed-only latency samples (commit_ms /
// push_ms) the published event bodies carry; nothing judgmental consumes
// them.
type PushFunc func(ctx context.Context, o gitops.Opts, message string) (localSHA string, pushed bool, timings gitops.PushTimings, err error)

// WIInfo carries the per-WI static facts a monitor needs; supplied by
// Runtime alongside the RTStarted protocol message.
type WIInfo struct {
	WorkItem   string
	CapsuleDir string
	Git        gitops.Opts
}

// Handle is the driver face a monitor watches (the runtime's agent
// handle); Observe owns no process code.
type Handle interface {
	// Exit is closed once the supervised driver has returned: the
	// confirmed exit.
	Exit() <-chan struct{}
}

type pushResult struct {
	version int64
	sha     string
	pushed  bool
	timings gitops.PushTimings
	err     error
}

// monitorCfg carries the tunables (defaults in observe.go Config).
type monitorCfg struct {
	PollInterval time.Duration
	// ModelErrorGrace is rule 1's grace after an error-ended turn.
	ModelErrorGrace time.Duration
	// DriverStall is N: the driver between steps (not waiting on a
	// human) for longer than this is stalled. <= 0 disables the clock.
	DriverStall time.Duration
	// LiveLimits, when set, answers the CURRENT supervision defaults at
	// every tick (a live reload's T and N).
	LiveLimits func() supervise.Defaults
	// PushRetryBackoff is the first retry delay after a failed push; each
	// further failure doubles it up to PushBackoffMax.
	PushRetryBackoff time.Duration
	PushBackoffMax   time.Duration
	PushBound        time.Duration
	// revalidate is the v1.5 M4 session-verify cadence (0 = disabled:
	// no verifier, no checks — the unit-test default).
	revalidate time.Duration
	// verify is the M4 aihub seam (nil = revalidation disabled).
	verify SessionVerifier
	push   PushFunc
	// locateLive resolves the live harness transcript on demand (F3);
	// nil = the transcript sync is disabled.
	locateLive func(capsuleDir string, spawnAt time.Time, harness, session string) string
}

// driver phases the supervisor tracks.
const (
	phaseBetween = "between_steps"
	phaseInStep  = "in_step"
	phaseWaiting = "waiting_human"
)

// observationGapFactor: a tick arriving later than this many poll
// intervals after the previous one means the SUPERVISOR was stopped (a
// host or daemon freeze), not the dispatch. The span it did not watch is
// removed from every judged duration; one supervisor_paused event records
// it. The factor keeps ordinary scheduler jitter (a few missed ticks) out.
const observationGapFactor = 10

// dispatchState is the supervisor's per-dispatch state (created on
// step_started, dropped on step_ended).
type dispatchState struct {
	spawnN    int64
	step      string
	startedAt time.Time
	candidate string // the dispatch's `<harness>:<model>[:<effort>]` ("" = the harness default model)
	tracker   *supervise.Tracker
	verdicted bool
	// waitsBefore is the step's wait-call total over the earlier
	// dispatches this monitor watched (set before the state is published,
	// never changed after: readers need no monitor-goroutine access).
	waitsBefore waitTotal
}

// waitTotal is a step's folded wait-call count and summed seconds
// (supervise wait.go): counted and surfaced, never judged.
type waitTotal struct {
	calls int
	secs  int64
}

// Monitor supervises one work item.
type Monitor struct {
	wi   WIInfo
	cfg  monitorCfg
	log  *EventLog
	ob   *supervise.Queue[protocol.OBMsg]
	link *supervise.Link

	// spawnN is the v1.5 B2 derivation counter of the CURRENTLY ADOPTED
	// derivation (guarded by stateMu for the HTTP fence's read). It is
	// fed from RTMsg.SpawnN at RTStarted/RTRecovered adoption.
	spawnN int
	// sessionID is the v1.5 session secret carried in-process on the
	// RTStarted/RTRecovered message (M4 revalidation input). It lives
	// ONLY here — never in events, never in the capsule, never in a log
	// line.
	sessionID string
	exitCh    <-chan struct{}

	rtQ    *supervise.Queue[Attach]
	stopCh chan struct{}
	stop   sync.Once
	// done is closed once run has returned (Observe.Release waits on it).
	done chan struct{}

	// supervision state (monitor goroutine; the activity face reads the
	// published copy under stateMu).
	cur        *dispatchState
	phase      string
	phaseSince time.Time
	stalled    bool // the driver stall was raised for the current gap
	// stepWaits folds each ended dispatch's wait calls into its step's
	// total, so the status and watch faces read a per-step figure across
	// retries (monitor goroutine only; nil until the first fold).
	stepWaits map[string]waitTotal
	// lastTick is the monitor's own heartbeat: the instant of the last
	// tick it ran. A much later tick declares its own observation gap.
	lastTick time.Time

	// execution.json state, refreshed on driver facts (monitor goroutine).
	dirtyVersion  int64 // latest Version seen that has no confirmed push
	pushedVersion int64

	// the single-in-flight push gate.
	gate     chan struct{}
	pushDone chan pushResult

	// push retry backoff (monitor goroutine only): after a failure the
	// same Version is retried at retryAt; the delay doubles per failure
	// up to PushBackoffMax. A newer Version is pushed at once; only a
	// success resets the delay. backpressured: the episode's one
	// push_backpressure event (the delay reached its cap) was written.
	failedPushes  int
	failedVersion int64
	backoff       time.Duration
	retryAt       time.Time
	backpressured bool

	// live-transcript sync state (F3; monitor goroutine only — the push
	// sub-goroutines receive the values as arguments, never over m).
	trDst            string    // the capsule's declared session path (latest step_started)
	trSrc            string    // the live path when known (a resumed dispatch writes into the capsule)
	trSpawnAt        time.Time // the dispatch's birth moment
	trHarness        string    // the dispatch's step harness (polyforge#159)
	trSession        string    // its harness-native session id (claude's uuid)
	transcriptWarned bool      // one transcript_missing event per dispatch

	// stateMu guards the fields every goroutine (HTTP view, push
	// sub-goroutines) reads.
	stateMu sync.Mutex
	// quiet: a recovery was requested — no judging, no recovery request,
	// no revalidation until RTRecovered; closing: the same for good, a
	// daemon-initiated close is under way (see the file comment).
	quiet, closing bool
	// act is the published activity copy of the current dispatch (the
	// ops status face reads it): nil outside a dispatch.
	act *dispatchState

	// revalidation bookkeeping (M4; monitor goroutine only except where
	// noted). verifyDone is created with the monitor (never nil) so the
	// select case below is always backed by a real channel.
	lastVerifyAt   time.Time
	verifyInFlight bool
	verifyDone     chan verifyResult
}

func (m *Monitor) execJSONPath() string {
	return filepath.Join(m.wi.CapsuleDir, capsule.ExecutionJSON)
}

// ---------------------------------------------------------------------------
// main loop

// announceStart appends the job_started event. The attaching side calls
// it synchronously before launching run(): launching first would leave a
// scheduling gap in which an event from another writer of the shared log
// (the driver's EventLogger) could land ahead of job_started.
func (m *Monitor) announceStart() {
	_ = m.log.Append(m.wi.WorkItem, EvJobStarted, map[string]any{
		"spawn_n": m.spawnN,
	})
}

func (m *Monitor) run() {
	defer func() {
		_ = m.log.Append(m.wi.WorkItem, EvJobStopped, map[string]any{
			"spawn_n": m.currentSpawn(),
		})
	}()
	// job_started is announced by the attaching side (announceStart)
	// BEFORE this goroutine is scheduled, so no event appended after the
	// attach can precede it in the log.
	// the seed read: a restarted supervisor learns the capsule's state
	// (pending question, version to publish) before any fact arrives
	m.refreshExec(0)
	ticker := time.NewTicker(m.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			m.awaitPushInFlight()
			return
		case <-m.rtQ.Notify():
			for at, ok := m.rtQ.Pop(); ok; at, ok = m.rtQ.Pop() {
				m.onRecovered(at)
			}
		case <-m.exitCh:
			m.onExit()
		case <-m.link.Facts.Notify():
			m.drainFacts()
		case <-ticker.C:
			m.onTick()
		case res := <-m.pushDone:
			m.onPushResult(res)
		case res := <-m.verifyDone:
			m.onVerifyResult(res)
		}
	}
}

// awaitPushInFlight blocks the monitor's exit until a still-running
// commit+push sub-goroutine has finished (it is bounded by its own
// PushBound context): "the monitor is gone" must mean no writer of the
// WI's git repositories is left behind it. The result is dropped — the
// monitor is stopping.
func (m *Monitor) awaitPushInFlight() {
	if len(m.gate) == 0 {
		return
	}
	bound := m.cfg.PushBound
	if bound <= 0 {
		bound = time.Minute
	}
	select {
	case <-m.pushDone:
	case <-time.After(bound + 5*time.Second):
	}
}

// stop requests the monitor loop to exit (idempotent).
func (m *Monitor) requestStop() { m.stop.Do(func() { close(m.stopCh) }) }

// onTick is the heartbeat: judge the dispatch (or the driver), retry a
// push whose backoff elapsed, M4 revalidation.
func (m *Monitor) onTick() {
	m.drainFacts() // belt and braces: a wake-up can coalesce
	m.observeGap()
	m.judge()
	m.schedulePush()
	m.checkRevalidate()
}

// observeGap removes the supervisor's own observation gap from everything
// it judges. It keeps the previous tick's instant: a tick far later than
// the declared cadence means the monitor (and usually the host) was
// stopped, so the wall span in between was never watched and must not be
// charged to the dispatch as inactivity. Every instant stamped at or
// before the pause is moved forward, which leaves the observed age intact
// and lets a real hang re-arm in observed time; the cumulative model/tool
// split is corrected by the tracker. One event per gap names its length.
func (m *Monitor) observeGap() {
	now := m.now()
	last := m.lastTick
	m.lastTick = now
	if last.IsZero() {
		if m.cur != nil {
			m.cur.tracker.ObserveTick(time.Time{}, 0)
		}
		return
	}
	poll := m.cfg.PollInterval
	gap := now.Sub(last)
	if gap <= observationGapFactor*poll {
		if m.cur != nil {
			m.cur.tracker.ObserveTick(time.Time{}, 0)
		}
		return
	}
	shift := gap - poll
	cutoff := last.Add(poll)
	if m.cur != nil {
		m.cur.tracker.ObserveTick(cutoff, shift)
	}
	if !m.phaseSince.IsZero() && !m.phaseSince.After(cutoff) {
		m.phaseSince = m.phaseSince.Add(shift)
	} else if m.phaseSince.Before(now) {
		// the clock started inside the unobserved span — the end of a
		// declared retry wait (step_retry_wait) that ran out during the
		// pause: none of that span was watched, so N starts now
		m.phaseSince = now
	}
	_ = m.log.Append(m.wi.WorkItem, EvSupervisorPaused, map[string]any{
		"paused_ms":        gap.Milliseconds(),
		"poll_interval_ms": poll.Milliseconds(),
		"spawn_n":          m.currentSpawn(),
	})
}

// isQuiet reads the quiet state (markClosing sets it from another
// goroutine).
func (m *Monitor) isQuiet() bool {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	return m.quiet || m.closing
}

// ---------------------------------------------------------------------------
// facts in

func (m *Monitor) now() time.Time { return m.link.Clock() }

// drainFacts consumes every queued driver fact, in order.
//
// A monitor told to stop consumes nothing more: the link is the work
// item's, and its facts may already be the next derivation's, whose
// monitor is built right after this one is told to stop.
func (m *Monitor) drainFacts() {
	select {
	case <-m.stopCh:
		return
	default:
	}
	facts := m.link.Facts.Drain()
	if len(facts) == 0 {
		return
	}
	for _, f := range facts {
		switch v := f.(type) {
		case supervise.StepStarted:
			m.onStepStarted(v)
		case supervise.StepEnded:
			m.onStepEnded(v)
		case supervise.StepWaitingHuman:
			m.setPhase(phaseWaiting)
			m.refreshExec(0)
		case supervise.StepAnswered:
			m.setPhase(phaseBetween)
			m.refreshExec(0)
		case supervise.ExecCommitted:
			m.refreshExec(v.Version)
		case supervise.StepRetryWait:
			m.onRetryWait(v)
		}
	}
	m.schedulePush() // event-triggered: do not wait for the next tick
}

func (m *Monitor) setPhase(p string) {
	m.phase = p
	m.phaseSince = m.now()
	m.stalled = false
}

// onStepStarted creates the per-dispatch state (a still-open older one is
// dropped: the driver runs one dispatch at a time).
func (m *Monitor) onStepStarted(f supervise.StepStarted) {
	if m.cur != nil && m.cur.spawnN < f.SpawnN {
		m.foldWaits(m.cur)
		m.link.Release(m.cur.spawnN)
	}
	m.link.Release(f.SpawnN - 1) // fence every older ordinal
	tr := m.link.Tracker(f.SpawnN)
	tr.Begin(f)
	d := &dispatchState{spawnN: f.SpawnN, step: f.Step, startedAt: f.StartedAt, tracker: tr, waitsBefore: m.stepWaits[f.Step]}
	if f.Model != "" {
		d.candidate = harness.Candidate{Harness: harness.Of(f.Harness), Model: f.Model, Effort: f.Effort}.String()
	}
	if d.startedAt.IsZero() {
		d.startedAt = m.now()
	}
	m.cur = d
	m.setPhase(phaseInStep)
	// F3: the transcript sync sources from the latest step_started
	m.trDst = f.TranscriptPath
	m.trSpawnAt = f.StartedAt
	m.trHarness, m.trSession = f.Harness, f.HarnessSession
	// a claude dispatch's native session lives outside the capsule even
	// when resumed: it is always copied
	if f.Resumed && f.Harness != "claude" {
		m.trSrc = f.TranscriptPath // a resumed dispatch appends to the capsule file itself
	} else {
		m.trSrc = ""
	}
	m.stateMu.Lock()
	m.transcriptWarned = false
	m.act = d // published last: readers that see the dispatch see its transcript facts
	m.stateMu.Unlock()
}

// onStepEnded drops the dispatch's state; the supervisor is silent for
// that spawn from here on.
func (m *Monitor) onStepEnded(f supervise.StepEnded) {
	m.link.Release(f.SpawnN)
	if m.cur != nil && m.cur.spawnN <= f.SpawnN {
		m.foldWaits(m.cur)
		m.cur = nil
		m.stateMu.Lock()
		m.act = nil
		m.stateMu.Unlock()
	}
	m.setPhase(phaseBetween)
}

// foldWaits adds an ending dispatch's wait calls to its step's total.
func (m *Monitor) foldWaits(d *dispatchState) {
	snap := d.tracker.Snapshot(m.now())
	if snap.WaitCalls == 0 {
		return
	}
	if m.stepWaits == nil {
		m.stepWaits = map[string]waitTotal{}
	}
	w := m.stepWaits[d.step]
	w.calls += snap.WaitCalls
	w.secs += snap.WaitSeconds
	m.stepWaits[d.step] = w
}

// onRetryWait moves the between-steps stall clock to the end of a backoff
// the driver declared: the wait is the driver's decision, not a stall, so
// the clock N runs from Until. A declaration that ends earlier than the
// clock already starts (or arrives outside the between-steps phase)
// changes nothing.
func (m *Monitor) onRetryWait(f supervise.StepRetryWait) {
	if m.phase != phaseBetween || !f.Until.After(m.phaseSince) {
		return
	}
	m.phaseSince = f.Until
	m.stalled = false
}

// refreshExec is the event-triggered read of the authoritative file: the
// Version to publish (version > 0 is the committed Version the fact
// named) and the pending_invalid diagnostic.
func (m *Monitor) refreshExec(version int64) {
	if version > m.dirtyVersion {
		m.dirtyVersion = version
	}
	data, err := os.ReadFile(m.execPath())
	if err != nil {
		return
	}
	var exec capsule.Execution
	if json.Unmarshal(data, &exec) != nil {
		return // torn/partial write: the next fact retries
	}
	if exec.Version > m.dirtyVersion {
		m.dirtyVersion = exec.Version
	}
	if st := exec.Steps[exec.CurrentStep]; st != nil && st.PendingQuestion != nil && st.PendingQuestion.ID == "" {
		// F10: a schema-invalid pending (id missing — e.g. the wire field
		// name written instead of the schema's `id`) is unanswerable; the
		// status face ignores it, this event names it.
		_ = m.log.Append(m.wi.WorkItem, "pending_invalid", map[string]any{
			"step": st.PendingQuestion.Step, "hint": "execution.json pending_question.id missing; see skill §7.2",
		})
	}
}

func (m *Monitor) execPath() string { return m.execJSONPath() }

// ---------------------------------------------------------------------------
// judging

// judge applies the rules to the current dispatch (one verdict, then
// silence until step_ended) or times the driver between steps.
func (m *Monitor) judge() {
	if m.isQuiet() {
		return
	}
	now := m.now()
	var live supervise.Defaults
	hasLive := m.cfg.LiveLimits != nil
	if hasLive {
		live = m.cfg.LiveLimits()
	}
	if m.cur != nil {
		if m.cur.verdicted {
			return
		}
		snap := m.cur.tracker.Snapshot(now)
		if hasLive {
			// A live step_timeout reaches an in-flight dispatch: recompute
			// T = max(the live global, the step's own floor) every tick.
			global := supervise.Defaults{StepTimeout: live.StepTimeout}.Fill().StepTimeout
			snap.Thresholds.StepTimeout = max(global, snap.Thresholds.StepTimeoutFloor)
		}
		v := supervise.Judge(snap, now, m.cfg.ModelErrorGrace)
		if v == nil {
			return
		}
		m.cur.verdicted = true
		m.link.Verdict(*v) // the driver records it: step_dead + step_failed
		return
	}
	stall := m.cfg.DriverStall
	if hasLive {
		// 0 = unset: the documented default; a negative value disables.
		if live.DriverStall != 0 {
			stall = live.DriverStall
		} else {
			stall = DefaultDriverStall
		}
	}
	if m.phase == phaseBetween && !m.stalled && stall > 0 {
		if gap := now.Sub(m.phaseSince); gap >= stall {
			m.stalled = true
			m.link.Verdict(supervise.DriverStalled{Duration: gap, Phase: phaseBetween})
			// A driver that cannot consume its verdict is the point: the
			// recovery engine kills its tree (any stuck git/push child with
			// it) and relaunches it, counted against the driver-crash bound.
			m.needRecovery(protocol.RecoverDriverStalled,
				fmt.Sprintf("driver stalled between steps for %s", gap.Round(time.Second)))
		}
	}
}

// activity answers the ops-status face: when the current dispatch began
// and when its harness last did real work.
func (m *Monitor) activity() Activity {
	m.stateMu.Lock()
	d := m.act
	m.stateMu.Unlock()
	if d == nil {
		return Activity{}
	}
	now := m.now()
	snap := d.tracker.Snapshot(now)
	return Activity{Step: d.step, SpawnN: d.spawnN, StepStartedAt: d.startedAt, LastWorkAt: snap.LastWork, InDispatch: true, Candidate: d.candidate,
		WaitCalls: d.waitsBefore.calls + snap.WaitCalls, WaitSeconds: d.waitsBefore.secs + snap.WaitSeconds}
}

// liveProgress answers the watch face's per-dispatch reading (any
// goroutine: it reads the published dispatch pointer under stateMu and the
// tracker's own lock).
func (m *Monitor) liveProgress() LiveProgress {
	m.stateMu.Lock()
	d := m.act
	m.stateMu.Unlock()
	if d == nil {
		return LiveProgress{}
	}
	now := m.now()
	snap := d.tracker.Snapshot(now)
	p := LiveProgress{InDispatch: true, Step: d.step, RecentFiles: snap.RecentFiles, ToolCalls: snap.ToolCalls,
		WaitCalls: d.waitsBefore.calls + snap.WaitCalls, WaitSeconds: d.waitsBefore.secs + snap.WaitSeconds}
	if n := len(snap.Tools); n > 0 {
		cur := snap.Tools[n-1] // oldest first: the last is the newest
		p.Command = cur.Summary
		p.CommandAgeSec = int64(now.Sub(cur.Since) / time.Second)
	}
	return p
}

// ---------------------------------------------------------------------------
// detection: driver death

// onExit handles the CONFIRMED driver-exit signal (the handle already
// waited on the driver). An expected exit (a daemon-initiated close, or a
// recovery already requested for this derivation) is silent. A terminal
// exit needs no publish first: the runtime's close seal-pushes the
// capsule itself.
//
// A driver that exits over a settled workflow_status — a terminal one
// (completed, failed_gate, failed_rewind_exhausted) or blocked, the
// non-terminal owner park its Run loop stops on by design — finished; it
// did not die. The driver writes that status before it returns, so the
// read is race-free (and a relaunch over a parked capsule rewrites it to
// running before its Run starts). The runtime's recovery door is still
// the one that closes it (recoverCore translates the status into the WI's
// close: wrapped, failed, or the owner's bounded-fail block), but no
// needs_recovery event is recorded for it; the driver's own blocked event
// (incomplete, for a park on an incomplete step result) already records
// the park.
func (m *Monitor) onExit() {
	m.exitCh = nil // fires once per handle
	m.drainFacts() // the driver's final commit-boundary facts may still be queued
	m.signalRecovery(protocol.RecoverDeath, "agent process exited", !m.workflowSettled())
}

// workflowSettled reports whether execution.json carries a workflow_status
// the driver stops on: a terminal one or blocked (an unreadable or torn
// file is not settled).
func (m *Monitor) workflowSettled() bool {
	data, err := os.ReadFile(m.execPath())
	if err != nil {
		return false
	}
	var exec capsule.Execution
	if json.Unmarshal(data, &exec) != nil {
		return false
	}
	switch exec.WorkflowStatus {
	case capsule.WorkflowStatusCompleted, capsule.WorkflowStatusFailedGate,
		capsule.WorkflowStatusFailedRewindExhausted, capsule.WorkflowStatusBlocked:
		return true
	}
	return false
}

// needRecovery sends the derivation's one WINeedsRecovery and turns the
// monitor quiet until RTRecovered ("已发后静默到恢复完成"); a quiet or
// closing monitor sends nothing.
func (m *Monitor) needRecovery(reason, detail string) {
	m.signalRecovery(reason, detail, true)
}

// signalRecovery is needRecovery; record=false sends the request without
// the needs_recovery event (a settled driver exit is no death).
func (m *Monitor) signalRecovery(reason, detail string, record bool) {
	m.stateMu.Lock()
	if m.quiet || m.closing {
		m.stateMu.Unlock()
		return
	}
	m.quiet = true
	m.stateMu.Unlock()
	if record {
		_ = m.log.Append(m.wi.WorkItem, EvNeedsRecovery, map[string]any{
			"reason": reason, "detail": detail, "spawn_n": m.currentSpawn(),
		})
	}
	m.sendOB(protocol.OBMsg{
		Kind: protocol.OBNeedsRecovery, SpawnN: m.currentSpawn(),
		WorkItem: m.wi.WorkItem, Reason: reason, Detail: detail,
	})
}

// sendOB queues one message for the Runtime; the queue is unbounded, so
// the monitor never blocks and a recovery request is never dropped.
func (m *Monitor) sendOB(msg protocol.OBMsg) { m.ob.Push(msg) }

// ---------------------------------------------------------------------------
// recovery lifecycle

// onRecovered adopts the new handle Runtime derived (RTRecovered). The
// monitor leaves RECOVERING and drops every per-dispatch state: the
// relaunched driver announces its own dispatches.
func (m *Monitor) onRecovered(at Attach) {
	if at.Handle == nil || at.Msg.SpawnN <= 0 {
		return // malformed attachment (no announced derivation): never adopted
	}
	m.exitCh = at.Handle.Exit()
	if m.cur != nil {
		m.foldWaits(m.cur) // the dropped dispatch's wait calls stay in its step's total
		m.link.Release(m.cur.spawnN)
	}
	m.cur = nil
	m.stateMu.Lock()
	m.act = nil
	m.stateMu.Unlock()
	m.setPhase(phaseBetween)
	// No dispatch is in flight until the successor's first step_started
	// names its transcript: nothing to sync in between, and the dead
	// dispatch's destination must never receive the successor's file.
	m.trDst, m.trSrc, m.trSpawnAt = "", "", time.Time{}
	m.trHarness, m.trSession = "", ""
	m.stateMu.Lock()
	m.transcriptWarned = false
	m.quiet = false
	m.spawnN = at.Msg.SpawnN
	m.sessionID = at.Msg.SessionID
	m.stateMu.Unlock()
	_ = m.log.Append(m.wi.WorkItem, EvAgentRecovered, map[string]any{
		"spawn_n": at.Msg.SpawnN,
	})
}

// ---------------------------------------------------------------------------
// async commit/push

// transcriptSnapshot captures the current derivation's transcript-sync
// inputs (monitor goroutine only); the push sub-goroutines receive the
// VALUES, never the monitor fields — no cross-goroutine reads.
type transcriptSnapshot struct {
	dst     string
	src     string
	spawnAt time.Time
	harness string
	session string
}

func (m *Monitor) transcriptSnapshot() transcriptSnapshot {
	return transcriptSnapshot{dst: m.trDst, src: m.trSrc, spawnAt: m.trSpawnAt, harness: m.trHarness, session: m.trSession}
}

// claimTranscriptWarning claims the once-per-attempt transcript_missing
// warning (push sub-goroutines): true exactly once per derivation.
func (m *Monitor) claimTranscriptWarning() bool {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if m.transcriptWarned {
		return false
	}
	m.transcriptWarned = true
	return true
}

// syncTranscript is the F3 commit-boundary transcript sync: copy the
// current derivation's live harness transcript onto the capsule's
// declared session path, READ-ONLY w.r.t. the source, so the transcript
// rides the very commit the caller is about to make (v1 ruling C1 (archive/v1-architecture-spec.md)). Runs in
// the push sub-goroutines BEFORE the push itself; a missing source is
// the typed advisory transcript_missing (once per attempt), never an
// error — the commit proceeds without the transcript.
func (m *Monitor) syncTranscript(s transcriptSnapshot) {
	if s.dst == "" {
		return // no declared session path: nothing to sync onto
	}
	src := s.src
	if src == "" && m.cfg.locateLive != nil {
		src = m.cfg.locateLive(m.wi.CapsuleDir, s.spawnAt, s.harness, s.session)
	}
	if src == "" {
		if m.claimTranscriptWarning() {
			_ = m.log.Append(m.wi.WorkItem, EvTranscriptMissing, map[string]any{
				"detail":  "live harness transcript not found at the commit boundary; committing without it",
				"spawn_n": m.currentSpawn(),
			})
		}
		return
	}
	if src == s.dst {
		return // a resumed derivation already writes into the capsule
	}
	if err := sessionjsonl.CopyReadonly(src, s.dst); err != nil {
		if m.claimTranscriptWarning() {
			_ = m.log.Append(m.wi.WorkItem, EvTranscriptMissing, map[string]any{
				"detail":  fmt.Sprintf("transcript sync failed: %v", err),
				"spawn_n": m.currentSpawn(),
			})
		}
	}
}

// schedulePush starts one async commit+push sub-goroutine when a newer
// execution Version exists, the gate is free and — for the Version that
// just failed — the retry delay has elapsed. It is event-triggered: the
// driver's exec_committed facts raise dirtyVersion and the loop calls it
// right after draining them (the tick serves the retry delay).
func (m *Monitor) schedulePush() {
	if m.dirtyVersion <= m.pushedVersion {
		return
	}
	newer := m.dirtyVersion > m.failedVersion
	if !newer && time.Now().Before(m.retryAt) {
		return
	}
	// try-acquire the gate: at most ONE push in flight per WI.
	select {
	case m.gate <- struct{}{}:
	default:
		return // one already in flight; the next tick retries
	}
	version := m.dirtyVersion
	tr := m.transcriptSnapshot() // captured BEFORE the spawn: no shared-field reads
	go func() {
		// The transcript sync rides the commit pipeline (F3): the copy
		// precedes the push in THIS goroutine, so the transcript lands in
		// the commit it belongs to — and never blocks the select loop.
		m.syncTranscript(tr)
		ctx, cancel := context.WithTimeout(context.Background(), m.cfg.PushBound)
		defer cancel()
		message := fmt.Sprintf("observe: execution v%d", version)
		sha, pushed, timings, err := m.cfg.push(ctx, m.wi.Git, message)
		m.pushDone <- pushResult{version: version, sha: sha, pushed: pushed, timings: timings, err: err}
	}()
}

func (m *Monitor) onPushResult(res pushResult) {
	<-m.gate // release the single-in-flight slot
	// 兜底: an honest gitops result is either (pushed, nil) or a non-nil
	// error — a clean-tree CommitPush pushes the standing HEAD, so a
	// failed push always carries its error. Should a PushFunc ever
	// report (nil error, pushed=false) anyway, publishing it would mark
	// the version server-reached when it was not; classify it
	// retry-worthy instead.
	if res.err == nil && !res.pushed {
		res.err = fmt.Errorf("push reported success without pushing (sha=%q)", res.sha)
	}
	if res.err == nil {
		if res.version > m.pushedVersion {
			m.pushedVersion = res.version
		}
		m.failedPushes, m.backoff, m.retryAt, m.backpressured = 0, 0, time.Time{}, false
		_ = m.log.Append(m.wi.WorkItem, EvPublished, map[string]any{
			"version": res.version, "local_commit": res.sha, "pushed": res.pushed,
			"commit_ms": res.timings.CommitMS, "push_ms": res.timings.PushMS,
		})
		return
	}
	// Exponential retry: a deterministic failure (dead server config,
	// poisoned tree) costs at most one attempt per PushBackoffMax once the
	// delay has climbed — the pilot's #48 storm was 185 identical
	// failures at a fixed 10s cadence.
	m.failedPushes++
	m.failedVersion = res.version
	m.backoff = min(max(2*m.backoff, m.cfg.PushRetryBackoff), m.cfg.PushBackoffMax)
	m.retryAt = time.Now().Add(m.backoff)
	if gitops.IsLocalGitlink(res.err) {
		// V8: the LOCAL gitlink precheck refused to create the commit —
		// its own kind (see EvPushPoisonedLocal), retried like any
		// persistent failure.
		_ = m.log.Append(m.wi.WorkItem, EvPushPoisonedLocal, map[string]any{
			"version": res.version, "error": res.err.Error(),
			"detail": "local gitlink precheck refused to create the commit (GIT-412 LOCAL_GITLINK_DETECTED); " +
				"not an occurred boundary-sync failure — excluded from push_failed and the M8 read-through by design",
		})
	} else {
		_ = m.log.Append(m.wi.WorkItem, EvPushFailed, map[string]any{
			"version": res.version, "error": res.err.Error(),
		})
	}
	if m.backoff >= m.cfg.PushBackoffMax && !m.backpressured {
		// §九 degradation: never kills the agent — local commits keep
		// accumulating and the next success ends the episode.
		m.backpressured = true
		_ = m.log.Append(m.wi.WorkItem, EvPushBackpressure, map[string]any{
			"failed_pushes": m.failedPushes, "error": res.err.Error(),
			"note": "push retries reached their longest delay; agent continues, commits accumulate locally",
		})
	}
	if gitops.IsCredentialRefused(res.err) {
		// the git face refused the session credential — this session may
		// no longer hold the work item: revalidate at the next tick, not
		// at the next interval (a superseded work item stops at once)
		m.lastVerifyAt = time.Time{}
	}
}

// ---------------------------------------------------------------------------
// state read face (HTTP view + sub-goroutines)

// currentSpawn answers the derivation counter the monitor has ADOPTED
// (stateMu-guarded — the HTTP read face reads it cross-goroutine).
func (m *Monitor) currentSpawn() int {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	return m.spawnN
}

// markClosing turns the monitor quiet for good for a daemon-initiated
// close (see Observe.MarkClosing): the agent's imminent exit is expected,
// even after a recovery the close was queued behind.
func (m *Monitor) markClosing() {
	m.stateMu.Lock()
	m.closing = true
	m.stateMu.Unlock()
}
