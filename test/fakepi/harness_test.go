package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pathOf answers the last PATH value of an environment.
func pathOf(env []string) string {
	p := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			p = v
		}
	}
	return p
}

// Every scripted command sees the harness's bin dir first on PATH, so a
// bare `pf` is the binary under test, never the machine's installed CLI.
func TestHarnessEnvPutsTheBinDirFirst(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got := pathOf(harnessEnv()); !strings.HasPrefix(got, filepath.Dir(exe)+string(os.PathListSeparator)) {
		t.Fatalf("SYMPTOM: a scripted command's PATH does not start with the harness's bin dir: %s", got)
	}
}
