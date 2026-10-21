package daemon

// revalidate_flag_test.go: pins the -revalidate-every seam. Main()'s
// flag parse is not unit-testable (it runs the daemon), so the flag
// test follows the established seam-test shape — the same
// supervision-config injection pattern: inject
// Config.Supervision through newTestDaemon and assert the assembled
// observe lane honors it. Two pins:
//
//   - a small injected cadence drives REPEATED periodic session-verify
//     calls at the hub's M4 seam (0 would keep the 5m production
//     default and this test would time out instead of accumulate);
//
//   - a negative value disables revalidation entirely: a live,
//     monitored job never produces a single verify call.

import (
	"testing"
	"time"
)

func TestRevalidateEveryInjectionDrivesPeriodicSessionVerify(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		// The seam: the M4 cadence the assembly passes through.
		c.Supervision.SessionRevalidateEvery = 40 * time.Millisecond
	})
	td.submitClaim(t, "wi_rv")
	td.waitMonitored(t, "wi_rv")

	// The injected cadence rides the monitor tick: repeated periodic
	// session-verify calls reach the hub's M4 seam.
	deadline := time.Now().Add(5 * time.Second)
	for td.hub.verifyCount() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("periodic session verifies never accumulated on the injected cadence (count=%d)", td.hub.verifyCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Periodic, not one-shot: the count keeps growing on the cadence.
	base := td.hub.verifyCount()
	deadline = time.Now().Add(5 * time.Second)
	for td.hub.verifyCount() <= base {
		if time.Now().After(deadline) {
			t.Fatal("session verifies stopped after the first round; the cadence did not ride the tick")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRevalidateEveryNegativeDisablesSessionVerify(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.Supervision.SessionRevalidateEvery = -1
	})
	td.submitClaim(t, "wi_rvn")
	td.waitMonitored(t, "wi_rvn")

	// Negative = disabled: the job is live and monitored, yet no
	// periodic session verify ever reaches the hub.
	time.Sleep(300 * time.Millisecond)
	if n := td.hub.verifyCount(); n != 0 {
		t.Fatalf("session verifies = %d with revalidation disabled, want 0", n)
	}
}
