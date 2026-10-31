package driver

// w132_prompt_contract_test.go: pins the bug-fix repro
// step's narrow-repro rule, mechanically (whitespace-normalized, so a
// rewrap never breaks the pin).
//
//  1. TestW132ReproReproducesOnlyTheNamedFailureNarrowly — the repro step
//     reproduces the failure the work item names as narrowly as it can be
//     reproduced and never re-runs the whole test bar or every suite to
//     "confirm"; the rule appears exactly once, inside item 2 ("Reproduce
//     it"). Red on the pre-change tree (the paragraph was absent).
//
// The file adds no production code, no driver field and no config key.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w132Rule is the narrow-repro paragraph of
// workflows/bug-fix/steps/repro.md, whitespace-normalized.
const w132Rule = "Reproduce the failure the work item names, as narrowly as it can be reproduced: the single test, suite or command that goes red. Do not run the project's whole test bar or every suite to \"confirm\" — `verification` runs the full bar later, mechanically; a repro that re-runs everything adds minutes and proves nothing the narrow run did not. When the failure only reproduces in the whole suite (an order-dependent test), say so in the repro document and keep the narrowest run that still shows it."

func TestW132ReproReproducesOnlyTheNamedFailureNarrowly(t *testing.T) {
	path := filepath.Join(shippedWorkflowsDir, "bug-fix", "steps", "repro.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	t.Logf("%s: %d bytes", path, len(raw))
	flat := w111Flat(string(raw))

	if n := strings.Count(flat, w132Rule); n != 1 {
		t.Fatalf("%s: narrow-repro rule appears %d times, want exactly 1:\n%s", path, n, w132Rule)
	}
	const item2 = "2. Reproduce it. Prefer, in order:"
	const item3 = "3. Write `artifacts/repro.md`:"
	start := strings.Index(flat, item2)
	end := strings.Index(flat, item3)
	if start < 0 || end < 0 {
		t.Fatalf("%s: item 2 lead-in found=%v, item 3 lead-in found=%v", path, start >= 0, end >= 0)
	}
	if at := strings.Index(flat, w132Rule); at < start || at > end {
		t.Fatalf("%s: narrow-repro rule at %d, want inside item 2 (between %d and %d)", path, at, start, end)
	}
}
