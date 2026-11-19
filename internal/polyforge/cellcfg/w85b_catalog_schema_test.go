package cellcfg

import "testing"

// TestW85bCatalogKeysHostOnlyLive (polyforge#143, 85b): the [step_tiers]
// model-candidate catalog is a Host-only, live key family addressed by
// section — any tier name looks up the one row, exactly like a
// [workspace] or [memory_policy] table (the tier names are not a fixed
// set: the lane `tiers` fix nothing here, Q1 A).
func TestW85bCatalogKeysHostOnlyLive(t *testing.T) {
	for _, name := range []string{"default", "cheap", "mid", "a-tier-name-of-forty"} {
		k, ok := Lookup("step_tiers", name)
		if !ok {
			t.Fatalf("Lookup(step_tiers, %s) must hit the catalog row (a tier name is not a fixed key)", name)
		}
		if k.Scope != ScopeHost || k.Class != ClassLive || k.Section != "step_tiers" {
			t.Fatalf("step_tiers/%s = %+v, want a live Host-only [step_tiers] key", name, k)
		}
		if !HostOnly("step_tiers", name) {
			t.Errorf("step_tiers/%s must be Host-only", name)
		}
	}
	found := false
	for _, n := range HostOnlyKeyNames() {
		if n == "step_tiers" {
			found = true
		}
	}
	if !found {
		t.Errorf("the Host-only refusal list must name step_tiers: %v", HostOnlyKeyNames())
	}
}
