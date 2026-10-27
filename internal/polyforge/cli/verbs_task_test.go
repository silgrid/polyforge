package cli

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/task"
)

func TestTaskSpawnRefusesUnsupportedArgsAndPaths(t *testing.T) {
	sock := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a refused spawn must not reach the daemon: %s %s", r.Method, r.URL.Path)
	}))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"task", "spawn", "--project", "p", "--models", "pi:a/b", "hi"}, "--models"},
		{[]string{"task", "spawn", "--project", "p", "--wait", "hi"}, "--wait"},
		{[]string{"task", "spawn", "--project", "p", "--type", "x", "hi"}, "--type"},
		{[]string{"task", "spawn", "--project", "/tmp/x", "hi"}, "selector"},
		{[]string{"task", "spawn", "--project", "p", "--max-duration", "2h", "hi"}, "60m"},
		{[]string{"task", "spawn", "--project", "p", "--max-tokens", "2000000", "hi"}, "1000000"},
		{[]string{"task", "spawn", "hi"}, "--project"},
	} {
		code, _, stderr := runPf(t, map[string]string{"PF_DAEMON_SOCKET": sock}, tc.args...)
		if code == 0 || !strings.Contains(stderr, tc.want) {
			t.Fatalf("%v: code=%d stderr=%q, want a refusal naming %q", tc.args, code, stderr, tc.want)
		}
	}
}

func TestTaskSpawnPrintsTheIDAndSendsOrigin(t *testing.T) {
	rec := &reqRecorder{}
	sock := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"id":"task_0123456789abcdef","spec":{"project":"p"},"state":"running"}`))
	}))
	code, stdout, stderr := runPf(t, map[string]string{"PF_DAEMON_SOCKET": sock, "PF_CHAT_ID": "chat_0123456789abcdef"},
		"task", "spawn", "--project", "p", "--max-tokens", "1000", "look", "around")
	if code != 0 || strings.TrimSpace(stdout) != "task_0123456789abcdef" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	b := rec.last().Body
	for _, want := range []string{`"project":"p"`, `"prompt":"look around"`, `"max_tokens":1000`, `"origin_chat":"chat_0123456789abcdef"`} {
		if !strings.Contains(b, want) {
			t.Fatalf("body %s lacks %s", b, want)
		}
	}
}

func TestTaskListAndShowRender(t *testing.T) {
	sock := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/task/list":
			_, _ = w.Write([]byte(`{"tasks":[{"id":"task_0123456789abcdef","spec":{"project":"p"},"state":"capped","created_at":"2026-01-01T00:00:00Z"}]}`))
		default:
			_, _ = w.Write([]byte(`{"id":"task_0123456789abcdef","spec":{"project":"p","max_tokens":10},"state":"capped","truncated":true,"report":"the report body"}`))
		}
	}))
	env := map[string]string{"PF_DAEMON_SOCKET": sock}
	_, out, _ := runPf(t, env, "task", "list")
	if !strings.Contains(out, "task_0123456789abcdef") || !strings.Contains(out, "capped") {
		t.Fatalf("list:\n%s", out)
	}
	_, out, _ = runPf(t, env, "task", "show", "task_0123456789abcdef")
	if !strings.Contains(out, "state: capped") || !strings.Contains(out, "truncated: true") || !strings.Contains(out, "the report body") {
		t.Fatalf("show:\n%s", out)
	}
}

func TestStatusRendersOneTaskLine(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, &StatusResponse{Tasks: &task.Summary{Running: 1, Limit: 2, Completed: 3, Capped: 1}})
	if !strings.Contains(b.String(), "tasks: 1/2 running (completed 3, capped 1, failed 0, interrupted 0)\n") {
		t.Fatalf("status:\n%s", b.String())
	}
	b.Reset()
	renderStatus(&b, &StatusResponse{})
	if strings.Contains(b.String(), "tasks:") {
		t.Fatalf("an older daemon carries no tasks line:\n%s", b.String())
	}
}

func TestTaskModeRoutesMemoryToTheTaskFaceAndRefusesTheRest(t *testing.T) {
	rec := &reqRecorder{}
	sock := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	env := map[string]string{"PF_DAEMON_SOCKET": sock, "PF_TASK_ID": "task_0123456789abcdef", "PF_DAEMON_ID": "d"}
	code, _, _ := runPf(t, env, "recall", "--project", "p", "--any", "x")
	if code != 0 || !strings.HasPrefix(rec.last().Path, "/task/memories/search") {
		t.Fatalf("recall in task mode: code=%d last=%+v", code, rec.last())
	}
	for _, args := range [][]string{{"remember", "--project", "p"}, {"task", "spawn", "--project", "p", "x"}, {"queue"}} {
		code, _, stderr := runPf(t, env, args...)
		if code == 0 || !strings.Contains(stderr, "refused inside a task agent") {
			t.Fatalf("pf %v in a task: code=%d stderr=%q", args, code, stderr)
		}
	}
}
