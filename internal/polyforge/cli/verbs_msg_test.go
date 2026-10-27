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
			io.WriteString(w, `{"conversations":[{"id":"conv_1","kind":"direct","last_seq":7,"unread":1,"members":["u_1","u_2"]}]}`)
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
	code, stdout, stderr = runPf(t, env, "msg", "list", "--conversation", "conv_1", "--after", "3", "--limit", "2")
	if code != 0 || !strings.Contains(stdout, "hello there") {
		t.Fatalf("list messages: exit %d %s %s", code, stdout, stderr)
	}
	if got := rec.last().Path; !strings.Contains(got, "/api/v1/conversations/conv_1/messages") ||
		!strings.Contains(got, "after=3") || !strings.Contains(got, "limit=2") {
		t.Fatalf("list messages path = %s", got)
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
