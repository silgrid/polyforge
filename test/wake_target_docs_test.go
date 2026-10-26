package test

// wake_target_docs_test.go
// (wi_zgL0mlf5zegK, artifacts/e2e-scenarios.md) acceptance scenario
// "Docs cover the behavior change": docs/upgrade.md, docs/user-guide.md
// and skills/polyforge/SKILL.md change with the code. Authored against
// artifacts/spec.md BEFORE the code: every assertion below is red on
// the pre-implementation tree (no doc names the new verb or the new
// routing) and turns green only on the delivered docs. Assertions are
// on spec-pinned tokens, never wording — the prose halves of the docs
// criterion (what the upgrade note explains about the tag refresh and
// the absent backfill, what the guide says about the scoped routing,
// what the skill says about adopt never running automatically) are
// judged by review against the same spec.

import (
	"os"
	"strings"
	"testing"
)

func TestDocsCoverWakeTargetHandover(t *testing.T) {
	for _, tc := range []struct {
		file string
		want []string
	}{
		// docs/user-guide.md documents the new verb and the scoped routing
		{"../docs/user-guide.md", []string{"pf watch adopt"}},
		// skills/polyforge/SKILL.md documents that adopt runs only on an
		// explicit user request — never automatic
		{"../skills/polyforge/SKILL.md", []string{"pf watch adopt"}},
		// docs/upgrade.md documents the chat workspace tag (recorded on
		// every pf chat init, refreshed on re-init, no backfill for
		// pre-existing chats) and the adopt verb with its refusals
		{"../docs/upgrade.md", []string{"pf watch adopt", "backfill"}},
	} {
		raw, err := os.ReadFile(tc.file)
		if err != nil {
			t.Errorf("%s: %v", tc.file, err)
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(string(raw), want) {
				t.Errorf("%s must cover %q (the spec's docs criterion: docs change with the code)", tc.file, want)
			}
		}
	}
}
