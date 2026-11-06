package task

// w440_task_prohibited_actions_test.go: every task-lane
// run's harness reads the prohibited-actions list — the very list every
// step carries (harness.TaskProhibitedActions: one list, reused, never
// copied; only its exception differs, naming the task alone, never "this
// prompt"), scoped to the task's working directory — ahead of the task's
// own prompt; the task's record keeps the prompt as given.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/harness"
)

func TestW440TaskRunCarriesTheProhibitedActions(t *testing.T) {
	var mu sync.Mutex
	var seen []RunSpec
	m, _ := newTestManager(t, fakeRunner{func(_ context.Context, s RunSpec) (RunResult, error) {
		mu.Lock()
		seen = append(seen, s)
		n := len(seen)
		mu.Unlock()
		if n == 1 {
			return RunResult{Tokens: 1}, errors.New("candidate down") // the first candidate fails: rotation
		}
		return RunResult{Report: "done", Tokens: 1}, nil
	}}, []string{"pi:a/b", "pi:c/d"})
	const prompt = "Summarize the open questions.\nKeep it short."
	tk, err := m.Spawn(Spec{Project: "p", Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	done := waitState(t, m, tk.ID, StateCompleted)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("runs = %d, want 2 (one per candidate)", len(seen))
	}
	for i, s := range seen {
		list := harness.TaskProhibitedActions(s.WorkDir)
		if !strings.HasPrefix(s.Prompt, list) || strings.Count(s.Prompt, "# Prohibited actions") != 1 {
			t.Errorf("run %d: the harness prompt must open with the prohibited-actions list scoped to %s, once:\n%s", i, s.WorkDir, s.Prompt)
		}
		if !strings.HasSuffix(s.Prompt, "\n# Task\n\n"+prompt) {
			t.Errorf("run %d: the task's own prompt follows the list under `# Task`:\n%s", i, s.Prompt)
		}
	}
	if done.Prompt != prompt || PromptSummary(done.Prompt) != "Summarize the open questions." {
		t.Errorf("the record keeps the prompt as given: %q", done.Prompt)
	}
}

func TestW440TaskListIsTheStepListWithTheTaskAsItsException(t *testing.T) {
	const dir = "/ws/demo"
	const stepOpener = "Unless your step definition explicitly requires it, never:"
	const taskOpener = "Unless your task explicitly requires it, never:"
	task, step := harness.TaskProhibitedActions(dir), harness.StepProhibitedActions(dir)
	if strings.Count(task, taskOpener) != 1 || strings.Contains(task, "this prompt") {
		t.Errorf("the task list's one exception is the task itself (%q), never \"this prompt\":\n%s", taskOpener, task)
	}
	// one list: the two sections differ in the opener alone
	if strings.Replace(task, taskOpener, stepOpener, 1) != step {
		t.Errorf("the task list must be the step list with the task as its exception:\ntask:\n%s\nstep:\n%s", task, step)
	}
	if len(task) > 1024 {
		t.Errorf("the task list is %d bytes; it stays short (1024)", len(task))
	}
}
