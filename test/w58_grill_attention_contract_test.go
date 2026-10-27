package test

// w58_grill_attention_contract_test.go: text contract for workbench#58.
// The grill question batch is a four-column table whose last column is the
// owner-attention grade rendered as stars (★ routine confirmation up to
// ★★★★★ directional ruling), followed by 预期效果 then 注意事项. Prompt
// material only: nothing mechanically consumes the stars. Every surface that
// teaches the table is pinned so a one-file drift fails.
//
// Red on the pre-change tree: the base text has the three-column header and
// no star glyph at all, so every assertion below fails there.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW58GrillAttentionContract(t *testing.T) {
	const (
		header    = "| 问题 | 推荐选项 | 理由 | 重点关注程度 |"
		starScale = "★~★★★★★"
		topStar   = "★★★★★"
	)
	templates := []string{
		"workflows/feature-full/steps/grill.md",
		"workflows/feature-full/steps/grill-review.md",
		"workflows/autopilot/steps/grill.md",
	}
	surfaces := append(append([]string{}, templates...),
		"skills/output-format/SKILL.md", "docs/user-guide.md")
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(v1Root(t), rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// Every surface that teaches the table carries the four-column header,
	// the star grade, and both closing sections after the table.
	for _, rel := range surfaces {
		s := read(rel)
		for _, want := range []string{header, "重点关注程度", "预期效果", "注意事项", starScale, topStar} {
			if !strings.Contains(s, want) {
				t.Errorf("%s lacks %q", rel, want)
			}
		}
		h, e, n := strings.Index(s, header), strings.Index(s, "预期效果"), strings.Index(s, "注意事项")
		if h >= 0 && e >= 0 && n >= 0 && !(h < e && e < n) {
			t.Errorf("%s: order must be header, 预期效果, 注意事项", rel)
		}
	}

	// The three step templates carry the full legend verbatim: the star
	// endpoints, the midpoint band, every row marked, and the four-column
	// shape (no fifth column).
	for _, rel := range templates {
		s := read(rel)
		for _, want := range []string{
			"每行必标", "★ = 例行确认", "★★★★★ = 方向性裁定", "与既有裁定冲突",
			"★★–★★★★", "exactly four columns", "no fifth column",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("%s lacks %q", rel, want)
			}
		}
	}

	// The output-format skill names the shape and carries the same legend.
	skill := read("skills/output-format/SKILL.md")
	for _, want := range []string{"four-column", "每行必标", "★ = 例行确认"} {
		if !strings.Contains(skill, want) {
			t.Errorf("skills/output-format/SKILL.md lacks %q", want)
		}
	}

	// No surface still teaches the retired three-column shape.
	for _, rel := range append(append([]string{}, templates...), "skills/output-format/SKILL.md") {
		s := read(rel)
		for _, bad := range []string{"three columns", "no fourth column", "three-column"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s still says %q", rel, bad)
			}
		}
	}
}
