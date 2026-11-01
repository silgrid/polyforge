package test

// w214_filing_tier_e2e_carried_contract_test.go: text contract for.
//
// Work item #214 makes the acceptance carrier a decision axis of the filing
// tier heuristic: when the acceptance is e2e-carried (a criterion an
// e2e-author step would author, or behaviour an existing e2e suite pins), the
// filer proposes `full`, because a scoped run deliberately skips the e2e
// suites. The clause lands verbatim in both Tier slots — the filing skill and
// its execute-intent mirror — so the two copies cannot diverge.
//
// The change is prompt material: no endpoint, verb, flag or protocol moves, so
// the only mechanical contract is the shipped text itself. This test is red on
// the base tree (the clause existed in neither file) and green only once it
// landed in both. The paths are literal repo-relative strings on purpose: the
// scoped engine selects harness tests by grepping their source for the changed
// paths. The qualitative half — a real proposal choosing `full` for an
// e2e-carried acceptance — is observed on real WIs.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w214Clause is the e2e-carried decision, compared after whitespace
// normalization: line wrapping may differ between the two slots, the sentence
// may not.
const w214Clause = "propose `full` when the acceptance is e2e-carried — a criterion an `e2e-author` step would author, or behaviour an existing e2e suite pins — because a scoped run deliberately skips the e2e suites;"

func TestW214FilingTierE2ECarriedContract(t *testing.T) {
	root := v1Root(t)
	for _, rel := range []string{
		"skills/polyforge/verbs/file.md",
		"skills/polyforge/verbs/execute-intent.md",
	} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s carries a Tier slot the e2e-carried clause must reach: %v", rel, err)
		}
		norm := strings.Join(strings.Fields(string(b)), " ")
		if !strings.Contains(norm, w214Clause) {
			t.Errorf("%s must carry, in its `Tier + reason` slot, the clause %q — an e2e-carried acceptance proposes full in both slots, verbatim", rel, w214Clause)
		}
		for _, want := range []string{"propose `scoped` ", "`e2e-author`", "skips the e2e suites"} {
			if !strings.Contains(norm, want) {
				t.Errorf("%s must keep the tier-heuristic marker %q", rel, want)
			}
		}
		if !strings.Contains(norm, "propose `scoped` for a text/docs change confined to one repository and a small surface; "+w214Clause) {
			t.Errorf("%s must place the e2e-carried clause right after the `scoped` proposal in the Tier slot", rel)
		}
	}
}
