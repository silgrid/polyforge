package daemon

// w386_status_command_path_test.go: GET /status reports
// the daemon's effective command PATH — the live [daemon] command_path,
// else the daemon process's own PATH — and the PATH its steps start with
// (step_path: the daemon's pf directory in front of a set command_path,
// else the daemon's own PATH), which `pf doctor` judges.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestW386StatusReportsCommandPath(t *testing.T) {
	td := newTestDaemon(t, nil)
	if st := td.statusSnapshot(false); st.CommandPath != os.Getenv("PATH") || st.StepPath != os.Getenv("PATH") {
		t.Fatalf("unset: the process PATH, got %q / step %q", st.CommandPath, st.StepPath)
	}
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "cli.toml")
	td.cfg.ConfigPath = cfgPath
	toml := "[workspace.\"" + ws + "\"]\nprojects = [\"proj\"]\n\n[daemon]\ncommand_path = \"/opt/w386/bin:/usr/bin:/bin\"\n"
	if err := os.WriteFile(cfgPath, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	resp, err := td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	applied := false
	for _, o := range resp.Outcomes {
		applied = applied || o.Key == "command_path" && o.Status == "applied"
	}
	if !applied {
		t.Fatalf("command_path is live: %+v", resp.Outcomes)
	}
	st := td.statusSnapshot(false)
	if st.CommandPath != "/opt/w386/bin:/usr/bin:/bin" {
		t.Fatalf("after the reload: the configured PATH, got %q", st.CommandPath)
	}
	if want := selfDir() + ":/opt/w386/bin:/usr/bin:/bin"; st.StepPath != want {
		t.Fatalf("after the reload: the step PATH is the daemon's pf directory in front of it, want %q, got %q", want, st.StepPath)
	}
}
