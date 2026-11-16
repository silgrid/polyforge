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
	"regexp"
	"strings"
	"testing"
)

// TestPF75DeployHandoffCopiesTarget: the deploy step's own text makes the
// hand-off carry the deploy log's target as the log states it.
func TestPF75DeployHandoffCopiesTarget(t *testing.T) {
	dep := w68Flat(t, "workflows/release/steps/deploy.md")
	for _, want := range []string{
		"every `target:` line and its last line copied verbatim", // polyforge#76: one per deploy target
		"never a description from memory or inference",
	} {
		if !strings.Contains(dep, want) {
			t.Errorf("deploy.md must state %q: the hand-off copies the deploy log's target verbatim, never an inference", want)
		}
	}
}

// TestPF75CellDeployDocs: the shared texts state the deploy-configuration
// rules the incident fixed — per file, the facts its reader needs — and
// none of them names a concrete project, host or Cell (no IP literal; the
// placeholders are `<profile>` and the like).
func TestPF75CellDeployDocs(t *testing.T) {
	for file, wants := range map[string][]string{
		"workflows/release/README.md": {
			"carries its Cell's facts (`PF_CELL`, `PF_CELL_SERVER`, `PF_DEPLOY_ENV`",
			"`deploy.env` for the default Cell, `deploy-<profile>.env` for a named one",
			"refusing before any work when it does not exist",
			"skips the target check and says so",
		},
		"docs/reference/release.md": {
			"The deploy configuration follows the work item's Cell",
			"a Cell never falls back to another Cell's configuration",
			"checks, before any network work, that the merged `AIHUB_HOST` names that Cell's server",
			"A run without the Cell facts (a human operator) skips the target check and says so",
		},
		"docs/reference/config.md": {
			"named `deploy.env` for the default Cell and `deploy-<profile>.env` for a named one",
			"`PF_CELL` is the Cell's name",
			"`PF_DEPLOY_ENV` is the deploy configuration file that Cell's deploys read",
		},
		"docs/reference/workflow-contract.md": {
			"| `PF_CELL` | the Cell the work item belongs to: `default`, else the named Cell's profile |",
			"| `PF_CELL_SERVER` | that Cell's server as configured |",
			"| `PF_DEPLOY_ENV` | the deploy configuration file that Cell's deploys read |",
			"with the work item's Cell facts: `PF_CELL`, `PF_CELL_SERVER` and `PF_DEPLOY_ENV`",
		},
	} {
		text := w68Flat(t, file)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s must state %q", file, want)
			}
		}
		// no concrete host, Cell or place: an address names one deployment
		if ip := regexp.MustCompile(`\b[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+\b`).FindString(text); ip != "" {
			t.Errorf("%s names a concrete host %q: the shared rules carry placeholders, never a place", file, ip)
		}
	}
}
