package cli

// verbs_msg_test.go: the user-to-user messaging verbs. send/list/show are
// data-plane (direct to aihub with the human key) and pass --json through
// unchanged; analyze is execution-plane (the local daemon). A step
// process is refused before any request leaves.

import (
	"io"
	"net/http"
	"strings"
	"testing"
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
	sock := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		drec.record(r)
		io.WriteString(w, `{"message_id":"msg_1","conclusion":"do the thing","model":"pi:m"}`)
	}))
	env := map[string]string{"PF_SERVER": srv.URL, "PF_DAEMON_SOCKET": sock, "PF_API_KEY": "k_msg"}
	code, stdout, stderr := runPf(t, env, "msg", "analyze", "msg_1")
	if code != 0 || !strings.Contains(stdout, "do the thing") || !strings.Contains(stdout, "model pi:m") {
		t.Fatalf("analyze: exit %d %s %s", code, stdout, stderr)
	}
	if got := drec.last(); got.Method != "POST" || got.Path != "/messages/msg_1/analyze" {
		t.Fatalf("analyze request: %s %s", got.Method, got.Path)
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("the analyzer CLI reached aihub %d time(s)", n)
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
