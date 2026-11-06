package driver

// w460_acceptance_heading_test.go: a ticket's acceptance section headed
// `## 验收` lists its criteria exactly as one headed `## Acceptance`
// (workflow.AcceptanceItems — the reader `pf submit` uses; the driver's
// own acceptance stop was
// deleted with the checks, workflow contract v2 change 19).

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

func TestW460TheChineseAcceptanceHeadingListsTheCriteria(t *testing.T) {
	const body = "- AC1 the first criterion\n- AC2 the second criterion\n"
	for _, heading := range []string{"## 验收", "## 验收（external oracle）", "## Acceptance"} {
		content := "## 背景\n\nwhy\n\n" + heading + "\n\n" + body + "\n## Notes\n\n- not a criterion\n"
		if ids, found := workflow.AcceptanceItems(content, false); !found || strings.Join(ids, ",") != "AC1,AC2" {
			t.Errorf("%q: found %v ids %v, want true AC1,AC2", heading, found, ids)
		}
	}
	for name, content := range map[string]string{
		"a heading with no list item": "## 验收\n\nprose only, no criterion\n",
		"a heading inside a fence":    "## 背景\n\n```\n## 验收\n- AC1 quoted\n```\n",
	} {
		if ids, _ := workflow.AcceptanceItems(content, false); len(ids) != 0 {
			t.Errorf("%s: ids %v, want none", name, ids)
		}
	}
}
