package driver

// rewind_quota_test.go: the rewind ledger — max_rewinds
// is the global hard cap over every rewind whichever edge charged it
// (held at the call: RewindBudgetSpent), and each record attributes its
// edge.

import (
	"testing"
)

// driveEdge runs one build→gate round and FAILs the gate: one rewind of
// the named gate→target edge.
func driveEdge(t *testing.T, d *Driver, build, gate string, n int64) {
	t.Helper()
	mustStart(t, d, build, n)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: build, SpawnN: n, Result: completedResult(build, n, `{}`),
	}); err != nil {
		t.Fatalf("result %s (round %d): %v", build, n, err)
	}
	mustStart(t, d, gate, n+1)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: gate, SpawnN: n + 1,
		Result: &StepResult{StepID: gate, SpawnN: n + 1, Outcome: OutcomeGateFailed},
	}); err != nil {
		t.Fatalf("gate_fail %s (round %d): %v", gate, n, err)
	}
}

// TestRewindQuotaMaxTotalStaysGlobalCap: max_rewinds caps every rewind
// regardless of which edge charged it — with two edges and max_rewinds 2,
// the budget is spent after two rewinds across them, so the call refuses
// the third FAIL.
func TestRewindQuotaMaxTotalStaysGlobalCap(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec("checkA", "build"), gateSpec("checkB", "build"))
	d.workflow.MaxRewinds = 2
	mustInit(t, d)
	driveEdge(t, d, "build", "checkA", 1)
	if why := RewindBudgetSpent(d.workflow, readExec(t, execPath)); why != "" {
		t.Fatalf("one rewind of two: not spent: %s", why)
	}
	driveEdge(t, d, "build", "checkB", 3)
	exec := readExec(t, execPath)
	if RewindBudgetSpent(d.workflow, exec) == "" || exec.RewindStats.Count != 2 {
		t.Fatalf("max_rewinds 2 is spent across the two edges: %+v", exec.RewindStats)
	}
}
