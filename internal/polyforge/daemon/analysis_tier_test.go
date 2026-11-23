package daemon

// analysis_tier_test.go (polyforge#81 u4, AC4): the pre-submit analysis
// prompt carries the Host [step_tiers] catalog (each tier's ordered
// candidates) and the ticket's lane step table (id, model_tier,
// model_candidates), so the analysis can recommend one tier per step;
// an empty catalog is one fallback line and the analysis prompt still
// builds, and a lane this machine cannot read is named rather than
// invented. The expected text comes from AC4's inputs, not from this
// implementation's output.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

const analysisTierLane = `max_rewinds: 3
defaults:
  tiers:
    judgment: [pi:prov/lane-judge]
steps:
  - id: build
    model_candidates: [pi:prov/build-a]
  - id: review
    model_tier: judgment
    gate: true
    on_fail: { target: build }
  - id: merge
`

func TestAnalysisTierPrompt(t *testing.T) {
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "develop", analysisTierLane)
	td := newTestDaemon(t, func(c *Config) {
		c.WorkflowsDir = wf
		c.StartupFile.StepTiers = map[string][]string{
			"main":     {"pi:prov/main", "claude:main"},
			"judgment": {"pi:prov/judge"},
		}
	})
	content := "## 背景\n\nthe ticket body\n"
	item := &api.WorkItem{ID: "wi_tieran", ProjectName: "proj", BundleName: strPtr("develop"), Content: &content}
	p, err := td.analysisPrompt(item)
	if err != nil {
		t.Fatalf("analysisPrompt: %v", err)
	}
	for _, want := range []string{
		"步骤能力档目录与车道 step 表",
		"- judgment: pi:prov/judge",
		"- main: pi:prov/main, claude:main",
		"| build |  | pi:prov/build-a |",
		"| review | judgment |  |",
		"| merge |  |  |",
		"按每个 step 的判断/机械程度",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("the analysis prompt lacks %q:\n%s", want, p)
		}
	}

	// an empty catalog is one fallback line; the prompt still builds and
	// carries no invented tier.
	tdEmpty := newTestDaemon(t, func(c *Config) {
		c.WorkflowsDir = wf
	})
	p, err = tdEmpty.analysisPrompt(item)
	if err != nil {
		t.Fatalf("the empty-catalog prompt must still build (never block): %v", err)
	}
	if !strings.Contains(p, "Host 未配置 [step_tiers]（目录为空）") || strings.Contains(p, "- main:") {
		t.Fatalf("an empty catalog must answer one fallback line and no tiers:\n%s", p)
	}

	// a lane this machine cannot read is named, never invented.
	bad := &api.WorkItem{ID: "wi_tieran2", ProjectName: "proj", BundleName: strPtr("nosuch-lane"), Content: &content}
	p, err = td.analysisPrompt(bad)
	if err != nil {
		t.Fatalf("an unreadable lane must not block the prompt: %v", err)
	}
	if !strings.Contains(p, "本机不可读") || strings.Contains(p, "| build |") {
		t.Fatalf("an unreadable lane must be named without an invented step table:\n%s", p)
	}
}
