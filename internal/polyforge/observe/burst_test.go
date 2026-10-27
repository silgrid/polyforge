package observe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// A burst of recovery requests across many work items while nobody
// consumes the OB queue: every request must still be delivered (the
// queue never drops), so no work item stays "recovering" with its
// request lost.
func TestRecoveryBurstNeverDropped(t *testing.T) {
	const n = 300
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

	handles := make([]*fakeHandle, n)
	for i := 0; i < n; i++ {
		wi := fmt.Sprintf("wi_%03d", i)
		dir := filepath.Join(t.TempDir(), "capsule")
		if err := os.MkdirAll(filepath.Join(dir, "runtime-state"), 0o755); err != nil {
			t.Fatal(err)
		}
		handles[i] = newFakeHandle(wi)
		agents.set(wi, handles[i], dir)
		rt.Push(protocol.RTMsg{Kind: protocol.RTStarted, WorkItem: wi, SpawnN: 1})
	}
	soon(t, 5*time.Second, "all monitors registered", func() bool {
		for i := 0; i < n; i++ {
			if o.lookup(fmt.Sprintf("wi_%03d", i)) == nil {
				return false
			}
		}
		return true
	})
	for _, h := range handles {
		h.die()
	}
	seen := map[string]int{}
	soon(t, 10*time.Second, "every recovery request queued", func() bool {
		// Queue.Len was deleted with the audit's dead-code sweep; the wait
		// drains through Pop and counts distinct work items instead of a
		// size probe.
		for {
			m, ok := ob.Pop()
			if !ok {
				return false
			}
			if m.Kind == protocol.OBNeedsRecovery {
				seen[m.WorkItem]++
				if len(seen) >= n {
					return true
				}
			}
		}
	})

	if len(seen) != n {
		t.Fatalf("recovery requests delivered for %d of %d work items", len(seen), n)
	}
	for wi, c := range seen {
		if c != 1 {
			t.Fatalf("%s: %d requests, want exactly 1", wi, c)
		}
	}
}
