package daemon

// step_tiers_test.go (polyforge#81 u1, AC1): the Host [step_tiers]
// catalog reaches the running daemon through liveconfig — the startup
// value is injected, a Host reload swaps it without a restart, removing
// the table empties it, and a named Cell's reload never touches it. The
// consumer read is the runtime's catalog accessor, the same one the
// dispatch-time expansion and the override route use (not the reload's
// "applied" label alone).

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

func TestStepTiersHostReload(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.StartupFile = cli.Config{StepTiers: map[string][]string{"main": {"pi:a", "claude:b"}}}
	})
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	// AC1's startup half: the daemon reads the Host catalog it started with.
	if got := td.rt.StepTiersCatalog(); !reflect.DeepEqual(got, map[string][]string{"main": {"pi:a", "claude:b"}}) {
		t.Fatalf("the startup catalog = %v, want main=[pi:a claude:b]", got)
	}
	path := filepath.Join(t.TempDir(), "cli.toml")
	td.cfg.ConfigPath = path
	base := "[workspace.\"" + ws + "\"]\nprojects = [\"proj\"]\n"
	write := func(extra string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(base+extra), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// AC1's reload half: the same daemon reads the new catalog at once.
	write("\n[step_tiers]\nmain = [\"pi:c\"]\n")
	if _, err := td.reloadConfig(); err != nil {
		t.Fatal(err)
	}
	if got := td.rt.StepTiersCatalog(); !reflect.DeepEqual(got, map[string][]string{"main": {"pi:c"}}) {
		t.Fatalf("after the reload the catalog = %v, want only main=[pi:c] (no restart)", got)
	}

	// removing the whole table is an empty catalog, again live.
	write("")
	if _, err := td.reloadConfig(); err != nil {
		t.Fatal(err)
	}
	if got := td.rt.StepTiersCatalog(); got != nil {
		t.Fatalf("after removing the table the catalog = %v, want none", got)
	}

	// AC1: a named Cell's reload never changes the Host's catalog.
	write("\n[step_tiers]\nmain = [\"pi:cell\"]\n")
	td.cfg.Profile = "alpha"
	if _, err := td.reloadConfig(); err != nil {
		t.Fatal(err)
	}
	if got := td.rt.StepTiersCatalog(); got != nil {
		t.Fatalf("a named Cell's reload changed the Host catalog to %v, want none", got)
	}
}
