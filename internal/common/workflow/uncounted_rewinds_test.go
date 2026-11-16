package workflow_test

// uncounted_rewinds_test.go: the lane-declared uncounted rewind class
// (polyforge#67): `uncounted_rewinds: {reason_contains, max}` names the
// class of gate-FAIL rewinds charged to no budget and counted on their
// own — the reason substring the lane declares, the class's own cap with
// DefaultUncountedRewinds when the lane names none.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// uncountedLaneFront is a lane front matter carrying a rewind edge and
// the uncounted class beside it.
const uncountedLaneFront = `max_rewinds: 2
uncounted_rewinds:
  reason_contains: the default branch moved since verification
  max: 2
steps:
  - { id: work }
  - { id: check, gate: true, on_fail: { target: work } }
`

// loadUncountedLane loads a lane whose front matter is front (a rewind
// edge and the class beside it unless front says otherwise), answering it
// or failing the test on its defects.
func loadUncountedLane(t *testing.T, front string) *workflow.Lane {
	t.Helper()
	dir := workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", front)
	lane, err := workflow.LoadStrict(dir)
	if err != nil {
		t.Fatalf("lane %s loads: %v", front, err)
	}
	return lane
}

// defectOf answers the one defect of front whose YAML path is path, or
// fails the test on what the load reported instead.
func defectOf(t *testing.T, front, path string) workflow.ContractError {
	t.Helper()
	dir := workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", front)
	_, errs := workflow.Load(dir)
	for _, e := range errs {
		if e.Path == path {
			return e
		}
	}
	t.Fatalf("lane %s: no defect at %s (have %v)", front, path, errs)
	return workflow.ContractError{}
}

// TestUncountedRewindsLaneKey: the lane declares the uncounted rewind
// class with `uncounted_rewinds` — the reason substring a gate FAIL's
// one-line reason carries, and the class's own cap (the default when the
// lane names none, 0 a valid budget asking the owner every time); a
// missing or blank reason, a negative cap and an unknown sub-key are
// defects; a lane with no rewind edge may not declare the class, exactly
// like max_rewinds.
func TestUncountedRewindsLaneKey(t *testing.T) {
	const class = "the default branch moved since verification"

	t.Run("declares the class", func(t *testing.T) {
		lane := loadUncountedLane(t, uncountedLaneFront)
		c := lane.UncountedRewinds
		if c == nil {
			t.Fatalf("the lane declares uncounted_rewinds: %+v", lane.UncountedRewinds)
		}
		if c.ReasonContains != class || c.Max != 2 {
			t.Fatalf("the declared class: %+v, want reason %q max 2", c, class)
		}
	})
	t.Run("the cap defaults when the lane names none", func(t *testing.T) {
		lane := loadUncountedLane(t, `max_rewinds: 2
uncounted_rewinds:
  reason_contains: moved
steps:
  - { id: work }
  - { id: check, gate: true, on_fail: { target: work } }
`)
		if c := lane.UncountedRewinds; c == nil || c.Max != workflow.DefaultUncountedRewinds {
			t.Fatalf("an absent max takes the default %d: %+v", workflow.DefaultUncountedRewinds, lane.UncountedRewinds)
		}
	})
	t.Run("zero is a valid cap", func(t *testing.T) {
		lane := loadUncountedLane(t, `max_rewinds: 2
uncounted_rewinds:
  reason_contains: moved
  max: 0
steps:
  - { id: work }
  - { id: check, gate: true, on_fail: { target: work } }
`)
		if c := lane.UncountedRewinds; c == nil || c.Max != 0 {
			t.Fatalf("max 0 is a budget (every matching FAIL asks the owner): %+v", lane.UncountedRewinds)
		}
	})
	t.Run("the reason is required", func(t *testing.T) {
		e := defectOf(t, `max_rewinds: 2
uncounted_rewinds:
  max: 1
steps:
  - { id: work }
  - { id: check, gate: true, on_fail: { target: work } }
`, "uncounted_rewinds.reason_contains")
		if e.Code != workflow.CodeMissingKey {
			t.Fatalf("a class without reason_contains: %s %s", e.Code, e.Msg)
		}
	})
	t.Run("a blank reason is a defect", func(t *testing.T) {
		e := defectOf(t, `max_rewinds: 2
uncounted_rewinds:
  reason_contains: " "
  max: 1
steps:
  - { id: work }
  - { id: check, gate: true, on_fail: { target: work } }
`, "uncounted_rewinds.reason_contains")
		if e.Code != workflow.CodeInvalidValue {
			t.Fatalf("a blank reason: %s %s", e.Code, e.Msg)
		}
	})
	t.Run("a negative cap is a defect", func(t *testing.T) {
		e := defectOf(t, `max_rewinds: 2
uncounted_rewinds:
  reason_contains: moved
  max: -1
steps:
  - { id: work }
  - { id: check, gate: true, on_fail: { target: work } }
`, "uncounted_rewinds.max")
		if e.Code != workflow.CodeInvalidValue {
			t.Fatalf("a negative cap: %s %s", e.Code, e.Msg)
		}
	})
	t.Run("an unknown sub-key is a defect", func(t *testing.T) {
		e := defectOf(t, `max_rewinds: 2
uncounted_rewinds:
  reason_contains: moved
  verdict: never
steps:
  - { id: work }
  - { id: check, gate: true, on_fail: { target: work } }
`, "uncounted_rewinds.verdict")
		if e.Code != workflow.CodeUnknownKey {
			t.Fatalf("an unknown sub-key: %s %s", e.Code, e.Msg)
		}
	})
	t.Run("a lane with no rewind edge refuses the class", func(t *testing.T) {
		e := defectOf(t, `uncounted_rewinds:
  reason_contains: moved
steps:
  - { id: work }
`, "uncounted_rewinds")
		if e.Code != workflow.CodeInvalidValue || !strings.Contains(e.Msg, "nothing to rewind") {
			t.Fatalf("no on_fail refuses uncounted_rewinds: %s %s", e.Code, e.Msg)
		}
	})
}
