package observe

// w51_slow_thresholds_live_test.go (polyforge#51): the slow-model latency
// bounds are live — a dispatch in flight is judged by the bounds the
// LiveLimits closure reads at the tick, not the ones its step_started
// announced, so a reload of slow_model_median_latency reaches it without a
// restart.

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
			return supervise.Defaults{StepTimeout: time.Hour, SlowModelMedianLatency: time.Duration(median.Load())}
		}
	})
	// The dispatch announces slow bounds far above anything this test
	// produces; the live closure starts at the same far value.
	median.Store(int64(time.Hour))
	tr := startDispatch(m, 1, "fix", supervise.Thresholds{
		Silence: time.Hour, Tool: time.Hour,
		StepTimeout:            500 * time.Millisecond,
		SlowModelMedianLatency: time.Hour,
		SlowModelP90Latency:    time.Hour,
	})
	// five completed calls, individually ~70ms — slow once the operator
	// lowers the live median bound to 10ms. Each call is closed by a
	// tool's start (never an error turn: rule 1 outranks rule 4).
	for i := 0; i < 5; i++ {
		tr.WaitingModel()
		time.Sleep(70 * time.Millisecond)
		id := "c" + string(rune('0'+i))
		tr.ToolStart(id, "bash", "")
		tr.ToolEnd(id, "bash")
	}
	median.Store(int64(10 * time.Millisecond))
	// run the clock past T (the calls already put the dispatch past half
	// of it); the tick judges with the LIVE bound -> slow model
	time.Sleep(200 * time.Millisecond)
	v := nextVerdict(t, m, "the live slow threshold")
	dead, ok := v.(supervise.StepDead)
	if !ok || dead.Cause != supervise.CauseStepTimeoutSlowModel {
		t.Fatalf("verdict %+v, want %s — the live median bound must reach the in-flight dispatch",
			v, supervise.CauseStepTimeoutSlowModel)
	}
}
