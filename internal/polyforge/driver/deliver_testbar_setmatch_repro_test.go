package driver

// deliver_testbar_setmatch_repro_test.go:'s repro. Two
// deliveries on 2026-10-26 re-ran verification's whole test bar on the
// pushed head, green, and were still refused by the merge rule with
// "the tests are not shown to pass on the pushed head":
//
//   - (PR #705): the evidence recorded a bar command
//     containing an unquoted `$(... | ...)` substitution as a multi-word
//     argv, while the bar side splits that command at the shell
//     metacharacters — barKey never met.
//   - (pi-web-): the evidence recorded the bar in the
//     order it actually ran (bar entry 18 before entry 17), and the
//     comparison walks the evidence with a forward-only cursor, so
//     everything after the inversion was "missing".
//
// What the merge rule must prove is only that every test-bar command ran
// in its repo with exit 0 inside the post-merge/pre-PR-merge window —
// not the order they were recorded in, nor the surface shape of the
// recording. These tests encode that, and pin that the strictness (every
// bar command present, all green) is untouched.

import (
	"path/filepath"
	"testing"
)

// TestTestBarRerunOutOfOrderEvidenceRepro is the /
// pi-web- shape: every bar command re-ran green after the merge and
// before the PR merge, but not in the bar's listed order.
func TestTestBarRerunOutOfOrderEvidenceRepro(t *testing.T) {
	bar := []testBarCmd{
		{Repo: "demo", Command: "make test"},
		{Repo: "demo", Command: "npm run lint"},
		{Repo: "demo", Command: "sh test/e2e.sh"},
	}
	root := t.TempDir()
	const prURL = "https://github.com/o/demo/pull/7"
	evidence := []CommandEvidence{
		{Argv: []string{"git merge --no-edit origin/main"}, Cwd: "repos/demo", DurationMs: 5},
		{Argv: []string{"sh test/e2e.sh"}, Cwd: "repos/demo", DurationMs: 5},
		{Argv: []string{"npm run lint"}, Cwd: "repos/demo", DurationMs: 5},
		{Argv: []string{"make test"}, Cwd: "repos/demo", DurationMs: 5},
		{Argv: []string{"gh pr merge " + prURL + " --merge"}, Cwd: "repos/demo", DurationMs: 5},
	}
	if !testBarRerun("demo", prURL, bar, evidenceEvents(evidence, root)) {
		t.Fatal("REPORTED FAILURE: a re-run of the whole test bar, green, after the merge and before the PR merge, was not recognised because the evidence recorded the commands in execution order rather than the bar's listed order")
	}
}

// TestTestBarRerunMultiWordArgvSubstitutionRepro is the /
// PR #705 shape: one bar command carries an unquoted `$(... | ...)`
// substitution and the evidence recorded it as a multi-word argv.
func TestTestBarRerunMultiWordArgvSubstitutionRepro(t *testing.T) {
	const cmd = `go test ./... -run $(grep -h '^func TestW58' internal/x/x_test.go | sed 's/func //' | cut -d'(' -f1) -count=1`
	bar := []testBarCmd{{Repo: "demo", Command: cmd}}
	root := t.TempDir()
	evidence := []CommandEvidence{
		{Argv: []string{"git", "merge", "--no-edit", "origin/main"}, Cwd: "repos/demo", DurationMs: 5},
		{Argv: []string{
			"go", "test", "./...", "-run",
			`$(grep -h '^func TestW58' internal/x/x_test.go | sed 's/func //' | cut -d'(' -f1)`,
			"-count=1",
		}, Cwd: "repos/demo", DurationMs: 5},
	}
	if !testBarRerun("demo", "", bar, evidenceEvents(evidence, root)) {
		t.Fatal("REPORTED FAILURE: the same command recorded as a multi-word argv (the bar side splits the substitution at its shell metacharacters) was not recognised as the bar command that ran green after the merge")
	}
}

// TestTestBarRerunSetMatchKeepsStrictness pins what the set comparison
// must NOT relax (the WI's acceptance): every bar command present, run in
// its repo, exit 0, inside the window. Order is the only freedom.
func TestTestBarRerunSetMatchKeepsStrictness(t *testing.T) {
	bar := []testBarCmd{
		{Repo: "demo", Command: "make test"},
		{Repo: "demo", Command: "npm run lint"},
	}
	root := t.TempDir()
	merge := CommandEvidence{Argv: []string{"git merge --no-edit origin/main"}, Cwd: "repos/demo", DurationMs: 5}
	cases := []struct {
		name string
		ev   []CommandEvidence
		want bool
	}{
		{"reordered, all green", []CommandEvidence{
			merge,
			{Argv: []string{"npm run lint"}, Cwd: "repos/demo", DurationMs: 5},
			{Argv: []string{"make test"}, Cwd: "repos/demo", DurationMs: 5},
		}, true},
		{"one bar command missing", []CommandEvidence{
			merge,
			{Argv: []string{"make test"}, Cwd: "repos/demo", DurationMs: 5},
		}, false},
		{"one bar command non-zero", []CommandEvidence{
			merge,
			{Argv: []string{"npm run lint"}, Cwd: "repos/demo", DurationMs: 5, ExitCode: 1},
			{Argv: []string{"make test"}, Cwd: "repos/demo", DurationMs: 5},
		}, false},
		{"bar command in another repo", []CommandEvidence{
			merge,
			{Argv: []string{"npm run lint"}, Cwd: "repos/other", DurationMs: 5},
			{Argv: []string{"make test"}, Cwd: "repos/demo", DurationMs: 5},
		}, false},
		{"bar command before the merge", []CommandEvidence{
			{Argv: []string{"npm run lint"}, Cwd: "repos/demo", DurationMs: 5},
			{Argv: []string{"make test"}, Cwd: "repos/demo", DurationMs: 5},
			merge,
		}, false},
		{"bar command after the PR merge", []CommandEvidence{
			merge,
			{Argv: []string{"make test"}, Cwd: "repos/demo", DurationMs: 5},
			{Argv: []string{"gh pr merge https://github.com/o/demo/pull/7 --merge"}, Cwd: "repos/demo", DurationMs: 5},
			{Argv: []string{"npm run lint"}, Cwd: "repos/demo", DurationMs: 5},
		}, false},
		{"one bar command run twice, the other missing", []CommandEvidence{
			merge,
			{Argv: []string{"make test"}, Cwd: "repos/demo", DurationMs: 5},
			{Argv: []string{"make test"}, Cwd: "repos/demo", DurationMs: 5},
		}, false},
	}
	for _, c := range cases {
		if got := testBarRerun("demo", "https://github.com/o/demo/pull/7", bar, evidenceEvents(c.ev, root)); got != c.want {
			t.Errorf("%s: testBarRerun = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestDeliverReorderedTestBarBlockerRepro shows the shape
// at the merge rule itself: origin/main had moved, the dispatch merged it
// in, re-ran the whole bar green in its real order, pushed — and the
// merge rule refused the completed envelope.
func TestDeliverReorderedTestBarBlockerRepro(t *testing.T) {
	repos := deliverCapsule(t)
	spec := deliverSpec(repos, true, 0)
	spec.VerifiedTestBar = []testBarCmd{
		{Repo: "demo", Command: "make test"},
		{Repo: "demo", Command: "go vet ./..."},
	}
	dir := filepath.Join(repos, "demo")
	origin := filepath.Join(filepath.Dir(filepath.Dir(repos)), "origin.git")
	other := filepath.Join(t.TempDir(), "other")
	tgit(t, filepath.Dir(other), "clone", "-q", origin, other)
	tgit(t, other, "commit", "-q", "--allow-empty", "-m", "another PR merged to main")
	tgit(t, other, "push", "-q", "origin", "HEAD:main")
	tgit(t, dir, "fetch", "-q", "origin")
	tgit(t, dir, "merge", "-q", "--no-edit", "origin/main")
	pushFeature(t, dir)
	evidence := []CommandEvidence{
		{Argv: []string{"git", "merge", "--no-edit", "origin/main"}, Cwd: "repos/demo", DurationMs: 5},
		{Argv: []string{"go", "vet", "./..."}, Cwd: "repos/demo", DurationMs: 5},
		{Argv: []string{"make", "test"}, Cwd: "repos/demo", DurationMs: 5},
		{Argv: []string{"git", "push", "origin", "HEAD:refs/heads/polyforge/polyforge-166"}, Cwd: "repos/demo", DurationMs: 5},
	}
	if why := mergeBlocker(spec, deliverRepo{Name: "demo"}, "polyforge/polyforge-166", evidence); why != "" {
		t.Fatalf("REPORTED FAILURE: the merge rule refused a delivery whose whole test bar was re-run green after the merge and before the push, only because the evidence order differed from the bar's: %s", why)
	}
}
