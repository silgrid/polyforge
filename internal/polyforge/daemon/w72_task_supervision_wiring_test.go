// carries: internal/polyforge/daemon/tasks.go, internal/polyforge/daemon/daemon.go, internal/polyforge/daemon/analysis_queue.go

package daemon

// w72_task_supervision_wiring_test.go (polyforge#72, u12 — AC9/AC10's
// daemon half): the task manager the daemon assembles carries the step
// lane's stuck-detection thresholds from their existing source (the
// daemon's supervision config, AC9 「阈值沿用现有来源」), and an analysis
// task's next candidate is prompted with the ticket re-read — the draft
// its predecessor already wrote through the narrow write path included
// (AC10). The judged-stuck attempt is visible in `GET /task/show` (AC9).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
	"github.com/silgrid/polyforge/internal/polyforge/task"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// w72StuckRunner is a task runner whose FIRST call blocks until the test
// lets it go and then answers the stuck verdict (the candidate is ended,
// the manager moves on); every later call completes at once. It records
// every RunSpec it was handed.
type w72StuckRunner struct {
	mu      sync.Mutex
	specs   []task.RunSpec
	started chan string   // the first call's task id
	proceed chan struct{} // closed by the test: the first call answers stuck
}

func (r *w72StuckRunner) Run(ctx context.Context, spec task.RunSpec, _ func(int64)) (task.RunResult, error) {
	r.mu.Lock()
	r.specs = append(r.specs, spec)
	n := len(r.specs)
	r.mu.Unlock()
	if n > 1 {
		return task.RunResult{Report: "continued draft", Tokens: 1}, nil
	}
	r.started <- spec.TaskID
	select {
	case <-r.proceed:
	case <-ctx.Done():
		return task.RunResult{}, ctx.Err()
	}
	return task.RunResult{Tokens: 1}, &task.StuckError{Cause: supervise.Cause("model_hung"), Evidence: "no model output for 7m0s"}
}

func (r *w72StuckRunner) recorded() []task.RunSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]task.RunSpec(nil), r.specs...)
}

// w72StuckWorld is w72World with the stuck runner, two task candidates and
// the supervision windows set to values no default has.
func w72StuckWorld(t *testing.T) (*testDaemon, *w72Hub, *w72StuckRunner) {
	t.Helper()
	runner := &w72StuckRunner{started: make(chan string, 1), proceed: make(chan struct{})}
	td, hub, _ := w72World(t, func(c *Config) {
		c.TaskRunner = runner
		c.Supervision.SilenceWindow = 7 * time.Minute
		c.Supervision.ToolWindow = 11 * time.Minute
	})
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, workspace.FileName),
		[]byte("steps:\n  proj:\n    task: { models: [pi:w72/first, pi:w72/second] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return td, hub, runner
}

// w72RunStuckThenNext schedules one analysis on wi, has the first
// candidate write draft through the narrow write path, ends it stuck and
// waits until the second candidate ran. It answers the task id.
func w72RunStuckThenNext(t *testing.T, td *testDaemon, hub *w72Hub, runner *w72StuckRunner, wi, draft string) string {
	t.Helper()
	w72Ticket(t, hub, wi, "## 背景\n\nthe idea\n\n## Acceptance\n\n- AC1 x — carried by build\n")
	res := w72Enqueue(t, td, wi)
	if res.State != "scheduled" {
		t.Fatalf("the analysis must schedule at once: %+v", res)
	}
	var id string
	select {
	case id = <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first candidate never started")
	}
	if draft != "" {
		if code, body := w72ProbeWrite(t, td, id, draft); code != 200 {
			t.Fatalf("the first candidate's draft write = %d %s", code, body)
		}
	}
	close(runner.proceed)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(runner.recorded()) >= 2 {
			return id
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the second candidate never ran (runs: %d)", len(runner.recorded()))
	return ""
}

func TestW72AnalysisPromptReReadsTicket(t *testing.T) {
	td, hub, runner := w72StuckWorld(t)
	draft := "### 1. 改动范围\n\n- written by the first candidate\n"
	w72RunStuckThenNext(t, td, hub, runner, "wi_w72_reread", draft)
	specs := runner.recorded()
	if strings.Contains(specs[0].Prompt, "written by the first candidate") {
		t.Fatalf("the first candidate cannot have seen a draft not yet written:\n%s", specs[0].Prompt)
	}
	if !strings.Contains(specs[1].Prompt, "written by the first candidate") {
		t.Fatalf("the second candidate's prompt must carry the draft its predecessor wrote (the ticket re-read, AC10):\n%s", specs[1].Prompt)
	}
	if !strings.Contains(specs[1].Prompt, "the idea") {
		t.Fatalf("the second candidate's prompt must carry the ticket body:\n%s", specs[1].Prompt)
	}
}

func TestW72TaskSupervisionLimitsAndShowAttempts(t *testing.T) {
	td, hub, runner := w72StuckWorld(t)
	id := w72RunStuckThenNext(t, td, hub, runner, "wi_w72_limits", "")
	for i, s := range runner.recorded() {
		if s.Limits.Silence != 7*time.Minute || s.Limits.Tool != 11*time.Minute {
			t.Errorf("dispatch %d carries limits %+v, want the daemon's silence 7m and tool 11m windows (AC9: the thresholds' existing source)", i, s.Limits)
		}
	}
	// the stuck verdict is visible in the task's show document once the
	// task has settled
	var shown task.Task
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		code, body := td.ovDo(t, "GET", "/task/show/"+id, nil)
		if code != 200 {
			t.Fatalf("GET /task/show = %d %s", code, body)
		}
		shown = task.Task{}
		if err := json.Unmarshal(body, &shown); err != nil {
			t.Fatalf("show body: %v\n%s", err, body)
		}
		if shown.State == task.StateCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if shown.State != task.StateCompleted {
		t.Fatalf("the task never completed on its second candidate: %+v", shown)
	}
	if len(shown.Attempts) != 1 || shown.Attempts[0].Candidate != "pi:w72/first" || shown.Attempts[0].Cause != "model_hung" {
		t.Fatalf("show must carry the first candidate's stuck attempt and its cause: %+v", shown.Attempts)
	}
}
