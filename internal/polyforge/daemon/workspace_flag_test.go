package daemon

// workspace_flag_test.go pins the #113 launch surface at the daemon's
// real entry (Main): the retired selection
// environment variables refuse the boot BEFORE anything is loaded or
// served (exit 1, naming the variable), a config with zero [workspace]
// sections loads with its execution plane closed, and a mapping-bearing
// config proceeds past the launch guard. All retired
// names are assembled from fragments so this file's bytes never carry
// them contiguously (the greppable floor — S36's own convention).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// TestNoMappingLoadsWithExecutionDisabled: a config with zero [workspace]
// sections — the fresh-install template shape — loads (a Cell with a
// complete connection and no workspace serves its data plane), and its
// execution plane is closed with the reason on GET /status; a submit is
// refused naming it. The Host then shuts down cleanly.
func TestNoMappingLoadsWithExecutionDisabled(t *testing.T) {
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
	sock := filepath.Join(t.TempDir(), "d.sock")
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Main([]string{"--socket", sock, "--data-dir", t.TempDir(), "--heartbeat-every", "0"}, nil, &bytes.Buffer{}, &stderr)
	}()
	client := cli.NewDaemonClient(sock)
	deadline := time.Now().Add(30 * time.Second)
	var raw []byte
	for {
		r, err := client.Status()
		if err == nil {
			raw = r
			break
		}
		select {
		case code := <-done:
			t.Fatalf("the Host exited %d instead of serving: %s", code, stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Host never answered GET /status: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(string(raw), "no workspace configured") {
		t.Fatalf("GET /status must name the closed execution plane: %s", raw)
	}
	if _, err := client.ClaimWithModels("wi_x", "normal", nil); err == nil || !strings.Contains(err.Error(), "no workspace configured") {
		t.Fatalf("a submit must be refused naming the reason: %v", err)
	}
	if _, err := client.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d after shutdown: %s", code, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the Host did not exit after POST /shutdown")
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
