package driver

// start.go: runs a Driver as the runtime's agent — the handle the
// runtime kills and the observe lane watches for the driver's exit.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/spawn"
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

// Answer delivers an owner answer to the driver's pending question.
func (h *Handle) Answer(questionID, answer, answeredBy string) error {
	return h.drv.Answer(questionID, answer, answeredBy)
}

// ReviseBinding applies owner corrections of done step binding commands
// through the live driver's single write entry point. One
// call carries the whole batch, so a result whose bindings all carry the
// same malformed quote is corrected before the output is validated again.
func (h *Handle) ReviseBinding(stepID string, corrections []capsule.BindingCorrection, by, reason string) error {
	return h.drv.ReviseBinding(stepID, BindingRevisionRequest{Corrections: corrections, By: by, Reason: reason})
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
}

// Start instantiates the driver over the capsule and runs it.
func Start(ctx context.Context, cfg StartConfig) (*Handle, error) {
	if cfg.CapsuleDir == "" || cfg.BundleName == "" {
		return nil, fmt.Errorf("driver: capsule dir or bundle name empty")
	}
	wi := WIInfo{ID: cfg.WI, CapsuleDir: cfg.CapsuleDir, BundleName: cfg.BundleName, SessionHandle: cfg.SessionHandle}
	adapter, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI: wi, Spawner: cfg.Spawner, Link: cfg.Link, ClaudeConfigDir: cfg.ClaudeConfigDir,
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
		close(h.done)
		if h.err != nil {
			fmt.Fprintf(os.Stderr, "driver: %s: Run: %v\n", cfg.WI, h.err)
		}
	}()
	return h, nil
}
