package daemon

// pf101_concise_rule_test.go (polyforge#101, AC4): the ticket-analysis
// task's harness input — the daemon's own analysisPrompt, ahead of the
// task manager's wrapping — carries the concise output-discipline rule
// (harness.ConciseRule, the one source) exactly once, between the
// prohibited actions and the analysis template; the template's own
// report format and the message-analysis JSON contract are the rule's
// third bullet's "a task's own required format always wins" cases and
// stay untouched.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/harness"
)

// TestPF101AnalysisPromptCarriesConciseRuleOnce: the analysis prompt
// carries the rule exactly once, in position.
func TestPF101AnalysisPromptCarriesConciseRuleOnce(t *testing.T) {
	td := newTestDaemon(t, nil)
	content := "## 背景\n\nthe ticket body\n"
	item := &api.WorkItem{ID: "wi_pf101", ProjectName: "proj", BundleName: strPtr("develop"), Content: &content}
	p, err := td.analysisPrompt(item)
	if err != nil {
		t.Fatalf("analysisPrompt: %v", err)
	}
	rule := harness.ConciseRule()
	if n := strings.Count(p, rule); n != 1 {
		t.Errorf("the analysis prompt carries the concise rule %d times, want 1:\n%s", n, p)
	}
	prohibited := strings.Index(p, "# Prohibited actions")
	tpl := strings.Index(p, "提交前分析任务") // the template's own heading
	at := strings.Index(p, rule)
	if prohibited < 0 || tpl < 0 || at < 0 {
		t.Fatalf("the prompt lacks a part (prohibited=%d template=%d rule=%d)", prohibited, tpl, at)
	}
	if at < prohibited || at > tpl {
		t.Errorf("the rule belongs after the prohibited actions and before the analysis template, at %d (prohibited %d, template %d)", at, prohibited, tpl)
	}
	if !strings.HasPrefix(p, harness.TaskProhibitedActions(pf101TaskWorkDir(t, td))+"\n") {
		t.Errorf("the analysis prompt must still open with the prohibited-actions list:\n%s", p[:200])
	}
}

// pf101TaskWorkDir answers the project workspace the daemon's task lane
// runs in (the list's scope).
func pf101TaskWorkDir(t *testing.T, td *testDaemon) string {
	t.Helper()
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

// strPtr answers a pointer to s.
func strPtr(s string) *string { return &s }
