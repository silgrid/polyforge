// carries: workflows/develop/steps/build.md, workflows/_shared/skills/build.md, workflows/epic/steps/build.md, workflows/develop/README.md, docs/concepts.md, docs/bundles.md

package test

// w131_build_close_facts_contract_test.go (polyforge#131, AC3;
// re-pinned by polyforge#181): the texts that state the build close's
// own facts wiring agree with what the close now keeps — the develop
// step's 3c and the shared build skill (the epic fix build's whole
// procedure) run review's own lines before the hand-off:
// plan-conformance and build with the replay (the line review runs,
// --isolation host included, so the close replays each unit's run red
// on the tree of its test commit in the same environment review
// judges in), a red fixed before the hand-off, an exit 2 retried
// after one minute at most twice and, still 2, disclosed in the
// hand-off for review to re-run — never judged blocked; a plan the
// lane inherited (the epic fix build's) skips the plan-conformance
// line and runs only the replay; the skill's units.json template takes
// each unit's commit ids from git rev-parse's own output, never a
// hand copy; the lane's narratives (the README's build entry,
// concepts, bundles) state the same close; every new sentence names
// no project, lane or concrete suite. Whitespace-flattened, so a
// rewrap keeps them pinned; the wiring's behaviour on real fixtures
// is pinned by the Python tests under workflows/_shared/scripts/tests/,
// not here.

import (
	"regexp"
	"strings"
	"testing"
)

// w131NoNames: what a close-wiring text may not carry — a project,
// lane or concrete-suite identifier (a ticket reference included);
// facts.py and the artifacts paths are the mechanism's own names.
var w131NoNames = regexp.MustCompile(`polyforge|pi-web|spec-superflow|e2e-parallel|develop lane|micro lane|#[0-9]`)

func TestW131BuildCloseFactsTexts(t *testing.T) {
	planLine := `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" plan-conformance --json --out artifacts/facts/plan-conformance.json`
	replayLine := `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" build --replay --isolation host --json --out artifacts/facts/build.json`
	carriesLine := `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" carries --json --out artifacts/facts/carries.json`
	for rel, wants := range map[string][]string{
		"workflows/develop/steps/build.md": {
			planLine,
			replayLine,
			carriesLine,
			// review's lines, replay included: a red never hands off
			"is fixed before the hand-off, never handed off on",
			// the close's own exit-2 rule: retry, then disclose, never blocked
			"wait one minute and run that line again, at most twice",
			"never `blocked` for it",
			"exit 2 is no pass either (fix the input it names; a real machine blocker is `blocked`)",
			"no file here is review's evidence",
		},
		"workflows/_shared/skills/build.md": {
			planLine,
			replayLine,
			carriesLine,
			// an inherited plan (a fix build that never re-plans) runs only the replay
			"(a plan the lane inherited skips this line and runs only the replay)",
			"is fixed before the hand-off, never handed off on",
			"wait one minute and run that line again, at most twice",
			"never `blocked` for it",
			"exit 2 on `carries` is no pass either",
			// the units.json template's commit ids: rev-parse's own output
			"Both commit ids are the full output of `git -C repos/<repo> rev-parse <ref>`, taken right after each commit is made",
			"never hand-copied from a transcript or a log",
			"a `pre_existing_red` unit reports only `impl_commit`, taken the same way",
			// the done condition and the evidence list carry the close's lines
			"the close's facts checks (plan-conformance, build with the replay, carries) green",
			"an exit 2 that stayed 2 after its retries disclosed in the hand-off with its summary",
			"`artifacts/facts/plan-conformance.json`, `artifacts/facts/build.json`, `artifacts/facts/carries.json`",
		},
		"workflows/epic/steps/build.md": {
			// the epic's fix build runs the shared skill's close over its increment
			"the close's facts checks (`facts.py build` with the replay and `carries`, and `plan-conformance` beside them when the plan is this lane's own — an inherited plan skips that line; the skill's own lines) run over it too, a red fixed before the hand-off",
		},
		"workflows/develop/README.md": {
			"the close's facts checks — `facts.py build --replay --isolation host` (each unit's run replayed red at its test commit), `plan-conformance` and `carries`, the same engine and the same lines review judges with, a red naming the unit, the plan's undeclared or lapsed obligation, or the repo, suite and reason and fixed before the hand-off (an exit 2 that stays 2 after its retries is disclosed in the hand-off, for review to re-run)",
		},
		"docs/concepts.md": {
			"Its close runs the same facts first — `plan-conformance`, `build` with the replay (each unit's run red at its test commit) and `carries` — and hands off only on green.",
		},
		"docs/bundles.md": {
			"its close running the `build` replay, `plan-conformance` and `carries` facts first",
		},
	} {
		flat := w68Flat(t, rel)
		for _, w := range wants {
			if m := w131NoNames.FindString(w); m != "" {
				t.Errorf("a close-wiring text must name no project, component or concrete suite: %q carries %q", w, m)
			}
			if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
				t.Errorf("%s must state %q", rel, w)
			}
		}
	}
}
