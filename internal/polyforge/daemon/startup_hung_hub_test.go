package daemon

// startup_hung_hub_test.go — the restart never waits on aihub: a daemon
// whose hub hangs at a record's session verify answers `pf ops status`
// within a second, recovers nothing before the hub answered, and the
// record ends up recovered and monitored (announced once) once it does.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

func TestStartupWithHungHubServesStatusPromptly(t *testing.T) {
	setGitIdentity(t)
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	data := filepath.Join(dir, "data")
	hubBase := filepath.Join(dir, "hub")
	for _, p := range []string{ws, data, hubBase} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	const wi = "wi_startup_hung"
	bare := mkBare(t, hubBase, wi)
	capDir := filepath.Join(ws, "capsules", wi)
	gitDir := filepath.Join(data, "git", wi+".git")
	seedCapsuleGit(t, capDir, gitDir, bare)
	seedTranscript(t, capDir, apiSessionFingerprint(e2eSession(wi)))
	seedStateJobs(t, data, "m-e2e", e2eRecord(wi, capDir, gitDir, bare))
	// a submit the previous process accepted but never claimed: its
	// replay must not probe the hung hub before the socket serves
	const queued = "wi_startup_queued"
	mkBare(t, hubBase, queued)
	now := time.Now().UTC()
	qblob, err := json.Marshal(submitQueueState{Version: 1, Entries: []qEntry{{
		WorkItemID: queued, Priority: "normal", EnqueuedAt: now, SubmittedAt: now,
		Origin: runtime.Origin{Kind: runtime.OriginChat},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, submitQueueFile), qblob, 0o600); err != nil {
		t.Fatal(err)
	}
	hub := newFakeHub()
	hub.hold(wi, e2eSession(wi), api.StatusRunning)
	hub.status[queued] = api.StatusQueued
	release := make(chan struct{})
	released := false
	open := func() {
		if !released {
			released = true
			close(release)
		}
	}
	t.Cleanup(open)
	hub.blockVerify = release
	hub.blockProbe = release
	hub.verifyEntered = make(chan struct{})
	sp := &fakeSpawner{}
	sock := filepath.Join(dir, "d.sock")
	cfg := e2eConfig(sock, ws, data, hubBase, hub, sp)
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	runErr := make(chan error, 1)
	go func() { runErr <- d.Run(context.Background()) }()
	t.Cleanup(func() {
		open()
		_ = d.Shutdown(context.Background(), DirectShutdownCause())
		select {
		case <-runErr:
		case <-time.After(15 * time.Second):
		}
		// the shutdown does not wait for the queued submit's claim; let its
		// deferral finish writing before the temp dir goes
		waitFor(t, "the claim to settle after the shutdown", 15*time.Second, func() bool {
			ji, live := d.rt.Info(queued)
			return !live || ji.State != runtime.StateClaiming
		})
	})
	select {
	case <-hub.verifyEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("the restart verify never reached the hub")
	}

	client := cli.NewDaemonClient(sock)
	type answer struct {
		raw json.RawMessage
		err error
	}
	got := make(chan answer, 1)
	go func() {
		// the socket file exists before the face serves: wait for it, then ask
		for i := 0; i < 500; i++ {
			if _, err := os.Stat(sock); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		raw, err := client.Status()
		got <- answer{raw, err}
	}()
	var st cli.StatusResponse
	select {
	case a := <-got:
		if a.err != nil {
			t.Fatalf("status: %v", a.err)
		}
		if err := json.Unmarshal(a.raw, &st); err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon did not serve /status while the hub hangs (startup blocked on the restart verify)")
	}
	for i := 0; i < 3; i++ {
		t0 := time.Now()
		raw, err := client.Status()
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if d := time.Since(t0); d > time.Second {
			t.Fatalf("status took %s while the hub hangs, want ≤ 1s", d)
		}
		st = cli.StatusResponse{}
		if err := json.Unmarshal(raw, &st); err != nil {
			t.Fatal(err)
		}
	}
	if len(st.Jobs) != 1 || st.Jobs[0].WorkItemID != wi {
		t.Fatalf("status while the verify hangs = %+v, want the kept record", st.Jobs)
	}
	if sp.callCount() != 0 {
		t.Fatal("nothing may be recovered before the hub answered")
	}

	open()
	waitFor(t, "the record recovered once the hub answers", 20*time.Second, func() bool {
		// the replayed submit's claim (no longer held back behind the
		// restart) spawns too: look at the recovered record itself
		ji, live := d.rt.Info(wi)
		_, monitored := d.obs.Progress(wi)
		return live && ji.State == runtime.StateRunning && ji.SpawnN == 2 && monitored
	})
	if hub.endCount() != 0 {
		t.Fatalf("the recovery must not end the session, got %+v", hub.lastEnd())
	}
	// announced exactly once, with a monitor present: one job_started, no
	// "dropped" job_stopped for a recovery nobody watched yet
	time.Sleep(200 * time.Millisecond) // a duplicate attachment would land by now
	starts, stops := 0, 0
	for _, k := range eventKinds(d, wi) {
		switch k {
		case observe.EvJobStarted:
			starts++
		case observe.EvJobStopped:
			stops++
		}
	}
	if starts != 1 || stops != 0 {
		t.Fatalf("%s observe events: %d job_started, %d job_stopped (want 1, 0): %v", wi, starts, stops, eventKinds(d, wi))
	}
	// the unprobed queue entry is claimed once the hub answers
	waitFor(t, "the replayed submit to be claimed", 20*time.Second, func() bool {
		ji, live := d.rt.Info(queued)
		return live && ji.State == runtime.StateRunning
	})
	hub.mu.Lock()
	n := 0
	for _, c := range hub.claims {
		if c.WorkItemID == queued {
			n++
		}
	}
	hub.mu.Unlock()
	if n != 1 {
		t.Fatalf("the replayed submit claimed %d times, want 1", n)
	}
	if c := sp.callCount(); c != 2 {
		t.Fatalf("%d agent spawns, want 2 (the one recovery, the one claim)", c)
	}
}
