package driver

// deliver_evidence_shapes_test.go: polyforge#203's incident shapes.
// Four deliveries re-ran the test bar after merging origin/main and
// before `gh pr merge`, yet the driver could not read that evidence:
// the evidence side of the comparison stripped the leading NAME=value
// assignments and wrapper words the test-bar side kept, so a bar such
// as `GOWORK=off go build ./...` could never match (cause 2), and the
// evidence carried no repo context because the prompt's example showed
// no `cwd` (cause 1).

import (
	"path/filepath"
	"strings"
	"testing"
)

// The incident test bars (#202, pi#108, #196): assignment- and
// env-prefixed commands, recorded verbatim with their cwd, after the
// merge and before the PR merge, are the re-run they claim to be.
func TestTestBarRerunAssignmentPrefixedBar(t *testing.T) {
	bar := []testBarCmd{
		{Repo: "demo", Command: "GOWORK=off go build ./..."},
		{Repo: "demo", Command: "E2E_RUN=all E2E_JOBS=8 sh test/e2e-parallel.sh"},
		{Repo: "demo", Command: "env -u NODE_ENV npm test 2>&1"},
	}
	rec := func(cmds ...string) []CommandEvidence {
		var out []CommandEvidence
		for _, c := range cmds {
			out = append(out, CommandEvidence{Argv: strings.Fields(c), Cwd: "repos/demo", DurationMs: 5})
		}
		return out
	}
	evidence := rec("git merge --no-edit origin/main",
		"GOWORK=off go build ./...",
		"E2E_RUN=all E2E_JOBS=8 sh test/e2e-parallel.sh",
		"env -u NODE_ENV npm test",
		"git push origin HEAD:refs/heads/polyforge/polyforge-166",
		"gh pr merge https://github.com/o/r/pull/1 --merge")
	root := t.TempDir()
	if !testBarRerun("demo", "https://github.com/o/r/pull/1", bar, evidenceEvents(evidence, root)) {
		t.Fatal("a verbatim re-run of an assignment/env-prefixed test bar, after the merge and before the PR merge, was not recognised (polyforge#203 cause 2)")
	}
	// the same command before the merge still does not count
	if testBarRerun("demo", "", bar[:1], evidenceEvents(rec("GOWORK=off go build ./...", "git merge --no-edit origin/main"), root)) {
		t.Fatal("a test bar run before the last merge counted")
	}
	// a shell-string re-run with the bar's quoting is the same command
	quoted := []testBarCmd{{Repo: "demo", Command: "E2E_RUN='^(TestA|TestB)$' E2E_JOBS=8 sh test/e2e-parallel.sh"}}
	if !testBarRerun("demo", "", quoted, evidenceEvents([]CommandEvidence{{Argv: []string{"cd repos/demo && E2E_RUN='^(TestA|TestB)$' E2E_JOBS=8 sh test/e2e-parallel.sh > /tmp/e2e.log 2>&1"}, DurationMs: 5}}, root)) {
		t.Fatal("a verbatim quoted E2E_RUN re-run, redirected to a log, was not recognised")
	}
}

// The comparison is exact: the leading assignments and wrapper words
// are part of the command. A re-run that drops or changes them is a
// different run and must not license the merge (polyforge#203 code review;
// the E2E_RUN-subset pitfall of polyforge#202).
func TestTestBarRerunAssignmentMismatchRefused(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name, bar, rerun string
	}{
		{"different E2E_RUN subset", "E2E_RUN='^(TestA|TestB)$' E2E_JOBS=8 sh test/e2e-parallel.sh", "E2E_RUN='^(TestA)$' E2E_JOBS=8 sh test/e2e-parallel.sh"},
		{"E2E_RUN dropped", "E2E_RUN=all sh test/e2e-parallel.sh", "sh test/e2e-parallel.sh"},
		{"GOWORK=off missing", "GOWORK=off go build ./...", "go build ./..."},
		{"GOWORK added", "go build ./...", "GOWORK=off go build ./..."},
		{"env -u dropped", "env -u NODE_ENV npm test", "npm test"},
	}
	for _, c := range cases {
		bar := []testBarCmd{{Repo: "demo", Command: c.bar}}
		evidence := []CommandEvidence{
			{Argv: []string{"git merge --no-edit origin/main"}, Cwd: "repos/demo", DurationMs: 5},
			{Argv: []string{c.rerun}, Cwd: "repos/demo", DurationMs: 5},
		}
		if testBarRerun("demo", "", bar, evidenceEvents(evidence, root)) {
			t.Errorf("%s: re-run %q satisfied test bar %q", c.name, c.rerun, c.bar)
		}
	}
}

// The `gh pr merge` that closes a repo's window is its own PR's: run in
// that repo or naming its PR URL. Another repo's PR merge does not
// close it.
func TestTestBarRerunPRMergeWindow(t *testing.T) {
	bar := []testBarCmd{{Repo: "demo", Command: "make test"}}
	root := t.TempDir()
	const url = "https://github.com/o/demo/pull/1"
	cases := []struct {
		name string
		cmds []string
		want bool
	}{
		{"other repo's PR merged first", []string{"cd repos/demo && git merge origin/main", "gh pr merge https://github.com/o/other/pull/2 --merge", "cd repos/demo && make test", "gh pr merge " + url + " --merge"}, true},
		{"own PR by number in the repo", []string{"cd repos/demo && git merge origin/main", "cd repos/demo && gh pr merge 1 --merge", "cd repos/demo && make test"}, false},
		{"own PR by URL", []string{"cd repos/demo && git merge origin/main", "gh pr merge " + url + " --merge", "cd repos/demo && make test"}, false},
		{"sync after the PR merge", []string{"cd repos/demo && git merge origin/main", "cd repos/demo && make test", "gh pr merge " + url + " --merge", "cd repos/demo && git merge --no-edit origin/main"}, false},
		{"no merge, already delivered", []string{"cd repos/demo && make test"}, true},
	}
	for _, c := range cases {
		if got := testBarRerun("demo", url, bar, evidenceEvents(ev(c.cmds...), root)); got != c.want {
			t.Errorf("%s: testBarRerun = %v, want %v", c.name, got, c.want)
		}
	}
}

// Through the merge rule on the incident's git shape (origin/main
// moved, merged in, pushed): a GOWORK=off-prefixed bar re-run recorded
// verbatim with its cwd, as argv, stands as the evidence.
func TestDeliverAssignmentPrefixedBarAccepted(t *testing.T) {
	repos := deliverCapsule(t)
	spec := deliverSpec(repos, true, 0)
	spec.VerifiedTestBar = []testBarCmd{{Repo: "demo", Command: "GOWORK=off make test"}}
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
		{Argv: []string{"GOWORK=off", "make", "test"}, Cwd: "repos/demo", DurationMs: 5},
		{Argv: []string{"git", "push", "origin", "HEAD:refs/heads/polyforge/polyforge-166"}, Cwd: "repos/demo", DurationMs: 5},
	}
	if why := mergeBlocker(spec, deliverRepo{Name: "demo"}, "polyforge/polyforge-166", evidence); why != "" {
		t.Fatalf("merge rule refused a correctly ordered, verbatim re-run of an assignment-prefixed test bar (polyforge#203 cause 2): %s", why)
	}
}
