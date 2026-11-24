// carries: skills/output-format/SKILL.md, workflows/_shared/batch_questions.md, workflows/_shared/build_subagents.md, workflows/_shared/readability_checklist.md, workflows/develop/steps/build.md

package test

// readability_contract_test.go (polyforge#236): the three text contracts of
// the "text quality (3/3)" change — the question card's Chinese readability
// rules and the "check the facts yourself" rule (AC1), the sub-agent brief's
// STE80 rules (AC2), and the advisory-only readability checklist (AC3).
//
// The acceptance itself is text the owner reads (a skill, two shared
// fragments and a step's rendered prompt), so the text assertion IS the
// behaviour here — test_quality.md's exception for a criterion that is itself
// a text or a document. The test-quality record of each unit in
// artifacts/plan.md's ## Units names the change that would turn it red.

import (
	"strings"
	"testing"
)

// readabilityCardRules is the question card's rule set (AC1): the first line
// states the decision in one short sentence, then the evidence is itemized,
// then the four-column table; one thing per sentence; a supplement gets its
// own sentence; identifiers and paths take code format; one evidence item a
// line within 120 characters; facts checkable from the repo, tools and memory
// are checked first, and the card asks only for a decision.
var readabilityCardRules = []string{
	"第一行用一句短句说明本次要定的决定",
	"接着逐条列出证据",
	"没有证据时直接从表格开始",
	"每句表达一件事",
	"单句控制在 40 字以内",
	"英文句控制在 20 词以内",
	"需要补充说明时另起一句",
	"括号里只放一句完整说明",
	"标识符、路径和命令用反引号代码格式标注",
	"证据一条一行",
	"单条控制在 120 字以内",
	"能从仓库、工具和记忆查到的事实先查清并写进证据",
	"问题卡只承载需要 owner 决定的取舍",
}

// TestQuestionCardReadabilityContract: the skill's envelope face and the
// shared batch_questions fragment carry the same positive readability rules,
// and develop/build's real rendered definition carries them through the
// fragment.
func TestQuestionCardReadabilityContract(t *testing.T) {
	for _, rel := range []string{
		"skills/output-format/SKILL.md",
		"workflows/_shared/batch_questions.md",
	} {
		flat := w68Flat(t, rel)
		for _, want := range readabilityCardRules {
			if !strings.Contains(flat, want) {
				t.Errorf("%s lacks the question-card rule %q", rel, want)
			}
		}
	}
	rendered := grillStepDef(t, "develop", "build")
	for _, want := range readabilityCardRules {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered develop/build lacks the question-card rule %q (batch_questions must render it)", want)
		}
	}
}
