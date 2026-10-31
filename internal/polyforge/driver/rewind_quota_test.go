package driver

// rewind_quota_test.go: the per-edge rewind quota —
// max_per_step is charged per gate→target edge (two gates sharing one
// target no longer eat each other's slots), max_total stays the global
// hard cap over every rewind, a ledger that cannot attribute its count
// (the pre-w69 shape) fails closed on the next rewind, and the omitted
// max_total default derives the reachable min(4, #edges × max_per_step)
// instead of refusing the load (code review).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
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

// TestRewindQuotaCountsPerEdge: two gates share one rewind target; each
// gate→target edge carries its own max_per_step. The edge's bound fires
// on the gate that used it, never on the sibling gate, and the ledger
// attributes every rewind to its edge.
func TestRewindQuotaCountsPerEdge(t *testing.T) {
	d, log, _, execPath := testEnv(t, spec("build"), gateSpec("checkA", "build"), gateSpec("checkB", "build"))
	d.workflow.MaxTotal = 6
	d.workflow.MaxPerStep = 2
	mustInit(t, d)
	driveEdge(t, d, "build", "checkA", 1) // A edge: 1 charged
	driveEdge(t, d, "build", "checkB", 3) // B edge: 1 charged (the shared target no longer pools)
	driveEdge(t, d, "build", "checkA", 5) // A edge: 2 charged
	driveEdge(t, d, "build", "checkB", 7) // B edge: 2 charged
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusRunning || exec.RewindStats.Count != 4 {
		t.Fatalf("four rewinds across two edges must all land: %q %+v", exec.WorkflowStatus, exec.RewindStats)
	}
	for i, want := range []capsule.RewindRecord{
		{Gate: "checkA", ToStep: "build", Class: capsule.RewindCharged},
		{Gate: "checkB", ToStep: "build", Class: capsule.RewindCharged},
		{Gate: "checkA", ToStep: "build", Class: capsule.RewindCharged},
		{Gate: "checkB", ToStep: "build", Class: capsule.RewindCharged},
	} {
		if got := exec.RewindStats.Rewinds[i]; got != want {
			t.Fatalf("rewind record %d = %+v, want %+v (edge attribution in the ledger)", i, got, want)
		}
	}
	// The FIFTH rewind — checkA's edge at its max_per_step — must
	// terminate before any mutation: no new archive on the target, no
	// routed verdict, the sibling edge's quota intact.
	driveEdge(t, d, "build", "checkA", 9)
	exec = readExec(t, execPath)
	if exec.WorkflowStatus != StatusFailedRewindExhausted || exec.RewindStats.Count != 4 {
		t.Fatalf("checkA's third rewind must exhaust ITS edge only: %q %+v", exec.WorkflowStatus, exec.RewindStats)
	}
	var archives int
	for _, a := range exec.Steps["build"].Attempts {
		if a.Cause == CauseGateFailRewind {
			archives++
		}
	}
	if archives != 4 || exec.Steps["build"].PendingRewindInput != nil {
		t.Fatalf("the exhausted rewind must not mutate the target: %d archives, pri %+v", archives, exec.Steps["build"].PendingRewindInput)
	}
	if got := log.kinds(); got[len(got)-2] != "gate_verdict" || got[len(got)-1] != "terminal" {
		t.Fatalf("the exhausted round emits its verdict then the terminal: %v", got)
	}
}

// TestRewindQuotaMaxTotalStaysGlobalCap: max_total caps every rewind
// regardless of which edge charged it — with two edges of max_per_step 2
// (4 charged rewinds reachable) but max_total 2, the THIRD rewind ends
// failed_rewind_exhausted on the global bound.
func TestRewindQuotaMaxTotalStaysGlobalCap(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec("checkA", "build"), gateSpec("checkB", "build"))
	d.workflow.MaxTotal = 2
	d.workflow.MaxPerStep = 2
	mustInit(t, d)
	driveEdge(t, d, "build", "checkA", 1)
	driveEdge(t, d, "build", "checkB", 3)
	driveEdge(t, d, "build", "checkA", 5)
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusFailedRewindExhausted || exec.RewindStats.Count != 2 {
		t.Fatalf("max_total must end the workflow at count 2: %q %+v", exec.WorkflowStatus, exec.RewindStats)
	}
}

// TestRewindOldLedgerShapeFailsClosed: an execution.json written before
// per-edge accounting (a count with no records to attribute it) can prove
// no quota. Its next gate FAIL ends failed_rewind_exhausted with a durable
// event naming why — never a guess at the attribution (no compat path).
func TestRewindOldLedgerShapeFailsClosed(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatal(err)
	}
	exec := skeletonExecution(WIInfo{ID: "wi_t1", CapsuleDir: dir, BundleName: "b"},
		&Workflow{Steps: []StepSpec{spec("build"), gateSpec("check", "build")}})
	exec.WorkflowStatus = StatusRunning
	exec.RewindStats = capsule.RewindStats{Count: 3} // the pre-w69 shape: a count, no records
	blob, _ := json.MarshalIndent(exec, "", "  ")
	if err := os.WriteFile(execPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	log := &eventLog{}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_t1", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: &Workflow{Steps: []StepSpec{spec("build"), gateSpec("check", "build")}, MaxTotal: 6, MaxPerStep: 2},
		Spawner:  &fakeSpawner{script: []fakeEntry{done(`{}`), gateFAIL()}},
		Log:      log,
		SpawnN:   0,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := readExec(t, execPath)
	if x.WorkflowStatus != StatusFailedRewindExhausted || x.RewindStats.Count != 3 {
		t.Fatalf("the unattributable ledger must fail closed without a new count: %q %+v", x.WorkflowStatus, x.RewindStats)
	}
	var named bool
	for _, e := range log.entries {
		if e.kind == "rewind_ledger_unattributable" {
			named = true
			if e.body["gate"] != "check" || e.body["count"] != int64(3) {
				t.Fatalf("the event must name the gate and the unattributable count: %+v", e.body)
			}
		}
	}
	if !named {
		t.Fatalf("the fail-close must emit its durable event: %v", log.kinds())
	}
}

// TestRewindDefaultsLoadOnEveryLaneShape (code review): the
// driver's own defaults must never fail the load. A one-edge workflow
// with no rewind block loads (the omitted total derives the reachable
// min(4, 1×2) = 2), a declared max_per_step ≤ 0 falls back like an
// omitted one, and a declared total above the edges' charged capacity
// rides as declared — max_total also counts the uncharged light,
// mechanical and converging rewinds, so such a total stays reachable
// and binding; no reachability refusal exists.
func TestRewindDefaultsLoadOnEveryLaneShape(t *testing.T) {
	// one on_fail edge, no rewind block: the omitted total derives 2.
	wf := mustParseExt(t, "steps:\n  - { id: a }\n  - { id: g, gate: true, context_policy: fresh, on_fail: { target: a } }\n")
	if wf.MaxTotal != 2 || wf.MaxPerStep != 2 {
		t.Fatalf("a one-edge workflow without a rewind block must load with the derived 2/2 defaults: %d/%d", wf.MaxTotal, wf.MaxPerStep)
	}
	// a declared max_per_step: 0 falls back like an omitted one (2),
	// and the omitted total still derives the reachable bound.
	wf = mustParseExt(t, "rewind:\n  max_per_step: 0\nsteps:\n  - { id: a }\n  - { id: g, gate: true, context_policy: fresh, on_fail: { target: a } }\n")
	if wf.MaxTotal != 2 || wf.MaxPerStep != 2 {
		t.Fatalf("a declared max_per_step: 0 must fall back to 2 with the derived total: %d/%d", wf.MaxTotal, wf.MaxPerStep)
	}
	// two edges × 2: the plain default 4 is reachable and rides.
	wf = mustParseExt(t, "steps:\n  - { id: a }\n  - { id: g1, gate: true, context_policy: fresh, on_fail: { target: a } }\n  - { id: g2, gate: true, context_policy: fresh, on_fail: { target: a } }\n")
	if wf.MaxTotal != 4 || wf.MaxPerStep != 2 {
		t.Fatalf("two edges keep the plain 4/2 defaults: %d/%d", wf.MaxTotal, wf.MaxPerStep)
	}
	// a declared total above the charged capacity loads as declared.
	wf = mustParseExt(t, "rewind:\n  max_total: 9\n  max_per_step: 3\nsteps:\n  - { id: a }\n  - { id: g, gate: true, context_policy: fresh, on_fail: { target: a } }\n")
	if wf.MaxTotal != 9 || wf.MaxPerStep != 3 {
		t.Fatalf("a declared total above edges × max_per_step must load as declared: %d/%d", wf.MaxTotal, wf.MaxPerStep)
	}
	// no rewind edges (a release-shaped lane): the 4/2 defaults untouched.
	wf = mustParseExt(t, "steps:\n  - { id: solo }\n")
	if wf.MaxTotal != 4 || wf.MaxPerStep != 2 {
		t.Fatalf("a lane without rewind edges keeps the 4/2 defaults: %d/%d", wf.MaxTotal, wf.MaxPerStep)
	}
}

// TestCrashAroundRewindKeepsEdgeLedger: the rewind record (count, edge
// attribution) lands in the SAME single gate-FAIL write — a kill -9 the
// instant the rewind event fires leaves a ledger the restart keeps
// verbatim, so a replayed FAIL round charges the edge exactly once.
func TestCrashAroundRewindKeepsEdgeLedger(t *testing.T) {
	e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
	e.wf.MaxTotal = 6
	e.wf.MaxPerStep = 2
	crashed := filepath.Join(t.TempDir(), "execution.json")
	log := &snapshotLog{kind: "rewind", src: e.execPath, dst: crashed}
	sp := &fakeSpawner{script: []fakeEntry{
		done(`{}`),                 // build (spawn 1)
		gateFAIL(),                 // review (spawn 2): the charged rewind
		done(`{}`),                 // build re-entry (spawn 3)
		done(`{"verdict":"PASS"}`), // review re-run (spawn 4)
	}}
	if err := e.driver(sp, 0, log).Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !log.taken {
		t.Fatal("no rewind event fired")
	}
	atCrash := tryReadExec(crashed)
	if atCrash == nil {
		t.Fatal("the crash-instant execution.json is unreadable")
	}
	if atCrash.RewindStats.Count != 1 || len(atCrash.RewindStats.Rewinds) != 1 ||
		atCrash.RewindStats.Rewinds[0] != (capsule.RewindRecord{Gate: "review", ToStep: "build", Class: capsule.RewindCharged}) {
		t.Fatalf("the crash instant must hold the full rewind record (count + edge attribution): %+v", atCrash.RewindStats)
	}
	raw, _ := os.ReadFile(crashed)
	if err := os.WriteFile(e.execPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// the restart continues on the durable ledger: the crashed round's
	// slot is already charged (never twice), two more FAIL rounds charge
	// the edge's second slot and then hit max_per_step — the edge's own
	// bound, with max_total still slack.
	sp2 := &fakeSpawner{script: []fakeEntry{
		done(`{}`), // build re-entry (spawn 3)
		gateFAIL(), // review (spawn 4): the edge's second slot
		done(`{}`), // build re-entry (spawn 5)
		gateFAIL(), // review (spawn 6): the edge's third — exhausted
	}}
	if err := e.driver(sp2, 2, nil).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	x := readExec(t, e.execPath)
	if x.WorkflowStatus != StatusFailedRewindExhausted || x.RewindStats.Count != 2 {
		t.Fatalf("the restart must keep the crashed round's slot and charge one more (then the edge bound): %q %+v", x.WorkflowStatus, x.RewindStats)
	}
	for i := range x.RewindStats.Rewinds {
		if got := x.RewindStats.Rewinds[i]; got != (capsule.RewindRecord{Gate: "review", ToStep: "build", Class: capsule.RewindCharged}) {
			t.Fatalf("rewind record %d = %+v", i, got)
		}
	}
}

// TestSelfReportEscalationChargesTheEdgeOnce: a text-only
// review FAIL rewinds light and charges no edge; when the light re-entry
// then self-reports needs_code_change: true, the round it escalates to is
// a fresh review→build round and must occupy that edge — exactly once
// (the light record flips to charged, no second record and no second
// max_total count: a classification error is not charged twice). Without
// it "light → needs_code_change → fresh → light FAIL → …" never charged
// review→build and only max_total stopped the loop. With max_per_step 1,
// the second escalation finds the edge spent and ends
// failed_rewind_exhausted before any mutation, emitting the terminal.
func TestSelfReportEscalationChargesTheEdgeOnce(t *testing.T) {
	d, log, _, execPath := testEnv(t, spec("build"), gateSpec("review", "build"))
	d.workflow.MaxTotal = 10 // far above the edge: only the edge may stop the loop
	d.workflow.MaxPerStep = 1
	mustInit(t, d)
	result := func(n int64, out string) {
		t.Helper()
		mustStart(t, d, "build", n)
		if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: n,
			Result: completedResult("build", n, out)}); err != nil {
			t.Fatalf("result build %d: %v", n, err)
		}
	}
	failLight := func(n int64) {
		t.Helper()
		mustStart(t, d, "review", n)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: "review", SpawnN: n,
			Result: &StepResult{StepID: "review", SpawnN: n, Outcome: OutcomeGateFailed,
				Output: lightReviewOutput(`[{"severity":"blocking","detail":"d","text_only":true}]`)},
		}); err != nil {
			t.Fatalf("gate_fail %d: %v", n, err)
		}
	}
	// Round 1: the light rewind (uncharged), then the self-report escalates.
	result(1, `{}`)
	failLight(2)
	result(3, `{"needs_code_change":true}`)
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusRunning || exec.RewindStats.Count != 1 ||
		len(exec.RewindStats.Rewinds) != 1 || exec.RewindStats.Rewinds[0] != (capsule.RewindRecord{Gate: "review", ToStep: "build", Class: capsule.RewindCharged}) {
		t.Fatalf("the escalation must charge the light record's edge once (no new record, no new count): %q %+v", exec.WorkflowStatus, exec.RewindStats)
	}
	if st := exec.Steps["build"]; st.Status != capsule.StepPending || st.PendingRewindInput == nil {
		t.Fatalf("the escalation still rewinds the step fresh: %+v", st)
	}
	// Round 2: the fresh round completes, review FAILs text-only again (the
	// light rewind itself still charges no edge), and the re-entry
	// self-reports again — the edge is spent.
	result(4, `{}`)
	failLight(5)
	result(6, `{"needs_code_change":true}`)
	exec = readExec(t, execPath)
	if exec.WorkflowStatus != StatusFailedRewindExhausted {
		t.Fatalf("the second escalation must find review→build spent (max_per_step 1), got %q %+v", exec.WorkflowStatus, exec.RewindStats)
	}
	if exec.RewindStats.Count != 2 || len(exec.RewindStats.Rewinds) != 2 ||
		exec.RewindStats.Rewinds[0].Class != capsule.RewindCharged || exec.RewindStats.Rewinds[1].Class == capsule.RewindCharged {
		t.Fatalf("the refused escalation must not mutate the ledger: %+v", exec.RewindStats)
	}
	st := exec.Steps["build"]
	if st.Status != capsule.StepInProgress || st.Output["needs_code_change"] != true {
		t.Fatalf("the refused escalation keeps the report on the step, never rewound or done: %+v", st)
	}
	if att := st.Attempts[len(st.Attempts)-1]; att.Cause != CauseGateFailRewindTextOnly {
		t.Fatalf("the refused escalation must archive nothing: %+v", att)
	}
	if got := log.kinds(); got[len(got)-1] != "terminal" {
		t.Fatalf("the refused escalation emits the terminal: %v", got)
	}
	for _, e := range log.entries {
		if (e.kind == "rewind" || e.kind == "step_done") && e.body["spawn_n"] == int64(6) {
			t.Fatalf("the refused escalation is neither a rewind nor done: %s %+v", e.kind, e.body)
		}
	}
}
