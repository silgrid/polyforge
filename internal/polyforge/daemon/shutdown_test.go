package daemon

// shutdown_test.go + restart wiring: the shutdown (state.json on disk,
// the socket closing, the jobs torn down in parallel) and the daemon-start
// Restart reconciliation wiring.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// TestShutdownSequence: two live jobs → the sequence is fast (no per-job
// grace window), Runtime's state.json lands with both jobs recorded, the
// socket closes and Run returns.
func TestShutdownSequence(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_s1")
	td.submitClaim(t, "wi_s2")

	start := time.Now()
	if err := td.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("shutdown took %v (degradation path expected to be fast)", el)
	}
	select {
	case <-td.runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the shutdown sequence")
	}

	// Runtime's state.json landed with both jobs recorded (the
	// bookkeeping the next start reconciles against aihub).
	raw, err := os.ReadFile(filepath.Join(td.cfg.DataDir, "runtime-state.json"))
	if err != nil {
		t.Fatalf("state.json: %v", err)
	}
	var st struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("state.json unparsable: %v", err)
	}
	if len(st.Jobs) != 2 {
		t.Fatalf("state.json jobs = %d, want 2 (both interrupted jobs recorded)", len(st.Jobs))
	}
	for _, j := range st.Jobs {
		if _, ok := j["work_item_id"]; !ok {
			t.Fatalf("state.json job record missing work_item_id: %v", j)
		}
	}

	// The UDS face is gone.
	if dialWorks(td.sock) {
		t.Fatal("socket still accepts connections after shutdown")
	}
}

// TestRestartWiring: a recorded job whose aihub verdict is running is
// recovered at daemon start (transcript resume through the recovery
// engine) and re-attached to a fresh monitor — the observe
// lane reports it.
func TestRestartWiring(t *testing.T) {
	setGitIdentity(t)
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	data := filepath.Join(dir, "data")
	hubBase := filepath.Join(dir, "hub")
	for _, d := range []string{ws, data, hubBase} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wi := "wi_r1"
	bare := mkBare(t, hubBase, wi)
	capDir := filepath.Join(ws, "capsules", wi)
	gitDir := filepath.Join(data, "git", wi+".git")
	seedCapsuleGit(t, capDir, gitDir, bare)
	session := e2eSession(wi)
	transcript := filepath.Join(capDir, "session", "session-"+apiSessionFingerprint(session)+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(`{"type":"message_end"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedStateFile(t, data, "m-test", map[string]any{
		"work_item_id":        wi,
		"project":             "proj",
		"session_id":          session,
		"session_fingerprint": apiSessionFingerprint(session),
		"state":               "running",
		"capsule_dir":         capDir,
		"git_dir":             gitDir,
		"remote_url":          bare,
		"clean_pause":         false,
		"transcript":          transcript,
		"bundle_name":         "demo-bundle",
		"agent_pid":           0,
		"spawn_n":             1,
	})

	td := newTestDaemon(t, func(c *Config) {
		c.Mapping = mappingFor(ws)
		c.DataDir = data
		c.GitRemoteBase = hubBase
	})

	// The recovery engine re-derived the recorded agent at restart
	// (polyforge#106: the WI-Agent transcript-resume discriminator is
	// retired with the legacy cascade; the driver derivation's own
	// resume shape is pinned by the e2e journal assertions, s08/s17 —
	// here the pin is that restart reconciliation re-derives AT ALL).
	deadline := time.Now().Add(5 * time.Second)
	for {
		td.spawner.mu.Lock()
		calls := len(td.spawner.calls)
		td.spawner.mu.Unlock()
		if calls > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recorded job was never recovered at restart")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The recovered job is supervised again, with its attempt identity.
	td.waitMonitored(t, wi)
	if ji, ok := td.rt.Info(wi); !ok || ji.SpawnN == 0 || ji.State != runtime.StateRunning {
		t.Fatalf("restarted job = %+v (live %v)", ji, ok)
	}
}

// repo review 2026-10-16 B-16: closing the face makes Serve return
// ErrServerClosed; Run must still wait for the rest of the shutdown
// before it returns and the process exits.
func TestRunReturnsOnlyAfterTheShutdownSequence(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.AgentStarter = func(ctx context.Context, in runtime.AgentStartInput) (Agent, error) {
			return &fakeAgent{}, nil
		}
	})
	td.submitClaim(t, "wi_sweep")
	go func() { _ = td.Shutdown(context.Background(), DirectShutdownCause()) }()
	select {
	case <-td.runDone:
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return")
	}
	select {
	case <-td.shutDone:
	default:
		t.Fatal("Run returned before the shutdown sequence finished")
	}
}

// slowTermAgent takes a while to terminate.
type slowTermAgent struct{ fakeAgent }

func (a *slowTermAgent) Terminate(b time.Duration) error {
	time.Sleep(1500 * time.Millisecond)
	return a.fakeAgent.Terminate(b)
}

// The shutdown terminates (and WIP-saves) the live jobs in parallel: three
// agents that take 1.5s each to terminate cost the shutdown ~1.5s, not
// 4.5s.
func TestShutdownTerminatesJobsInParallel(t *testing.T) {
	var mu sync.Mutex
	var agents []*slowTermAgent
	td := newTestDaemon(t, func(c *Config) {
		c.AgentStarter = func(ctx context.Context, in runtime.AgentStartInput) (Agent, error) {
			a := &slowTermAgent{}
			mu.Lock()
			agents = append(agents, a)
			mu.Unlock()
			return a, nil
		}
	})
	for _, wi := range []string{"wi_par1", "wi_par2", "wi_par3"} {
		td.submitClaim(t, wi)
	}
	start := time.Now()
	if err := td.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if el := time.Since(start); el > 3500*time.Millisecond {
		t.Fatalf("shutdown took %v: the agents were terminated one after another", el)
	}
	mu.Lock()
	defer mu.Unlock()
	for i, a := range agents {
		if !a.isDone() {
			t.Fatalf("agent %d was not terminated", i)
		}
	}
}

// Only the data-dir lock holder sweeps crash residue: a second start the
// lock refuses leaves the running daemon's askpass scripts and carrier
// temp repos alone (a push of the running daemon may be using them).
func TestRefusedStartLeavesTheRunningDaemonsGitFiles(t *testing.T) {
	td := newTestDaemon(t, nil)
	gitRoot := filepath.Join(td.cfg.DataDir, "git")
	askpass := filepath.Join(gitRoot, "askpass", "askpass-live")
	carrier := filepath.Join(gitRoot, "pf-carrier-live")
	for _, dir := range []string{filepath.Dir(askpass), carrier} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(askpass, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := td.cfg
	cfg.SocketPath = filepath.Join(t.TempDir(), "second.sock")
	d2, err := New(cfg)
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	if err := d2.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "data-dir lock") {
		t.Fatalf("second Run = %v, want the data-dir lock refusal", err)
	}
	for _, p := range []string{askpass, carrier} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("the refused start removed the running daemon's %s: %v", p, err)
		}
	}
}

// An explicit -auto is persisted only by the data-dir lock holder: a
// second start the lock refuses leaves the running daemon's state file as
// it was.
func TestRefusedStartLeavesTheRunningDaemonsStateFile(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.persistModeQuietly()
	before, err := os.ReadFile(td.statePath())
	if err != nil {
		t.Fatal(err)
	}
	cfg := td.cfg
	cfg.SocketPath = filepath.Join(t.TempDir(), "second.sock")
	flip := !td.modes.Auto()
	cfg.Auto = &flip
	d2, err := New(cfg)
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	if err := d2.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "data-dir lock") {
		t.Fatalf("second Run = %v, want the data-dir lock refusal", err)
	}
	after, err := os.ReadFile(td.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the refused start rewrote the running daemon's state file:\n%s\n->\n%s", before, after)
	}
}
