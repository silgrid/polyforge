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

// pf101SeenSpec runs one task through the manager with a recording
// runner and answers the RunSpec its harness read (the prompt, the
// working directory the prohibited-actions list is scoped to, and the
// scratch directories #121's closing section names).
func pf101SeenSpec(t *testing.T, prompt string) RunSpec {
	t.Helper()
	var mu sync.Mutex
	var got []RunSpec
	m, _ := newTestManager(t, fakeRunner{func(_ context.Context, s RunSpec) (RunResult, error) {
		mu.Lock()
		got = append(got, s)
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
	spec := pf101SeenSpec(t, prompt)
	pf101AssertRulePosition(t, "ordinary task", spec.Prompt)
	// the task's own prompt unchanged under `# Task`, closed by #121's
	// `# Scratch` section (the merged shape — verification r1's finding:
	// the bare-prompt suffix predated it)
	if !strings.HasSuffix(spec.Prompt, "\n# Task\n\n"+prompt+w121ScratchSection(spec.ScratchTaskDir, spec.ScratchRawDir)) {
		t.Errorf("the task's own prompt must still follow under `# Task` unchanged, closed by the scratch section:\n%s", spec.Prompt)
	}
}

// TestPF101MessageAnalysisKeepsJSONContract: a message-analysis task's
// input carries the rule once, in position, and its own JSON contract
// stays intact after it.
func TestPF101MessageAnalysisKeepsJSONContract(t *testing.T) {
	const prompt = "Analyze message msg_8\n\nYou are analyzing one user-to-user message for its recipient.\n\nAnswer with exactly one JSON object and nothing else:\n{\"type\": \"...\"}\n"
	p := pf101SeenSpec(t, prompt).Prompt
	pf101AssertRulePosition(t, "message analysis", p)
	if !strings.Contains(p, "Answer with exactly one JSON object and nothing else") {
		t.Errorf("the message-analysis JSON contract must stay in the task body:\n%s", p)
	}
}

// TestPF101ProhibitedListUntouched: the rule rides beside the list, not
// into it — the list is still exactly harness.TaskProhibitedActions,
// still ≤1024 bytes.
func TestPF101ProhibitedListUntouched(t *testing.T) {
	spec := pf101SeenSpec(t, "the body")
	p, dir := spec.Prompt, spec.WorkDir
	list := harness.TaskProhibitedActions(dir)
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
