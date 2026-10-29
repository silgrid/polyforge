package test

// w69_review_before_verification_acceptance_test.go: — the
// delivery lanes run `code -> review -> verification -> deliver` every
// round, delivery needs a review that covers the final diff, an authored
// scenario skip routes to a human, and rewind quota is charged per
// gate->target edge.
//
// Authored against artifacts/spec.md BEFORE the implementation
// (e2e-author), through the real daemon + Go driver and the fake harness
// (test/fakepi as pi and claude, the fake `gh`). Every scenario scripts
// the SHIPPED lane (bug-fix, autopilot, feature-full) and judges only
// records the spec names: the capsule spawn journal (dispatch order), the
// execution.json rewind ledger, the daemon's durable events and the
// human-question face. No driver-internal symbol is referenced, so the
// baseline red is assertion-level, never a build failure.
//
// ORDER-AGNOSTIC SCRIPTING: the gate envelopes carry NO `id`, so the fake
// agent's drop line is filled with the dispatched step's identity
// (spawn.go's collect) and the same plan runs on either gate order. That
// keeps the baseline run free of identity refusals and its 60s
// re-dispatch pause; the assertions read the DISPATCH ORDER and the
// ledger, which is exactly the behaviour under test.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// w69Journal answers the capsule spawn journal's step ids in dispatch
// order (a missing journal is "not yet", never fatal).
func w69Journal(capDir string) []string {
	raw, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "spawn-journal.jsonl"))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		var e map[string]any
		if json.Unmarshal([]byte(line), &e) == nil {
			if s, _ := e["step_id"].(string); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// w69BugFixHead is the bug-fix lane up to (excluding) its gate tail.
func w69BugFixHead() []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "repro", "outcome": "completed", "handoff": true,
			"files": map[string]string{"artifacts/repro.md": "# repro\n\nw69 fake agent reproduction record\n"}},
		w222Env("root-cause", true), w222Env("fix", true),
	}
}

// w69GatePass is one passing gate envelope, addressed to whichever gate
// the lane dispatches next (no id: collect fills the dispatch identity).
func w69GatePass() map[string]any {
	return map[string]any{"do": "envelope", "outcome": "completed",
		"output": map[string]any{"verdict": "pass"}}
}

// w69GateFail is one failing gate envelope; the gate's on_fail rewind
// fires (id-less, same reason as w69GatePass).
func w69GateFail(reason string) map[string]any {
	return map[string]any{"do": "envelope", "outcome": "gate_failed",
		"output": map[string]any{"verdict": "fail", "fail_reason": reason}}
}

// w69RunLane files one WI on bundle, scripts it and submits it.
func w69RunLane(t *testing.T, goal, bundle string, plan []map[string]any) (*v1Daemon, string, string) {
	t.Helper()
	inst := newV1Install(t)
	wi := newWIWithBundle(t, inst, goal, bundle, plan)
	d := newV1Daemon(t, inst)
	d.start()
	if out := d.pf(t, "submit", wi); !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}
	return d, wi, d.capsuleDir(wi)
}

// Scenario 1 (bug-fix, first round): after `fix` the next dispatch is
// `review`; verification follows it. On the pre-implementation tree the
// order is `verification` then `review`, so the assertion below is red.
func TestW69FirstRoundDispatchesReviewBeforeVerification(t *testing.T) {
	plan := append(w69BugFixHead(), w69GatePass(), w69GatePass())
	d, wi, capDir := w69RunLane(t, "w69: review precedes verification on the first round", "bug-fix", plan)
	waitWorkflowStatus(t, d, wi, "completed", 150*time.Second)
	seq := w69Journal(capDir)
	if len(seq) < 5 {
		t.Fatalf("dispatch journal %v, want the five lane dispatches", seq)
	}
	if seq[3] != "review" || seq[4] != "verification" {
		t.Fatalf("SYMPTOM: dispatch order after code is %v, want review then verification "+
			"(the first gate before the fix's tree can be machine-verified MUST be review)", seq[3:5])
	}
}

// Scenario 2 (bug-fix, gate FAIL rewind): the FAIL rewinds to fix; the
// very next gate in the re-entered round must be review again, then
// verification. The id-less FAIL also fires on the old order (it becomes
// a verification FAIL), so both trees reach the same re-entry; only the
// gate order after it differs — review first (new) vs verification first
// (old), which is the assertion.
func TestW69RewindRoundKeepsReviewBeforeVerification(t *testing.T) {
	plan := append(w69BugFixHead(),
		w69GateFail("w69: scripted gate fail"),
		w222Env("fix", true),
		w69GatePass(), w69GatePass(),
	)
	d, wi, capDir := w69RunLane(t, "w69: a gate rewind re-enters through review first", "bug-fix", plan)
	waitWorkflowStatus(t, d, wi, "completed", 150*time.Second)
	seq := w69Journal(capDir)
	if len(seq) < 7 {
		t.Fatalf("dispatch journal %v, want the five lane dispatches plus the re-entry", seq)
	}
	x := readExecution(t, capDir)
	if n, _ := x.RewindStats["count"].(float64); n != 1 {
		t.Fatalf("rewind_stats.count = %v, want exactly one rewind: %v", n, x.RewindStats)
	}
	// The re-entry fix is the last `fix` dispatch; the gate right after it
	// must be review.
	lastFix := -1
	for i, s := range seq {
		if s == "fix" {
			lastFix = i
		}
	}
	if lastFix < 0 || lastFix+1 >= len(seq) {
		t.Fatalf("no re-entry fix in the observed dispatch order %v", seq)
	}
	if seq[lastFix+1] != "review" {
		t.Fatalf("SYMPTOM: after the rewind to fix the dispatch order is %v — the re-entry re-ran "+
			"%q without a review round on the repaired diff", seq[lastFix:], seq[lastFix+1])
	}
}

// Scenario 3 (autopilot, per-edge quota): two verification FAILs then
// four deliver FAILs, each rewind separated by the interval's passing
// verification. Per-edge quota charges verification twice and deliver
// three times (count 5) and the FOURTH deliver FAIL hits the deliver
// edge's max_per_step. The pre-implementation tree counts by target step,
// so the second deliver FAIL is already the fourth rewind to `fix` and
// ends the workflow with count 3 — the assertion below is red.
func TestW69PerGateQuotaDoesNotShareRewindSlots(t *testing.T) {
	inst := newV1Install(t)
	inst.keepDeliver = true
	verFail := map[string]any{"do": "envelope", "id": "verification", "outcome": "gate_failed",
		"output": map[string]any{"verdict": "fail", "fail_reason": "w69: scripted verification fail"}}
	delFail := map[string]any{"do": "envelope", "id": "deliver", "outcome": "gate_failed",
		"output": map[string]any{"verdict": "fail", "fail_reason": "w69: scripted deliver fail"}}
	verPass := map[string]any{"do": "envelope", "id": "verification", "outcome": "completed",
		"output": map[string]any{"verdict": "pass"}}
	plan := []map[string]any{{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true}}
	for i := 0; i < 2; i++ {
		plan = append(plan, w222Env("fix", true), verFail)
	}
	for i := 0; i < 4; i++ {
		plan = append(plan, w222Env("fix", true), verPass, delFail)
	}
	wi := inst.newWI(t, "w69: per-gate rewind quota", plan, nil)
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	waitWorkflowStatus(t, d, wi, "failed_rewind_exhausted", 240*time.Second)
	x := readExecution(t, d.capsuleDir(wi))
	n, _ := x.RewindStats["count"].(float64)
	if int(n) != 5 {
		t.Fatalf("SYMPTOM: rewind_stats.count = %d, want 5 (two charged verification rewinds + "+
			"three charged deliver rewinds; the fourth deliver FAIL is the deliver edge's bound, "+
			"not a slot) — %v", int(n), x.RewindStats)
	}
}

// w69DeliverRun is one scripted delivery attempt: push the feature
// branch, open/reuse the PR and claim the merge (the driver judges the
// claimed merge, no real gh merge is needed).
func w69DeliverRun(branch string) []string {
	return literalBranch(branch, pushAndPR("demo"),
		claimRecord("demo", `"merged":true,"merge_commit":"pending","tests_passed":true`), assemble)
}

// w69FeatureFullDeliver scripts a feature-full delivery. The gate and
// delivery envelopes are id-less (see the file header), so the plan is
// valid for both gate orders: the third gate action is the one that
// carries the scenario's new fact.
func w69FeatureFullDeliver(origin, branch, qid, question string, secondGate map[string]any) []map[string]any {
	deliver := w69DeliverRun(branch)
	return []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "spec", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "grill-review", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "e2e-author", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		w69GatePass(),
		secondGate,
		{"do": "envelope", "outcome": "completed", "output_file": deliverOutput, "run": deliver},
		{"do": "envelope", "outcome": "needs_input", "question_id": qid, "question": question},
	}
}

// w69RunFeatureFullDeliver starts a feature-full delivery scenario.
func w69RunFeatureFullDeliver(t *testing.T, goal, qid, question string, secondGate map[string]any) (*v1Daemon, *deliverWorld, string) {
	t.Helper()
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, goal, "feature-full")
	inst.setPlanFull(t, wi, w69FeatureFullDeliver(origin, branch, qid, question, secondGate), nil)
	d := startDeliverDaemon(t, inst, w, wi)
	return d, w, wi
}

// w69AwaitHumanOrStop waits until the human question qid is open (true)
// or the workflow leaves running (false).
func w69AwaitHumanOrStop(t *testing.T, d *v1Daemon, wi, qid string, bound time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		if questionIDOf(statusQuiet(d), wi) == qid {
			return true
		}
		if x, err := readExecutionFile(d.capsuleDir(wi)); err == nil &&
			x.WorkflowStatus != "" && x.WorkflowStatus != "running" {
			return false
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// w69Rejections answers the envelope_rejected event bodies of one WI.
func w69Rejections(t *testing.T, d *v1Daemon, wi string) []string {
	t.Helper()
	var out []string
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind == "envelope_rejected" {
			out = append(out, string(e.Body))
		}
	}
	return out
}

// Scenario 4 (feature-full deliver): a required-mode verification report
// skips an authored scenario. The auto-merge must be refused and the
// delivery routed to a human question naming the skipped title. On the
// pre-implementation tree review judged the report in-process and deliver
// auto-merged — the workflow completes with no question (red).
func TestW69AuthoredScenarioSkipRoutesToHuman(t *testing.T) {
	const qid = "w69-authored-skip"
	secondGate := map[string]any{"do": "envelope", "outcome": "completed",
		"output": map[string]any{
			"verdict":         "pass",
			"authored_titles": []string{"w69 authored skip probe"},
			"scenarios": []map[string]any{{
				"title":  "w69 authored skip probe",
				"status": "skipped",
				"detail": "owner-facing skip probe",
			}},
		}}
	d, w, wi := w69RunFeatureFullDeliver(t, "w69: a skipped authored scenario needs a human", qid,
		"the verification report skipped an authored scenario; merge / don't merge / back to code?", secondGate)
	if !w69AwaitHumanOrStop(t, d, wi, qid, 180*time.Second) {
		x, _ := readExecutionFile(d.capsuleDir(wi))
		t.Fatalf("SYMPTOM: deliver did not ask a human about the skipped authored scenario "+
			"(workflow_status=%s, pr state %q, gh merges %d, rejections %v)",
			execDigest(x), w.prState("demo", "1"), w.calls("gh pr merge"), w69Rejections(t, d, wi))
	}
	var named bool
	for _, body := range w69Rejections(t, d, wi) {
		if strings.Contains(body, "w69 authored skip probe") {
			named = true
		}
	}
	if !named {
		t.Fatalf("the refusal does not name the skipped authored scenario: %v", w69Rejections(t, d, wi))
	}
}

// Scenario 5 (feature-full deliver, review coverage): review passes on
// head H, then the following gate dispatch lands a further commit (H2)
// and passes on it. Verification covers the delivered tree, but no review
// covers the final diff, so auto-merge must be refused and a human asked.
// The old tree has no reviewed head: its verification pass is followed by
// the commit, so the refusal it gives (if any) is the hand-made-history
// one — the assertion that the refusal names the review's missing
// coverage is red.
func TestW69PostReviewCommitBlocksAutoMerge(t *testing.T) {
	const qid = "w69-review-coverage"
	const changeAfterReview = `set -e; cd repos/demo; echo "landed after review" >> work.txt; ` +
		`git add work.txt; git -c user.name=w69 -c user.email=w69@invalid commit -q -m "w69: change after review"`
	secondGate := map[string]any{"do": "envelope", "outcome": "completed",
		"output": map[string]any{"verdict": "pass"}, "run": []string{changeAfterReview}}
	d, w, wi := w69RunFeatureFullDeliver(t, "w69: a change after review invalidates the pass", qid,
		"the review pass does not cover the delivered diff; merge / don't merge / back to code?", secondGate)
	human := w69AwaitHumanOrStop(t, d, wi, qid, 180*time.Second)
	var covers bool
	for _, body := range w69Rejections(t, d, wi) {
		low := strings.ToLower(body)
		if strings.Contains(low, "review") && (strings.Contains(low, "cover") || strings.Contains(low, "head")) {
			covers = true
		}
	}
	if !human || !covers {
		x, _ := readExecutionFile(d.capsuleDir(wi))
		t.Fatalf("SYMPTOM: deliver did not refuse the merge for missing review coverage "+
			"(human question open=%v, workflow_status=%s, pr state %q, gh merges %d, rejections %v)",
			human, execDigest(x), w.prState("demo", "1"), w.calls("gh pr merge"), w69Rejections(t, d, wi))
	}
}
