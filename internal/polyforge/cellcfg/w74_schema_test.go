package cellcfg

import (
	"testing"
)

// TestW74MaxConcurrentTasksHostOnlyLive (polyforge#74): the task lane's
// concurrency bound is a Host-only, live [daemon] key — every Cell's
// one-off tasks share it, the default Cell's file sets it and a reload
// applies it at once.
func TestW74MaxConcurrentTasksHostOnlyLive(t *testing.T) {
	k, ok := Lookup("daemon", "max_concurrent_tasks")
	if !ok {
		t.Fatal("max_concurrent_tasks must be in the Host/Cell key table")
	}
	if k.Scope != ScopeHost || k.Class != ClassLive || k.Section != "daemon" {
		t.Fatalf("max_concurrent_tasks = %+v, want a live Host-only [daemon] key", k)
	}
	if !HostOnly("daemon", "max_concurrent_tasks") {
		t.Error("max_concurrent_tasks must be Host-only")
	}
	inList := false
	for _, n := range HostOnlyKeyNames() {
		if n == "max_concurrent_tasks" {
			inList = true
		}
	}
	if !inList {
		t.Errorf("the Host-only refusal list must name max_concurrent_tasks: %v", HostOnlyKeyNames())
	}
}
