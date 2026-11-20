package test

// carries: docs/upgrade.md, test/w254_docs_set_acceptance_test.go, internal/contracttest/w174_upgrade_section_contract_test.go

// w174_upgrade_section_contract_test.go (polyforge#174, u5): the
// split-attrs change appends ONE section at the end of docs/upgrade.md
// — append-only, the sha256 pin moving in the same commit is
// TestW254UpgradeByteIdentical's own bar; this test pins the section
// exists, heads with the batch's one version (v0.2.4, the latest tag's
// next patch — pf123's own bar), is the LAST one, quotes the window
// SQL (TestDocsQuoteTheWindowSQL's own bar), and that the pin's
// upgrade-sections list carries its whole line.

import (
	"strings"
	"testing"
)

// TestW174UpgradeSectionContract pins the appended upgrade section and
// its list line in the pin's comment.
func TestW174UpgradeSectionContract(t *testing.T) {
	upgrade := w68Raw(t, "docs/upgrade.md")
	heading := "## v0.2.4, the split attrs one shape and the data migration (polyforge#174)"
	if !strings.Contains(upgrade, heading) {
		// red-state safety (review r1#3): the slicing below would panic
		// on a missing heading — stop here instead.
		t.Fatalf("docs/upgrade.md must carry the appended section %q (polyforge#174: docs/upgrade.md 末尾追加一节同步更新)", heading)
	}
	// append-only: the new section lands at the END, after the batch's
	// previous last one.
	if strings.LastIndex(upgrade, heading) < strings.LastIndex(upgrade, "## v0.2.4, the build fast check's flaky re-run and the red-state limit (polyforge#157)") {
		t.Error("docs/upgrade.md is append-only — the new section must come after polyforge#157's, never before it (polyforge#174)")
	}
	// the section states the migration's load-bearing facts: the one
	// shape, the window SQL, the review queries and the whole-batch
	// refusal.
	for _, want := range []string{
		"One shape, write and read",
		"deploy/sql/v0.2.4",
		"split_attrs_before_w174",
		"refuses the whole batch",
		"the old ids",
	} {
		if !strings.Contains(upgrade[strings.LastIndex(upgrade, heading):], want) {
			t.Errorf("the appended section must state %q — the migration's blast radius, refusal and comparison (polyforge#174)", want)
		}
	}
	// the pin's list carries one whole line naming the section.
	pin := w68Raw(t, "test/w254_docs_set_acceptance_test.go")
	found := false
	for _, l := range strings.Split(pin, "\n") {
		if strings.HasPrefix(l, "// - ") && strings.Contains(l, "the split attrs one shape and the data migration") {
			found = true
		}
	}
	if !found {
		t.Error("the w254 pin's upgrade-sections list must carry one whole `// - ` line naming the appended section (AGENTS.md: each append adds one line at the end of the list, in the same commit) (polyforge#174)")
	}
}
