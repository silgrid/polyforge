// Package supervise is the driver <-> supervisor contract of the v2
// converge (docs/design/polyforge-vnext/v2-converge.md section 2.2-2.5).
//
// The two sides live in the same `pf daemon` process and talk through
// one Link per work item:
//
//   - driver -> supervisor FACTS (step_started, step_waiting_human /
//     step_answered, step_ended, plus the additive exec_committed commit
//     boundary notice): an unbounded per-WI queue, the sender never
//     blocks and a fact is never dropped;
//   - supervisor -> driver VERDICTS (step_dead, driver_stalled): the
//     same queue shape in the other direction;
//   - harness output rides a third, MERGED channel: the harness
//     translator folds native records into one Tracker per dispatch
//     (latest state only, nothing queues), which the supervisor judges.
//
// The fence is the spawn ordinal: the driver accepts a verdict only for
// its current dispatch, at most one verdict and one failure record exist
// per dispatch, and every piece of supervisor state is per dispatch
// (created on step_started, dropped on step_ended).
package supervise

import (
	"sync"
	"time"
)

// Cause is the supervisor's judgement of why a dispatch is dead
// (v2-converge 2.3). The numeric prefix in the comments is the rule
// priority: when several rules fire at once the lowest number wins.
type Cause string

const (
	CauseHarnessSilent        Cause = "harness_silent"          // rule 0
	CauseModelError           Cause = "model_error"             // rule 1 (error-ended turn)
	CauseEndedWithoutResult   Cause = "ended_without_result"    // rule 1 (ok-ended turn)
	CauseModelHung            Cause = "model_hung"              // rule 2
	CauseToolHung             Cause = "tool_hung"               // rule 3
	CauseStepTimeoutLongTask  Cause = "step_timeout_long_task"  // rule 4a
	CauseStepTimeoutSlowModel Cause = "step_timeout_slow_model" // rule 4b
	// CauseRateLimited is the provider's transient throttle (429 / rate
	// limit / too many requests / overloaded — never quota, billing,
	// credit or permission problems, which stay model-class and switch
	// candidates), judged by the driver's own
	// exit classification rather than by a supervisor rule
	// (polyforge#147 D7). It is deliberately NOT model-class: the
	// provider is busy, not broken, so the same model is retried after a
	// backoff (the wait counts into the step's cumulative T, the only
	// bound) and the candidate chain stays where it is.
	CauseRateLimited Cause = "rate_limited"
)

// Outcome is how a dispatch ended, as reported by step_ended.
type Outcome string

const (
	// OutcomeResult: the dispatch delivered a result envelope.
	OutcomeResult Outcome = "result"
	// OutcomeExitedWithoutResult: the process is gone (or was killed on a
	// verdict / cancellation) and no usable envelope landed.
	OutcomeExitedWithoutResult Outcome = "exited_without_result"
)

// Thresholds are the per-dispatch judging limits the driver sends with
// step_started (relaxed values already folded in, 2.4). Zero disables a
// rule.
type Thresholds struct {
	// Silence is X: no work-class signal after dispatch (rule 0) and no
	// model output while waiting for the model (rule 2).
	Silence time.Duration `json:"silence"`
	// Tool is Y: one tool running longer than this is tool_hung.
	Tool time.Duration `json:"tool"`
	// StepTimeout is T: the CUMULATIVE step runtime bound.
	StepTimeout time.Duration `json:"step_timeout"`
	// StepTimeoutFloor is the step's own part of T, EXCLUDING the global
	// step_timeout: max(3 x budget.wall_minutes, the step's persisted
	// relaxed bound). It lets the supervisor recompute
	// T = max(liveGlobal, StepTimeoutFloor) at every tick so a live
	// step_timeout change reaches an in-flight dispatch (workbench#3).
	StepTimeoutFloor time.Duration `json:"step_timeout_floor,omitempty"`
	// PriorRuntime is the step runtime already spent by earlier
	// dispatches of the same step (execution.json is the authority);
	// T is judged against PriorRuntime + this dispatch's age.
	PriorRuntime time.Duration `json:"prior_runtime"`
}

// ---------------------------------------------------------------------------
// Facts (driver -> supervisor).

// Fact is one driver -> supervisor message.
type Fact interface{ FactKind() string }

// StepStarted announces a dispatch.
type StepStarted struct {
	Step    string `json:"step"`
	SpawnN  int64  `json:"spawn_n"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"` // the candidate's thinking effort (polyforge#176; "" = the harness default)
	Harness string `json:"harness"`
	// HarnessSession is the harness-native session id of the dispatch
	// (polyforge#159: claude's --session-id/--resume uuid; "" for pi).
	// The transcript sync locates claude's native session file by it.
	HarnessSession string     `json:"harness_session,omitempty"`
	PID            int        `json:"pid,omitempty"`
	TranscriptPath string     `json:"transcript_path,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	Thresholds     Thresholds `json:"thresholds"`
	// Resumed: the dispatch resumes TranscriptPath in place (the live
	// transcript IS that file; the F3 sync has nothing to copy).
	Resumed bool `json:"resumed,omitempty"`
}

// StepWaitingHuman: the driver parked on a human question. There is no
// step process while it lasts, so no dispatch timer runs.
type StepWaitingHuman struct {
	Step       string `json:"step"`
	SpawnN     int64  `json:"spawn_n"`
	QuestionID string `json:"question_id"`
}

// StepAnswered: the human answered; the driver is between steps again.
type StepAnswered struct {
	Step       string `json:"step"`
	SpawnN     int64  `json:"spawn_n"`
	QuestionID string `json:"question_id"`
}

// StepEnded closes a dispatch.
type StepEnded struct {
	Step      string  `json:"step"`
	SpawnN    int64   `json:"spawn_n"`
	Outcome   Outcome `json:"outcome"`
	ExitCode  int     `json:"exit_code"`
	LastError string  `json:"last_error,omitempty"`
}

// ExecCommitted is the commit-boundary notice (additive to the spec'd
// facts): the driver persisted a new execution.json Version. It is the
// event that triggers the capsule push (the supervisor does not poll
// the file).
type ExecCommitted struct {
	Version int64  `json:"version"`
	Step    string `json:"step,omitempty"`
}

func (StepStarted) FactKind() string      { return "step_started" }
func (StepWaitingHuman) FactKind() string { return "step_waiting_human" }
func (StepAnswered) FactKind() string     { return "step_answered" }
func (StepEnded) FactKind() string        { return "step_ended" }
func (ExecCommitted) FactKind() string    { return "exec_committed" }

// ---------------------------------------------------------------------------
// Verdicts (supervisor -> driver).

// Verdict is one supervisor -> driver message.
type Verdict interface{ VerdictKind() string }

// StepDead: the dispatch is judged dead.
type StepDead struct {
	Step     string `json:"step"`
	SpawnN   int64  `json:"spawn_n"`
	Cause    Cause  `json:"cause"`
	Evidence string `json:"evidence"`
	// TurnError is the harness's own error text of the error-ended turn
	// (model_error only): the driver classifies it like the error text of
	// an exited dispatch (D7: a throttle is not a model-class death).
	TurnError string `json:"turn_error,omitempty"`
}

// DriverStalled: the driver sat between steps (not waiting on a human)
// longer than the stall bound.
type DriverStalled struct {
	Duration time.Duration `json:"duration"`
	// Phase names the gap the driver is stuck in ("between_steps").
	Phase string `json:"phase"`
}

func (StepDead) VerdictKind() string      { return "step_dead" }
func (DriverStalled) VerdictKind() string { return "driver_stalled" }

// ---------------------------------------------------------------------------
// Queue: the unbounded, never-blocking, never-dropping mailbox.

// Queue is an unbounded FIFO. Push never blocks and never drops; the
// consumer is woken through Notify and takes everything with Drain.
type Queue[T any] struct {
	mu     sync.Mutex
	items  []T
	notify chan struct{}
}

// NewQueue builds an empty queue.
func NewQueue[T any]() *Queue[T] { return &Queue[T]{notify: make(chan struct{}, 1)} }

// Push appends one item; it can never block the sender.
func (q *Queue[T]) Push(v T) {
	q.mu.Lock()
	q.items = append(q.items, v)
	q.mu.Unlock()
	select {
	case q.notify <- struct{}{}:
	default: // a wake-up is already pending; the consumer drains everything
	}
}

// Notify is signalled whenever items may be waiting.
func (q *Queue[T]) Notify() <-chan struct{} { return q.notify }

// Drain takes every queued item in order.
func (q *Queue[T]) Drain() []T {
	q.mu.Lock()
	out := q.items
	q.items = nil
	q.mu.Unlock()
	return out
}

// Pop takes the oldest queued item.
func (q *Queue[T]) Pop() (v T, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return v, false
	}
	v = q.items[0]
	var zero T
	q.items[0] = zero
	q.items = q.items[1:]
	return v, true
}

// ---------------------------------------------------------------------------
// Link: one per work item.

// Link is the per-WI meeting point of the driver, the supervisor and the
// harness translator.
type Link struct {
	WI string
	// Facts flows driver -> supervisor; Verdicts flows supervisor -> driver.
	Facts    *Queue[Fact]
	Verdicts *Queue[Verdict]

	// Now is the clock (tests); nil = time.Now.
	Now func() time.Time

	mu       sync.Mutex
	trackers map[int64]*Tracker
	endedMax int64
}

// NewLink builds a link for one work item.
func NewLink(wi string) *Link {
	return &Link{
		WI:       wi,
		Facts:    NewQueue[Fact](),
		Verdicts: NewQueue[Verdict](),
		trackers: map[int64]*Tracker{},
	}
}

func (l *Link) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Clock answers the link's current time (the injectable clock).
func (l *Link) Clock() time.Time { return l.now() }

// Fact sends one driver fact (never blocks).
func (l *Link) Fact(f Fact) { l.Facts.Push(f) }

// Verdict sends one supervisor verdict (never blocks).
func (l *Link) Verdict(v Verdict) { l.Verdicts.Push(v) }

// Tracker answers the per-dispatch harness state of spawnN, creating it
// on first use (harness records can arrive before the supervisor has
// drained step_started). A dispatch that already ended answers a
// discarding tracker: late records of a dead spawn never resurrect it.
func (l *Link) Tracker(spawnN int64) *Tracker {
	l.mu.Lock()
	defer l.mu.Unlock()
	if spawnN <= l.endedMax {
		return newTracker(spawnN, l.now) // detached: nobody judges it
	}
	t := l.trackers[spawnN]
	if t == nil {
		t = newTracker(spawnN, l.now)
		l.trackers[spawnN] = t
	}
	return t
}

// Reset starts a new driver epoch on the link: the dispatch fence and every
// tracker of the previous driver are dropped (a re-claimed capsule may
// restart its spawn ordinals) and stale verdicts are discarded. Facts are
// kept: the supervisor consumes them in order and a newer step_started
// replaces any dispatch an old driver left open.
func (l *Link) Reset() {
	l.mu.Lock()
	l.trackers = map[int64]*Tracker{}
	l.endedMax = 0
	l.mu.Unlock()
	l.Verdicts.Drain()
}

// Release drops the dispatch's tracker (step_ended) and fences off every
// older ordinal.
func (l *Link) Release(spawnN int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for n := range l.trackers {
		if n <= spawnN {
			delete(l.trackers, n)
		}
	}
	if spawnN > l.endedMax {
		l.endedMax = spawnN
	}
}

// Registry hands out one Link per work item.
type Registry struct {
	mu    sync.Mutex
	links map[string]*Link
	// Now seeds the clock of every link it creates (tests).
	Now func() time.Time
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry { return &Registry{links: map[string]*Link{}} }

// Link answers the work item's link, creating it on first use.
func (r *Registry) Link(wi string) *Link {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.links[wi]
	if l == nil {
		l = NewLink(wi)
		l.Now = r.Now
		r.links[wi] = l
	}
	return l
}
