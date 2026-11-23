package liveconfig

// step_tiers_test.go (polyforge#81 u1, AC1): the Host [step_tiers]
// catalog behind the live state — the daemon reads the snapshot at
// dispatch time and a Host reload swaps it without a restart. The
// expected shapes come from AC1's three inputs (a two-tier catalog, a
// reload to a one-tier catalog, an empty catalog), never from the
// implementation's output; the copy check is what catches a getter that
// leaks the live map or a candidate slice.

import (
	"testing"
)

func TestStepTiersSnapshot(t *testing.T) {
	var nilState *State
	if got := nilState.StepTiers(); got != nil {
		t.Fatalf("a nil state must answer no catalog, got %v", got)
	}
	s := New(nil, "", 0, 0)
	if got := s.StepTiers(); got != nil {
		t.Fatalf("a fresh state must answer no catalog, got %v", got)
	}

	// AC1's startup catalog: main and judgment tiers, in order.
	s.SetStepTiers(map[string][]string{
		"main":      {"pi:a", "claude:b"},
		"judgment":  {"pi:j"},
	})
	got := s.StepTiers()
	if len(got) != 2 || len(got["main"]) != 2 || got["main"][0] != "pi:a" || got["main"][1] != "claude:b" ||
		len(got["judgment"]) != 1 || got["judgment"][0] != "pi:j" {
		t.Fatalf("the catalog = %v, want the two tiers as set", got)
	}

	// The getter is a deep copy: mutating what it returned (map or a
	// candidate slice) must not change the live state.
	got["main"][0] = "pi:mutated"
	delete(got, "judgment")
	got["new"] = []string{"pi:new"}
	again := s.StepTiers()
	if again["main"][0] != "pi:a" || len(again["judgment"]) != 1 || again["judgment"][0] != "pi:j" || again["new"] != nil {
		t.Fatalf("the getter must return a deep copy, state became %v", again)
	}

	// AC1's reload: the same state answers the new catalog, no restart;
	// an empty table is no catalog.
	s.SetStepTiers(map[string][]string{"main": {"pi:c"}})
	if got := s.StepTiers(); len(got) != 1 || got["main"][0] != "pi:c" {
		t.Fatalf("after the reload the catalog = %v, want only main=[pi:c]", got)
	}
	s.SetStepTiers(nil)
	if got := s.StepTiers(); got != nil {
		t.Fatalf("an empty catalog must read as nil, got %v", got)
	}
}
