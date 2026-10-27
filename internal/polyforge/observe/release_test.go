package observe

// release_test.go — form ①: the runtime's terminal
// close releases the work item's monitor BEFORE its seal push and
// SessionEnd (runtime Config.ReleaseMonitor → Observe.Release). Release
// must return only once the monitor is gone: its in-flight capsule push
// finished, and nothing it still had queued pushed afterwards — else a
// monitor push can still reach the hub after the close's SessionEnd and
// be refused "Authentication failed" (the pi#126 GIT-500).

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func TestReleaseDrainsTheInFlightPushAndStopsTheMonitor(t *testing.T) {
	var mu sync.Mutex
	pushes := 0
	started := make(chan struct{}, 8)
	unblock := make(chan struct{})
	push := func(ctx context.Context, _ gitops.Opts, _ string) (string, bool, gitops.PushTimings, error) {
		mu.Lock()
		pushes++
		mu.Unlock()
		started <- struct{}{}
		<-unblock
		return "sha", true, gitops.PushTimings{}, nil
	}
	rt := supervise.NewQueue[protocol.RTMsg]()
	ob := supervise.NewQueue[protocol.OBMsg]()
	agents := newAgentTable()
	o, err := New(Config{RT: rt, OB: ob, Agent: agents.lookup, DataDir: t.TempDir(),
		PollInterval: 5 * time.Millisecond, PushFunc: push})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { o.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	dir := filepath.Join(t.TempDir(), "capsule")
	if err := os.MkdirAll(filepath.Join(dir, "runtime-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	agents.set("wi_rel", newFakeHandle("a"), dir)
	rt.Push(protocol.RTMsg{Kind: protocol.RTStarted, WorkItem: "wi_rel", SpawnN: 1})
	soon(t, 2*time.Second, "the monitor", func() bool { return o.lookup("wi_rel") != nil })
	m := o.lookup("wi_rel")

	m.writeExec(t, 1, "s")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the monitor never pushed version 1")
	}
	released := make(chan struct{})
	go func() { o.Release("wi_rel"); close(released) }()
	// The terminal version lands while the push is still in flight.
	m.writeExec(t, 2, "s")
	select {
	case <-released:
		t.Fatal("Release returned while the monitor's push was still in flight")
	case <-time.After(200 * time.Millisecond):
	}
	close(unblock)
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("Release never returned after the in-flight push finished")
	}
	if o.lookup("wi_rel") != nil {
		t.Fatal("the released monitor is still registered")
	}
	time.Sleep(100 * time.Millisecond) // several ticks: a surviving monitor would push version 2
	mu.Lock()
	n := pushes
	mu.Unlock()
	if n != 1 {
		t.Fatalf("pushes = %d after Release, want 1 (no monitor push may follow the release)", n)
	}
	if len(eventsOf(m, EvJobStopped)) != 1 {
		t.Fatalf("job_stopped events = %d, want 1", len(eventsOf(m, EvJobStopped)))
	}
}
