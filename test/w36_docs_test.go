// carries: workflows/develop/README.md, docs/reference/config.md, docs/concepts.md, docs/reference/commands.md, docs/bundles.md, skills/polyforge/SKILL.md, skills/polyforge/verbs/ship.md

package test

// w36_docs_test.go (u10, AC8): the documents teach the develop lane's
// three merge routes — the lane merges, a person merges (the project
// declares `steps.<project>.merge.by: human`) and the skipped merge —
// which of them enter the integrate lane's pool, and which of GitHub's
// three merge methods the human merge's landing rules take. The upgrade
// note is appended and the w254 pin moves in the same commit.

import (
	"strings"
	"testing"
)

func TestW36DocsTeachTheThreeMergeRoutes(t *testing.T) {
	need := func(path, body string, needles ...string) {
		t.Helper()
		norm := w254Norm(body)
		for _, n := range needles {
			if !strings.Contains(norm, n) {
				t.Errorf("%s does not say %q", path, n)
			}
		}
	}

	// the lane's narrative: the three routes and which enters integrate
	need("workflows/develop/README.md", w254Read(t, "workflows/develop/README.md"),
		"Merging by a person",
		"steps.<project>.merge.by: human",
		"landed", "published",
		"Rebase and merge", "Squash and merge", "Create a merge commit",
		"human_mergeable",
	)
	// AC8: each route says whether it enters the integration lane
	readme := w254Read(t, "workflows/develop/README.md")
	for _, route := range []struct{ heading, enters string }{
		{"Merging by a person", "lands on the merge rules and enters the integration lane's pool"},
		{"Skipping merge", "never enters an integration batch"},
	} {
		sec := w521Section(readme, route.heading)
		if sec == "" {
			t.Fatalf("workflows/develop/README.md has no \"%s\" section", route.heading)
		}
		if !strings.Contains(w254Norm(sec), route.enters) {
			t.Errorf("workflows/develop/README.md's %s section does not say the route's integrate reach: %q", route.heading, route.enters)
		}
	}

	// the config reference: the by key, its class, the claim's freeze, the
	// refusals, the per-lane reach
	config := w254Read(t, "docs/reference/config.md")
	need("docs/reference/config.md", config,
		"steps.<project>.<step-id>.by",
		"human",
		"next-claim",
		"`human_mergeable: true`",
		"refuse",
	)
	bySec := w521Section(config, "`steps.<project>.<step-id>.by`")
	if bySec == "" {
		t.Fatalf("docs/reference/config.md has no `steps.<project>.<step-id>.by` section")
	}
	need("docs/reference/config.md (by section)", bySec,
		"runtime-state/step-modes.json",
		"micro",
		"skip",
	)
	// AC8: the config reference names the three routes' integrate reach
	// and the GitHub methods the person's merge may use (r1#3)
	need("docs/reference/config.md (by section, the routes' reach)", bySec,
		"landed", "integration", "published",
		"Create a merge commit", "Rebase and merge", "Squash and merge",
	)
	need("docs/concepts.md", w254Read(t, "docs/concepts.md"),
		"enters the batch like the lane's own merges do",
	)

	// the concepts page: the person's merge among the lane's ends
	need("docs/concepts.md", w254Read(t, "docs/concepts.md"),
		"steps.<project>.merge.by: human",
		"Rebase and merge",
		"Squash and merge",
	)

	// the command reference: merge.py's human mode, its exit 3 and the
	// landing record's merge flags
	commands := w254Read(t, "docs/reference/commands.md")
	need("docs/reference/commands.md (Step scripts)", w521Section(commands, "Step scripts"),
		"step-modes.json",
		"exit 3",
		"merged_by_human",
	)

	// the bundles chapter and the routing skill: the person's merge is a
	// route of the develop lane
	need("docs/bundles.md", w254Read(t, "docs/bundles.md"),
		"steps.<project>.merge.by: human",
	)
	need("skills/polyforge/SKILL.md", w254Read(t, "skills/polyforge/SKILL.md"),
		"steps.<project>.merge.by: human",
	)
	ship := w254Read(t, "skills/polyforge/verbs/ship.md")
	if strings.Contains(w254Norm(ship), "The merge goes ahead unattended once publish passes") {
		t.Errorf("skills/polyforge/verbs/ship.md still says the merge always goes ahead unattended (a project declaring steps.<project>.merge.by: human hands it to the person)")
	}
	need("skills/polyforge/verbs/ship.md", ship,
		"steps.<project>.merge.by: human",
	)

	// the upgrade note names the new key, the mode and the record's shape
	need("docs/upgrade.md", w254Read(t, "docs/upgrade.md"),
		"steps.<project>.merge.by: human",
		"exit 3",
	)
}
