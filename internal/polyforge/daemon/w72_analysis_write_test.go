package daemon

// w72_analysis_write_test.go (polyforge#72, u8 — AC3/AC4): the narrow
// write path. A running analysis task — and ONLY one: the ticket comes
// from the task's own record, never from the request — writes its
// current draft into its ticket's `## 分析` section through
// POST /task/analysis-write. Only that section changes (an owner edit
// elsewhere survives, and a stale read is re-read and re-written under
// the aihub content CAS); a section someone else already wrote (「免分析」
// by hand, no auto-analysis marker) is refused with the ticket
// untouched; an ordinary task is refused 403 before anything is written.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// w72WriteProbeSock / w72WriteProbeBody hand the re-exec'd write probe
// its socket, content and markers.
const (
	w72WriteProbeSock = "W72_WRITE_SOCK"
	w72WriteProbeBody = "W72_WRITE_BODY"
)

// TestW72WriteProbeHelper is not a test: re-executed with the analysis
// task's markers by the tests below, it writes one draft through the
// route and prints one machine-readable line.
func TestW72WriteProbeHelper(t *testing.T) {
	sock := os.Getenv(w72WriteProbeSock)
	if sock == "" {
		t.Skip("helper process only: re-executed with the task markers by the write tests")
	}
	code, body := stepDo(t, sock, "POST", "/task/analysis-write",
		`{"content":`+w72JSON(os.Getenv(w72WriteProbeBody))+`}`)
	fmt.Printf("W72WRITE %d %s\n", code, strings.TrimSpace(body))
}

// w72JSON answers the body as a JSON string literal.
func w72JSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// w72ProbeWrite runs one write as the task process of taskID and answers
// (status, response body).
func w72ProbeWrite(t *testing.T, td *testDaemon, taskID, content string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestW72WriteProbeHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		w72WriteProbeSock+"="+td.sock,
		w72WriteProbeBody+"="+content,
		"PF_TASK_ID="+taskID, "PF_DAEMON_ID="+td.cfg.MachineID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the write probe process failed: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "W72WRITE ") {
			continue
		}
		rest := strings.TrimPrefix(line, "W72WRITE ")
		if i := strings.Index(rest, " "); i > 0 {
			var code int
			if _, err := fmt.Sscanf(rest[:i], "%d", &code); err == nil {
				return code, rest[i+1:]
			}
		}
	}
	t.Fatalf("no W72WRITE line in:\n%s", out)
	return 0, ""
}

// w72AnalysisTask enqueues one analysis and waits until the task is
// running (its runner call started). The ticket body is the standard
// one unless body names another.
func w72AnalysisTask(t *testing.T, td *testDaemon, hub *w72Hub, runner *w72Runner, wi string) string {
	return w72AnalysisTaskWithBody(t, td, hub, runner, wi, "## 背景\n\nthe body before the write\n\n## Acceptance\n\n- AC1 x — carried by build\n")
}

func w72AnalysisTaskWithBody(t *testing.T, td *testDaemon, hub *w72Hub, runner *w72Runner, wi, body string) string {
	t.Helper()
	w72Ticket(t, hub, wi, body)
	res := w72Enqueue(t, td, wi)
	if res.State != "scheduled" {
		t.Fatalf("the analysis must schedule at once: %+v", res)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, id := range runner.taskIDs() {
			if id == res.TaskID {
				return res.TaskID
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the analysis task never started")
	return ""
}

func TestW72AnalysisWriteReplacesOnlySection(t *testing.T) {
	td, hub, runner := w72World(t, nil)
	id := w72AnalysisTask(t, td, hub, runner, "wi_w72_write")
	if code, body := w72ProbeWrite(t, td, id, "### 1. 改动范围\n\n- internal/x\n"); code != 200 {
		t.Fatalf("the analysis task's write = %d %s, want 200", code, body)
	}
	hub.mu.Lock()
	after := *hub.contents["wi_w72_write"]
	hub.mu.Unlock()
	if !strings.Contains(after, workflow.AutoAnalysisMarker(id)) {
		t.Fatalf("the written section must carry the task's unconfirmed marker:\n%s", after)
	}
	if !strings.Contains(after, "### 1. 改动范围") {
		t.Fatalf("the draft must be in the section:\n%s", after)
	}
	if !strings.Contains(after, "the body before the write") || !strings.Contains(after, "## Acceptance\n\n- AC1 x — carried by build\n") {
		t.Fatalf("every other section must be untouched:\n%s", after)
	}
}

func TestW72AnalysisWriteRefusedWhenSectionTaken(t *testing.T) {
	td, hub, runner := w72World(t, nil)
	id := w72AnalysisTask(t, td, hub, runner, "wi_w72_taken")
	// the owner hand-wrote the section WHILE the task runs: no
	// auto-analysis marker, real content.
	hub.mu.Lock()
	hub.contents["wi_w72_taken"] = strp("## 背景\n\nx\n\n## 分析\n\n免分析（2026-11-16，owner）\n\n## Acceptance\n\n- AC1 x — carried by build\n")
	hub.mu.Unlock()
	before := *hub.contents["wi_w72_taken"]
	if code, _ := w72ProbeWrite(t, td, id, "### 1. 改动范围\n\n- mine now\n"); code == 200 {
		t.Fatal("a section someone else wrote must refuse the task's write")
	}
	if after := *hub.contents["wi_w72_taken"]; after != before {
		t.Fatalf("the refused write must change nothing:\n%s", after)
	}
}

func TestW72AnalysisWriteCASRetryKeepsOwnerEdits(t *testing.T) {
	td, hub, runner := w72World(t, nil)
	id := w72AnalysisTask(t, td, hub, runner, "wi_w72_cas")
	// the hub's PATCH answers CONTENT_STALE once — and the edit that made
	// it stale changed ANOTHER section, which must survive the retry.
	hub.mu.Lock()
	hub.staleOnce = "wi_w72_cas"
	hub.mu.Unlock()
	if code, body := w72ProbeWrite(t, td, id, "### 1. 改动范围\n\n- draft\n"); code != 200 {
		t.Fatalf("the write must win its CAS retry: %d %s", code, body)
	}
	hub.mu.Lock()
	after := *hub.contents["wi_w72_cas"]
	hub.mu.Unlock()
	if !strings.Contains(after, "owner edited while the task ran") {
		t.Fatalf("the owner's concurrent edit of another section must be kept:\n%s", after)
	}
	if !strings.Contains(after, workflow.AutoAnalysisMarker(id)) || !strings.Contains(after, "### 1. 改动范围") {
		t.Fatalf("the retry must still write the section:\n%s", after)
	}
}

func TestW72NonAnalysisTaskCannotWrite(t *testing.T) {
	td, hub, _ := w72World(t, nil)
	w72Ticket(t, hub, "wi_w72_other", "## 背景\n\nx\n")
	code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"ordinary"}`)
	if code != 200 {
		t.Fatalf("ordinary spawn = %d %s", code, body)
	}
	var tk struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &tk); err != nil || tk.ID == "" {
		t.Fatalf("no task id in %s", body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := td.tasks.Show(tk.ID); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	before := *hub.contents["wi_w72_other"]
	code, body = w72ProbeWrite(t, td, tk.ID, "### 1. 改动范围\n\n- hijack\n")
	if code == 200 {
		t.Fatalf("an ordinary task's write must be refused, got 200 %s", body)
	}
	if !strings.Contains(body, "DAEMON-403") {
		t.Fatalf("the refusal must carry DAEMON-403: %d %s", code, body)
	}
	if after := *hub.contents["wi_w72_other"]; after != before {
		t.Fatalf("the refused write must change nothing:\n%s", after)
	}
}

var _ = api.StatusQueued
