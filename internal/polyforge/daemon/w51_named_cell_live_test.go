// carries: internal/polyforge/daemon/cells.go, internal/polyforge/daemon/daemon.go

package daemon

// w51_named_cell_live_test.go (polyforge#51, self#B1): the four Host-only
// keys — transient_error_patterns, model_error_patterns,
// slow_model_median_latency, slow_model_p90_latency — live in the Host
// file, and a Host reload applies them to the default Cell. A named Cell
// reads them from the Host at the moment it judges, so its next
// classification and its next slow-model judgement see the change without
// a restart (AC1: 改后无需重启，下一次判定即生效).

import (
	"os"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

// TestW51NamedCellReadsHostLiveRules: a Host with a default and a named
// Cell; the Host file changes all four keys and the default Cell reloads;
// the named Cell's text rules and live limits read the new values.
func TestW51NamedCellReadsHostLiveRules(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	th.mu.Lock()
	def, alpha := th.cells[cellcfg.DefaultProfile].d, th.cells["alpha"].d
	th.mu.Unlock()
	if def == nil || alpha == nil {
		t.Fatal("premise: both Cells are valid")
	}
	if r := alpha.textRulesFn(); len(r.Transient) != 0 || len(r.Model) != 0 {
		t.Fatalf("premise: no rules before the reload: %+v", r)
	}

	ws, err := def.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	toml := "[workspace.\"" + ws + "\"]\nprojects = [\"proj\"]\n\n[daemon]\n" +
		"transient_error_patterns = ['\\bhost-reloaded\\b']\n" +
		"model_error_patterns = ['host-boom-model']\n" +
		"slow_model_median_latency = \"45s\"\n" +
		"slow_model_p90_latency = \"4m\"\n"
	if err := os.WriteFile(th.cfg.Set.HostPath, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := def.reloadConfig(); err != nil {
		t.Fatalf("the Host reload: %v", err)
	}

	r := alpha.textRulesFn()
	if len(r.Transient) != 1 || !r.Transient[0].MatchString("it host-reloaded, ok") {
		t.Fatalf("the named Cell's transient rules = %v, want the Host's reloaded \\bhost-reloaded\\b", r.Transient)
	}
	if len(r.Model) != 1 || !r.Model[0].MatchString("a host-boom-model failure") {
		t.Fatalf("the named Cell's model rules = %v, want the Host's reloaded host-boom-model", r.Model)
	}
	lim := alpha.liveLimitsFn()
	if lim.SlowModelMedianLatency != 45*time.Second {
		t.Fatalf("the named Cell's median bound = %s, want the Host's reloaded 45s", lim.SlowModelMedianLatency)
	}
	if lim.SlowModelP90Latency != 4*time.Minute {
		t.Fatalf("the named Cell's p90 bound = %s, want the Host's reloaded 4m", lim.SlowModelP90Latency)
	}
}
