package cli

// verbs_msg_test.go: the user-to-user messaging verbs. send/list/show are
// data-plane (direct to aihub with the human key) and pass --json through
// unchanged; analyze and prompt are execution-plane (the local daemon). A step
// process is refused before any request leaves.

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func msgAihub(t *testing.T, rec *reqRecorder) string {
	t.Helper()
	srv := newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/messages/") && r.Method == http.MethodGet {
			io.WriteString(w, `{"id":"msg_1","seq":7,"conversation_id":"conv_1","sender_id":"u_1","text":"hello there","about_wi":"wi_1","created_at":"2026-10-25T00:00:00Z"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/messages") && r.Method == http.MethodGet {
			io.WriteString(w, `{"messages":[{"id":"msg_1","seq":7,"conversation_id":"conv_1","sender_id":"u_1","text":"hello there"}],"last_seq":7,"after_seq":3}`)
			return
		}
		if r.Method == http.MethodGet {
			io.WriteString(w, `{"conversations":[{"id":"conv_1","kind":"direct","last_seq":7,"unread":1,"members":["u_1","u_2"],"member_names":{"u_1":"Alice","u_2":"Bob"}}]}`)
			return
		}
		io.WriteString(w, `{"id":"msg_9","seq":9,"conversation_id":"conv_1","sender_id":"u_1","text":"hi","about_wi":"wi_1","reply_to":"msg_1","created_at":"2026-10-25T00:00:00Z"}`)
	})
	return srv.URL
}

func TestMsgSendShapesAndJSONPassthrough(t *testing.T) {
	rec := &reqRecorder{}
	url := msgAihub(t, rec)
	env := map[string]string{"PF_SERVER": url, "PF_API_KEY": "k_msg"}
	code, stdout, stderr := runPf(t, env, "msg", "send", "--to", "u_2", "--text", "hi",
		"--about-wi", "wi_1", "--reply-to", "msg_1")
	if code != 0 || !strings.Contains(stdout, "sent msg_9 to u_2 (conversation conv_1)") {
		t.Fatalf("send: exit %d %s %s", code, stdout, stderr)
	}
	req := rec.last()
	if req.Method != "POST" || req.Path != "/api/v1/messages" {
		t.Fatalf("send request: %s %s", req.Method, req.Path)
	}
	for _, want := range []string{`"to":"u_2"`, `"text":"hi"`, `"about_wi":"wi_1"`, `"reply_to":"msg_1"`} {
		if !strings.Contains(req.Body, want) {
			t.Fatalf("send body %s lacks %s", req.Body, want)
		}
	}
	code, stdout, stderr = runPf(t, env, "--json", "msg", "send", "--to", "u_2", "--text", "hi")
	if code != 0 || !strings.Contains(stdout, `"id":"msg_9"`) {
		t.Fatalf("send --json passthrough: exit %d %s %s", code, stdout, stderr)
	}
}

func TestMsgListShowShapes(t *testing.T) {
	rec := &reqRecorder{}
	url := msgAihub(t, rec)
	env := map[string]string{"PF_SERVER": url, "PF_API_KEY": "k_msg"}
	code, stdout, stderr := runPf(t, env, "msg", "list")
	if code != 0 || !strings.Contains(stdout, "conv_1") || !strings.Contains(stdout, "1 conversation(s)") {
		t.Fatalf("list conversations: exit %d %s %s", code, stdout, stderr)
	}
	if got := rec.last().Path; got != "/api/v1/conversations" {
		t.Fatalf("list path = %s", got)
	}
	// The human render prints the conversation row but gains no name
	// surface (renderConversations shows members, not names).
	if strings.Contains(stdout, "Alice") || strings.Contains(stdout, "Bob") {
		t.Fatalf("human render grew a name surface: %s", stdout)
	}
	// --json passes the server's member_names through unchanged, with no
	// reshape or rename.
	code, stdout, stderr = runPf(t, env, "--json", "msg", "list")
	if code != 0 || !strings.Contains(stdout, `"member_names":{"u_1":"Alice","u_2":"Bob"}`) {
		t.Fatalf("list --json member_names passthrough: exit %d %s %s", code, stdout, stderr)
	}
	code, stdout, stderr = runPf(t, env, "msg", "list", "--conversation", "conv_1", "--after", "3", "--limit", "2")
	if code != 0 || !strings.Contains(stdout, "hello there") {
		t.Fatalf("list messages: exit %d %s %s", code, stdout, stderr)
	}
	if got := rec.last().Path; !strings.Contains(got, "/api/v1/conversations/conv_1/messages") ||
		!strings.Contains(got, "after=3") || !strings.Contains(got, "limit=2") {
		t.Fatalf("list messages path = %s", got)
	}
	code, stdout, stderr = runPf(t, env, "msg", "list", "--conversation", "conv_1", "--before", "71", "--limit", "50")
	if code != 0 || !strings.Contains(stdout, "hello there") {
		t.Fatalf("list messages --before: exit %d %s %s", code, stdout, stderr)
	}
	if got := rec.last().Path; !strings.Contains(got, "before=71") || !strings.Contains(got, "limit=50") ||
		strings.Contains(got, "after=") {
		t.Fatalf("list messages --before path = %s", got)
	}
	code, stdout, stderr = runPf(t, env, "msg", "show", "msg_1")
	if code != 0 || !strings.Contains(stdout, "#7 msg_1") || !strings.Contains(stdout, "hello there") {
		t.Fatalf("show: exit %d %s %s", code, stdout, stderr)
	}
	if got := rec.last().Path; got != "/api/v1/messages/msg_1" {
		t.Fatalf("show path = %s", got)
	}
	if code, stdout, stderr = runPf(t, env, "--json", "msg", "show", "msg_1"); code != 0 || !strings.Contains(stdout, `"seq":7`) {
		t.Fatalf("show --json: exit %d %s %s", code, stdout, stderr)
	}
}

func TestMsgAnalyzeGoesToTheDaemon(t *testing.T) {
	arec := &reqRecorder{}
	srv := newFakeAihub(t, arec, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{}`) })
	drec := &reqRecorder{}
	answer := `{"message_id":"msg_1","task_id":"task_0123456789abcdef","project":"proj","type":"bug",` +
		`"summary":"File the crash.","suggested_actions":["file_ticket","reply"],"model":"pi:m","duration":"1m2.3s",` +
		`"reply_draft":"Thanks, filing it."}`
	sock := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		drec.record(r)
		io.WriteString(w, answer)
	}))
	env := map[string]string{"PF_SERVER": srv.URL, "PF_DAEMON_SOCKET": sock, "PF_API_KEY": "k_msg", "PF_USER_ID": "usr_carol"}
	code, stdout, stderr := runPf(t, env, "msg", "analyze", "msg_1")
	if code != 0 {
		t.Fatalf("analyze: exit %d %s %s", code, stdout, stderr)
	}
	for _, want := range []string{"analysis of msg_1: bug", "summary: File the crash.", "suggested actions: file_ticket, reply",
		"reply draft: Thanks, filing it.", "model: pi:m", "duration: 1m2.3s", "task: task_0123456789abcdef", "pf task show task_0123456789abcdef"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("analyze render lacks %q: %s", want, stdout)
		}
	}
	if strings.Contains(stdout, "conclusion") {
		t.Fatalf("the render names the retired conclusion: %s", stdout)
	}
	got := drec.last()
	if got.Method != "POST" || got.Path != "/messages/msg_1/analyze" {
		t.Fatalf("analyze request: %s %s", got.Method, got.Path)
	}
	if !strings.Contains(got.Body, `"caller_id":"usr_carol"`) {
		t.Fatalf("analyze body %s lacks the caller id", got.Body)
	}
	code, stdout, stderr = runPf(t, env, "--json", "msg", "analyze", "msg_1")
	if code != 0 || strings.TrimSpace(stdout) != answer {
		t.Fatalf("analyze --json passthrough: exit %d %q %s", code, stdout, stderr)
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("the analyzer CLI reached aihub %d time(s)", n)
	}
}

// TestMsgPromptGoesToTheDaemon: `pf msg prompt <id>` is one POST to the
// local daemon's prompt route with the caller id, renders the prompt and
// its task facts, passes --json through unchanged, takes exactly one id
// and never reaches aihub.
func TestMsgPromptGoesToTheDaemon(t *testing.T) {
	arec := &reqRecorder{}
	srv := newFakeAihub(t, arec, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{}`) })
	drec := &reqRecorder{}
	answer := `{"prompt":"Fix the login timeout in the session store.","task_id":"task_0123456789abcdef","model":"pi:t/m","duration":"1m2.3s"}`
	sock := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		drec.record(r)
		io.WriteString(w, answer)
	}))
	env := map[string]string{"PF_SERVER": srv.URL, "PF_DAEMON_SOCKET": sock, "PF_API_KEY": "k_msg", "PF_USER_ID": "usr_carol"}
	code, stdout, stderr := runPf(t, env, "msg", "prompt", "msg_1")
	if code != 0 {
		t.Fatalf("prompt: exit %d %s %s", code, stdout, stderr)
	}
	for _, want := range []string{"Fix the login timeout in the session store.", "model: pi:t/m", "duration: 1m2.3s",
		"task: task_0123456789abcdef (pf task show task_0123456789abcdef)"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("prompt render lacks %q: %s", want, stdout)
		}
	}
	got := drec.last()
	if got.Method != "POST" || got.Path != "/messages/msg_1/prompt" || !strings.Contains(got.Body, `"caller_id":"usr_carol"`) {
		t.Fatalf("prompt request: %s %s %s", got.Method, got.Path, got.Body)
	}
	code, stdout, stderr = runPf(t, env, "--json", "msg", "prompt", "msg_1")
	if code != 0 || strings.TrimSpace(stdout) != answer {
		t.Fatalf("prompt --json passthrough: exit %d %q %s", code, stdout, stderr)
	}
	before := len(drec.all())
	for _, args := range [][]string{{"msg", "prompt"}, {"msg", "prompt", "msg_1", "msg_2"}} {
		if code, _, stderr := runPf(t, env, args...); code == 0 || !strings.Contains(stderr, "expected exactly one message id") {
			t.Fatalf("%v: exit %d %s, want the one-id refusal", args, code, stderr)
		}
	}
	if n := len(drec.all()); n != before {
		t.Fatalf("a refused prompt call reached the daemon")
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("the prompt CLI reached aihub %d time(s)", n)
	}
}

func TestAnalyzeClientTimeoutOutlastsTheDaemonWait(t *testing.T) {
	if analyzeClientTimeout <= 6*time.Minute {
		t.Fatalf("analyze client timeout %v must outlast the daemon's 6-minute wait bound", analyzeClientTimeout)
	}
	d := NewDaemonClient("/nonexistent.sock")
	if d.HTTP.Timeout != 120*time.Second {
		t.Fatalf("the global client bound changed: %v", d.HTTP.Timeout)
	}
}

func TestMsgVerbsAreRefusedInsideAStep(t *testing.T) {
	arec := &reqRecorder{}
	srv := newFakeAihub(t, arec, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{}`) })
	env := map[string]string{
		"PF_SERVER": srv.URL, "PF_API_KEY": "k_should_never_leave",
		"PF_WI_ID": "wi_st", "PF_DAEMON_ID": "m-test", "PF_DAEMON_SOCKET": "/nonexistent.sock",
	}
	for _, args := range [][]string{
		{"msg", "send", "--to", "u_2", "--text", "hi"},
		{"msg", "list"},
		{"msg", "ack", "conv_1"},
		{"msg", "show", "msg_1"},
		{"msg", "analyze", "msg_1"},
		{"msg", "prompt", "msg_1"},
		{"msg", "contacts"},
		{"machines", "rekey", "m-1", "--public-key", "pk"},
	} {
		code, _, stderr := runPf(t, env, args...)
		if code == 0 {
			t.Fatalf("%v must be refused inside a step", args)
		}
		if stderr == "" {
			t.Fatalf("%v: no refusal reason", args)
		}
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("a step process reached aihub %d time(s)", n)
	}
}

// msgAckAihub answers the ack read-then-write path: the newest page first,
// then the ack POST. conv_missing is CORE-404 on the read.
func msgAckAihub(t *testing.T, rec *reqRecorder) string {
	t.Helper()
	srv := newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "conv_missing") {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"code":"CORE-404","detail":"conversation conv_missing does not exist"}`)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/messages") {
			io.WriteString(w, `{"messages":[{"id":"msg_12","seq":12,"conversation_id":"conv_1","sender_id":"u_1","text":"newest"}],"last_seq":12}`)
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages/ack") {
			io.WriteString(w, `{"ok":true}`)
			return
		}
		io.WriteString(w, `{"conversations":[]}`)
	})
	return srv.URL
}

// TestMsgAckUsesTheNewestSequenceAndForwardsExplicitSeq: without --seq the
// CLI reads the newest sequence first; an explicit --seq is forwarded
// unchanged so the server enforces bounds; an unknown conversation surfaces
// the read's CORE-404 and no ack is sent.
func TestMsgAckUsesTheNewestSequenceAndForwardsExplicitSeq(t *testing.T) {
	rec := &reqRecorder{}
	url := msgAckAihub(t, rec)
	env := map[string]string{"PF_SERVER": url, "PF_API_KEY": "k_msg"}

	code, stdout, stderr := runPf(t, env, "msg", "ack", "conv_1")
	if code != 0 || !strings.Contains(stdout, "acknowledged conv_1 through seq 12") {
		t.Fatalf("ack newest: exit %d %s %s", code, stdout, stderr)
	}
	reqs := rec.all()
	if len(reqs) != 2 || reqs[0].Method != http.MethodGet ||
		!strings.Contains(reqs[0].Path, "/api/v1/conversations/conv_1/messages") ||
		!strings.Contains(reqs[0].Path, "before=9223372036854775807") ||
		reqs[1].Method != http.MethodPost || reqs[1].Path != "/api/v1/messages/ack" ||
		!strings.Contains(reqs[1].Body, `"seq":12`) {
		t.Fatalf("ack newest requests: %+v", reqs)
	}

	code, _, stderr = runPf(t, env, "msg", "ack", "conv_1", "--seq", "5")
	if code != 0 || !strings.Contains(rec.last().Body, `"seq":5`) || rec.last().Path != "/api/v1/messages/ack" {
		t.Fatalf("ack --seq 5: exit %d %s last=%+v", code, stderr, rec.last())
	}
	// An explicit non-positive sequence reaches the server unchanged: the
	// CLI is not a second bound enforcer.
	code, _, stderr = runPf(t, env, "msg", "ack", "conv_1", "--seq", "0")
	if code != 0 || !strings.Contains(rec.last().Body, `"seq":0`) {
		t.Fatalf("ack --seq 0: exit %d %s last=%+v", code, stderr, rec.last())
	}

	before := len(rec.all())
	code, stdout, stderr = runPf(t, env, "msg", "ack", "conv_missing")
	if code == 0 || !strings.Contains(stderr, "CORE-404") {
		t.Fatalf("ack unknown conversation: exit %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if after := len(rec.all()); after != before+1 {
		t.Fatalf("an unknown conversation ack sent the ack POST too: %d -> %d", before, after)
	}
}

// contactsWorkspace writes a cli.toml whose one workspace maps projects in
// the given order and returns the env pointing pf at it and at server.
func contactsWorkspace(t *testing.T, server string, projects ...string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	quoted := make([]string, len(projects))
	for i, p := range projects {
		quoted[i] = `"` + p + `"`
	}
	body := "[workspace.\"" + filepath.Join(dir, "ws") + "\"]\nprojects = [" + strings.Join(quoted, ", ") + "]\n"
	if err := os.MkdirAll(filepath.Join(dir, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "polyforge", "cli.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"XDG_CONFIG_HOME": dir, "PF_SERVER": server, "PF_API_KEY": "k_contacts"}
}

// TestMsgContactsOrderFirstNameWinsAndHumanRender: the union keeps mapping
// declaration order, the first project's display name wins for a shared
// user, a failing project contributes zero, and the human render is one
// "<id>  <name>" line per contact.
func TestMsgContactsOrderFirstNameWinsAndHumanRender(t *testing.T) {
	rec := &reqRecorder{}
	srv := newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/pz/members":
			io.WriteString(w, `{"contacts":[{"id":"u_z","display_name":"Zoe"},{"id":"u_s","display_name":"First"}]}`)
		case "/api/v1/projects/pa/members":
			io.WriteString(w, `{"contacts":[{"id":"u_s","display_name":"Second"},{"id":"u_a","display_name":"Abe"}]}`)
		default:
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"code":"CORE-500","detail":"boom"}`)
		}
	})
	env := contactsWorkspace(t, srv.URL, "pz", "pbroken", "pa")
	code, stdout, stderr := runPf(t, env, "--json", "msg", "contacts")
	want := `{"contacts":[{"id":"u_z","display_name":"Zoe"},{"id":"u_s","display_name":"First"},{"id":"u_a","display_name":"Abe"}]}`
	if code != 0 || strings.TrimSpace(stdout) != want {
		t.Fatalf("--json: exit %d %q %s, want %s", code, stdout, stderr, want)
	}
	code, stdout, stderr = runPf(t, env, "msg", "contacts")
	if code != 0 || stdout != "u_z  Zoe\nu_s  First\nu_a  Abe\n" {
		t.Fatalf("human: exit %d %q %s", code, stdout, stderr)
	}
	if code, _, stderr := runPf(t, env, "msg", "contacts", "extra"); code == 0 || stderr == "" {
		t.Fatalf("a positional argument must be refused, got exit %d", code)
	}
}

// TestMsgContactsBoundsInFlightReads: with more mapped projects than the
// in-flight bound, at most contactsInFlight reads are outstanding at once
// and every project is still read exactly once.
func TestMsgContactsBoundsInFlightReads(t *testing.T) {
	var mu sync.Mutex
	active, peak := 0, 0
	release := make(chan struct{})
	var once sync.Once
	rec := &reqRecorder{}
	srv := newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		if active == contactsInFlight {
			once.Do(func() { close(release) })
		}
		mu.Unlock()
		// Hold the read until the bound is reached (deadline only guards a
		// broken bound from hanging the test).
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		mu.Lock()
		active--
		mu.Unlock()
		io.WriteString(w, `{"contacts":[]}`)
	})
	projects := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7"}
	env := contactsWorkspace(t, srv.URL, projects...)
	code, stdout, stderr := runPf(t, env, "--json", "msg", "contacts")
	if code != 0 || strings.TrimSpace(stdout) != `{"contacts":[]}` {
		t.Fatalf("exit %d %q %s", code, stdout, stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak != contactsInFlight {
		t.Fatalf("peak in-flight reads = %d, want %d", peak, contactsInFlight)
	}
	if n := len(rec.all()); n != len(projects) {
		t.Fatalf("reads = %d, want one per project (%d)", n, len(projects))
	}
}
