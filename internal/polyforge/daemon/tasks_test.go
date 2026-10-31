package daemon

// tasks_test.go: the one-shot task daemon face: spawn /
// list / show / cancel over the real UDS, the two-slot refusal, the named
// missing-key refusal, and the task-peer guard (a task process tree is
// refused every non-GET route, its read face stays open).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
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
	// The list row carries the bounded first-line summary only — no full
	// prompt field, no report.
	if code, body := stepGet(t, td.sock, "/task/list"); code != 200 || !strings.Contains(body, tk.ID) ||
		!strings.Contains(body, `"prompt_summary":"look around"`) || strings.Contains(body, `"prompt":`) ||
		strings.Contains(body, `"report":`) {
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

// TestTaskSpawnResolvesTheReporterOnce: the spawn route stores the
// caller's display name only when the caller id matches a human member of
// the task's project on the include-caller member face; no id (no hub
// read at all), no match, a blank name and a hub failure each store the
// empty snapshot (read as machine) and the spawn still succeeds. Spawn and
// show answers carry neither internal fact.
func TestTaskSpawnResolvesTheReporterOnce(t *testing.T) {
	td := taskDaemon(t, quickRunner{}, taskYaml)
	td.hub.mu.Lock()
	td.hub.contacts = map[string][]api.ProjectMemberContact{
		"proj":  {{ID: "usr_carol", DisplayName: "Carol C"}, {ID: "usr_blank", DisplayName: " "}},
		"other": {{ID: "usr_dana", DisplayName: "Dana"}},
	}
	td.hub.mu.Unlock()
	for _, tc := range []struct {
		name, caller string
		hubErr       error
		want         string
		wantReads    int
	}{
		{"human member", "usr_carol", nil, "Carol C", 1},
		{"no id", "", nil, "", 0},
		{"unresolved id", "usr_missing", nil, "", 1},
		{"another project's member", "usr_dana", nil, "", 1},
		{"blank display name", "usr_blank", nil, "", 1},
		{"hub failure", "usr_carol", errors.New("hub down"), "", 1},
	} {
		td.hub.mu.Lock()
		td.hub.contactsErr, td.hub.contactReads = tc.hubErr, 0
		td.hub.mu.Unlock()
		body := `{"project":"proj","prompt":"x"}`
		if tc.caller != "" {
			body = fmt.Sprintf(`{"project":"proj","prompt":"x","caller_id":%q}`, tc.caller)
		}
		code, answer := stepDo(t, td.sock, "POST", "/task/spawn", body)
		if code != 200 {
			t.Fatalf("%s: spawn = %d %s", tc.name, code, answer)
		}
		if strings.Contains(answer, "caller_id") || strings.Contains(answer, `"reporter"`) {
			t.Fatalf("%s: spawn answer carries an internal reporter fact: %s", tc.name, answer)
		}
		var tk task.Task
		if err := json.Unmarshal([]byte(answer), &tk); err != nil {
			t.Fatal(err)
		}
		stored, err := td.Daemon.tasks.Show(tk.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Reporter != tc.want || stored.CallerID != tc.caller {
			t.Fatalf("%s: stored caller=%q reporter=%q, want %q/%q", tc.name, stored.CallerID, stored.Reporter, tc.caller, tc.want)
		}
		td.hub.mu.Lock()
		reads := td.hub.contactReads
		td.hub.mu.Unlock()
		if reads != tc.wantReads {
			t.Fatalf("%s: %d member-face reads, want %d", tc.name, reads, tc.wantReads)
		}
		if _, show := stepGet(t, td.sock, "/task/show/"+tk.ID); strings.Contains(show, "caller_id") || strings.Contains(show, `"reporter"`) {
			t.Fatalf("%s: show carries an internal reporter fact: %s", tc.name, show)
		}
		waitTaskTerminal(t, td, tk.ID)
	}
}

// TestTaskSpawnReporterResolutionIsBounded: the spawn-time member read
// runs under its own reporterResolveTimeout deadline and honours the
// caller's context, so a hung hub ends as the documented fallback — the
// empty snapshot, read as machine — instead of holding the spawn for the
// client's global timeout.
func TestTaskSpawnReporterResolutionIsBounded(t *testing.T) {
	td := taskDaemon(t, quickRunner{}, taskYaml)
	td.hub.mu.Lock()
	td.hub.contacts = map[string][]api.ProjectMemberContact{"proj": {{ID: "usr_carol", DisplayName: "Carol C"}}}
	td.hub.mu.Unlock()

	// The read carries the bound even when the caller sets no deadline.
	before := time.Now()
	if got := td.Daemon.resolveReporter(context.Background(), "proj", "usr_carol"); got != "Carol C" {
		t.Fatalf("resolveReporter = %q, want Carol C", got)
	}
	td.hub.mu.Lock()
	deadline := td.hub.contactsDeadline
	td.hub.contactsHang = true
	td.hub.mu.Unlock()
	if deadline.IsZero() || deadline.Before(before) || deadline.After(time.Now().Add(reporterResolveTimeout)) {
		t.Fatalf("member read deadline = %v, want within %v of the call", deadline, reporterResolveTimeout)
	}

	// A hung hub answers only when the context ends; the caller's shorter
	// deadline ends it, and the snapshot falls back to empty.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if got := td.Daemon.resolveReporter(ctx, "proj", "usr_carol"); got != "" {
		t.Fatalf("resolveReporter on a hung hub = %q, want the empty snapshot", got)
	}
	if took := time.Since(start); took >= reporterResolveTimeout {
		t.Fatalf("resolveReporter on a hung hub took %v, want the caller's 50ms deadline", took)
	}
}

// waitTaskTerminal polls the manager until the task leaves running (the
// two-slot limit never trips between a test's sequential spawns).
func waitTaskTerminal(t *testing.T, td *testDaemon, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		tk, err := td.Daemon.tasks.Show(id)
		if err == nil && tk.State.Terminal() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s never ended: %+v %v", id, tk, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestTaskListRowCarriesReporterAndReportTail: each /task/list row carries
// the stored reporter snapshot (machine when empty, never re-read from the
// hub) and the bounded last report line of a terminal task ("" while
// running, for an empty report, never the error); neither the full report
// nor the full prompt is on the list.
func TestTaskListRowCarriesReporterAndReportTail(t *testing.T) {
	td := taskDaemon(t, quickRunner{}, taskYaml)
	td.hub.mu.Lock()
	td.hub.contacts = map[string][]api.ProjectMemberContact{"proj": {{ID: "usr_carol", DisplayName: "Carol C"}}}
	td.hub.mu.Unlock()
	code, answer := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"spawned","caller_id":"usr_carol"}`)
	if code != 200 {
		t.Fatalf("spawn = %d %s", code, answer)
	}
	var spawned task.Task
	if err := json.Unmarshal([]byte(answer), &spawned); err != nil {
		t.Fatal(err)
	}
	waitTaskTerminal(t, td, spawned.ID)
	// A rename and a hub outage after spawn change nothing on the list.
	td.hub.mu.Lock()
	td.hub.contacts = map[string][]api.ProjectMemberContact{"proj": {{ID: "usr_carol", DisplayName: "Caroline"}}}
	td.hub.contactsErr = errors.New("hub down")
	td.hub.contactReads = 0
	td.hub.mu.Unlock()

	long := strings.Repeat("a", 150) + strings.Repeat("字", 100)
	longRunes := []rune(long)
	store := task.NewStore(td.cfg.DataDir)
	now := time.Now().UTC()
	seed := func(id string, state task.State, report, errText, reporter string) {
		t.Helper()
		if err := store.Save(task.Task{ID: id, State: state, CreatedAt: now, Report: report, Error: errText,
			Spec: task.Spec{Project: "proj", Prompt: "label " + id + "\nsecret prompt body", MaxDuration: time.Minute, MaxTokens: 5,
				Reporter: reporter}}); err != nil {
			t.Fatal(err)
		}
	}
	seed("task_00000000000000b1", task.StateCompleted, "first secret line\n  short last  \n\n", "", "")
	seed("task_00000000000000b2", task.StateRunning, "running body", "", "Dana")
	seed("task_00000000000000b3", task.StateFailed, "", "boom", "")
	seed("task_00000000000000b4", task.StateCapped, "head\n"+long, "", "")
	seed("task_00000000000000b5", task.StateCompleted, "intro\n```json\n", "", "")

	code, raw := stepGet(t, td.sock, "/task/list")
	if code != 200 {
		t.Fatalf("list = %d %s", code, raw)
	}
	var doc struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	rows := map[string]map[string]any{}
	for _, row := range doc.Tasks {
		rows[row["id"].(string)] = row
	}
	for _, tc := range []struct{ id, reporter, tail string }{
		{spawned.ID, "Carol C", "done"},
		{"task_00000000000000b1", task.ReporterMachine, "short last"},
		{"task_00000000000000b2", "Dana", ""},
		{"task_00000000000000b3", task.ReporterMachine, ""},
		{"task_00000000000000b4", task.ReporterMachine, "…" + string(longRunes[len(longRunes)-199:])},
		{"task_00000000000000b5", task.ReporterMachine, "```json"},
	} {
		row := rows[tc.id]
		if row == nil {
			t.Fatalf("list lacks %s: %s", tc.id, raw)
		}
		if got, ok := row["reporter"].(string); !ok || got != tc.reporter {
			t.Fatalf("%s reporter = %#v, want %q", tc.id, row["reporter"], tc.reporter)
		}
		if got, ok := row["report_tail"].(string); !ok || got != tc.tail {
			t.Fatalf("%s report_tail = %#v, want %q", tc.id, row["report_tail"], tc.tail)
		}
	}
	for _, leak := range []string{"first secret line", "running body", "secret prompt body", `"report":`, `"prompt":`, "caller_id", "Caroline"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("list leaks %q: %s", leak, raw)
		}
	}
	td.hub.mu.Lock()
	reads := td.hub.contactReads
	td.hub.mu.Unlock()
	if reads != 0 {
		t.Fatalf("list read the member face %d times, want none", reads)
	}
}
