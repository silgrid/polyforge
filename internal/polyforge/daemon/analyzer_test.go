package daemon

// analyzer_test.go: the explicit, read-only message analyzer as ONE task
// of the task lane. It never starts on its own; one call
// spawns one task in the message's project with the read-only tool
// profile, the fixed 5m/100000 budget and the human caller as reporter,
// blocks until that task is terminal and answers the structured result.
// There is no analyzer-wide single-flight: the lane's two slots are the
// only bound.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// analyzeRunner is the scripted task runner: it records every RunSpec,
// answers report, and — when release is non-nil — parks until it closes.
type analyzeRunner struct {
	mu      sync.Mutex
	specs   []task.RunSpec
	report  string
	err     error
	release chan struct{}
	entered chan struct{}
}

func (r *analyzeRunner) Run(ctx context.Context, spec task.RunSpec, _ func(int64)) (task.RunResult, error) {
	r.mu.Lock()
	r.specs = append(r.specs, spec)
	release, entered, report, err := r.release, r.entered, r.report, r.err
	r.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return task.RunResult{Report: report}, nil
		}
	}
	if err != nil {
		return task.RunResult{Tokens: 1}, err
	}
	return task.RunResult{Report: report, Tokens: 3}, nil
}

func (r *analyzeRunner) runs() []task.RunSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]task.RunSpec(nil), r.specs...)
}

const analyzeBody = "please review the SECRET-BODY-WORD change from Bob"

// scriptMessages serves the analyzer's message reads from the fake hub and
// records every hub call.
func scriptMessages(td *testDaemon, msgs map[string]api.Message) *[]routeHubCall {
	var mu sync.Mutex
	calls := &[]routeHubCall{}
	td.hub.mu.Lock()
	td.hub.memDo = func(method, path string, body, out any) error {
		mu.Lock()
		*calls = append(*calls, routeHubCall{method: method, path: path, body: fmt.Sprint(body)})
		mu.Unlock()
		if method == "GET" {
			if id, ok := strings.CutPrefix(path, "/api/v1/messages/"); ok {
				if m, ok := msgs[id]; ok {
					raw, _ := json.Marshal(m)
					return json.Unmarshal(raw, out)
				}
			}
		}
		return &api.Error{Code: api.ErrCoreNotFound, Detail: "fakeHub: unscripted " + method + " " + path}
	}
	td.hub.contacts = map[string][]api.ProjectMemberContact{"proj": {{ID: "usr_carol", DisplayName: "Carol C"}}}
	td.hub.mu.Unlock()
	return calls
}

func testMessage(id string) api.Message {
	return api.Message{ID: id, Seq: 7, ConversationID: "conv_1", SenderID: "usr_bob",
		Text: analyzeBody, CreatedAt: "2026-10-25T00:00:00Z"}
}

type analyzeAnswerOrErr struct {
	raw json.RawMessage
	err error
}

func analyzeAsync(td *testDaemon, id string) chan analyzeAnswerOrErr {
	ch := make(chan analyzeAnswerOrErr, 1)
	go func() {
		raw, err := td.client.AnalyzeMessage(id, "usr_carol")
		ch <- analyzeAnswerOrErr{raw: raw, err: err}
	}()
	return ch
}

func waitAnswer(t *testing.T, ch chan analyzeAnswerOrErr) analyzeAnswerOrErr {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(10 * time.Second):
		t.Fatal("the analyze route did not answer")
	}
	return analyzeAnswerOrErr{}
}

func waitEntered(t *testing.T, ch chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("the analysis task never reached its runner")
	}
}

// TestAnalyzerNeverSelfStarts: a running daemon makes no analysis task
// until a human triggers the route.
func TestAnalyzerNeverSelfStarts(t *testing.T) {
	run := &analyzeRunner{report: "x"}
	td := taskDaemon(t, run, taskYaml)
	time.Sleep(150 * time.Millisecond)
	if got := run.runs(); len(got) != 0 {
		t.Fatalf("the analyzer self-started: %v", got)
	}
	if ts, _ := td.tasks.List(); len(ts) != 0 {
		t.Fatalf("tasks exist without a request: %v", ts)
	}
}

// TestAnalyzeRunsOneReadOnlyBudgetedTask: one call is one task in the
// message's project with the read-only profile, the fixed budget, the
// caller as reporter and the neutral first prompt line; it blocks until
// the task ends and answers the structured result only to the caller —
// no aihub write, no job slot, no tree change, no conclusion key.
func TestAnalyzeRunsOneReadOnlyBudgetedTask(t *testing.T) {
	run := &analyzeRunner{
		report:  `{"type":"bug","summary":"File a bug for the crash. Bob reports it.","suggested_actions":["file_ticket","reply"],"reply_draft":"Thanks, filing it."}`,
		release: make(chan struct{}), entered: make(chan struct{}, 4),
	}
	td := taskDaemon(t, run, taskYaml)
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	m := testMessage("msg_1")
	m.AboutWI = "wi_anchor"
	calls := scriptMessages(td, map[string]api.Message{"msg_1": m})
	treeBefore := routeTree(t, ws, td.cfg.GitRemoteBase)
	slotsBefore := routeJobSlots(td)

	ans := analyzeAsync(td, "msg_1")
	waitEntered(t, run.entered)
	// The call blocks while the task runs: no early answer.
	select {
	case a := <-ans:
		t.Fatalf("the analyze call answered before the task ended: %s %v", a.raw, a.err)
	case <-time.After(100 * time.Millisecond):
	}
	ts, err := td.tasks.List()
	if err != nil || len(ts) != 1 {
		t.Fatalf("tasks = %v %v, want exactly one", ts, err)
	}
	tk := ts[0]
	if tk.Project != "proj" || !tk.ReadOnlyTools || tk.MaxDuration != 5*time.Minute || tk.MaxTokens != 100000 ||
		tk.Reporter != "Carol C" || tk.CallerID != "usr_carol" || tk.State != task.StateRunning {
		t.Fatalf("analysis task = %+v", tk.Spec)
	}
	if got := task.PromptSummary(tk.Prompt); got != "Analyze message msg_1" {
		t.Fatalf("prompt_summary = %q, want the neutral label", got)
	}
	for _, leak := range []string{"SECRET-BODY-WORD", "usr_bob", "wi_anchor", "review"} {
		if strings.Contains(task.PromptSummary(tk.Prompt), leak) {
			t.Fatalf("prompt_summary leaks %q", leak)
		}
	}
	if !strings.Contains(tk.Prompt, analyzeBody) {
		t.Fatal("the full prompt must carry the message body (readable through pf task show)")
	}
	if specs := run.runs(); len(specs) != 1 || !specs[0].ReadOnlyTools || specs[0].Candidate.String() != "pi:t/m" {
		t.Fatalf("runner specs = %+v", specs)
	}
	close(run.release)
	a := waitAnswer(t, ans)
	if a.err != nil {
		t.Fatalf("analyze: %v", a.err)
	}
	var generic map[string]any
	if err := json.Unmarshal(a.raw, &generic); err != nil {
		t.Fatal(err)
	}
	if _, ok := generic["conclusion"]; ok {
		t.Fatalf("the answer carries the retired conclusion key: %s", a.raw)
	}
	var res api.AnalyzeResult
	if err := json.Unmarshal(a.raw, &res); err != nil {
		t.Fatal(err)
	}
	want := api.AnalyzeResult{MessageID: "msg_1", TaskID: tk.ID, Project: "proj", Type: "bug",
		Summary: "File a bug for the crash. Bob reports it.", SuggestedActions: []string{"file_ticket", "reply"},
		Model: "pi:t/m", Duration: res.Duration, ReplyDraft: "Thanks, filing it."}
	if !reflect.DeepEqual(res, want) || res.Duration == "" {
		t.Fatalf("answer = %+v, want %+v", res, want)
	}
	if _, err := time.ParseDuration(res.Duration); err != nil {
		t.Fatalf("duration %q is not a Go duration: %v", res.Duration, err)
	}
	done, err := td.tasks.Show(tk.ID)
	if err != nil || done.State != task.StateCompleted {
		t.Fatalf("the task record = %+v %v", done, err)
	}
	for _, c := range *calls {
		if c.method != "GET" {
			t.Fatalf("the analyzer wrote to aihub: %+v", c)
		}
	}
	if slots := routeJobSlots(td); slots != slotsBefore {
		t.Fatalf("the analyzer changed the job queue: %v -> %v", slotsBefore, slots)
	}
	if tree := routeTree(t, ws, td.cfg.GitRemoteBase); !reflect.DeepEqual(tree, treeBefore) {
		t.Fatalf("the analyzer changed the workspace/repo tree")
	}
}

// TestAnalyzeUnanchoredUsesFirstMappedProject: a message with no
// work-item anchor runs in the first mapped project.
func TestAnalyzeUnanchoredUsesFirstMappedProject(t *testing.T) {
	run := &analyzeRunner{report: "plain words"}
	td := taskDaemon(t, run, taskYaml)
	scriptMessages(td, map[string]api.Message{"msg_2": testMessage("msg_2")})
	a := waitAnswer(t, analyzeAsync(td, "msg_2"))
	if a.err != nil {
		t.Fatal(a.err)
	}
	var res api.AnalyzeResult
	_ = json.Unmarshal(a.raw, &res)
	if res.Project != "proj" || res.Type != "other" || res.Summary != "plain words" ||
		res.SuggestedActions == nil || len(res.SuggestedActions) != 0 || res.ReplyDraft != "" {
		t.Fatalf("answer = %+v", res)
	}
	if !strings.Contains(string(a.raw), `"suggested_actions":[]`) {
		t.Fatalf("suggested_actions must be an array, never null: %s", a.raw)
	}
}

// TestAnalyzeTwoRunAtOnceAndSlotFullRefuses: no single-flight — a second
// analysis spawns while the first runs; with both slots busy a third is
// the retryable DAEMON-409 and creates no task.
func TestAnalyzeTwoRunAtOnceAndSlotFullRefuses(t *testing.T) {
	run := &analyzeRunner{report: `{"type":"discussion","summary":"Nothing to do."}`,
		release: make(chan struct{}), entered: make(chan struct{}, 4)}
	td := taskDaemon(t, run, taskYaml)
	scriptMessages(td, map[string]api.Message{"msg_a": testMessage("msg_a"), "msg_b": testMessage("msg_b"), "msg_c": testMessage("msg_c")})
	first := analyzeAsync(td, "msg_a")
	waitEntered(t, run.entered)
	second := analyzeAsync(td, "msg_b")
	waitEntered(t, run.entered)
	_, err := td.client.AnalyzeMessage("msg_c", "usr_carol")
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "nothing is queued") {
		t.Fatalf("slot-full analyze = %v, want the DAEMON-409 busy refusal", err)
	}
	if ts, _ := td.tasks.List(); len(ts) != 2 {
		t.Fatalf("tasks after a refused spawn = %d, want 2", len(ts))
	}
	close(run.release)
	for _, ch := range []chan analyzeAnswerOrErr{first, second} {
		if a := waitAnswer(t, ch); a.err != nil {
			t.Fatalf("concurrent analysis: %v", a.err)
		}
	}
}

// TestAnalyzePreconditionRefusals: a missing task chain or an unmapped
// project is a typed refusal before any task.
func TestAnalyzePreconditionRefusals(t *testing.T) {
	run := &analyzeRunner{report: "x"}
	td := taskDaemon(t, run, "")
	scriptMessages(td, map[string]api.Message{"msg_1": testMessage("msg_1")})
	_, err := td.client.AnalyzeMessage("msg_1", "")
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonBadInput || !strings.Contains(ae.Detail, "steps.proj.task.models") {
		t.Fatalf("no task chain = %v, want DAEMON-400 naming steps.proj.task.models", err)
	}
	if ts, _ := td.tasks.List(); len(ts) != 0 || len(run.runs()) != 0 {
		t.Fatalf("a refused analysis created a task: %v", ts)
	}

	run2 := &analyzeRunner{report: "x"}
	td2 := newTestDaemon(t, func(c *Config) {
		c.TaskRunner = run2
		ws, _ := c.Mapping.WorkspaceFor("proj")
		c.Mapping = mappingFor(ws, "other")
	})
	m := testMessage("msg_1")
	m.AboutWI = "wi_elsewhere" // the fake hub answers project "proj", unmapped here
	scriptMessages(td2, map[string]api.Message{"msg_1": m})
	_, err = td2.client.AnalyzeMessage("msg_1", "")
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonUnmappedProject {
		t.Fatalf("unmapped project = %v, want DAEMON-412", err)
	}
	if ts, _ := td2.tasks.List(); len(ts) != 0 || len(run2.runs()) != 0 {
		t.Fatalf("a refused analysis created a task: %v", ts)
	}
}

// TestAnalyzeTerminalFailureIsTyped: a task that ends failed answers the
// typed DAEMON-500 naming the state and no structured result.
func TestAnalyzeTerminalFailureIsTyped(t *testing.T) {
	run := &analyzeRunner{err: errors.New("model exploded")}
	td := taskDaemon(t, run, taskYaml)
	scriptMessages(td, map[string]api.Message{"msg_1": testMessage("msg_1")})
	a := waitAnswer(t, analyzeAsync(td, "msg_1"))
	var ae *api.Error
	if !errors.As(a.err, &ae) || ae.Code != api.ErrDaemonInternal || !strings.Contains(ae.Detail, "ended failed") ||
		!strings.Contains(ae.Detail, "pf task show task_") {
		t.Fatalf("failed task = %s %v, want DAEMON-500 naming the state", a.raw, a.err)
	}
	if strings.Contains(ae.Detail, `"type"`) {
		t.Fatalf("a failure carries a structured result: %s", ae.Detail)
	}
}

// TestAnalyzeAnswerTerminalStates: capped and interrupted (and a completed
// task with nothing in its report) are the typed failure, never a result.
func TestAnalyzeAnswerTerminalStates(t *testing.T) {
	for _, st := range []task.State{task.StateCapped, task.StateInterrupted, task.StateFailed} {
		_, err := analyzeAnswer("msg_1", task.Task{ID: "task_0123456789abcdef", State: st, Report: `{"type":"bug","summary":"partial"}`})
		if err == nil || !strings.Contains(err.Error(), "ended "+string(st)) {
			t.Fatalf("%s: err = %v", st, err)
		}
	}
	if _, err := analyzeAnswer("msg_1", task.Task{ID: "task_0123456789abcdef", State: task.StateCompleted, Report: "  \n"}); err == nil {
		t.Fatal("an empty report must not fabricate a result")
	}
	res, err := analyzeAnswer("msg_1", task.Task{ID: "task_0123456789abcdef", Spec: task.Spec{Project: "proj"},
		State: task.StateCompleted, Candidate: "claude:opus", WallConsumed: 62340 * time.Millisecond,
		Report: `{"type":"ruling","summary":"Adopt option B."}`})
	if err != nil || res.Duration != "1m2.3s" || res.Model != "claude:opus" || res.Type != "ruling" || res.TaskID != "task_0123456789abcdef" {
		t.Fatalf("answer = %+v %v", res, err)
	}
}

// TestParseAnalyzeAnswerTolerance pins the tolerant parse and its
// degradation rules.
func TestParseAnalyzeAnswerTolerance(t *testing.T) {
	type out struct {
		typ, summary string
		actions      []string
		draft        string
	}
	for _, tc := range []struct {
		name, report string
		want         out
	}{
		{"whole json", `{"type":"requirement","summary":"Add X.","suggested_actions":["file_ticket"]}`,
			out{"requirement", "Add X.", []string{"file_ticket"}, ""}},
		{"fenced block", "Here you go:\n```json\n{\"type\":\"clarification\",\"summary\":\"Ask which env.\",\"suggested_actions\":[\"reply\"],\"reply_draft\":\"Which env?\"}\n```\n",
			out{"clarification", "Ask which env.", []string{"reply"}, "Which env?"}},
		{"object inside prose", `I think: {"type":"outreach","summary":"Say hi."} done.`,
			out{"outreach", "Say hi.", []string{}, ""}},
		{"plain text", "  just some words  ", out{"other", "just some words", []string{}, ""}},
		{"invalid json", `{"type": "bug", "summary": `, out{"other", `{"type": "bug", "summary":`, []string{}, ""}},
		{"unknown type", `{"type":"feature","summary":"S."}`, out{"other", `{"type":"feature","summary":"S."}`, []string{}, ""}},
		{"missing type", `{"summary":"S."}`, out{"other", `{"summary":"S."}`, []string{}, ""}},
		{"valid type, blank summary", `{"type":"bug","summary":"  "}`, out{"other", `{"type":"bug","summary":"  "}`, []string{}, ""}},
		{"valid type, non-string summary", `{"type":"bug","summary":3}`, out{"other", `{"type":"bug","summary":3}`, []string{}, ""}},
		{"unknown and duplicate actions dropped", `{"type":"bug","summary":"S.","suggested_actions":["reply","escalate","reply",7,"note_only"]}`,
			out{"bug", "S.", []string{"reply", "note_only"}, ""}},
		{"non-array actions", `{"type":"bug","summary":"S.","suggested_actions":"reply"}`, out{"bug", "S.", []string{}, ""}},
		{"draft without reply dropped", `{"type":"bug","summary":"S.","suggested_actions":["file_ticket"],"reply_draft":"hi"}`,
			out{"bug", "S.", []string{"file_ticket"}, ""}},
		{"blank draft dropped", `{"type":"bug","summary":"S.","suggested_actions":["reply"],"reply_draft":"  "}`,
			out{"bug", "S.", []string{"reply"}, ""}},
		{"non-string draft dropped", `{"type":"bug","summary":"S.","suggested_actions":["reply"],"reply_draft":{"x":1}}`,
			out{"bug", "S.", []string{"reply"}, ""}},
	} {
		typ, summary, actions, draft := parseAnalyzeAnswer(tc.report)
		got := out{typ, summary, actions, draft}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// TestAnalyzerPromptFirstLineIsNeutral: the first non-empty line is the
// label plus the id and nothing from the message, its sender or its
// work item; the closed vocabularies are spelled out below it.
func TestAnalyzerPromptFirstLineIsNeutral(t *testing.T) {
	m := testMessage("msg_9")
	m.AboutWI = "wi_ctx"
	p := analyzerPrompt(m, &api.WorkItem{ID: "wi_ctx", Slug: "proj#3", Goal: "GOAL-WORD"}, &api.Message{ID: "msg_8", SenderID: "usr_dan", Text: "REPLY-WORD"})
	first, _, _ := strings.Cut(p, "\n")
	if first != "Analyze message msg_9" {
		t.Fatalf("first line = %q", first)
	}
	for _, want := range append(append([]string{analyzeBody, "GOAL-WORD", "REPLY-WORD"}, api.AnalyzeTypes...), api.AnalyzeActions...) {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q", want)
		}
	}
}

// routeHubCall is one recorded fake-hub call.
type routeHubCall struct {
	method string
	path   string
	body   string
}

// routeJobSlots is (pending, inflight, capacity, live jobs).
func routeJobSlots(td *testDaemon) [4]int {
	td.q.mu.Lock()
	pending, inflight := len(td.q.pending), len(td.q.inflight)
	td.q.mu.Unlock()
	return [4]int{pending, inflight, td.q.MaxConcurrent(), len(td.rt.List())}
}

// routeTree snapshots names, modes and bytes under every root.
func routeTree(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if d.IsDir() {
				out[path] = info.Mode().String() + "/"
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] = info.Mode().String() + ":" + string(raw)
			return nil
		})
		if err != nil {
			t.Fatalf("snapshot %s: %v", root, err)
		}
	}
	return out
}

// TestAnalyzerEscapesMessageIDs pins B3: both message ids the analyzer
// puts in an aihub path (the route's and the fetched message's reply_to)
// are one percent-encoded segment, so a `?`, `#` or `/` inside an id can
// never rewrite the query or the route aihub sees.
func TestAnalyzerEscapesMessageIDs(t *testing.T) {
	td := newTestDaemon(t, nil)
	var mu sync.Mutex
	var paths []string
	td.hub.mu.Lock()
	td.hub.memDo = func(method, path string, _, out any) error {
		mu.Lock()
		paths = append(paths, path)
		mu.Unlock()
		if m, ok := out.(*api.Message); ok && len(paths) == 1 {
			m.ReplyTo = "re/p?x=1#y"
		}
		return nil
	}
	td.hub.mu.Unlock()
	req := httptest.NewRequest("POST", "/messages/x/analyze", nil)
	req.SetPathValue("id", "m/1?all=1#frag")
	td.analyzeRoute(httptest.NewRecorder(), req)
	mu.Lock()
	defer mu.Unlock()
	want := []string{"/api/v1/messages/m%2F1%3Fall=1%23frag", "/api/v1/messages/re%2Fp%3Fx=1%23y"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("hub request paths = %q, want %q", paths, want)
	}
}
