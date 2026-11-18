// carries: workflows/develop/steps/build.md, workflows/_shared/skills/build.md, workflows/epic/steps/build.md, workflows/develop/README.md, docs/concepts.md, docs/bundles.md

package test

// w131_build_close_facts_contract_test.go (polyforge#131, AC3): the
// texts that state the build close's own facts wiring agree with what
// the close now keeps — the develop step's 3c and the shared build
// skill (the epic fix build's whole procedure) carry the two facts
// lines (build without the replay, carries), the skill's units.json
// template takes each unit's commit ids from git rev-parse's own
// output, never a hand copy, and the lane's narratives (the README's
// build entry, concepts, bundles) state the same close; every new
// sentence names no project, lane or concrete suite. Whitespace-
// flattened, so a rewrap keeps them pinned; the wiring's behaviour on
// real fixtures is pinned by the Python tests under
// workflows/_shared/scripts/tests/, not here.

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
	buildLine := `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" build --json --out artifacts/facts/build.json`
	carriesLine := `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" carries --json --out artifacts/facts/carries.json`
	for rel, wants := range map[string][]string{
		"workflows/develop/steps/build.md": {
			buildLine,
			carriesLine,
			"exit 2 is no pass either (fix the input it names; a real machine blocker is `blocked`)",
			"never replay evidence",
		},
		"workflows/_shared/skills/build.md": {
			buildLine,
			carriesLine,
			// the units.json template's commit ids: rev-parse's own output
			"Both commit ids are the full output of `git -C repos/<repo> rev-parse <ref>`, taken right after each commit is made",
			"never hand-copied from a transcript or a log",
			"a `pre_existing_red` unit reports only `impl_commit`, taken the same way",
			// the done condition and the evidence list carry the two checks
			"the close's facts checks (build, carries) green",
			"`artifacts/facts/build.json`, `artifacts/facts/carries.json`",
			"exit 2 is no pass either",
		},
		"workflows/epic/steps/build.md": {
			// the epic's fix build runs the shared skill's close over its increment
			"the close's facts checks (`facts.py build` without the replay, and `carries` — the skill's own lines) run over it too, a red fixed before the hand-off",
			"`artifacts/facts/build.json`, `artifacts/facts/carries.json`",
		},
		"workflows/develop/README.md": {
			"the close's facts checks — `facts.py build` without the replay (each unit's static commit facts) and `carries`, the same engine review judges with, a red naming the unit, or the repo, suite and reason and fixed before the hand-off",
		},
		"docs/concepts.md": {
			"Its close runs two of the same facts first — `build` without the replay (each unit's static commit facts) and `carries` — and hands off only on green",
		},
		"docs/bundles.md": {
			"its close running the static `build` (no replay) and `carries` facts first",
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
