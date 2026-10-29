package daemon

// waitcall_status_test.go: the current step's wait calls
// on GET /status's job summary and on the watch progress block.

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func TestStatusShowsTheStepsWaitCalls(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_waitst")
	td.waitMonitored(t, "wi_waitst")
	link := td.sup.Link("wi_waitst")
	link.Fact(supervise.StepStarted{
		Step: "verification", SpawnN: 1, Model: "m", Harness: "claude",
		StartedAt: time.Now(), Thresholds: supervise.Thresholds{Silence: time.Hour, Tool: time.Hour, StepTimeout: time.Hour},
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := td.statusOf(t)
		if j := statusJobOf(&st, "wi_waitst"); j != nil && j.StepAgeSec != nil {
			if j.WaitCalls != nil || j.WaitSeconds != nil {
				t.Fatalf("no wait call yet, the fields must be absent: %+v", j)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the dispatch never surfaced on /status: %+v", st.Jobs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	tr := link.Tracker(1)
	tr.ToolStart("c1", "Bash", `{"command":"sleep 90 && gh run view 42"}`)
	tr.ToolStart("c2", "Bash", `{"command":"for i in 1 2 3; do make check && break; sleep 20; done"}`)
	st := td.statusOf(t)
	j := statusJobOf(&st, "wi_waitst")
	if j == nil || j.WaitCalls == nil || *j.WaitCalls != 1 || j.WaitSeconds == nil || *j.WaitSeconds != 90 {
		t.Fatalf("status job %+v: want wait_calls 1, wait_seconds 90", j)
	}
}

func TestWatchProgressProjectsWaitCalls(t *testing.T) {
	p := watchProgress(observe.LiveProgress{InDispatch: true, Step: "s", WaitCalls: 3, WaitSeconds: 240})
	if p == nil || p.WaitCalls == nil || *p.WaitCalls != 3 || p.WaitSeconds == nil || *p.WaitSeconds != 240 {
		t.Fatalf("a step whose only progress is its wait calls still shows them: %+v", p)
	}
	p = watchProgress(observe.LiveProgress{InDispatch: true, ToolCalls: 2})
	if p == nil || p.WaitCalls != nil || p.WaitSeconds != nil {
		t.Fatalf("no wait call: the fields are absent: %+v", p)
	}
}
