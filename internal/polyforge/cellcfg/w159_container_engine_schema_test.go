package cellcfg

// w159_container_engine_schema_test.go (polyforge#159, 84-engine):
// container_engine is a Host-only, restart-only [daemon] key — the engine
// is a Host capability (one image and volume storage per Host, never
// mixed between Cells of one Host), applied by a Host restart.

import (
	"slices"
	"testing"
)

func TestContainerEngineIsHostOnlyRestartOnly(t *testing.T) {
	k, ok := Lookup("daemon", "container_engine")
	if !ok {
		t.Fatal("container_engine must be in the Host/Cell key table")
	}
	if k.Section != "daemon" || k.Scope != ScopeHost || k.Class != ClassRestartOnly {
		t.Fatalf("container_engine = %+v, want a restart-only Host-only [daemon] key", k)
	}
	if !HostOnly("daemon", "container_engine") {
		t.Error("container_engine must be Host-only")
	}
	if !slices.Contains(HostOnlyKeyNames(), "container_engine") {
		t.Errorf("the Host-only refusal list must name container_engine: %v", HostOnlyKeyNames())
	}
}
