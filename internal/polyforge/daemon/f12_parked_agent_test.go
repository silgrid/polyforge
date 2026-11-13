package daemon

// f12_parked_agent_test.go — the parked-agent fixture of the shutdown/restart
// full-chain test (f12_shutdown_restart_e2e_test.go).

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// parkedSpawner spawns fake agents whose teardown parks until the release
// channel closes — the stand-in for an agent that is still working through
// its stop boundary, keeping the graceful shutdown (and therefore the
// data-dir flock and the UDS listener) alive. Spawn runs on the runtime's
// message-loop goroutine and Terminate on the shutdown's goroutines while
// the test goroutine polls callCount and parkedStops, so the counters are
// atomic.
type parkedSpawner struct {
	release chan struct{}
	calls   atomic.Int64
	stops   atomic.Int64
}

func (s *parkedSpawner) Spawn(ctx context.Context, p runtime.SpawnParams) (runtime.AgentHandle, error) {
	s.calls.Add(1)
	return &parkedAgent{owner: s}, nil
}

func (s *parkedSpawner) callCount() int { return int(s.calls.Load()) }

// parkedStops counts the stop sequences parked in one of this spawner's
// agents.
func (s *parkedSpawner) parkedStops() int { return int(s.stops.Load()) }

// parkedAgent is a fakeAgent whose Terminate first parks until the
// spawner's release channel closes. The handle contract's own bound stays
// the outer limit: past it Terminate answers an error without terminating
// (the caller's teardown wait then gives up), so a test that fails before
// releasing leaves no goroutine parked for good.
type parkedAgent struct {
	fakeAgent
	owner *parkedSpawner
}

func (a *parkedAgent) Terminate(bound time.Duration) error {
	a.owner.stops.Add(1)
	select {
	case <-a.owner.release:
	case <-time.After(bound):
		return errors.New("parked agent: stop boundary not released within the bound")
	}
	return a.fakeAgent.Terminate(bound)
}
