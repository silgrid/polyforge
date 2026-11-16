package observe

// w51_slow_thresholds_live_test.go (polyforge#51): the slow-model latency
// bounds are live — a dispatch in flight is judged by the bounds the
// LiveLimits closure reads at the tick, not the ones its step_started
// announced, so a reload of slow_model_median_latency reaches it without a
// restart. The waiting share alone decides nothing (polyforge#25): a
// dispatch that spent the whole clock waiting still expires as a long task
// once its completed calls were individually fast.

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func TestW51LiveSlowThresholdsReachTheInFlightDispatch(t *testing.T) {
	h := newFakeHandle("att_1")
	var median atomic.Int64
	median.Store(int64(time.Hour))
	m, _ := newTestMonitor(t, h, func(c *monitorCfg) {
		c.LiveLimits = func() supervise.Defaults {
			return supervise.Defaults{StepTimeout: 500 * time.Millisecond, SlowModelMedianLatency: time.Duration(median.Load())}
		}
	})
	// The dispatch announced slow bounds of 10ms; the operator RAISES the
	// live median to an hour before the expiry, so the tick must judge
	// with the live bound and the expiry stays a long task.
	tr := startDispatch(m, 1, "fix", supervise.Thresholds{
		Silence: time.Hour, Tool: time.Hour,
		StepTimeout:            500 * time.Millisecond,
		SlowModelMedianLatency: 10 * time.Millisecond,
		SlowModelP90Latency:    10 * time.Millisecond,
	})
	// five completed calls of ~30ms (slow under the announced 10ms bound,
	// fast under the raised one), closed by a tool's start — never an
	// error turn (rule 1 outranks rule 4) — and then one call in progress
	// that carries the rest of the clock.
	for i := 0; i < 5; i++ {
		tr.WaitingModel()
		time.Sleep(30 * time.Millisecond)
		id := "c" + string(rune('0'+i))
		tr.ToolStart(id, "bash", "")
		tr.ToolEnd(id, "bash")
	}
	tr.WaitingModel()
	time.Sleep(400 * time.Millisecond)
	v := nextVerdict(t, m, "the live slow threshold")
	dead, ok := v.(supervise.StepDead)
	if !ok || dead.Cause != supervise.CauseStepTimeoutLongTask {
		t.Fatalf("verdict %+v, want %s — the RAISED live median bound must reach the in-flight dispatch (the waiting share alone decides nothing)",
			v, supervise.CauseStepTimeoutLongTask)
	}
}
