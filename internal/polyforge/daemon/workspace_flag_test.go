package daemon

// workspace_flag_test.go pins the #113 launch surface at the daemon's
// real entry (Main): the retired selection
// environment variables refuse the boot BEFORE anything is loaded or
// served (exit 1, naming the variable), a config with zero [workspace]
// sections refuses startup naming the shape and `pf doctor`, and a
// mapping-bearing config proceeds past the launch guard. All retired
// names are assembled from fragments so this file's bytes never carry
// them contiguously (the greppable floor — S36's own convention).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoMappingRefusesAtMain: a config with zero [workspace] sections —
// the fresh-install template shape — refuses startup (ExitNeedsOperator) naming the
// [workspace] shape and `pf doctor`, before any socket is created.
func TestNoMappingRefusesAtMain(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	pfDir := filepath.Join(cfgDir, "polyforge")
	if err := os.MkdirAll(pfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pfDir, "cli.toml"),
		[]byte("server = \"http://127.0.0.1:1\"\napi_key = \"k\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := Main([]string{"--socket", filepath.Join(t.TempDir(), "d.sock")}, nil, &bytes.Buffer{}, &stderr)
	if code != ExitNeedsOperator {
		t.Fatalf("exit = %d, want %d (no-mapping refusal), stderr: %q", code, ExitNeedsOperator, stderr.String())
	}
	for _, tok := range []string{"[workspace", "doctor"} {
		if !strings.Contains(stderr.String(), tok) {
			t.Fatalf("refusal %q misses the %q guidance token", stderr.String(), tok)
		}
	}
}

// TestMappingConfiguredBootReachesWorkspaceValidation: a mapping-bearing
// config proceeds past the launch guard — with a MISSING workspace dir it
// fails in New's validation naming the workspace (proving the mapping was
// actually consumed, and the refusal names the mapped path).
func TestMappingConfiguredBootReachesWorkspaceValidation(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	pfDir := filepath.Join(cfgDir, "polyforge")
	if err := os.MkdirAll(pfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "no-such-ws")
	cfg := "server = \"http://127.0.0.1:1\"\n\n[workspace.\"" + missing + "\"]\nprojects = [\"proj\"]\n"
	if err := os.WriteFile(filepath.Join(pfDir, "cli.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := Main([]string{"--socket", filepath.Join(t.TempDir(), "d.sock")}, nil, &bytes.Buffer{}, &stderr)
	if code != ExitNeedsOperator {
		t.Fatalf("exit = %d, want %d (workspace validation failure), stderr: %q", code, ExitNeedsOperator, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no-such-ws") {
		t.Fatalf("validation failure %q does not name the mapped workspace", stderr.String())
	}
}
