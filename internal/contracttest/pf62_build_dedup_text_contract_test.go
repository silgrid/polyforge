package test

// carries: workflows/develop/steps/build.md, workflows/_shared/skills/build.md, workflows/_shared/build_subagents.md, workflows/develop/README.md, internal/contracttest/pf62_build_dedup_text_contract_test.go

// pf62_build_dedup_text_contract_test.go: text contract for the build
// session's verification-dedup rules (polyforge#62): the targeted
// command runs only its unit's test (the test runner's own name filter;
// a suite without one is named in the hand-off and the whole suite
// runs once, in the scoped command), a command's result already in
// hand on one tree is not re-run (a bare commit changes no tree, the
// test-first red is the build's own evidence and the facts engine's
// replay is the record — no manual worktree replay beside it; a flaky
// failure is re-run only in the suite that failed, once, by the written
// fast-check command itself, never by hand, and a red-state run must
// exit non-zero within its limit — the unit's self-reported `timeout`,
// else the default), and a long command writes its output to a log file
// with its exit code and the log is searched after the run — never
// piped straight into grep, head or tail. The rule texts name no
// project, component or concrete suite: workflows/ stays
// project-neutral, and AC5 pins that too. Whitespace-
// flattened, so a rewrap keeps them pinned; the behaviour of the facts
// engine's structure-before-replay ordering is pinned by the Python
// tests under workflows/_shared/scripts/tests/, not here.

import (
	"regexp"
	"strings"
	"testing"
)

// pf62NoNames: what a rule text may not carry — a project, lane or
// concrete-suite identifier (a ticket reference included). The test
// runner filters (`go test -run`, `pytest -k`) and the facts engine are
// the acceptance's own words for the mechanism, not names.
var pf62NoNames = regexp.MustCompile(`polyforge|pi-web|spec-superflow|e2e-parallel|develop lane|micro lane|#[0-9]`)

func TestPF62BuildDedupTextContract(t *testing.T) {
	// the three rules, stated wherever the build instructions state them
	r1 := "The targeted command runs only that unit's test, filtered by the test runner's own name filter (`go test -run`, `pytest -k`, or the suite's own scenario whitelist); a suite that offers no filter is named in the hand-off, and the whole suite runs once, in the scoped command, and nowhere else."
	r2 := "A command's result already in hand for its key — the same command text, the same working-tree hash of the root it runs in, the same declared prerequisites — is not re-run: a bare commit changes no tree, and a differently spelled command proves nothing new on it. The test-first red run is the build's own red evidence — beside it no manual worktree replay is made, the facts engine's replay is the record. A flaky failure is re-run only in the suite that failed, once, by the written fast-check command itself — the same rules verification runs — never by hand: the check already contains that one re-run, and a failure still red after it is red."
	r3 := "A long command writes its output to a log file and records its exit code (`> log 2>&1; echo exit=$?`), and the log is searched after the run; a long command is never piped straight into grep, head or tail — the pipe eats the failure detail and the exit code."
	// the red-state limit rule (polyforge#157): a red-state run must
	// fail fast, never hang past its limit
	r4 := "A red-state run must exit non-zero within its limit — a unit self-reports `timeout` in units.json, else the default — and a run its limit kills is an evidence problem, never a pass: the waiting inside it is bounded and an assertion failure exits at once."
	// the on-the-spot unit check's flaky policy (polyforge#157): a unit
	// this lane just wrote must be stable; a pre-existing reproduction
	// test may flake and be re-run once
	r5 := "A red unit declared `pre_existing_red` is re-run once by the check, and a pass records it flaky; a unit this lane just wrote is never re-run there — a test just written must be stable, so red it stays."
	// the hand-off's Status names the flaky units a fast check recorded
	r6 := "a fast check that turned green through a flaky re-run has its flaky units (with their first-red logs) listed in Status"
	// the same flaky and limit rules as they read mid-sentence in the
	// sub-agent brief and the lane README
	flakyLower := "a flaky failure is re-run only in the suite that failed, once, by the written fast-check command itself — the same rules verification runs — never by hand: the check already contains that one re-run, and a failure still red after it is red"
	r4Lower := "a red-state run must exit non-zero within its limit — a unit self-reports `timeout` in units.json, else the default — and a run its limit kills is an evidence problem, never a pass: the waiting inside it is bounded and an assertion failure exits at once"
	for rel, c := range map[string]struct{ wants, stale []string }{
		"workflows/develop/steps/build.md": {wants: []string{
			r1,
			"(4) commit the implementation (C_i, `impl_commit`) — the tree is the one the green run of (3) saw, and a bare commit changes no tree, so the command is not run again on it;",
			r2,
			r4,
			r5,
			r3,
		}, stale: []string{"run the same command from the same directory"}},
		"workflows/_shared/skills/build.md": {wants: []string{
			r1,
			"Implement until it is green, then commit the implementation (`impl_commit`): the tree is the one the green run saw (a bare commit changes no tree), so the command is not run again on it",
			"beside it no manual worktree replay is made (the facts engine's replay is the record)",
			"A flaky failure is re-run only in the suite that failed, once, by the written fast-check command itself — the same rules verification runs — never by hand: the check already contains that one re-run, and a failure still red after it is red.",
			r4,
			r5,
			r6,
			r3,
			"Run its targeted command yourself at the head: green — its red evidence is the facts engine's replay on the tree of the change's base, and no manual worktree replay is made",
		}, stale: []string{
			"To check an earlier commit's red again",
			"Run its targeted command yourself on the tree of the change's base",
			"run the same command from the same directory",
			"A flaky failure re-runs only the suite that failed, once",
		}},
		"workflows/_shared/build_subagents.md": {wants: []string{
			"the run is that unit's test alone (the test runner's own name filter; a suite without one is named in the task's answer and runs once, in the scoped command)",
			"the red is taken once, at the test commit — no manual worktree replay: the facts engine's replay is the record",
			flakyLower,
			r4Lower,
			"a long command writes its output to a log file with its exit code (`> log 2>&1; echo exit=$?`) and the log is searched after the run — never piped straight into grep, head or tail",
		}},
		"workflows/develop/README.md": {wants: []string{
			"Each unit's targeted command runs only that unit's test (the test runner's own name filter; a suite that offers no filter is named in the hand-off and runs once, in the scoped command)",
			"a result already in hand for its key — the same command text, the same working-tree hash of the root it runs in, the same declared prerequisites — is not re-proven: a bare commit changes no tree, and beside the test-first red no manual worktree replay is made (the facts engine's replay is the record)",
			flakyLower,
			r4Lower,
			"a long command writes its output to a log file with its exit code and the log is searched after the run, never piped straight into grep, head or tail",
		}},
	} {
		flat := w68Flat(t, rel)
		for _, w := range c.wants {
			if m := pf62NoNames.FindString(w); m != "" {
				t.Errorf("a rule text must name no project, component or concrete suite: %q carries %q", w, m)
			}
			if !strings.Contains(flat, w) {
				t.Errorf("%s must state %q", rel, w)
			}
		}
		for _, s := range c.stale {
			if strings.Contains(flat, s) {
				t.Errorf("%s still promises %q: the dedup rule removes it", rel, s)
			}
		}
	}
}
