package test

// carries: docs/upgrade.md, test/w254_docs_set_acceptance_test.go, internal/contracttest/pf118_upgrade_section_contract_test.go

// pf118_upgrade_section_contract_test.go (polyforge#118, AC3): the
// epic-split change appends ONE section at the end of docs/upgrade.md —
// append-only, the sha256 pin moving in the same commit is
// TestW254UpgradeByteIdentical's own bar; this test pins the section
// exists, is the last one, and that the pin's upgrade-sections list
// carries its whole line (the daemon's mechanical merge unions whole
// lines only).

import (
	"strings"
	"testing"
)

// TestPF118UpgradeSectionContract pins the appended upgrade section and
// its list line in the pin's comment.
func TestPF118UpgradeSectionContract(t *testing.T) {
	upgrade := w68Raw(t, "docs/upgrade.md")
	heading := "## v0.2.4, the epic split filing procedure and the split metrics (polyforge#118)"
	if !strings.Contains(upgrade, heading) {
		t.Errorf("docs/upgrade.md must carry the appended section %q (polyforge#118 AC3: docs/upgrade.md 末尾追加一节同步更新)", heading)
	}
	// append-only: the new section lands at the END, after the epic
	// lane's section (the last one before this change).
	if strings.LastIndex(upgrade, heading) < strings.LastIndex(upgrade, "## v0.2.3, the epic lane (polyforge#117)") {
		t.Error("docs/upgrade.md is append-only — the new section must come after the epic lane's, never before it (polyforge#118 AC3)")
	}
	// the pin's list carries one whole line naming the section.
	pin := w68Raw(t, "test/w254_docs_set_acceptance_test.go")
	found := false
	for _, l := range strings.Split(pin, "\n") {
		if strings.HasPrefix(l, "// - ") && strings.Contains(l, "the epic split filing procedure and the split metrics") {
			found = true
		}
	}
	if !found {
		t.Error("the w254 pin's upgrade-sections list must carry one whole `// - ` line naming the appended section (AGENTS.md: each append adds one line at the end of the list, in the same commit) (polyforge#118)")
	}
}
