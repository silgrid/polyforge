package cli

// stepctx_test.go: `pf` inside a workflow step (PF_WI_ID set) — `pf
// step recall | memory show | remember` are the step's memory face (one
// /step/call each), every data-plane verb — recall, memory and remember
// included — is refused, contract 3's `pf result` and `pf ask` are gone,
// and nothing ever reaches aihub (even with an api key in the
// environment).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stepEnv answers stepCallEnv's fake daemon (/step/call), a step
// invocation's env pointed at it, the daemon's request log and the
// untouched aihub's.
func stepEnv(t *testing.T) (map[string]string, *reqRecorder, *reqRecorder) {
	t.Helper()
	return stepCallEnv(t)
}

func TestStepRecallAndShowGoThroughTheDaemon(t *testing.T) {
	env, drec, arec := stepEnv(t)
	code, stdout, stderr := runPf(t, env, "step", "recall", "--any", "--type", "experience.*", "sqlite", "busy")
	if code != 0 || !strings.Contains(stdout, "1 hit(s)") || !strings.Contains(stdout, "mem_1") {
		t.Fatalf("step recall: exit %d %s %s", code, stdout, stderr)
	}
	if verb, args := lastCall(t, drec); verb != "recall" || args["q"] != "sqlite busy" || args["match"] != "any" || args["type"] != "experience.*" {
		t.Fatalf("the recall call = %s %v", verb, args)
	}
	code, stdout, stderr = runPf(t, env, "step", "memory", "show", "mem_1")
	if code != 0 || !strings.Contains(stdout, "id: mem_1") || !strings.Contains(stdout, "the body") {
		t.Fatalf("step memory show: exit %d %s %s", code, stdout, stderr)
	}
	if verb, args := lastCall(t, drec); verb != "memory.show" || args["id"] != "mem_1" {
		t.Fatalf("the memory show call = %s %v", verb, args)
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("a step invocation reached aihub %d time(s)", n)
	}
}

// TestStepRecallCannotBrowse: browse is the human
// direct-aihub shape. Inside a step `pf recall` is refused altogether and
// `pf step recall` without a query is refused locally — no daemon
// request, no aihub request; a task agent's browse is refused the same way.
func TestStepRecallCannotBrowse(t *testing.T) {
	env, drec, arec := stepEnv(t)
	for _, args := range [][]string{
		{"recall", "--project", "polyforge"},
		{"recall", "--project", "polyforge", "x", "--after", "cur-1"},
		{"memory", "show", "mem_1"},
	} {
		code, _, stderr := runPf(t, env, args...)
		if code != 1 || !strings.Contains(stderr, "refused inside a workflow step") || !strings.Contains(stderr, "pf step recall") {
			t.Fatalf("%v: exit %d %s, want the step-mode refusal naming pf step", args, code, stderr)
		}
	}
	if code, _, stderr := runPf(t, env, "step", "recall"); code != 1 || !strings.Contains(stderr, "a query is required") {
		t.Fatalf("step recall without a query: exit %d %s", code, stderr)
	}
	if n := len(drec.all()); n != 0 {
		t.Fatalf("a refused browse reached the daemon %d time(s)", n)
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("a step invocation reached aihub %d time(s)", n)
	}
	// Task mode refuses browse locally.
	env["PF_TASK_ID"] = "task_0123456789abcdef"
	delete(env, "PF_WI_ID")
	if code, _, stderr := runPf(t, env, "recall", "--project", "polyforge"); code != 1 || !strings.Contains(stderr, "human direct-aihub") {
		t.Fatalf("task browse: exit %d %s", code, stderr)
	}
	if n := len(drec.all()); n != 0 {
		t.Fatalf("a refused task browse reached the daemon %d time(s)", n)
	}
}

func TestStepRememberGoesToTheDaemon(t *testing.T) {
	env, drec, arec := stepEnv(t)
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte("做了什么\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runPf(t, env, "step", "remember", "--type", "experience.wrap",
		"--title", "polyforge#7: did it", "--tags", "policy-proposal", "@"+body)
	if code != 0 || !strings.Contains(stdout, "stored mem_9") {
		t.Fatalf("step remember: exit %d %s %s", code, stdout, stderr)
	}
	if verb, args := lastCall(t, drec); verb != "remember" || args["type"] != "experience.wrap" || args["title"] != "polyforge#7: did it" ||
		args["content"] != "做了什么\n" || len(args["tags"].([]any)) != 1 {
		t.Fatalf("the remember call = %s %v", verb, args)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--type", "experience.debug", "--title", "t", "b"}, "writes only its experience.wrap summary"},
		{[]string{"--type", "experience.wrap", "b"}, "a title is required"},
		{[]string{"--type", "experience.wrap", "--title", "t", "token ghp_abcdefghijklmnopqrstuvwxyz0123"}, "never the value"},
	} {
		n := len(drec.all())
		args := append([]string{"step", "remember"}, tc.args...)
		if code, _, stderr := runPf(t, env, args...); code != 1 || !strings.Contains(stderr, tc.want) {
			t.Fatalf("%v: exit %d %s, want %q", tc.args, code, stderr, tc.want)
		}
		if len(drec.all()) != n {
			t.Fatalf("%v: a refused memory reached the daemon", tc.args)
		}
	}
	// a second, different summary: the daemon's refusal is printed
	if code, _, stderr := runPf(t, env, "step", "remember", "--type", "experience.wrap", "--title", "t", "second summary"); code != 1 || !strings.Contains(stderr, "already stored its summary mem_9") {
		t.Fatalf("second summary: exit %d %s", code, stderr)
	}
	// `pf remember` itself is refused inside a step, naming `pf step remember`
	if code, _, stderr := runPf(t, env, "remember", "--project", "polyforge", "--type", "experience.wrap", "--title", "t", "b"); code != 1 || !strings.Contains(stderr, "pf step remember") {
		t.Fatalf("pf remember in a step: exit %d %s", code, stderr)
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
		{"recall", "--project", "polyforge", "x"},
		{"memory", "show", "mem_1"},
		{"remember", "--project", "polyforge", "--type", "experience.wrap", "--title", "t", "b"},
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

// TestRemovedStepVerbsAreUnknownCommands: contract 3's `pf result submit`
// and `pf ask` are gone — inside a step each is an unknown
// command, and nothing is sent to the daemon or aihub.
func TestRemovedStepVerbsAreUnknownCommands(t *testing.T) {
	env, drec, arec := stepEnv(t)
	for _, argv := range [][]string{{"result", "submit", `{"outcome":"completed","output":{"a":1}}`}, {"ask", "which?"}} {
		code, stdout, stderr := runPf(t, env, argv...)
		if code != 2 || !strings.Contains(stderr, "unknown command") {
			t.Fatalf("%v: exit %d %s %s", argv, code, stdout, stderr)
		}
	}
	if got := drec.all(); len(got) != 0 {
		t.Fatalf("a removed verb reached the daemon: %+v", got)
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("a step invocation reached aihub %d time(s)", n)
	}
}

// TestStepRefusesProfileFlag: inside a step or task --profile is refused —
// the Host routes an agent by its own ownership record.
func TestStepRefusesProfileFlag(t *testing.T) {
	for _, env := range [][2]string{{"PF_WI_ID", "wi_x"}, {"PF_TASK_ID", "task_x"}} {
		t.Run(env[0], func(t *testing.T) {
			t.Setenv(env[0], env[1])
			t.Setenv("PF_DAEMON_ID", "m-x")
			var stdout, stderr bytes.Buffer
			if code := Run([]string{"--profile", "alpha", "watch", "wi_x", "--once"}, nil, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "DAEMON-403") {
				t.Fatalf("exit %d (%s)", code, stderr.String())
			}
		})
	}
}
