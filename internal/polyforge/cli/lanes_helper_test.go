package cli

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// writeLane writes the lane name under root from a workflow.yaml fixture
// (its `name:` line, if any, replaced by the contract header: contract,
// name, description), filling in the definitions the lane needs; the
// loader's verdict on what remains is the test's to judge.
func writeLane(t *testing.T, root, name, front string) string {
	t.Helper()
	var keep []string
	for _, l := range strings.SplitAfter(front, "\n") {
		if !strings.HasPrefix(l, "name: ") {
			keep = append(keep, l)
		}
	}
	return workflowtest.WriteLaneUnchecked(t, root, name, strings.Join(keep, ""))
}
