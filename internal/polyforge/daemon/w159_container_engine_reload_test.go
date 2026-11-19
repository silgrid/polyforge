package daemon

// w159_container_engine_reload_test.go (polyforge#159): container_engine
// is restart-only — a reload of a changed value reports it
// pending-restart, the running daemon keeps its resolved engine, and a
// value set back to the startup baseline clears itself from pending.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContainerEngineReloadIsPendingRestart(t *testing.T) {
	td := newTestDaemon(t, nil)
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cli.toml")
	td.cfg.ConfigPath = path
	base := "[workspace.\"" + ws + "\"]\nprojects = [\"proj\"]\n"
	write := func(v string) {
		t.Helper()
		extra := ""
		if v != "" {
			extra = "[daemon]\ncontainer_engine = \"" + v + "\"\n"
		}
		if err := os.WriteFile(path, []byte(base+extra), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("podman")
	resp, err := td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, o := range resp.Outcomes {
		status[o.Key] = o.Status + "/" + o.Class
	}
	if status["container_engine"] != "pending-restart/restart-only" {
		t.Fatalf("container_engine outcome = %q, want pending-restart/restart-only (all: %v)", status["container_engine"], status)
	}
	// set back to the startup baseline: the pending report clears
	write("")
	if _, err := td.reloadConfig(); err != nil {
		t.Fatal(err)
	}
	resp, err = td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range resp.Outcomes {
		if o.Key == "container_engine" {
			t.Fatalf("an unchanged container_engine must not be reported: %+v", o)
		}
	}
}
