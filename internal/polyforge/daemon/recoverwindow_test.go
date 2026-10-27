package daemon

// recoverwindow_test.go: the recovery-window regressions of the e2e bug
// lane. Two pins:
//
//   - Bug 2: the recovery window (state RECOVERING) passes, the monitor
//     adopts the recovered derivation, and a CLI wrap then closes the
//     work item exactly once.
//
//   - the supervision config seam: daemon.Config.Supervision carries the
//     bounds into the assembled supervisor, and a driver that keeps
//     stalling is bounded by the recovery engine's consecutive-failures
//     ledger (C7).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// ---------------------------------------------------------------------------
// Bug 2: the recovery window, then a single close.

// parkingSpawner parks every spawn AFTER the first (the recovery engine's
// re-derivation), holding the runtime in StateRecovering until released —
// the recovery window, stretched to a test-controllable length.
type parkingSpawner struct {
	fakeSpawner
	park chan struct{}
}

func (s *parkingSpawner) Spawn(ctx context.Context, p runtime.SpawnParams) (runtime.AgentHandle, error) {
	if s.callCount() >= 1 {
		<-s.park
	}
	return s.fakeSpawner.Spawn(ctx, p)
}

func TestRecoveryWindowThenWrapClosesOnce(t *testing.T) {
	park := make(chan struct{})
	var sp *parkingSpawner
	td := newTestDaemon(t, func(c *Config) {
		sp = &parkingSpawner{park: park}
		c.Spawner = sp
	})
	td.submitClaim(t, "wi_rw")
	td.waitMonitored(t, "wi_rw")

	// A confirmed agent death arms the recovery engine; the parked
	// re-derivation holds it in StateRecovering.
	sp.agentOf(0).Terminate(0)
	waitState(t, td, "wi_rw", "recovering")

	// The window passes: the re-derivation completes.
	close(park)
	waitState(t, td, "wi_rw", "running")
	// The monitor's adoption of the new derivation lags the RUNNING flip
	// by the attachment pipeline: poll until it logs agent_recovered for
	// spawn 2 (the recovered derivation's counter).
	adopted := func() bool {
		for _, e := range td.obs.Events().SinceAfter("wi_rw", 0) {
			if e.Kind != observe.EvAgentRecovered {
				continue
			}
			var b struct {
				SpawnN int `json:"spawn_n"`
			}
			if json.Unmarshal(e.Body, &b) == nil && b.SpawnN == 2 {
				return true
			}
		}
		return false
	}
	deadline := time.Now().Add(5 * time.Second)
	for !adopted() {
		if time.Now().After(deadline) {
			t.Fatal("the monitor never adopted the recovered derivation (no agent_recovered spawn_n 2)")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := td.client.Wrap("wi_rw", "wrap"); err != nil {
		t.Fatalf("post-window wrap: %v", err)
	}
	waitGone(t, td, "wi_rw")
	if td.hub.endCount() != 1 {
		t.Fatalf("session-end calls = %d, want 1", td.hub.endCount())
	}
	if st := td.hub.lastEnd().Status; st != protocol.FinishWrapped {
		t.Fatalf("end status = %q, want wrapped", st)
	}
}

// ---------------------------------------------------------------------------
// The supervision config seam + the driver-crash bound.

// TestSupervisionStallWindowDrivesRecoveryThenBlocks pins two things at
// the daemon assembly: Config.Supervision reaches the supervisor (a
// driver that sits between steps past the injected stall window — here a
// fake engine that never announces a dispatch — draws a driver_stalled
// recovery through the production path), and the recovery engine's
// consecutive-failures ledger still bounds a driver that keeps
// stalling: after the bound the WI is parked blocked, never
// relaunched forever (the #97 crash-loop lesson, now fed by the stall
// verdict too).
func TestSupervisionStallWindowDrivesRecoveryThenBlocks(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.Supervision = SupervisionConfig{StallWindow: 80 * time.Millisecond}
	})
	td.submitClaim(t, "wi_stall")
	td.waitMonitored(t, "wi_stall")

	deadline := time.Now().Add(10 * time.Second)
	for td.spawner.callCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the stalled driver was never relaunched (spawns=%d)", td.spawner.callCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
	var sawStall bool
	for _, e := range td.obs.Events().SinceAfter("wi_stall", 0) {
		if e.Kind == observe.EvNeedsRecovery && strings.Contains(string(e.Body), protocol.RecoverDriverStalled) {
			sawStall = true
		}
	}
	if !sawStall {
		t.Fatalf("no driver_stalled recovery event: %v", td.obs.Events().SinceAfter("wi_stall", 0))
	}

	// It keeps stalling: the consecutive-failures ledger parks the WI
	// blocked.
	deadline = time.Now().Add(15 * time.Second)
	for td.hub.endCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("a driver that keeps stalling was never parked blocked (spawns=%d)", td.spawner.callCount())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st := td.hub.lastEnd().Status; st != "blocked" {
		t.Fatalf("end status = %q, want blocked (the driver-crash bound)", st)
	}
}
