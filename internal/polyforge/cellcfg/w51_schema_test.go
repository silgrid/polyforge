package cellcfg

import "testing"

// TestW51ErrorPatternKeysHostOnlyLive (polyforge#51): the two additional
// failure-classification pattern lists and the two slow-model latency
// thresholds are Host-only, live [daemon] keys — the Host file (cli.toml)
// owns them, a named Cell's file never carries them, and a reload applies
// them without a restart.
func TestW51ErrorPatternKeysHostOnlyLive(t *testing.T) {
	for _, name := range []string{"transient_error_patterns", "model_error_patterns", "slow_model_median_latency", "slow_model_p90_latency"} {
		k, ok := Lookup("daemon", name)
		if !ok {
			t.Fatalf("%s must be in the Host/Cell key table", name)
		}
		if k.Scope != ScopeHost || k.Class != ClassLive || k.Section != "daemon" {
			t.Fatalf("%s = %+v, want a live Host-only [daemon] key", name, k)
		}
		if !HostOnly("daemon", name) {
			t.Errorf("%s must be Host-only", name)
		}
	}
	for _, name := range []string{"transient_error_patterns", "slow_model_p90_latency"} {
		found := false
		for _, n := range HostOnlyKeyNames() {
			if n == name {
				found = true
			}
		}
		if !found {
			t.Errorf("the Host-only refusal list must name %s: %v", name, HostOnlyKeyNames())
		}
	}
}
