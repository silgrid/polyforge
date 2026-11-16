package daemon

// w72_analysis_failure_comment_test.go (polyforge#72, u9 — AC5): a
// stopped analysis never disappears silently. A task that ended capped,
// failed or interrupted leaves ONE comment on the ticket naming the task
// id, its state and how far the draft got (the top-level 1–7 count of the
// section as it stands — established through the real narrow write path,
// never by seeding the ticket); what is written in `## 分析` stays. An
// interrupted analysis — the daemon stopped or restarted under it — is
// re-queued, at most twice per ticket (ruling 13); a completed one
// comments nothing (its gate is the confirm command, u10).

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// w72Comments answers the comments the fake hub recorded for wi.
func w72Comments(t *testing.T, hub *w72Hub, wi string) []string {
	t.Helper()
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return append([]string(nil), hub.comments[wi]...)
}

// w72WaitComment polls for the first comment on wi and answers it.
func w72WaitComment(t *testing.T, hub *w72Hub, wi string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cs := w72Comments(t, hub, wi); len(cs) > 0 {
			return cs[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no comment ever landed on %s", wi)
	return ""
}

func TestW72FailedAnalysisLeavesComment(t *testing.T) {
	td, hub, runner := w72World(t, nil)
	id := w72AnalysisTask(t, td, hub, runner, "wi_w72_fail")
	// the draft got two items in through the real write path before the
	// task died
	if code, body := w72ProbeWrite(t, td, id, "### 1. 改动范围\n\n- a\n\n### 2. 证明命令\n\n- b\n"); code != 200 {
		t.Fatalf("the draft write = %d %s", code, body)
	}
	runner.mu.Lock()
	runner.fail = true
	runner.mu.Unlock()
	runner.release()
	comment := w72WaitComment(t, hub, "wi_w72_fail")
	for _, want := range []string{id, "失败", "第 2 项"} {
		if !strings.Contains(comment, want) {
			t.Errorf("the comment must carry %q: %s", want, comment)
		}
	}
	// what was written stays in the section
	hub.mu.Lock()
	after := *hub.contents["wi_w72_fail"]
	hub.mu.Unlock()
	if !strings.Contains(after, "### 2. 证明命令") {
		t.Fatalf("the written part must survive the failure:\n%s", after)
	}
}

func TestW72CappedAnalysisLeavesComment(t *testing.T) {
	td, hub, runner := w72World(t, nil)
	id := w72AnalysisTask(t, td, hub, runner, "wi_w72_cap")
	runner.mu.Lock()
	runner.capped = true
	runner.mu.Unlock()
	runner.release()
	comment := w72WaitComment(t, hub, "wi_w72_cap")
	for _, want := range []string{id, "截停", "第 0 项"} {
		if !strings.Contains(comment, want) {
			t.Errorf("the comment must carry %q: %s", want, comment)
		}
	}
}

func TestW72CompletedAnalysisCommentsNothing(t *testing.T) {
	td, hub, runner := w72World(t, nil)
	w72AnalysisTask(t, td, hub, runner, "wi_w72_ok")
	runner.release()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(w72Comments(t, hub, "wi_w72_ok")) > 0 {
			t.Fatalf("a completed analysis comments nothing: %v", w72Comments(t, hub, "wi_w72_ok"))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// w72ProbeStatus probes GET /status on a daemon socket.
func w72ProbeStatus(sock string) (int, error) {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
	c := &http.Client{Transport: tr, Timeout: 2 * time.Second}
	resp, err := c.Get("http://daemon/status")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// w72RestartWorld assembles one analysis-restart round over the shared
// data dir and hub; the runner's gate stays closed, so the analysis is
// running when the daemon stops under it.
func w72RestartWorld(t *testing.T, dir, wf string, hub *w72Hub, runner *w72Runner, round int) (*testDaemon, chan error) {
	t.Helper()
	cfg := e2eConfig(filepath.Join(dir, "s.sock"), filepath.Join(dir, "ws"), filepath.Join(dir, "data"), filepath.Join(dir, "hub"), hub.fakeHub, &fakeSpawner{})
	cfg.WorkflowsDir = wf
	cfg.TaskRunner = runner
	cfg.Hub = hub
	cfg.SocketPath = filepath.Join(dir, fmt.Sprintf("s%d.sock", round))
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- d.Run(context.Background()) }()
	waitFor(t, "daemon serving", 5*time.Second, func() bool {
		_, serr := w72ProbeStatus(cfg.SocketPath)
		return serr == nil
	})
	return &testDaemon{Daemon: d, hub: hub.fakeHub, sock: cfg.SocketPath, runDone: runDone}, runDone
}

func TestW72InterruptedAnalysisRequeuedOnRestart(t *testing.T) {
	setGitIdentity(t)
	dir := t.TempDir()
	for _, d := range []string{filepath.Join(dir, "ws"), filepath.Join(dir, "data"), filepath.Join(dir, "hub")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "develop", "steps:\n  - { id: build }\n")
	if err := os.WriteFile(filepath.Join(dir, "ws", workspace.FileName),
		[]byte("steps:\n  proj:\n    task: { models: [pi:w72/m] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hub := &w72Hub{fakeHub: newFakeHub(), contents: map[string]*string{}}
	w72Ticket(t, hub, "wi_w72_r", "## 背景\n\nx\n\n## Acceptance\n\n- AC1 — carried by build\n")

	// round 1: the analysis is running when the daemon stops under it.
	runner := newW72Runner()
	td, runDone := w72RestartWorld(t, dir, wf, hub, runner, 1)
	res := w72Enqueue(t, td, "wi_w72_r")
	if res.State != "scheduled" {
		t.Fatalf("the analysis must schedule at once: %+v", res)
	}
	waitFor(t, "the analysis running", 5*time.Second, func() bool { return len(runner.taskIDs()) > 0 })
	firstTask := runner.taskIDs()[0]
	if err := td.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
		t.Fatal(err)
	}
	<-runDone
	settleAfterShutdown(t, td.Daemon)
	comment := w72WaitComment(t, hub, "wi_w72_r")
	for _, want := range []string{firstTask, "中断", "第 1/2 次"} {
		if !strings.Contains(comment, want) {
			t.Errorf("the interrupted comment must carry %q: %s", want, comment)
		}
	}

	// round 2: the re-queued analysis dispatches on the restarted daemon
	// with its retry count on the record, and one more interrupt re-queues
	// again.
	runner2 := newW72Runner()
	td2, runDone2 := w72RestartWorld(t, dir, wf, hub, runner2, 2)
	t.Cleanup(func() {
		_ = td2.Shutdown(context.Background(), DirectShutdownCause())
		<-runDone2
	})
	waitFor(t, "the re-queued analysis running", 10*time.Second, func() bool { return len(runner2.taskIDs()) > 0 })
	secondTask := runner2.taskIDs()[0]
	tk, err := td2.tasks.Show(secondTask)
	if err != nil || tk.Spec.AnalysisOf != "wi_w72_r" {
		t.Fatalf("the restarted daemon must run the same ticket's analysis: %+v %v", tk, err)
	}
	if tk.Spec.AnalysisRetries != 1 {
		t.Fatalf("the re-queued analysis must carry retry 1, got %d", tk.Spec.AnalysisRetries)
	}
	if err := td2.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
		t.Fatal(err)
	}
	<-runDone2
	settleAfterShutdown(t, td2.Daemon)
	cs := w72Comments(t, hub, "wi_w72_r")
	if len(cs) < 2 {
		t.Fatalf("the second interrupt must comment again: %v", cs)
	}
	for _, want := range []string{secondTask, "中断", "第 2/2 次"} {
		if !strings.Contains(cs[len(cs)-1], want) {
			t.Errorf("the second interrupt's comment must carry %q: %s", want, cs[len(cs)-1])
		}
	}

	// round 3: the third interrupt hits ruling 13's cap — commented, never
	// re-queued.
	runner3 := newW72Runner()
	td3, runDone3 := w72RestartWorld(t, dir, wf, hub, runner3, 3)
	t.Cleanup(func() {
		_ = td3.Shutdown(context.Background(), DirectShutdownCause())
		<-runDone3
	})
	waitFor(t, "the second re-queue's analysis running", 10*time.Second, func() bool { return len(runner3.taskIDs()) > 0 })
	if err := td3.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
		t.Fatal(err)
	}
	<-runDone3
	settleAfterShutdown(t, td3.Daemon)
	cs = w72Comments(t, hub, "wi_w72_r")
	last := cs[len(cs)-1]
	if !strings.Contains(last, "上限") {
		t.Fatalf("the third interrupt must name the cap, not re-queue: %s", last)
	}
	// and the queue file holds nothing: no third re-queue happened.
	raw, err := os.ReadFile(filepath.Join(dir, "data", analysisQueueFile))
	if err == nil && strings.Contains(string(raw), "wi_w72_r") {
		t.Fatalf("the capped analysis must not be re-queued: %s", raw)
	}
	_ = workflow.AutoAnalysisMarker
	_ = api.StatusQueued
}
