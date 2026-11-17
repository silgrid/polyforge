// carries: skills/polyforge/verbs/doctor.md, docs/reference/commands.md, docs/user-guide.md, docs/reference/workflow-contract.md, docs/upgrade.md, tools/install.sh

package test

// pf101_docs_contract_test.go (polyforge#101, AC6): the linked documents
// follow the change — the commands reference's doctor row names both
// guidance blocks and the new row, the user guide's doctor paragraph
// names both, the workflow contract's part 2 states the rule that rides
// beside the prohibited actions, and docs/upgrade.md gains one appended
// section (the byte-identity pin beside it moves in the same commit, as
// the AGENTS.md append rule demands). The doctor verb doc's own row and
// section landed with the code (u2); this test pins them too, so a later
// doc drift fails here.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pf101Read answers a repo file with its whitespace collapsed.
func pf101Read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

func TestPF101DocsSyncContract(t *testing.T) {
	// the commands reference: the fix appends both blocks, and the row
	// is named
	commands := pf101Read(t, filepath.Join("docs", "reference", "commands.md"))
	for _, want := range []string{
		"`--fix-harness-prompts` appends the native-SVG diagram-guidance block",
		"it appends the concise-output guidance block too (the `harness concise guidance` row)",
	} {
		if !strings.Contains(commands, want) {
			t.Errorf("docs/reference/commands.md's doctor row lacks %q", want)
		}
	}

	// the user guide: both guidance blocks named in the doctor paragraph
	guide := pf101Read(t, filepath.Join("docs", "user-guide.md"))
	if !strings.Contains(guide, "retired chat-subscription state and diagram and concise-output guidance") {
		t.Errorf("docs/user-guide.md's doctor paragraph must name both guidance blocks")
	}

	// the workflow contract: the rule rides part 2, unchangeable by a lane
	contract := pf101Read(t, filepath.Join("docs", "reference", "workflow-contract.md"))
	for _, want := range []string{
		"Its text is `harness.StepProhibitedActions`; a lane cannot change it.",
		"Beside the list rides the concise output-discipline rule — `harness.ConciseRule`, the one short rule the harness context files and every task input also carry; a lane cannot change it either.",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("docs/reference/workflow-contract.md lacks %q", contract)
		}
	}

	// the doctor verb doc: the new row, the claude target resolution and
	// the section
	doctor := pf101Read(t, filepath.Join("skills", "polyforge", "verbs", "doctor.md"))
	for _, want := range []string{
		"`harness concise guidance`",
		"`~/.claude/CLAUDE.md`, else `~/.claude/AGENTS.md`",
		"<!-- polyforge:concise-guidance:begin -->",
	} {
		if !strings.Contains(doctor, want) {
			t.Errorf("skills/polyforge/verbs/doctor.md lacks %q", want)
		}
	}

	// upgrade.md: one appended section for this change
	upgrade := pf101Read(t, filepath.Join("docs", "upgrade.md"))
	if !strings.Contains(upgrade, "## v0.2.3, harness guidance: the concise-output block, doctor's claude target and the installer's auto-fix") {
		t.Errorf("docs/upgrade.md lacks the appended harness-guidance section")
	}
	for _, want := range []string{"`harness.ConciseRule`", "`harness concise guidance`", "tools/install.sh runs `pf doctor --fix-harness-prompts`"} {
		if !strings.Contains(upgrade, want) {
			t.Errorf("docs/upgrade.md's new section lacks %q", want)
		}
	}
}
