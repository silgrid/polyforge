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

// authoredClone builds the verification-merge scene of the authored-tests
// gate: an origin repo on main holding a ten-line authored test, its clone
// at <capsule>/repos/app (so refs/remotes/origin/HEAD names the default
// branch), the authoring step's commit (line 1 edited — the baseline) and a
// code commit on top that never touches the test. It answers the capsule's
// repos dir, the clone, the origin and the baseline sha.
func authoredClone(t *testing.T) (reposDir, clone, origin, baseline string) {
	t.Helper()
	caps := t.TempDir()
	origin = filepath.Join(t.TempDir(), "origin")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "init", "-q", "-b", "main")
	writeFile(t, origin, "t/a_test.go", "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n")
	writeFile(t, origin, "main.go", "v1\n")
	runGit(t, origin, "add", "-A")
	runGit(t, origin, "commit", "-q", "-m", "seed")
	reposDir = filepath.Join(caps, capsule.DirRepos)
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, reposDir, "clone", "-q", origin, "app")
	clone = filepath.Join(reposDir, "app")
	writeFile(t, clone, "t/a_test.go", "authored\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n")
	runGit(t, clone, "commit", "-q", "-am", "author the test")
	baseline = headOf(t, clone)
	writeFile(t, clone, "main.go", "v2\n")
	runGit(t, clone, "commit", "-q", "-am", "code")
	return reposDir, clone, origin, baseline
}

// upstreamEditsTest is the default branch moving on: upstream edits the
// authored test's last line (a hunk the author's edit does not touch, so
// git merges it cleanly) and the clone fetches it.
func upstreamEditsTest(t *testing.T, clone, origin string) {
	t.Helper()
	writeFile(t, origin, "t/a_test.go", "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nupstream\n")
	runGit(t, origin, "commit", "-q", "-am", "upstream edits the test")
	runGit(t, clone, "fetch", "-q", "origin")
}

// TestAuthoredTestsUpstreamMergeIsNotAnAgentChange: verification merges the
// latest default branch before its bar (`git merge --no-edit
// origin/<default>`), so an authored test file upstream also edited reaches
// HEAD with a blob that differs from the author commit's. A change that came
// ONLY through machine-made merges of the default branch is not a change by
// the agent: the gate accepts it (restoring the author blob would undo
// upstream's edit). An agent's own edit, a side branch merged in to launder
// one, and a hand-made merge are still changes.
func TestAuthoredTestsUpstreamMergeIsNotAnAgentChange(t *testing.T) {
	at := func(baseline string) *AuthoredTests {
		return &AuthoredTests{Files: []string{"t/a_test.go"}, Baseline: map[string]string{"app": baseline}}
	}
	differs := func(t *testing.T, clone, baseline string) {
		t.Helper()
		b, _ := repoGit(clone, lsTreeArgs(baseline, []string{"t/a_test.go"})...)
		h, _ := repoGit(clone, lsTreeArgs("HEAD", []string{"t/a_test.go"})...)
		if b == h {
			t.Fatalf("control: the test blob at HEAD must differ from the baseline's (the case would be vacuous): %q", h)
		}
	}
	rejected := func(t *testing.T, reposDir string, baseline string) {
		t.Helper()
		changed, err := checkAuthoredTestsUnchanged(reposDir, at(baseline), 0)
		if err == nil || len(changed) != 1 || changed[0] != "repos/app: t/a_test.go" {
			t.Fatalf("the change must be rejected naming the file: changed=%v err=%v", changed, err)
		}
	}

	t.Run("upstream edit merged in is accepted", func(t *testing.T) {
		reposDir, clone, origin, baseline := authoredClone(t)
		upstreamEditsTest(t, clone, origin)
		runGit(t, clone, "merge", "-q", "--no-edit", "origin/main")
		differs(t, clone, baseline)
		changed, err := checkAuthoredTestsUnchanged(reposDir, at(baseline), 0)
		if err != nil || len(changed) != 0 {
			t.Fatalf("a test changed only by a machine-made merge of the default branch is not an agent change: changed=%v err=%v", changed, err)
		}
		// A later code commit (a rewind's fix on the merged HEAD) that leaves
		// the test alone keeps it accepted.
		writeFile(t, clone, "main.go", "v3\n")
		runGit(t, clone, "commit", "-q", "-am", "fix")
		if changed, err := checkAuthoredTestsUnchanged(reposDir, at(baseline), 0); err != nil || len(changed) != 0 {
			t.Fatalf("a non-merge commit that does not touch the test keeps the merge accepted: changed=%v err=%v", changed, err)
		}
	})

	t.Run("agent edit after the merge is rejected", func(t *testing.T) {
		reposDir, clone, origin, baseline := authoredClone(t)
		upstreamEditsTest(t, clone, origin)
		runGit(t, clone, "merge", "-q", "--no-edit", "origin/main")
		writeFile(t, clone, "t/a_test.go", "authored\nl2\nl3\nl4\nloosened\nl6\nl7\nl8\nl9\nupstream\n")
		runGit(t, clone, "commit", "-q", "-am", "agent loosens the test")
		rejected(t, reposDir, baseline)
	})

	t.Run("agent edit without any merge is rejected", func(t *testing.T) {
		reposDir, clone, _, baseline := authoredClone(t)
		writeFile(t, clone, "t/a_test.go", "authored\nl2\nl3\nl4\nloosened\nl6\nl7\nl8\nl9\nl10\n")
		runGit(t, clone, "commit", "-q", "-am", "agent loosens the test")
		rejected(t, reposDir, baseline)
	})

	t.Run("agent edit laundered through a side-branch merge is rejected", func(t *testing.T) {
		reposDir, clone, _, baseline := authoredClone(t)
		runGit(t, clone, "checkout", "-q", "-b", "side", baseline)
		writeFile(t, clone, "t/a_test.go", "authored\nl2\nl3\nl4\nloosened\nl6\nl7\nl8\nl9\nl10\n")
		runGit(t, clone, "commit", "-q", "-am", "agent loosens the test on a side branch")
		runGit(t, clone, "checkout", "-q", "main")
		runGit(t, clone, "merge", "-q", "--no-ff", "--no-edit", "side")
		differs(t, clone, baseline)
		rejected(t, reposDir, baseline)
	})

	t.Run("hand-made merge of the default branch is rejected", func(t *testing.T) {
		reposDir, clone, origin, baseline := authoredClone(t)
		upstreamEditsTest(t, clone, origin)
		runGit(t, clone, "merge", "-q", "--no-commit", "origin/main")
		writeFile(t, clone, "t/a_test.go", "authored\nl2\nl3\nl4\nloosened\nl6\nl7\nl8\nl9\nupstream\n")
		runGit(t, clone, "add", "t/a_test.go")
		runGit(t, clone, "commit", "-q", "--no-edit")
		rejected(t, reposDir, baseline)
	})
}
