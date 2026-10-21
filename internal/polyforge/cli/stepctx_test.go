package cli

// stepctx_test.go: `pf` inside a workflow step (PF_WI_ID set) — recall
// and memory show go to the daemon socket, remember posts the wrap
// summary to it, every other aihub verb is refused, and nothing ever reaches
// aihub (even with an api key in the environment).

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stepEnv answers a fake daemon serving /step/*, the env of a step
// invocation pointed at it (plus a live fake aihub that must stay
// untouched), the daemon's request log and the aihub's.
func stepEnv(t *testing.T) (map[string]string, *reqRecorder, *reqRecorder) {
	t.Helper()
	drec := &reqRecorder{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /step/memories/search", func(w http.ResponseWriter, r *http.Request) {
		drec.record(r)
		io.WriteString(w, `{"items":[{"id":"mem_1","type":"experience.debug","title":"sqlite busy","content_excerpt":"x"}],"total":1}`)
	})
	mux.HandleFunc("GET /step/memories/{id}", func(w http.ResponseWriter, r *http.Request) {
		drec.record(r)
		io.WriteString(w, `{"meta":{"id":"`+r.PathValue("id")+`","type":"experience.debug","title":"sqlite busy","visibility":"project"},"content":"the body\n"}`)
	})
	mux.HandleFunc("POST /step/memories", func(w http.ResponseWriter, r *http.Request) {
		drec.record(r)
		if strings.Contains(drec.last().Body, "second summary") {
			w.WriteHeader(409)
			io.WriteString(w, `{"code":"DAEMON-409","detail":"this step already stored its summary mem_9; summaries are not edited — put corrections into the one call"}`)
			return
		}
		io.WriteString(w, `{"id":"mem_9","type":"experience.wrap","visibility":"project"}`)
	})
	mux.HandleFunc("POST /step/result", func(w http.ResponseWriter, r *http.Request) {
		drec.record(r)
		if strings.Contains(drec.last().Body, `"wi_id":"wi_other"`) {
			w.WriteHeader(403)
			io.WriteString(w, `{"code":"DAEMON-403","detail":"the calling step belongs to work item wi_st (the result names \"wi_other\")"}`)
			return
		}
		io.WriteString(w, `{"wi_id":"wi_st","step_id":"fix","spawn_n":3,"outcome":"completed"}`)
	})
	sock := newFakeDaemon(t, mux)
	arec := &reqRecorder{}
	srv := newFakeAihub(t, arec, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{}`) })
	env := map[string]string{"PF_WI_ID": "wi_st", "PF_DAEMON_ID": "m-test", "PF_DAEMON_SOCKET": sock, "PF_SERVER": srv.URL, "PF_API_KEY": "k_should_never_leave"}
	return env, drec, arec
}

func TestStepRecallAndShowGoThroughTheDaemon(t *testing.T) {
	env, drec, arec := stepEnv(t)
	// no cli.toml mapping is needed inside a step
	code, stdout, stderr := runPf(t, env, "recall", "--project", "polyforge", "--any", "--type", "experience.*", "sqlite busy")
	if code != 0 || !strings.Contains(stdout, "1 hit(s)") || !strings.Contains(stdout, "mem_1") {
		t.Fatalf("recall: exit %d %s %s", code, stdout, stderr)
	}
	q := drec.last()
	if !strings.Contains(q.Path, "match=any") || !strings.Contains(q.Path, "project=polyforge") || !strings.Contains(q.Path, "q=sqlite+busy") {
		t.Fatalf("daemon query = %s", q.Path)
	}
	code, stdout, stderr = runPf(t, env, "memory", "show", "mem_1")
	if code != 0 || !strings.Contains(stdout, "id: mem_1") || !strings.Contains(stdout, "the body") {
		t.Fatalf("memory show: exit %d %s %s", code, stdout, stderr)
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("a step invocation reached aihub %d time(s)", n)
	}
}

func TestStepRememberGoesToTheDaemon(t *testing.T) {
	env, drec, arec := stepEnv(t)
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte("做了什么\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runPf(t, env, "remember", "--project", "polyforge", "--type", "experience.wrap", "--work-item", "polyforge#7",
		"--title", "polyforge#7: did it", "--tags", "policy-proposal", "@"+body)
	if code != 0 || !strings.Contains(stdout, "stored mem_9") {
		t.Fatalf("remember: exit %d %s %s", code, stdout, stderr)
	}
	sent := drec.last()
	for _, want := range []string{`"type":"experience.wrap"`, `"title":"polyforge#7: did it"`, `"tags":["policy-proposal"]`, `"content":"做了什么\n"`, `"project":"polyforge"`, `"work_item":"polyforge#7"`} {
		if sent.Method != "POST" || !strings.Contains(sent.Body, want) {
			t.Fatalf("daemon request %s %s lacks %s", sent.Method, sent.Body, want)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--type", "experience.debug", "--title", "t", "b"}, "writes only its experience.wrap summary"},
		{[]string{"--type", "experience.wrap", "b"}, "a title is required"},
		{[]string{"--type", "experience.wrap", "--title", "t", "token ghp_abcdefghijklmnopqrstuvwxyz0123"}, "never the value"},
		{[]string{"--type", "experience.wrap", "--title", "t", "--visibility", "public", "b"}, "project-visible"},
	} {
		n := len(drec.all())
		args := append([]string{"remember", "--project", "polyforge"}, tc.args...)
		if code, _, stderr := runPf(t, env, args...); code != 1 || !strings.Contains(stderr, tc.want) {
			t.Fatalf("%v: exit %d %s, want %q", tc.args, code, stderr, tc.want)
		}
		if len(drec.all()) != n {
			t.Fatalf("%v: a refused memory reached the daemon", tc.args)
		}
	}
	// a second, different summary: the daemon's refusal is printed
	if code, _, stderr := runPf(t, env, "remember", "--project", "polyforge", "--type", "experience.wrap", "--title", "t", "second summary"); code != 1 || !strings.Contains(stderr, "already stored its summary mem_9") {
		t.Fatalf("second summary: exit %d %s", code, stderr)
	}
	// the daemon unreachable: a clear error, nothing else
	env["PF_DAEMON_SOCKET"] = filepath.Join(t.TempDir(), "gone.sock")
	if code, _, stderr := runPf(t, env, "remember", "--project", "polyforge", "--type", "experience.wrap", "--title", "t", "b"); code != 1 || !strings.Contains(stderr, "not stored (the step goes on)") {
		t.Fatalf("daemon down: exit %d %s", code, stderr)
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("a step remember reached aihub %d time(s)", n)
	}
}

func TestStepRefusesOtherAihubVerbs(t *testing.T) {
	env, _, arec := stepEnv(t)
	for _, argv := range [][]string{
		{"file", "create", "--project", "polyforge", "--goal", "g"},
		{"queue", "--project", "polyforge"},
		{"cancel", "wi_x"},
		{"projects", "list"},
		{"users", "list"},
	} {
		code, _, stderr := runPf(t, env, argv...)
		if code != 1 || !strings.Contains(stderr, "DAEMON-403") || !strings.Contains(stderr, "refused inside a workflow step") {
			t.Fatalf("%v: exit %d %s", argv, code, stderr)
		}
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("refused verbs reached aihub %d time(s)", n)
	}
	// outside a step the same remember goes to aihub as before
	delete(env, "PF_WI_ID")
	for k, v := range withMappingConfig(t) {
		env[k] = v
	}
	if code, _, stderr := runPf(t, env, "remember", "--project", "polyforge", "--type", "fact.note", "x"); code != 0 || len(arec.all()) != 1 {
		t.Fatalf("outside a step: exit %d %s, aihub calls %d", code, stderr, len(arec.all()))
	}
}

func TestStepModeNeedsBothMarkersAndRefusesDoctor(t *testing.T) {
	env, _, arec := stepEnv(t)
	code, _, stderr := runPf(t, env, "doctor")
	if code != 1 || !strings.Contains(stderr, "pf doctor refused inside a workflow step") || !strings.Contains(stderr, "PF_WI_ID=wi_st and PF_DAEMON_ID=m-test are set") {
		t.Fatalf("doctor in a step: exit %d %s", code, stderr)
	}
	if len(arec.all()) != 0 {
		t.Fatal("doctor in a step reached aihub")
	}
	// PF_WI_ID alone does not switch step mode on: recall then goes to
	// aihub directly (and is refused locally for the missing mapping)
	env["PF_DAEMON_ID"] = ""
	if code, _, stderr := runPf(t, env, "recall", "--project", "polyforge", "x"); code != 1 || !strings.Contains(stderr, "no [workspace] mapping") {
		t.Fatalf("PF_WI_ID alone: exit %d %s", code, stderr)
	}
}

// TestStepResultSubmitGoesToTheDaemon: `pf result submit` posts the
// payload verbatim to the daemon's /step/result (inline or @file), a
// daemon refusal and an invalid JSON payload exit non-zero, and nothing
// reaches aihub.
func TestStepResultSubmitGoesToTheDaemon(t *testing.T) {
	env, drec, arec := stepEnv(t)
	code, stdout, stderr := runPf(t, env, "result", "submit", `{"outcome":"completed","output":{"a":1}}`)
	if code != 0 || !strings.Contains(stdout, "delivered completed result of wi_st step fix (spawn 3)") {
		t.Fatalf("submit: exit %d %s %s", code, stdout, stderr)
	}
	if got := drec.last(); got.Method != "POST" || got.Path != "/step/result" || got.Body != `{"outcome":"completed","output":{"a":1}}` {
		t.Fatalf("daemon request = %+v", got)
	}
	file := filepath.Join(t.TempDir(), "r.json")
	if err := os.WriteFile(file, []byte(`{"outcome":"needs_input","output":{},"question_id":"q1","question":"?"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := runPf(t, env, "result", "submit", "@"+file); code != 0 || !strings.Contains(drec.last().Body, `"question_id":"q1"`) {
		t.Fatalf("@file submit: exit %d %s %s", code, stdout, stderr)
	}
	n := len(drec.all())
	if code, _, stderr := runPf(t, env, "result", "submit", `{"outcome":`); code == 0 || !strings.Contains(stderr, "not valid JSON") || len(drec.all()) != n {
		t.Fatalf("invalid JSON: exit %d %s", code, stderr)
	}
	if code, _, stderr := runPf(t, env, "result", "submit", `{"wi_id":"wi_other","outcome":"completed","output":{}}`); code != 1 || !strings.Contains(stderr, "DAEMON-403") || !strings.Contains(stderr, "belongs to work item wi_st") {
		t.Fatalf("refused submit: exit %d %s", code, stderr)
	}
	if code, _, stderr := runPf(t, env, "result"); code == 0 || !strings.Contains(stderr, "expected submit") {
		t.Fatalf("bare result: exit %d %s", code, stderr)
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("a step invocation reached aihub %d time(s)", n)
	}
}
