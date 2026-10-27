package daemon

// heartbeat_test.go: the M3 daemon liveness loop — the live-job
// projection, the backoff kernel, tick/stop behavior over a real Run,
// the disabled mode, and survival across a failing aihub (log-only,
// never fatal).

import (
	"errors"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/version"
)

func TestLiveWIIDs(t *testing.T) {
	jobs := []runtime.JobInfo{
		{WorkItem: "wi-sealing", State: runtime.StateSealing},
		{WorkItem: "wi-paused", State: runtime.StatePaused},
		{WorkItem: "wi-running", State: runtime.StateRunning},
		{WorkItem: "wi-done", State: runtime.StateDone},
		{WorkItem: "wi-recovering", State: runtime.StateRecovering},
	}
	got := liveWIIDs(jobs)
	want := []string{"wi-recovering", "wi-running", "wi-sealing"} // sorted, live only
	if len(got) != len(want) {
		t.Fatalf("liveWIIDs: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("liveWIIDs: got %v want %v", got, want)
		}
	}
	// Paused jobs are retained records with a closed session — not held.
	if got := liveWIIDs([]runtime.JobInfo{{WorkItem: "wi-p", State: runtime.StatePaused}}); len(got) != 0 {
		t.Fatalf("paused must not be live: %v", got)
	}
	if got := liveWIIDs(nil); len(got) != 0 {
		t.Fatalf("empty runtime: %v", got)
	}
}

func TestNextHeartbeatDelay(t *testing.T) {
	base := 60 * time.Second
	cases := []struct {
		streak int
		want   time.Duration
	}{
		{1, 120 * time.Second},
		{2, 240 * time.Second},
		{3, 480 * time.Second},
		{4, 960 * time.Second},
		{5, 960 * time.Second},  // capped at 16× base
		{50, 960 * time.Second}, // deep failure storm: still bounded
	}
	for _, c := range cases {
		if got := nextHeartbeatDelay(base, c.streak); got != c.want {
			t.Fatalf("nextHeartbeatDelay(base, %d) = %s, want %s", c.streak, got, c.want)
		}
	}
}

func heartbeatCount(h *fakeHub) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.heartbeats)
}

func heartbeatAttempts(h *fakeHub) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.heartbeatAttempts
}

// TestHeartbeatLoopTicksPayloadAndStops: a real Run with a fast cadence
// POSTs the expected payload repeatedly, and the loop is dead after
// Shutdown (no more attempts — the liveness face follows the daemon).
func TestHeartbeatLoopTicksPayloadAndStops(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.HeartbeatEvery = 15 * time.Millisecond
	})
	waitFor(t, "first heartbeat ticks", 3*time.Second, func() bool { return heartbeatCount(td.hub) >= 3 })
	payloads := td.hub.heartbeatsSnapshot()
	p := payloads[0]
	if p.MachineID != "m-test" || p.Mode != "manual" || p.RuntimeVersion != version.Version {
		t.Fatalf("payload identity: %+v", p)
	}
	if p.LiveWIIDs == nil {
		t.Fatalf("idle daemon must carry an empty live list, not nil: %+v", p)
	}

	if err := td.Daemon.Shutdown(t.Context(), DirectShutdownCause()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	// Grace for an in-flight tick, then the count must be frozen.
	time.Sleep(80 * time.Millisecond)
	frozen := heartbeatCount(td.hub)
	time.Sleep(60 * time.Millisecond)
	if got := heartbeatCount(td.hub); got != frozen {
		t.Fatalf("heartbeat attempts after shutdown: %d → %d", frozen, got)
	}
}

// TestHeartbeatDisabled: HeartbeatEvery <= 0 must produce zero POSTs —
// the flag's 0=disabled contract.
func TestHeartbeatDisabled(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.HeartbeatEvery = 0
	})
	// Give a hypothetical 60s-default ticker no chance to mask the bug:
	// the loop must not even arm. A short Run window suffices.
	time.Sleep(150 * time.Millisecond)
	if got := heartbeatAttempts(td.hub); got != 0 {
		t.Fatalf("disabled heartbeat still attempted %d POSTs", got)
	}
	if err := td.Daemon.Shutdown(t.Context(), DirectShutdownCause()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// TestHeartbeatFailureBackoffAndRecovery: a failing aihub never kills the
// loop (log-only), attempts keep coming (backed off — the arithmetic is
// pinned by TestNextHeartbeatDelay), and the first success resets the
// cadence and lands in the success ledger.
func TestHeartbeatFailureBackoffAndRecovery(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.HeartbeatEvery = 15 * time.Millisecond
	})
	base := heartbeatCount(td.hub) // the immediate first heartbeat already succeeded
	td.hub.setHeartbeatErr(errors.New("aihub down"))
	waitFor(t, "failed heartbeat attempts accumulate", 3*time.Second, func() bool { return heartbeatAttempts(td.hub) >= 3 })
	if got := heartbeatCount(td.hub); got != base {
		t.Fatalf("failures must not land in the success ledger: %d (base %d)", got, base)
	}
	// Recover: the next tick succeeds and the ledger moves.
	td.hub.setHeartbeatErr(nil)
	waitFor(t, "heartbeat recovers after the hub heals", 3*time.Second, func() bool { return heartbeatCount(td.hub) >= 1 })
	if err := td.Daemon.Shutdown(t.Context(), DirectShutdownCause()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// review nit on B-25: the clamp is exact for a cap that is not a power
// of two.
func TestCappedBackoffNonPowerOfTwoCap(t *testing.T) {
	base := time.Second
	for streak, want := range []time.Duration{1, 2, 4, 8, 10, 10, 10} {
		if got := cappedBackoff(base, streak, 10); got != want*time.Second {
			t.Fatalf("cappedBackoff(1s, %d, 10) = %s, want %s", streak, got, want*time.Second)
		}
	}
}
