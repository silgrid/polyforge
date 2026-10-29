package verifyrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// trivialPlan is an engine answering one passing full command.
func trivialPlan(t *testing.T, repos ...string) string {
	t.Helper()
	reps := map[string]any{}
	for _, r := range repos {
		reps[r] = report("full", cmdOf("full-1", "full", "cat a.txt"))
	}
	return fakeEngine(t, reps)
}

// TestUntouchedRepoUpToDate: a repo with no commit beyond its default
// branch merges as a no-op, is recorded untouched (skipped) and runs no
// bar — the engine is never asked.
func TestUntouchedRepoUpToDate(t *testing.T) {
	f := newFixture(t, map[string]map[string]string{"app": {"a.txt": "base\n"}})
	eng := trivialPlan(t, "app")
	res := runFixture(t, f, eng, nil)
	m := res.MergeOutcomes["app"]
	if m == nil || m.Outcome != MergeUpToDate || m.Touched || m.DefaultBranch != "main" {
		t.Fatalf("merge = %+v", m)
	}
	if c := checkOf(t, res, "app:untouched"); c.Status != "skipped" {
		t.Errorf("untouched check = %+v", c)
	}
	if len(res.TestBar) != 0 {
		t.Errorf("an untouched repo has no bar, got %v", res.TestBar)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(eng), "args.log")); err == nil {
		t.Error("the engine must not be asked about an untouched repo")
	}
	if res.MechanicalVerdict != "pass" {
		t.Errorf("verdict = %q", res.MechanicalVerdict)
	}
}

// TestUntouchedRepoFastForward: the default branch moved, the repo has
// nothing of its own — a fast-forward, still untouched.
func TestUntouchedRepoFastForward(t *testing.T) {
	f := newFixture(t, map[string]map[string]string{"app": {"a.txt": "base\n"}})
	f.advanceOrigin(t, "app", map[string]string{"b.txt": "upstream\n"}, "upstream")
	res := runFixture(t, f, trivialPlan(t, "app"), nil)
	if m := res.MergeOutcomes["app"]; m.Outcome != MergeFastForward || m.Touched {
		t.Fatalf("merge = %+v", m)
	}
	checkOf(t, res, "app:untouched")
}

// TestTouchedRepoMergeCommit: the branch's own commit plus a moved default
// branch is a machine-made merge commit; the engine is asked with the
// merged-in default tip as its base.
func TestTouchedRepoMergeCommit(t *testing.T) {
	f := newFixture(t, map[string]map[string]string{"app": {"a.txt": "base\n"}})
	f.commitInRepo(t, "app", map[string]string{"c.txt": "mine\n"}, "feature")
	f.advanceOrigin(t, "app", map[string]string{"b.txt": "upstream\n"}, "upstream")
	tip := gitT(t, f.seeds["app"], "rev-parse", "HEAD")
	eng := trivialPlan(t, "app")
	res := runFixture(t, f, eng, nil)
	m := res.MergeOutcomes["app"]
	if m.Outcome != MergeCommit || !m.Touched || m.Base != tip {
		t.Fatalf("merge = %+v (want base %s)", m, tip)
	}
	if got := gitT(t, f.repo("app"), "rev-list", "--parents", "-n", "1", "HEAD"); len(strings.Fields(got)) != 3 {
		t.Errorf("HEAD is not a two-parent merge: %s", got)
	}
	args, _ := os.ReadFile(filepath.Join(filepath.Dir(eng), "args.log"))
	if !strings.Contains(string(args), "--json --full --base "+tip) {
		t.Errorf("engine args = %q, want --json --full --base %s", args, tip)
	}
	if c := checkOf(t, res, "app:full-1"); c.Status != "pass" {
		t.Errorf("bar check = %+v", c)
	}
	var sawFetch, sawMerge bool
	for _, e := range res.CommandEvidence {
		j := strings.Join(e.Argv, " ")
		sawFetch = sawFetch || j == "git fetch origin"
		sawMerge = sawMerge || j == "git merge --no-edit origin/main"
	}
	if !sawFetch || !sawMerge {
		t.Errorf("fetch/merge evidence missing: %+v", res.CommandEvidence)
	}
}

// TestConflictAbortedRunsOnUnmergedTree: a conflicting default branch is
// aborted, the pre-merge tree confirmed restored, and the bar proves the
// unmerged tree.
func TestConflictAbortedRunsOnUnmergedTree(t *testing.T) {
	f := newFixture(t, map[string]map[string]string{"app": {"a.txt": "base\n"}})
	pre := f.commitInRepo(t, "app", map[string]string{"a.txt": "mine\n"}, "feature")
	f.advanceOrigin(t, "app", map[string]string{"a.txt": "theirs\n"}, "upstream")
	res := runFixture(t, f, trivialPlan(t, "app"), nil)
	m := res.MergeOutcomes["app"]
	if m.Outcome != MergeConflictAborted || m.Head != pre || m.PreHead != pre || !m.Touched {
		t.Fatalf("merge = %+v", m)
	}
	if mergeInProgress(t.Context(), f.repo("app")) {
		t.Fatal("MERGE_HEAD survived the abort")
	}
	if res.Incomplete != "" {
		t.Fatalf("a conflict is not incomplete: %s", res.Incomplete)
	}
	log := filepath.Join(f.capsule, res.Repos[0].Commands[0].LogPath)
	raw, _ := os.ReadFile(log)
	if !strings.Contains(string(raw), "mine") || strings.Contains(string(raw), "theirs") {
		t.Errorf("the bar did not run on the unmerged tree:\n%s", raw)
	}
}

// TestMergeHeadSelfHeal: a merge an interrupted dispatch left in progress
// is aborted first, then the run proceeds normally.
func TestMergeHeadSelfHeal(t *testing.T) {
	f := newFixture(t, map[string]map[string]string{"app": {"a.txt": "base\n"}})
	f.commitInRepo(t, "app", map[string]string{"a.txt": "mine\n"}, "feature")
	f.advanceOrigin(t, "app", map[string]string{"a.txt": "theirs\n"}, "upstream")
	gitT(t, f.repo("app"), "fetch", "-q", "origin")
	_ = runGitAllowFail(f.repo("app"), "merge", "--no-edit", "origin/main")
	if !mergeInProgress(t.Context(), f.repo("app")) {
		t.Fatal("setup: no merge in progress")
	}
	res := runFixture(t, f, trivialPlan(t, "app"), nil)
	m := res.MergeOutcomes["app"]
	if !m.SelfHealed || m.Outcome != MergeConflictAborted {
		t.Fatalf("merge = %+v", m)
	}
	if res.CommandEvidence[0].Argv[1] != "merge" || res.CommandEvidence[0].Argv[2] != "--abort" {
		t.Errorf("first evidence = %v, want the self-heal abort", res.CommandEvidence[0].Argv)
	}
}

// TestFetchFailureIsIncomplete: no fetch, no merged-tree proof: the
// result is incomplete and no bar runs.
func TestFetchFailureIsIncomplete(t *testing.T) {
	f := newFixture(t, map[string]map[string]string{"app": {"a.txt": "base\n"}})
	f.commitInRepo(t, "app", map[string]string{"c.txt": "mine\n"}, "feature")
	gitT(t, f.repo("app"), "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	res := runFixture(t, f, trivialPlan(t, "app"), nil)
	if !strings.Contains(res.Incomplete, "git fetch origin") {
		t.Fatalf("incomplete = %q", res.Incomplete)
	}
	if len(res.Checks) != 0 || len(res.TestBar) != 0 || res.MechanicalVerdict != "" {
		t.Errorf("an incomplete run reports no verdict: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(f.capsule, res.OutDir, "result.json")); err != nil {
		t.Errorf("result.json must still be written: %v", err)
	}
}

// TestDefaultBranchFallback: without origin/HEAD the default branch is
// main, else master — always without the remote prefix.
func TestDefaultBranchFallback(t *testing.T) {
	f := newFixture(t, map[string]map[string]string{"app": {"a.txt": "base\n"}})
	dir := f.repo("app")
	if got := resolveDefault(t.Context(), dir); got != "main" {
		t.Fatalf("origin/HEAD default = %q", got)
	}
	gitT(t, dir, "remote", "set-head", "origin", "--delete")
	if got := resolveDefault(t.Context(), dir); got != "main" {
		t.Errorf("fallback = %q, want main", got)
	}
	gitT(t, dir, "update-ref", "refs/remotes/origin/master", "refs/remotes/origin/main")
	gitT(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
	if got := resolveDefault(t.Context(), dir); got != "master" {
		t.Errorf("fallback = %q, want master", got)
	}
}

func runGitAllowFail(dir string, args ...string) error {
	c := exec.Command("git", args...)
	c.Dir = dir
	return c.Run()
}
