package observe

// waitcall_test.go: the status and watch faces read the current step's
// wait-call total summed over every dispatch of the step
// this monitor watched, and never mixed with another step's.

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func TestWaitCallsSumPerStepAcrossDispatches(t *testing.T) {
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	th := supervise.Thresholds{Silence: time.Hour}
	waits := func(want int, wantSecs int64, what string) {
		t.Helper()
		soon(t, time.Second, what, func() bool {
			a, p := m.activity(), m.liveProgress()
			return a.WaitCalls == want && a.WaitSeconds == wantSecs && p.WaitCalls == want && p.WaitSeconds == wantSecs
		})
	}

	tr := startDispatch(m, 1, "verification", th)
	soon(t, time.Second, "dispatch 1 adopted", func() bool { return m.activity().SpawnN == 1 })
	tr.ToolStart("c1", "bash", `{"command":"sleep 120 && gh pr checks 7"}`)
	tr.ToolStart("c2", "bash", `{"command":"until make check; do sleep 30; done"}`) // legal: not counted
	waits(1, 120, "dispatch 1 counts its leading sleep")

	// the step's retry: the total carries over the dispatch boundary
	m.link.Fact(supervise.StepEnded{Step: "verification", SpawnN: 1, Outcome: supervise.OutcomeExitedWithoutResult})
	tr2 := startDispatch(m, 2, "verification", th)
	soon(t, time.Second, "dispatch 2 adopted", func() bool { return m.activity().SpawnN == 2 })
	waits(1, 120, "the retry starts from the step's total")
	tr2.ToolStart("c3", "Bash", `{"command":"sleep 1m"}`)
	waits(2, 180, "the retry adds its own")

	// a dispatch replaced without step_ended still folds into its step
	tr3 := startDispatch(m, 3, "verification", th)
	soon(t, time.Second, "dispatch 3 adopted", func() bool { return m.activity().SpawnN == 3 })
	waits(2, 180, "a replaced dispatch's waits are kept")
	tr3.ToolStart("c4", "bash", `{"command":"sleep 10"}`)
	waits(3, 190, "dispatch 3 adds its own")

	// another step starts from zero
	m.link.Fact(supervise.StepEnded{Step: "verification", SpawnN: 3, Outcome: supervise.OutcomeResult})
	startDispatch(m, 4, "deliver", th)
	soon(t, time.Second, "dispatch 4 adopted", func() bool { return m.activity().SpawnN == 4 })
	waits(0, 0, "another step has its own total")
}

// A driver relaunch (RTRecovered) drops the in-flight dispatch without a
// step_ended; its wait calls still stay in the step's total, so the
// relaunched driver's dispatch of the same step reads them.
func TestWaitCallsSurviveADriverRelaunch(t *testing.T) {
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	th := supervise.Thresholds{Silence: time.Hour}
	tr := startDispatch(m, 1, "verification", th)
	soon(t, time.Second, "dispatch 1 adopted", func() bool { return m.activity().SpawnN == 1 })
	tr.ToolStart("c1", "bash", `{"command":"sleep 120 2>/dev/null && gh pr checks 7"}`)
	soon(t, time.Second, "dispatch 1 counts its wait call", func() bool { return m.activity().WaitCalls == 1 })

	h2 := newFakeHandle("att_2")
	m.rtQ.Push(Attach{Msg: protocol.RTMsg{Kind: protocol.RTRecovered, WorkItem: "wi_test", SpawnN: 2}, Handle: h2})
	soon(t, time.Second, "relaunch adopted, dispatch dropped", func() bool {
		return m.currentSpawn() == 2 && !m.activity().InDispatch
	})
	tr2 := startDispatch(m, 2, "verification", th)
	soon(t, time.Second, "the relaunched driver's dispatch keeps the step's total", func() bool {
		a, p := m.activity(), m.liveProgress()
		return a.SpawnN == 2 && a.WaitCalls == 1 && a.WaitSeconds == 120 && p.WaitCalls == 1 && p.WaitSeconds == 120
	})
	tr2.ToolStart("c2", "bash", `{"command":"sleep 60"}`)
	soon(t, time.Second, "and adds its own", func() bool {
		a := m.activity()
		return a.WaitCalls == 2 && a.WaitSeconds == 180
	})
}
