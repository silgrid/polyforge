package test

// w68_build_step_rules_contract_test.go: pins the build-step rule this
// chore surfaced, so it cannot silently drift back: the chore `code` step
// names both rewind verdict sources, in feature-full's exact wording, and
// the old verification-only form is gone. (The chore's second rule, the
// projection hygiene bullet, holds only in the internal tree: its pin lives
// in an internal-only test, since the public projection drops the bullet.)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w68Flat reads a repo file and collapses every whitespace run to one
// space, so line wrapping cannot make the text assertions brittle.
func w68Flat(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

func TestW68ChoreCodeStepNamesReviewRewind(t *testing.T) {
	const bothSources = "(a `verification` or `review` FAIL verdict)"
	chore := w68Flat(t, "workflows/chore/steps/code.md")
	if !strings.Contains(chore, bothSources) {
		t.Errorf("the chore code step must name both rewind verdict sources, missing %q", bothSources)
	}
	if strings.Contains(chore, "(a `verification` FAIL") {
		t.Error("the chore code step still carries the old verification-only rewind wording")
	}
	full := w68Flat(t, "workflows/feature-full/steps/code.md")
	if !strings.Contains(full, bothSources) {
		t.Errorf("the feature-full code step must keep naming both rewind verdict sources (anti-drift pin), missing %q", bothSources)
	}
}
