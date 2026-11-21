// carries: internal/polyforge/daemon/analysis_prompt.md

package test

// w72_analysis_prompt_contract_test.go (polyforge#72, u5 — AC7;
// polyforge#89 AC3; polyforge#213 AC1): the
// analysis task's prompt template — embedded into the daemon binary
// with go:embed and read here from the repository source (the embedded
// copy IS this file at compile time; the daemon package fails to build
// without it). The contract: the ticket's 7-item report format defined
// by skills/polyforge/verbs/submit.md, with each item's heading shape
// fixed (`### <n>. <标题>`), a separate epic-upgrade recommendation
// section after the seven (ruling 18's rules, recommendation
// only, never a sub-ticket; judged on the component division and the
// effective thresholds the daemon attaches after the template —
// polyforge#89 — the defaults 3 / 8 / 30 applying without it), the
// narrow write command the task writes through, and no project or
// component name anywhere (the template ships
// with the binary and serves any project). polyforge#213 adds the
// estimate line's fixed form, the decidability requirement, the
// per-acceptance test case, the hand-back section and rule ②'s
// risk-only wording.

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
	// ruling 18's rules: recommendation only, never a sub-ticket; judged
	// on the daemon-attached component division and thresholds, the
	// defaults named for the no-attachment case (polyforge#89).
	for _, want := range []string{
		"升级 epic 建议",
		"建议升级",
		"不建议",
		"≥ 3 个",
		"> 8 个",
		"> 30 个文件",
		"批量修改不算",
		"只给建议",
		"不要建子票",
		"各仓库组件划分与生效阈值",
		"每个仓库算一个组件",
		"没有附带这一节时",
		"3 / 8 / 30",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s must carry %q — the epic-upgrade section and ruling 18's default rules (AC7)", rel, want)
		}
	}

	// polyforge#213 (AC1): the analysis's output requirements — the fixed
	// estimate line (its own line, no list marker, no backticks), the
	// decidability requirement, the per-acceptance test case and the
	// hand-back section.
	for _, want := range []string{
		"估算：<仓库> 新增 N 个文件、修改 M 个文件、单元 K 个",
		"每个仓库一行",
		"单独成行",
		"不加列表符号",
		"不加反引号",
		"可判定",
		"边界情况",
		"做不到时怎么办",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s must carry %q — polyforge#213 AC1's estimate line and decidability requirement", rel, want)
		}
	}

	// The test-case record sits in item 2's own line: the name and file,
	// the input, the independently derived expected value and at least one
	// counterexample (polyforge#213 AC1; scoped, so moving the words to
	// another item is red).
	item2 := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "2. ") {
			item2 = strings.Join(strings.Fields(line), " ")
			break
		}
	}
	if item2 == "" {
		t.Fatalf("%s carries no 2. item line — the test-case record belongs there (polyforge#213 AC1)", rel)
	}
	for _, want := range []string{"测试用例", "输入", "独立推导的期望值", "反例"} {
		if !strings.Contains(item2, want) {
			t.Errorf("the 2. item line of %s must carry %q — the per-acceptance test case (polyforge#213 AC1)", rel, want)
		}
	}

	// The hand-back section, scoped from its heading to the next level-2
	// heading: the analysis's own heading and the three situations the
	// ticket names (polyforge#213 AC1).
	handBack := strings.Join(strings.Fields(pf118Section(t, doc, "## 何时必须交回", "## ")), " ")
	for _, want := range []string{
		"### 何时必须交回",
		"分析与代码对不上",
		"测试用例看起来不对",
		"分析没提到的接口",
	} {
		if !strings.Contains(handBack, want) {
			t.Errorf("the hand-back section of %s must carry %q (polyforge#213 AC1)", rel, want)
		}
	}

	// Rule ②'s new semantics, scoped to the epic section: a large single
	// repository marks a risk and recommends a split only when independent
	// parts can run in parallel; the old unconditional-upgrade sentence is
	// gone (polyforge#213 AC1).
	epicIdx := strings.Index(doc, "## 升级 epic 建议")
	if epicIdx < 0 {
		t.Fatalf("%s carries no epic-upgrade section (polyforge#213 AC1)", rel)
	}
	epic := strings.Join(strings.Fields(doc[epicIdx:]), " ")
	for _, want := range []string{"只标风险", "预计至少一轮 review 返工", "可并行的独立部分"} {
		if !strings.Contains(epic, want) {
			t.Errorf("the epic-upgrade section of %s must carry %q (polyforge#213 AC1)", rel, want)
		}
	}
	rule2 := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, "> 30 个文件") {
			rule2 = line
			break
		}
	}
	if rule2 == "" || !strings.Contains(rule2, "只标风险") {
		t.Errorf("%s's rule ② line (the one carrying > 30 个文件) must carry 只标风险: a size-only overage marks a risk, it no longer recommends an upgrade (polyforge#213 AC1)", rel)
	}
	if strings.Contains(doc, "满足任一条即建议升级") {
		t.Errorf("%s still carries 满足任一条即建议升级 — the unconditional upgrade sentence rule ② replaces (polyforge#213 AC1)", rel)
	}

	// No project or component name: the template ships with the binary
	// and serves any project (AC7).
	for _, banned := range []string{"polyforge", "aihub", "pi-web", "webui"} {
		if strings.Contains(strings.ToLower(doc), banned) {
			t.Errorf("%s names the project/component %q — the template text is project-neutral (AC7)", rel, banned)
		}
	}
}
