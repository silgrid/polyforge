package driver

// autoanswer_parse_test.go: the strict question-card parser and the reply
// text (autoanswer.go, polyforge#239). The positive shape is the one
// workflows/_shared/batch_questions.md teaches; every ambiguity refuses
// the whole card (the owner's ruling Q2 A: one unambiguous table, no
// lenient repair), and the reply names the REAL elapsed wait, never the
// configured threshold (ruling Q9/analysis 2).

import (
	"strings"
	"testing"
	"time"
)

const autoAnswerCardTwoRows = "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n" +
	"|---|---|---|---|\n" +
	"| 1. 格式 | A：JSON | 可审计 | ★ |\n" +
	"| 2. 重试 | B：两次 | 有界 | ★★ |\n"

func TestAutoAnswerParseCard(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
		ok   bool
	}{
		{"one strict ★/★★ table", autoAnswerCardTwoRows, []string{"A：JSON", "B：两次"}, true},
		{"prose before and after", "请裁定下面两项。\n\n" + autoAnswerCardTwoRows + "\n预期效果：按推荐继续。\n\n注意事项：第 1 行依赖第 2 行。\n", []string{"A：JSON", "B：两次"}, true},
		{"CRLF", strings.ReplaceAll(autoAnswerCardTwoRows, "\n", "\r\n"), []string{"A：JSON", "B：两次"}, true},
		{"surrounding whitespace", "  \n  " + autoAnswerCardTwoRows + "  \n", []string{"A：JSON", "B：两次"}, true},
		{"escaped pipe is a literal pipe", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. 格式 | A\\|B | 可审计 | ★ |\n", []string{"A|B"}, true},
		{"inline code pipe does not split", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. 格式 | `A|B` | 可审计 | ★ |\n", []string{"`A|B`"}, true},
		{"colon separator", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|:---|---|---|---:|\n| 1. 格式 | A | 可审计 | ★ |\n", []string{"A"}, true},

		{"★★★★★ refuses", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. 格式 | A | 可审计 | ★★★ |\n", nil, false},
		{"empty star refuses", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. 格式 | A | 可审计 |  |\n", nil, false},
		{"hollow star refuses", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. 格式 | A | 可审计 | ☆ |\n", nil, false},
		{"digit star refuses", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. 格式 | A | 可审计 | 2 |\n", nil, false},
		{"spaced stars refuse", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. 格式 | A | 可审计 | ★ ★ |\n", nil, false},
		{"comment after stars refuses", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. 格式 | A | 可审计 | ★★ 加说明 |\n", nil, false},
		{"empty recommendation refuses", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. 格式 |  | 可审计 | ★ |\n", nil, false},
		{"empty reason refuses", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. 格式 | A |  | ★ |\n", nil, false},
		{"three columns refuses", "| 问题 | 推荐选项 | 理由 |\n|---|---|---|\n| 1. 格式 | A | 可审计 |\n", nil, false},
		{"five columns refuses", "| 问题 | 推荐选项 | 理由 | 重点关注程度 | 备注 |\n|---|---|---|---|---|\n| 1. 格式 | A | 可审计 | ★ | x |\n", nil, false},
		{"wrong header refuses", "| 问题 | 推荐 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. 格式 | A | 可审计 | ★ |\n", nil, false},
		{"wrong header order refuses", "| 推荐选项 | 问题 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| A | 1. 格式 | 可审计 | ★ |\n", nil, false},
		{"missing separator refuses", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n| 1. 格式 | A | 可审计 | ★ |\n", nil, false},
		{"no data row refuses", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n", nil, false},
		{"truncated row refuses", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. 格式 | A | 可审计 |\n", nil, false},
		{"fenced table refuses", "```\n" + autoAnswerCardTwoRows + "```\n", nil, false},
		{"two tables refuse", autoAnswerCardTwoRows + "\n" + "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 3. 又一张 | C | 别的 | ★ |\n", nil, false},
		{"prose pipe is no table", "选 A | B 都行。\n", nil, false},
		{"plain prose refuses", "lean or compatible?", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseAutoAnswerCard(tc.text)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (recommendations %v)", ok, tc.ok, got)
			}
			if !ok {
				if got != nil {
					t.Fatalf("a refused card returned recommendations %v", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("recommendations = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("row %d = %q, want %q", i+1, got[i], tc.want[i])
				}
			}
		})
	}
}

// The reply says what actually elapsed (31m5s), never the configured
// threshold (30m): an audit line naming 30m after a 31m5s wait would be
// false. The authorization date is the owner ruling's, fixed.
func TestAutoAnswerReplyText(t *testing.T) {
	got := autoAnswerReplyText([]string{"A：JSON", "B：两次"}, 31*time.Minute+5*time.Second)
	want := "自动：按推荐（超时 31m5s，owner 2026-11-23 授权）\n1. A：JSON\n2. B：两次"
	if got != want {
		t.Fatalf("reply text = %q, want %q", got, want)
	}
	got = autoAnswerReplyText([]string{"A"}, 30*time.Minute)
	if !strings.Contains(got, "超时 30m0s") {
		t.Fatalf("reply text = %q, want the real 30m0s wait", got)
	}
}
