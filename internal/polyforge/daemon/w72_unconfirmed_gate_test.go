package daemon

// w72_unconfirmed_gate_test.go (polyforge#72, u10 — AC6's daemon half):
// the auto-pull wave skips a ticket whose analysis carries the
// unconfirmed marker — the machine never claims a report the owner has
// not confirmed — and claims it once the confirm command stripped the
// marker. A manual `pf submit` is untouched (the CLI half pins that).

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

func TestW72AutoPullSkipsUnconfirmedAnalysis(t *testing.T) {
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "develop", "steps:\n  - { id: build }\n")
	td := newTestDaemon(t, func(c *Config) {
		c.WorkflowsDir = wf
		c.MaxConcurrent = 8
		c.DrainInterval = time.Hour
		c.DispatchTick = time.Hour
	})
	bundle := "develop"
	item := func(id, content string) api.WorkItem {
		return api.WorkItem{ID: id, Slug: id, ProjectName: "proj", Status: api.StatusQueued,
			Priority: api.PriorityNormal, BundleName: &bundle, Content: strp(content)}
	}
	td.hub.mu.Lock()
	td.hub.queues = append(td.hub.queues,
		item("wi_w72_unconfirmed", "## Acceptance\n\n- AC1 — carried by build\n\n## 分析\n\n"+
			workflow.AutoAnalysisMarker("task_0123456789abcdef")+"\n\n### 1. 改动范围\n\n- a\n"),
		item("wi_w72_confirmed", "## Acceptance\n\n- AC1 — carried by build\n\n## 分析\n\n### 1. 改动范围\n\n- a\n"),
	)
	td.hub.mu.Unlock()
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatal(err)
	}
	td.fetchAutoWave()
	if td.q.IsPending("wi_w72_unconfirmed") {
		t.Error("the wave must skip a ticket whose analysis is unconfirmed (the owner has not confirmed the machine's report)")
	}
	if !td.q.IsPending("wi_w72_confirmed") {
		t.Error("the wave must claim the ticket whose analysis was confirmed")
	}
}
