package test

// pf76_release_deploy_targets_contract_test.go (polyforge#76): one release
// work item deploys every deploy target of the machine - one per Cell
// whose deploy configuration file exists - one after another. The release
// lane's deploy step copies every target's `target:` line and the run's
// summary last line verbatim, names a run that deployed some targets but
// not all `partial` (still a FAIL), and runs under a 60-minute window; the
// shared texts state the multi-target rules and the all-Cells variable a
// step process carries - naming no project, host or Cell.

import (
	"regexp"
	"strings"
	"testing"
)

// TestPF76ReleaseDeployTargetsDocs: each text states the facts its reader
// needs, and none names a concrete host.
func TestPF76ReleaseDeployTargetsDocs(t *testing.T) {
	for file, wants := range map[string][]string{
		"workflows/release/steps/deploy.md": {
			"this step's tool window is 60 minutes",
			"every `target:` line and its last line copied verbatim",
			"production state `partial`",
			"still a `fail`",
		},
		"workflows/release/README.md": {
			"tool 60 min",
			"`deploy` declares a 60-minute window",
			"one deploy target per Cell whose deploy configuration file exists",
			"`PF_CELL_TARGETS`",
			"`partial`",
		},
		"docs/reference/release.md": {
			"`deploy` likewise holds the heavy admission under a 60-minute window",
			"every Cell whose deploy configuration file exists is one deploy target",
			"must point at its own Cell's server",
			"the work item's Cell must be one of the targets",
			"the work item's Cell first, the rest by name",
			"`not_attempted`",
			"`already_deployed`",
			"the same `BACKUP_ROOT`",
			"a foreign version line",
		},
		"docs/reference/workflow-contract.md": {
			"| `PF_CELL_TARGETS` | every serving Cell of this machine, one line each: its profile, its server and its deploy configuration file, tab-separated |",
			"and every serving Cell's facts: `PF_CELL_TARGETS`",
		},
	} {
		text := w68Flat(t, file)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s must state %q", file, want)
			}
		}
		if ip := regexp.MustCompile(`\b[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+\b`).FindString(text); ip != "" {
			t.Errorf("%s names a concrete host %q: the shared rules carry placeholders, never a place", file, ip)
		}
	}
}
