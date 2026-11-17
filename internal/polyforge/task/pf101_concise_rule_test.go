package task

// pf101_concise_rule_test.go (polyforge#101, AC4): every task-lane run's
// harness input carries the concise output-discipline rule
// (harness.ConciseRule — the one source, the same bytes the step prompt
// and the harness context files carry) exactly once, between the
// prohibited actions and the task's own prompt under `# Task` — for an
// ordinary task and for a message-analysis task alike (whose own JSON
// contract the rule's third bullet defers to). The prohibited-actions
// list itself stays untouched (its own pin: w440, ≤1024 bytes).

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/harness"
)

// pf101SeenPrompt runs one task through the manager with a recording
// runner and answers the prompt its harness read.
func pf101SeenPrompt(t *testing.T, prompt string) string {
	t.Helper()
	var mu sync.Mutex
	var got []string
	m, _ := newTestManager(t, fakeRunner{func(_ context.Context, s RunSpec) (RunResult, error) {
		mu.Lock()
		got = append(got, s.Prompt)
		mu.Unlock()
		return RunResult{Report: "done", Tokens: 1}, nil
	}}, []string{"pi:a/b"})
	tk, err := m.Spawn(Spec{Project: "p", Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, tk.ID, StateCompleted)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("runs = %d, want 1", len(got))
	}
	return got[0]
}

// pf101AssertRulePosition asserts the rule rides the input exactly
// once, between the prohibited actions and the task body.
func pf101AssertRulePosition(t *testing.T, face, p string) {
	t.Helper()
	rule := harness.ConciseRule()
	if n := strings.Count(p, rule); n != 1 {
		t.Errorf("%s: the harness input carries the concise rule %d times, want 1:\n%s", face, n, p)
	}
	prohibited := strings.Index(p, "# Prohibited actions")
	body := strings.Index(p, "\n# Task\n\n")
	at := strings.Index(p, rule)
	if prohibited < 0 || body < 0 || at < 0 {
		t.Fatalf("%s: the input lacks a part (prohibited=%d task=%d rule=%d)", face, prohibited, body, at)
	}
	if at < prohibited || at > body {
		t.Errorf("%s: the rule belongs after the prohibited actions and before the task body, at %d (prohibited %d, task %d)", face, at, prohibited, body)
	}
}

// TestPF101TaskPromptCarriesConciseRuleOnce: an ordinary task's harness
// input carries the rule exactly once, in position.
func TestPF101TaskPromptCarriesConciseRuleOnce(t *testing.T) {
	const prompt = "Summarize the open questions.\nKeep it short."
	p := pf101SeenPrompt(t, prompt)
	pf101AssertRulePosition(t, "ordinary task", p)
	if !strings.HasSuffix(p, "\n# Task\n\n"+prompt) {
		t.Errorf("the task's own prompt must still follow under `# Task` unchanged:\n%s", p)
	}
}

// TestPF101MessageAnalysisKeepsJSONContract: a message-analysis task's
// input carries the rule once, in position, and its own JSON contract
// stays intact after it.
func TestPF101MessageAnalysisKeepsJSONContract(t *testing.T) {
	const prompt = "Analyze message msg_8\n\nYou are analyzing one user-to-user message for its recipient.\n\nAnswer with exactly one JSON object and nothing else:\n{\"type\": \"...\"}\n"
	p := pf101SeenPrompt(t, prompt)
	pf101AssertRulePosition(t, "message analysis", p)
	if !strings.Contains(p, "Answer with exactly one JSON object and nothing else") {
		t.Errorf("the message-analysis JSON contract must stay in the task body:\n%s", p)
	}
}

// TestPF101ProhibitedListUntouched: the rule rides beside the list, not
// into it — the list is still exactly harness.TaskProhibitedActions,
// still ≤1024 bytes.
func TestPF101ProhibitedListUntouched(t *testing.T) {
	const dir = "/ws/demo"
	list := harness.TaskProhibitedActions(dir)
	p := pf101SeenPrompt(t, "the body")
	if !strings.HasPrefix(p, list+"\n") {
		t.Errorf("the input must still open with the prohibited-actions list:\n%s", p[:len(list)+32])
	}
	if n := strings.Count(p, "# Prohibited actions"); n != 1 {
		t.Errorf("the prohibited-actions heading occurs %d times, want 1", n)
	}
	if len(list) > 1024 {
		t.Errorf("the prohibited-actions list is %d bytes; it rides every dispatch and stays short (1024)", len(list))
	}
}
