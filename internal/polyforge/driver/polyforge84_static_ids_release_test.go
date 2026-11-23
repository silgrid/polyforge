// carries: internal/polyforge/cli/runtime_layout.go, internal/polyforge/cli/doctor_layout.go, internal/polyforge/driver/static_ids_test.go

package driver

// polyforge84_static_ids_release_test.go (polyforge#84 fix build, close red):
// contract ⑥'s coincidence for the host release tree moved with the code.
// #175 moved <data-dir>/release/current into cli/runtime_layout.go, but the
// coincidence map still declared cli/doctor_layout.go:release and never
// declared the file that carries the literal now — so
// TestNoStepIDOrLaneNameLiterals reds on the epic tree with one new hit and
// one stale listed literal. This guard names both halves, so a later move of
// the release tree fails loudly with the file to update.

import "testing"

func TestReleaseTreeCoincidenceFollowsTheCode(t *testing.T) {
	if coincidences["cli/runtime_layout.go:release"] == "" {
		t.Error("contract ⑥: cli/runtime_layout.go carries the host release tree's \"release\" literal; its coincidence must be declared")
	}
	if _, stale := coincidences["cli/doctor_layout.go:release"]; stale {
		t.Error("stale coincidence: cli/doctor_layout.go no longer carries the release tree literal; the entry belongs to the file that does")
	}
}
