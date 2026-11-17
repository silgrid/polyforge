package workflow_test

// rewind_only_coverage_test.go (polyforge#116 u2): the loader refuses a
// `rewind_only` step no gate's [on_fail target, gate) range covers —
// without a covering gate the step would never run (the first round
// skips it and no rewind can reach it). The range is the rewind's: it
// holds the target and every step between target and gate, and a gate
// may declare the key itself when a later gate's range covers it. The
// defect is WFC-112 at the step's own path, naming the step.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

func TestRewindOnlyCoverage(t *testing.T) {
	// covered: every shape the range takes
	for name, front := range map[string]string{
		"the on_fail target itself": "max_rewinds: 1\nsteps:\n  - id: build\n    rewind_only: true\n  - id: check\n    gate: true\n    on_fail: { target: build }\n",
		"a step between target and gate": "max_rewinds: 1\nsteps:\n  - id: build\n  - id: fix\n    rewind_only: true\n  - id: check\n    gate: true\n    on_fail: { target: build }\n",
		"a gate covered by a later gate's range": "max_rewinds: 1\nsteps:\n  - id: build\n  - id: check-a\n    gate: true\n    rewind_only: true\n    on_fail: { target: build }\n  - id: check-b\n    gate: true\n    on_fail: { target: build }\n",
		"either of two gates covers it": "max_rewinds: 1\nsteps:\n  - id: build\n  - id: fix\n    rewind_only: true\n  - id: check-b\n    gate: true\n    on_fail: { target: build }\n  - id: check-a\n    gate: true\n    on_fail: { target: fix }\n",
	} {
		lane, errs := workflow.Load(workflowtest.WriteLane(t, t.TempDir(), "demo", front))
		if len(errs) > 0 || lane == nil {
			t.Errorf("%s: defects %v, want none", name, errs)
		}
	}

	// refused: no gate's [on_fail target, gate) range covers the step
	for name, tc := range map[string]struct {
		front  string
		stepID string
	}{
		"a step after the last gate": {"max_rewinds: 1\nsteps:\n  - id: build\n  - id: check\n    gate: true\n    on_fail: { target: build }\n  - id: tail\n    rewind_only: true\n", "tail"},
		"a step before every target": {"max_rewinds: 1\nsteps:\n  - id: pre\n    rewind_only: true\n  - id: build\n  - id: check\n    gate: true\n    on_fail: { target: build }\n", "pre"},
		"a lane without any gate": {"steps:\n  - id: x\n    rewind_only: true\n", "x"},
		"a gate nothing later covers": {"max_rewinds: 1\nsteps:\n  - id: build\n  - id: check-a\n    gate: true\n    rewind_only: true\n    on_fail: { target: build }\n", "check-a"},
	} {
		_, errs := workflow.Load(workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", tc.front))
		if len(errs) != 1 {
			t.Errorf("%s: defects %v, want exactly one", name, errs)
			continue
		}
		e := errs[0]
		if e.Code != workflow.CodeInvalidValue {
			t.Errorf("%s: code %s, want %s", name, e.Code, workflow.CodeInvalidValue)
		}
		if !strings.Contains(e.Path, "("+tc.stepID+").rewind_only") {
			t.Errorf("%s: path %q names not the step's rewind_only", name, e.Path)
		}
		if !strings.Contains(e.Msg, tc.stepID) {
			t.Errorf("%s: the defect names not the step %q: %q", name, tc.stepID, e.Msg)
		}
		if !strings.Contains(e.Msg, "range covers") {
			t.Errorf("%s: the defect explains no covering range: %q", name, e.Msg)
		}
	}
}
