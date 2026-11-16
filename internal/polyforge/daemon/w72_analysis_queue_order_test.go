// carries: internal/polyforge/daemon/analysis_queue.go, internal/polyforge/task/manager.go

package daemon

// w72_analysis_queue_order_test.go (polyforge#72, u13 — AC2/AC5, the
// build's self-review s1#1–#3): an owner's `pf task cancel` of an
// analysis is no restart — it leaves the comment but is never re-queued
// (ruling 13 re-queues what a daemon stop interrupted); a queue head whose
// ticket the daemon can never read (CORE-404/403) is dropped instead of
// blocking every entry behind it; and a new request never overtakes the
// entries already waiting (「有空位后按入队顺序派出」).

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// w72GateHub answers GetWorkItem of the ids in fail with that error
// (until cleared); everything else is the w72Hub's.
type w72GateHub struct {
	*w72Hub
	gmu  sync.Mutex
	fail map[string]error
}

func (h *w72GateHub) GetWorkItem(id string) (*api.WorkItem, error) {
	h.gmu.Lock()
	err := h.fail[id]
	h.gmu.Unlock()
	if err != nil {
		return nil, err
	}
	return h.w72Hub.GetWorkItem(id)
}

func (h *w72GateHub) setFail(id string, err error) {
	h.gmu.Lock()
	defer h.gmu.Unlock()
	if err == nil {
		delete(h.fail, id)
		return
	}
	h.fail[id] = err
}

func w72GateWorld(t *testing.T) (*testDaemon, *w72GateHub, *w72Runner) {
	t.Helper()
	var gate *w72GateHub
	td, hub, runner := w72World(t, func(c *Config) {
		gate = &w72GateHub{w72Hub: c.Hub.(*w72Hub), fail: map[string]error{}}
		c.Hub = gate
	})
	if gate.w72Hub != hub {
		t.Fatal("the gate hub must wrap the world's hub")
	}
	return td, gate, runner
}

// w72WaitFor polls cond until it holds or the deadline passes.
func w72WaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func TestW72CancelledAnalysisIsNotRequeued(t *testing.T) {
	td, hub, runner := w72World(t, nil)
	id := w72AnalysisTask(t, td, hub, runner, "wi_w72_cancel")
	if code, body := stepDo(t, td.sock, "POST", "/task/cancel/"+id, ""); code != 200 {
		t.Fatalf("cancel = %d %s", code, body)
	}
	comment := w72WaitComment(t, hub, "wi_w72_cancel")
	if !strings.Contains(comment, id) || !strings.Contains(comment, "取消") {
		t.Errorf("the comment must name the task and say it was cancelled: %s", comment)
	}
	if strings.Contains(comment, "重新入队") {
		t.Errorf("a cancelled analysis must not claim a re-queue: %s", comment)
	}
	// the comment is posted after the re-queue decision: the queue is
	// settled by now
	if entries := readW72Queue(t, td); len(entries) != 0 {
		t.Fatalf("an owner's cancel must not re-queue the analysis: %+v", entries)
	}
}

func TestW72UnreadableQueueHeadIsDropped(t *testing.T) {
	td, hub, runner := w72GateWorld(t)
	w72FillSlots(t, td, 2)
	w72Ticket(t, hub.w72Hub, "wi_w72_lost", "## 背景\n\nx\n")
	w72Ticket(t, hub.w72Hub, "wi_w72_next", "## 背景\n\ny\n")
	if res := w72Enqueue(t, td, "wi_w72_lost"); res.State != "queued" {
		t.Fatalf("the full lane queues it: %+v", res)
	}
	if res := w72Enqueue(t, td, "wi_w72_next"); res.State != "queued" {
		t.Fatalf("the full lane queues it: %+v", res)
	}
	// the head's ticket vanishes for good (the daemon key can never read it)
	hub.setFail("wi_w72_lost", &api.Error{Code: api.ErrCoreNotFound, Detail: "work item not found"})
	runner.release()
	w72WaitFor(t, "the entry behind the unreadable head dispatches", func() bool {
		for _, tk := range tdTasks(t, td) {
			if tk.Spec.AnalysisOf == "wi_w72_next" {
				return true
			}
		}
		return false
	})
	w72WaitFor(t, "the queue empties", func() bool { return len(readW72Queue(t, td)) == 0 })
	for _, tk := range tdTasks(t, td) {
		if tk.Spec.AnalysisOf == "wi_w72_lost" {
			t.Fatalf("an unreadable ticket's analysis must never spawn: %+v", tk)
		}
	}
}

func TestW72EnqueueKeepsQueueOrder(t *testing.T) {
	td, hub, runner := w72GateWorld(t)
	w72FillSlots(t, td, 2)
	w72Ticket(t, hub.w72Hub, "wi_w72_first", "## 背景\n\nx\n")
	w72Ticket(t, hub.w72Hub, "wi_w72_second", "## 背景\n\ny\n")
	if res := w72Enqueue(t, td, "wi_w72_first"); res.State != "queued" {
		t.Fatalf("the full lane queues it: %+v", res)
	}
	// the head cannot be judged for now (a transient hub failure): it
	// stays queued while the slots free
	hub.setFail("wi_w72_first", errors.New("aihub: connection refused"))
	runner.release()
	w72WaitFor(t, "the filler tasks end", func() bool {
		for _, tk := range tdTasks(t, td) {
			if !tk.State.Terminal() {
				return false
			}
		}
		return true
	})
	// a free slot, a waiting entry: the new request queues behind it
	res := w72Enqueue(t, td, "wi_w72_second")
	if res.State != "queued" {
		t.Fatalf("a new request must not overtake the waiting entry: %+v", res)
	}
	if entries := readW72Queue(t, td); len(entries) != 2 || entries[0] != "wi_w72_first" || entries[1] != "wi_w72_second" {
		t.Fatalf("the queue must hold both in enqueue order: %+v", entries)
	}
	// the hub answers again: both dispatch, the first first
	hub.setFail("wi_w72_first", nil)
	var order []string
	w72WaitFor(t, "both analyses dispatch", func() bool {
		order = order[:0]
		for _, tk := range tdTasks(t, td) {
			if tk.Spec.AnalysisOf != "" {
				order = append(order, tk.Spec.AnalysisOf)
			}
		}
		return len(order) == 2
	})
	first, second := w72SpawnOrder(t, td, "wi_w72_first"), w72SpawnOrder(t, td, "wi_w72_second")
	if !first.Before(second) {
		t.Fatalf("the first enqueued ticket must dispatch first: first %v, second %v", first, second)
	}
}

// w72SpawnOrder answers when wi's analysis task was created.
func w72SpawnOrder(t *testing.T, td *testDaemon, wi string) time.Time {
	t.Helper()
	for _, tk := range tdTasks(t, td) {
		if tk.Spec.AnalysisOf == wi {
			return tk.CreatedAt
		}
	}
	t.Fatalf("no analysis task for %s", wi)
	return time.Time{}
}
