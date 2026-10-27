package driver

// deliver_merge_before_evidence_repro_test.go: the repro of
// polyforge#203 (2026-10-18). Four work items delivered that day
// (polyforge#196, polyforge#199, polyforge#202 and pi#108) all ran
// `gh pr merge` on their delivery PR BEFORE the evidence the merge
// rule requires — the test-bar re-run shown on the pushed head after
// the last origin/main merge — existed. The driver refused the
// completed envelope only once the merge had already happened (a PR
// merge cannot be undone), so every one of the four needed a human
// deliver-merge sanction after the fact, with full re-verification
// green each time.
//
// The mechanical hole this test pins: the driver judges a deliver
// envelope at completion time, from its recorded command evidence,
// and testBarRerun checks only that the test-bar commands appear in
// that evidence AFTER the dispatch's last `git merge`/`git pull` — it
// never looks at where `gh pr merge` itself sits in the recorded
// order. A dispatch that merges the PR FIRST and re-runs the test bar
// afterwards therefore passes the merge rule exactly as if the
// evidence had preceded the merge: the completed envelope below is
// ACCEPTED, the ordering the rule means to require ("tests are
// covered on the pushed head ... then merge") is invisible to the
// machinery that is supposed to enforce it.
//
// Expected per the WI: the lane must finish the post-merge test-bar
// re-run and record its evidence BEFORE `gh pr merge` executes — a
// completed envelope whose recorded evidence shows the PR merge
// running before the test-bar re-run must be refused.
//
// The first subtest fails before the fix, for exactly the reported
// reason; the other two pin the behaviour around it.

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDeliverPRMergeBeforeTestBarEvidence: a deliver dispatch's
// recorded command evidence must show the test-bar re-run (on the
// pushed head, after the last origin/main merge) BEFORE the
// `gh pr merge` that consumes it.
func TestDeliverPRMergeBeforeTestBarEvidence(t *testing.T) {
	// The incident's shape: verification passed on the pre-merge head;
	// origin/main moved ahead (another PR landed); deliver merged it
	// into the feature branch and pushed; the PR merge landed on
	// origin main — what `gh pr merge --merge` did in all four WIs,
	// before their test-bar re-run evidence existed.
	repos := deliverCapsule(t)
	spec := deliverSpec(repos, true, 0)
	dir := filepath.Join(repos, "demo")
	origin := filepath.Join(filepath.Dir(filepath.Dir(repos)), "origin.git")
	other := filepath.Join(t.TempDir(), "other")
	tgit(t, filepath.Dir(other), "clone", "-q", origin, other)
	tgit(t, other, "commit", "-q", "--allow-empty", "-m", "another PR merged to main")
	tgit(t, other, "push", "-q", "origin", "HEAD:main")
	tgit(t, dir, "fetch", "-q", "origin")
	tgit(t, dir, "merge", "-q", "--no-edit", "origin/main")
	pushFeature(t, dir)
	tgit(t, other, "fetch", "-q", "origin")
	tgit(t, other, "merge", "-q", "--no-ff", "-m", "Merge pull request #1", "origin/polyforge/polyforge-166")
	tgit(t, other, "push", "-q", "origin", "HEAD:main")
	tgit(t, dir, "fetch", "-q", "origin")
	out := strings.Replace(mergedOut, `"merge_commit":"abc"`, `"merge_commit":"`+tgit(t, other, "rev-parse", "HEAD")+`"`, 1)

	const prMerge = "gh pr merge https://github.com/o/r/pull/1 --merge"
	const mergeMain = "cd repos/demo && git merge --no-edit origin/main"
	const rerun = "cd repos/demo && make test"

	t.Run("PR merged before the test-bar re-run is refused", func(t *testing.T) {
		// the incident's recorded order: merge origin/main, push,
		// `gh pr merge` — and only then the test-bar re-run.
		evidence := ev(mergeMain, goodPush, prMerge, rerun)
		err := ValidateStepResult(deliverRes(OutcomeCompleted, out, evidence), spec)
		if err == nil {
			t.Fatal("REPORTED FAILURE (polyforge#203): a completed deliver whose recorded evidence shows `gh pr merge` running BEFORE the test-bar re-run on the pushed head was accepted — the test-bar re-run and its evidence must exist before the PR merge executes, not catch up after it")
		}
	})
	t.Run("test-bar re-run before the PR merge stays accepted", func(t *testing.T) {
		evidence := ev(mergeMain, rerun, goodPush, prMerge)
		if err := ValidateStepResult(deliverRes(OutcomeCompleted, out, evidence), spec); err != nil {
			t.Fatalf("a delivery that recorded the test-bar re-run before merging its PR was refused: %v", err)
		}
	})
	t.Run("PR merged with no test-bar re-run stays refused", func(t *testing.T) {
		// what the four WIs' first completed attempts looked like to
		// the driver: refused — but only once the merge had already
		// happened, which is why each needed a human deliver-merge
		// sanction after the fact.
		evidence := ev(mergeMain, goodPush, prMerge)
		err := ValidateStepResult(deliverRes(OutcomeCompleted, out, evidence), spec)
		if err == nil {
			t.Fatal("a completed deliver that merged its PR without any test-bar re-run was accepted")
		}
	})
}
