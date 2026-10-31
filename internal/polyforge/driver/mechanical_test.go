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
	if exec.RewindStats.Count != 1 || len(exec.RewindStats.Rewinds) != 1 ||
		exec.RewindStats.Rewinds[0].Gate != "verification" || exec.RewindStats.Rewinds[0].ToStep != "code" ||
		exec.RewindStats.Rewinds[0].Class == capsule.RewindCharged {
		t.Fatalf("exactly one slot, logged to code (a mechanical rewind charges no edge slot): %+v", exec.RewindStats)
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
// budget bypass - an over-bound mechanical FAIL ends
// failed_rewind_exhausted BEFORE any mutation (no archive, no slot, no
// verdict routed), exactly like every rewind. The second mechanical FAIL
// arrives after the light path settled (the light recheck FAILed and
// escalated in place); the mechanical rewind charges NO edge slot
// (the light rewinds are max_total's alone), so the bound
// that stops it is max_total.
func TestTransitionMechanicalRewindBoundsHold(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("code"), mechGateSpec("code"))
	d.workflow.MaxTotal = 1
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
	// follow-up mechanical rewind hits max_total BEFORE any mutation (the
	// edge was never charged — the first record below pins that).
	mechFail(5)
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusFailedRewindExhausted {
		t.Fatalf("the second mechanical rewind must hit max_total, got %q", exec.WorkflowStatus)
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
	if len(exec.RewindStats.Rewinds) != 1 || exec.RewindStats.Rewinds[0].Class == capsule.RewindCharged {
		t.Fatalf("the mechanical rewind must charge no edge slot: %+v", exec.RewindStats.Rewinds)
	}
	if code := exec.Steps["code"]; code.PendingRewindInput != nil || code.Status != capsule.StepDone {
		t.Fatalf("the exhausted rewind must not route a verdict nor reset the target: %+v", code)
	}
}

// TestTransitionMechanicalSelfReportRewindsFreshSameSlot: a completed
// result of the MECHANICAL light re-entry carrying needs_code_change:
// true is not archived done — the same write rewinds fresh with the same
// verdict, taking no additional slot but charging the light record's
// edge once (; the generalization of #222's self-report
// rewind to the second light cause).
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
			LightDiffBase: map[string]string{"app": "0123"},
		}); err != nil {
			t.Fatalf("gate_fail %d: %v", n, err)
		}
	}
	mechFailAt(2)
	// the mechanical rewind set the pending light recheck marker on the
	// gate — the self-report's fresh rewind must clear it in the same write
	if readExec(t, execPath).Steps[verificationStepID].PendingLightRecheck == nil {
		t.Fatalf("the mechanical rewind must set the light recheck marker first")
	}
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
	if exec.RewindStats.Count != 1 || len(exec.RewindStats.Rewinds) != 1 ||
		exec.RewindStats.Rewinds[0] != (capsule.RewindRecord{Gate: verificationStepID, ToStep: "code", Class: capsule.RewindCharged}) {
		t.Fatalf("the self-report reuses the mechanical rewind's slot and charges its edge once: %+v", exec.RewindStats)
	}
	if pri := st.PendingRewindInput; pri == nil || pri.ToStep != "code" || pri.Note != "" ||
		!strings.Contains(pri.Reason, "lint red") {
		t.Fatalf("the same FAIL verdict must ride the fresh re-entry (no Note): %+v", pri)
	}
	// the fresh re-entry re-derives from scratch: no resume order
	if lightRewindContext(st.Attempts) {
		t.Fatalf("the self-report's fresh rewind must not order a resume: %+v", st.Attempts)
	}
	// the self-report's same write abandons the pending light recheck on
	// the gate (the marker lives on the gate, outside the range the
	// self-report archives): a stale marker would make the gate's next
	// dispatch a light recheck over a fresh re-derivation's bound. Since
	// S1 it is abandoned like every other fresh
	// re-derivation's — with the audit record and the event, after the
	// rewind event (never step_done).
	assertLightRecheckAbandoned(t, exec, log, "needs_code_change")
}

// assertLightRecheckAbandoned pins's abandonment of
// verification's pending light recheck by a write that opened a fresh
// re-derivation of the build step: the marker cleared, the audit-only
// light_recheck_escalated attempt archived on verification (the reason
// naming why, the marker's facts, the abandoned flag) and, after the
// write's own rewind event, one light_recheck_escalated event naming
// verification and the same reason.
func assertLightRecheckAbandoned(t *testing.T, exec *capsule.Execution, log *eventLog, why string) {
	t.Helper()
	ver := exec.Steps[verificationStepID]
	if ver.PendingLightRecheck != nil {
		t.Fatalf("a fresh re-derivation must clear verification's pending light recheck: %+v", ver.PendingLightRecheck)
	}
	if len(ver.Attempts) == 0 {
		t.Fatalf("the abandonment must archive the audit-only escalation attempt on verification: none archived")
	}
	att := ver.Attempts[len(ver.Attempts)-1]
	if att.Cause != CauseLightRecheckEscalated || !strings.Contains(att.Detail, why) {
		t.Fatalf("the abandonment must archive the audit-only escalation attempt naming %q: %+v", why, att)
	}
	var audit map[string]any
	if json.Unmarshal(att.Result, &audit) != nil || audit["escalated"] != true || audit["abandoned"] != true ||
		audit["bound"] == nil || audit["diff_base"] == nil || audit["reason"] != att.Detail {
		t.Fatalf("the audit record must carry the marker's facts, the reason and the abandoned flag: %s", att.Result)
	}
	if !escalatedRecheckContext(ver.Attempts) || inPlaceEscalationContext(ver.Attempts) {
		t.Fatalf("an abandoned recheck orders the escalation note but never the unchanged-tree exemption ( S2): %+v", ver.Attempts)
	}
	kinds := log.kinds()
	n := len(kinds)
	if n < 2 || kinds[n-2] != "rewind" || kinds[n-1] != EvLightRecheckEscalated {
		t.Fatalf("the write's rewind event, then one escalation event: %v", kinds)
	}
	last := log.entries[len(log.entries)-1]
	if last.body["step"] != verificationStepID || last.body["detail"] != att.Detail {
		t.Fatalf("the escalation event must name verification and the reason: %+v", last.body)
	}
}

// mechFailThenLight drives a lane whose mechanical verification FAIL set
// the pending light recheck marker on verification and rewound build:
// build (1) and review PASS (2), the mechanical FAIL (3). The build
// step's light re-entry is the caller's next dispatch (ordinal 4).
func mechFailThenLight(t *testing.T, d *Driver, execPath, build string) {
	t.Helper()
	mustInit(t, d)
	result := func(step string, n int64, out string) {
		t.Helper()
		mustStart(t, d, step, n)
		if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: step, SpawnN: n,
			Result: completedResult(step, n, out)}); err != nil {
			t.Fatalf("result %s %d: %v", step, n, err)
		}
	}
	result(build, 1, `{}`)
	result("review", 2, `{"verdict":"pass"}`)
	mustStart(t, d, verificationStepID, 3)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: verificationStepID, SpawnN: 3,
		Result: &StepResult{StepID: verificationStepID, SpawnN: 3, Outcome: OutcomeGateFailed,
			Output: json.RawMessage(`{"verdict":"fail","fail_reason":"lint red","mechanical_bound":` + mechanicalBoundJSON + `}`)},
		LightDiffBase: map[string]string{"app": "0123"},
	}); err != nil {
		t.Fatal(err)
	}
	if readExec(t, execPath).Steps[verificationStepID].PendingLightRecheck == nil {
		t.Fatalf("the mechanical rewind must set the marker first")
	}
}

// TestTransitionTextOnlySelfReportAbandonsLightRecheck ( S1):
// verification FAIL mechanical → code light re-entry → review FAIL
// text_only (the marker kept: the code re-entry resumes the light repair)
// → code self-reports needs_code_change: true. That self-report opens a
// fresh re-derivation of code, so the marker must not survive it: left in
// place, review PASS would hand verification a LIGHT recheck over a
// fresh re-derivation — skipping the full bar and the authored scenarios.
// The latest light cause is text_only here, so the mechanical-only clear
// the self-report carried before never ran.
func TestTransitionTextOnlySelfReportAbandonsLightRecheck(t *testing.T) {
	d, log, _, execPath := testEnv(t, spec("code"), gateSpec("review", "code"), mechGateSpec("code"))
	d.workflow.MaxTotal, d.workflow.MaxPerStep = 6, 2
	mechFailThenLight(t, d, execPath, "code")
	mustStart(t, d, "code", 4) // the mechanical light re-entry
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "code", SpawnN: 4,
		Result: completedResult("code", 4, `{}`)}); err != nil {
		t.Fatal(err)
	}
	mustStart(t, d, "review", 5)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: 5,
		Result: &StepResult{StepID: "review", SpawnN: 5, Outcome: OutcomeGateFailed,
			Output: lightReviewOutput(`[{"severity":"blocking","detail":"d","text_only":true}]`)},
	}); err != nil {
		t.Fatal(err)
	}
	if readExec(t, execPath).Steps[verificationStepID].PendingLightRecheck == nil {
		t.Fatal("control: a text-only review FAIL keeps the marker")
	}
	mustStart(t, d, "code", 6) // the text-only light re-entry
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "code", SpawnN: 6,
		Result: completedResult("code", 6, `{"needs_code_change":true}`)}); err != nil {
		t.Fatalf("self-report: %v", err)
	}
	exec := readExec(t, execPath)
	if st := exec.Steps["code"]; st.Status != capsule.StepPending || lightRewindContext(st.Attempts) {
		t.Fatalf("the self-report rewinds code fresh: %+v", st)
	}
	assertLightRecheckAbandoned(t, exec, log, "needs_code_change")
}

// TestTransitionRootCauseFalseFromMechanicalReentryAbandonsLightRecheck
// ( S1): on a lane with a diagnosis step, the fix's
// mechanical light re-entry completes reporting root_cause_addressed:
// false. The driver rewinds to root-cause — a range [root-cause, fix)
// that never reaches verification — and the fix re-derives after the new
// diagnosis, so the pending light recheck is abandoned in that same
// write.
func TestTransitionRootCauseFalseFromMechanicalReentryAbandonsLightRecheck(t *testing.T) {
	d, log, _, execPath := testEnv(t, spec("repro"), gateSpec("root-cause", "repro"), spec("fix"),
		gateSpec("review", "fix"), mechGateSpec("fix"))
	d.workflow.MaxTotal, d.workflow.MaxPerStep = 6, 2
	mustInit(t, d)
	result := func(step string, n int64, out string) {
		t.Helper()
		mustStart(t, d, step, n)
		if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: step, SpawnN: n,
			Result: completedResult(step, n, out)}); err != nil {
			t.Fatalf("result %s %d: %v", step, n, err)
		}
	}
	result("repro", 1, `{}`)
	result("root-cause", 2, `{"verdict":"pass"}`)
	result("fix", 3, `{}`)
	result("review", 4, `{"verdict":"pass"}`)
	mustStart(t, d, verificationStepID, 5)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: verificationStepID, SpawnN: 5,
		Result: &StepResult{StepID: verificationStepID, SpawnN: 5, Outcome: OutcomeGateFailed,
			Output: json.RawMessage(`{"verdict":"fail","fail_reason":"lint red","mechanical_bound":` + mechanicalBoundJSON + `}`)},
		LightDiffBase: map[string]string{"app": "0123"},
	}); err != nil {
		t.Fatal(err)
	}
	if readExec(t, execPath).Steps[verificationStepID].PendingLightRecheck == nil {
		t.Fatalf("the mechanical rewind must set the marker first")
	}
	result("fix", 6, `{"root_cause_addressed":false,"summary":"the lint failure exposed a wrong diagnosis"}`)
	exec := readExec(t, execPath)
	if exec.CurrentStep != rootCauseStepID {
		t.Fatalf("the false report rewinds to root-cause: %q", exec.CurrentStep)
	}
	assertLightRecheckAbandoned(t, exec, log, "root_cause_addressed")
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

// TestTransitionStructuralReviewFailAbandonsLightRecheck:
// review runs before verification, so a review FAIL's range [code,
// review) no longer resets verification — a pending light recheck marker
// an earlier mechanical verification FAIL set would survive a STRUCTURAL
// review FAIL and make the next verification a light recheck over a fresh
// re-derivation. A non-light review FAIL abandons it in the same write,
// the light path's own fallback way: an audit-only light_recheck_escalated
// attempt on verification (reason + the marker's audit facts), the marker
// cleared, one light_recheck_escalated event after the review's own
// gate_verdict and rewind. A TEXT-ONLY (light) review FAIL keeps it: the
// code re-entry resumes the light repair the marker describes.
func TestTransitionStructuralReviewFailAbandonsLightRecheck(t *testing.T) {
	scene := func(t *testing.T, reviewFail json.RawMessage) (*eventLog, *capsule.Execution) {
		t.Helper()
		d, log, _, execPath := testEnv(t, spec("code"), gateSpec("review", "code"), mechGateSpec("code"))
		d.workflow.MaxTotal, d.workflow.MaxPerStep = 6, 2
		mustInit(t, d)
		result := func(step string, n int64, out string) {
			t.Helper()
			mustStart(t, d, step, n)
			if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: step, SpawnN: n,
				Result: completedResult(step, n, out)}); err != nil {
				t.Fatalf("result %s %d: %v", step, n, err)
			}
		}
		result("code", 1, `{}`)
		result("review", 2, `{"verdict":"pass"}`)
		// the mechanical verification FAIL sets the marker and rewinds to code
		mustStart(t, d, verificationStepID, 3)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: verificationStepID, SpawnN: 3,
			Result: &StepResult{StepID: verificationStepID, SpawnN: 3, Outcome: OutcomeGateFailed,
				Output: json.RawMessage(`{"verdict":"fail","fail_reason":"lint red","mechanical_bound":` + mechanicalBoundJSON + `}`)},
			LightDiffBase: map[string]string{"app": "0123"},
		}); err != nil {
			t.Fatal(err)
		}
		if readExec(t, execPath).Steps[verificationStepID].PendingLightRecheck == nil {
			t.Fatalf("the mechanical rewind must set the marker first")
		}
		// the mechanical light re-entry repairs, then review (now FIRST) FAILs
		result("code", 4, `{}`)
		mustStart(t, d, "review", 5)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: "review", SpawnN: 5,
			Result: &StepResult{StepID: "review", SpawnN: 5, Outcome: OutcomeGateFailed, Output: reviewFail},
		}); err != nil {
			t.Fatal(err)
		}
		return log, readExec(t, execPath)
	}

	t.Run("structural review FAIL abandons the light recheck", func(t *testing.T) {
		log, exec := scene(t, json.RawMessage(`{"verdict":"fail","fail_reason":"wrong"}`))
		if exec.WorkflowStatus != StatusRunning || exec.CurrentStep != "code" {
			t.Fatalf("the review FAIL still rewinds to code: %q %q", exec.WorkflowStatus, exec.CurrentStep)
		}
		ver := exec.Steps[verificationStepID]
		if ver.PendingLightRecheck != nil {
			t.Fatalf("a structural review FAIL must clear verification's pending light recheck: %+v", ver.PendingLightRecheck)
		}
		att := ver.Attempts[len(ver.Attempts)-1]
		if att.Cause != CauseLightRecheckEscalated || !strings.Contains(att.Detail, "review") {
			t.Fatalf("the abandonment must archive the audit-only escalation attempt with its reason: %+v", att)
		}
		var audit map[string]any
		if json.Unmarshal(att.Result, &audit) != nil || audit["escalated"] != true || audit["bound"] == nil ||
			audit["diff_base"] == nil || audit["reason"] != att.Detail {
			t.Fatalf("the audit record must carry the marker's facts and the reason: %s", att.Result)
		}
		if !escalatedRecheckContext(ver.Attempts) || ver.Supervision == nil || ver.Supervision.NextDispatch != capsule.NextDispatchFresh {
			t.Fatalf("verification's next dispatch is the escalated full re-run, fresh: %+v %+v", ver.Attempts, ver.Supervision)
		}
		kinds := log.kinds()
		n := len(kinds)
		if n < 3 || kinds[n-3] != "gate_verdict" || kinds[n-2] != "rewind" || kinds[n-1] != EvLightRecheckEscalated {
			t.Fatalf("the review's verdict and rewind, then one escalation event: %v", kinds)
		}
		last := log.entries[len(log.entries)-1]
		if last.body["step"] != verificationStepID || last.body["detail"] != att.Detail {
			t.Fatalf("the escalation event must name verification and the reason: %+v", last.body)
		}
	})

	t.Run("text-only review FAIL keeps the light recheck", func(t *testing.T) {
		log, exec := scene(t, lightReviewOutput(`[{"severity":"blocking","detail":"d","text_only":true}]`))
		ver := exec.Steps[verificationStepID]
		if ver.PendingLightRecheck == nil {
			t.Fatal("a text-only (light) review FAIL resumes the light repair: the marker stays")
		}
		for _, a := range ver.Attempts {
			if a.Cause == CauseLightRecheckEscalated {
				t.Fatalf("a light review FAIL abandons nothing: %+v", a)
			}
		}
		for _, k := range log.kinds() {
			if k == EvLightRecheckEscalated {
				t.Fatalf("a light review FAIL emits no escalation: %v", log.kinds())
			}
		}
	})
}

// TestMechanicalRewindNoteKeepsCodeFixesInScope pins the one instruction the
// de-duplication dropped from the step texts: on a mechanical
// re-entry a lint or format fix inside a bound-named CODE file is the agent's
// to apply, so it must not self-report needs_code_change for it.
func TestMechanicalRewindNoteKeepsCodeFixesInScope(t *testing.T) {
	for _, want := range []string{
		"A lint or format fix inside a bound-named code file IS yours to apply",
		"the bound is the boundary, not the text-only rule",
	} {
		// the note is hard-wrapped like every driver-rendered instruction:
		// compare with whitespace runs collapsed
		if !strings.Contains(strings.Join(strings.Fields(MechanicalRewindNote), " "), want) {
			t.Errorf("MechanicalRewindNote lost %q", want)
		}
	}
}
