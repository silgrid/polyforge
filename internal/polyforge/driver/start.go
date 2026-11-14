package driver

// start.go: runs a Driver as the runtime's agent — the handle the
// runtime kills and the observe lane watches for the driver's exit.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/polyforge/pendingwrites"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// Handle is a running Driver.Run: the runtime's agent handle
// (spawn.Handle) plus the exit signal the observe monitor selects on.
type Handle struct {
	done    chan struct{} // closed when Run returned
	err     error         // Run's error; written before done closes
	cancel  context.CancelFunc
	started string
	drv     *Driver
}

// errNoDriver is a handle's answer when no driver runs behind it (a
// capsule parked on its lane snapshot's contract version).
var errNoDriver = errors.New("driver: no driver runs this work item: its lane snapshot targets another workflow contract version (blocked, kind contract)")

// OwnerMessage relays one owner message to the current step's agent
// (relay.go, workflow contract v2 §5.2): recorded first, then delivered
// into the live process or carried by the resumed session.
func (h *Handle) OwnerMessage(ctx context.Context, text, by string) (protocol.OwnerMessageAck, error) {
	if h.drv == nil {
		return protocol.OwnerMessageAck{}, errNoDriver
	}
	return h.drv.OwnerMessage(ctx, text, by)
}

// Exit is closed once Run has returned (the confirmed exit).
func (h *Handle) Exit() <-chan struct{} { return h.done }

func (h *Handle) PID() int          { return os.Getpid() }
func (h *Handle) StartedAt() string { return h.started }
func (h *Handle) Alive() bool       { return !h.Done() }
func (h *Handle) Done() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

// Terminate cancels Run and blocks, bounded, until it returned: a cancel
// that merely returned would let the old driver's journal appends land
// after the relaunch read the journal, so both would dispatch the same
// "fresh" ordinal.
func (h *Handle) Terminate(bound time.Duration) error {
	h.cancel()
	select {
	case <-h.done:
		if h.err != nil && !errors.Is(h.err, context.Canceled) {
			return h.err
		}
		return nil
	case <-time.After(bound):
		return fmt.Errorf("driver: did not exit within %s", bound)
	}
}

// StartConfig carries what one driver derivation needs from the runtime.
type StartConfig struct {
	WI         string
	CapsuleDir string
	BundleName string
	// SessionHandle is the owning session's PUBLIC fingerprint.
	SessionHandle string
	SpawnN        int64 // the job's derivation ordinal (≥1)
	Spawner       spawn.Spawner
	// Compose is the runtime's dispatch-time candidate composition
	// (polyforge#33; `<harness>:<model>` each): the driver's only
	// candidate source, called at every dispatch.
	Compose func() (map[string][]string, error)
	// Interrupted archives the dispatch the previous derivation left
	// without a result (nil on a fresh claim or a deliberate stop).
	Interrupted *InterruptedDispatch
	StepEnded   func(context.Context)
	// ClaudeConfigDir is the step claude processes' CLAUDE_CONFIG_DIR: the
	// claude resume decision locates a previous native session by it.
	ClaudeConfigDir string
	Log             EventLogger        // nil = stderr only
	Link            *supervise.Link    // nil = unsupervised
	Limits          supervise.Defaults // the global X, Y, T
	// LimitsFunc, when set, is consulted at every judgement so a live
	// daemon reload changes the bounds without a restart.
	LimitsFunc func() supervise.Defaults
	// Admission is the daemon's heavy-run admission queue
	// (Config.Admission).
	Admission VerifyAdmission
	// Forge is the forge binding (Config.Forge): the command environment
	// and the per-repo merge lock a step declaring it holds.
	Forge Forge
	// HubWrites makes a step round's pending `pf step hub` writes when it
	// hands off done (Config.HubWrites, workflow contract 4).
	HubWrites pendingwrites.Sink
	// OwnerWait is the runtime's owner-wait flag of the job (Config.OwnerWait,
	// ask.go): set while the step's `pf ask` is open, so the daemon counts
	// no running slot for it.
	OwnerWait func(waiting bool)
	// Language answers the live agent language (`[daemon] agent_language`),
	// read at every dispatch (StepSpawnAdapter.Language)
	// and whenever the daemon writes the opening or an event section of
	// artifacts/handoff.md (Config.Language).
	Language func() string
}

// Start instantiates the driver over the capsule and runs it.
func Start(ctx context.Context, cfg StartConfig) (*Handle, error) {
	if cfg.CapsuleDir == "" || cfg.BundleName == "" {
		return nil, fmt.Errorf("driver: capsule dir or bundle name empty")
	}
	wi := WIInfo{ID: cfg.WI, CapsuleDir: cfg.CapsuleDir, BundleName: cfg.BundleName, SessionHandle: cfg.SessionHandle}
	// A lane snapshot of another workflow contract version cannot run
	// here (a capsule the drain left in flight): park it blocked, kind
	// contract, and run nothing — a resume parks it again.
	if reason := snapshotContractMismatch(filepath.Join(cfg.CapsuleDir, capsule.DirWorkflows, cfg.BundleName)); reason != "" {
		if err := blockOnContract(filepath.Join(cfg.CapsuleDir, capsule.ExecutionJSON), cfg.WI, reason); err != nil {
			return nil, fmt.Errorf("driver: contract block: %w", err)
		}
		h := &Handle{done: make(chan struct{}), cancel: func() {}, started: fmt.Sprintf("driver-%d", cfg.SpawnN+1)}
		close(h.done)
		return h, nil
	}
	adapter, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI: wi, Spawner: cfg.Spawner, Link: cfg.Link, ClaudeConfigDir: cfg.ClaudeConfigDir,
		Language: cfg.Language,
	})
	if err != nil {
		return nil, fmt.Errorf("driver: adapter: %w", err)
	}
	// Step ordinals continue from the capsule's spawn journal (the
	// observable contract: drop paths, archive dirs), not from the job's
	// derivation ordinal, so a predecessor's stale drops stay fenced. An
	// unreadable journal refuses the start: it is the fence's source of
	// truth, never silently fresh.
	stepSeed, err := capsule.SpawnJournalMaxN(cfg.CapsuleDir)
	if err != nil {
		return nil, fmt.Errorf("driver: spawn journal unreadable: %w", err)
	}
	if cfg.Link != nil {
		cfg.Link.Reset() // a new driver epoch: no fence or verdict of a predecessor leaks in
	}
	drv, err := New(Config{
		WI:          wi,
		BundleDir:   adapter.BundleDir,
		Workflow:    adapter.Workflow,
		Spawner:     adapter,
		SpawnN:      int64(stepSeed),
		Log:         cfg.Log,
		Link:        cfg.Link,
		Limits:      cfg.Limits,
		LimitsFunc:  cfg.LimitsFunc,
		Compose:     cfg.Compose,
		Interrupted: cfg.Interrupted,
		StepEnded:   cfg.StepEnded,
		Admission:   cfg.Admission,
		Forge:       cfg.Forge,
		OwnerWait:   cfg.OwnerWait,
		HubWrites:   cfg.HubWrites,
		Language:    cfg.Language,
	})
	if err != nil {
		return nil, fmt.Errorf("driver: %w", err)
	}
	// A capsule that arrives BLOCKED (a parked verdict) would make Run
	// exit at once, forever: every claim of a parked capsule is the
	// owner's resume, so the frozen step is re-dispatched in place.
	if drv.Snapshot().WorkflowStatus == StatusBlocked {
		if err := drv.ResumeCurrent(); err != nil {
			return nil, fmt.Errorf("driver: owner resume of the parked workflow: %w", err)
		}
	}
	dctx, cancel := context.WithCancel(ctx)
	h := &Handle{done: make(chan struct{}), cancel: cancel, started: fmt.Sprintf("driver-%d", cfg.SpawnN+1), drv: drv}
	go func() {
		h.err = drv.Run(dctx)
		// a returned Run writes nothing more — the runtime may hand the
		// capsule to another writer now (a relaunch)
		drv.stopWrites()
		close(h.done)
		if h.err != nil {
			fmt.Fprintf(os.Stderr, "driver: %s: Run: %v\n", cfg.WI, h.err)
		}
	}()
	return h, nil
}

// ErrDriverStopped is every write's answer once Run has returned: the
// runtime may already have handed the capsule to another writer (a
// relaunch), so a late write — an answer that reached the old handle —
// must fail loudly, never land or vanish.
var ErrDriverStopped = errors.New("driver: this driver's Run has returned; it writes nothing more — retry once the work item's next derivation is live")

// stopWrites fences the driver once Run has returned: every later
// transition answers ErrDriverStopped (a failed persist's error stays the
// answer when it came first).
func (d *Driver) stopWrites() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failed == nil {
		d.failed = ErrDriverStopped
	}
}
