package test

// carries: workflows/develop/steps/build.md, workflows/develop/README.md, internal/contracttest/w132_build_self_review_doc_impact_contract_test.go

// w132_build_self_review_doc_impact_contract_test.go (polyforge#132,
// AC1): the develop build's self-review section runs review's
// doc-impact check item by item — the check review judges a stale
// document by moved forward into the build's own close, so a document
// the change leaves out of sync is caught before the gate, not by it.
// The step's self-review records, in artifacts/self-review.md, each
// document compared, its conclusion, and synced or no-change-needed
// with the reason; the lane README's build entry states the same
// self-review (AC4); and review's own doc-impact check stays in the
// shared review step, its sentences unchanged — the build runs the
// check earlier, review keeps blocking on it (Q1: review's source
// files and existing judgment rules do not change; only a shared
// fragment's new examples may change a render). Whitespace-flattened,
// so a rewrap keeps them pinned.

import (
	"strings"
	"testing"
)

func TestW132BuildSelfReviewRunsTheDocImpactCheck(t *testing.T) {
	// the develop step's self-review (its 3c/3d close) runs review's
	// doc-impact check item by item and records the comparison
	build := w68Flat(t, "workflows/develop/steps/build.md")
	for _, want := range []string{
		"review's doc-impact check, item by item",
		"each document that describes what the change altered",
		"`artifacts/self-review.md` lists each document compared, its conclusion, and synced or no-change-needed with the reason",
	} {
		if !strings.Contains(build, want) {
			t.Errorf("the build step's self-review must run the doc-impact check item by item, missing %q", want)
		}
	}
	// the lane README's build entry describes the same self-review (AC4)
	readme := w68Flat(t, "workflows/develop/README.md")
	for _, want := range []string{
		"a self-review against review's five blocking classes and its doc-impact check, item by item",
		"each document compared, its conclusion, and synced or no-change-needed with the reason",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("the develop README's build entry must state the doc-impact self-review, missing %q", want)
		}
	}
	// review's own doc-impact check stays where it was, its sentences
	// unchanged — the build front-loads it, review keeps blocking on it
	review := w68Flat(t, "workflows/_shared/steps/review.md")
	for _, want := range []string{
		"The doc-impact check: when the change alters user-visible behaviour, a configuration key, a command or a step definition, the documents that describe it change in the same diff",
		"Record in `artifacts/review.md` every document you compared.",
	} {
		if !strings.Contains(review, want) {
			t.Errorf("the shared review step keeps its doc-impact check, missing %q", want)
		}
	}
}
