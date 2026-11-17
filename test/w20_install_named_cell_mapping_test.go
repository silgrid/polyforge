package test

// carries: tools/install.sh, test/w20_install_named_cell_mapping_test.go

// w20_install_named_cell_mapping_test.go (polyforge#20, AC6): the
// installer's steps 3 and 4 look for a [workspace] mapping in every
// Cell's file (Q8=a) — a machine whose only mapping lives in a named
// Cell (cli-alpha.toml) gets `pf daemon ensure` run by step 4 and no
// "the daemon refuses to start" wording; a fresh install (no mapping
// anywhere) behaves as before (pinned by TestW113InstallFreshTemplate).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestW20InstallNamedCellMapping (AC6): with cli.toml carrying no
// mapping and cli-alpha.toml carrying one, tools/install.sh runs
// `pf daemon ensure` (the daemon really starts) and never says the
// daemon refuses to start.
func TestW20InstallNamedCellMapping(t *testing.T) {
	ic := w113InstallPrep(t)
	t.Cleanup(func() { w113KillArgv(ic.home) })

	ws := filepath.Join(ic.home, "w20ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(ic.home, ".config", "polyforge", "cli.toml")
	// cli.toml: the connection, NO mapping (the default Cell)
	w113ConfigFile(t, cfgPath, "http://127.0.0.1:8080", "w113key",
		filepath.Join(ic.home, ".local", "share", "polyforge", "daemon.sock"), nil)
	// cli-alpha.toml: the named Cell carrying the machine's only mapping
	// (its own server: the same one as the default Cell's would conflict
	// and mark the Cell invalid)
	alpha := filepath.Join(ic.home, ".config", "polyforge", "cli-alpha.toml")
	w113ConfigFile(t, alpha, "http://127.0.0.1:8081", "w113key", "",
		[]w113WS{{key: ws, projects: []string{"w20proj"}}})

	out, code := w113RunInstall(t, ic)
	if code != 0 {
		t.Fatalf("install.sh (named-Cell mapping only): exit %d (output %q)", code, out)
	}
	if strings.Contains(out, "refuses to start") {
		t.Fatalf("the installer still says the daemon refuses to start (output %q)", out)
	}
	// step 3 saw the mapping in the named Cell's file
	if !strings.Contains(out, "workspace mapping present") {
		t.Fatalf("step 3 does not see the named Cell's mapping (output %q)", out)
	}
	// step 4 did not SKIP: `pf daemon ensure` ran and the daemon answers
	for _, gone := range []string{"SKIP", "has no [workspace] section yet"} {
		if strings.Contains(out, gone) {
			t.Fatalf("step 4 still skips the named-Cell-mapped machine (output %q)", out)
		}
	}
	argv := w113FindDaemonArgv(t, ic.home, 15*time.Second)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, cfgPath) {
		t.Fatalf("the daemon did not start through pf daemon ensure (argv %v)", argv)
	}
}
