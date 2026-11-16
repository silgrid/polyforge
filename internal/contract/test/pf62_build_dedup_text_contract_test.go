package test

// carries: workflows/develop/steps/build.md, workflows/_shared/skills/build.md, workflows/_shared/build_subagents.md, workflows/develop/README.md, internal/contract/test/pf62_build_dedup_text_contract_test.go

// pf62_build_dedup_text_contract_test.go: text contract for the build
// session's verification-dedup rules (polyforge#62): the targeted
// command runs only its unit's test (the test runner's own name filter;
// a suite without one is named in the hand-off and the whole suite
// runs once, in the scoped command), a command's result already in
// hand on one tree is not re-run (a bare commit changes no tree, the
// test-first red is the build's own evidence and the facts engine's
// replay is the record — no manual worktree replay beside it, and a
// flaky failure re-runs only the suite that failed, once), and a long
// command writes its output to a log file with its exit code and the
// log is searched after the run — never piped straight into grep, head
// or tail. The rule texts name no project, component or concrete suite:
// workflows/ stays project-neutral, and AC5 pins that too. Whitespace-
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
	r2 := "A command's result already in hand on one tree (the same tree hash) is not re-run: a bare commit changes no tree, and a differently spelled command proves nothing new on it. The test-first red run is the build's own red evidence — beside it no manual worktree replay is made, the facts engine's replay is the record. A flaky failure re-runs only the suite that failed, once."
	r3 := "A long command writes its output to a log file and records its exit code (`> log 2>&1; echo exit=$?`), and the log is searched after the run; a long command is never piped straight into grep, head or tail — the pipe eats the failure detail and the exit code."
	for rel, c := range map[string]struct{ wants, stale []string }{
		"workflows/develop/steps/build.md": {wants: []string{
			r1,
			"(4) commit the implementation (C_i, `impl_commit`) — the tree is the one the green run of (3) saw, and a bare commit changes no tree, so the command is not run again on it;",
			r2,
			r3,
		}, stale: []string{"run the same command from the same directory"}},
		"workflows/_shared/skills/build.md": {wants: []string{
			r1,
			"Implement until it is green, then commit the implementation (`impl_commit`): the tree is the one the green run saw (a bare commit changes no tree), so the command is not run again on it",
			"beside it no manual worktree replay is made (the facts engine's replay is the record)",
			"A flaky failure re-runs only the suite that failed, once",
			r3,
			"Run its targeted command yourself at the head: green — its red evidence is the facts engine's replay on the tree of the change's base, and no manual worktree replay is made",
		}, stale: []string{
			"To check an earlier commit's red again",
			"Run its targeted command yourself on the tree of the change's base",
			"run the same command from the same directory",
		}},
		"workflows/_shared/build_subagents.md": {wants: []string{
			"the run is that unit's test alone (the test runner's own name filter; a suite without one is named in the task's answer and runs once, in the scoped command)",
			"the red is taken once, at the test commit — no manual worktree replay: the facts engine's replay is the record",
			"a flaky failure re-runs only the suite that failed, once",
			"a long command writes its output to a log file with its exit code (`> log 2>&1; echo exit=$?`) and the log is searched after the run — never piped straight into grep, head or tail",
		}},
		"workflows/develop/README.md": {wants: []string{
			"Each unit's targeted command runs only that unit's test (the test runner's own name filter; a suite that offers no filter is named in the hand-off and runs once, in the scoped command)",
			"a result already in hand on one tree is not re-proven — a bare commit changes no tree, and beside the test-first red no manual worktree replay is made (the facts engine's replay is the record)",
			"a flaky failure re-runs only the suite that failed, once",
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
