package test

// Regression test for wi_WLczIJMOLDhq item B4 (2026-10-13 audit): the
// installer's daemon launch hard-codes `-auto=false` and
// `-max-concurrent-jobs 4`, so every install/upgrade WIPES the
// operator's persisted auto-pull and concurrency settings (the daemon
// state file's auto/max_concurrent_jobs) by forcing them on argv.
//
// Expected behavior per the work item: install.sh starts the daemon
// WITHOUT either flag — the daemon itself applies its persisted
// settings (and its own first-boot defaults). The same expectation the
// fix's acceptance names: "install.sh 不再包含两个参数".
//
// Regression test: before the fix install.sh passed both flags.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestB4InstallShDoesNotForceAutoOrConcurrency(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "tools", "install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	for i, ln := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "-auto=") {
			t.Fatalf("REPORTED FAILURE (B4): tools/install.sh line %d still forces the auto switch on the daemon argv (%q) — "+
				"an upgrade wipes the operator's persisted auto setting; the installer must start the daemon without it", i+1, trimmed)
		}
		if strings.HasPrefix(trimmed, "-max-concurrent-jobs") {
			t.Fatalf("REPORTED FAILURE (B4): tools/install.sh line %d still forces the concurrency cap on the daemon argv (%q) — "+
				"an upgrade wipes the operator's persisted max-concurrent-jobs; the installer must start the daemon without it", i+1, trimmed)
		}
	}
}
