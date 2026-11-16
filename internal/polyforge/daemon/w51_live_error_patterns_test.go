package daemon

// w51_live_error_patterns_test.go (polyforge#51): the two pattern lists and
// the two slow-model thresholds are LIVE [daemon] keys — the startup file
// seeds the live state and a reload swaps them without a restart, so the
// next classification and the next slow-model judgement read the new
// values.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// TestW51ReloadAppliesErrorConfigLive: the startup values are live before
// any reload, and one reload over a file that changes all four keys applies
// each of them at once (AC1: 改后无需重启，下一次判定即生效).
func TestW51ReloadAppliesErrorConfigLive(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.StartupFile = cli.Config{Daemon: cli.DaemonSettings{
			TransientErrorPatterns: []string{"startup-rule"},
			SlowModelP90Latency:    2 * time.Minute,
		}}
	})
	if r := td.live.TransientErrorRules(); len(r) != 1 || !r[0].MatchString("the startup-rule fired") {
		t.Fatalf("the startup file's patterns must be live before any reload: %v", r)
	}
	if got := td.live.SlowModelP90Latency(); got != 2*time.Minute {
		t.Fatalf("the startup file's slow threshold must be live: %s", got)
	}

	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cli.toml")
	td.cfg.ConfigPath = path
	toml := "[workspace.\"" + ws + "\"]\nprojects = [\"proj\"]\n\n[daemon]\n" +
		"transient_error_patterns = ['\\breloaded\\b']\n" +
		"model_error_patterns = ['boom-model']\n" +
		"slow_model_median_latency = \"45s\"\n" +
		"slow_model_p90_latency = \"4m\"\n"
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	resp, err := td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	applied := map[string]bool{}
	for _, o := range resp.Outcomes {
		applied[o.Key] = o.Status == "applied"
	}
	for _, k := range []string{"transient_error_patterns", "model_error_patterns", "slow_model_median_latency", "slow_model_p90_latency"} {
		if !applied[k] {
			t.Errorf("%s must apply live on the reload: %+v", k, resp.Outcomes)
		}
	}
	if r := td.live.TransientErrorRules(); len(r) != 1 || !r[0].MatchString("it reloaded, ok") {
		t.Fatalf("the reloaded transient pattern must be live: %v", r)
	}
	if r := td.live.ModelErrorRules(); len(r) != 1 || !r[0].MatchString("a boom-model failure") {
		t.Fatalf("the reloaded model pattern must be live: %v", r)
	}
	if got := td.live.SlowModelMedianLatency(); got != 45*time.Second {
		t.Fatalf("the reloaded median threshold must be live: %s", got)
	}
	if got := td.live.SlowModelP90Latency(); got != 4*time.Minute {
		t.Fatalf("the reloaded p90 threshold must be live: %s", got)
	}
}

// TestW51LiveLimitsCarryTheSlowThresholds: the live-limits closure the
// supervisor and the driver read carries the two slow-model thresholds, so
// the next judgement sees a changed file value (the defaults apply when
// unset).
func TestW51LiveLimitsCarryTheSlowThresholds(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.StartupFile = cli.Config{Daemon: cli.DaemonSettings{
			SlowModelMedianLatency: 45 * time.Second,
		}}
	})
	td.live.SetSlowModelP90Latency(4 * time.Minute)
	// the observe Monitor's LiveLimits closure is bound to the daemon's
	// live state; read it back through the daemon's own wiring.
	lim := td.liveLimitsForTest()
	if lim.SlowModelMedianLatency != 45*time.Second {
		t.Fatalf("liveLimits median = %s, want the live 45s", lim.SlowModelMedianLatency)
	}
	if lim.SlowModelP90Latency != 4*time.Minute {
		t.Fatalf("liveLimits p90 = %s, want the live 4m", lim.SlowModelP90Latency)
	}
	filled := lim.Fill()
	if filled.SlowModelMedianLatency != 45*time.Second || filled.SlowModelP90Latency != 4*time.Minute {
		t.Fatalf("Fill must keep the set thresholds: %+v", filled)
	}
}

// liveLimitsForTest reads the daemon's one live-limits closure.
func (td *testDaemon) liveLimitsForTest() supervise.Defaults {
	return td.liveLimitsFn()
}
