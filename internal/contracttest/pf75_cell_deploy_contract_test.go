package test

// pf75_cell_deploy_contract_test.go (polyforge#75): a deploy that reached
// the wrong target once read exactly like a success — the hand-off
// described the log's target from inference and named another host than
// the one the deploy had reached. The release lane's deploy step now
// requires the hand-off to copy the deploy log's `target:` line and its
// last line verbatim, and the shared texts state the deploy-configuration
// rules the incident fixed: the configuration file follows the work
// item's Cell (deploy.env for the default Cell, deploy-<profile>.env for a
// named one), the three Cell variables every step process carries, the
// refusal when the stated file is missing, the target check against the
// Cell's server, and the skip a manual run prints — naming no project,
// host or Cell.

import (
	"strings"
	"testing"
)

// TestPF75DeployHandoffCopiesTarget: the deploy step's own text makes the
// hand-off carry the deploy log's target as the log states it.
func TestPF75DeployHandoffCopiesTarget(t *testing.T) {
	dep := w68Flat(t, "workflows/release/steps/deploy.md")
	for _, want := range []string{
		"its `target:` line and its last line copied verbatim",
		"never a description from memory or inference",
	} {
		if !strings.Contains(dep, want) {
			t.Errorf("deploy.md must state %q: the hand-off copies the deploy log's target verbatim, never an inference", want)
		}
	}
}
