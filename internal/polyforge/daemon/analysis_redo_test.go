package daemon

// analysis_redo_test.go (polyforge#233): an owner's `pf file analysis redo`
// re-schedules a ticket's pre-submit analysis under the current prompt and
// keeps the old text. The route answers only a queued or blocked execution
// ticket on an acceptance lane that no local execution job or analysis task
// holds; every refusal leaves the ticket untouched.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/task"
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
	raw, err := os.ReadFile(filepath.Join(td.cfg.DataDir, "analysis-queue.json"))
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

const pf233TicketBody = "## 背景\n\nthe ticket body\n\n## 分析\n\n### 1. 改动范围\n\n- old draft\n\n## Acceptance\n\n- AC1 x — carried by build\n"

func TestAnalysisRedoArchivesAndQueues(t *testing.T) {
	td, hub, _ := w72World(t, nil)
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
	if res.State != "queued" || res.RequestID == "" {
		t.Fatalf("a redo whose lane cannot take it yet must queue a named request: %+v", res)
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
	if ts := tdTasks(t, td); len(ts) != 0 {
		t.Fatalf("this unit only registers the request; no task may spawn yet: %+v", ts)
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
		td, hub, _ := w72World(t, nil)
		w72Ticket(t, hub, "wi_redo_queued", pf233TicketBody)
		td.analysisQ.add(analysisEntry{WorkItemID: "wi_redo_queued", Project: "proj"})
		code, body := pf233RedoCall(t, td, "wi_redo_queued")
		if code == 200 || !strings.Contains(body, "pending analysis request") {
			t.Fatalf("a pending analysis request must refuse a second one, got %d %s", code, body)
		}
	})

}
