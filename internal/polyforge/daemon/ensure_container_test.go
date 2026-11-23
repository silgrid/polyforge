package daemon

// ensure_container_test.go (polyforge#160, AC1): `pf daemon ensure` under
// the container supervisor is a successful no-op that starts nothing —
// the branch short-circuits before the workflows-dir refusal the host
// path raises — while the machine-wide mapping gate still runs (the
// no-op skips STARTING, never CHECKING).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/supervisor"
)

// ensureContainerFixture answers a cli.toml whose mapping admits the
// plan and a container marker file the probe is pointed at.
func ensureContainerFixture(t *testing.T, mapping bool) (cfg string) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "d.sock")
	body := "server = \"http://127.0.0.1:1\"\ndaemon_socket = \"" + sock + "\"\n"
	if mapping {
		body += "[workspace.\"" + filepath.Join(dir, "ws") + "\"]\nprojects = [\"e2e\"]\n"
	}
	cfg = filepath.Join(dir, "cli.toml")
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, ".containerenv")
	if err := os.WriteFile(marker, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	old := supervisor.ContainerMarkerFiles
	supervisor.ContainerMarkerFiles = []string{marker}
	t.Cleanup(func() { supervisor.ContainerMarkerFiles = old })
	return cfg
}

// TestEnsureContainerNoop: under the container supervisor (the marker
// variable AND the container marker file) ensure answers 0 and starts
// nothing — a missing workflows dir, a hard refusal on the host path,
// proves the start was skipped — and the mapping gate still refuses a
// machine with no mapping in any Cell.
func TestEnsureContainerNoop(t *testing.T) {
	cfg := ensureContainerFixture(t, true)
	t.Setenv(supervisor.MarkerEnv, supervisor.MarkerContainerValue)
	args := []string{"ensure", "--config", cfg, "--timeout", "1s", "--workflows-dir", filepath.Join(filepath.Dir(cfg), "absent")}

	var out, errb bytes.Buffer
	if code := Main(args, nil, &out, &errb); code != 0 {
		t.Fatalf("container no-op: exit %d stderr %q, want 0", code, errb.String())
	}
	if !strings.Contains(out.String(), "container supervisor") {
		t.Fatalf("container no-op: stdout %q must say the container supervisor keeps the daemon", out.String())
	}
	if strings.Contains(out.String(), supervisor.SessionName) || strings.Contains(errb.String(), supervisor.SessionName) {
		t.Fatalf("container no-op must not mention the tmux loop: out %q err %q", out.String(), errb.String())
	}

	// the mapping gate still runs: a machine whose no Cell has a mapping
	// is refused even under the container supervisor
	noMap := ensureContainerFixture(t, false)
	out.Reset()
	errb.Reset()
	if code := Main([]string{"ensure", "--config", noMap, "--timeout", "1s"}, nil, &out, &errb); code != 1 || !strings.Contains(errb.String(), "[workspace") {
		t.Fatalf("no mapping under the container supervisor: exit %d stderr %q, want 1 naming the mapping", code, errb.String())
	}

	// the same first config on the HOST path still refuses the missing
	// workflows dir — the container branch is what short-circuited it
	t.Setenv(supervisor.MarkerEnv, "")
	out.Reset()
	errb.Reset()
	if code := Main(args, nil, &out, &errb); code != 1 || !strings.Contains(errb.String(), "workflows dir") {
		t.Fatalf("host path contrast: exit %d stderr %q, want 1 naming the workflows dir", code, errb.String())
	}
}
