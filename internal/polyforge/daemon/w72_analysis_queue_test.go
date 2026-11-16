package daemon

// w72_analysis_queue_test.go (polyforge#72, u6 — AC2): the daemon's
// pending-analysis queue. A `pf file create` of a ticket whose lane
// requires acceptance and whose body has no `## 分析` section asks this
// daemon to schedule one analysis task; when the task lane is full — or
// execution is disabled, or a connection rotation is draining — the
// request is queued, persisted, and dispatched in enqueue order as soon
// as a Host slot frees (every Cell's dispatch tick re-tries, so any
// Cell's queue wakes). A ticket that went terminal, or gained an
// analysis section meanwhile, has its entry dropped at dispatch.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/task"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// w72Stamp is the updated_at every fake ticket read answers.
const w72Stamp = "2026-11-16T00:00:00Z"

// w72Hub layers content and analysis facts over the fake hub: the
// scheduler's reads must see the ticket body. Do serves the narrow
// write path's PATCH (with the content CAS, plus the one scripted
// stale answer with its concurrent owner edit) and the failure comment's
// POST; anything else falls through to the fake hub.
type w72Hub struct {
	*fakeHub
	contents map[string]*string
	// staleOnce, when set to a work item id, makes the FIRST content
	// PATCH of that id answer CONTENT_STALE — and inject the owner edit
	// that made it stale (another section changed) into the ticket, which
	// the retry must keep.
	staleOnce string
	// comments records every POST /work-items/{id}/comments body.
	comments map[string][]string
}

func (h *w72Hub) GetWorkItem(idOrSlug string) (*api.WorkItem, error) {
	wi, err := h.fakeHub.GetWorkItem(idOrSlug)
	if err != nil {
		return nil, err
	}
	if c, ok := h.contents[wi.ID]; ok {
		wi.Content = c
	}
	if wi.UpdatedAt == "" {
		wi.UpdatedAt = w72Stamp
	}
	return wi, nil
}

// w72FreshStamp answers a stamp no previous read has seen.
func w72FreshStamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// Do serves the write path's calls on the fake hub.
func (h *w72Hub) Do(method, path string, body, out any) error {
	if id, ok := strings.CutPrefix(path, "/api/v1/work-items/"); ok && method == "PATCH" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(w72RawBody(body)), &fields); err != nil {
			return &api.Error{Code: api.ErrCoreBadInput, Detail: "bad body"}
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		cur, ok := h.contents[id]
		if !ok || cur == nil {
			return &api.Error{Code: api.ErrCoreNotFound, Detail: "work item not found"}
		}
		if h.updatedAt == nil {
			h.updatedAt = map[string]string{}
		}
		stamp := h.updatedAt[id]
		if stamp == "" {
			stamp = w72Stamp
		}
		if h.staleOnce == id {
			h.staleOnce = ""
			// the owner edited ANOTHER section while the task held its read
			owner := strings.Replace(*cur, "the body before the write", "owner edited while the task ran", 1)
			h.contents[id] = &owner
			h.updatedAt[id] = w72FreshStamp()
			return &api.Error{Code: api.ErrCoreConflict, Detail: "work item changed since it was read (CONTENT_STALE): re-read and write again"}
		}
		var expected string
		if raw, has := fields["expected_updated_at"]; has && json.Unmarshal(raw, &expected) == nil && expected != "" && expected != stamp {
			return &api.Error{Code: api.ErrCoreConflict, Detail: "work item changed since it was read (CONTENT_STALE): re-read and write again"}
		}
		var content string
		if raw, has := fields["content"]; has && json.Unmarshal(raw, &content) == nil {
			c := content
			h.contents[id] = &c
			h.updatedAt[id] = w72FreshStamp()
		}
		return nil
	}
	if id, ok := strings.CutPrefix(path, "/api/v1/work-items/"); ok && strings.HasSuffix(id, "/comments") && method == "POST" {
		var req struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal([]byte(w72RawBody(body)), &req); err != nil || strings.TrimSpace(req.Body) == "" {
			return &api.Error{Code: api.ErrCoreBadInput, Detail: "comment body required"}
		}
		wi := strings.TrimSuffix(id, "/comments")
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.comments == nil {
			h.comments = map[string][]string{}
		}
		h.comments[wi] = append(h.comments[wi], req.Body)
		return nil
	}
	return h.fakeHub.Do(method, path, body, out)
}

// w72RawBody answers one Do body as JSON bytes.
func w72RawBody(body any) []byte {
	switch b := body.(type) {
	case []byte:
		return b
	case string:
		return []byte(b)
	case nil:
		return nil
	default:
		raw, _ := json.Marshal(b)
		return raw
	}
}

// w72Runner gates every task on one channel and records the task ids in
// run order (the FIFO the queue must keep).
type w72Runner struct {
	mu    sync.Mutex
	gate  chan struct{}
	calls []string
	specs []task.RunSpec
}

func newW72Runner() *w72Runner {
	return &w72Runner{gate: make(chan struct{})}
}

func (r *w72Runner) Run(ctx context.Context, spec task.RunSpec, progress func(int64)) (task.RunResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, spec.TaskID)
	r.specs = append(r.specs, spec)
	r.mu.Unlock()
	select {
	case <-r.gate:
	case <-ctx.Done():
		return task.RunResult{}, ctx.Err()
	}
	if progress != nil {
		progress(5)
	}
	return task.RunResult{Report: "draft of " + spec.TaskID, Tokens: 5}, nil
}

func (r *w72Runner) release() { close(r.gate) }

func (r *w72Runner) taskIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *w72Runner) specOf(id string) (task.RunSpec, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.specs {
		if s.TaskID == id {
			return s, true
		}
	}
	return task.RunSpec{}, false
}

// readW72Queue answers the persisted queue file's entries (ids only:
// the entry shape is the implementation's, the test decodes its own).
func readW72Queue(t *testing.T, td *testDaemon) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(td.cfg.DataDir, "analysis-queue.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var st struct {
		Entries []struct {
			WorkItemID string `json:"work_item_id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("analysis-queue.json: %v", err)
	}
	ids := make([]string, 0, len(st.Entries))
	for _, e := range st.Entries {
		ids = append(ids, e.WorkItemID)
	}
	return ids
}

func tdTasks(t *testing.T, td *testDaemon) []task.Task {
	t.Helper()
	ts, err := td.tasks.List()
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// analysisOf answers the ticket one task record is bound to.
func analysisOf(t *testing.T, td *testDaemon, id string) string {
	t.Helper()
	tk, err := td.tasks.Show(id)
	if err != nil {
		t.Fatalf("task %s: %v", id, err)
	}
	return tk.Spec.AnalysisOf
}

// w72Enqueue posts one enqueue request and answers its decoded body.
func w72Enqueue(t *testing.T, td *testDaemon, wi string) struct {
	State  string `json:"state"`
	TaskID string `json:"task_id,omitempty"`
	Detail string `json:"detail,omitempty"`
} {
	t.Helper()
	code, body := stepDo(t, td.sock, "POST", "/analysis/enqueue",
		`{"work_item_id":"`+wi+`","project":"proj"}`)
	if code != 200 {
		t.Fatalf("enqueue %s = %d %s, want 200", wi, code, body)
	}
	var res struct {
		State  string `json:"state"`
		TaskID string `json:"task_id,omitempty"`
		Detail string `json:"detail,omitempty"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("enqueue %s answer: %v (%s)", wi, err, body)
	}
	return res
}

// w72World is one daemon over the analysis-hub, the gate runner and a
// develop lane that requires acceptance, with the project's task models
// declared.
func w72World(t *testing.T, mutate func(*Config)) (*testDaemon, *w72Hub, *w72Runner) {
	t.Helper()
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "develop", "steps:\n  - { id: build }\n")
	inner := newFakeHub()
	hub := &w72Hub{fakeHub: inner, contents: map[string]*string{}}
	runner := newW72Runner()
	td := newTestDaemon(t, func(c *Config) {
		c.Hub = hub
		c.WorkflowsDir = wf
		c.TaskRunner = runner
		if mutate != nil {
			mutate(c)
		}
	})
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, workspace.FileName),
		[]byte("steps:\n  proj:\n    task: { models: [pi:w72/m] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return td, hub, runner
}

// w72Ticket seeds one queued execution ticket with the given body.
func w72Ticket(t *testing.T, hub *w72Hub, id, body string) {
	t.Helper()
	bundle := "develop"
	hub.mu.Lock()
	if hub.status == nil {
		hub.status = map[string]string{}
	}
	if hub.bundles == nil {
		hub.bundles = map[string]string{}
	}
	hub.status[id] = api.StatusQueued
	hub.bundles[id] = bundle
	hub.mu.Unlock()
	if hub.contents == nil {
		hub.contents = map[string]*string{}
	}
	hub.contents[id] = strp(body)
}

func w72FillSlots(t *testing.T, td *testDaemon, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"filler"}`)
		if code != 200 {
			t.Fatalf("filler spawn %d = %d %s", i, code, body)
		}
	}
}

func TestW72AnalysisEnqueueSpawnsImmediately(t *testing.T) {
	td, hub, runner := w72World(t, nil)
	w72Ticket(t, hub, "wi_w72_a", "## 背景\n\nthe ticket body\n\n## Acceptance\n\n- AC1 x — carried by build\n")
	res := w72Enqueue(t, td, "wi_w72_a")
	if res.State != "scheduled" || !strings.HasPrefix(res.TaskID, "task_") {
		t.Fatalf("a free slot must schedule at once: %+v", res)
	}
	if of := analysisOf(t, td, res.TaskID); of != "wi_w72_a" {
		t.Fatalf("the task must bind the analyzed ticket, got %q", of)
	}
	spec, ok := runner.specOf(res.TaskID)
	if !ok {
		t.Fatal("the runner never saw the analysis task")
	}
	if !strings.Contains(spec.Prompt, "the ticket body") || !strings.Contains(spec.Prompt, "提交前分析") {
		t.Fatalf("the prompt must carry the template and the ticket body:\n%s", spec.Prompt)
	}
	// nothing stays queued
	if entries := readW72Queue(t, td); len(entries) != 0 {
		t.Fatalf("a scheduled analysis leaves nothing queued: %+v", entries)
	}
}

func TestW72AnalysisQueuedWhenSlotsFull(t *testing.T) {
	td, hub, runner := w72World(t, nil)
	w72FillSlots(t, td, 2) // the Host bound's default: 2
	w72Ticket(t, hub, "wi_w72_a", "## 背景\n\nbody a\n")
	w72Ticket(t, hub, "wi_w72_b", "## 背景\n\nbody b\n")
	if res := w72Enqueue(t, td, "wi_w72_a"); res.State != "queued" {
		t.Fatalf("a full task lane must queue the analysis: %+v", res)
	}
	if res := w72Enqueue(t, td, "wi_w72_b"); res.State != "queued" {
		t.Fatalf("the second request queues behind the first: %+v", res)
	}
	entries := readW72Queue(t, td)
	if len(entries) != 2 || entries[0] != "wi_w72_a" || entries[1] != "wi_w72_b" {
		t.Fatalf("the queue must hold both entries in enqueue order: %+v", entries)
	}
	// a slot frees: both dispatch, in order, and the queue empties.
	runner.release()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(runner.taskIDs()) >= 4 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ids := runner.taskIDs()
	if len(ids) < 4 {
		t.Fatalf("both analyses must dispatch once the slot frees: %v", ids)
	}
	if of := analysisOf(t, td, ids[2]); of != "wi_w72_a" {
		t.Fatalf("the FIRST enqueued ticket dispatches first, got %q (order %v)", of, ids)
	}
	if of := analysisOf(t, td, ids[3]); of != "wi_w72_b" {
		t.Fatalf("the second follows, got %q (order %v)", of, ids)
	}
	if entries := readW72Queue(t, td); len(entries) != 0 {
		t.Fatalf("a dispatched queue empties: %+v", entries)
	}
}

func TestW72AnalysisQueueSurvivesRestart(t *testing.T) {
	setGitIdentity(t)
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	data := filepath.Join(dir, "data")
	hubBase := filepath.Join(dir, "hub")
	for _, d := range []string{ws, data, hubBase} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "develop", "steps:\n  - { id: build }\n")
	if err := os.WriteFile(filepath.Join(ws, workspace.FileName),
		[]byte("steps:\n  proj:\n    task: { models: [pi:w72/m] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hub := &w72Hub{fakeHub: newFakeHub(), contents: map[string]*string{}}
	c := strp("## 背景\n\nsurvives\n")
	hub.contents["wi_w72_r"] = c
	hub.mu.Lock()
	hub.status["wi_w72_r"] = api.StatusQueued
	if hub.bundles == nil {
		hub.bundles = map[string]string{}
	}
	bundle := "develop"
	hub.bundles["wi_w72_r"] = bundle
	hub.mu.Unlock()

	cfg := e2eConfig(filepath.Join(dir, "s.sock"), ws, data, hubBase, hub.fakeHub, &fakeSpawner{})
	cfg.WorkflowsDir = wf
	runner := newW72Runner()
	cfg.TaskRunner = runner
	cfg.Hub = hub

	// round 1: both slots busy, the analysis queues, the daemon dies.
	d1, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- d1.Run(context.Background()) }()
	waitFor(t, "daemon serving", 5*time.Second, func() bool {
		_, serr := cli.NewDaemonClient(cfg.SocketPath).Status()
		return serr == nil
	})
	for i := 0; i < 2; i++ {
		if code, b := stepDo(t, cfg.SocketPath, "POST", "/task/spawn", `{"project":"proj","prompt":"filler"}`); code != 200 {
			t.Fatalf("filler spawn = %d %s", code, b)
		}
	}
	if code, b := stepDo(t, cfg.SocketPath, "POST", "/analysis/enqueue", `{"work_item_id":"wi_w72_r","project":"proj"}`); code != 200 || !strings.Contains(b, `"queued"`) {
		t.Fatalf("enqueue under a full lane = %d %s, want queued", code, b)
	}
	if err := d1.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runDone:
	case <-time.After(15 * time.Second):
		t.Fatal("the first daemon never stopped")
	}
	settleAfterShutdown(t, d1)

	// round 2: the queue survived; a free slot dispatches the entry.
	runner2 := newW72Runner()
	cfg.TaskRunner = runner2
	cfg.SocketPath = filepath.Join(dir, "s2.sock")
	d2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	runDone2 := make(chan error, 1)
	go func() { runDone2 <- d2.Run(context.Background()) }()
	t.Cleanup(func() {
		_ = d2.Shutdown(context.Background(), DirectShutdownCause())
		<-runDone2
	})
	waitFor(t, "the restarted daemon serving", 5*time.Second, func() bool {
		_, serr := cli.NewDaemonClient(cfg.SocketPath).Status()
		return serr == nil
	})
	deadline := time.Now().Add(10 * time.Second)
	var ids []string
	for time.Now().Before(deadline) {
		ids = runner2.taskIDs()
		if len(ids) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(ids) == 0 {
		t.Fatal("the queued analysis never dispatched after the restart")
	}
	tk, err := d2.tasks.Show(ids[0])
	if err != nil || tk.Spec.AnalysisOf != "wi_w72_r" {
		t.Fatalf("the restarted daemon's task must be the queued analysis: %+v %v", tk, err)
	}
}

func TestW72AnalysisQueueDropsTerminalAndAnalyzed(t *testing.T) {
	td, hub, _ := w72World(t, nil)
	// not scheduled at all: the body already carries an analysis.
	w72Ticket(t, hub, "wi_w72_done", "## 分析\n\n免分析（2026-11-16，owner）\n")
	if res := w72Enqueue(t, td, "wi_w72_done"); res.State != "not_needed" {
		t.Fatalf("a ticket with an analysis section must not be scheduled: %+v", res)
	}
	// not scheduled: the ticket is terminal.
	w72Ticket(t, hub, "wi_w72_gone", "## 背景\n\nx\n")
	hub.mu.Lock()
	hub.status["wi_w72_gone"] = api.StatusWrapped
	hub.mu.Unlock()
	if res := w72Enqueue(t, td, "wi_w72_gone"); res.State != "not_needed" {
		t.Fatalf("a terminal ticket must not be scheduled: %+v", res)
	}
	// queued, then the ticket wraps before a slot frees: the entry is
	// dropped at dispatch, never spawned.
	w72FillSlots(t, td, 2)
	w72Ticket(t, hub, "wi_w72_late", "## 背景\n\nx\n")
	if res := w72Enqueue(t, td, "wi_w72_late"); res.State != "queued" {
		t.Fatalf("the full lane queues it: %+v", res)
	}
	hub.mu.Lock()
	hub.status["wi_w72_late"] = api.StatusCanceled
	hub.mu.Unlock()
	// let enough ticks pass, then confirm nothing spawned and the entry
	// left the queue.
	time.Sleep(300 * time.Millisecond)
	for _, tk := range tdTasks(t, td) {
		if tk.Spec.AnalysisOf == "wi_w72_late" {
			t.Fatalf("a terminal ticket's analysis must never spawn: %+v", tk)
		}
	}
	if entries := readW72Queue(t, td); len(entries) != 0 {
		t.Fatalf("the terminal ticket's entry must be dropped: %+v", entries)
	}
}

func TestW72AnalysisQueuedWhileExecDisabled(t *testing.T) {
	td, hub, runner := w72World(t, func(c *Config) { c.ExecutionDisabled = "the test disabled it" })
	w72Ticket(t, hub, "wi_w72_off", "## 背景\n\nx\n")
	res := w72Enqueue(t, td, "wi_w72_off")
	if res.State != "queued" {
		t.Fatalf("a disabled execution plane queues without dispatching: %+v", res)
	}
	time.Sleep(300 * time.Millisecond) // several dispatch ticks
	if n := len(runner.taskIDs()); n != 0 {
		t.Fatalf("a disabled execution plane dispatches nothing, got %d task(s)", n)
	}
	// the plane re-enables: the tick dispatches the entry.
	td.execDisabled = ""
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(runner.taskIDs()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := len(runner.taskIDs()); n == 0 {
		t.Fatal("the queued analysis never dispatched after the plane re-enabled")
	}
	if of := analysisOf(t, td, runner.taskIDs()[0]); of != "wi_w72_off" {
		t.Fatalf("the dispatched task must bind the queued ticket, got %q", of)
	}
}
