package daemon

// analysis_redo_test.go (polyforge#233): an owner's `pf file analysis redo`
// re-schedules a ticket's pre-submit analysis under the current prompt and
// keeps the old text. The route answers only a queued or blocked execution
// ticket on an acceptance lane that no local execution job or analysis task
// holds; every refusal leaves the ticket untouched.

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/task"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// pf233RedoCall posts one redo request over the daemon's own socket.
func pf233RedoCall(t *testing.T, td *testDaemon, wi string) (int, string) {
	t.Helper()
	return stepDo(t, td.sock, "POST", "/analysis/redo",
		`{"work_item_id":"`+wi+`","project":"proj"}`)
}

// redoRequestOf answers the pending redo request id the ticket carries.
func redoRequestOf(content string) (string, bool) {
	return workflow.AnalysisRedoRequestID(content)
}

// pf233RedoCallSock posts one redo request over a given daemon socket.
func pf233RedoCallSock(t *testing.T, sock, wi string) (int, string) {
	t.Helper()
	return stepDo(t, sock, "POST", "/analysis/redo",
		`{"work_item_id":"`+wi+`","project":"proj"}`)
}

// pf233RedoCallAlways is pf233RedoCall without t.Fatalf on transport
// errors: the concurrency test calls it from goroutines.
func pf233RedoCallAlways(t *testing.T, sock, wi string) (int, string) {
	t.Helper()
	return stepDo(t, sock, "POST", "/analysis/redo",
		`{"work_item_id":"`+wi+`","project":"proj"}`)
}

// pf233QueueEntriesFrom reads the queue file under one data dir.
func pf233QueueEntriesFrom(t *testing.T, dataDir string) []struct {
	WorkItemID  string `json:"work_item_id"`
	RedoRequest string `json:"redo_request_id"`
	EnqueuedAt  string `json:"enqueued_at"`
	Retries     int    `json:"retries"`
} {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dataDir, "analysis-queue.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var st struct {
		Entries []struct {
			WorkItemID  string `json:"work_item_id"`
			RedoRequest string `json:"redo_request_id"`
			EnqueuedAt  string `json:"enqueued_at"`
			Retries     int    `json:"retries"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("analysis-queue.json: %v", err)
	}
	return st.Entries
}

// pf233Content answers the ticket body the hub currently serves.
func pf233Content(t *testing.T, hub *w72Hub, wi string) string {
	t.Helper()
	item, err := hub.GetWorkItem(wi)
	if err != nil {
		t.Fatalf("read %s: %v", wi, err)
	}
	if item.Content == nil {
		return ""
	}
	return *item.Content
}

// pf233QueueEntries answers the persisted redo request ids.
func pf233QueueEntries(t *testing.T, td *testDaemon) []struct {
	WorkItemID  string `json:"work_item_id"`
	RedoRequest string `json:"redo_request_id"`
	EnqueuedAt  string `json:"enqueued_at"`
	Retries     int    `json:"retries"`
} {
	t.Helper()
	return pf233QueueEntriesFrom(t, td.cfg.DataDir)
}

const pf233TicketBody = "## 背景\n\nthe ticket body\n\n## 分析\n\n### 1. 改动范围\n\n- old draft\n\n## Acceptance\n\n- AC1 x — carried by build\n"

func TestAnalysisRedoArchivesAndQueues(t *testing.T) {
	td, hub, _ := w72World(t, nil)
	w72FillSlots(t, td, 2) // the lane is full: the request must wait, persisted
	w72Ticket(t, hub, "wi_redo_ok", pf233TicketBody)
	before := pf233Content(t, hub, "wi_redo_ok")

	code, body := pf233RedoCall(t, td, "wi_redo_ok")
	if code != 200 {
		t.Fatalf("redo = %d %s, want 200", code, body)
	}
	var res struct {
		State     string `json:"state"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("redo answer: %v (%s)", err, body)
	}
	if res.State != "queued" {
		t.Fatalf("a redo under a full task lane must queue its request, got %+v", res)
	}
	if res.RequestID == "" {
		t.Fatalf("a redo must name its request id: %+v", res)
	}
	content := pf233Content(t, hub, "wi_redo_ok")
	if !strings.Contains(content, "## 历次分析") || !strings.Contains(content, "### 1. 改动范围\n\n- old draft") {
		t.Fatalf("the old analysis must be archived on the ticket:\n%s", content)
	}
	if got, ok := redoRequestOf(content); !ok || got != res.RequestID {
		t.Fatalf("the pending marker names %q, %v; want the request id %q", got, ok, res.RequestID)
	}
	if content == before {
		t.Fatal("the ticket is unchanged: the redo must archive and mark it")
	}
	entries := pf233QueueEntries(t, td)
	if len(entries) != 1 || entries[0].WorkItemID != "wi_redo_ok" || entries[0].RedoRequest != res.RequestID {
		t.Fatalf("the redo request must be persisted before dispatch, got %+v", entries)
	}
	for _, tk := range tdTasks(t, td) {
		if tk.Spec.AnalysisOf != "" {
			t.Fatalf("a queued redo must not spawn an analysis before a slot frees, got %+v", tk)
		}
	}
}

// TestAnalysisRedoDispatchesOneTaskWithBuildPrompt (u3, AC1): the redo's
// round reaches the same analysis task the create-time scheduler builds —
// one task, bound to the ticket, prompted by the one analysisPrompt.
func TestAnalysisRedoDispatchesOneTaskWithBuildPrompt(t *testing.T) {
	td, hub, runner := w72World(t, func(c *Config) { c.MaxConcurrent = 8 })
	w72Ticket(t, hub, "wi_redo_dispatch", pf233TicketBody)
	code, body := pf233RedoCall(t, td, "wi_redo_dispatch")
	if code != 200 {
		t.Fatalf("redo = %d %s, want 200", code, body)
	}
	var res struct {
		State     string `json:"state"`
		TaskID    string `json:"task_id"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("redo answer: %v (%s)", err, body)
	}
	if res.State != "scheduled" || !strings.HasPrefix(res.TaskID, "task_") {
		t.Fatalf("a free lane must schedule one analysis task: %+v", res)
	}
	if of := analysisOf(t, td, res.TaskID); of != "wi_redo_dispatch" {
		t.Fatalf("analysis_of = %q, want the redone ticket", of)
	}
	spec, ok := w72WaitSpec(runner, res.TaskID)
	if !ok {
		t.Fatalf("the runner never saw the redo task %s", res.TaskID)
	}
	item, err := hub.GetWorkItem("wi_redo_dispatch")
	if err != nil {
		t.Fatal(err)
	}
	want, err := td.analysisPrompt(item)
	if err != nil {
		t.Fatalf("build the create-time prompt: %v", err)
	}
	tk, err := td.tasks.Show(res.TaskID)
	if err != nil {
		t.Fatalf("show the redo task: %v", err)
	}
	if tk.Spec.Prompt != want {
		t.Fatalf("the redo task's prompt must be the create-time analysisPrompt, byte for byte:\n--- got ---\n%s\n--- want ---\n%s", tk.Spec.Prompt, want)
	}
	content := pf233Content(t, hub, "wi_redo_dispatch")
	if !strings.Contains(spec.Prompt, content) {
		t.Fatalf("the redo prompt must carry the ticket as it stands (the archived old text included):\n%s", spec.Prompt)
	}
	if !strings.Contains(content, "### 1. 改动范围\n\n- old draft") || !strings.Contains(content, "## 历次分析") {
		t.Fatalf("the ticket must keep the archive while the task runs:\n%s", content)
	}
	if ts := tdTasks(t, td); len(ts) != 1 {
		t.Fatalf("exactly one analysis task per redo round, got %d: %+v", len(ts), ts)
	}
}

// TestAnalysisRedoWriteAndConfirmGate (u3, AC2): the scheduled task writes
// through the existing narrow path because the redo marker is unconfirmed;
// the auto-pull wave skips the ticket until the owner confirms, then claims
// it, and the archive survives both.
func TestAnalysisRedoWriteAndConfirmGate(t *testing.T) {
	td, hub, runner := w72World(t, func(c *Config) {
		c.MaxConcurrent = 8
		c.DrainInterval = time.Hour
		c.DispatchTick = time.Hour
	})
	w72Ticket(t, hub, "wi_redo_write", pf233TicketBody)
	code, body := pf233RedoCall(t, td, "wi_redo_write")
	if code != 200 {
		t.Fatalf("redo = %d %s, want 200", code, body)
	}
	var res struct {
		State  string `json:"state"`
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("redo answer: %v (%s)", err, body)
	}
	if res.TaskID == "" {
		t.Fatalf("the redo must schedule a task: %s", body)
	}
	if _, ok := w72WaitSpec(runner, res.TaskID); !ok {
		t.Fatalf("the runner never saw %s", res.TaskID)
	}

	draft := "### 1. 改动范围\n\n#### 1.1 新稿\n\n- new draft\n"
	if err := td.writeAnalysisSection(res.TaskID, "wi_redo_write", draft); err != nil {
		t.Fatalf("the running redo task must write through the narrow path: %v", err)
	}
	written := pf233Content(t, hub, "wi_redo_write")
	if !strings.Contains(written, workflow.AutoAnalysisMarker(res.TaskID)) {
		t.Fatalf("the write must stamp the task id marker:\n%s", written)
	}
	if !strings.Contains(written, "- new draft") {
		t.Fatalf("the task's draft must be the active section:\n%s", written)
	}
	if !strings.Contains(written, "### 1. 改动范围\n\n- old draft") {
		t.Fatalf("the old analysis must survive in the archive:\n%s", written)
	}
	if !workflow.AnalysisUnconfirmed(written) {
		t.Fatalf("the written analysis must still be unconfirmed:\n%s", written)
	}

	// confirm 前：auto-pull 跳过这张票。
	bundle := "develop"
	stamp := w72Stamp
	if hub.updatedAt != nil {
		if s := hub.updatedAt["wi_redo_write"]; s != "" {
			stamp = s
		}
	}
	queueItem := func(content string) api.WorkItem {
		return api.WorkItem{ID: "wi_redo_write", Slug: "wi_redo_write", ProjectName: "proj",
			Status: api.StatusQueued, Priority: api.PriorityNormal, BundleName: &bundle,
			Content: strp(content), UpdatedAt: stamp}
	}
	hub.mu.Lock()
	hub.queues = []api.WorkItem{queueItem(written)}
	hub.mu.Unlock()
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}
	td.fetchAutoWave()
	if td.q.IsPending("wi_redo_write") {
		t.Fatal("the auto-pull wave claimed a ticket whose redo analysis is unconfirmed")
	}

	confirmed, changed := workflow.ConfirmAnalysisSection(written)
	if !changed {
		t.Fatal("confirm must remove the redo task's marker")
	}
	hub.mu.Lock()
	hub.queues = []api.WorkItem{queueItem(confirmed)}
	hub.mu.Unlock()
	td.fetchAutoWave()
	if !td.q.IsPending("wi_redo_write") {
		t.Fatal("after confirm the auto-pull wave must claim the analyzed ticket")
	}
	if strings.Contains(confirmed, "pf:auto-analysis") {
		t.Fatalf("the confirmed section must carry no marker:\n%s", confirmed)
	}
	if !strings.Contains(confirmed, "- new draft") || !strings.Contains(confirmed, "- old draft") {
		t.Fatalf("confirm must keep the draft and the archive:\n%s", confirmed)
	}
}

func TestAnalysisRedoRefusesIneligibleUnchanged(t *testing.T) {
	cases := []struct {
		name    string
		status  string
		content string
		reason  string
	}{
		{"running", api.StatusRunning, pf233TicketBody, "queued or blocked"},
		{"paused", api.StatusPaused, pf233TicketBody, "queued or blocked"},
		{"wrapped", api.StatusWrapped, pf233TicketBody, "queued or blocked"},
		{"failed", api.StatusFailed, pf233TicketBody, "queued or blocked"},
		{"canceled", api.StatusCanceled, pf233TicketBody, "queued or blocked"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			td, hub, _ := w72World(t, nil)
			w72Ticket(t, hub, "wi_redo_no", c.content)
			hub.mu.Lock()
			hub.status["wi_redo_no"] = c.status
			hub.mu.Unlock()
			before := pf233Content(t, hub, "wi_redo_no")
			code, body := pf233RedoCall(t, td, "wi_redo_no")
			if code == 200 {
				t.Fatalf("a %s ticket must be refused, got 200 %s", c.status, body)
			}
			if !strings.Contains(body, c.reason) {
				t.Fatalf("the refusal must say why (%q), got %s", c.reason, body)
			}
			if after := pf233Content(t, hub, "wi_redo_no"); after != before {
				t.Fatalf("a refused redo must leave the ticket unchanged:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if entries := pf233QueueEntries(t, td); len(entries) != 0 {
				t.Fatalf("a refused redo must queue nothing, got %+v", entries)
			}
		})
	}

	t.Run("not an execution ticket", func(t *testing.T) {
		td, hub, _ := w72World(t, nil)
		w72Ticket(t, hub, "wi_redo_nobundle", pf233TicketBody)
		hub.mu.Lock()
		delete(hub.bundles, "wi_redo_nobundle")
		hub.mu.Unlock()
		code, body := pf233RedoCall(t, td, "wi_redo_nobundle")
		if code == 200 || !strings.Contains(body, "bundle") {
			t.Fatalf("a ticket without a bundle must be refused naming it, got %d %s", code, body)
		}
	})

	t.Run("local execution job", func(t *testing.T) {
		td, hub, _ := w72World(t, nil)
		w72Ticket(t, hub, "wi_redo_pending", pf233TicketBody)
		if err := td.q.Enqueue(qEntry{WorkItemID: "wi_redo_pending", Origin: runtime.Origin{Kind: runtime.OriginChat}}); err != nil {
			t.Fatalf("seed pending job: %v", err)
		}
		code, body := pf233RedoCall(t, td, "wi_redo_pending")
		if code == 200 || !strings.Contains(body, "execution job") {
			t.Fatalf("a ticket with a local pending job must be refused, got %d %s", code, body)
		}
	})

	t.Run("running analysis task", func(t *testing.T) {
		td, hub, _ := w72World(t, nil)
		w72Ticket(t, hub, "wi_redo_running", pf233TicketBody)
		tk, err := td.tasks.Spawn(task.Spec{Project: "proj", Prompt: "x", AnalysisOf: "wi_redo_running"})
		if err != nil {
			t.Fatalf("spawn the running analysis: %v", err)
		}
		code, body := pf233RedoCall(t, td, "wi_redo_running")
		if code == 200 || !strings.Contains(body, tk.ID) {
			t.Fatalf("a running analysis task %s must refuse a second round naming it, got %d %s", tk.ID, code, body)
		}
	})

	t.Run("pending analysis request", func(t *testing.T) {
		td, hub, _ := w72World(t, func(c *Config) {
			// no dispatch tick may drain the seeded request before the call
			c.DispatchTick = time.Hour
			c.DrainInterval = time.Hour
		})
		w72Ticket(t, hub, "wi_redo_queued", pf233TicketBody)
		td.analysisQ.add(analysisEntry{WorkItemID: "wi_redo_queued", Project: "proj"})
		code, body := pf233RedoCall(t, td, "wi_redo_queued")
		if code == 200 || !strings.Contains(body, "pending analysis request") {
			t.Fatalf("a pending analysis request must refuse a second one, got %d %s", code, body)
		}
	})

}

// TestAnalysisRedoSurvivesRestart (u4, AC2): the request is on disk before
// the ticket changes, so a daemon that dies with the lane full replays the
// same round once — no second archive, no second marker, no lost request.
func TestAnalysisRedoSurvivesRestart(t *testing.T) {
	setGitIdentity(t)
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	data := filepath.Join(dir, "data")
	hubBase := filepath.Join(dir, "hub")
	sock := filepath.Join(dir, "d1.sock")
	for _, d := range []string{ws, data, hubBase} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "develop", "steps:\n  - { id: build }\n")
	inner := newFakeHub()
	hub := &w72Hub{fakeHub: inner, contents: map[string]*string{}}
	bundle := "develop"
	hub.mu.Lock()
	hub.status = map[string]string{"wi_redo_restart": api.StatusQueued}
	hub.bundles = map[string]string{"wi_redo_restart": bundle}
	hub.contents["wi_redo_restart"] = strp(pf233TicketBody)
	hub.mu.Unlock()
	if err := os.WriteFile(filepath.Join(ws, workspace.FileName),
		[]byte("steps:\n  proj:\n    task: { models: [pi:w72/m] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := newW72Runner()
	cfg := e2eConfig(sock, ws, data, hubBase, hub.fakeHub, &fakeSpawner{})
	cfg.WorkflowsDir = wf
	cfg.TaskRunner = runner
	cfg.Hub = hub
	cfg.MaxConcurrent = 8
	d1, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- d1.Run(context.Background()) }()
	waitFor(t, "the first daemon serving", 5*time.Second, func() bool {
		_, serr := cli.NewDaemonClient(cfg.SocketPath).Status()
		return serr == nil
	})
	for i := 0; i < 2; i++ {
		if code, b := stepDo(t, cfg.SocketPath, "POST", "/task/spawn", `{"project":"proj","prompt":"filler"}`); code != 200 {
			t.Fatalf("filler spawn %d = %d %s", i, code, b)
		}
	}
	code, body := pf233RedoCallSock(t, cfg.SocketPath, "wi_redo_restart")
	if code != 200 || !strings.Contains(body, `"queued"`) {
		t.Fatalf("redo under a full lane = %d %s, want queued", code, body)
	}
	var first struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal([]byte(body), &first); err != nil || first.RequestID == "" {
		t.Fatalf("redo answer: %v (%s)", err, body)
	}
	beforeRestart := pf233Content(t, hub, "wi_redo_restart")
	if err := d1.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runDone:
	case <-time.After(15 * time.Second):
		t.Fatal("the first daemon never stopped")
	}
	settleAfterShutdown(t, d1)
	if entries := pf233QueueEntriesFrom(t, data); len(entries) != 1 || entries[0].RedoRequest != first.RequestID {
		t.Fatalf("the redo request must survive the shutdown: %+v", entries)
	}

	runner2 := newW72Runner()
	cfg.TaskRunner = runner2
	cfg.SocketPath = filepath.Join(dir, "d2.sock")
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
	for len(runner2.taskIDs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ids := runner2.taskIDs()
	if len(ids) != 1 {
		t.Fatalf("the restarted daemon must dispatch the redo request exactly once, got %d tasks: %v", len(ids), ids)
	}
	tk, err := d2.tasks.Show(ids[0])
	if err != nil || tk.Spec.AnalysisOf != "wi_redo_restart" {
		t.Fatalf("the replayed task must bind the ticket: %+v %v", tk, err)
	}
	if after := pf233Content(t, hub, "wi_redo_restart"); after != beforeRestart {
		t.Fatalf("a restart must not re-apply the archive or marker:\nbefore:\n%s\nafter:\n%s", beforeRestart, after)
	}
	waitFor(t, "the dispatched round leaving the queue", 5*time.Second, func() bool {
		return len(pf233QueueEntriesFrom(t, data)) == 0
	})
}

// TestAnalysisRedoConcurrentRequests (u4, AC3): two simultaneous redo
// calls accept exactly one; the ticket gains one archive and one pending
// request, the loser gets a conflict naming the pending request.
func TestAnalysisRedoConcurrentRequests(t *testing.T) {
	td, hub, _ := w72World(t, nil)
	w72FillSlots(t, td, 2)
	w72Ticket(t, hub, "wi_redo_race", pf233TicketBody)
	start := make(chan struct{})
	type result struct {
		code int
		body string
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			code, body := pf233RedoCallAlways(t, td.sock, "wi_redo_race")
			results <- result{code: code, body: body}
		}()
	}
	close(start)
	var ok, conflict int
	for i := 0; i < 2; i++ {
		r := <-results
		switch {
		case r.code == 200:
			ok++
		case r.code == 409 && strings.Contains(r.body, "pending analysis request"):
			conflict++
		default:
			t.Fatalf("unexpected answer: %d %s", r.code, r.body)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("two concurrent redos must accept one and refuse one, got %d ok / %d conflict", ok, conflict)
	}
	content := pf233Content(t, hub, "wi_redo_race")
	if n := strings.Count(content, "<!-- pf:analysis-archive "); n != 2 {
		t.Fatalf("the ticket must carry exactly one archive entry (begin+end markers), got %d markers:\n%s", n, content)
	}
	if entries := pf233QueueEntries(t, td); len(entries) != 1 {
		t.Fatalf("one pending redo request, got %+v", entries)
	}
}

// TestAnalysisRedoStaleAutoWaveSkips (u4, AC2/AC3 local guarantee): a wave
// snapshot fetched before the redo CAS carries the old, confirmed text. The
// marker it lacks is on the ticket now, so the local wave must not claim it
// from that stale snapshot — the redo started a new round.
func TestAnalysisRedoStaleAutoWaveSkips(t *testing.T) {
	td, hub, _ := w72World(t, func(c *Config) {
		c.MaxConcurrent = 8
		c.DrainInterval = time.Hour
		c.DispatchTick = time.Hour
	})
	const staleStamp = "2026-11-22T00:00:00Z"
	w72Ticket(t, hub, "wi_redo_stale", pf233TicketBody)
	hub.mu.Lock()
	if hub.updatedAt == nil {
		hub.updatedAt = map[string]string{}
	}
	stale := api.WorkItem{ID: "wi_redo_stale", Slug: "wi_redo_stale", ProjectName: "proj",
		Status: api.StatusQueued, Priority: api.PriorityNormal, BundleName: strp("develop"),
		Content: strp(pf233TicketBody), UpdatedAt: staleStamp}
	hub.updatedAt["wi_redo_stale"] = staleStamp
	hub.queues = []api.WorkItem{stale}
	hub.mu.Unlock()

	if code, body := pf233RedoCall(t, td, "wi_redo_stale"); code != 200 {
		t.Fatalf("redo = %d %s, want 200", code, body)
	}
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}
	td.fetchAutoWave()
	if td.q.IsPending("wi_redo_stale") {
		t.Fatal("the auto-pull wave claimed the ticket from a snapshot taken before the redo; the local redo round must hold it back")
	}
}

// pf233SlowHub blocks GetWorkItem for one ticket and signals when a caller
// reached it; it is the regression seam for the redo route's lock scope.
type pf233SlowHub struct {
	*w72Hub
	wi          string
	entered     chan struct{}
	release     chan struct{}
	once        sync.Once
	releaseOnce sync.Once
}

func (h *pf233SlowHub) GetWorkItem(idOrSlug string) (*api.WorkItem, error) {
	if idOrSlug == h.wi {
		h.once.Do(func() { close(h.entered) })
		<-h.release
	}
	return h.w72Hub.GetWorkItem(idOrSlug)
}

func (h *pf233SlowHub) unblock() { h.releaseOnce.Do(func() { close(h.release) }) }

// pf233Post answers one raw daemon request without t.Fatalf, so a goroutine
// can be bounded by a deadline instead of blocking the test.
func pf233Post(sock, path, body string, timeout time.Duration) (int, string, error) {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
	c := &http.Client{Transport: tr, Timeout: timeout}
	req, err := http.NewRequest("POST", "http://daemon"+path, strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), nil
}

// TestAnalysisRedoSlowHubKeepsAdmissionMoving (round 2, r1#1): a hub read
// or a queue persist in the redo route must never hold drainGate. While the
// route waits on GetWorkItem, a /task/spawn admission still completes — the
// same gate serves submit, the auto wave, every task spawn and the drain
// switch's safe-idle check.
func TestAnalysisRedoSlowHubKeepsAdmissionMoving(t *testing.T) {
	td, hub, _ := w72World(t, func(c *Config) { c.MaxConcurrent = 8 })
	w72Ticket(t, hub, "wi_redo_slowhub", pf233TicketBody)
	slow := &pf233SlowHub{w72Hub: hub, wi: "wi_redo_slowhub", entered: make(chan struct{}), release: make(chan struct{})}
	td.Daemon.hub = slow
	t.Cleanup(slow.unblock)

	type answer struct {
		code int
		body string
		err  error
	}
	redoCh := make(chan answer, 1)
	go func() {
		code, body, err := pf233Post(td.sock, "/analysis/redo",
			`{"work_item_id":"wi_redo_slowhub","project":"proj"}`, 30*time.Second)
		redoCh <- answer{code: code, body: body, err: err}
	}()
	select {
	case <-slow.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the redo request never reached the hub read")
	}

	spawnCh := make(chan answer, 1)
	go func() {
		code, body, err := pf233Post(td.sock, "/task/spawn", `{"project":"proj","prompt":"admission probe"}`, 10*time.Second)
		spawnCh <- answer{code: code, body: body, err: err}
	}()
	select {
	case res := <-spawnCh:
		if res.err != nil || res.code != 200 {
			t.Fatalf("a task spawn must still be admitted while the redo route waits on its hub read, got %d %s (%v)", res.code, res.body, res.err)
		}
	case <-time.After(3 * time.Second):
		slow.unblock()
		t.Fatal("a task spawn waited on drainGate while the redo route was blocked in GetWorkItem: the route holds the gate across hub I/O")
	}

	slow.unblock()
	select {
	case res := <-redoCh:
		if res.err != nil || res.code != 200 {
			t.Fatalf("the redo request must finish after the hub answers, got %d %s (%v)", res.code, res.body, res.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the redo request never finished after the hub was released")
	}
}
