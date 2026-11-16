// carries: internal/polyforge/daemon/analysis_write.go, internal/common/workflow/analysis.go

package daemon

// w72_analysis_draft_shape_test.go (polyforge#72, u15 — review r1#2,
// AC3/AC4): a draft line that would END the `## 分析` section (a line
// that, trimmed, opens with "# " or "## " — a shell comment in a code
// block, a level-2 item heading) would split the section, and the next
// whole-section replace would leave the old draft's tail outside it. The
// narrow write path refuses such a draft (DAEMON-400) and the ticket is
// unchanged; a draft using ### headings is written.

import (
	"strings"
	"testing"
)

func TestW72AnalysisWriteRefusesSectionEndingLine(t *testing.T) {
	td, hub, runner := w72World(t, nil)
	id := w72AnalysisTask(t, td, hub, runner, "wi_w72_shape")
	hub.mu.Lock()
	before := *hub.contents["wi_w72_shape"]
	hub.mu.Unlock()
	for _, draft := range []string{
		"### 2. 证明命令\n\n```\n# 在仓库根运行\nmake test\n```\n",
		"## 1. 改动范围\n\n- x\n",
	} {
		code, body := w72ProbeWrite(t, td, id, draft)
		if code != 400 || !strings.Contains(body, "DAEMON-400") {
			t.Errorf("a draft with a section-ending line must be refused DAEMON-400, got %d %s\n%s", code, body, draft)
		}
		hub.mu.Lock()
		after := *hub.contents["wi_w72_shape"]
		hub.mu.Unlock()
		if after != before {
			t.Fatalf("a refused draft must leave the ticket unchanged:\n%s", after)
		}
	}
	if code, body := w72ProbeWrite(t, td, id, "### 2. 证明命令\n\n```\nmake test   # 在仓库根运行\n```\n"); code != 200 {
		t.Fatalf("a draft with no section-ending line is written, got %d %s", code, body)
	}
}
