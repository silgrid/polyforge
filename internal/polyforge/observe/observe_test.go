package observe

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// The lifecycle queue is consumed directly: a recovery of a work item no
// monitor watches (a restart's, or a paused record's resume) builds the
// monitor when its agent is live, is dropped when nothing is live, and a
// recovery behind a start is adopted by that start's monitor — whose
// driver exit then asks for recovery under the adopted derivation.
func TestLifecycleMessagesBuildAndFeedMonitors(t *testing.T) {
	rt := supervise.NewQueue[protocol.RTMsg]()
	ob := supervise.NewQueue[protocol.OBMsg]()
	agents := newAgentTable()
	o, err := New(Config{RT: rt, OB: ob, Agent: agents.lookup, DataDir: t.TempDir(), PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { o.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	capsule := func() string {
		dir := filepath.Join(t.TempDir(), "capsule")
		if err := os.MkdirAll(filepath.Join(dir, "runtime-state"), 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	resumed := newFakeHandle("resumed")
	agents.set("wi_resumed", resumed, capsule())
	rt.Push(protocol.RTMsg{Kind: protocol.RTRecovered, WorkItem: "wi_none", SpawnN: 2})
	rt.Push(protocol.RTMsg{Kind: protocol.RTRecovered, WorkItem: "wi_resumed", SpawnN: 3})

	first, second := newFakeHandle("first"), newFakeHandle("second")
	agents.set("wi_live", first, capsule())
	rt.Push(protocol.RTMsg{Kind: protocol.RTStarted, WorkItem: "wi_live", SpawnN: 1})
	soon(t, 2*time.Second, "the started monitor", func() bool { return o.lookup("wi_live") != nil })
	agents.set("wi_live", second, capsule())
	rt.Push(protocol.RTMsg{Kind: protocol.RTRecovered, WorkItem: "wi_live", SpawnN: 2})
	soon(t, 2*time.Second, "the recovery adopted", func() bool { return o.lookup("wi_live").currentSpawn() == 2 })

	if m := o.lookup("wi_resumed"); m == nil || m.currentSpawn() != 3 {
		t.Fatalf("a recovery nothing monitored must build the monitor, got %v", m)
	}
	if o.lookup("wi_none") != nil {
		t.Fatal("a recovery with no live agent must not build a monitor")
	}
	second.die()
	msg := waitOB(t, ob, "the adopted derivation's exit")
	if msg.WorkItem != "wi_live" || msg.SpawnN != 2 || msg.Kind != protocol.OBNeedsRecovery {
		t.Fatalf("OB message %+v, want needs_recovery of wi_live@2", msg)
	}
}
