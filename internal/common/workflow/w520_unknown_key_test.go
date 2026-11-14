package workflow_test

// w520_unknown_key_test.go: a key this pf does not know —
// on the lane or on a step — is WFC-111 naming the key and asking to
// upgrade pf (a later release may define it: a compatible change adds keys
// without bumping the contract version); a key the contract removed keeps
// its rewrite.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

func TestW520UnknownKeyAsksToUpgradePf(t *testing.T) {
	for _, front := range []string{
		"skipme: true\nsteps:\n  - id: work\n",
		"steps:\n  - id: work\n    skipme: true\n",
	} {
		_, errs := workflow.Load(workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", front))
		if len(errs) != 1 || errs[0].Code != workflow.CodeUnknownKey {
			t.Errorf("%q: defects %v, want exactly one %s", front, errs, workflow.CodeUnknownKey)
			continue
		}
		for _, want := range []string{"skipme", "upgrade pf"} {
			if !strings.Contains(errs[0].Msg, want) {
				t.Errorf("%q: the WFC-111 message does not say %q: %s", front, want, errs[0].Msg)
			}
		}
	}

	front := "steps:\n  - id: work\n    inputs: []\n"
	_, errs := workflow.Load(workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", front))
	if len(errs) != 1 || errs[0].Code != workflow.CodeUnknownKey || !strings.HasPrefix(errs[0].Msg, "removed before workflow contract 1") {
		t.Errorf("a removed key: defects %v, want one %s with its rewrite", errs, workflow.CodeUnknownKey)
	}
}
