// carries: internal/polyforge/daemon/analysis_prompt.md

package test

// w72_analysis_prompt_contract_test.go (polyforge#72, u5 — AC7): the
// analysis task's prompt template — embedded into the daemon binary
// with go:embed and read here from the repository source (the embedded
// copy IS this file at compile time; the daemon package fails to build
// without it). The contract: the ticket's 7-item report format defined
// by skills/polyforge/verbs/submit.md, with each item's heading shape
// fixed (`### <n>. <标题>`), a separate epic-upgrade recommendation
// section after the seven (ruling 18's default rules, recommendation
// only, never a sub-ticket), the narrow write command the task writes
// through, and no project or component name anywhere (the template ships
// with the binary and serves any project).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW72AnalysisPromptTemplateContract(t *testing.T) {
	rel := filepath.Join("internal", "polyforge", "daemon", "analysis_prompt.md")
	raw, err := os.ReadFile(filepath.Join(v1Root(t), rel))
	if err != nil {
		t.Fatalf("%s must exist — the analysis prompt template the daemon embeds: %v", rel, err)
	}
	doc := string(raw)
	flat := strings.Join(strings.Fields(doc), " ")

	// The 7-item report format, one heading shape for every item.
	for _, want := range []string{
		"### <n>. <标题>",
		"改动范围",
		"证明命令和运行目录",
		"断言旧行为的已有测试与文档",
		"依赖或冲突",
		"相关记忆",
		"风险",
		"待决问题",
		"不拍板",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s must carry %q — the 7-item report format and its heading shape (AC7)", rel, want)
		}
	}

	// The write path the task uses, and the continue-don't-restart rule
	// (AC10's prompt side).
	for _, want := range []string{
		"pf task analysis-write",
		"接着写",
		"不从头",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s must carry %q — the narrow write command and the continuation rule (AC7/AC10)", rel, want)
		}
	}

	// The epic-upgrade recommendation section after the seven, on
	// ruling 18's default rules: recommendation only, never a sub-ticket.
	for _, want := range []string{
		"升级 epic 建议",
		"建议升级",
		"不建议",
		"3 个组件",
		"> 8 个",
		"> 30 个文件",
		"批量修改不算",
		"只给建议",
		"不要建子票",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s must carry %q — the epic-upgrade section and ruling 18's default rules (AC7)", rel, want)
		}
	}

	// No project or component name: the template ships with the binary
	// and serves any project (AC7).
	for _, banned := range []string{"polyforge", "aihub", "pi-web", "webui"} {
		if strings.Contains(strings.ToLower(doc), banned) {
			t.Errorf("%s names the project/component %q — the template text is project-neutral (AC7)", rel, banned)
		}
	}
}
