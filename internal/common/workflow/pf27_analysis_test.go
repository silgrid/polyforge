package workflow

// pf27_analysis_test.go (polyforge#27, AC1/AC2): the one presence
// predicate of a work item's pre-submit analysis section — `## 分析`
// or `## Analysis` — that `pf submit`, the auto-pull wave and `pf
// queue` share. A section with content passes (「免分析」 with its
// date, written by the session on the owner's explicit skip, is one
// such content); a missing section, or a heading with no content,
// does not.

import "testing"

func TestPF27AnalysisSectionPresence(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want bool
	}{
		{"no section", "## 背景\n\nan idea\n", false},
		{"a heading with no content", "## 分析\n", false},
		{"a heading with blank content only", "## 分析\n\n   \n\n## 其他\n\nx\n", false},
		{"中文 section with content", "## 背景\n\nan idea\n\n## 分析\n\n- 改动范围：internal/x\n", true},
		{"中文 section, 免分析 with date", "## 分析\n\n免分析（2026-11-14，owner 明确跳过）\n", true},
		{"English section, case-insensitive", "## Analysis\n\nscope: one module\n", true},
		{"English prefix match", "## analysis notes\n\nscope\n", true},
		{"中文 heading with suffix", "## 分析（提交前）\n\n- scope\n", true},
		{"heading ends at the next level-one/two heading", "## 分析\n\n- scope\n\n## 其他\n\nnot part of the analysis section\n", true},
		{"empty section before a later filled one", "## 分析\n\n## 其他\n\n## 分析\n\nreal content\n", false},
		{"filled first section before an empty later one", "## 分析\n\n- real content\n\n## 其他\n\n## 分析\n\n", true},
		{"a level-three heading does not end the section", "### 分析\n\nno: level three is not the section heading\n", false},
		{"acceptance section is not the analysis section", "## Acceptance\n\n- AC1 x\n", false},
		{"no content at all", "", false},
	}
	for _, c := range cases {
		if got := HasAnalysis(c.doc); got != c.want {
			t.Errorf("%s: HasAnalysis = %v, want %v", c.name, got, c.want)
		}
	}
	if IsAnalysisHeading("## 分析") != true || IsAnalysisHeading("## Analysis") != true {
		t.Error("IsAnalysisHeading must match ## 分析 and ## Analysis")
	}
	if IsAnalysisHeading("### 分析") || IsAnalysisHeading("## 验收") {
		t.Error("IsAnalysisHeading must not match a level-three heading or an acceptance heading")
	}
}
