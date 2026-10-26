package daemon

// stepmem_test.go: the step agent's daemon-mediated memory read face —
// the caller's work item comes from its peer (never the request), every
// search is scoped to that work item's project, another project's memory
// is not found, and the routes pass the step-origin guard as reads while
// refusing callers that are no step process.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// stepGet issues one GET over the daemon socket and answers status + body.
func stepGet(t *testing.T, sock, path string) (int, string) {
	t.Helper()
	return stepDo(t, sock, "GET", path, "")
}

// stepDo issues one request over the daemon socket.
func stepDo(t *testing.T, sock, method, path, body string) (int, string) {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}}
	req, err := http.NewRequest(method, "http://daemon"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// stepDaemon is a test daemon whose /step/* callers all resolve to wi.
func stepDaemon(t *testing.T, wi string) *testDaemon {
	t.Helper()
	td := newTestDaemon(t, nil)
	td.submitClaim(t, wi)
	td.stepOriginOf = func(*http.Request) (stepJob, string) { return stepJob{wi: wi, step: "fix", attempt: 2}, "" }
	return td
}

func TestStepMemorySearchScopedToTheJobsProject(t *testing.T) {
	td := stepDaemon(t, "wi_sm1")
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

	code, body := stepGet(t, td.sock, "/step/memories/search?q=sqlite+busy&match=any&type=experience.*&limit=5")
	if code != 200 || !strings.Contains(body, `"mem_1"`) {
		t.Fatalf("search = %d %s", code, body)
	}
	mu.Lock()
	got := paths[len(paths)-1]
	mu.Unlock()
	u, _ := url.Parse(strings.TrimPrefix(got, "GET "))
	q := u.Query()
	if u.Path != "/api/v1/memories/search" || q.Get("project") != "proj" || q.Get("q") != "sqlite busy" || q.Get("match") != "any" || q.Get("type") != "experience.*" || q.Get("limit") != "5" {
		t.Fatalf("forwarded %s", got)
	}
	// a caller naming another project is refused, nothing forwarded
	n := len(paths)
	if code, body := stepGet(t, td.sock, "/step/memories/search?q=x&project=other"); code != 403 || !strings.Contains(body, "DAEMON-403") {
		t.Fatalf("other project = %d %s", code, body)
	}
	if code, _ := stepGet(t, td.sock, "/step/memories/search?q=x&project=proj"); code != 200 {
		t.Fatalf("own project named = %d", code)
	}
	if code, _ := stepGet(t, td.sock, "/step/memories/search"); code != 400 {
		t.Fatalf("no query = %d", code)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != n+1 {
		t.Fatalf("refused searches were forwarded: %v", paths[n:])
	}
}

func TestStepMemoryShowOwnProjectOnly(t *testing.T) {
	td := stepDaemon(t, "wi_sm2")
	td.hub.mu.Lock()
	td.hub.memDo = func(method, path string, body, out any) error {
		switch path {
		case "/api/v1/projects/proj":
			return json.Unmarshal([]byte(`{"id":"prj_own","name":"proj"}`), out)
		case "/api/v1/memories/mem_own":
			return json.Unmarshal([]byte(`{"id":"mem_own","project_id":"prj_own","type":"experience.env","title":"ok"}`), out)
		case "/api/v1/memories/mem_other":
			return json.Unmarshal([]byte(`{"id":"mem_other","project_id":"prj_other","type":"experience.env"}`), out)
		}
		return fmt.Errorf("unexpected %s %s", method, path)
	}
	td.hub.memContent = func(id string) ([]byte, error) { return []byte("body of " + id), nil }
	td.hub.mu.Unlock()
	code, body := stepGet(t, td.sock, "/step/memories/mem_own")
	var doc struct {
		Meta    map[string]any `json:"meta"`
		Content string         `json:"content"`
	}
	if code != 200 || json.Unmarshal([]byte(body), &doc) != nil || doc.Meta["id"] != "mem_own" || doc.Content != "body of mem_own" {
		t.Fatalf("own memory = %d %s", code, body)
	}
	if code, body := stepGet(t, td.sock, "/step/memories/mem_other"); code != 404 || !strings.Contains(body, "MEM-404") {
		t.Fatalf("other project's memory = %d %s", code, body)
	}
}

// memHub scripts the step memory write: stores (idempotently by key)
// or fails with fail while it is non-nil.
type memHub struct {
	mu    sync.Mutex
	fail  error
	posts []map[string]any
	byKey map[string]string
	body  map[string]string // id → stored content
}

// content serves GetMemoryContent from the stored bodies.
func (m *memHub) content(id string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.body[id]; ok {
		return []byte(b), nil
	}
	return nil, &api.Error{Code: api.ErrMemNotFound}
}

func (m *memHub) do(method, path string, body, out any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if method != "POST" || path != "/api/v1/memories" {
		return fmt.Errorf("unexpected %s %s", method, path)
	}
	if m.fail != nil {
		return m.fail
	}
	b := body.(map[string]any)
	m.posts = append(m.posts, b)
	key, _ := b["idempotency_key"].(string)
	if m.byKey == nil {
		m.byKey = map[string]string{}
	}
	id, ok := m.byKey[key]
	if !ok {
		id = fmt.Sprintf("mem_%d", len(m.byKey)+1)
		m.byKey[key] = id
		if m.body == nil {
			m.body = map[string]string{}
		}
		m.body[id] = b["content"].(string)
	}
	return json.Unmarshal([]byte(`{"id":"`+id+`","type":"`+b["type"].(string)+`","visibility":"project"}`), out)
}

func wrapBody(extra string) string {
	return `{"type":"experience.wrap","title":"proj#1: did it","tags":["policy-proposal"],"content":"做了什么\n建议新增条目：x\n"` + extra + `}`
}

func TestStepRememberStoresAttributedAndIdempotent(t *testing.T) {
	td := stepDaemon(t, "wi_sr1")
	mh := &memHub{}
	td.hub.mu.Lock()
	td.hub.memDo = mh.do
	td.hub.memContent = mh.content
	td.hub.mu.Unlock()
	code, body := stepDo(t, td.sock, "POST", "/step/memories", wrapBody(`,"project":"proj","work_item":"wi_sr1"`))
	if code != 200 || !strings.Contains(body, `"id":"mem_1"`) {
		t.Fatalf("store = %d %s", code, body)
	}
	p := mh.posts[0]
	if p["project"] != "proj" || p["work_item_id"] != "wi_sr1" || p["type"] != "experience.wrap" || p["visibility"] != "project" ||
		p["idempotency_key"] != "pf-step-memory:wi_sr1:fix:2" || p["title"] != "proj#1: did it" {
		t.Fatalf("forwarded %+v", p)
	}
	// a retry after a lost answer: same key, the same memory comes back
	if code, body := stepDo(t, td.sock, "POST", "/step/memories", wrapBody("")); code != 200 || !strings.Contains(body, `"id":"mem_1"`) {
		t.Fatalf("retry = %d %s", code, body)
	}
	if len(mh.byKey) != 1 {
		t.Fatalf("a retry stored a second memory: %v", mh.byKey)
	}
	// a second, different summary in the same attempt is refused, not
	// silently answered with the first
	other := `{"type":"experience.wrap","title":"proj#1: another","content":"something else"}`
	if code, body := stepDo(t, td.sock, "POST", "/step/memories", other); code != 409 || !strings.Contains(body, "already stored its summary mem_1") {
		t.Fatalf("different body = %d %s", code, body)
	}
}

func TestStepRememberRefusalsAndOutage(t *testing.T) {
	td := stepDaemon(t, "wi_sr2")
	mh := &memHub{}
	td.hub.mu.Lock()
	td.hub.memDo = mh.do
	td.hub.mu.Unlock()
	for _, tc := range []struct{ body, want string }{
		{`{"type":"experience.debug","title":"t","content":"b"}`, "writes only its experience.wrap summary"},
		{`{"type":"experience.wrap","title":"","content":"b"}`, "a title is required"},
		{`{"type":"experience.wrap","title":"t","content":"` + strings.Repeat("x", 2049) + `"}`, "at most 2048"},
		{`{"type":"experience.wrap","title":"t","content":"token ghp_abcdefghijklmnopqrstuvwxyz0123"}`, "credential value"},
		{wrapBody(`,"project":"other"`), `writes to project`},
		{wrapBody(`,"work_item":"wi_other"`), "attributed to its own work item wi_sr2"},
	} {
		if code, body := stepDo(t, td.sock, "POST", "/step/memories", tc.body); code < 400 || code >= 500 || !strings.Contains(body, tc.want) {
			t.Fatalf("%s: %d %s, want %q", tc.body, code, body, tc.want)
		}
	}
	if len(mh.posts) != 0 {
		t.Fatalf("refused memories were forwarded: %+v", mh.posts)
	}
	// a where-it-lives line is fine
	if code, body := stepDo(t, td.sock, "POST", "/step/memories", `{"type":"experience.wrap","title":"t","content":"the key lives in ~/.config/polyforge/cli.toml (api_key=${PF_API_KEY})"}`); code != 200 {
		t.Fatalf("where-it-lives: %d %s", code, body)
	}
	mh.mu.Lock()
	mh.fail = errors.New("dial tcp: connection refused")
	mh.mu.Unlock()
	if code, body := stepDo(t, td.sock, "POST", "/step/memories", wrapBody("")); code != 503 || !strings.Contains(body, "aihub unreachable, the memory was not stored") {
		t.Fatalf("aihub down: %d %s", code, body)
	}
	td.stepOriginOf = nil // the real resolver: this test process is no step
	if code, body := stepDo(t, td.sock, "POST", "/step/memories", wrapBody("")); code != 403 || !strings.Contains(body, "serves step agents only") {
		t.Fatalf("non-step POST: %d %s", code, body)
	}
}

// TestStepMemoryHelperProcess is not a test: re-executed as a child it
// plays one role of a step process tree (STEPMEM_ROLE):
//
//	relay  — spawns a "step" child with PF_WI_ID=STEPMEM_INNER_WI
//	step   — writes its pid to STEPMEM_PIDFILE, waits for STEPMEM_GO,
//	         spawns the caller (PF_WI_ID=STEPMEM_CALLER_WI and
//	         PF_DAEMON_ID=STEPMEM_CALLER_DAEMON when set)
//	caller — POSTs a wrap memory (or STEPMEM_BODY to STEPMEM_PATH, from
//	         STEPMEM_CWD when set) and prints the answer
//
// Every role relays its child's output.
func TestStepMemoryHelperProcess(t *testing.T) {
	sock := os.Getenv("STEPMEM_SOCK")
	if sock == "" {
		t.Skip("helper process only")
	}
	child := func(role string, env ...string) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestStepMemoryHelperProcess$", "-test.count=1")
		cmd.Env = append(os.Environ(), append([]string{"STEPMEM_ROLE=" + role}, env...)...)
		out, _ := cmd.CombinedOutput()
		fmt.Print(string(out))
	}
	switch os.Getenv("STEPMEM_ROLE") {
	case "relay":
		child("step", "PF_WI_ID="+os.Getenv("STEPMEM_INNER_WI"))
	case "step":
		_ = os.WriteFile(os.Getenv("STEPMEM_PIDFILE"), []byte(strconv.Itoa(os.Getpid())), 0o644)
		for i := 0; i < 500; i++ {
			if _, err := os.Stat(os.Getenv("STEPMEM_GO")); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		var env []string
		if wi := os.Getenv("STEPMEM_CALLER_WI"); wi != "" {
			env = append(env, "PF_WI_ID="+wi)
		}
		if id := os.Getenv("STEPMEM_CALLER_DAEMON"); id != "" {
			env = append(env, "PF_DAEMON_ID="+id)
		}
		child("caller", env...)
	default:
		// STEPMEM_PATH/STEPMEM_BODY pick another step write (default: a
		// wrap memory); STEPMEM_CWD moves the caller elsewhere first
		path, body := "/step/memories", wrapBody("")
		if p := os.Getenv("STEPMEM_PATH"); p != "" {
			path, body = p, os.Getenv("STEPMEM_BODY")
		}
		if dir := os.Getenv("STEPMEM_CWD"); dir != "" {
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}
		}
		code, answer := stepDo(t, sock, "POST", path, body)
		fmt.Printf("STEPMEM %d %s\n", code, answer)
	}
}

// stepTree runs one helper tree: role relay or step at the top, the
// journal callback receiving the top pid, the inner step pid and the
// started_at stamp taken just before the tree starts (as the spawn
// adapter journals it), the caller's output answered.
func stepTree(t *testing.T, td *testDaemon, role string, journal func(top, step int, startedAt string), env ...string) string {
	t.Helper()
	dir := t.TempDir()
	pidFile, goFile := filepath.Join(dir, "pid"), filepath.Join(dir, "go")
	cmd := exec.Command(os.Args[0], "-test.run=^TestStepMemoryHelperProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), append([]string{"STEPMEM_SOCK=" + td.sock, "STEPMEM_ROLE=" + role,
		"STEPMEM_PIDFILE=" + pidFile, "STEPMEM_GO=" + goFile}, env...)...)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	startedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var step int
	for i := 0; i < 500 && step == 0; i++ {
		if raw, err := os.ReadFile(pidFile); err == nil {
			step, _ = strconv.Atoi(string(raw))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if journal != nil {
		journal(cmd.Process.Pid, step, startedAt)
	}
	if err := os.WriteFile(goFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper tree: %v\n%s", err, out.String())
	}
	return out.String()
}

// TestStepRoutesResolveTheSpawnedStep dials the real UDS face from a
// child process tree. The work item is the one whose journaled step
// process is the caller's ancestor — never the caller's own PF_WI_ID —
// matched against any of the job's journal entries; a caller claiming
// another live work item, a tree reaching two jobs' steps, a marked
// process outside every step and an unmarked one are refused.
func TestStepRoutesResolveTheSpawnedStep(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_sm4")
	td.submitClaim(t, "wi_sm5")
	mh := &memHub{}
	td.hub.mu.Lock()
	td.hub.memDo = mh.do
	td.hub.mu.Unlock()
	ji4, _ := td.rt.Info("wi_sm4")
	ji5, _ := td.rt.Info("wi_sm5")
	marks := []string{"PF_WI_ID=wi_sm4", "PF_DAEMON_ID=m-test"}
	// the step's entry is not the newest one (a later dispatch was
	// journaled, e.g. an in-flight spawn): it still resolves
	journalStep := func(_, step int, startedAt string) {
		writeJournal(t, ji4.CapsuleDir, []capsule.SpawnJournalEntry{
			{WorkItem: "wi_sm4", StepID: "fix", SpawnN: 7, PID: step, StartedAt: startedAt},
			{WorkItem: "wi_sm4", StepID: "review", SpawnN: 8, PID: 1 << 22},
		})
	}
	if out := stepTree(t, td, "step", journalStep, marks...); !strings.Contains(out, "STEPMEM 200") {
		t.Fatalf("step child: %s", out)
	}
	mh.mu.Lock()
	if len(mh.posts) != 1 || mh.posts[0]["work_item_id"] != "wi_sm4" || mh.posts[0]["idempotency_key"] != "pf-step-memory:wi_sm4:fix:7" {
		t.Fatalf("resolved write = %+v", mh.posts)
	}
	mh.mu.Unlock()
	if out := stepTree(t, td, "step", journalStep, append(marks, "STEPMEM_CALLER_WI=wi_sm5")...); !strings.Contains(out, "STEPMEM 403") || !strings.Contains(out, "claims PF_WI_ID=wi_sm5") {
		t.Fatalf("a caller claiming another work item: %s", out)
	}
	// a step of wi_sm5 running inside wi_sm4's step tree: both match
	both := func(top, step int, startedAt string) {
		writeJournal(t, ji4.CapsuleDir, []capsule.SpawnJournalEntry{{WorkItem: "wi_sm4", StepID: "fix", SpawnN: 1, PID: top, StartedAt: startedAt}})
		writeJournal(t, ji5.CapsuleDir, []capsule.SpawnJournalEntry{{WorkItem: "wi_sm5", StepID: "fix", SpawnN: 1, PID: step, StartedAt: startedAt}})
	}
	if out := stepTree(t, td, "relay", both, append(marks, "STEPMEM_INNER_WI=wi_sm5")...); !strings.Contains(out, "STEPMEM 403") || !strings.Contains(out, "several work items") {
		t.Fatalf("a tree reaching two steps: %s", out)
	}
	writeJournal(t, ji4.CapsuleDir, nil)
	writeJournal(t, ji5.CapsuleDir, nil)
	if out := stepTree(t, td, "step", nil, marks...); !strings.Contains(out, "STEPMEM 403") || !strings.Contains(out, "no live step process") {
		t.Fatalf("a marked process outside every step: %s", out)
	}
	if out := stepTree(t, td, "step", journalStep); !strings.Contains(out, "STEPMEM 403") || !strings.Contains(out, "serves step agents only") {
		t.Fatalf("unmarked child: %s", out)
	}
}

// TestStepRoutesIdentifyAStepWhoseEnvironmentIsUnreadable pins
// polyforge#234: on darwin the kernel omits an entitled (CS_RESTRICT)
// agent's environment from the guard's kern.procargs2 read, so the
// daemon's own spawned step process shows no PF_WI_ID/PF_DAEMON_ID —
// modeled here by a journaled step process without this daemon's
// markers whose `pf` child (unentitled, readable) carries them. The
// step is identified by its journaled pid and start time; a journal
// line whose start time no longer matches (pid reuse) or carries none
// is not claimed.
func TestStepRoutesIdentifyAStepWhoseEnvironmentIsUnreadable(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_sm6")
	mh := &memHub{}
	td.hub.mu.Lock()
	td.hub.memDo = mh.do
	td.hub.mu.Unlock()
	ji, _ := td.rt.Info("wi_sm6")
	callerMarks := []string{"STEPMEM_CALLER_WI=wi_sm6", "STEPMEM_CALLER_DAEMON=m-test"}
	journal := func(startedAt func(string) string) func(_, step int, stamp string) {
		return func(_, step int, stamp string) {
			writeJournal(t, ji.CapsuleDir, []capsule.SpawnJournalEntry{{WorkItem: "wi_sm6", StepID: "fix", SpawnN: 3, PID: step, StartedAt: startedAt(stamp)}})
		}
	}
	journaled := journal(func(s string) string { return s })
	if out := stepTree(t, td, "step", journaled, callerMarks...); !strings.Contains(out, "STEPMEM 200") {
		t.Fatalf("a step process whose environment is unreadable: %s", out)
	}
	mh.mu.Lock()
	if len(mh.posts) != 1 || mh.posts[0]["idempotency_key"] != "pf-step-memory:wi_sm6:fix:3" {
		t.Fatalf("resolved write = %+v", mh.posts)
	}
	mh.mu.Unlock()
	reused := journal(func(s string) string {
		at, _ := time.Parse(time.RFC3339Nano, s)
		return at.Add(-time.Hour).Format(time.RFC3339Nano)
	})
	if out := stepTree(t, td, "step", reused, callerMarks...); !strings.Contains(out, "STEPMEM 403") || !strings.Contains(out, "no live step process") {
		t.Fatalf("a journal pid now held by another process: %s", out)
	}
	unstamped := journal(func(string) string { return "" })
	if out := stepTree(t, td, "step", unstamped, callerMarks...); !strings.Contains(out, "STEPMEM 403") || !strings.Contains(out, "no live step process") {
		t.Fatalf("a journal line without a start stamp: %s", out)
	}
}

// TestDaemonMainRefusedInsideAStep: `pf daemon` reads the machine key;
// never inside a step.
func TestDaemonMainRefusedInsideAStep(t *testing.T) {
	t.Setenv("PF_WI_ID", "wi_x")
	t.Setenv("PF_DAEMON_ID", "m-x")
	var stderr strings.Builder
	for _, argv := range [][]string{{"--socket", filepath.Join(t.TempDir(), "s")}, {"ensure"}} {
		stderr.Reset()
		if code := Main(argv, nil, io.Discard, &stderr); code != 1 || !strings.Contains(stderr.String(), "refused inside a workflow step") {
			t.Fatalf("Main(%v) = %d %s", argv, code, stderr.String())
		}
	}
}

// writeJournal writes the capsule's spawn journal.
func writeJournal(t *testing.T, capDir string, entries []capsule.SpawnJournalEntry) {
	t.Helper()
	var b strings.Builder
	for _, e := range entries {
		raw, _ := json.Marshal(e)
		b.Write(raw)
		b.WriteString("\n")
	}
	path := filepath.Join(capDir, capsule.DirRuntimeState, capsule.SpawnJournalFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
