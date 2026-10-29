package daemon

// tasks_test.go: the one-shot task daemon face: spawn /
// list / show / cancel over the real UDS, the two-slot refusal, the named
// missing-key refusal, and the task-peer guard (a task process tree is
// refused every non-GET route, its read face stays open).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/task"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

type blockRunner struct{ report string }

func (b blockRunner) Run(ctx context.Context, _ task.RunSpec, _ func(int64)) (task.RunResult, error) {
	<-ctx.Done()
	return task.RunResult{Report: b.report}, nil
}

type quickRunner struct{}

func (quickRunner) Run(context.Context, task.RunSpec, func(int64)) (task.RunResult, error) {
	return task.RunResult{Report: "done", Tokens: 1}, nil
}

func taskDaemon(t *testing.T, r task.Runner, yaml string) *testDaemon {
	t.Helper()
	td := newTestDaemon(t, func(c *Config) { c.TaskRunner = r })
	if yaml != "" {
		ws, err := td.cfg.Mapping.WorkspaceFor("proj")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, workspace.FileName), []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return td
}

const taskYaml = "steps:\n  proj:\n    task: { models: [pi:t/m] }\n"

func TestTaskSpawnListShowCancel(t *testing.T) {
	td := taskDaemon(t, blockRunner{report: "partial"}, taskYaml)
	code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"look around"}`)
	if code != 200 {
		t.Fatalf("spawn = %d %s", code, body)
	}
	var tk task.Task
	if err := json.Unmarshal([]byte(body), &tk); err != nil || !task.ValidID(tk.ID) || tk.State != task.StateRunning {
		t.Fatalf("spawn answer = %s (%v)", body, err)
	}
	if code, body := stepGet(t, td.sock, "/task/list"); code != 200 || !strings.Contains(body, tk.ID) || strings.Contains(body, "look around") {
		t.Fatalf("list = %d %s", code, body)
	}
	if code, body := stepGet(t, td.sock, "/task/show/"+tk.ID); code != 200 || !strings.Contains(body, tk.ID) {
		t.Fatalf("show = %d %s", code, body)
	}
	if code, _ := stepDo(t, td.sock, "POST", "/task/cancel/"+tk.ID, ""); code != 200 {
		t.Fatalf("cancel = %d", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, body := stepGet(t, td.sock, "/task/show/"+tk.ID)
		if strings.Contains(body, `"interrupted"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancelled task never ended interrupted: %s", body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code, _ := stepGet(t, td.sock, "/task/show/task_ffffffffffffffff"); code != 404 {
		t.Fatalf("unknown task = %d, want 404", code)
	}
}

func TestTaskSpawnRefusals(t *testing.T) {
	td := taskDaemon(t, blockRunner{}, taskYaml)
	for _, tc := range []struct{ body, want string }{
		{`{"project":"proj","prompt":"x","max_duration":"2h"}`, "60m"},
		{`{"project":"proj","prompt":"x","max_tokens":2000000}`, "1000000"},
		{`{"project":"/tmp/x","prompt":"x"}`, "[workspace]"},
	} {
		code, body := stepDo(t, td.sock, "POST", "/task/spawn", tc.body)
		if code < 400 || !strings.Contains(strings.ToLower(body), strings.ToLower(tc.want)) {
			t.Fatalf("%s = %d %s, want a refusal naming %q", tc.body, code, body, tc.want)
		}
	}
	for i := 0; i < task.Limit; i++ {
		if code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"x"}`); code != 200 {
			t.Fatalf("spawn %d = %d %s", i, code, body)
		}
	}
	if code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"x"}`); code != 409 || !strings.Contains(body, "limit") {
		t.Fatalf("third spawn = %d %s", code, body)
	}
}

func TestTaskSpawnNamesTheMissingModelKey(t *testing.T) {
	td := taskDaemon(t, quickRunner{}, "")
	code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"x"}`)
	if code < 400 || !strings.Contains(body, "steps.proj.task.models") || !strings.Contains(body, "steps.proj.default.models") {
		t.Fatalf("spawn = %d %s", code, body)
	}
}

// TestTaskPeerGuardHelperProcess is not a test: re-executed as a child
// carrying the task markers, it issues requests over the socket.
func TestTaskPeerGuardHelperProcess(t *testing.T) {
	sock := os.Getenv("W40_SOCK")
	if sock == "" {
		t.Skip("helper process only")
	}
	for _, rq := range [][2]string{
		{"POST", "/task/spawn"},
		{"POST", "/task/cancel/task_0123456789abcdef"},
		{"POST", "/step/result"},
		{"POST", "/shutdown"},
		{"GET", "/status"},
		{"GET", "/task/list"},
		{"GET", "/task/memories/search?q=x"},
	} {
		code, _ := stepDo(t, sock, rq[0], rq[1], `{"project":"proj","prompt":"x"}`)
		fmt.Printf("W40 %s %s %d\n", rq[0], rq[1], code)
	}
}

func TestTaskPeerIsRefusedEverythingButItsReadFace(t *testing.T) {
	td := taskDaemon(t, quickRunner{}, taskYaml)
	cmd := exec.Command(os.Args[0], "-test.run=^TestTaskPeerGuardHelperProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), "W40_SOCK="+td.sock, "PF_TASK_ID=task_0123456789abcdef", "PF_DAEMON_ID="+td.cfg.MachineID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	got := string(out)
	// Every write is refused — including the step-write allowlist paths
	// (/step/result) and another spawn — with the DAEMON-403 envelope.
	for _, want := range []string{
		"W40 POST /task/spawn 403", "W40 POST /task/cancel/task_0123456789abcdef 403",
		"W40 POST /step/result 403", "W40 POST /shutdown 403",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	// The read face passes the guard. GET /status and GET /task/list are
	// ordinary local reads (200); the memory read answers the route's own
	// 404 because the caller's task is unknown — never the guard's 403.
	for _, want := range []string{"W40 GET /status 200", "W40 GET /task/list 200", "W40 GET /task/memories/search?q=x 404"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestTaskTerminalSummaryIsDeliveredOnceToTheOriginChat(t *testing.T) {
	td := taskDaemon(t, quickRunner{}, taskYaml)
	chat := "chat_0123456789abcdef"
	code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"x","origin_chat":"`+chat+`"}`)
	if code != 200 {
		t.Fatalf("spawn = %d %s", code, body)
	}
	var tk task.Task
	_ = json.Unmarshal([]byte(body), &tk)
	var notices string
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, notices = stepGet(t, td.sock, "/chats/"+chat+"/task-notices")
		if strings.Contains(notices, tk.ID) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(notices, "pf task show "+tk.ID) {
		t.Fatalf("notices = %s", notices)
	}
	if _, other := stepGet(t, td.sock, "/chats/chat_other/task-notices"); strings.Contains(other, tk.ID) {
		t.Fatalf("another chat saw the notice: %s", other)
	}
	if code, _ := stepDo(t, td.sock, "POST", "/chats/"+chat+"/task-notices/ack", `{"ids":["`+tk.ID+`"]}`); code != 200 {
		t.Fatalf("ack = %d", code)
	}
	if _, again := stepGet(t, td.sock, "/chats/"+chat+"/task-notices"); strings.Contains(again, tk.ID) {
		t.Fatalf("acked notice came back: %s", again)
	}
}

// TestW103TaskBrowseHelperProcess is not a test: it is the marked task
// process that sends one browse-shaped memory search through its
// daemon's UDS face.
func TestW103TaskBrowseHelperProcess(t *testing.T) {
	sock := os.Getenv("W103_SOCK")
	if sock == "" {
		t.Skip("helper process only")
	}
	code, _ := stepGet(t, sock, "/task/memories/search?q=needle&after=not-a-cursor&sort=created&limit=5")
	fmt.Printf("W103 GET /task/memories/search %d\n", code)
}

// TestW103TaskSearchForwardsNoBrowseParameters: the task
// proxy shares forwardMemorySearch, so a browse-shaped request a task
// agent may send forwards no cursor and no sort value — aihub sees the
// plain keyword shape and the agent receives the keyword page, never a
// browse page.
func TestW103TaskSearchForwardsNoBrowseParameters(t *testing.T) {
	td := taskDaemon(t, blockRunner{}, taskYaml)
	code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"browse shape"}`)
	if code != 200 {
		t.Fatalf("spawn = %d %s", code, body)
	}
	var tk task.Task
	if err := json.Unmarshal([]byte(body), &tk); err != nil || tk.State != task.StateRunning {
		t.Fatalf("spawn answer: %s (%v)", body, err)
	}
	var mu sync.Mutex
	var paths []string
	td.hub.mu.Lock()
	td.hub.memDo = func(method, path string, body, out any) error {
		mu.Lock()
		paths = append(paths, method+" "+path)
		mu.Unlock()
		return json.Unmarshal([]byte(`{"items":[{"id":"mem_1","type":"experience.debug","title":"t"}]}`), out)
	}
	td.hub.mu.Unlock()

	cmd := exec.Command(os.Args[0], "-test.run=^TestW103TaskBrowseHelperProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), "W103_SOCK="+td.sock, "PF_TASK_ID="+tk.ID, "PF_DAEMON_ID="+td.cfg.MachineID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "W103 GET /task/memories/search 200") {
		t.Fatalf("the task peer's browse-shaped search was not served as the keyword page:\n%s", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) == 0 {
		t.Fatal("nothing was forwarded")
	}
	got := paths[len(paths)-1]
	if strings.Contains(got, "after=") || strings.Contains(got, "sort=") {
		t.Fatalf("the task proxy forwarded a browse parameter: %s", got)
	}
	if !strings.Contains(got, "q=needle") {
		t.Fatalf("the forwarded shape lost the query: %s", got)
	}
}
