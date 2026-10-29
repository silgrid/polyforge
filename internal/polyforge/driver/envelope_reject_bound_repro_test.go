package driver

// envelope_reject_bound_repro_test.go: B3 — the
// maxEnvelopeRejects bound is off by one. The documented contract is
// that a step whose envelopes fail validation THREE times
// consecutively goes BLOCKED (maxEnvelopeRejects = 3); the driver checks
// `count > maxEnvelopeRejects`, so it takes a fourth rejection.
//
// Written RED against the current driver: four dispatches before the
// block, not three. (The existing driver tests
// TestRunRejectedEnvelopeBlocksResultInvalidAfterBound,
// TestRejectionBoundBlocksOnceWithoutRelaunch and
// TestRunStaleResultFenceRejected encode the same off-by-one and will
// need their expectations moved to `maxEnvelopeRejects` when this is
// fixed.)

import (
	"context"
	"strings"
	"testing"
)

func TestEnvelopeRejectBoundBlocksOnTheThirdConsecutiveRejection(t *testing.T) {
	d, sp, execPath := runEnv(t, StepSpec{ID: "fix"})
	// badQuestion is a needs_input envelope without its question pair: a
	// deterministic substance-gate rejection. The trailing good envelope
	// is there to prove the block lands before it is ever consumed.
	sp.script = []fakeEntry{badQuestion(), badQuestion(), badQuestion(), done(`{}`)}

	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("a spent in-process retry bound must BLOCK cleanly, not error: %v", err)
	}
	if n := len(sp.spawns); n != maxEnvelopeRejects {
		t.Fatalf("%d dispatches before the block, want exactly the documented bound %d", n, maxEnvelopeRejects)
	}
	x := readExec(t, execPath)
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, FailResultInvalid) {
		t.Fatalf("status %q reason %q, want blocked with %s", x.WorkflowStatus, x.BlockedReason, FailResultInvalid)
	}
}
