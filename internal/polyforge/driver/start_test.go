package driver

// start_test.go: the driver's agent handle — Terminate joins Run's exit,
// the exit signal, the candidate pass-through, and the driver-event /
// supervision-link wiring.

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// TestTerminateJoinsDriverExit is the astra-r2 #1 regression: Terminate
// must BLOCK (bounded) until the driver goroutine actually returned —
// a cancel that merely returns lets the old driver's journal appends
// land after the relaunch reads the journal, re-colliding ordinals.
func TestTerminateJoinsDriverExit(t *testing.T) {
	h := &Handle{done: make(chan struct{}), cancel: func() {}}
	// the driver goroutine exits 150ms after cancel
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		<-time.After(150 * time.Millisecond)
		h.err = context.Canceled
		close(h.done)
	}()
	start := time.Now()
	if err := h.Terminate(5 * time.Second); err != nil {
		t.Fatalf("terminate must join a clean (context-cancelled) driver exit: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("terminate returned before the driver exited (after %v)", elapsed)
	}
	select {
	case <-closed:
	default:
		t.Fatal("terminate must return only after the driver goroutine closed done")
	}
	select {
	case <-h.Exit():
	default:
		t.Fatal("the exit signal must fire once Run returned")
	}
}

// TestTerminateBoundedWhenDriverNeverExits: a wedged driver answers the
// bound, loudly — the caller surfaces the incomplete teardown instead of
// assuming the writer is gone.
func TestTerminateBoundedWhenDriverNeverExits(t *testing.T) {
	h := &Handle{done: make(chan struct{}), cancel: func() {}}
	if err := h.Terminate(40 * time.Millisecond); err == nil {
		t.Fatal("terminate must fail loudly when the driver does not exit in bound")
	}
}

// startSpawner is a scripted spawn.Spawner: it records every Spawn's
// Params and parks the derivation until its terminate channel closes.
type startSpawner struct {
	mu     sync.Mutex
	params []spawn.Params
	block  chan struct{}
}

func (s *startSpawner) Spawn(ctx context.Context, p spawn.Params) (spawn.Handle, error) {
	s.mu.Lock()
	s.params = append(s.params, p)
	s.mu.Unlock()
	if s.block == nil {
		s.block = make(chan struct{})
	}
	return &startStepHandle{block: s.block}, nil
}

func (s *startSpawner) last() spawn.Params {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.params[len(s.params)-1]
}

type startStepHandle struct {
	block chan struct{}
	once  sync.Once
}

func (h *startStepHandle) PID() int          { return 0 }
func (h *startStepHandle) StartedAt() string { return "fake" }
func (h *startStepHandle) Alive() bool       { return true }
func (h *startStepHandle) Done() bool        { return false }
func (h *startStepHandle) Terminate(bound time.Duration) error {
	h.once.Do(func() { close(h.block) })
	return nil
}

// TestStartForwardsStepCandidates pins the pass-through: the driver's
// FIRST dispatch pins its model from the effective list the runtime
// supplied (the post-precedence compose), not the bundle's own list.
func TestStartForwardsStepCandidates(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "workflows", "wf")
	if err := os.MkdirAll(filepath.Join(bundle, "steps"), 0o755); err != nil {
		t.Fatal(err)
	}
	front := "---\nname: wf\ndefaults:\n  model_candidates: [pi:bundle-weak]\nsteps:\n  - { id: fix }\n---\n# body\n"
	if err := os.WriteFile(filepath.Join(bundle, "workflow.md"), []byte(front), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "steps", "fix.md"), []byte("do the fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sp := &startSpawner{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, err := Start(ctx, StartConfig{
		WI:            "wi_pass",
		CapsuleDir:    dir,
		BundleName:    "wf",
		SessionHandle: "fp123",
		SpawnN:        1,
		Spawner:       sp,
		Compose: composeOf(map[string][]string{
			"fix": {"pi:eff-a", "pi:eff-b"},
		}),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer h.Terminate(2 * time.Second)
	waitUntil(t, "the first dispatch", func() bool {
		sp.mu.Lock()
		defer sp.mu.Unlock()
		return len(sp.params) > 0
	})
	if got := sp.last().Model; got != "eff-a" {
		t.Fatalf("first dispatch model %q, want the effective head eff-a", got)
	}
	// the Start's session handle names the dispatch (spawn token and
	// capsule transcript)
	if p := sp.last(); p.SessionHandle != "fp123" || filepath.Base(p.TranscriptPath) != "session-fp123.jsonl" {
		t.Fatalf("dispatch handle %q / transcript %q, want the Start's fp123", p.SessionHandle, p.TranscriptPath)
	}
}

// startLog records driver events.
type startLog struct {
	mu    sync.Mutex
	kinds []string
}

func (l *startLog) Append(wi, kind string, body any) error {
	l.mu.Lock()
	l.kinds = append(l.kinds, kind)
	l.mu.Unlock()
	return nil
}

func (l *startLog) has(kind string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range l.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// TestStartWiresDriverEventsAndSupervisionLink pins the v2 event base
// and the contract wiring: the driver's events reach the Log the daemon
// supplied (not a stderr logger) and the step_started fact reaches the
// WI's link, carrying the dispatch's model, harness and transcript.
func TestStartWiresDriverEventsAndSupervisionLink(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "workflows", "wf")
	if err := os.MkdirAll(filepath.Join(bundle, "steps"), 0o755); err != nil {
		t.Fatal(err)
	}
	front := "---\nname: wf\nsteps:\n  - { id: fix }\n---\n# body\n"
	if err := os.WriteFile(filepath.Join(bundle, "workflow.md"), []byte(front), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "steps", "fix.md"), []byte("do the fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sp := &startSpawner{}
	log := &startLog{}
	link := supervise.NewLink("wi_wire")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, err := Start(ctx, StartConfig{
		WI: "wi_wire", CapsuleDir: dir, BundleName: "wf", SpawnN: 1, Spawner: sp,
		Log: log, Link: link, Compose: composeOf(map[string][]string{"fix": {"pi:m-a"}}),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer h.Terminate(2 * time.Second)
	waitUntil(t, "the step_started event in the supplied log", func() bool { return log.has("step_started") })
	var started *supervise.StepStarted
	waitUntil(t, "the step_started fact on the link", func() bool {
		for _, f := range link.Facts.Drain() {
			if st, ok := f.(supervise.StepStarted); ok {
				c := st
				started = &c
			}
		}
		return started != nil
	})
	if started.Step != "fix" || started.Model != "m-a" || started.Harness != "pi" || started.SpawnN != 1 {
		t.Fatalf("step_started fact = %+v", *started)
	}
	if started.Thresholds.StepTimeout <= 0 || started.Thresholds.Tool <= 0 || started.Thresholds.Silence <= 0 {
		t.Fatalf("the fact must carry the thresholds, got %+v", started.Thresholds)
	}
}
