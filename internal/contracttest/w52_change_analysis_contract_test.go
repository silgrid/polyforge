// carries: workflows/develop/*, workflows/_shared/skills/*, workflows/integrate/*, docs/concepts.md, docs/contributing.md

package test

// w52_change_analysis_contract_test.go (polyforge#52): the texts that
// state the change analysis and the fix-ticket marker carry what the code
// keeps — build writes the analysis at its close and its hand-off names
// it, review judges it for obviously missing tests, verification runs the
// union of the engine's selection and the analysis's listed tests, and
// an integration batch's fix tickets carry the integration_fix attr that
// switches their verification to the full bar. Whitespace-flattened, so
// a rewrap keeps them pinned.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w52Flat reads a repository file with its whitespace folded.
func w52Flat(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return strings.Join(strings.Fields(string(raw)), " ")
}

// w52Wants checks each want is stated (folded) in rel.
func w52Wants(t *testing.T, rel string, wants []string) {
	t.Helper()
	flat := w52Flat(t, rel)
	for _, w := range wants {
		if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
			t.Errorf("%s must state %q", rel, w)
		}
	}
}

// TestW52FixTicketsCarryTheMarker (AC4, AC5): an integration batch's fix
// tickets are filed with the structured integration_fix attr — the
// full-gate's and collect's records JSON carry the attrs write beside
// each file, the lane's whitelist names the key, and the fix ticket's
// develop verification reads it.
func TestW52FixTicketsCarryTheMarker(t *testing.T) {
	w52Wants(t, "workflows/integrate/skills/full-gate.md", []string{
		`{"verb": "attrs", "target": "key:<this ticket's key>", "name": "integration_fix", "value": true}`,
		"the structured marker that makes the fix ticket's own develop verification run the full bar (a ticket without it runs the light tier)",
	})
	w52Wants(t, "workflows/integrate/skills/collect.md", []string{
		`{"verb": "attrs", "target": "key:<the ticket's key>", "name": "integration_fix", "value": true}`,
	})
	for _, step := range []string{"collect", "full-gate"} {
		raw, err := os.ReadFile(filepath.Join(v1Root(t), "workflows", "integrate", "workflow.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		yaml := string(raw)
		at := strings.Index(yaml, "id: "+step)
		if at < 0 {
			t.Fatalf("workflows/integrate/workflow.yaml names no step %s", step)
		}
		rest := yaml[at:]
		if end := strings.Index(rest, "\n  - id: "); end > 0 {
			rest = rest[:end]
		}
		if !strings.Contains(rest, "attrs: [integration, integration_fix]") {
			t.Errorf("workflows/integrate/workflow.yaml: the %s step must whitelist the integration_fix attrs key (hub.attrs; the attrs verb follows it)", step)
		}
	}
	w52Wants(t, "workflows/integrate/README.md", []string{
		"every fix ticket it files carries the integration_fix attr",
	})
}

// TestW52BuildWritesTheChangeAnalysis (AC1): develop's build says so at
// its close — the structured analysis names what the change altered, the
// existing tests that already pin it (with the evidence that found them),
// the engine report's paths no carries: declaration covers, and the
// lane's supplement cap; the hand-off names the file.
func TestW52BuildWritesTheChangeAnalysis(t *testing.T) {
	w52Wants(t, "workflows/develop/steps/build.md", []string{
		"Write the change analysis — `artifacts/build/change-analysis.json`",
		"`supplement_limit`",
		"{{supplement_limit}} test runs",
		"`changed`",
		"`uncovered`",
		"`tests`",
		"`evidence`",
		"covered by no `carries:` declaration",
		"run each where you found it and list only a test that passes at HEAD",
		"`verify.py --analysis`, add-only, never narrowing it",
	})
	w52Wants(t, "workflows/_shared/skills/build.md", []string{
		"`artifacts/plan.md`, `artifacts/build/units.json`, `artifacts/build/change-analysis.json` and `artifacts/self-review.md`",
		"`artifacts/plan.md`, `artifacts/build/units.json`, `artifacts/build/change-analysis.json`, `artifacts/build/units-check.json`",
	})
	raw, err := os.ReadFile(filepath.Join(v1Root(t), "workflows", "develop", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `supplement_limit: "10"`) {
		t.Error("workflows/develop/workflow.yaml: the build step must carry the lane's supplement cap (vars.supplement_limit, rendered into the step's text)")
	}
}

// TestW52ReviewChecksTheChangeAnalysis (AC3): review's judgment states
// the analysis check — an obviously missing test (an existing test that
// already pins a named change, unlisted), an uncovered list that does not
// match the engine's report, a listed test that does not exist — is a
// blocking test_weakened finding naming the entry.
func TestW52ReviewChecksTheChangeAnalysis(t *testing.T) {
	w52Wants(t, "workflows/develop/fragments/review-judge.md", []string{
		"6. **The change analysis (`test_weakened`)**",
		"`artifacts/build/change-analysis.json`",
		"an existing test already pins, one the analysis does not list, is a blocking `test_weakened` finding",
		"an `uncovered` list that does not match that report",
		"a listed test that does not exist (its file or name)",
		"each finding names the missing or wrong entry",
	})
}

// w52Stale checks each stale phrase is gone (folded) from rel.
func w52Stale(t *testing.T, rel string, stale []string) {
	t.Helper()
	flat := w52Flat(t, rel)
	for _, s := range stale {
		if strings.Contains(flat, strings.Join(strings.Fields(s), " ")) {
			t.Errorf("%s still states %q (the tier/analysis contract moved on)", rel, s)
		}
	}
}

// TestW52TheReferencesStateTheTierAndTheUnion (review r1 #1–#4, AC6 and
// the stale references): the lane narratives and the references state the
// new contract — develop's verification entry runs --tier auto with
// --analysis, the integrate entries name the integration_fix attr their
// fix tickets carry, bundles.md no longer denies a per-work-item tier,
// commands.md no longer calls light "the only tier it runs", and
// verify.py's --help states the tier and the union without breaking a
// sentence in half.
func TestW52TheReferencesStateTheTierAndTheUnion(t *testing.T) {
	w52Wants(t, "docs/concepts.md", []string{
		// the develop lane's verification entry (the Shipped lanes list)
		"`verify.py --tier auto --unit-runs artifacts/build/units.json --analysis artifacts/build/change-analysis.json --json --out artifacts/verify/result.json`",
		"then runs the tier the work item's attrs decide over the tree as it stands",
		"the change analysis's listed tests run beside the plan, add-only",
		// the build close's scoped selection (polyforge#164)
		"its close runs the scoped selection verification plans — `fast_checks.py --close-scoped`, one shared implementation — and merges its record into the change analysis's `selected`",
		// the integrate lane's collect: a stuck hold's fix ticket carries the marker
		"a `file` write on the develop lane, with its acceptance, registered on the held item by an `attrs` write",
		"carries the `integration_fix` attr that makes its own verification run the full bar",
		// the integrate lane's full-gate: every fix ticket carries the marker
		"each fix ticket filed with the `integration_fix` attr",
	})
	w52Stale(t, "docs/concepts.md", []string{"runs the light tier over the tree as it stands"})
	w52Wants(t, "docs/bundles.md", []string{
		"develop's verification runs `verify.py --tier auto --unit-runs artifacts/build/units.json --analysis artifacts/build/change-analysis.json` through its skill (the build's recorded unit runs ride along; the tier is the work item's own — an integration batch's fix ticket runs the full bar, every other ticket the light one)",
		"→ `verification` (`verify.py`, the tier the work item's attrs decide: the light tier, or the full bar on an integration batch's fix ticket; the lane's one waivable gate)",
		// the build close's scoped selection (polyforge#164)
		"the scoped selection `fast_checks.py --close-scoped` before them — its record the change analysis's `selected`",
	})
	w52Stale(t, "docs/bundles.md", []string{"there is no per-work-item tier", "`verify.py`, the light tier"})
	w52Wants(t, "docs/reference/commands.md", []string{
		"`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/verify.py\" --tier auto --unit-runs artifacts/build/units.json --analysis artifacts/build/change-analysis.json --json --out artifacts/verify/result.json`",
		"the tier the work item's attrs decide — an integration batch's fix ticket (its `integration_fix` marker) runs the full bar; every other ticket the light tier",
		"the change analysis's listed tests run beside the plan, add-only, under the analysis's supplement cap",
		// the build close's scoped selection (polyforge#164): the same
		// light selection, run at the close; its record is the analysis's
		// machine selection section
		"`--repo <name> --close-scoped`",
		"the same light-tier selection verification's light tier plans",
		"a selection the engine answers with the full tier, or a selected command that cannot run, records `incomplete` with the reason kept and exits non-zero, claiming nothing",
		"the change analysis's machine selection record",
		// review r1 #3: the selected records' judgment, stated where the
		// two consumers of the analysis are documented
		"The analysis's machine selection records (`selected`, one per task repo, which the build close's `fast_checks.py --close-scoped` writes) are judged by the same shared validator",
		"a sync's merge commits between its head and the verified HEAD leave it valid",
		"the machine selection records the analysis carries (`selected`) are judged statically by the same shared validator",
	})
	w52Wants(t, "docs/contributing.md", []string{
		// review r1 #3: the --analysis check's selected judgment
		"a sync's merge commits between the record's head and the verified HEAD leave it valid",
	})
	w52Stale(t, "docs/reference/commands.md", []string{"the only tier it runs"})
	w52Wants(t, "workflows/_shared/scripts/verify.py", []string{
		"verify.py — the mechanical verification of a capsule's task repos as the sync step left them (develop's verification step)",
		"\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/verify.py\" --tier auto|light|full \\\\",
		"[--unit-runs FILE] [--analysis FILE] [--capsule DIR]",
		"The tier: ``--tier auto`` (the lane's line) reads the capsule's work item",
		"Each task repo's tree is verified at its HEAD: no fetch, no merge (a merge in progress makes the run not judged); the base",
	})
	w52Stale(t, "workflows/_shared/scripts/verify.py", []string{"the light verification of a capsule's task repos"})
}
