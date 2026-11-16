package test

// w294_diagram_svg_contract_test.go: a text contract.
//
// Work item #294 moves the diagram guidance to native SVG: the `harness
// diagram guidance` doctor row passes only the SVG block, warns legacy
// mermaid-only guidance, and --fix-harness-prompts appends the SVG block;
// the output-format skill carries the same rule. The block itself and the
// detection are pinned by the cli unit tests; this test pins the shipped
// text — the doctor verb's authority (verbs/doctor.md), the command
// reference row, and the output-format skill. Every absence check is
// paired with a positive marker in the same file, so none passes
// vacuously. It is red on the base tree (none of the SVG markers existed).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w294Read answers a repo file with its whitespace collapsed (markdown
// wraps lines).
func w294Read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), rel))
	if err != nil {
		t.Fatalf("%v", err)
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

func TestW294DoctorDocsTeachSVGGuidance(t *testing.T) {
	doctor := w294Read(t, filepath.Join("skills", "polyforge", "verbs", "doctor.md"))
	for _, want := range []string{
		"<!-- polyforge:diagram-guidance:svg:begin -->", // pass rule + rollback anchor
		"<!-- polyforge:diagram-guidance:svg:end -->",
		"A file passes only when it carries the SVG block",
		"is reported `legacy`, a `warn`, never a pass",             // legacy-warn rule
		"`<!-- polyforge:diagram-guidance:begin -->`",              // the legacy marker named
		"SVG-first with mermaid as history",                        // the block's stance
		"`pass` / `legacy` / `missing` / `no file` / `unreadable`", // the row vocabulary
		"never rewrites or removes a byte already there",           // append-only
		"To roll back, delete only the SVG block",                  // rollback line
		"## 输出规范：图形优先（原生 SVG）",                                     // the block heading
	} {
		if !strings.Contains(doctor, want) {
			t.Errorf("verbs/doctor.md lacks %q", want)
		}
	}
	for _, gone := range []string{"mermaid first", "prefer mermaid", "with `mermaid` or `svg` in the file"} {
		if strings.Contains(doctor, gone) {
			t.Errorf("verbs/doctor.md still carries the expired mermaid-first wording %q", gone)
		}
	}

	commands := w294Read(t, filepath.Join("docs", "reference", "commands.md"))
	for _, want := range []string{"`--fix-harness-prompts` appends the native-SVG diagram-guidance block", "warns as `legacy`"} {
		if !strings.Contains(commands, want) {
			t.Errorf("docs/reference/commands.md's doctor row lacks %q", want)
		}
	}
}

func TestW294OutputFormatSkillSVGRule(t *testing.T) {
	skill := w294Read(t, filepath.Join("skills", "output-format", "SKILL.md"))
	for _, want := range []string{
		"## 图形输出（原生 SVG）",
		"```svg",
		"historical compatibility only",             // mermaid = history
		"add the diagram unasked",                   // proactive timing
		"≥3 subjects/components",                    // timing trigger
		"Element text ≤2 lines, at most 8 elements", // compactness
		"one structure per diagram",                 // compactness
		"viewBox",                                   // quality baseline
		"no hard-coded bright colours",              // quality baseline
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("skills/output-format/SKILL.md lacks %q", want)
		}
	}
	for _, gone := range []string{"优先用 mermaid", "prefer mermaid"} {
		if strings.Contains(skill, gone) {
			t.Errorf("skills/output-format/SKILL.md still instructs %q", gone)
		}
	}
}
