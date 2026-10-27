package daemon

// askpass_test.go: the STARTUP WIRING of the crash-residue sweep.
// gitops.SweepAskpass's own behavior has a direct unit test in the
// gitops package; this covers daemon.go's Run(), whose first statement
// must call it on <DataDir>/git — so residue askpass-* scripts left by
// a killed previous daemon are gone once this boot serves, while an
// unrelated entry in the same directory survives.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRunSweepsAskpassResidueAtStartup: pre-seed <DataDir>/git/askpass
// with crash-residue one-shot scripts plus one unrelated file, boot the
// daemon, and assert the sweep ran: residue swept, unrelated file kept.
func TestRunSweepsAskpassResidueAtStartup(t *testing.T) {
	residueNames := []string{
		"askpass-wi_dead-0123456789abcdef",
		"askpass-wi_dead-fedcba9876543210",
	}
	// newTestDaemon builds Config (DataDir chosen) then hands it to
	// mutate BEFORE Run starts — the hook is the one place we can
	// still seed the directory the first statement of Run will sweep.
	var askpassDir string
	newTestDaemon(t, func(cfg *Config) {
		askpassDir = filepath.Join(cfg.DataDir, "git", "askpass")
		if err := os.MkdirAll(askpassDir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range residueNames {
			if err := os.WriteFile(filepath.Join(askpassDir, name), []byte("#!/bin/sh\nplaceholder-push-secret\n"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(askpassDir, "keep-me.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	// newTestDaemon returns only once the socket is live; the sweep is
	// sequenced before listenUDS, so it has certainly run by now.
	for _, name := range residueNames {
		if _, err := os.Stat(filepath.Join(askpassDir, name)); !os.IsNotExist(err) {
			t.Fatalf("startup sweep must remove crash residue %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(askpassDir, "keep-me.txt")); err != nil {
		t.Fatalf("unrelated entries must survive the startup sweep: %v", err)
	}
}
