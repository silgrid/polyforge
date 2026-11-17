package test

// carries: docs/concepts.md, docs/bundles.md, docs/reference/config.md, internal/contracttest/pf118_epic_split_docs_contract_test.go

// pf118_epic_split_docs_contract_test.go (polyforge#118, AC3): the three
// doc faces the ticket names — 车道 (bundles.md), 配置 (reference/config.md),
// 概念 (concepts.md) — each carries the epic split's own fact, the
// procedure itself stays in the routing skill (one fact, one place), and
// the added texts stay project-neutral.

import (
	"regexp"
	"strings"
	"testing"
)

// TestPF118EpicSplitDocsContract pins the split flow's coverage across
// the three doc faces and its project neutrality.
func TestPF118EpicSplitDocsContract(t *testing.T) {
	// 概念 — concepts.md: the Work item term carries the split concept.
	concepts := w68Flat(t, "docs/concepts.md")
	for _, want := range []string{
		"one sub-ticket per decomposition unit",
		"check-first per unit, so a re-run files no duplicate",
		"routing skill's file verb owns the filing steps",
	} {
		if !strings.Contains(concepts, want) {
			t.Errorf("docs/concepts.md (the Work item term) must state %q — the split concept a newcomer meets (polyforge#118 AC3: 概念)", want)
		}
	}

	// 车道 — bundles.md: the children_branch bullet names the epic
	// lane's use for an owner-confirmed split, the hold, the bundle move
	// and the check-first filing, and points at the skill for the steps.
	bundles := w68Flat(t, "docs/bundles.md")
	for _, want := range []string{
		"holds the original out of the ready queue",
		"pf file edit --bundle epic",
		"check-first, one per unit",
	} {
		if !strings.Contains(bundles, want) {
			t.Errorf("docs/bundles.md (the children_branch bullet) must state %q — the epic lane's use for an owner-confirmed split (polyforge#118 AC3: 车道)", want)
		}
	}

	// 配置 — reference/config.md: the epic thresholds' row names what
	// they feed and where the confirmed procedure lives.
	cfg := w68Flat(t, "docs/reference/config.md")
	for _, want := range []string{
		"升级 epic 建议",
		"the owner-confirmed split's filing steps are the routing skill's file verb's",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("docs/reference/config.md (the epic_components row) must state %q — the thresholds feed the upgrade recommendation (polyforge#118 AC3: 配置)", want)
		}
	}

	// the added texts stay project-neutral (AC3): scoped to the
	// passages this change adds, whole files carry the product's own
	// config paths (.polyforge/, polyforge.yaml) that are not routing
	// targets.
	for rel, text := range map[string]string{
		"docs/concepts.md (the Work item term)":              pf118Section(t, w68Raw(t, "docs/concepts.md"), "### Work item", "### "),
		"docs/bundles.md (the children_branch bullet)":       pf118Bullet(t, w68Raw(t, "docs/bundles.md"), "`children_branch:`"),
		"docs/reference/config.md (the epic_components row)": pf118Bullet(t, w68Raw(t, "docs/reference/config.md"), "`epic_components`"),
	} {
		for _, m := range regexp.MustCompile(`polyforge(#[0-9]+)?`).FindAllStringSubmatch(text, -1) {
			if m[1] == "" {
				t.Errorf("%s names the project outside a ticket citation: %q (polyforge#118 AC3: 文字与项目无关)", rel, m[0])
			}
		}
	}
}

// pf118Bullet answers the one list/table line (row) starting with or
// containing marker, whole.
func pf118Bullet(t *testing.T, body, marker string) string {
	t.Helper()
	for _, l := range strings.Split(body, "\n") {
		if strings.Contains(l, marker) {
			return l
		}
	}
	t.Fatalf("no line carrying %q", marker)
	return ""
}
