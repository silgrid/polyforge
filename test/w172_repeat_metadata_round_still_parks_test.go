package test

// w172_repeat_metadata_round_still_parks_test.go:, the
// repeat-citation counterpart of the repro's fixture A. After a first
// metadata-only repair (the conformance facts went from blocking to clean
// on an unchanged tree), a later gate FAIL rewinds code again; the
// re-submitted tree is still byte-identical and the build step re-emits the
// same cumulative `declared_extensions`. The guard must park
// that resubmission: the facts the gate last judged were already clean, so
// nothing in its input changed. The exemption keys on the facts the gate
// judged, never on a live citation (the build-step schema makes
// `declared_extensions` cumulative across rounds).
//
// The verification FAIL is the strongest variant: chore's verification
// entry sets `plan_conformance: true`, so it judged the repaired facts
// before failing.

import (
	"strings"
	"testing"
	"time"
)

// w172VerificationFail is a scripted verification FAIL after the metadata
// repair; the verdict carries the required checks/fail_reason shape.
func w172VerificationFail() map[string]any {
	return map[string]any{"do": "envelope", "id": "verification", "outcome": "gate_failed",
		"output": map[string]any{
			"verdict":     "fail",
			"fail_reason": "w172: a scripted verification FAIL after the metadata repair; the tree needs no further declaration change",
			"checks": []map[string]any{{
				"name": "tests", "status": "fail",
				"detail": "w172: scripted machine-check failure, unrelated to the plan-conformance metadata",
			}},
		}}
}

func TestW172RepeatedCumulativeCitationStillParks(t *testing.T) {
	extension := w172Extension("w172_undeclared.txt", "s1#1")
	plan := []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true,
			"output": w172PlanOutput("w172_declared.txt")},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true,
			"run":    []string{w172CommitRound(1, "w172_declared.txt", "w172_undeclared.txt")},
			"output": map[string]any{"build_status": "green"}},
		// review round 1: FAIL only on the driver's undeclared-file fact;
		// code's metadata-only repair is exempt from the guard.
		w172ReviewFailUnconformed("w172_undeclared.txt"),
		w172MetadataRound(extension),
		w172GatePass(),
		// verification then judges the repaired facts and FAILs for another
		// reason, rewinding code without a tree change.
		w172VerificationFail(),
		// code round 3 re-submits the identical tree with the same
		// cumulative citation: the facts the gate tail last judged were
		// already clean, so it must not re-run. A live-citation exemption
		// would dispatch the full tail again — review, then verification —
		// and complete on the same input.
		w172MetadataRound(extension),
		w172GatePass(),
		w172GatePass(),
	}
	_, _, capDir := w172RunLane(t, "w172: a repeated cumulative citation on an unchanged tree must still park", plan)
	waitExecution(t, capDir, 120*time.Second, "the workflow never settled", func(x *executionView) bool {
		return x.WorkflowStatus != "" && x.WorkflowStatus != "running"
	})

	x := readExecution(t, capDir)
	if x.WorkflowStatus != "blocked" {
		t.Fatalf("SYMPTOM: workflow_status %q, want blocked — the facts verification last judged were already clean, so an identical resubmission with the same cumulative citation must not re-run the gate: %s",
			x.WorkflowStatus, execDigest(x))
	}
	if !strings.Contains(x.BlockedReason, "gate tree unchanged") {
		t.Errorf("blocked reason misses %q: %s", "gate tree unchanged", x.BlockedReason)
	}
	if got := strings.Join(w222Order(t, capDir), ","); got != "plan,code,review,code,review,verification,code" {
		t.Errorf("dispatch order %s, want the no-change metadata round to park before a second verification", got)
	}
	if n := w222RewindCount(x); n != 2 {
		t.Errorf("rewind_stats.count = %v, want 2 (the light review rewind plus the charged verification rewind; the parked metadata round charges none)", n)
	}
}
