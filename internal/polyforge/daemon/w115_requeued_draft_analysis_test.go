// carries: internal/polyforge/daemon/analysis_queue.go
package daemon

// w115_requeued_draft_analysis_test.go (polyforge#115): an interrupted
// analysis that already wrote one unconfirmed draft re-queues, and the
// re-queued entry (Retries > 0) keeps dispatching past that draft — the
// resume the re-queue comment「已重新入队，接着已写的部分继续」promises
// (AC1: red on the pre-fix tree, where the draft made the ticket count as
//「已有分析」and the entry was dropped). An analysis the owner confirmed,
// or wrote by hand — a section without the unconfirmed marker — still
// needs none: a re-queued entry for such a ticket is dropped at dispatch,
// and a first dispatch never spawns on a draft (AC2).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// w115World assembles the restart shape the shared w72RestartWorld needs:
// the ws/data/hub subdirectories of one dir, the develop lane and the
// project's declared task models, over one fresh analysis hub.
func w115World(t *testing.T) (dir, wf string, hub *w72Hub) {
	t.Helper()
	dir = t.TempDir()
	for _, d := range []string{filepath.Join(dir, "ws"), filepath.Join(dir, "data"), filepath.Join(dir, "hub")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wf = t.TempDir()
	workflowtest.WriteLane(t, wf, "develop", "steps:\n  - { id: build }\n")
	if err := os.WriteFile(filepath.Join(dir, "ws", workspace.FileName),
		[]byte("steps:\n  proj:\n    task: { models: [pi:w72/m] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, wf, &w72Hub{fakeHub: newFakeHub(), contents: map[string]*string{}}
}

func TestW115InterruptedDraftAnalysisResumesOnRestart(t *testing.T) {
	setGitIdentity(t)
	dir, wf, hub := w115World(t)
	w72Ticket(t, hub, "wi_w115_r", "## 背景\n\nx\n\n## Acceptance\n\n- AC1 — carried by build\n")

	// round 1: the analysis runs, writes one unconfirmed draft item
	// through the real narrow write path, and the daemon stops under it.
	runner := newW72Runner()
	td, runDone := w72RestartWorld(t, dir, wf, hub, runner, 1)
	res := w72Enqueue(t, td, "wi_w115_r")
	if res.State != "scheduled" {
		t.Fatalf("the analysis must schedule at once: %+v", res)
	}
	waitFor(t, "the analysis running", 5*time.Second, func() bool { return len(runner.taskIDs()) > 0 })
	firstTask := runner.taskIDs()[0]
	draft := "### 1. 改动范围\n\n- internal/polyforge/daemon/analysis_queue.go 的重新派发判定\n"
	if code, body := w72ProbeWrite(t, td, firstTask, draft); code != 200 {
		t.Fatalf("the draft write = %d %s", code, body)
	}
	hub.mu.Lock()
	after := *hub.contents["wi_w115_r"]
	hub.mu.Unlock()
	if !strings.Contains(after, workflow.AutoAnalysisMarker(firstTask)) {
		t.Fatalf("the written section must carry the task's unconfirmed marker:\n%s", after)
	}
	if err := td.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
		t.Fatal(err)
	}
	<-runDone
	settleAfterShutdown(t, td.Daemon)
	comment := w72WaitComment(t, hub, "wi_w115_r")
	for _, want := range []string{firstTask, "被中断", "第 1 项", "已重新入队，接着已写的部分继续", "第 1/2 次"} {
		if !strings.Contains(comment, want) {
			t.Errorf("the interrupted comment must carry %q: %s", want, comment)
		}
	}

	// round 2: the re-queued entry dispatches despite the section — the
	// draft is the interrupted analysis's own unconfirmed one — and the
	// resumed candidate runs with that draft in its prompt.
	runner2 := newW72Runner()
	td2, runDone2 := w72RestartWorld(t, dir, wf, hub, runner2, 2)
	t.Cleanup(func() {
		// the body leaves the daemon running for its assertions; the
		// backstop only makes sure a failed body leaves it stopped.
		_ = td2.Shutdown(context.Background(), DirectShutdownCause())
		select {
		case <-runDone2:
		default:
		}
	})
	waitFor(t, "the re-queued analysis running", 10*time.Second, func() bool { return len(runner2.taskIDs()) > 0 })
	secondTask := runner2.taskIDs()[0]
	tk, err := td2.tasks.Show(secondTask)
	if err != nil || tk.Spec.AnalysisOf != "wi_w115_r" {
		t.Fatalf("the restarted daemon must run the same ticket's analysis: %+v %v", tk, err)
	}
	if tk.Spec.AnalysisRetries != 1 {
		t.Fatalf("the re-queued analysis must carry retry 1, got %d", tk.Spec.AnalysisRetries)
	}
	spec, ok := runner2.specOf(secondTask)
	if !ok {
		t.Fatal("the runner never saw the resumed analysis task")
	}
	if !strings.Contains(spec.Prompt, draft) {
		t.Fatalf("the resumed candidate must run with the draft already written:\n%s", spec.Prompt)
	}
	// the dispatched entry left the queue
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(readW72Queue(t, td2)) != 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if entries := readW72Queue(t, td2); len(entries) != 0 {
		t.Fatalf("the dispatched entry must leave the queue: %+v", entries)
	}
}

func TestW115RequeueDropsMarkerlessAnalysis(t *testing.T) {
	setGitIdentity(t)
	dir, wf, hub := w115World(t)
	w72Ticket(t, hub, "wi_w115_conf", "## 背景\n\nx\n\n## Acceptance\n\n- AC1 — carried by build\n")
	w72Ticket(t, hub, "wi_w115_hand", "## 背景\n\nx\n\n## Acceptance\n\n- AC1 — carried by build\n")

	runner := newW72Runner()
	td, runDone := w72RestartWorld(t, dir, wf, hub, runner, 1)
	// first dispatch keeps the old judgment: a ticket whose body already
	// carries an unconfirmed draft needs no FIRST analysis.
	w72Ticket(t, hub, "wi_w115_first",
		"## 背景\n\nx\n\n## 分析\n\n"+workflow.AutoAnalysisMarker("task_w115_pre")+
			"\n\n### 1. 改动范围\n\n- a\n\n## Acceptance\n\n- AC1 — carried by build\n")
	if res := w72Enqueue(t, td, "wi_w115_first"); res.State != "not_needed" {
		t.Fatalf("a ticket with a draft needs no first analysis: %+v", res)
	}
	// both analyses run, each writes one draft, the daemon stops under them.
	resConf := w72Enqueue(t, td, "wi_w115_conf")
	if resConf.State != "scheduled" {
		t.Fatalf("the first analysis must schedule: %+v", resConf)
	}
	resHand := w72Enqueue(t, td, "wi_w115_hand")
	if resHand.State != "scheduled" {
		t.Fatalf("the second analysis must schedule: %+v", resHand)
	}
	waitFor(t, "both analyses running", 5*time.Second, func() bool { return len(runner.taskIDs()) >= 2 })
	for _, r := range []struct{ task, draft string }{
		{resConf.TaskID, "### 1. 改动范围\n\n- conf\n"},
		{resHand.TaskID, "### 1. 改动范围\n\n- hand\n"},
	} {
		if code, body := w72ProbeWrite(t, td, r.task, r.draft); code != 200 {
			t.Fatalf("the draft write of %s = %d %s", r.task, code, body)
		}
	}
	if err := td.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
		t.Fatal(err)
	}
	<-runDone
	settleAfterShutdown(t, td.Daemon)
	entries := readW72Queue(t, td)
	if len(entries) != 2 {
		t.Fatalf("both interrupted analyses must requeue: %+v", entries)
	}
	got := map[string]bool{entries[0]: true, entries[1]: true}
	if !got["wi_w115_conf"] || !got["wi_w115_hand"] {
		t.Fatalf("the requeued entries must be exactly the two analyses: %+v", entries)
	}
	// while the daemon is down, the owner confirms one draft (the real
	// confirm edit strips the marker) and writes the other by hand.
	hub.mu.Lock()
	confirmed, changed := workflow.ConfirmAnalysisSection(*hub.contents["wi_w115_conf"])
	if !changed {
		hub.mu.Unlock()
		t.Fatal("the confirm edit must strip the unconfirmed marker")
	}
	hub.contents["wi_w115_conf"] = &confirmed
	handWritten, _ := workflow.ReplaceAnalysisSection(*hub.contents["wi_w115_hand"],
		"手写分析（owner，2026-11-18）：改 internal/polyforge/daemon/analysis_queue.go 的重新派发判定即可。")
	hub.contents["wi_w115_hand"] = &handWritten
	hub.mu.Unlock()
	for _, wi := range []string{"wi_w115_conf", "wi_w115_hand"} {
		hub.mu.Lock()
		body := *hub.contents[wi]
		hub.mu.Unlock()
		if !workflow.HasAnalysis(body) || workflow.AnalysisUnconfirmed(body) {
			t.Fatalf("%s must carry an analysis without the unconfirmed marker:\n%s", wi, body)
		}
	}

	// round 2: both re-queued entries are dropped at dispatch — nothing
	// re-triggers an analysis the owner settled.
	runner2 := newW72Runner()
	td2, runDone2 := w72RestartWorld(t, dir, wf, hub, runner2, 2)
	t.Cleanup(func() {
		_ = td2.Shutdown(context.Background(), DirectShutdownCause())
		select {
		case <-runDone2:
		default:
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(readW72Queue(t, td2)) != 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if entries := readW72Queue(t, td2); len(entries) != 0 {
		t.Fatalf("both entries must be dropped: %+v", entries)
	}
	if ids := runner2.taskIDs(); len(ids) != 0 {
		t.Fatalf("no analysis may re-trigger for a ticket without the unconfirmed marker: %v", ids)
	}
}
