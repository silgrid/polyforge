package observe

// observe.go: the Observe hub — the supervisor's root object. It owns
// the events log (driver events land in it too, durably), spawns one
// per-WI supervisor goroutine per RTStarted, routes RTRecovered/RTFinished
// to the right monitor, and exposes the monitors' read faces (Activities,
// Progress, the events log) to the daemon. Runtime talks to
// Observe through two queues (RT lifecycle messages in, OB messages out);
// the agent handle a monitor watches is resolved when its lifecycle
// message is consumed (Config.Agent).

import (
	"context"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// Attach is one adopted derivation: its lifecycle message and the agent
// handle the monitor watches.
type Attach struct {
	Msg    protocol.RTMsg
	Handle Handle
}

// Config wires the hub. Zero fields take the defaults in fill().
type Config struct {
	// RT is the Runtime→Observe lifecycle queue (RTStarted/
	// RTRecovered/RTFinished). Runtime writes; Observe reads.
	RT *supervise.Queue[protocol.RTMsg]
	// Agent resolves a work item's live agent handle and per-WI facts at
	// the moment its RTStarted/RTRecovered is consumed; ok=false = no
	// live agent to watch.
	Agent func(wi string) (h Handle, info WIInfo, ok bool)
	// OB is the Observe→Runtime queue (WINeedsRecovery, WICompleted).
	// Unbounded: a send never blocks and never drops.
	OB *supervise.Queue[protocol.OBMsg]
	// DataDir hosts the per-WI events files (events/).
	DataDir string
	// PushFunc overrides the capsule commit+push operation (tests).
	PushFunc PushFunc
	// LocateLive, when non-nil, resolves the live harness transcript of a
	// derivation (capsuleDir + spawn moment + the dispatch's step harness
	// and harness-native session id → absolute .jsonl path; "" = not
	// found). The monitor calls it at each dispatch's commit-boundary
	// sync points (the F3 daemon-side transcript sync, onto the path the
	// dispatch's step_started names). nil disables the sync entirely
	// (unit tests that never exercise it).
	LocateLive func(capsuleDir string, spawnAt time.Time, harness, session string) string
	// SessionVerify is the v1.5 M4 revalidation seam: the periodic
	// per-RUNNING-job session-verify the monitor tick piggybacks
	// (session.go). The daemon binds the shared aihub client; nil — the
	// unit-test default — disables revalidation entirely.
	SessionVerify SessionVerifier
	// SessionRevalidateEvery is the M4 cadence (0 = the default 30s;
	// negative = disabled).
	SessionRevalidateEvery time.Duration

	// Registry hands out the per-WI supervision links shared with the
	// driver (facts in, verdicts out, harness trackers). nil = every
	// monitor builds a private link (unit tests).
	Registry *supervise.Registry
	// PollInterval is the supervisor tick (judging cadence; 500ms).
	PollInterval time.Duration
	// ModelErrorGrace is rule 1's grace after an error-ended turn (1m).
	ModelErrorGrace time.Duration
	// DriverStall is N: the driver between steps for longer than this,
	// not waiting on a human, is stalled (10m; negative disables).
	DriverStall time.Duration
	// LiveLimits, when set, answers the CURRENT supervision defaults at
	// every tick so a live step_timeout / stall_window reload takes effect
	// without a restart. A zero StepTimeout/DriverStall
	// means unset and resolves to the documented default; a negative
	// DriverStall still disables the stall check. nil keeps DriverStall
	// and the dispatch-time T.
	LiveLimits       func() supervise.Defaults
	PushRetryBackoff time.Duration // first retry delay after a failed push (10s); doubles per failure
	PushBackoffMax   time.Duration // the longest retry delay (5m); reaching it writes push_backpressure
	PushBound        time.Duration // per-push bound (protocol.PushTimeout, 60s)
}

// DefaultDriverStall is N's documented default (the driver between steps
// for longer than this is stalled).
const DefaultDriverStall = 10 * time.Minute

func (c *Config) fill() {
	if c.PollInterval == 0 {
		c.PollInterval = 500 * time.Millisecond
	}
	if c.ModelErrorGrace == 0 {
		c.ModelErrorGrace = supervise.DefaultModelErrorGrace
	}
	if c.DriverStall == 0 {
		c.DriverStall = DefaultDriverStall
	}
	if c.PushRetryBackoff == 0 {
		c.PushRetryBackoff = 10 * time.Second
	}
	if c.PushBackoffMax == 0 {
		c.PushBackoffMax = 5 * time.Minute
	}
	if c.PushBound == 0 {
		c.PushBound = protocol.PushTimeout
	}
	if c.SessionRevalidateEvery == 0 {
		c.SessionRevalidateEvery = DefaultRevalidateEvery
	}
	if c.PushFunc == nil {
		// ctx (PushBound) bounds the chain push; on its end the push's
		// git process group is killed and the per-WI lock released
		c.PushFunc = gitops.CommitPush
	}
}

// Observe is the hub.
type Observe struct {
	cfg      Config
	log      *EventLog
	mu       sync.Mutex
	monitors map[string]*Monitor

	wg sync.WaitGroup
}

// New validates the config and opens the events log.
func New(cfg Config) (*Observe, error) {
	cfg.fill()
	log, err := NewEventLog(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	if cfg.RT == nil || cfg.OB == nil || cfg.Agent == nil {
		log.Close()
		return nil, errConfig("the RT and OB queues and the Agent resolver are required")
	}
	return &Observe{cfg: cfg, log: log,
		monitors: map[string]*Monitor{}}, nil
}

type errConfig string

func (e errConfig) Error() string { return "observe: " + string(e) }

// Run drives the hub until ctx is cancelled .
// On exit it stops every monitor, waits for them and closes the events
// log.
func (o *Observe) Run(ctx context.Context) {
	defer func() {
		o.stopAll()
		o.wg.Wait()
		_ = o.log.Close()
	}()
	for {
		for msg, ok := o.cfg.RT.Pop(); ok; msg, ok = o.cfg.RT.Pop() {
			o.onRT(msg)
		}
		select {
		case <-ctx.Done():
			return
		case <-o.cfg.RT.Notify():
		}
	}
}

func (o *Observe) onRT(msg protocol.RTMsg) {
	wi := msg.WorkItem
	switch msg.Kind {
	case protocol.RTStarted, protocol.RTRecovered:
		h, info, live := o.cfg.Agent(wi)
		live = live && h != nil && msg.SpawnN > 0 && info.WorkItem != ""
		o.mu.Lock()
		m := o.monitors[wi]
		o.mu.Unlock()
		if msg.Kind == protocol.RTRecovered && m != nil {
			if live {
				m.rtQ.Push(Attach{Msg: msg, Handle: h})
			}
			return
		}
		// a start, or a recovery no monitor watches (a restart's, or a
		// paused record's resume — the pause released its monitor): it
		// builds the monitor
		if m != nil {
			m.requestStop() // a stale monitor (no RTFinished): the new derivation wins
		}
		if !live {
			if msg.Kind == protocol.RTStarted {
				_ = o.log.Append(wi, EvJobStopped, map[string]any{
					"error": "RTStarted without a live agent or spawn_n; not monitored",
				})
			}
			o.takeMonitor(wi)
			return
		}
		m = o.newMonitor(Attach{Msg: msg, Handle: h}, info)
		o.setMonitor(wi, m)
		m.announceStart()
		o.wg.Add(1)
		go func() {
			defer o.wg.Done()
			defer close(m.done)
			m.run()
		}()
	case protocol.RTFinished:
		if m := o.takeMonitor(wi); m != nil {
			m.requestStop() // monitor emits its own job_stopped event
		}
	default:
		_ = o.log.Append(wi, EvJobStopped, map[string]any{
			"error": "unknown RT message kind: " + string(msg.Kind),
		})
	}
}

func (o *Observe) stopAll() {
	o.mu.Lock()
	ms := make([]*Monitor, 0, len(o.monitors))
	for _, m := range o.monitors {
		ms = append(ms, m)
	}
	o.monitors = map[string]*Monitor{}
	o.mu.Unlock()
	for _, m := range ms {
		m.requestStop()
	}
}

// lookup answers the monitor of a work item by its canonical id (the
// key the runtime announced); callers canonicalize a slug spelling
// first.
func (o *Observe) lookup(wi string) *Monitor {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.monitors[wi]
}

func (o *Observe) setMonitor(wi string, m *Monitor) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.monitors[wi] = m
}

// MarkClosing turns the work item's monitor quiet for a daemon-initiated
// close: the agent's imminent exit raises no recovery and writes no
// needs_recovery event. No monitor (never monitored) is a no-op.
func (o *Observe) MarkClosing(wi string) {
	if m := o.lookup(wi); m != nil {
		m.markClosing()
	}
}

// Release stops the work item's monitor and returns once its goroutine
// is gone — an in-flight push drained (awaitPushInFlight), queued ones
// dropped — so no monitor push runs after the caller's next step (the
// runtime's close: its seal push, then SessionEnd). No monitor is a
// no-op; the close's later RTFinished then finds none.
func (o *Observe) Release(wi string) {
	if m := o.takeMonitor(wi); m != nil {
		m.requestStop()
		<-m.done
	}
}

func (o *Observe) takeMonitor(wi string) *Monitor {
	o.mu.Lock()
	defer o.mu.Unlock()
	m := o.monitors[wi]
	delete(o.monitors, wi)
	return m
}

// newMonitor builds the per-WI supervisor for one adopted derivation.
func (o *Observe) newMonitor(at Attach, info WIInfo) *Monitor {
	var link *supervise.Link
	if o.cfg.Registry != nil {
		link = o.cfg.Registry.Link(info.WorkItem)
	} else {
		link = supervise.NewLink(info.WorkItem)
	}
	m := &Monitor{
		wi: info,
		cfg: monitorCfg{
			PollInterval:     o.cfg.PollInterval,
			ModelErrorGrace:  o.cfg.ModelErrorGrace,
			DriverStall:      o.cfg.DriverStall,
			LiveLimits:       o.cfg.LiveLimits,
			PushRetryBackoff: o.cfg.PushRetryBackoff,
			PushBackoffMax:   o.cfg.PushBackoffMax,
			PushBound:        o.cfg.PushBound,
			revalidate:       o.cfg.SessionRevalidateEvery,
			verify:           o.cfg.SessionVerify,
			push:             o.cfg.PushFunc,
			locateLive:       o.cfg.LocateLive,
		},
		log:    o.log,
		ob:     o.cfg.OB,
		link:   link,
		rtQ:    supervise.NewQueue[Attach](),
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),

		gate:       make(chan struct{}, 1),
		pushDone:   make(chan pushResult, 1),
		verifyDone: make(chan verifyResult, 1),
	}
	m.attach(at)
	m.phase = phaseBetween
	m.phaseSince = m.now()
	return m
}

// attach binds the first handle of a monitor's life (onRecovered does
// the same for successors).
func (m *Monitor) attach(at Attach) {
	m.exitCh = at.Handle.Exit()
	m.stateMu.Lock()
	m.spawnN = at.Msg.SpawnN
	m.sessionID = at.Msg.SessionID
	m.stateMu.Unlock()
}

// Events exposes the log (diagnostic/tests).
func (o *Observe) Events() *EventLog { return o.log }
