package task

// w72_analysis_prompt_test.go (polyforge#72, u4 — AC10): the manager
// consults Config.PromptFor before EVERY candidate dispatch — the
// daemon re-reads the ticket and rebuilds the prompt so the next
// candidate continues from what is already in `## 分析` instead of
// starting over (ruling 8). Spec.AnalysisOf is the ticket binding the
// narrow write path checks. Expected values are hand-built prompt
// texts, never copied from the implementation.

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// promptRecorder records the prompt of every candidate run; the first
// candidate is judged stuck so the manager walks to the next one.
type promptRecorder struct {
	prompts []string
	specs   []RunSpec
	calls   int
}

func (p *promptRecorder) Run(_ context.Context, spec RunSpec, _ func(int64)) (RunResult, error) {
	p.calls++
	p.prompts = append(p.prompts, spec.Prompt)
	p.specs = append(p.specs, spec)
	if p.calls == 1 {
		return RunResult{Tokens: 1}, &StuckError{Cause: "harness_silent", Evidence: "the first candidate went silent"}
	}
	return RunResult{Report: "continued", Tokens: 1}, nil
}

func TestW72PromptRebuiltPerCandidate(t *testing.T) {
	r := &promptRecorder{}
	// The hook answers an evolving text: read #2 sees what candidate 1
	// already wrote — exactly the re-read AC10 names.
	reads := 0
	promptFor := func(tk Task) string {
		reads++
		if tk.Spec.AnalysisOf == "" {
			t.Errorf("the hook must see the analysis task's binding, got an empty AnalysisOf")
		}
		return fmt.Sprintf("# 分析任务（第 %d 次重读）\n已写进 ## 分析 的部分：草稿 v%d", reads, reads)
	}
	m := NewManager(Config{
		Store:  NewStore(t.TempDir()),
		Runner: r,
		Resolve: func(string) ([]string, string, error) {
			return []string{"pi:a/one", "pi:b/two"}, "steps.p.task.models", nil
		},
		WorkDir:   func(string) (string, error) { return t.TempDir(), nil },
		PromptFor: promptFor,
	})
	t.Cleanup(m.Close)
	tk, err := m.Spawn(Spec{Project: "p", Prompt: "the filed prompt", AnalysisOf: "wi_72analysis"})
	if err != nil {
		t.Fatal(err)
	}
	done := waitState(t, m, tk.ID, StateCompleted)
	if done.Spec.AnalysisOf != "wi_72analysis" {
		t.Fatalf("the binding must ride the record: %+v", done.Spec)
	}
	if r.calls != 2 || len(r.prompts) != 2 {
		t.Fatalf("two candidates must run: calls=%d prompts=%d", r.calls, len(r.prompts))
	}
	if reads != 2 {
		t.Fatalf("the hook must be consulted before EVERY candidate, got %d calls", reads)
	}
	if !strings.Contains(r.prompts[0], "草稿 v1") || !strings.Contains(r.prompts[1], "草稿 v2") {
		t.Fatalf("each candidate must run with the prompt its own re-read built:\n%q\n%q", r.prompts[0], r.prompts[1])
	}
	if strings.Contains(r.prompts[0], "the filed prompt") || strings.Contains(r.prompts[1], "the filed prompt") {
		t.Fatalf("a PromptFor task's stdin is the hook's answer, not the spec's prompt:\n%q", r.prompts[0])
	}
}

// Without the hook nothing changes: the candidate runs the spec's own
// prompt behind the prohibited-actions section (dispatchedPrompt).
func TestW72PromptWithoutHookUnchanged(t *testing.T) {
	var got []string
	m := NewManager(Config{
		Store: NewStore(t.TempDir()),
		Runner: runnerFunc(func(_ context.Context, spec RunSpec, _ func(int64)) (RunResult, error) {
			got = append(got, spec.Prompt)
			return RunResult{Report: "done", Tokens: 1}, nil
		}),
		Resolve: func(string) ([]string, string, error) { return []string{"pi:a/one"}, "steps.p.task.models", nil },
		WorkDir: func(string) (string, error) { return t.TempDir(), nil },
	})
	t.Cleanup(m.Close)
	tk, err := m.Spawn(Spec{Project: "p", Prompt: "the filed prompt"})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, tk.ID, StateCompleted)
	if len(got) != 1 {
		t.Fatalf("prompts = %d, want 1", len(got))
	}
	if !strings.Contains(got[0], "# Prohibited actions") || !strings.Contains(got[0], "# Task") || !strings.Contains(got[0], "the filed prompt") {
		t.Fatalf("the candidate must run the ordinary dispatchedPrompt:\n%q", got[0])
	}
}

type runnerFunc func(ctx context.Context, spec RunSpec, progress func(int64)) (RunResult, error)

func (f runnerFunc) Run(ctx context.Context, spec RunSpec, p func(int64)) (RunResult, error) {
	return f(ctx, spec, p)
}
