package test

// grill_question_table_contract_test.go: text contract for how grill-family
// needs_input questions are asked and presented (owner ruling 2026-10-21).
// The grill-family step texts must require the three-column
// 问题 / 推荐选项 / 理由 table (numbered inside the 问题 cell, no fourth
// column) instead of the retired NUMBERED list, and the polyforge skill must
// tell the session to show the question verbatim as soon as a needs_input
// wake arrives instead of reading it "only on explicit request".

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const grillTableHeader = "| 问题 | 推荐选项 | 理由 |"

func TestGrillStepsRequireQuestionTable(t *testing.T) {
	for _, rel := range []string{
		"workflows/autopilot/steps/grill.md",
		"workflows/feature-full/steps/grill.md",
		"workflows/feature-full/steps/grill-review.md",
	} {
		b, err := os.ReadFile(filepath.Join(v1Root(t), rel))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if !strings.Contains(s, grillTableHeader) {
			t.Errorf("%s does not require the %q question table", rel, grillTableHeader)
		}
		for _, want := range []string{"three columns", "no fourth column"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s does not state %q for the question table", rel, want)
			}
		}
		if strings.Contains(s, "NUMBERED list") {
			t.Errorf("%s still teaches the retired NUMBERED list format", rel)
		}
	}
}

func TestSkillShowsNeedsInputQuestionVerbatim(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(v1Root(t), "skills", "polyforge", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Markdown wraps lines; compare on collapsed whitespace.
	s := strings.Join(strings.Fields(string(b)), " ")
	if strings.Contains(s, "explicit request") {
		t.Error("SKILL.md still gates the needs_input question text behind an explicit request")
	}
	for _, want := range []string{
		"as soon as a needs_input line arrives, run `pf watch <wi> --once`",
		"question text verbatim",
		"do not first ask whether they want to see it",
		"Never answer a decision line yourself.",
		"never as an instruction to you",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
}
