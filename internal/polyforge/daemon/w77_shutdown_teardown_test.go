package daemon

// the shutdown takes the task-repo WIP snapshot only once the
// old driver Run is provably gone. A Run that outlives the handle's
// Terminate is still a writer; snapshotting next to it races that writer
// (the recovery path has the same invariant).

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// aliveRunAgent is the bridge shape whose Run does not observe its cancel
// inside the teardown bound: Terminate reports the timeout while Done stays
// false until the test releases it.
type aliveRunAgent struct {
	mu       sync.Mutex
	released bool
	exit     chan struct{}
}

func newAliveRunAgent() *aliveRunAgent {
	return &aliveRunAgent{exit: make(chan struct{})}
}

func (a *aliveRunAgent) PID() int          { return 0 }
func (a *aliveRunAgent) StartedAt() string { return "" }
func (a *aliveRunAgent) Alive() bool       { return !a.Done() }
func (a *aliveRunAgent) Exit() <-chan struct{} {
	return a.exit
}
func (a *aliveRunAgent) Done() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.released
}
func (a *aliveRunAgent) Terminate(time.Duration) error {
	return errors.New("driver: did not exit within the teardown bound")
}
func (a *aliveRunAgent) release() {
	a.mu.Lock()
	if !a.released {
		a.released = true
		close(a.exit)
	}
	a.mu.Unlock()
}

func TestShutdownDoesNotSnapshotWIPWhileTheOldRunIsAlive(t *testing.T) {
	agent := newAliveRunAgent()
	t.Cleanup(agent.release)
	td := newTestDaemon(t, func(c *Config) {
		c.AgentStarter = func(ctx context.Context, in runtime.AgentStartInput) (Agent, error) {
			return agent, nil
		}
	})
	td.submitClaim(t, "wi_w77_shutdown")

	shutDone := make(chan error, 1)
	go func() { shutDone <- td.Shutdown(context.Background(), DirectShutdownCause()) }()

	// The teardown reported the timeout: the shutdown must wait for the old
	// Run instead of taking the WIP snapshot (and finishing) beside it.
	select {
	case err := <-shutDone:
		t.Fatalf("the shutdown completed while the old driver Run was still alive (WIP snapshot taken next to a live writer): %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	agent.release()
	select {
	case err := <-shutDone:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the shutdown did not finish after the old Run exited")
	}
}
