package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// TestW85bCatalogReloadIsLive (polyforge#143, 85b): the [step_tiers]
// model-candidate catalog is a Host-only live family — a reload that
// adds it reports one [step_tiers] outcome applied/live (the
// classification is the hot-reload proof, Q5 B: the catalog has no
// daemon consumer, doctor and `pf config list` read the file), removing
// the table reports it applied again (back to the empty catalog), and a
// named Cell's reload never reports the Host's family.
func TestW85bCatalogReloadIsLive(t *testing.T) {
	td := newTestDaemon(t, nil)
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
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
	statusOf := func(resp *cli.ConfigReloadResponse, keys map[string]bool) map[string]string {
		t.Helper()
		status := map[string]string{}
		for _, o := range resp.Outcomes {
			status[o.Key] = o.Status + "/" + o.Class
			keys[o.Key] = true
		}
		return status
	}
	seen := map[string]bool{}
	write("\n[step_tiers]\ndefault = [\"pi:prov/main\"]\n")
	resp, err := td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if status := statusOf(resp, seen); status["[step_tiers]"] != "applied/live" {
		t.Fatalf("outcomes = %v, want one [step_tiers] outcome applied/live", status)
	}
	write("")
	resp, err = td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if status := statusOf(resp, seen); status["[step_tiers]"] != "applied/live" {
		t.Fatalf("outcomes after removing the table = %v, want [step_tiers] applied/live again (the empty catalog is a change too)", status)
	}
	// a named Cell's reload never reports the Host's family
	write("\n[step_tiers]\ndefault = [\"pi:prov/main\"]\n")
	td.cfg.Profile = "alpha"
	resp, err = td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range resp.Outcomes {
		if o.Key == "[step_tiers]" {
			t.Fatalf("a named Cell's reload must not report the Host-only catalog: %+v", o)
		}
	}
}
