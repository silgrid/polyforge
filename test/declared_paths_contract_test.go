package test

// declared_paths_contract_test.go: declared_paths has exactly one
// definition — review's scope — and verification points at it.

import (
	"strings"
	"testing"
)

func TestDeclaredPathsContract(t *testing.T) {
	const def = "The one definition of declared_paths: the path set review judged in this round and verification verifies for that same round"
	review := w68Flat(t, "workflows/_shared/steps/review.md")
	if n := strings.Count(review, def); n != 1 {
		t.Errorf("review.md must define declared_paths exactly once, found %d", n)
	}
	verification := w68Flat(t, "workflows/_shared/steps/verification.md")
	if strings.Contains(verification, "agent-declared coverage") {
		t.Error("verification.md must not give declared_paths a second meaning")
	}
	if !strings.Contains(verification, "declared_paths as review's `scope` defines it") {
		t.Error("verification.md must point at review's declared_paths definition")
	}
	if !strings.Contains(w68Flat(t, "workflows/_shared/scoped_verification.md"), "means exactly what review's `scope` defines") {
		t.Error("the scoped-tier rules must point at review's declared_paths definition")
	}
}
