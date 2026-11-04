package daemon

// prompter_test.go: the explicit, read-only message-to-prompt task. One
// call spawns one task in the anchored work item's project (else the first
// mapped project) with the read-only profile and the fixed 5m/100000
// budget, whose prompt carries exactly the clicked message, at most the 10
// most recent earlier messages of its conversation in order, the replied-to
// message and the anchored work item; it blocks until the task is terminal
// and answers the four-field {prompt, task_id, model, duration} shape, or a
// typed failure without prompt text.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// promptFixture is the one wire shape both sides pin: pi-web's sanitizer
// test carries the same four keys.
const promptFixture = `{"prompt":"Fix the login timeout in the session store.","task_id":"task_0123456789abcdef","model":"pi:t/m","duration":"1m2.3s"}`

// promptHub scripts the fake hub for the prompter: single-message reads
// from msgs, conversation pages from the stored history (the hub's
// before/limit semantics), refusals from denied, and records every call.
type promptHub struct {
	mu      sync.Mutex
	calls   []routeHubCall
	msgs    map[string]api.Message
	history map[string][]api.Message // conversation id → every message
	denied  map[string]bool          // a hub path prefix answered CORE-403
}

func scriptPrompt(td *testDaemon, ph *promptHub) {
	td.hub.mu.Lock()
	defer td.hub.mu.Unlock()
	td.hub.contacts = map[string][]api.ProjectMemberContact{"proj": {{ID: "usr_carol", DisplayName: "Carol C"}}}
	td.hub.memDo = func(method, path string, body, out any) error {
		ph.mu.Lock()
		defer ph.mu.Unlock()
		ph.calls = append(ph.calls, routeHubCall{method: method, path: path, body: fmt.Sprint(body)})
		for prefix := range ph.denied {
			if strings.HasPrefix(path, prefix) {
				return &api.Error{Code: api.ErrCoreForbidden, Detail: "not a member of this conversation"}
			}
		}
		if method != "GET" {
			return &api.Error{Code: api.ErrCoreNotFound, Detail: "unscripted " + method}
		}
		answer := func(v any) error {
			raw, _ := json.Marshal(v)
			return json.Unmarshal(raw, out)
		}
		if id, ok := strings.CutPrefix(path, "/api/v1/messages/"); ok {
			if m, ok := ph.msgs[id]; ok {
				return answer(m)
			}
		}
		if rest, ok := strings.CutPrefix(path, "/api/v1/conversations/"); ok {
			conv, query, _ := strings.Cut(rest, "/messages?")
			q, _ := url.ParseQuery(query)
			before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
			limit, _ := strconv.Atoi(q.Get("limit"))
			var below []api.Message
			for _, m := range ph.history[conv] {
				if m.Seq < before {
					below = append(below, m)
				}
			}
			sort.Slice(below, func(i, j int) bool { return below[i].Seq < below[j].Seq })
			if limit > 0 && len(below) > limit {
				below = below[len(below)-limit:]
			}
			return answer(api.MessagePage{Messages: append([]api.Message{}, below...)})
		}
		return &api.Error{Code: api.ErrCoreNotFound, Detail: "unscripted " + path}
	}
}

// promptConversation is conversation conv_1 with seq 1..n, each message
// text "EARLY-<seq>", plus a later message and a foreign conversation.
func promptConversation(n int) *promptHub {
	ph := &promptHub{msgs: map[string]api.Message{}, history: map[string][]api.Message{}}
	for i := 1; i <= n; i++ {
		m := api.Message{ID: fmt.Sprintf("msg_e%d", i), Seq: int64(i), ConversationID: "conv_1", SenderID: "usr_bob",
			Text: fmt.Sprintf("EARLY-%02d", i), CreatedAt: "2026-10-25T00:00:00Z"}
		ph.history["conv_1"] = append(ph.history["conv_1"], m)
		ph.msgs[m.ID] = m
	}
	clicked := api.Message{ID: "msg_click", Seq: int64(n + 1), ConversationID: "conv_1", SenderID: "usr_bob",
		Text: analyzeBody, CreatedAt: "2026-10-25T00:01:00Z"}
	later := api.Message{ID: "msg_later", Seq: int64(n + 2), ConversationID: "conv_1", SenderID: "usr_carol",
		Text: "LATER-WORD", CreatedAt: "2026-10-25T00:02:00Z"}
	foreign := api.Message{ID: "msg_foreign", Seq: 1, ConversationID: "conv_2", SenderID: "usr_dan",
		Text: "FOREIGN-WORD", CreatedAt: "2026-10-25T00:00:00Z"}
	ph.history["conv_1"] = append(ph.history["conv_1"], clicked, later)
	ph.history["conv_2"] = []api.Message{foreign}
	for _, m := range []api.Message{clicked, later, foreign} {
		ph.msgs[m.ID] = m
	}
	return ph
}

func promptCall(t *testing.T, td *testDaemon, id string) (int, string) {
	t.Helper()
	return stepDo(t, td.sock, "POST", "/messages/"+url.PathEscape(id)+"/prompt", `{"caller_id":"usr_carol"}`)
}

func promptAsync(t *testing.T, td *testDaemon, id string) chan [2]string {
	ch := make(chan [2]string, 1)
	go func() {
		code, body := promptCall(t, td, id)
		ch <- [2]string{strconv.Itoa(code), body}
	}()
	return ch
}

func promptWait(t *testing.T, ch chan [2]string) (int, string) {
	t.Helper()
	select {
	case a := <-ch:
		code, _ := strconv.Atoi(a[0])
		return code, a[1]
	case <-time.After(10 * time.Second):
		t.Fatal("the prompt route did not answer")
	}
	return 0, ""
}

func typedErr(t *testing.T, body string) api.Error {
	t.Helper()
	var ae api.Error
	if err := json.Unmarshal([]byte(body), &ae); err != nil || ae.Code == "" {
		t.Fatalf("not a typed error: %s (%v)", body, err)
	}
	return ae
}

// TestPromptRunsOneReadOnlyBudgetedTask: one call is one task in the
// anchored work item's project with the read-only profile, the fixed
// budget, the caller as reporter and the neutral first prompt line; the
// prompt carries the clicked message, the 10 most recent earlier messages
// in order, the replied-to message and the anchor — nothing later or
// foreign; the answer is exactly the four-field shape.
func TestPromptRunsOneReadOnlyBudgetedTask(t *testing.T) {
	run := &analyzeRunner{report: "\n  Fix the login timeout in the session store.\nKeep the API stable.  \n",
		release: make(chan struct{}), entered: make(chan struct{}, 4)}
	td := taskDaemon(t, run, taskYaml)
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	ph := promptConversation(12)
	clicked := ph.msgs["msg_click"]
	clicked.AboutWI = "wi_anchor"
	clicked.ReplyTo = "msg_e1"
	ph.msgs["msg_click"] = clicked
	scriptPrompt(td, ph)
	treeBefore := routeTree(t, ws, td.cfg.GitRemoteBase)
	slotsBefore := routeJobSlots(td)

	ans := promptAsync(t, td, "msg_click")
	waitEntered(t, run.entered)
	select {
	case a := <-ans:
		t.Fatalf("the prompt call answered before the task ended: %v", a)
	case <-time.After(100 * time.Millisecond):
	}
	ts, err := td.tasks.List()
	if err != nil || len(ts) != 1 {
		t.Fatalf("tasks = %v %v, want exactly one", ts, err)
	}
	tk := ts[0]
	if tk.Project != "proj" || !tk.ReadOnlyTools || tk.MaxDuration != 5*time.Minute || tk.MaxTokens != 100000 ||
		tk.Reporter != "Carol C" || tk.CallerID != "usr_carol" || tk.State != task.StateRunning {
		t.Fatalf("prompt task = %+v", tk.Spec)
	}
	if got := task.PromptSummary(tk.Prompt); got != "Turn message msg_click into an execution prompt" {
		t.Fatalf("prompt_summary = %q, want the neutral label", got)
	}
	for _, leak := range []string{"SECRET-BODY-WORD", "usr_bob", "wi_anchor", "EARLY-"} {
		if strings.Contains(task.PromptSummary(tk.Prompt), leak) {
			t.Fatalf("prompt_summary leaks %q", leak)
		}
	}
	// Scope: EARLY-03..EARLY-12 in ascending order, EARLY-02 absent
	// (EARLY-01 is present only as the replied-to message).
	last := -1
	for i := 3; i <= 12; i++ {
		at := strings.Index(tk.Prompt, fmt.Sprintf("EARLY-%02d", i))
		if at < 0 || at <= last {
			t.Fatalf("earlier message %d missing or out of order in:\n%s", i, tk.Prompt)
		}
		last = at
	}
	for _, absent := range []string{"EARLY-02", "LATER-WORD", "FOREIGN-WORD", "msg_later", "msg_foreign"} {
		if strings.Contains(tk.Prompt, absent) {
			t.Fatalf("the prompt carries %q outside its scope:\n%s", absent, tk.Prompt)
		}
	}
	if strings.Count(tk.Prompt, "EARLY-01") != 1 || !strings.Contains(tk.Prompt, "replied-to message msg_e1") {
		t.Fatalf("the replied-to message is not carried once:\n%s", tk.Prompt)
	}
	for _, want := range []string{analyzeBody, "anchored work item: ", "wi_anchor", "untrusted", "language of the clicked message", "self-contained", "prompt text only"} {
		if !strings.Contains(tk.Prompt, want) {
			t.Fatalf("the prompt lacks %q:\n%s", want, tk.Prompt)
		}
	}
	if specs := run.runs(); len(specs) != 1 || !specs[0].ReadOnlyTools {
		t.Fatalf("runner specs = %+v", specs)
	}
	close(run.release)
	code, body := promptWait(t, ans)
	if code != 200 {
		t.Fatalf("prompt = %d %s", code, body)
	}
	var generic map[string]any
	if err := json.Unmarshal([]byte(body), &generic); err != nil {
		t.Fatal(err)
	}
	var fixture map[string]any
	_ = json.Unmarshal([]byte(promptFixture), &fixture)
	if keys(generic) != keys(fixture) {
		t.Fatalf("answer keys %s, want the fixture's %s: %s", keys(generic), keys(fixture), body)
	}
	var res api.MessagePromptResult
	_ = json.Unmarshal([]byte(body), &res)
	want := api.MessagePromptResult{Prompt: "Fix the login timeout in the session store.\nKeep the API stable.",
		TaskID: tk.ID, Model: "pi:t/m", Duration: res.Duration}
	if res != want {
		t.Fatalf("answer = %+v, want %+v", res, want)
	}
	if _, err := time.ParseDuration(res.Duration); err != nil {
		t.Fatalf("duration %q is not a Go duration: %v", res.Duration, err)
	}
	if done, err := td.tasks.Show(tk.ID); err != nil || done.State != task.StateCompleted {
		t.Fatalf("the task record = %+v %v", done, err)
	}
	ph.mu.Lock()
	for _, c := range ph.calls {
		if c.method != "GET" {
			t.Fatalf("the prompter wrote to aihub: %+v", c)
		}
		if strings.Contains(c.path, "conv_2") {
			t.Fatalf("the prompter read another conversation: %+v", c)
		}
	}
	ph.mu.Unlock()
	if slots := routeJobSlots(td); slots != slotsBefore {
		t.Fatalf("the prompter changed the job queue: %v -> %v", slotsBefore, slots)
	}
	if tree := routeTree(t, ws, td.cfg.GitRemoteBase); !reflect.DeepEqual(tree, treeBefore) {
		t.Fatalf("the prompter changed the workspace/repo tree")
	}
}

func keys(m map[string]any) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

// TestPromptFixtureShape: the answer type marshals to exactly the shared
// fixture's keys, and the fixture round-trips through it unchanged.
func TestPromptFixtureShape(t *testing.T) {
	var res api.MessagePromptResult
	if err := json.Unmarshal([]byte(promptFixture), &res); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(res)
	if string(raw) != promptFixture {
		t.Fatalf("round trip = %s, want %s", raw, promptFixture)
	}
}

// TestPromptFewerEarlierUnanchored: fewer than 10 earlier messages are all
// carried, the first message has none, and an unanchored message runs in
// the first mapped project.
func TestPromptFewerEarlierUnanchored(t *testing.T) {
	run := &analyzeRunner{report: "do the thing"}
	td := taskDaemon(t, run, taskYaml)
	ph := promptConversation(3)
	scriptPrompt(td, ph)
	if code, body := promptCall(t, td, "msg_click"); code != 200 {
		t.Fatalf("prompt = %d %s", code, body)
	}
	ts, _ := td.tasks.List()
	if len(ts) != 1 || ts[0].Project != "proj" {
		t.Fatalf("tasks = %+v", ts)
	}
	full, _ := td.tasks.Show(ts[0].ID)
	for _, want := range []string{"EARLY-01", "EARLY-02", "EARLY-03"} {
		if !strings.Contains(full.Prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, full.Prompt)
		}
	}
	if strings.Contains(full.Prompt, "anchored work item:") || strings.Contains(full.Prompt, "replied-to message msg_") {
		t.Fatalf("an unanchored, non-reply prompt carries an anchor or reply:\n%s", full.Prompt)
	}
	if code, body := promptCall(t, td, "msg_e1"); code != 200 {
		t.Fatalf("first-message prompt = %d %s", code, body)
	}
	ts, _ = td.tasks.List()
	for _, tk := range ts {
		if full, _ := td.tasks.Show(tk.ID); strings.HasPrefix(full.Prompt, "Turn message msg_e1 ") && strings.Contains(full.Prompt, "earlier messages of conversation") {
			t.Fatalf("the first message carries earlier messages:\n%s", full.Prompt)
		}
	}
}

// TestPromptRefusalsBeforeAnyTask: an unmapped project, a missing task
// chain and a conversation read the daemon's identity is refused are each
// a typed refusal with no task and no prompt.
func TestPromptRefusalsBeforeAnyTask(t *testing.T) {
	run := &analyzeRunner{report: "x"}
	td := taskDaemon(t, run, taskYaml)
	ph := promptConversation(2)
	ph.denied = map[string]bool{"/api/v1/conversations/conv_1/": true}
	scriptPrompt(td, ph)
	code, body := promptCall(t, td, "msg_click")
	if ae := typedErr(t, body); code != 403 || ae.Code != api.ErrCoreForbidden {
		t.Fatalf("refused conversation read = %d %s, want the typed CORE-403", code, body)
	}
	if ts, _ := td.tasks.List(); len(ts) != 0 || len(run.runs()) != 0 {
		t.Fatalf("a refused read created a task: %v", ts)
	}

	run2 := &analyzeRunner{report: "x"}
	td2 := taskDaemon(t, run2, "")
	scriptPrompt(td2, promptConversation(1))
	code, body = promptCall(t, td2, "msg_click")
	if ae := typedErr(t, body); ae.Code != api.ErrDaemonBadInput || !strings.Contains(ae.Detail, "steps.proj.task.models") {
		t.Fatalf("no task chain = %d %s", code, body)
	}

	run3 := &analyzeRunner{report: "x"}
	td3 := newTestDaemon(t, func(c *Config) {
		c.TaskRunner = run3
		ws, _ := c.Mapping.WorkspaceFor("proj")
		c.Mapping = mappingFor(ws, "other")
	})
	ph3 := promptConversation(1)
	m := ph3.msgs["msg_click"]
	m.AboutWI = "wi_elsewhere" // the fake hub answers project "proj", unmapped here
	ph3.msgs["msg_click"] = m
	scriptPrompt(td3, ph3)
	code, body = promptCall(t, td3, "msg_click")
	if ae := typedErr(t, body); ae.Code != api.ErrDaemonUnmappedProject {
		t.Fatalf("unmapped project = %d %s, want DAEMON-412", code, body)
	}
	for _, d := range []*testDaemon{td2, td3} {
		if ts, _ := d.tasks.List(); len(ts) != 0 {
			t.Fatalf("a refused prompt created a task: %v", ts)
		}
	}
	if len(run2.runs())+len(run3.runs()) != 0 {
		t.Fatal("a refused prompt reached the runner")
	}
}

// TestPromptConcurrentAndSlotFull: no per-message lock — two prompts run at
// once; with both slots busy a third is the retryable DAEMON-409 with no
// task.
func TestPromptConcurrentAndSlotFull(t *testing.T) {
	run := &analyzeRunner{report: "p", release: make(chan struct{}), entered: make(chan struct{}, 4)}
	td := taskDaemon(t, run, taskYaml)
	scriptPrompt(td, promptConversation(3))
	first := promptAsync(t, td, "msg_click")
	waitEntered(t, run.entered)
	second := promptAsync(t, td, "msg_click")
	waitEntered(t, run.entered)
	code, body := promptCall(t, td, "msg_e2")
	if ae := typedErr(t, body); ae.Code != api.ErrDaemonConflict {
		t.Fatalf("slot-full prompt = %d %s, want DAEMON-409", code, body)
	}
	if ts, _ := td.tasks.List(); len(ts) != 2 {
		t.Fatalf("tasks after a refused spawn = %d, want 2", len(ts))
	}
	close(run.release)
	for _, ch := range []chan [2]string{first, second} {
		if code, body := promptWait(t, ch); code != 200 {
			t.Fatalf("concurrent prompt = %d %s", code, body)
		}
	}
}

// TestPromptTerminalFailureIsTyped: a failed task answers DAEMON-500
// naming the state and the task id, with no prompt text.
func TestPromptTerminalFailureIsTyped(t *testing.T) {
	run := &analyzeRunner{err: errors.New("model exploded"), report: "PARTIAL-PROMPT"}
	td := taskDaemon(t, run, taskYaml)
	scriptPrompt(td, promptConversation(1))
	code, body := promptCall(t, td, "msg_click")
	ae := typedErr(t, body)
	if code != 500 || ae.Code != api.ErrDaemonInternal || !strings.Contains(ae.Detail, "ended failed") ||
		!strings.Contains(ae.Detail, "pf task show task_") || strings.Contains(body, "PARTIAL-PROMPT") {
		t.Fatalf("failed task = %d %s", code, body)
	}
}

// TestPromptAnswerTerminalStates: every non-completed end and a blank
// report are the typed failure; a completed report is the trimmed prompt.
func TestPromptAnswerTerminalStates(t *testing.T) {
	for _, st := range []task.State{task.StateCapped, task.StateInterrupted, task.StateFailed} {
		_, err := promptAnswer(task.Task{ID: "task_0123456789abcdef", State: st, Report: "partial prompt"})
		if err == nil || !strings.Contains(err.Error(), "ended "+string(st)) || !strings.Contains(err.Error(), "task_0123456789abcdef") ||
			strings.Contains(err.Error(), "partial prompt") {
			t.Fatalf("%s: err = %v", st, err)
		}
	}
	if _, err := promptAnswer(task.Task{ID: "task_0123456789abcdef", State: task.StateCompleted, Report: "  \n"}); err == nil ||
		!strings.Contains(err.Error(), "empty report") {
		t.Fatalf("an empty report must not fabricate a prompt: %v", err)
	}
	res, err := promptAnswer(task.Task{ID: "task_0123456789abcdef", State: task.StateCompleted, Candidate: "pi:t/m",
		WallConsumed: 62340 * time.Millisecond, Report: "\nFix the login timeout in the session store.\n"})
	raw, _ := json.Marshal(res)
	if err != nil || string(raw) != promptFixture {
		t.Fatalf("answer = %s %v, want %s", raw, err, promptFixture)
	}
}

// TestPrompterPromptFirstLineIsNeutral: the first line is the label plus
// the id only; the work item's slug, status and goal and the reply are
// carried below it.
func TestPrompterPromptFirstLineIsNeutral(t *testing.T) {
	m := testMessage("msg_9")
	m.AboutWI = "wi_ctx"
	p := prompterPrompt(m, nil, &api.Message{ID: "msg_8", SenderID: "usr_dan", Text: "REPLY-WORD"},
		&api.WorkItem{ID: "wi_ctx", Slug: "proj#3", Status: api.StatusRunning, Goal: "GOAL-WORD"})
	first, _, _ := strings.Cut(p, "\n")
	if first != "Turn message msg_9 into an execution prompt" {
		t.Fatalf("first line = %q", first)
	}
	for _, want := range []string{analyzeBody, "GOAL-WORD", "REPLY-WORD", "proj#3", string(api.StatusRunning)} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
}

// TestPromptRefusesStepProcess: a step process is refused DAEMON-403
// before the handler, so no hub read and no task happen.
func TestPromptRefusesStepProcess(t *testing.T) {
	run := &analyzeRunner{report: "x"}
	td := taskDaemon(t, run, taskYaml)
	ph := promptConversation(1)
	scriptPrompt(td, ph)
	out := stepOriginRequest(t, td, "POST", "/messages/msg_click/prompt", "PF_WI_ID=wi_st", "PF_DAEMON_ID="+td.cfg.MachineID)
	if !strings.Contains(out, "PFSO-STATUS 403") || !strings.Contains(out, "DAEMON-403") {
		t.Fatalf("a step process was not refused: %s", out)
	}
	ph.mu.Lock()
	n := len(ph.calls)
	ph.mu.Unlock()
	if ts, _ := td.tasks.List(); len(ts) != 0 || n != 0 {
		t.Fatalf("a refused step call reached the handler: tasks %v, hub calls %d", ts, n)
	}
}
