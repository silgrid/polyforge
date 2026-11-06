package daemon

// w350_cell_limit_test.go ( D3): a reloaded
// cell_max_concurrent_jobs governs the Cell's admission ceiling at once —
// raised, lowered, removed (back to the Host ceiling), clamped to the Host
// ceiling — and GET /profiles reports the applied value and the effective
// max_concurrent.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestW350ReloadedCellLimitGovernsTheCeiling(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.HostSlots = &hostSlots{max: 8, cells: func() []*Daemon { return nil }}
	})
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cli.toml")
	td.cfg.ConfigPath = path
	reload := func(extra string) map[string]string {
		t.Helper()
		if err := os.WriteFile(path, []byte("[workspace.\""+ws+"\"]\nprojects = [\"proj\"]\n"+extra), 0o600); err != nil {
			t.Fatal(err)
		}
		resp, err := td.reloadConfig()
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, o := range resp.Outcomes {
			out[o.Key] = o.Status
		}
		return out
	}
	for _, tc := range []struct {
		extra     string
		persisted int
		effective int
	}{
		{"[daemon]\ncell_max_concurrent_jobs = 7\n", 7, 7},
		{"[daemon]\ncell_max_concurrent_jobs = 2\n", 2, 2},
		{"[daemon]\ncell_max_concurrent_jobs = 12\n", 12, 8}, // clamped to the Host ceiling
		{"", 0, 8}, // removed: the Host ceiling
	} {
		got := reload(tc.extra)
		if got["cell_max_concurrent_jobs"] != "applied" {
			t.Fatalf("%q: the key was not applied: %v", tc.extra, got)
		}
		if n := td.q.MaxConcurrent(); n != tc.effective {
			t.Fatalf("%q: the Cell's admission ceiling = %d, want %d", tc.extra, n, tc.effective)
		}
		info := td.profileInfo("default", path)
		if info.CellMaxConcurrentJobs != tc.persisted || info.MaxConcurrent != tc.effective {
			t.Fatalf("%q: /profiles row = cell %d effective %d", tc.extra, info.CellMaxConcurrentJobs, info.MaxConcurrent)
		}
	}
	if got := reload(""); got["cell_max_concurrent_jobs"] != "" {
		t.Fatalf("an unchanged value must not be re-applied: %v", got)
	}
}

func TestW350LoweredCellLimitKillsNothing(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_a")
	td.submitClaim(t, "wi_b")
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cli.toml")
	td.cfg.ConfigPath = path
	if err := os.WriteFile(path, []byte("[workspace.\""+ws+"\"]\nprojects = [\"proj\"]\n[daemon]\ncell_max_concurrent_jobs = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := td.reloadConfig(); err != nil {
		t.Fatal(err)
	}
	if n := td.q.MaxConcurrent(); n != 1 {
		t.Fatalf("lowered ceiling = %d", n)
	}
	for _, wi := range []string{"wi_a", "wi_b"} {
		if _, live := td.rt.Info(wi); !live {
			t.Fatalf("lowering the Cell ceiling killed %s", wi)
		}
	}
}
