package test

// review_exit_and_deadend_acceptance_test.go: the owner-exit, cancel,
// bug-fix dead-end, conditional fix-commit and all-green-baseline
// acceptance scenarios, authored before the change from the spec's
// scenario grammar and driven end to end through the real aihub, pf
// daemon/driver and scripted fake harness. Only observable faces are
// judged: execution.json (pending questions, steps, rewind ledger),
// the capsule's artifacts, the hub work-item status, the daemon events
// and the spawn journal. No driver-internal symbol is referenced, so
// the file compiles and fails at assertion level on the old tree.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// featureFullHeadThroughE2E is the feature-full lane up to (excluding)
// review. The e2e-author result carries a red authored baseline so the
// review scenarios are not the all-green case; S6 replaces that baseline.
func featureFullHeadThroughE2E(e2eOutput map[string]any) []map[string]any {
	if e2eOutput == nil {
		e2eOutput = map[string]any{
			"scenarios_path":  "artifacts/e2e-scenarios.md",
			"scenario_titles": []string{"the new behaviour is observable"},
			"baseline": []map[string]any{{
				"title": "the new behaviour is observable", "status": "fail", "evidence": "observed old behaviour",
			}},
		}
	}
	return []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "spec", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "grill-review", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "e2e-author", "outcome": "completed", "handoff": true, "output": e2eOutput},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true},
	}
}

// offGraphReviewFail is a review FAIL whose only blocking finding can
// only be repaired upstream (the spec), never by the build step.
func offGraphReviewFail() map[string]any {
	return map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{
		"verdict":     "fail",
		"fail_reason": "the acceptance wording requires a correction to the spec, not to the code",
		"findings": []map[string]any{{
			"severity": "blocking", "repair_target": "spec",
			"detail": "the spec's THEN clause names a behaviour the implementation must not invent",
		}},
	}}
}

// runAcceptanceLane files a work item on bundle, starts one real daemon
// and submits it, returning the daemon, install and capsule dir.
func runAcceptanceLane(t *testing.T, goal, bundle string, plan []map[string]any) (*v1Daemon, *v1Install, string, string) {
	t.Helper()
	inst, _ := newV1InstallTaskRepo(t)
	wi := newWIWithBundle(t, inst, goal, bundle, plan)
	d := newV1Daemon(t, inst)
	d.start()
	if out := d.pf(t, "submit", wi); !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}
	return d, inst, wi, d.capsuleDir(wi)
}

// stepDispatches counts one step's entries in the capsule spawn journal
// (0 when the journal has not been written yet — the wait pollers call it).
func stepDispatches(t *testing.T, capDir, step string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "spawn-journal.jsonl"))
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		var e map[string]any
		if json.Unmarshal([]byte(line), &e) == nil && e["step_id"] == step {
			n++
		}
	}
	return n
}

// pendingQuestion answers the first open question in execution.json.
func pendingQuestion(t *testing.T, capDir string) map[string]any {
	t.Helper()
	x := readExecution(t, capDir)
	for _, st := range x.Steps {
		if st.PendingQuestion != nil {
			return *st.PendingQuestion
		}
	}
	return nil
}

// waitQuestionOrStep waits until an owner question is open or the named
// step reaches want dispatches. The second return is true when the step
// re-dispatched first — the pre-change wrong route, so tests fail fast.
func waitQuestionOrStep(t *testing.T, capDir, step string, want int, bound time.Duration) (map[string]any, bool) {
	t.Helper()
	var q map[string]any
	stopped := false
	waitUntil(t, bound, "neither an owner question nor the wrong "+step+" re-dispatch appeared", func() bool {
		if x, err := readExecutionFile(capDir); err == nil {
			for _, st := range x.Steps {
				if st.PendingQuestion != nil {
					q = *st.PendingQuestion
					return true
				}
			}
		}
		if stepDispatches(t, capDir, step) >= want {
			stopped = true
			return true
		}
		return false
	})
	return q, stopped
}

// answerPending answers the open question through the real pf CLI.
func answerPending(t *testing.T, d *v1Daemon, wi string, q map[string]any, text string) {
	t.Helper()
	id, _ := q["id"].(string)
	if id == "" {
		t.Fatalf("the pending question carries no id: %v", q)
	}
	answerQuestion(t, d, wi, id, text)
}

// TestReviewOwnerExitAllOffGraph: an all-off-graph review FAIL raises
// the owner exit and does not rewind to code.
func TestReviewOwnerExitAllOffGraph(t *testing.T) {
	plan := append(featureFullHeadThroughE2E(nil), offGraphReviewFail())
	_, _, _, capDir := runAcceptanceLane(t, "the review owner exit is raised for an upstream-only failure", "feature-full", plan)

	q, rewound := waitQuestionOrStep(t, capDir, "code", 2, 120*time.Second)
	if q == nil || rewound {
		t.Fatalf("an all-off-graph review FAIL re-dispatched code instead of parking an owner question (%s)", execDigest(readExecution(t, capDir)))
	}
	text, _ := q["question"].(string)
	for _, want := range []string{"amend", "waive", "cancel"} {
		if !strings.Contains(strings.ToLower(text), want) {
			t.Errorf("the owner-exit question does not name %q: %s", want, text)
		}
	}
	fmt.Println("PASS S1")
}

// TestAmendmentLandsAndRoutesToCode: amend-upstream records the owner's
// verbatim answer in the grill-qa owner-rulings section and routes the
// repair to code without re-running spec or plan.
func TestAmendmentLandsAndRoutesToCode(t *testing.T) {
	const ruling = "amend-upstream: correct the spec's acceptance wording, keep the implementation unchanged"
	plan := append(featureFullHeadThroughE2E(nil), offGraphReviewFail(),
		// the build-step re-entry the amendment routes to
		map[string]any{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true,
			"run": []string{w67ChangedRound("code", "demo-src", 1)["run"].([]string)[0]}},
	)
	d, _, wi, capDir := runAcceptanceLane(t, "an amend-upstream ruling reaches the build step without re-authoring spec or plan", "feature-full", plan)

	q, rewound := waitQuestionOrStep(t, capDir, "code", 2, 120*time.Second)
	if q == nil || rewound {
		t.Fatalf("the all-off-graph review FAIL did not ask the owner (%s)", execDigest(readExecution(t, capDir)))
	}
	answerPending(t, d, wi, q, ruling)

	waitUntil(t, 120*time.Second, "the amendment never re-dispatched code", func() bool {
		return stepDispatches(t, capDir, "code") >= 2
	})
	if n := stepDispatches(t, capDir, "spec"); n != 1 {
		t.Errorf("spec re-ran %d times after an amend-upstream answer, want exactly once (never re-authored)", n)
	}
	if n := stepDispatches(t, capDir, "plan"); n != 1 {
		t.Errorf("plan re-ran %d times after an amend-upstream answer, want exactly once (never re-authored)", n)
	}
	qa := filepath.Join(capDir, "artifacts", "grill-qa.md")
	waitUntil(t, 30*time.Second, "the owner ruling never landed in artifacts/grill-qa.md", func() bool {
		raw, err := os.ReadFile(qa)
		return err == nil && strings.Contains(string(raw), ruling)
	})
	raw, err := os.ReadFile(qa)
	if err != nil {
		t.Fatalf("read grill-qa.md: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(raw)), "owner rulings") {
		t.Errorf("grill-qa.md carries no owner-rulings section:\n%s", raw)
	}
	fmt.Println("PASS S2")
}

// TestCancelAnswerEndsWorkItem: the reserved cancel answer at a
// non-review needs_input ends the item in the hub's existing canceled
// status and writes no memory wrap.
func TestCancelAnswerEndsWorkItem(t *testing.T) {
	const qid = "continue_or_stop"
	plan := []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": qid,
			"question": "Continue the remediation or stop?"},
		// the pre-change route: the answer is treated as ordinary guidance
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
	}
	d, inst, wi, capDir := runAcceptanceLane(t, "a plain remediation accepts the reserved cancel answer", "autopilot", plan)

	waitQuestion(t, d, wi, qid, 60*time.Second)
	answerQuestion(t, d, wi, qid, "cancel")

	waitUntil(t, 60*time.Second, "the cancel answer neither canceled the item nor re-dispatched grill", func() bool {
		return inst.wiStatus(t, wi) == "canceled" || stepDispatches(t, capDir, "grill") >= 2
	})
	if got := inst.wiStatus(t, wi); got != "canceled" {
		t.Fatalf("a reserved cancel answer left the work item %q, want the existing canceled status", got)
	}
	if got := inst.wiStatus(t, wi); got == "wrapped" {
		t.Fatalf("a canceled work item was wrapped (%q)", got)
	}
	for _, kind := range d.eventKinds(t, wi) {
		if kind == "work_item_wrapped" || kind == "wrapped" {
			t.Errorf("a canceled work item emitted the wrap event %q", kind)
		}
	}
	fmt.Println("PASS S3")
}

// TestNonReproducingBugAsksOwner: repro's honest reproduces:false parks
// an owner question instead of continuing into root-cause.
func TestNonReproducingBugAsksOwner(t *testing.T) {
	plan := []map[string]any{
		{"do": "envelope", "id": "repro", "outcome": "completed", "handoff": true,
			"output": map[string]any{"reproduces": false, "summary": "the reported failure does not occur on the pre-fix base"}},
		// the pre-change route: root-cause consumes the non-reproduction
		{"do": "envelope", "id": "root-cause", "outcome": "completed", "handoff": true,
			"output": map[string]any{"verdict": "pass", "root_cause": "none observed"}},
	}
	_, _, _, capDir := runAcceptanceLane(t, "an honest non-reproduction asks the owner", "bug-fix", plan)

	q, continued := waitQuestionOrStep(t, capDir, "root-cause", 1, 120*time.Second)
	if q == nil || continued {
		t.Fatalf("reproduces:false continued into root-cause instead of asking the owner (%s)", execDigest(readExecution(t, capDir)))
	}
	text := strings.ToLower(fmt.Sprint(q["question"]))
	for _, want := range []string{"repro", "continue", "cancel"} {
		if !strings.Contains(text, want) {
			t.Errorf("the non-reproduction question does not offer %q: %s", want, text)
		}
	}
	fmt.Println("PASS S4")
}

// TestFixEmptyCommitsConditional: a completed fix claiming
// root_cause_addressed:true with no commits is an invalid completion.
func TestFixEmptyCommitsConditional(t *testing.T) {
	plan := []map[string]any{
		{"do": "envelope", "id": "repro", "outcome": "completed", "handoff": true},
		w78RootCause("pass", ""),
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "output": map[string]any{
			"commits": []any{}, "build_status": "green", "root_cause_addressed": true,
			"regression_test": "waived: no test surface", "summary": "the diagnosis was fixed without a change",
		}},
		// the pre-change route: review accepts the empty fix as a completion
		w78GatePass(),
	}
	d, _, wi, capDir := runAcceptanceLane(t, "a true fix with no commits is not a valid completion", "bug-fix", plan)

	waitUntil(t, 120*time.Second, "the empty true fix was neither rejected nor accepted into review", func() bool {
		for _, e := range d.eventsOf(t, wi) {
			if e.Kind == "envelope_rejected" {
				return true
			}
		}
		return stepDispatches(t, capDir, "review") >= 1
	})
	rejected := false
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind == "envelope_rejected" {
			body := strings.ToLower(string(e.Body))
			if strings.Contains(body, "commit") || strings.Contains(body, "root_cause_addressed") {
				rejected = true
			}
		}
	}
	if !rejected {
		t.Fatalf("root_cause_addressed:true with an empty commits list was accepted (review dispatched %d times, events %v)",
			stepDispatches(t, capDir, "review"), d.eventKinds(t, wi))
	}
	fmt.Println("PASS S5")
}

// TestE2EAuthorAllGreenAsksOwner: an e2e-author result whose authored
// scenarios all pass at baseline parks the three legal owner answers
// instead of advancing to code.
func TestE2EAuthorAllGreenAsksOwner(t *testing.T) {
	green := map[string]any{
		"scenarios_path":  "artifacts/e2e-scenarios.md",
		"scenario_titles": []string{"the existing behaviour is observable"},
		"baseline": []map[string]any{{
			"title": "the existing behaviour is observable", "status": "pass", "evidence": "already green on the pre-change tree",
		}},
	}
	plan := featureFullHeadThroughE2E(green)
	_, _, _, capDir := runAcceptanceLane(t, "an all-green authored baseline asks the owner", "feature-full", plan)

	q, advanced := waitQuestionOrStep(t, capDir, "code", 1, 120*time.Second)
	if q == nil || advanced {
		t.Fatalf("an all-green baseline advanced to code instead of asking the owner (%s)", execDigest(readExecution(t, capDir)))
	}
	text := strings.ToLower(fmt.Sprint(q["question"]))
	for _, want := range []string{"spec", "skip", "cancel"} {
		if !strings.Contains(text, want) {
			t.Errorf("the all-green question does not offer %q: %s", want, text)
		}
	}
	fmt.Println("PASS S6")
}
