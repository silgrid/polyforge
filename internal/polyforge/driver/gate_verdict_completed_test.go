package driver

// gate_verdict_completed_test.go: the polyforge#106 near-miss as a
// driver-level reproduction.
//
// The reported incident: on the bug-fix lane the review step (a
// gate-flagged step with on_fail rewind) judged its verdict "fail" and
// recorded that verdict — plus the fail_reason and blocking findings
// the review output schema demands — in the result envelope's OUTPUT
// JSON, while delivering the envelope with outcome "completed". The
// driver never reads a verdict field inside output: it branches only
// on the envelope outcome, so "completed" advanced the workflow to the
// release step instead of firing the on_fail rewind to fix.
//
// This test pins the invariant the work item demands (wi_7omkUJfGaJIS
// acceptance): a gate's FAIL verdict must not be swallowable by a
// completed outcome — the driver must either bind the verdict to
// outcome gate_failed (so the rewind fires) or refuse the envelope
// fail-closed. Either way, the gate must NOT be archived as done and
// the workflow must NOT march past it on the strength of a fail
// verdict.
//
// It FAILS on the current code, for exactly the reported reason: the
// envelope is accepted, review is marked done, and the workflow
// completes.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestRunGateFailVerdictInsideCompletedOutputMustNotAdvance(t *testing.T) {
	// The bug-fix tail in miniature: fix (the on_fail rewind target)
	// then review (gate: true, on_fail rewind to fix).
	d, sp, execPath := runEnv(t, spec("fix"), gateSpec("review", "fix"))
	sp.script = []fakeEntry{
		done(`{"summary":"fix applied"}`),
		// The #106 shape exactly: the review gate's FAIL verdict rides
		// the OUTPUT JSON of a COMPLETED envelope — schema-conformant
		// fail branch (verdict + fail_reason + a blocking finding), the
		// verdict the review step's instructions told it to emit.
		fakeEntry{res: &StepResult{
			Outcome: OutcomeCompleted,
			Output: json.RawMessage(`{"verdict":"fail",` +
				`"fail_reason":"the fix suppresses the symptom at one call site; the cause is still reachable",` +
				`"findings":[{"severity":"blocking","detail":"root cause not removed"}]}`),
		}},
	}
	// The fixed driver rewinds (re-dispatching fix) or rejects the
	// envelope; either path consumes the script differently, so Run's
	// return value is not itself the assertion — the durable state is.
	_ = d.Run(context.Background())

	exec := readExec(t, execPath)

	// The reported symptom, verbatim: the gate's fail verdict was
	// accepted as a completed step and advanced the workflow.
	if st := exec.Steps["review"]; st != nil && st.Status == capsule.StepDone {
		t.Fatalf("SYMPTOM (#106): review's verdict:fail was archived as a COMPLETED step "+
			"(review status %q, output %s) — the on_fail rewind to fix never fired",
			st.Status, mustJSON(t, st.Output))
	}
	if exec.WorkflowStatus == StatusCompleted {
		t.Fatalf("SYMPTOM (#106): the workflow COMPLETED on the strength of a gate FAIL verdict "+
			"(status %q, rewind_stats %+v, review_rounds %v) — expected the on_fail rewind to fix instead",
			exec.WorkflowStatus, exec.RewindStats, exec.ReviewRounds)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	blob, err := json.Marshal(v)
	if err != nil {
		return "<unmarshalable>"
	}
	return string(blob)
}
