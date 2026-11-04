package daemon

// step_bar_run_test.go: the deliver step's bar-run record entry
// only the calling work item's deliver step is served,
// the cwd must be one task repo of its own capsule, the daemon records the
// repo HEAD it reads itself and the caller's spawn ordinal, and the record
// is a file under the data dir that outlives the request (and the daemon).

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

// barRunRepoFixture makes repos/<name> a git repo with one commit in the
// work item's capsule and answers its dir and HEAD.
func barRunRepoFixture(t *testing.T, capDir, name string) (string, string) {
	t.Helper()
	dir := filepath.Join(capDir, "repos", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		c := exec.Command("git", args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("-c", "user.name=t", "-c", "user.email=t@invalid", "commit", "-q", "-m", "base")
	return dir, run("rev-parse", "HEAD")
}

func TestStepBarRunRecordsTheDeliverStep(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_br1")
	ji, _ := td.rt.Info("wi_br1")
	dir, head := barRunRepoFixture(t, ji.CapsuleDir, "demo")
	td.stepOriginOf = func(*http.Request) (stepJob, string) { return stepJob{wi: "wi_br1", step: "deliver", attempt: 7}, "" }

	body := `{"argv":["go test ./... -run '^(A|B)$'"],"cwd":"` + dir + `","exit":0,"duration_ms":42}`
	if code, answer := stepDo(t, td.sock, "POST", "/step/bar-run", body); code != 200 || !strings.Contains(answer, head) {
		t.Fatalf("deliver bar run = %d %s", code, answer)
	}
	failed := `{"argv":["npm test"],"cwd":"` + dir + `","exit":3,"duration_ms":5}`
	if code, answer := stepDo(t, td.sock, "POST", "/step/bar-run", failed); code != 200 {
		t.Fatalf("a failed bar run is recorded too: %d %s", code, answer)
	}
	got, err := driver.ReadBarRuns(td.cfg.DataDir, "wi_br1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("record = %+v", got)
	}
	e := got[0]
	if e.Repo != "demo" || e.SpawnN != 7 || e.Head != head || e.Exit != 0 || e.DurationMS != 42 ||
		len(e.Argv) != 1 || e.Argv[0] != "go test ./... -run '^(A|B)$'" || e.At == "" {
		t.Fatalf("entry = %+v (the argv must be the carried line byte-identically, the head the repo's)", e)
	}
	if got[1].Exit != 3 {
		t.Fatalf("the failed run keeps its exit: %+v", got[1])
	}
	// the record is a plain file under the data dir: it survives the
	// request, the spawn and the daemon (a restarted daemon reads the same
	// path)
	if _, err := os.Stat(filepath.Join(td.cfg.DataDir, "bar-runs", "wi_br1.jsonl")); err != nil {
		t.Fatalf("record file: %v", err)
	}
}

func TestStepBarRunRefusals(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_br2")
	ji, _ := td.rt.Info("wi_br2")
	dir, _ := barRunRepoFixture(t, ji.CapsuleDir, "demo")
	ok := `{"argv":["npm test"],"cwd":"` + dir + `","exit":0,"duration_ms":1}`
	for _, step := range []string{"code", "verification", "review"} {
		td.stepOriginOf = func(*http.Request) (stepJob, string) { return stepJob{wi: "wi_br2", step: step, attempt: 3}, "" }
		if code, answer := stepDo(t, td.sock, "POST", "/step/bar-run", ok); code != 403 || !strings.Contains(answer, "deliver step only") {
			t.Fatalf("step %s: %d %s", step, code, answer)
		}
	}
	td.stepOriginOf = func(*http.Request) (stepJob, string) { return stepJob{wi: "wi_br2", step: "deliver", attempt: 3}, "" }
	for _, tc := range []struct{ body, want string }{
		{`{"argv":["npm test"],"cwd":"` + ji.CapsuleDir + `","exit":0}`, "not a task repo"},
		{`{"argv":["npm test"],"cwd":"` + t.TempDir() + `","exit":0}`, "not a task repo"},
		{`{"argv":["npm test"],"cwd":"` + filepath.Join(dir, "sub") + `","exit":0}`, "does not exist"},
		{`{"argv":["npm test"],"cwd":"repos/demo","exit":0}`, "not an absolute path"},
		{`{"argv":["npm","test"],"cwd":"` + dir + `","exit":0}`, "exactly one"},
		{`{"argv":["npm test"],"cwd":"` + dir + `"}`, "its exit"},
	} {
		if code, answer := stepDo(t, td.sock, "POST", "/step/bar-run", tc.body); code != 400 || !strings.Contains(answer, tc.want) {
			t.Fatalf("%s: %d %s, want 400 %q", tc.body, code, answer, tc.want)
		}
	}
	td.stepOriginOf = nil // the real resolver: this test process (a human session) is no step
	if code, answer := stepDo(t, td.sock, "POST", "/step/bar-run", ok); code != 403 || !strings.Contains(answer, "serves step agents only") {
		t.Fatalf("non-step caller: %d %s", code, answer)
	}
	if got, _ := driver.ReadBarRuns(td.cfg.DataDir, "wi_br2"); len(got) != 0 {
		t.Fatalf("a refused bar run was recorded: %+v", got)
	}
}
