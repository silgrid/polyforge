// carries: internal/polyforge/daemon/analysis_prompt.md

package test

// w81_analysis_tier_template_test.go (polyforge#81 u4, AC4): the
// analysis prompt template carries a "步骤能力档建议" section AFTER the
// epic-upgrade recommendation section — one tier name per step with a
// reason, from the prompt's attached catalog, recommendations only
// (nothing is set automatically), no machine-readable block. The section
// is read from the repository source (the embedded copy IS this file at
// compile time). The expected text is the ticket's, not the
// implementation's.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalysisTierTemplate(t *testing.T) {
	rel := filepath.Join("internal", "polyforge", "daemon", "analysis_prompt.md")
	raw, err := os.ReadFile(filepath.Join(v1Root(t), rel))
	if err != nil {
		t.Fatalf("%s must exist — the analysis prompt template the daemon embeds: %v", rel, err)
	}
	doc := string(raw)

	epicIdx := strings.Index(doc, "## 升级 epic 建议")
	sectionIdx := strings.Index(doc, "## 步骤能力档建议")
	if epicIdx < 0 {
		t.Fatalf("%s carries no epic-upgrade section", rel)
	}
	if sectionIdx < 0 {
		t.Fatalf("%s carries no 步骤能力档建议 section (AC4)", rel)
	}
	if sectionIdx < epicIdx {
		t.Fatalf("the 步骤能力档建议 section must sit AFTER the epic-upgrade section (AC4): epic %d, section %d", epicIdx, sectionIdx)
	}

	section := strings.Join(strings.Fields(doc[sectionIdx:]), " ")
	for _, want := range []string{
		"### 步骤能力档建议",
		"每个 step",
		"建议档名",
		"理由",
		"只给建议",
		"不自动",
		"pf override",
		"不需要机器可读块",
		"无法按档推荐",
		"不要编造",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the 步骤能力档建议 section of %s must carry %q (AC4)", rel, want)
		}
	}

	// the template stays project-neutral.
	for _, banned := range []string{"polyforge", "aihub", "pi-web", "webui"} {
		if strings.Contains(strings.ToLower(doc), banned) {
			t.Errorf("%s names the project/component %q — the template text is project-neutral", rel, banned)
		}
	}
}
