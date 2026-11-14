package daemon

// pf27_autowave_analysis_test.go (polyforge#27, AC3): the auto-pull
// wave claims only an execution ticket that also carries a pre-submit
// analysis — a `## 分析` / `## Analysis` section with content, 「免分析」
// with its date included (workflow.HasAnalysis) — on a lane that
// requires acceptance; one with acceptance but no analysis stays queued
// for interaction, exactly like one without acceptance. A lane
// declaring `acceptance: none` is pulled without either.

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

func TestPF27AutoWaveSkipsATicketWithoutAnalysis(t *testing.T) {
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "plain", "steps:\n  - { id: work }\n")
	workflowtest.WriteLane(t, wf, "nolist", "acceptance: none\nsteps:\n  - { id: work }\n")
	td := newTestDaemon(t, func(c *Config) {
		c.WorkflowsDir = wf
		c.MaxConcurrent = 8 // both waves' claims fit the free-slot budget
		c.DrainInterval = time.Hour
		c.DispatchTick = time.Hour
	})
	bundle := "plain"
	item := func(id, content string) api.WorkItem {
		return api.WorkItem{ID: id, Slug: id, ProjectName: "proj", Status: api.StatusQueued,
			Priority: api.PriorityNormal, BundleName: &bundle, Content: strp(content)}
	}
	td.hub.mu.Lock()
	td.hub.queues = append(td.hub.queues,
		item("wi_pf27_analyzed", w461AcceptanceContent),
		item("wi_pf27_waived", "## Acceptance\n\n- AC1 x — carried by build\n\n## 分析\n\n免分析（2026-11-14，owner 明确跳过）\n"),
		item("wi_pf27_none", "## Acceptance\n\n- AC1 x — carried by build\n"),
		item("wi_pf27_empty", "## Acceptance\n\n- AC1 x — carried by build\n\n## 分析\n"),
	)
	td.hub.mu.Unlock()
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}
	td.fetchAutoWave()
	for id, want := range map[string]bool{
		"wi_pf27_analyzed": true,
		"wi_pf27_waived":   true,
		"wi_pf27_none":     false,
		"wi_pf27_empty":    false,
	} {
		if got := td.q.IsPending(id); got != want {
			t.Errorf("%s claimed by the wave = %v, want %v (a ticket is claimed only when its content carries an analysis section besides its acceptance criterion)", id, got, want)
		}
	}

	// a lane declaring acceptance none is pulled without an analysis
	// section too: the analysis check applies on the acceptance lanes.
	nolist := "nolist"
	td.hub.mu.Lock()
	td.hub.queues = append(td.hub.queues, api.WorkItem{ID: "wi_pf27_nolist", Slug: "wi_pf27_nolist",
		ProjectName: "proj", Status: api.StatusQueued, Priority: api.PriorityNormal,
		BundleName: &nolist, Content: strp("## Acceptance\n\n- AC1 x — carried by build\n")})
	td.hub.mu.Unlock()
	td.fetchAutoWave()
	if !td.q.IsPending("wi_pf27_nolist") {
		t.Error("wi_pf27_nolist claimed by the wave = false, want true (a lane declaring acceptance none is pulled without an analysis section)")
	}
}

// TestPF27AutoWaveStillSkipsWithoutAcceptanceWhenAnalyzed pins review
// r1#1's discriminating case: the acceptance half of the wave's
// condition stays load-bearing now that the analysis half rides beside
// it — a ticket whose content carries an analysis section but lists no
// acceptance criterion is still skipped, and a control carrying both is
// claimed. Removing the acceptance half of the condition claims the
// analyzed ticket and turns this red.
func TestPF27AutoWaveStillSkipsWithoutAcceptanceWhenAnalyzed(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.MaxConcurrent = 8
		c.DrainInterval = time.Hour
		c.DispatchTick = time.Hour
	})
	bundle := "demo-bundle"
	item := func(id, content string) api.WorkItem {
		return api.WorkItem{ID: id, Slug: id, ProjectName: "proj", Status: api.StatusQueued,
			Priority: api.PriorityNormal, BundleName: &bundle, Content: strp(content)}
	}
	td.hub.mu.Lock()
	td.hub.queues = append(td.hub.queues,
		item("wi_pf27_accless", "## 分析\n\n- 改动范围：internal/x；风险：低\n"),
		item("wi_pf27_control", w461AcceptanceContent),
	)
	td.hub.mu.Unlock()
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}
	td.fetchAutoWave()
	if td.q.IsPending("wi_pf27_accless") {
		t.Error("wi_pf27_accless claimed by the wave = true, want false (a ticket with an analysis section but no acceptance criterion is still skipped — the acceptance half of the condition is load-bearing)")
	}
	if !td.q.IsPending("wi_pf27_control") {
		t.Error("wi_pf27_control claimed by the wave = false, want true (the control carrying both sections is claimed)")
	}
}
