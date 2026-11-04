package driver

// deliver_fast_lane_test.go: the merge rule of a delivery lane with no
// verification step (fastLaneMergeBlocker,): the review
// pass covering the delivered HEAD plus this deliver dispatch's own
// in-window, exit-0 re-run of every recorded fast-lane command — and the
// verification-bearing rule left exactly as it was.

import (
	"path/filepath"
	"strings"
	"testing"
)

const (
	flPR      = "https://github.com/o/r/pull/1"
	flFastCmd = "sh checks/fast.sh"
	flKeysCmd = "sh checks/keys.sh"
	flMerge   = "cd repos/demo && git merge --no-edit origin/main"
	flPRMerge = "cd repos/demo && gh pr merge " + flPR + " --merge"
)

// flSpec is a verification-less delivery of repos/demo: review passed on
// the current HEAD, the fast-lane plan recorded.
func flSpec(t *testing.T) ResultSpec {
	t.Helper()
	repos := deliverCapsule(t)
	head, _ := repoGit(filepath.Join(repos, "demo"), "rev-parse", "HEAD")
	return ResultSpec{WIID: "wi_d", StepID: deliverStepID, SpawnN: 3, Gate: true, RequireCommandEvidence: true,
		ReposDir: repos, ReviewPassed: true, HasReview: true, NoVerification: true,
		ReviewedHeads: map[string]string{"demo": strings.TrimSpace(head)},
		FastEvidence:  []testBarCmd{{Repo: "demo", Command: flFastCmd}, {Repo: "demo", Command: flKeysCmd}}}
}

func flBlocker(spec ResultSpec, evidence ...CommandEvidence) string {
	yes := true
	r := deliverRepo{Name: "demo", Branch: "polyforge/polyforge-166", PRURL: flPR, MergeCommit: "abc", TestsPassed: &yes}
	return mergeBlocker(spec, r, "polyforge/polyforge-166", evidence)
}

func flSh(cmd string, exit int) CommandEvidence {
	return CommandEvidence{Argv: []string{"sh", "-c", cmd}, ExitCode: exit, DurationMs: 5}
}

func TestFastLaneMergeRule(t *testing.T) {
	spec := flSpec(t)
	fast, keys := flSh("cd repos/demo && "+flFastCmd, 0), flSh("cd repos/demo && "+flKeysCmd, 0)
	push, prMerge, merge := flSh(goodPush, 0), flSh(flPRMerge, 0), flSh(flMerge, 0)
	const owed = "the fast evidence is not shown green"
	cases := []struct {
		name string
		spec func(ResultSpec) ResultSpec
		ev   []CommandEvidence
		want string // "" = merges without a human
	}{
		{name: "green in-window evidence merges", ev: []CommandEvidence{merge, fast, keys, push, prMerge}},
		{name: "evidence before the last merge does not count", ev: []CommandEvidence{fast, keys, merge, push, prMerge}, want: owed},
		{name: "evidence after gh pr merge does not count", ev: []CommandEvidence{merge, push, prMerge, fast, keys}, want: owed},
		{name: "a missing plan command does not merge", ev: []CommandEvidence{merge, fast, push, prMerge}, want: owed},
		{name: "a non-zero exit does not merge", ev: []CommandEvidence{merge, fast, flSh("cd repos/demo && "+flKeysCmd, 1), push, prMerge}, want: owed},
		{name: "evidence from another repo does not count",
			ev: []CommandEvidence{merge, flSh("cd repos/other && "+flFastCmd, 0), flSh("cd repos/other && "+flKeysCmd, 0), push, prMerge}, want: owed},
		{name: "an earlier dispatch's bar run is never the credential",
			spec: func(s ResultSpec) ResultSpec {
				head := s.ReviewedHeads["demo"]
				s.BarRuns = []BarRun{{Repo: "demo", SpawnN: 2, Argv: []string{flFastCmd}, Head: head}, {Repo: "demo", SpawnN: 2, Argv: []string{flKeysCmd}, Head: head}}
				return s
			}, ev: []CommandEvidence{push, prMerge}, want: owed},
		{name: "no recorded plan routes to the human",
			spec: func(s ResultSpec) ResultSpec { s.FastEvidence = nil; return s },
			ev:   []CommandEvidence{merge, fast, keys, push, prMerge}, want: "no in-bounds fast-lane credential plan"},
		{name: "a failed review routes to the human",
			spec: func(s ResultSpec) ResultSpec { s.ReviewPassed = false; return s },
			ev:   []CommandEvidence{merge, fast, keys, push, prMerge}, want: "no independent review passed"},
		{name: "no review step routes to the human",
			spec: func(s ResultSpec) ResultSpec { s.HasReview, s.ReviewPassed = false, false; return s },
			ev:   []CommandEvidence{merge, fast, keys, push, prMerge}, want: "declares no review step"},
		{name: "a review that covers no head routes to the human",
			spec: func(s ResultSpec) ResultSpec { s.ReviewedHeads = nil; return s },
			ev:   []CommandEvidence{merge, fast, keys, push, prMerge}, want: "no review covers the delivered diff"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := spec
			if c.spec != nil {
				s = c.spec(s)
			}
			got := flBlocker(s, c.ev...)
			if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("mergeBlocker = %q, want %q", got, c.want)
			}
		})
	}
}

// TestFastLaneMergeRuleHandMadeCommit: a non-merge commit after the review
// pass breaks the coverage, whatever the evidence shows.
func TestFastLaneMergeRuleHandMadeCommit(t *testing.T) {
	spec := flSpec(t)
	dir := filepath.Join(spec.ReposDir, "demo")
	dwrite(t, filepath.Join(dir, "late.txt"), "late\n")
	tgit(t, dir, "add", "-A")
	tgit(t, dir, "commit", "-q", "-m", "after the review")
	pushFeature(t, dir)
	got := flBlocker(spec, flSh(flMerge, 0), flSh("cd repos/demo && "+flFastCmd, 0), flSh("cd repos/demo && "+flKeysCmd, 0), flSh(goodPush, 0), flSh(flPRMerge, 0))
	if !strings.Contains(got, "does not cover the delivered HEAD") {
		t.Fatalf("a hand-made post-review commit must block: %q", got)
	}
}

// TestFastLaneMergeRuleVerificationLanesUnchanged: a lane with a
// verification step never gets the fast credential — a perfect review,
// green fast evidence and even a recorded plan still need verified_heads.
func TestFastLaneMergeRuleVerificationLanesUnchanged(t *testing.T) {
	spec := flSpec(t)
	spec.NoVerification = false
	got := flBlocker(spec, flSh(flMerge, 0), flSh("cd repos/demo && "+flFastCmd, 0), flSh("cd repos/demo && "+flKeysCmd, 0), flSh(goodPush, 0), flSh(flPRMerge, 0))
	if !strings.Contains(got, "no verification record of this repo's head") {
		t.Fatalf("a verification-bearing lane must keep its rule: %q", got)
	}
}
