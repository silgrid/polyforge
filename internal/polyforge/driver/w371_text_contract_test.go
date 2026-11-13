package driver

// w371_text_contract_test.go: the texts that state the
// verification — develop's narrative, the concepts page and the
// references — carry the contract the code keeps. (The daemon-run
// verification, its judge and its `[daemon] verify_deadline` left with
// the daemon actions, workflow contract v2 change 16: no text states them
// as current. Since workflow contract 4 the narrative states the
// verification in brief and the verify skill carries its pre-written line
// and its rules; contract 3's `incomplete` is the skill's `blocked`.)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func w371Read(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(raw)), " ")
}

func TestW371TextContract(t *testing.T) {
	for rel, wants := range map[string][]string{
		"workflows/develop/workflow.yaml": {
			// (its heavy admission: test/w403_develop_grill_e2e_contract_test.go)
			"skill: verify", "waivable: true",
		},
		"workflows/develop/README.md": {
			// workflow contract v2 PR 12: the build's unit runs ride along
			"runs `verify.py --tier light --unit-runs artifacts/build/units.json` (in each repo's verification container when it declares one, else on the host; each unit's green run again, beside the light plan",
			"a denied dispatch waits in the daemon's queue, no process, no budget",
			// a red the agent cannot place is never passed: the owner's call
			"a red it can place on the change FAILs to `build`; a red it cannot place is the owner's call",
			// contract 3's `incomplete` with the log tail: blocked, then the owner
			"Exit 2 is retried, then `blocked`.",
			"the step hands off `blocked`; fix the machine, then write to the work item (or `pf resume`)",
		},
		// the pre-written line and the agent's rules (contract 4: the skill's)
		"workflows/_shared/skills/verify.md": {
			"`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/verify.py\" --tier light --unit-runs artifacts/build/units.json --json --out artifacts/verify/result.json`",
			"run any other build, lint or test command, or re-run one check by hand; hand off `done` on a red you could not place",
			"A red you cannot place: `pf step ask` the owner with the red checks, the log tails and your reading, then end your turn",
			"still 2: hand off `blocked`, the result's `summary` and the last log's tail in the body",
		},
		"docs/concepts.md": {
			"### The light verification", "`verify.py --tier light`", "`merge-base HEAD origin/<default>`",
			"A denied dispatch is **queued**: it runs no process, spends no budget and holds no job slot",
		},
		"docs/reference/config.md": {
			"| `max_concurrent_bars` | — | `2` |", "| `bar_load_threshold` | — | cores − 4 (at least `1`) |",
			"### A step's agents — under the step's own id", "Develop's verification runs `verify.py` on the lane's `cheap` tier",
		},
		// the /status job row; the watch snapshot's projection left with workflow contract v2 change 20
		"docs/reference/uds-routes.md": {"`verify_queue: {position, reason, since}`"},
		"docs/reference/commands.md":   {"`pf ops status` then shows a `VERIFY` column", "## The verification bar — `verify.py`'s mechanical run"},
	} {
		got := w371Read(t, rel)
		for _, w := range wants {
			if !strings.Contains(got, strings.Join(strings.Fields(w), " ")) {
				t.Errorf("%s must state %q", rel, w)
			}
		}
	}
	// the daemon-run verification is stated nowhere as current
	for _, rel := range []string{"docs/concepts.md", "docs/reference/config.md", "docs/reference/commands.md", "docs/contributing.md"} {
		got := w371Read(t, rel)
		for _, gone := range []string{"verify_deadline", "The verification gate run by the daemon", "`executor: daemon` verification step"} {
			if strings.Contains(got, gone) {
				t.Errorf("%s still states the deleted daemon-run verification: %q", rel, gone)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(shippedWorkflowsDir, "_shared", "steps", "verify-judge.md")); !os.IsNotExist(err) {
		t.Errorf("the verify-judge definition left with the verify action: %v", err)
	}
}
