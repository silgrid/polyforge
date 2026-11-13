package driver

// w374_d2_build_settle_test.go ( D2, item 9): the acceptance
// reader. The test-path rules this file also pinned moved with the facts
// computations to the workflow's facts.py and are pinned
// there (workflows/_shared/scripts/tests/test_facts.py).

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

func TestAcceptanceItemsParsing(t *testing.T) {
	content := "# Goal\n\ntext\n\n## Acceptance\n\n- [ ] AC3: the first\n  - a nested note, not a criterion\n- [x] second by position\n1. **AC7** bold token\n```\n- fenced, not one\n```\n\n## Notes\n\n- not a criterion\n"
	ids, found := workflow.AcceptanceItems(content, false)
	if !found || strings.Join(ids, ",") != "AC3,AC2,AC7" {
		t.Fatalf("ids %v found %v, want AC3,AC2,AC7 (token, else position)", ids, found)
	}
	if _, found := workflow.AcceptanceItems("# Goal\n\n- a list outside any acceptance section\n", false); found {
		t.Fatal("no `## Acceptance` heading means no section")
	}
	if ids, _ := workflow.AcceptanceItems("approved, with:\n- AC1 one\n- two\n", true); strings.Join(ids, ",") != "AC1,AC2" {
		t.Fatalf("an answer's list items: %v", ids)
	}
	if ids, _ := workflow.AcceptanceItems("approve", true); len(ids) != 0 {
		t.Fatalf("an approval carries no list: %v", ids)
	}
}
