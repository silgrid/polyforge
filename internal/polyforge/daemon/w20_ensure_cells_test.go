package daemon

// carries: internal/polyforge/daemon/ensure.go, internal/polyforge/daemon/w20_ensure_cells_test.go

// w20_ensure_cells_test.go (polyforge#20, AC5): the `pf daemon ensure`
// mapping gate is machine-wide (Q7=b) — a valid named Cell's [workspace]
// mapping admits the launch plan even when cli.toml has none; a machine
// whose every Cell lacks a mapping (an invalid named Cell's mapping does
// not count) is refused naming every checked file, still with the
// [workspace] shape, and never with the retired "the daemon would refuse
// to start" wording.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// w20EnsureConfig writes one config file under dir and answers its path.
func w20EnsureConfig(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestW20Ensure (AC5): the launch plan's mapping gate reads every Cell.
func TestW20Ensure(t *testing.T) {
	t.Setenv("POLYFORGE_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	sock := filepath.Join(dir, "d.sock")
	ws := t.TempDir()
	hostNoMap := "server = \"http://127.0.0.1:1\"\ndaemon_socket = \"" + sock + "\"\n"
	alphaMapped := "server = \"http://127.0.0.1:2\"\napi_key = \"ka\"\n\n[workspace.\"" + ws + "\"]\nprojects = [\"proj\"]\n"
	// a mapping in a named Cell whose connection is incomplete: the Cell
	// is invalid, so its mapping does not admit the plan
	alphaInvalid := "server = \"http://127.0.0.1:2\"\n\n[workspace.\"" + ws + "\"]\nprojects = [\"proj\"]\n"

	t.Run("a valid named Cell's mapping admits the plan", func(t *testing.T) {
		host := w20EnsureConfig(t, dir, "cli.toml", hostNoMap)
		w20EnsureConfig(t, dir, "cli-alpha.toml", alphaMapped)
		p, err := planEnsure(host, "", time.Second)
		if err != nil {
			t.Fatalf("planEnsure: %v", err)
		}
		if p.refuse != nil {
			t.Fatalf("the plan refuses on a machine whose alpha Cell maps: %v", p.refuse)
		}
	})
	t.Run("an invalid named Cell's mapping does not", func(t *testing.T) {
		host := w20EnsureConfig(t, dir, "cli.toml", hostNoMap)
		alpha := w20EnsureConfig(t, dir, "cli-alpha.toml", alphaInvalid)
		p, err := planEnsure(host, "", time.Second)
		if err != nil {
			t.Fatalf("planEnsure: %v", err)
		}
		if p.refuse == nil {
			t.Fatal("the plan admits a machine whose only mapping lives in an invalid Cell")
		}
		if !strings.Contains(p.refuse.Error(), host) || !strings.Contains(p.refuse.Error(), alpha) {
			t.Fatalf("the refusal must name every checked file: %v", p.refuse)
		}
	})
	t.Run("no Cell mapped refuses naming every checked file", func(t *testing.T) {
		host := w20EnsureConfig(t, dir, "cli.toml", hostNoMap)
		alpha := w20EnsureConfig(t, dir, "cli-beta.toml", "server = \"http://127.0.0.1:3\"\napi_key = \"kb\"\n")
		p, err := planEnsure(host, "", time.Second)
		if err != nil {
			t.Fatalf("planEnsure: %v", err)
		}
		if p.refuse == nil {
			t.Fatal("the plan admits a machine no Cell of maps")
		}
		msg := p.refuse.Error()
		for _, want := range []string{"[workspace", host, alpha} {
			if !strings.Contains(msg, want) {
				t.Fatalf("the refusal lacks %q: %s", want, msg)
			}
		}
		if strings.Contains(msg, "would refuse") {
			t.Fatalf("the refusal still carries the retired wording: %s", msg)
		}
	})
}
