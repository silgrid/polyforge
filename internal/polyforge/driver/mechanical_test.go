package driver

// mechanical_test.go: the mechanical light rewind (polyforge#260) — the
// FAIL-verdict consumption side of the mechanical fast lane. An ARMED
// verification gate's FAIL verdict that carries a mechanical
// classification (mechanical_bound) rewinds its target cheaply: one slot
// under today's bounds (checked before any mutation), the target archived
// gate_fail_rewind_mechanical, the verdict + the mechanical light re-entry
// note routed to the target's pending_rewind_input, and the pending light
// recheck marker set on the gate (the bound copied verbatim from the FAIL
// verdict's own classification, the diff base carried by the request).
// The light causes generalize: the re-entry resumes, the needs_code_change
// self-report rewinds fresh reusing the slot, and the candidate pointer
// resets on the mechanical cause like on every rewind archive.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// mechGateSpec is an ARMED verification gate over a target (the
// mechanical_fix switch on; the fast lane's every branch is gated on it).
func mechGateSpec(target string) StepSpec {
	s := gateSpec(verificationStepID, target)
	s.MechanicalFix = true
	return s
}

// mechanicalBoundJSON is one well-formed mechanical classification: per
// failed check its repo, its exact command and the file it named.
const mechanicalBoundJSON = `[{"check":"lint","repo":"app","command":"gofmt -l .","files":["main.go"]}]`

// mechanicalFAIL is an armed verification gate FAIL verdict carrying a
// mechanical classification (and the full bar's records a real one owes).
func mechanicalFAIL() fakeEntry {
	return fakeEntry{res: &StepResult{Outcome: OutcomeGateFailed, Output: json.RawMessage(
		`{"verdict":"fail","fail_reason":"lint: main.go carries trailing whitespace (check lint)",` +
			`"checks":[{"name":"build","status":"pass"},{"name":"lint","status":"fail","detail":"main.go carries trailing whitespace"},` +
			`{"name":"authored-scenarios","status":"pass"}],` +
			`"test_bar":[{"repo":"app","command":"go test ./..."}],` +
			`"mechanical_bound":` + mechanicalBoundJSON + `}`)}}
}

// mechGateFail lands one verification gate FAIL (the outcome the verdict
// binding would have produced) over a programmatic workflow and answers
// the durable execution.
func mechGateFail(t *testing.T, verification StepSpec, out string, base map[string]string) *capsule.Execution {
	t.Helper()
	d, _, _, execPath := testEnv(t, spec("code"), verification)
	mustInit(t, d)
	mustStart(t, d, "code", 1)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "code", SpawnN: 1,
		Result: completedResult("code", 1, `{}`)}); err != nil {
		t.Fatalf("result code: %v", err)
	}
	mustStart(t, d, verificationStepID, 2)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: verificationStepID, SpawnN: 2,
		Result: &StepResult{StepID: verificationStepID, SpawnN: 2, Outcome: OutcomeGateFailed,
			Output: json.RawMessage(out)},
		Gate:          &GateVerdict{GateStepID: verificationStepID, TargetStep: "code", VerdictText: "lint red"},
		LightDiffBase: base,
	}); err != nil {
		t.Fatalf("gate_fail: %v", err)
	}
	return readExec(t, execPath)
}

// TestTransitionMechanicalRewindChargesSlotAndSetsMarker: an armed
// verification FAIL carrying a classification takes the mechanical light
// path — the target archived gate_fail_rewind_mechanical from the
// verification gate, exactly one slot, the marker set with the bound
// copied verbatim and the request-carried diff base, the mechanical
// light re-entry note routed beside the verdict, the resume order
// durable (the ledger cause), and the gate's own re-entry fresh.
func TestTransitionMechanicalRewindChargesSlotAndSetsMarker(t *testing.T) {
	base := map[string]string{"app": "0123456789abcdef"}
	exec := mechGateFail(t, mechGateSpec("code"),
		`{"verdict":"fail","fail_reason":"lint red","mechanical_bound":`+mechanicalBoundJSON+`}`, base)
	code, ver := exec.Steps["code"], exec.Steps[verificationStepID]
	att := code.Attempts[len(code.Attempts)-1]
	if att.Cause != CauseGateFailRewindMechanical || att.RewindGate != verificationStepID {
		t.Fatalf("code archived as (%s, %s), want the mechanical light cause from the verification gate", att.Cause, att.RewindGate)
	}
	if code.Status != capsule.StepPending || code.Output != nil {
		t.Fatalf("the mechanical rewind resets code pending, output cleared: %+v", code)
	}
	if exec.RewindStats.Count != 1 || len(exec.RewindStats.ToStep) != 1 || exec.RewindStats.ToStep[0] != "code" {
		t.Fatalf("exactly one slot, logged to code: %+v", exec.RewindStats)
	}
	if pri := code.PendingRewindInput; pri == nil || pri.ToStep != "code" ||
		pri.Note != MechanicalRewindNote || !strings.Contains(pri.Reason, "lint red") {
		t.Fatalf("the verdict + the mechanical light re-entry note must route to code: %+v", pri)
	}
	if !lightRewindContext(code.Attempts) {
		t.Fatalf("the ledger must carry the resume order (the mechanical cause IS the light context): %+v", code.Attempts)
	}
	// the marker: the bound copied from the FAIL verdict's own
	// classification (never re-typed by any step agent — the canonical
	// form of the same value), the diff base carried by the request
	// (driver-recorded, never step-supplied)
	marker := ver.PendingLightRecheck
	if marker == nil {
		t.Fatalf("the mechanical rewind must set the pending light recheck marker on the gate: %+v", ver)
	}
	var wantBound any
	if err := json.Unmarshal([]byte(mechanicalBoundJSON), &wantBound); err != nil {
		t.Fatal(err)
	}
	var gotBound any
	if err := json.Unmarshal(marker.Bound, &gotBound); err != nil {
		t.Fatalf("marker.Bound is not JSON: %s", marker.Bound)
	}
	if gotJSON, _ := json.Marshal(gotBound); string(gotJSON) != canoJSON(wantBound) {
		t.Fatalf("marker.Bound = %s, want the FAIL verdict's classification verbatim %s", marker.Bound, mechanicalBoundJSON)
	}
	if len(marker.DiffBase) != 1 || marker.DiffBase["app"] != "0123456789abcdef" {
		t.Fatalf("marker.DiffBase = %v, want the request-carried FAIL-settlement HEADs", marker.DiffBase)
	}
	// the gate itself: in_progress holding its verdict, its own re-entry
	// ordered fresh, its window reset (D6) — the light recheck is a fresh
	// conversation, never a resume of the judged one.
	if ver.Status != capsule.StepInProgress || ver.Output["verdict"] != "fail" {
		t.Fatalf("the gate is never done and holds its verdict: %+v", ver)
	}
	if ver.Supervision == nil || ver.Supervision.NextDispatch != capsule.NextDispatchFresh {
		t.Fatalf("the gate's own re-entry must stay fresh: %+v", ver.Supervision)
	}
	if ver.BudgetConsumed != (capsule.Budget{}) {
		t.Fatalf("D6 window reset missing: %+v", ver.BudgetConsumed)
	}
	if exec.CurrentStep != "code" {
		t.Fatalf("the position must move back to code: %q", exec.CurrentStep)
	}
}

// TestTransitionMechanicalRewindUnarmedOrUnclassifiedRewindsFresh: the
// switch is the gate — an UNARMED lane's FAIL verdict carrying a
// classification anyway, and an armed lane's FAIL without one, both take
// today's fresh rewind exactly (the plain cause, no note, no marker, no
// light re-entry).
func TestTransitionMechanicalRewindUnarmedOrUnclassifiedRewindsFresh(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec StepSpec
		out  string
	}{
		{"unarmed lane, classification carried anyway", gateSpec(verificationStepID, "code"),
			`{"verdict":"fail","fail_reason":"lint red","mechanical_bound":` + mechanicalBoundJSON + `}`},
		{"armed lane, no classification", mechGateSpec("code"),
			`{"verdict":"fail","fail_reason":"a test failed","checks":[{"name":"tests","status":"fail"}]}`},
	} {
		exec := mechGateFail(t, tc.spec, tc.out, map[string]string{"app": "0123"})
		code, ver := exec.Steps["code"], exec.Steps[verificationStepID]
		att := code.Attempts[len(code.Attempts)-1]
		if att.Cause != CauseGateFailRewind || att.RewindGate != verificationStepID {
			t.Errorf("%s: code archived as (%s, %s), want today's fresh rewind", tc.name, att.Cause, att.RewindGate)
		}
		if pri := code.PendingRewindInput; pri == nil || pri.Note != "" {
			t.Errorf("%s: no light re-entry note rides an unclassified rewind: %+v", tc.name, pri)
		}
		if lightRewindContext(code.Attempts) {
			t.Errorf("%s: the fresh rewind must not order a resume", tc.name)
		}
		if ver.PendingLightRecheck != nil {
			t.Errorf("%s: no marker on an unclassified rewind: %+v", tc.name, ver.PendingLightRecheck)
		}
	}
}

// TestTransitionMechanicalRewindBoundsHold: the mechanical rewind is no
// budget bypass — an over-bound mechanical FAIL ends
// failed_rewind_exhausted BEFORE any mutation (no archive, no slot, no
// marker, no verdict routed), exactly like every rewind.
// TestTransitionMechanicalRewindBoundsHold: the mechanical rewind is no
// budget bypass - an over-bound mechanical FAIL ends
// failed_rewind_exhausted BEFORE any mutation (no archive, no slot, no
// verdict routed), exactly like every rewind. The second mechanical FAIL
// arrives after the light path settled (the light recheck FAILed and
// escalated in place): the follow-up mechanical rewind attempt hits
// max_per_step.
func TestTransitionMechanicalRewindBoundsHold(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("code"), mechGateSpec("code"))
	d.workflow.MaxPerStep = 1
	mustInit(t, d)
	complete := func(n int64, stepID string) {
		t.Helper()
		mustStart(t, d, stepID, n)
		if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: stepID, SpawnN: n,
			Result: completedResult(stepID, n, `{}`)}); err != nil {
			t.Fatalf("result %s %d: %v", stepID, n, err)
		}
	}
	mechFail := func(n int64) {
		t.Helper()
		mustStart(t, d, verificationStepID, n)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: verificationStepID, SpawnN: n,
			Result: &StepResult{StepID: verificationStepID, SpawnN: n, Outcome: OutcomeGateFailed,
				Output: json.RawMessage(`{"verdict":"fail","fail_reason":"lint red","mechanical_bound":` + mechanicalBoundJSON + `}`)},
			Gate:          &GateVerdict{GateStepID: verificationStepID, TargetStep: "code", VerdictText: "lint red"},
			LightDiffBase: map[string]string{"app": "0123"},
		}); err != nil {
			t.Fatalf("gate_fail %d: %v", n, err)
		}
	}
	lightFail := func(n int64) {
		t.Helper()
		mustStart(t, d, verificationStepID, n)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: verificationStepID, SpawnN: n,
			Result: &StepResult{StepID: verificationStepID, SpawnN: n, Outcome: OutcomeGateFailed,
				Output: json.RawMessage(`{"verdict":"fail","fail_reason":"the bound command is still red"}`)},
			Gate: &GateVerdict{GateStepID: verificationStepID, TargetStep: "code", VerdictText: "still red"},
		}); err != nil {
			t.Fatalf("light recheck fail %d: %v", n, err)
		}
	}
	// round 1: code completes, the armed classified FAIL rewinds
	// mechanically (one slot, the marker set).
	complete(1, "code")
	mechFail(2)
	if readExec(t, execPath).Steps[verificationStepID].PendingLightRecheck == nil {
		t.Fatalf("the mechanical rewind must set the marker first")
	}
	// the light re-entry completes; the light recheck FAILs (a bound
	// command still red) and escalates in place - marker cleared, no slot.
	complete(3, "code")
	lightFail(4)
	if readExec(t, execPath).Steps[verificationStepID].PendingLightRecheck != nil {
		t.Fatalf("the light-path escalation must clear the marker")
	}
	// the escalated full re-run FAILs classified mechanical again: the
	// follow-up mechanical rewind hits max_per_step BEFORE any mutation.
	mechFail(5)
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusFailedRewindExhausted {
		t.Fatalf("the second mechanical rewind must hit max_per_step, got %q", exec.WorkflowStatus)
	}
	var mech int
	for _, a := range exec.Steps["code"].Attempts {
		if a.Cause == CauseGateFailRewindMechanical {
			mech++
		}
	}
	if mech != 1 || exec.RewindStats.Count != 1 {
		t.Fatalf("the bound fired before any mutation: mechanical archives %d, count %d", mech, exec.RewindStats.Count)
	}
	if code := exec.Steps["code"]; code.PendingRewindInput != nil || code.Status != capsule.StepDone {
		t.Fatalf("the exhausted rewind must not route a verdict nor reset the target: %+v", code)
	}
}

// TestTransitionMechanicalSelfReportRewindsFreshSameSlot: a completed
// result of the MECHANICAL light re-entry carrying needs_code_change:
// true is not archived done — the same write rewinds fresh with the same
// verdict, charging no additional slot (the generalization of #222's
// self-report rewind to the second light cause).
func TestTransitionMechanicalSelfReportRewindsFreshSameSlot(t *testing.T) {
	d, log, _, execPath := testEnv(t, spec("code"), mechGateSpec("code"))
	mustInit(t, d)
	mustStart(t, d, "code", 1)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "code", SpawnN: 1,
		Result: completedResult("code", 1, `{"ok":1}`)}); err != nil {
		t.Fatal(err)
	}
	mechFailAt := func(n int64) {
		t.Helper()
		mustStart(t, d, verificationStepID, n)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: verificationStepID, SpawnN: n,
			Result: &StepResult{StepID: verificationStepID, SpawnN: n, Outcome: OutcomeGateFailed,
				Output: json.RawMessage(`{"verdict":"fail","fail_reason":"lint red","mechanical_bound":` + mechanicalBoundJSON + `}`)},
			Gate:          &GateVerdict{GateStepID: verificationStepID, TargetStep: "code", VerdictText: "lint red"},
			LightDiffBase: map[string]string{"app": "0123"},
		}); err != nil {
			t.Fatalf("gate_fail %d: %v", n, err)
		}
	}
	mechFailAt(2)
	// the mechanical light re-entry starts (consuming the order) and
	// self-reports a structural fix (the fix must leave the bound).
	mustStart(t, d, "code", 3)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "code", SpawnN: 3,
		Result: completedResult("code", 3, `{"ok":2,"needs_code_change":true}`)}); err != nil {
		t.Fatalf("self-report result: %v", err)
	}
	exec := readExec(t, execPath)
	st := exec.Steps["code"]
	if st.Status != capsule.StepPending || st.Output != nil {
		t.Fatalf("the self-report must rewind the step, never archive it done: %+v", st)
	}
	att := st.Attempts[len(st.Attempts)-1]
	if att.Cause != CauseGateFailRewind || att.RewindGate != verificationStepID {
		t.Fatalf("the self-report archive must mirror the range rewind's shape (the light gate named): %+v", att)
	}
	if exec.RewindStats.Count != 1 {
		t.Fatalf("the self-report reuses the mechanical rewind's slot (no charge): %+v", exec.RewindStats)
	}
	if pri := st.PendingRewindInput; pri == nil || pri.ToStep != "code" || pri.Note != "" ||
		!strings.Contains(pri.Reason, "lint red") {
		t.Fatalf("the same FAIL verdict must ride the fresh re-entry (no Note): %+v", pri)
	}
	if got := log.kinds(); got[len(got)-1] != "rewind" {
		t.Fatalf("the self-report emits the rewind event, never step_done: %v", got)
	}
	// the fresh re-entry re-derives from scratch: no resume order
	if lightRewindContext(st.Attempts) {
		t.Fatalf("the self-report's fresh rewind must not order a resume: %+v", st.Attempts)
	}
}

// TestTransitionRewindRangeClearsLightRecheckMarker: every rewind-range
// reset clears the pending light recheck marker — a reset step never
// carries a stale one (a later gate FAIL that rewinds past the
// verification gate abandons the light path; its re-run is a full one).
func TestTransitionRewindRangeClearsLightRecheckMarker(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("code"), mechGateSpec("code"), gateSpec("review", "code"))
	mustInit(t, d)
	mustStart(t, d, "code", 1)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "code", SpawnN: 1,
		Result: completedResult("code", 1, `{}`)}); err != nil {
		t.Fatal(err)
	}
	// the mechanical rewind sets the marker on the verification gate
	mustStart(t, d, verificationStepID, 2)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: verificationStepID, SpawnN: 2,
		Result: &StepResult{StepID: verificationStepID, SpawnN: 2, Outcome: OutcomeGateFailed,
			Output: json.RawMessage(`{"verdict":"fail","fail_reason":"lint red","mechanical_bound":` + mechanicalBoundJSON + `}`)},
		Gate:          &GateVerdict{GateStepID: verificationStepID, TargetStep: "code", VerdictText: "lint red"},
		LightDiffBase: map[string]string{"app": "0123"},
	}); err != nil {
		t.Fatal(err)
	}
	if readExec(t, execPath).Steps[verificationStepID].PendingLightRecheck == nil {
		t.Fatalf("the mechanical rewind must set the marker first")
	}
	// a later review FAIL rewinds the range [code, review), which resets
	// the verification step (superseded) — the marker must not survive it
	mustStart(t, d, "code", 3)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "code", SpawnN: 3,
		Result: completedResult("code", 3, `{}`)}); err != nil {
		t.Fatal(err)
	}
	mustStart(t, d, verificationStepID, 4)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: verificationStepID, SpawnN: 4,
		Result: completedResult(verificationStepID, 4, `{"verdict":"pass"}`)}); err != nil {
		t.Fatal(err)
	}
	mustStart(t, d, "review", 5)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: 5,
		Result: &StepResult{StepID: "review", SpawnN: 5, Outcome: OutcomeGateFailed,
			Output: json.RawMessage(`{"verdict":"fail","fail_reason":"wrong"}`)},
		Gate: &GateVerdict{GateStepID: "review", TargetStep: "code", VerdictText: "wrong"},
	}); err != nil {
		t.Fatal(err)
	}
	if marker := readExec(t, execPath).Steps[verificationStepID].PendingLightRecheck; marker != nil {
		t.Fatalf("a rewind-range reset must clear the light recheck marker: %+v", marker)
	}
}

// TestMechanicalLightReentryKeepsAuthoredTestsGate: the fast lane does not
// bypass the authored-tests human gate — the mechanical light re-entry of
// code is a code dispatch like any other, so its completed result is
// refused while it changed an authored test file, and accepted once the
// file is restored (the existing completed-result validation, untouched).
func TestMechanicalLightReentryKeepsAuthoredTestsGate(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("e2e-author"), spec("code"), mechGateSpec("code"))
	repo := gitRepo(t, d.wi.CapsuleDir, "app", map[string]string{"t/a_test.go": "assert(strict)\n", "main.go": "v1\n"})
	authored := headOf(t, repo)
	sp.script = []fakeEntry{
		done(`{"test_files":["t/a_test.go"]}`), // e2e-author authors + commits the suite
		done(`{}`),                             // code's first round
		mechanicalFAIL(),                       // the armed verification FAILs all-static, classified
		{res: done(`{}`).res, act: editAndCommit(t, repo, "t/a_test.go", "assert(loose)\n")},  // the light re-entry weakens the authored test
		{res: done(`{}`).res, act: editAndCommit(t, repo, "t/a_test.go", "assert(strict)\n")}, // the re-entry restores it
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, execPath)
	// code dispatched three times: the first round, the mechanical light
	// re-entry (rejected for the weakened authored test), the retry
	var code []StepSpawn
	for _, s := range sp.spawns {
		if s.StepID == "code" {
			code = append(code, s)
		}
	}
	if len(code) != 3 {
		t.Fatalf("code dispatches %d, want 3 (first round, light re-entry, retry)", len(code))
	}
	if !code[1].Resume {
		t.Fatalf("the mechanical light re-entry must resume the previous conversation")
	}
	if fb := sp.spawnAt(4).PriorRejection; !strings.Contains(fb, "changed authored test files") {
		t.Fatalf("the light re-entry's weakened-test completion must be refused by the authored-tests gate: %q", fb)
	}
	if exec.Steps["code"].Status != capsule.StepDone {
		t.Fatalf("the restored test completes the re-entry: %+v", exec.Steps["code"])
	}
	if stamp, _ := exec.Steps["e2e-author"].Output[testFilesCommitKey].(map[string]any); stamp["app"] != authored {
		t.Fatalf("the driver must stamp the authored commit %s: %v", authored, stamp)
	}
}

// canoJSON answers v's canonical JSON (sorted map keys - the byte-faithful
// comparison base the w260 acceptance suite shares).
func canoJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(b)
}
