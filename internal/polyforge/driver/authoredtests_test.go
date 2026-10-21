package driver

// authoredtests_test.go: the authored-tests immutability gate
// (polyforge#164 H4) over real temp git repos — a later step that
// changed a test file e2e-author/repro committed is rejected unless a
// human answered in its attempt chain; an approved change re-baselines.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// gitIn runs git in dir from a non-test goroutine (a fake step's act).
func gitIn(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %v: %s", args, err, out)
	}
	return nil
}

// editAndCommit is a fake step's work: rewrite rel and commit it.
func editAndCommit(t *testing.T, repo, rel, body string) func() {
	return func() {
		if err := os.WriteFile(filepath.Join(repo, rel), []byte(body), 0o644); err != nil {
			t.Error(err)
			return
		}
		if err := gitIn(repo, "commit", "-q", "-am", "edit "+rel); err != nil {
			t.Error(err)
		}
	}
}

func headOf(t *testing.T, repo string) string {
	t.Helper()
	out, err := repoGit(repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// The H4 scenario: code "fixes" a red scenario by weakening the authored
// test. The driver refuses the completion; restoring the test is
// accepted. A step-supplied baseline is never kept: the driver stamps
// the authored commit itself.
func TestRunRefusesChangedAuthoredTests(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("e2e-author"), spec("code"))
	repo := gitRepo(t, d.wi.CapsuleDir, "app", map[string]string{"t/a_test.go": "assert(strict)\n", "main.go": "v1\n"})
	authored := headOf(t, repo)
	sp.script = []fakeEntry{
		done(`{"test_files":["t/a_test.go"],"test_files_commit":{"app":"forged"}}`),
		{res: done(`{}`).res, act: editAndCommit(t, repo, "t/a_test.go", "assert(loose)\n")},
		{res: done(`{}`).res, act: editAndCommit(t, repo, "t/a_test.go", "assert(strict)\n")},
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sp.dispatched(); len(got) != 3 {
		t.Fatalf("the weakened-test completion must be rejected once, got %v", got)
	}
	fb := sp.spawnAt(2).PriorRejection
	for _, want := range []string{"changed authored test files", "repos/app: t/a_test.go", "needs_input"} {
		if !strings.Contains(fb, want) {
			t.Fatalf("feedback must contain %q: %q", want, fb)
		}
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusCompleted {
		t.Fatalf("restoring the authored test must be accepted: %s", exec.WorkflowStatus)
	}
	stamp, _ := exec.Steps["e2e-author"].Output[testFilesCommitKey].(map[string]any)
	if stamp["app"] != authored {
		t.Fatalf("the driver must stamp the authored commit %s (never the step's forged value): %v", authored, stamp)
	}
	if _, ok := exec.Steps["code"].Output[testFilesCommitKey]; ok {
		t.Fatalf("an unchanged-tests step carries no baseline stamp: %v", exec.Steps["code"].Output)
	}
}

// An authored test the code step believes wrong goes through needs_input:
// after a human answered in this attempt, the change is accepted and
// becomes the new baseline (stamped on the authoring step, so it also
// survives a later rewind of fix), and later steps are not refused for it.
func TestRunAuthoredTestChangeAcceptedAfterHumanAnswer(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("repro"), spec("fix"), spec("check"))
	repo := gitRepo(t, d.wi.CapsuleDir, "app", map[string]string{"t/r_test.go": "wrong expectation\n"})
	sp.script = []fakeEntry{
		done(`{"test_files":["t/r_test.go"]}`),
		asks("q_test", "the repro test expects the wrong value — may I correct it?"),
		{res: done(`{}`).res, act: editAndCommit(t, repo, "t/r_test.go", "right expectation\n")},
		done(`{}`), // check: judged against the new baseline
	}
	doneCh := make(chan error, 1)
	go func() { doneCh <- d.Run(context.Background()) }()
	waitUntil(t, "the fix step's question", func() bool {
		exec := tryReadExec(execPath)
		return exec != nil && exec.Steps["fix"].PendingQuestion != nil
	})
	if err := d.Answer("q_test", "yes, correct it", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := <-doneCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusCompleted || len(sp.dispatched()) != 4 {
		t.Fatalf("an approved change must not be refused anywhere: %s %v", exec.WorkflowStatus, sp.dispatched())
	}
	stamp, _ := exec.Steps["repro"].Output[testFilesCommitKey].(map[string]any)
	if stamp["app"] != headOf(t, repo) {
		t.Fatalf("the approved change must re-baseline the authoring step at the new HEAD (surviving a rewind of fix): %v", exec.Steps["repro"].Output)
	}
	if _, ok := exec.Steps["fix"].Output[testFilesCommitKey]; ok {
		t.Fatalf("the baseline lives on the authoring step, not the approving one: %v", exec.Steps["fix"].Output)
	}
	var rebased []any
	for _, e := range d.log.(*eventLog).entries {
		if e.kind == EvAuthoredTestsRebaselined {
			rebased, _ = e.body["files"].([]any)
			if files, ok := e.body["files"].([]string); ok {
				for _, f := range files {
					rebased = append(rebased, f)
				}
			}
		}
	}
	if len(rebased) != 1 || rebased[0] != "repos/app: t/r_test.go" {
		t.Fatalf("the approved re-baseline must be recorded naming the files: %v", rebased)
	}
	if exec.Steps["check"].Status != capsule.StepDone {
		t.Fatalf("check must complete against the new baseline: %+v", exec.Steps["check"])
	}
}
