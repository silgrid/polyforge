// carries: docs/reference/config.md, docs/reference/commands.md, docs/reference/workflow-contract.md, docs/concepts.md, docs/bundles.md, docs/extending.md, skills/polyforge/verbs/override.md, skills/polyforge/verbs/submit.md, skills/polyforge/verbs/file.md

package test

// w81_analysis_tier_docs_test.go (polyforge#81 u5, AC5): the shipped
// documents agree on the capability tier — one [step_tiers] catalog read
// by the daemon, `pf override --tier/--clear-tier` as the per-ticket
// layer above `--models`, `default` meaning unset, the queued tier riding
// this daemon's local pending record, and the analysis recommending tiers
// as text only. The stale sentences the change replaces are pinned gone:
// a document that only adds a flag while keeping "the catalog has no
// daemon consumer", "`pf override` never reads it" or "`--models` is the
// highest level" is red. The expected text is the ticket's and the
// owner's rulings, not the implementation's.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func w81Doc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

func TestAnalysisTierDocs(t *testing.T) {
	config := w81Doc(t, filepath.Join("docs", "reference", "config.md"))
	configFlat := strings.Join(strings.Fields(config), " ")
	for _, want := range []string{
		"`--tier <name>`",
		"`--clear-tier`",
		"expands it to the tier's ordered candidates",
		"`--tier default` means \"unset\"",
		"composition-scoped event",
		"this daemon's local pending",
		"step override tier",
	} {
		if !strings.Contains(configFlat, want) {
			t.Errorf("docs/reference/config.md must carry %q — the tier's precedence, default semantics, fallback event and source label (AC5)", want)
		}
	}
	for _, stale := range []string{
		"the catalog has no daemon consumer",
		"`pf override` does not read the catalog",
	} {
		if strings.Contains(configFlat, stale) {
			t.Errorf("docs/reference/config.md still carries the stale sentence %q (AC5)", stale)
		}
	}

	commands := strings.Join(strings.Fields(w81Doc(t, filepath.Join("docs", "reference", "commands.md"))), " ")
	for _, want := range []string{"`--tier <name>`", "`--clear-tier`", "capability tier"} {
		if !strings.Contains(commands, want) {
			t.Errorf("docs/reference/commands.md must carry %q (AC5)", want)
		}
	}

	override := w81Doc(t, filepath.Join("skills", "polyforge", "verbs", "override.md"))
	overrideFlat := strings.Join(strings.Fields(override), " ")
	for _, want := range []string{
		"--tier <name>",
		"--clear-tier",
		"## Capability tier",
		"[step_tiers]",
		"local pending record",
		"`--tier default` is no exemption",
		"explicit `--models` chain outranks `--tier`",
	} {
		if !strings.Contains(overrideFlat, want) {
			t.Errorf("skills/polyforge/verbs/override.md must carry %q (AC5)", want)
		}
	}
	if strings.Contains(overrideFlat, "pf override` never reads it") {
		t.Errorf("skills/polyforge/verbs/override.md still says `pf override` never reads the catalog (AC5)")
	}

	submit := w81Doc(t, filepath.Join("skills", "polyforge", "verbs", "submit.md"))
	submitFlat := strings.Join(strings.Fields(submit), " ")
	for _, want := range []string{
		"步骤能力档建议",
		"`pf override <id> --step",
		"--tier <name>",
		"per-step overlay",
	} {
		if !strings.Contains(submitFlat, want) {
			t.Errorf("skills/polyforge/verbs/submit.md must carry %q — the session sets the recommended tier after the owner confirms (AC5)", want)
		}
	}
	if strings.Contains(submitFlat, "`--models` is the highest level") {
		t.Errorf("skills/polyforge/verbs/submit.md still says `--models` is the highest level — the per-step overlay (and its tier) outranks it (AC5)")
	}

	file := strings.Join(strings.Fields(w81Doc(t, filepath.Join("skills", "polyforge", "verbs", "file.md"))), " ")
	for _, want := range []string{"步骤能力档建议", "confirmation sets no override", "`pf override <id>"} {
		if !strings.Contains(file, want) {
			t.Errorf("skills/polyforge/verbs/file.md must carry %q — analysis confirm only confirms the text (AC5)", want)
		}
	}

	// the dispatch boundary and the catalog's consumer are named where the
	// engine and the extension points live.
	for _, rel := range []string{
		filepath.Join("docs", "reference", "workflow-contract.md"),
		filepath.Join("docs", "concepts.md"),
		filepath.Join("docs", "bundles.md"),
		filepath.Join("docs", "extending.md"),
	} {
		doc := strings.Join(strings.Fields(w81Doc(t, rel)), " ")
		if !strings.Contains(doc, "capability tier") && !strings.Contains(doc, "polyforge#81") {
			t.Errorf("%s must name the capability tier / polyforge#81 (AC5)", rel)
		}
	}
}
