package cli

// verbs_step_test.go: `pf step` — outside a step it
// refuses non-zero before any request; inside one every subcommand is one
// POST /step/call carrying its verb and args (the hand-off's body read
// from @file, its evidence made absolute against the cwd; the hub writes'
// JSON read inline or from @file), a daemon refusal exits non-zero, and
// nothing reaches aihub.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// stepCallEnv answers a fake daemon serving /step/call (refusing a
// hand-off whose status is "refuse-me" DAEMON-400), a step invocation's
// env pointed at it, and its request log and the untouched aihub's.
func stepCallEnv(t *testing.T) (map[string]string, *reqRecorder, *reqRecorder) {
	t.Helper()
	drec := &reqRecorder{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /step/call", func(w http.ResponseWriter, r *http.Request) {
		drec.record(r)
		var req struct {
			Verb string          `json:"verb"`
			Args json.RawMessage `json:"args"`
		}
		_ = json.Unmarshal([]byte(drec.last().Body), &req)
		switch {
		case strings.Contains(string(req.Args), "refuse-me"):
			w.WriteHeader(400)
			io.WriteString(w, `{"code":"DAEMON-400","detail":"handoff refused: --status \"refuse-me\" is none of done, fail, blocked, cancel, waive, rewind"}`)
		case req.Verb == "handoff":
			io.WriteString(w, `{"call":3,"step":"build","round":1,"spawn":2,"status":"done","path":"artifacts/handoff.md"}`)
		case req.Verb == "ask":
			io.WriteString(w, `{"at":"2026-11-11T10:00:00Z","step":"build","spawn":2,"kind":"ask","text":"q"}`)
		case req.Verb == "hub.get":
			io.WriteString(w, `{"id":"wi_x","project":"proj"}`)
		case req.Verb == "recall":
			io.WriteString(w, `{"items":[{"id":"mem_1","type":"experience.debug","title":"sqlite busy","content_excerpt":"x"}],"total":1}`)
		case req.Verb == "memory.show":
			io.WriteString(w, `{"meta":{"id":"mem_1","type":"experience.debug","title":"sqlite busy","visibility":"project"},"content":"the body\n"}`)
		case req.Verb == "remember" && strings.Contains(string(req.Args), "second summary"):
			w.WriteHeader(409)
			io.WriteString(w, `{"code":"DAEMON-409","detail":"this step already stored its summary mem_9; summaries are not edited — put corrections into the one call"}`)
		case req.Verb == "remember":
			io.WriteString(w, `{"id":"mem_9","type":"experience.wrap","visibility":"project"}`)
		default:
			io.WriteString(w, `{"call":4,"pending":["4.1"],"note":"recorded pending"}`)
		}
	})
	sock := newFakeDaemon(t, mux)
	arec := &reqRecorder{}
	srv := newFakeAihub(t, arec, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{}`) })
	env := map[string]string{"PF_WI_ID": "wi_st", "PF_DAEMON_ID": "m-test", "PF_DAEMON_SOCKET": sock, "PF_SERVER": srv.URL, "PF_API_KEY": "k_should_never_leave"}
	return env, drec, arec
}

// lastCall decodes the fake daemon's last /step/call.
func lastCall(t *testing.T, drec *reqRecorder) (string, map[string]any) {
	t.Helper()
	var req struct {
		Verb string         `json:"verb"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(drec.last().Body), &req); err != nil {
		t.Fatalf("the call is not JSON: %v (%s)", err, drec.last().Body)
	}
	return req.Verb, req.Args
}

func TestStepVerbRefusedOutsideAStep(t *testing.T) {
	env, drec, _ := stepCallEnv(t)
	delete(env, "PF_WI_ID")
	for k, v := range withMappingConfig(t) {
		env[k] = v
	}
	for _, argv := range [][]string{
		{"step", "handoff", "--status", "done", "--body", "@x.md"},
		{"step", "hub", "get", "wi_x"},
		{"step", "ask", "q"},
	} {
		code, _, stderr := runPf(t, env, argv...)
		if code == 0 || !strings.Contains(stderr, "inside a workflow step only") {
			t.Fatalf("%v outside a step: exit %d %s", argv, code, stderr)
		}
	}
	if n := len(drec.all()); n != 0 {
		t.Fatalf("a refused call reached the daemon %d time(s)", n)
	}
}

func TestStepHandoffPostsTheCall(t *testing.T) {
	env, drec, arec := stepCallEnv(t)
	cwd := t.TempDir()
	bodyFile := filepath.Join(cwd, "handoff.md")
	if err := os.WriteFile(bodyFile, []byte("## 结论\n\n通过\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runPfInCwd(t, cwd, env, "step", "handoff", "--status", "rewind", "--to", "build", "--body", "@"+bodyFile,
		"--evidence", "artifacts/r.json", "--evidence", "/abs/e.json", "--per-owner", "2026-11-11T10:00:00Z", "--reason", "owner said so")
	if code != 0 || !strings.Contains(stdout, "now end your turn") {
		t.Fatalf("handoff: exit %d %s %s", code, stdout, stderr)
	}
	verb, args := lastCall(t, drec)
	if verb != "handoff" || args["status"] != "rewind" || args["to"] != "build" || args["body"] != "## 结论\n\n通过\n" ||
		args["per_owner"] != "2026-11-11T10:00:00Z" || args["reason"] != "owner said so" {
		t.Fatalf("the call = %s %v", verb, args)
	}
	ev := args["evidence"].([]any)
	if len(ev) != 2 || ev[0] != filepath.Join(cwd, "artifacts/r.json") || ev[1] != "/abs/e.json" {
		t.Fatalf("the evidence = %v", ev)
	}
	// a daemon refusal exits non-zero
	if code, _, stderr := runPfInCwd(t, cwd, env, "step", "handoff", "--status", "refuse-me", "--body", "@"+bodyFile); code != 1 || !strings.Contains(stderr, "DAEMON-400") {
		t.Fatalf("a refused hand-off: exit %d %s", code, stderr)
	}
	// the body is a file, never inline text
	n := len(drec.all())
	if code, _, stderr := runPfInCwd(t, cwd, env, "step", "handoff", "--status", "done", "--body", "inline"); code != 1 || !strings.Contains(stderr, "@file") || len(drec.all()) != n {
		t.Fatalf("an inline body: exit %d %s", code, stderr)
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("a step invocation reached aihub %d time(s)", n)
	}
}

// runPfStdin is runPf with stdin.
func runPfStdin(t *testing.T, env map[string]string, stdin string, args ...string) (int, string, string) {
	t.Helper()
	clearEnv(t)
	if _, ok := env["XDG_CONFIG_HOME"]; !ok {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}
	env = viaCellProxy(t, env)
	for k, v := range env {
		t.Setenv(k, v)
	}
	var stdout, stderr bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// `pf step ask` checks the question before any call (ported from contract
// 3's `pf ask`,): one is required, not blank, within
// capsule.MaxAskBytes and valid UTF-8; `-` reads it from stdin.
func TestStepAskChecksTheQuestion(t *testing.T) {
	env, drec, _ := stepCallEnv(t)
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"step", "ask"}, "question is required"},
		{[]string{"step", "ask", "   "}, "question is empty"},
		{[]string{"step", "ask", strings.Repeat("x", capsule.MaxAskBytes+1)}, "over the"},
		{[]string{"step", "ask", "\xff\xfe"}, "not valid UTF-8"},
	} {
		if code, _, stderr := runPf(t, env, c.argv...); code != 1 || !strings.Contains(stderr, c.want) {
			t.Fatalf("%q: exit %d %s, want %q", c.argv, code, stderr, c.want)
		}
	}
	if n := len(drec.all()); n != 0 {
		t.Fatalf("a refused question reached the daemon %d time(s)", n)
	}
	if code, stdout, stderr := runPfStdin(t, env, "line one\nline two\n", "step", "ask", "-"); code != 0 || !strings.Contains(stdout, "now end your turn") {
		t.Fatalf("stdin: exit %d %s %s", code, stdout, stderr)
	}
	if verb, args := lastCall(t, drec); verb != "ask" || args["text"] != "line one\nline two" {
		t.Fatalf("the stdin question = %s %v", verb, args)
	}
}

func TestStepHubAndAskPostTheirCalls(t *testing.T) {
	env, drec, arec := stepCallEnv(t)
	dir := t.TempDir()
	ops := filepath.Join(dir, "ops.json")
	if err := os.WriteFile(ops, []byte(`[{"verb":"file","key":"K1","lane":"develop","goal":"g","content":"c"},{"verb":"attrs","target":"key:K1","name":"integration","value":{"state":"merged"},"if":{"integration.state":"landed"}}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	content := filepath.Join(dir, "ticket.md")
	if err := os.WriteFile(content, []byte("the body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		argv []string
		verb string
		want map[string]any
	}{
		{[]string{"hub", "get", "wi_x"}, "hub.get", map[string]any{"wi": "wi_x"}},
		{[]string{"hub", "list", "--status", "wrapped", "--attr", "integration.state=landed", "--attr", "integration.batch=p#9", "--limit", "5"}, "hub.list", map[string]any{"status": "wrapped", "attrs": "[integration.state=landed integration.batch=p#9]", "limit": float64(5)}},
		{[]string{"hub", "file", "--key", "K1", "--lane", "develop", "--goal", "g", "--content", "@" + content, "--label", "a", "--label", "b"}, "hub.file",
			map[string]any{"key": "K1", "lane": "develop", "goal": "g", "content": "the body\n"}},
		{[]string{"hub", "attrs", "key:K1", "--set", "integration", `{"state":"merged"}`, "--if", "integration.state=landed", "--if", "integration.batch=p#9"}, "hub.attrs",
			map[string]any{"target": "key:K1", "name": "integration", "value": `map[state:merged]`, "if": `map[integration.batch:p#9 integration.state:landed]`, "per_owner": ""}},
		{[]string{"hub", "attrs", "wi_y", "--per-owner", "2026-11-11T10:00:00Z", "--set", "integration", "@" + ops}, "hub.attrs", map[string]any{"target": "wi_y", "name": "integration", "per_owner": "2026-11-11T10:00:00Z"}},
		{[]string{"hub", "wrap-attrs", "landing", `{"state":"landed"}`}, "hub.wrap-attrs", map[string]any{"name": "landing"}},
		{[]string{"hub", "batch", "@" + ops}, "hub.batch", nil},
		{[]string{"ask", "which", "design?"}, "ask", map[string]any{"text": "which design?"}},
		{[]string{"recall", "--any", "sqlite", "busy"}, "recall", map[string]any{"q": "sqlite busy", "match": "any"}},
	} {
		code, stdout, stderr := runPf(t, env, append([]string{"step"}, c.argv...)...)
		if code != 0 {
			t.Fatalf("%v: exit %d %s %s", c.argv, code, stdout, stderr)
		}
		verb, args := lastCall(t, drec)
		if verb != c.verb {
			t.Fatalf("%v: verb %s", c.argv, verb)
		}
		for k, v := range c.want {
			if fmt.Sprint(args[k]) != fmt.Sprint(v) {
				t.Fatalf("%v: args[%s] = %v, want %v (%v)", c.argv, k, args[k], v, args)
			}
		}
	}
	if code, _, _ := runPf(t, env, "step", "hub", "batch", "@"+ops); code != 0 {
		t.Fatal("batch")
	}
	_, args := lastCall(t, drec)
	if list, _ := args["ops"].([]any); len(list) != 2 {
		t.Fatalf("the batch's ops = %v", args)
	}
	// an empty ops file is posted as an empty list (the daemon records it
	// as a no-op: R4), never refused here
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := runPf(t, env, "step", "hub", "batch", "@"+empty); code != 0 {
		t.Fatalf("an empty batch: exit %d %s %s", code, stdout, stderr)
	}
	if verb, args := lastCall(t, drec); verb != "hub.batch" || args["ops"] == nil || len(args["ops"].([]any)) != 0 {
		t.Fatalf("the empty batch's call = %s %v", verb, args)
	}
	if code, _, stderr := runPf(t, env, "step", "hub", "attrs", "wi_x", "--set", "integration", "{not json"); code != 1 || !strings.Contains(stderr, "not valid JSON") {
		t.Fatalf("a malformed value: exit %d %s", code, stderr)
	}
	if code, _, stderr := runPf(t, env, "step", "hub", "attrs", "wi_x", "--set", "integration", "{}", "--if", "integration.state"); code != 1 || !strings.Contains(stderr, "is not <path>=<value>") {
		t.Fatalf("an --if without a value: exit %d %s", code, stderr)
	}
	for _, gone := range [][]string{{"hub", "move", "wi_x"}, {"hub", "clear-hold", "wi_x"}, {"hub", "list", "--integration", "landed"}} {
		if code, _, stderr := runPf(t, env, append([]string{"step"}, gone...)...); code == 0 {
			t.Fatalf("%v is gone (workflow contract 1): exit %d %s", gone, code, stderr)
		}
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("a step invocation reached aihub %d time(s)", n)
	}
}
