package test

// w69_quota_convergence_and_scope_mismatch_test.go: review
// B4 — the owner-approved extension of the authored suite (needs_input
// w69-authored-suite-extension, answered by u_5dFjeaMZ). Four more
// fake-harness scenarios over the SHIPPED lanes, in the frozen file's
// style: the converging-review quota exemptions (AC 15) and the
// review/verification scope-mismatch human route (AC 10).
//
// Authored against artifacts/spec.md with the same record-level discipline
// as the frozen file: only execution.json's rewind ledger
// ({count, rewinds[{gate, to_step, charged}]}), the workflow status, the
// daemon's envelope_rejected events and the human-question face are judged
// — no driver-internal symbol. The gate envelopes carry NO `id`, so the
// same plan runs on either gate order and the pre-implementation baseline
// stays free of identity refusals.

import (
	"fmt"
	"testing"
	"time"
)

// w69qFindingsFail is one id-less gate FAIL envelope whose blocking
// findings are given (the residual_of marks ride the items; the driver
// reads them for the quota classification).
func w69qFindingsFail(findings []map[string]any) map[string]any {
	return map[string]any{"do": "envelope", "outcome": "gate_failed",
		"output": map[string]any{"verdict": "fail", "fail_reason": "w69: scripted review findings", "findings": findings}}
}

// w69qTextOnlyFail is one id-less all-text-only review FAIL (the
// polyforge#222 light shape: one blocking finding, text_only true).
func w69qTextOnlyFail() map[string]any {
	return map[string]any{"do": "envelope", "outcome": "gate_failed",
		"output": map[string]any{"verdict": "fail", "fail_reason": "w69: comment wording only",
			"findings": []map[string]any{{
				"severity": "blocking", "repair_target": "code", "text_only": true,
				"detail": "the doc comment above the changed function still names the old flag",
			}}}}
}

// w69qBugFixDeliverHead is the bug-fix lane up to (excluding) its gate
// tail, with the task repo created at the fix step (the delivery
// scenarios' world).
func w69qBugFixDeliverHead(origin string) []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "repro", "outcome": "completed", "handoff": true,
			"files": map[string]string{"artifacts/repro.md": "# repro\n\nw69 fake agent reproduction record\n"}},
		w222Env("root-cause", true),
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
	}
}

// w69qRewinds answers the rewind ledger's per-record entries.
func w69qRewinds(x *executionView) []map[string]any {
	raw, _ := x.RewindStats["rewinds"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// w69qAssertEdge asserts every ledger record names the gate→target edge
// with the charged flags wanted (in order).
func w69qAssertEdge(t *testing.T, x *executionView, gate, target string, charged []bool) {
	t.Helper()
	recs := w69qRewinds(x)
	if len(recs) != len(charged) {
		t.Fatalf("SYMPTOM: the ledger holds %d rewind records, want %d (rewind_stats=%v)",
			len(recs), len(charged), x.RewindStats)
	}
	for i, want := range charged {
		g, _ := recs[i]["gate"].(string)
		ts, _ := recs[i]["to_step"].(string)
		ch := recs[i]["class"] == "charged"
		if g != gate || ts != target || ch != want {
			t.Fatalf("SYMPTOM: rewind record %d = {gate %q, to_step %q, charged %v}, want "+
				"{%q, %q, %v} — the per-edge quota charged the wrong slot (rewind_stats=%v)",
				i, g, ts, ch, gate, target, want, x.RewindStats)
		}
	}
}

// Scenario A15-1 (bug-fix, delivery): a converging review sequence
// 3 → 2 → 1 → pass — every post-first round fully residual_of-marked,
// strictly fewer blocking findings — never trips a bound: only round 1
// charges the review→fix edge, every round still counts toward
// max_total, and the run reaches the delivery. The pre-implementation
// tree shares one counter per target step and has no per-record ledger,
// so the charged-flags assertion below is red there.
func TestW69ConvergingReviewSequenceIsNeverExhausted(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w69: a converging review sequence reaches delivery", "bug-fix")
	plan := append(w69qBugFixDeliverHead(origin),
		w69qFindingsFail([]map[string]any{
			{"severity": "blocking", "repair_target": "code", "detail": "w69: blocking finding a"},
			{"severity": "blocking", "repair_target": "code", "detail": "w69: blocking finding b"},
			{"severity": "blocking", "repair_target": "code", "detail": "w69: blocking finding c"},
		}),
		w67ChangedRound("fix", "demo", 1), // each fix round really fixes one finding
		w69qFindingsFail([]map[string]any{
			{"severity": "blocking", "repair_target": "code", "detail": "w69: blocking finding a", "residual_of": "s4#1"},
			{"severity": "blocking", "repair_target": "code", "detail": "w69: blocking finding b", "residual_of": "s4#2"},
		}),
		w67ChangedRound("fix", "demo", 2),
		w69qFindingsFail([]map[string]any{
			{"severity": "blocking", "repair_target": "code", "detail": "w69: blocking finding a", "residual_of": "s6#1"},
		}),
		w67ChangedRound("fix", "demo", 3),
		w69GatePass(), // review passes on the fully fixed tree
		w69GatePass(), // verification behind it
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "completed",
			"output_file": deliverOutput,
			"run":         literalBranch(branch, pushAndPR("demo"), mergeRecord("demo", ""), assemble)},
	)
	inst.setPlanFull(t, wi, plan, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitWorkflowStatus(t, d, wi, "completed", 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	w.mergedInto(origin, branch)
	x := readExecution(t, d.capsuleDir(wi))
	if n, _ := x.RewindStats["count"].(float64); int(n) != 3 {
		t.Fatalf("SYMPTOM: rewind_stats.count = %d, want 3 (every round counts toward max_total) — %v",
			int(n), x.RewindStats)
	}
	// only round 1 charged the review→fix edge; the converging rounds
	// (all residuals, strictly fewer) charged none.
	w69qAssertEdge(t, x, "review", "fix", []bool{true, false, false})
}

// Scenario A15-2 (bug-fix, no delivery): every round carries one NEW
// true bug (an unmarked blocking finding), so no round converges — each
// one charges the review→fix edge until, after max_per_step charged
// rounds, the next FAIL ends the workflow failed_rewind_exhausted by the
// EDGE bound (count far below max_total). The pre-implementation tree
// has no per-record ledger to name the edge, so the record assertions
// are red there.
func TestW69NewTrueBugEveryRoundHitsTheReviewEdgeBound(t *testing.T) {
	newBug := func(round int) map[string]any {
		return w69qFindingsFail([]map[string]any{{
			"severity": "blocking", "repair_target": "code",
			"detail": fmt.Sprintf("w69: round %d found a new true bug (concurrency) elsewhere", round),
		}})
	}
	plan := append(w69BugFixHead(),
		newBug(1), w222Env("fix", true),
		newBug(2), w222Env("fix", true),
		newBug(3), w222Env("fix", true),
		newBug(4), // the review→fix edge is at max_per_step: exhausted
	)
	d, wi, _ := w69RunLane(t, "w69: a new true bug every round hits the review edge bound", "bug-fix", plan)
	waitWorkflowStatus(t, d, wi, "failed_rewind_exhausted", 240*time.Second)
	x := readExecution(t, d.capsuleDir(wi))
	if n, _ := x.RewindStats["count"].(float64); int(n) != 3 {
		t.Fatalf("SYMPTOM: rewind_stats.count = %d, want 3 (the edge bound, not max_total, ends it) — %v",
			int(n), x.RewindStats)
	}
	w69qAssertEdge(t, x, "review", "fix", []bool{true, true, true})
}

// Scenario A15-3 (bug-fix, no delivery): text-only light rewinds charge
// no edge slot but still count toward max_total — nine of them fill
// max_total exactly, and the tenth FAIL ends the workflow
// failed_rewind_exhausted by the GLOBAL cap, never by the per-edge
// bound. The pre-implementation tree counts light rewinds into the
// shared per-target counter, so it dies at the fourth FAIL with count 3
// — the count assertion below is red there.
func TestW69TextOnlyLightRewindsChargeMaxTotalOnly(t *testing.T) {
	plan := w69BugFixHead()
	for i := 0; i < 9; i++ { // bug-fix max_total is 9: nine light slots
		plan = append(plan, w69qTextOnlyFail(), w222Env("fix", true))
	}
	plan = append(plan, w69qTextOnlyFail()) // count = max_total: the global cap ends it
	d, wi, _ := w69RunLane(t, "w69: text-only light rewinds charge max_total only", "bug-fix", plan)
	waitWorkflowStatus(t, d, wi, "failed_rewind_exhausted", 300*time.Second)
	x := readExecution(t, d.capsuleDir(wi))
	if n, _ := x.RewindStats["count"].(float64); int(n) != 9 {
		t.Fatalf("SYMPTOM: rewind_stats.count = %d, want 9 (a light rewind charges max_total, "+
			"never the per-edge bound) — %v", int(n), x.RewindStats)
	}
	charged := make([]bool, 9) // every light rewind charged no edge slot
	w69qAssertEdge(t, x, "review", "fix", charged)
}

// w69qScopedDeliver scripts one scoped feature-full delivery whose two
// id-less gate envelopes carry the given review-side and
// verification-side outputs (the first gate is review on this order,
// verification on the old one — the plan runs on both), then a lying
// merge claim and the human question the refusal must open.
func w69qScopedDeliver(origin, branch, qid string, reviewOut, verOut map[string]any) []map[string]any {
	gate := func(out map[string]any) map[string]any {
		o := map[string]any{"verdict": "pass"}
		for k, v := range out {
			o[k] = v
		}
		return map[string]any{"do": "envelope", "outcome": "completed", "output": o}
	}
	return append(w19FeatureHead(origin),
		gate(reviewOut),
		gate(verOut),
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "completed",
			"run": literalBranch(branch, pushAndPR("demo"),
				claimRecord("demo", `"merged":true,"merge_commit":"pending","tests_passed":true`), assemble),
			"output_file": deliverOutput},
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "needs_input", "question_id": qid,
			"question": "PR https://github.com/fake/demo/pull/1: the scope credential does not hold. " +
				"merge / don't merge / back to code / 全量重验?"},
	)
}

// w69qRunScopedDeliver starts one scoped feature-full delivery scenario.
func w69qRunScopedDeliver(t *testing.T, goal, qid string, reviewOut, verOut map[string]any) (*v1Daemon, *deliverWorld, string) {
	t.Helper()
	inst := newV1Install(t)
	inst.keepDeliver = true
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi := w19CreateWI(t, inst, goal, "feature-full", true)
	branch := "polyforge/" + wiPathName(t, inst, wi)
	inst.setPlanFull(t, wi, w69qScopedDeliver(origin, branch, qid, reviewOut, verOut), nil)
	d := startDeliverDaemon(t, inst, w, wi)
	return d, w, wi
}

// Scenario A10-a (feature-full deliver, scoped): review declares one
// scope adequate, verification verifies a DIFFERENT one — the two
// declarations cannot form one merging credential, so the auto-merge is
// refused naming the disagreement and the delivery goes to the human.
// The pre-implementation tree has no consistency rule: it merges
// unattended and no question ever opens — both assertions are red there.
func TestW69ScopeMismatchRoutesDeliveryToHuman(t *testing.T) {
	const qid = "w69-scope-mismatch"
	d, _, wi := w69qRunScopedDeliver(t, "w69: review and verification disagree on the scope", qid,
		map[string]any{"scope_adequate": true, "scope": w19Scope("zzz/this/path/does/not/exist.go")},
		map[string]any{"scope_adequate": true, "scope": w19Scope("internal/polyforge/driver/scope.go")},
	)
	expectRejection(t, d, wi, "differs from the scope review declared")
	waitQuestion(t, d, wi, qid, 120*time.Second)
}

// Scenario A10-b (feature-full deliver, scoped): review answers
// scope_adequate but records NO scope of its own — the credential is
// one-sided, so the auto-merge is refused for the missing review scope
// and the delivery goes to the human. The pre-implementation tree never
// asks: it merges unattended — both assertions are red there.
func TestW69ReviewRecordedNoScopeRoutesDeliveryToHuman(t *testing.T) {
	const qid = "w69-review-no-scope"
	d, _, wi := w69qRunScopedDeliver(t, "w69: review recorded no scope of its own", qid,
		map[string]any{"scope_adequate": true}, // no scope object on the review side
		map[string]any{"scope_adequate": true, "scope": w19Scope("internal/polyforge/driver/scope.go")},
	)
	expectRejection(t, d, wi, "recorded no scope of its own")
	waitQuestion(t, d, wi, qid, 120*time.Second)
}
