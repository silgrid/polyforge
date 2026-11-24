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
	"os"
	"path/filepath"
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
	rendered := strings.Join(strings.Fields(grillStepDef(t, "develop", "build")), " ")
	for _, want := range readabilityCardRules {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered develop/build lacks the question-card rule %q (batch_questions must render it)", want)
		}
	}
}

// readabilitySTE80Rules are the sub-agent brief's writing rules (AC2): short
// sentences, one instruction per sentence, active voice, a full stop between
// independent instructions, one precise verb in place of a phrasal verb. A
// brief's prose follows them; command lines, paths and code stay verbatim.
var readabilitySTE80Rules = []string{
	"STE80",
	"short sentences",
	"one instruction per sentence",
	"active voice",
	"a full stop between independent instructions",
	"one precise verb in place of a phrasal verb",
	"command lines, paths and code stay verbatim",
}

// TestSubagentBriefSTE80Contract: the shared build_subagents fragment carries
// the five STE80 rules, and develop/build's rendered ## Sub-agents section
// carries them too.
func TestSubagentBriefSTE80Contract(t *testing.T) {
	flat := w68Flat(t, "workflows/_shared/build_subagents.md")
	for _, want := range readabilitySTE80Rules {
		if !strings.Contains(flat, want) {
			t.Errorf("workflows/_shared/build_subagents.md lacks the brief writing rule %q", want)
		}
	}
	rendered := strings.Join(strings.Fields(grillStepDef(t, "develop", "build")), " ")
	for _, want := range readabilitySTE80Rules {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered develop/build lacks the brief writing rule %q (build_subagents must render it)", want)
		}
	}
}

// readabilityChecklistRules are the advisory checklist's mechanical items and
// its advisory-only statement (AC3): the length hints, the semicolon and the
// evidence-line hints, the threshold rule, and the fact that no gate reads it.
var readabilityChecklistRules = []string{
	"只提示，不阻断",
	"中文单句 40 字以内",
	"英文单句 20 词以内",
	"单条 120 字以内",
	"`;` 或 `；`",
	"长度等于阈值时不提示，超过阈值才提示",
	"没有脚本读取它",
	"没有步骤因它失败",
}

// TestReadabilityChecklistRenderedAsAdvisory: workflows/_shared now carries
// the advisory checklist, develop/build renders it exactly once, and no other
// step file, lane, verification conf or shared script reads it — nothing can
// fail because of it.
func TestReadabilityChecklistRenderedAsAdvisory(t *testing.T) {
	root := v1Root(t)
	raw, err := os.ReadFile(filepath.Join(root, "workflows", "_shared", "readability_checklist.md"))
	if err != nil {
		t.Fatalf("workflows/_shared/readability_checklist.md: %v — the advisory fragment must exist", err)
	}
	body := strings.TrimSpace(string(raw))
	flat := strings.Join(strings.Fields(body), " ")
	for _, want := range readabilityChecklistRules {
		if !strings.Contains(flat, want) {
			t.Errorf("workflows/_shared/readability_checklist.md lacks %q", want)
		}
	}

	rendered := grillStepDef(t, "develop", "build")
	if n := strings.Count(rendered, body); n != 1 {
		t.Errorf("the rendered develop/build carries the advisory checklist %d times, want exactly 1", n)
	}

	// The fragment is included once, from develop/build only: every other
	// workflow file (step definitions, lane fragments) leaves it out.
	var includers []string
	if err := filepath.Walk(filepath.Join(root, "workflows"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(b), "{{readability_checklist}}") {
			rel, _ := filepath.Rel(root, path)
			includers = append(includers, filepath.ToSlash(rel))
		}
		return nil
	}); err != nil {
		t.Fatalf("walk workflows/: %v", err)
	}
	if len(includers) != 1 || includers[0] != "workflows/develop/steps/build.md" {
		t.Errorf("{{readability_checklist}} is included by %v, want only workflows/develop/steps/build.md", includers)
	}

	// No gate surface reads the checklist: not a verification conf, a lane,
	// or a shared script.
	var surfaces []string
	for _, pat := range []string{
		filepath.Join(root, ".polyforge", "*.conf"),
		filepath.Join(root, "*", ".polyforge", "verify-scope.conf"),
		filepath.Join(root, "workflows", "*", "workflow.yaml"),
	} {
		m, _ := filepath.Glob(pat)
		surfaces = append(surfaces, m...)
	}
	if err := filepath.Walk(filepath.Join(root, "workflows", "_shared", "scripts"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(path, ".py") {
			surfaces = append(surfaces, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk workflows/_shared/scripts: %v", err)
	}
	for _, path := range surfaces {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v: %v", path, err)
		}
		if strings.Contains(string(b), "readability_checklist") || strings.Contains(string(b), "readability") {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s reads the advisory readability checklist: the checklist is a hint, never a gate", filepath.ToSlash(rel))
		}
	}
}
