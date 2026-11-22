// carries: docs/reference/commands.md, docs/reference/uds-routes.md, docs/reference/workflow-contract.md, docs/bundles.md, docs/contributing.md, docs/user-guide.md, skills/polyforge/verbs/file.md, skills/polyforge/verbs/submit.md

package test

// analysis_redo_contract_test.go (polyforge#233, u6 — AC4): the redo verb is
// documented where the owner finds it — the CLI reference, the filing and
// submit skill fragments, the route table and the mechanism pages — with its
// use cases (old format, stale base, changed scope), its eligible states
// (queued or blocked), where the old analysis goes (`## 历次分析`), the
// unconfirmed/confirm flow and the local-daemon guarantee.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPF233AnalysisRedoDocumentation(t *testing.T) {
	docs := map[string][]string{
		filepath.Join("docs", "reference", "commands.md"): {
			"pf file analysis redo <id>",
			"queued or blocked",
			"## 历次分析",
			"unconfirmed",
			"strong guarantee covers this machine's daemon only",
		},
		filepath.Join("docs", "reference", "uds-routes.md"): {
			"`/analysis/redo`",
			"step and task processes refused",
		},
		filepath.Join("docs", "reference", "workflow-contract.md"): {
			"pf file analysis redo",
			"## 历次分析",
		},
		filepath.Join("docs", "bundles.md"): {
			"pf file analysis redo",
			"## 历次分析",
		},
		filepath.Join("docs", "contributing.md"): {
			"pf file analysis redo",
			"## 历次分析",
		},
		filepath.Join("docs", "user-guide.md"): {
			"pf file analysis redo",
			"## 历次分析",
		},
		filepath.Join("skills", "polyforge", "verbs", "file.md"): {
			"pf file analysis redo",
			"queued or blocked",
			"## 历次分析",
			"unconfirmed",
		},
		filepath.Join("skills", "polyforge", "verbs", "submit.md"): {
			"pf file analysis redo",
			"## 历次分析",
		},
	}
	for rel, wants := range docs {
		flat := pf27Flat(pf27Read(t, rel))
		for _, want := range wants {
			if !strings.Contains(flat, want) {
				t.Errorf("%s must carry %q — the redo verb's use cases, eligible states, archive section and confirm flow are the owner's documentation of polyforge#233 (AC4)", rel, want)
			}
		}
	}

	// `pf help file` is the fourth entry AC4 names; the real help output is
	// pinned by the CLI package's TestPF233HelpFileNamesRedo, and the one
	// usage source carries the same line.
	usage := pf27Flat(pf27Read(t, filepath.Join("internal", "polyforge", "cli", "verbs_aihub.go")))
	for _, want := range []string{"pf file analysis redo <id>", "旧分析", "## 历次分析"} {
		if !strings.Contains(usage, want) {
			t.Errorf("the `pf help file` usage source must carry %q", want)
		}
	}

	// The upgrade note is append-only; its own pin test proves the bytes and
	// the section list, and this pin proves the redo section is there.
	upgrade := pf27Flat(pf27Read(t, filepath.Join("docs", "upgrade.md")))
	if !strings.Contains(upgrade, "pf file analysis redo") || !strings.Contains(upgrade, "## 历次分析") {
		t.Errorf("docs/upgrade.md must carry the redo upgrade section (the verb, the archive and the confirm flow)")
	}
}
