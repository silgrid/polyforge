package driver

// testfiles_test.go: the committed-test-files gate (polyforge#156) over
// real temp git repos — a completed result whose output declares
// test_files is accepted only when every path is committed and clean in
// its task repo; anything else is an envelope rejection.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// gitRepo creates <capsule>/repos/<name> as a git repo with one commit
// holding the given files.
func gitRepo(t *testing.T, capsuleDir, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(capsuleDir, capsule.DirRepos, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-q")
	for rel, body := range files {
		writeFile(t, dir, rel, body)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "seed")
	return dir
}

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func testFilesResult(files ...string) *StepResult {
	out, _ := json.Marshal(map[string]any{"scenarios_path": "artifacts/e2e-scenarios.md", "test_files": files})
	return &StepResult{WIID: "wi_tf", StepID: "e2e-author", SpawnN: 1, Outcome: OutcomeCompleted, Output: out}
}

func testFilesSpec(capsuleDir string) ResultSpec {
	return ResultSpec{WIID: "wi_tf", StepID: "e2e-author", SpawnN: 1, ReposDir: filepath.Join(capsuleDir, capsule.DirRepos)}
}

func TestTestFilesAllCommittedAccepted(t *testing.T) {
	caps := t.TempDir()
	gitRepo(t, caps, "app", map[string]string{"e2e/a_test.go": "a", "e2e/b_test.go": "b"})
	gitRepo(t, caps, "lib", map[string]string{"x_test.go": "x"})
	res := testFilesResult("e2e/a_test.go", "./e2e/b_test.go", "x_test.go")
	if err := ValidateStepResult(res, testFilesSpec(caps)); err != nil {
		t.Fatalf("committed test files must be accepted: %v", err)
	}
}

func TestTestFilesUntrackedRejected(t *testing.T) {
	caps := t.TempDir()
	dir := gitRepo(t, caps, "app", map[string]string{"e2e/a_test.go": "a"})
	writeFile(t, dir, "e2e/new_test.go", "new")
	err := ValidateStepResult(testFilesResult("e2e/a_test.go", "e2e/new_test.go"), testFilesSpec(caps))
	if err == nil {
		t.Fatal("an untracked test file must be rejected")
	}
	msg := err.Error()
	for _, want := range []string{"e2e/new_test.go (not committed at HEAD in repos/app)", "BEFORE writing the result envelope", "git add -f"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("rejection must contain %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "e2e/a_test.go (") {
		t.Fatalf("the committed path must not be named as a problem: %s", msg)
	}
}

func TestTestFilesStagedOnlyRejected(t *testing.T) {
	caps := t.TempDir()
	dir := gitRepo(t, caps, "app", nil)
	writeFile(t, dir, "e2e/new_test.go", "new")
	runGit(t, dir, "add", "e2e/new_test.go")
	err := ValidateStepResult(testFilesResult("e2e/new_test.go"), testFilesSpec(caps))
	if err == nil || !strings.Contains(err.Error(), "e2e/new_test.go (not committed at HEAD in repos/app)") {
		t.Fatalf("a staged-but-uncommitted test file must be rejected by name: %v", err)
	}
}

// A committed file with a later worktree edit passes: HEAD is what a
// resume restores, and the gate deliberately never scans the worktree
// (no git status in an untrusted repo).
func TestTestFilesModifiedAfterCommitAccepted(t *testing.T) {
	caps := t.TempDir()
	dir := gitRepo(t, caps, "app", map[string]string{"e2e/a_test.go": "a"})
	writeFile(t, dir, "e2e/a_test.go", "edited after commit")
	if err := ValidateStepResult(testFilesResult("e2e/a_test.go"), testFilesSpec(caps)); err != nil {
		t.Fatalf("a path committed at HEAD must be accepted: %v", err)
	}
}

// The same path in two repos: committed in one is not enough when the
// other holds an uncommitted copy.
func TestTestFilesMultiRepoSamePathRejected(t *testing.T) {
	caps := t.TempDir()
	gitRepo(t, caps, "a", map[string]string{"e2e/s_test.go": "a"})
	b := gitRepo(t, caps, "b", nil)
	writeFile(t, b, "e2e/s_test.go", "b")
	err := ValidateStepResult(testFilesResult("e2e/s_test.go"), testFilesSpec(caps))
	if err == nil || !strings.Contains(err.Error(), "e2e/s_test.go (not committed at HEAD in repos/b)") {
		t.Fatalf("an uncommitted copy in a second repo must be rejected by name: %v", err)
	}
	if strings.Contains(err.Error(), "repos/a") {
		t.Fatalf("the repo holding the committed copy must not be named: %v", err)
	}
}

func TestTestFilesMissingRejected(t *testing.T) {
	caps := t.TempDir()
	gitRepo(t, caps, "app", map[string]string{"e2e/a_test.go": "a"})
	err := ValidateStepResult(testFilesResult("e2e/ghost_test.go"), testFilesSpec(caps))
	if err == nil || !strings.Contains(err.Error(), "e2e/ghost_test.go (does not exist in any task repo)") {
		t.Fatalf("a nonexistent test file must be rejected by name: %v", err)
	}
	// Not repo-relative, or the retired repos/<name>/ form.
	for _, p := range []string{"../escape_test.go", "/abs/x_test.go", "repos/app/e2e/a_test.go", "app/e2e/a_test.go"} {
		if err := ValidateStepResult(testFilesResult(p), testFilesSpec(caps)); err == nil || !strings.Contains(err.Error(), p) {
			t.Fatalf("%s must be rejected by name: %v", p, err)
		}
	}
}

// TestTestFilesHostileRepoConfigNotExecuted: the task repo is agent-
// controlled. A repo-local fsmonitor and a clean filter that a worktree
// scan would run as the daemon must not run during the gate.
func TestTestFilesHostileRepoConfigNotExecuted(t *testing.T) {
	caps := t.TempDir()
	dir := gitRepo(t, caps, "app", map[string]string{"e2e/a_test.go": "a"})
	marks := t.TempDir()
	fsmon := filepath.Join(marks, "fsmonitor.sh")
	if err := os.WriteFile(fsmon, []byte("#!/bin/sh\ntouch "+filepath.Join(marks, "fsmonitor-ran")+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "config", "core.fsmonitor", fsmon)
	runGit(t, dir, "config", "filter.evil.clean", "touch "+filepath.Join(marks, "filter-ran")+"; cat")
	writeFile(t, dir, ".gitattributes", "* filter=evil\n")
	writeFile(t, dir, "e2e/a_test.go", "b") // same size: a scan must hash it through the filter
	writeFile(t, dir, "e2e/new_test.go", "new")

	err := ValidateStepResult(testFilesResult("e2e/a_test.go", "e2e/new_test.go"), testFilesSpec(caps))
	if err == nil || !strings.Contains(err.Error(), "e2e/new_test.go (not committed at HEAD in repos/app)") {
		t.Fatalf("the gate must still run and reject the untracked path: %v", err)
	}
	for _, m := range []string{"fsmonitor-ran", "filter-ran"} {
		if _, serr := os.Stat(filepath.Join(marks, m)); serr == nil {
			t.Fatalf("hostile repo config executed during the gate: %s", m)
		}
	}
	// Control: the planted config is live — a worktree scan does run it.
	runGit(t, dir, "status", "--porcelain")
	for _, m := range []string{"fsmonitor-ran", "filter-ran"} {
		if _, serr := os.Stat(filepath.Join(marks, m)); serr != nil {
			t.Fatalf("control: git status should have run the planted %s (test setup is not hostile)", m)
		}
	}
}

func TestTestFilesEmptyUnaffected(t *testing.T) {
	caps := t.TempDir() // no repos/ at all
	spec := testFilesSpec(caps)
	for _, out := range []string{`{}`, `{"test_files":[]}`, `{"test_files":null}`} {
		res := testFilesResult()
		res.Output = json.RawMessage(out)
		if err := ValidateStepResult(res, spec); err != nil {
			t.Fatalf("%s: no declared test files must be unaffected: %v", out, err)
		}
	}
	// Non-completed outcomes are never judged by this gate.
	res := testFilesResult("ghost_test.go")
	res.Outcome = OutcomeIncomplete
	if err := ValidateStepResult(res, spec); err != nil {
		t.Fatalf("incomplete must be unaffected: %v", err)
	}
}

// TestRunTestFilesRejectionUsesRetryLane: the gate rides the in-process
// envelope-rejection lane — the step is re-dispatched with the rejection
// (naming the path) as feedback, and a committed retry is accepted.
func TestRunTestFilesRejectionUsesRetryLane(t *testing.T) {
	d, sp, execPath := runEnv(t, StepSpec{ID: "e2e-author"})
	dir := gitRepo(t, d.wi.CapsuleDir, "app", map[string]string{"e2e/ok_test.go": "ok"})
	writeFile(t, dir, "e2e/loose_test.go", "loose")
	sp.script = []fakeEntry{
		done(`{"test_files":["e2e/loose_test.go"]}`),
		done(`{"test_files":["e2e/ok_test.go"]}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := len(sp.spawns); n != 2 {
		t.Fatalf("uncommitted test files must be re-dispatched once, got %d spawns", n)
	}
	if fb := sp.spawnAt(1).PriorRejection; !strings.Contains(fb, "e2e/loose_test.go") || !strings.Contains(fb, "commit") {
		t.Fatalf("feedback must name the uncommitted path: %q", fb)
	}
	if st := readExec(t, execPath).Steps["e2e-author"]; st.Status != capsule.StepDone {
		t.Fatalf("committed retry must be accepted: %v", st.Status)
	}
}
