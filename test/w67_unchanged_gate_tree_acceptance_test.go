package test

// w67_unchanged_gate_tree_acceptance_test.go: criteria 5 and
// 6, end to end through the real daemon + Go driver and the fake harness
// (test/fakepi, the real bug-fix lane).
//
// The reported loop: a review FAIL rewinds fix; the rewound attempt dies of
// an upstream error; its retry resubmits the identical tree; verification and
// review re-run and the review FAILs again ( / #40). This test
// scripts exactly that shape and pins the two supplementary criteria:
//
//   - criterion 5: the fix retry's rendered prompt carries the FAIL verdict
//     and today's from-scratch reference, never "continue the previous
//     attempt's work" (the fast-path wording) — the attempt it
//     would continue made no code progress at all;
//   - criterion 6: the gate tail is not started again. Verification is not
//     dispatched, no extra rewind slot is charged, and the workflow parks
//     blocked with a human-visible reason naming the crashed fix attempt.
//
// RED on the pre-fix tree: the retry gets the continue-in-place text and
// verification/review re-run on the identical tree (the extra dispatch and
// the second rewind slot the assertions below reject). The fake fix steps
// change no repo file, so the task-repo tree really is unchanged; driver-
// owned artifacts/ archives are deliberately outside the judged tree.

import (
	"fmt"
	"strings"
	"testing"
)

// w67ChangedRound is the scripted stand-in for a build re-entry that really
// changed the code: one round-specific file committed in repos/<repo>, so the
// task-repo tree moves off the tree the rewinding gate judged. A re-entry
// that commits nothing is an identical resubmission, which the driver parks
// instead of re-running the gates ( D3,
// TestW67UncrashedIdenticalResubmissionParks) — so every scenario whose gate
// tail must re-run after a rewind scripts its build re-entry with this.
func w67ChangedRound(step, repo string, round int) map[string]any {
	return map[string]any{"do": "envelope", "id": step, "outcome": "completed", "handoff": true,
		"run": []string{fmt.Sprintf("set -e; cd repos/%s; printf 'build round %d\\n' > build-round.txt; git add build-round.txt; "+
			"git -c user.name=fake-agent -c user.email=fake-agent@invalid commit -q -m 'build round %d'", repo, round, round)}}
}

func TestW67UnchangedGateTreeStopsTheLoop(t *testing.T) {
	plan := append(w222BugFixHead(),
		// review FAIL: structural (no text_only), so the rewind is the fresh
		// shape the production #57/#40 loop used.
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{
			"verdict":     "fail",
			"fail_reason": "w67: the fix still does not guard the rewind round",
			"findings": []map[string]any{
				{"severity": "blocking", "detail": "w67: the guard is missing"},
			},
		}},
		// the rewound fix attempt dies of an upstream error (model class)
		map[string]any{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true,
			"crash": "before", "crash_stderr": w079ProviderErr},
		// the retry completes without touching the repo: the identical tree
		w222Env("fix", true),
	)
	capDir := w222RunPi(t, "w67: an unchanged rewound tree must not re-run the gates", plan)

	x := readExecution(t, capDir)
	if x.WorkflowStatus != "blocked" {
		t.Fatalf("workflow_status %q, want blocked (a human-visible unchanged-tree result): %s",
			x.WorkflowStatus, execDigest(x))
	}
	// review is the first gate behind fix now, so the guard
	// blocks the review re-entry (verification never even dispatched).
	for _, want := range []string{"gate tree unchanged", "review", "fix", "crashed with"} {
		if !strings.Contains(x.BlockedReason, want) {
			t.Errorf("blocked reason misses %q: %s", want, x.BlockedReason)
		}
	}
	if n, _ := x.RewindStats["count"].(float64); n != 1 {
		t.Errorf("rewind_stats.count = %v, want 1 — the unchanged-tree guard charges no extra slot (%v)",
			x.RewindStats["count"], x.RewindStats)
	}

	// Criterion 6: the gate tail did not start again.
	if got := strings.Join(w222Order(t, capDir), ","); got != "repro,root-cause,fix,review,fix,fix" {
		t.Errorf("dispatch order %s, want the review rewind, the crashed fix and its retry only — no review re-run", got)
	}
	if n := len(w222Steps(t, capDir, "verification")); n != 0 {
		t.Errorf("verification dispatched %d times, want never (the loop stops before the machine gate)", n)
	}
	if n := len(w222Steps(t, capDir, "review")); n != 1 {
		t.Errorf("review dispatched %d times, want once (never a re-run on the identical tree)", n)
	}

	// Criterion 5: the fix retry's rendered prompt. It is the second retry
	// shape (a candidate switch after the model-class crash), spawn 6
	// (repro 1, root-cause 2, fix 3, review 4, the crashed
	// fix 5, the retry 6).
	prompt := w21Prompt(t, capDir, 6)
	if !strings.Contains(prompt, "w67: the fix still does not guard the rewind round") {
		t.Errorf("the fix retry prompt lost the review FAIL verdict:\n%s", prompt)
	}
	if strings.Contains(prompt, "continue the previous attempt") || strings.Contains(prompt, "byte-identical") {
		t.Errorf("the no-progress fix retry still claims there is previous work to continue:\n%s", prompt)
	}
	if !strings.Contains(prompt, "start this attempt's work from scratch") {
		t.Errorf("the no-progress fix retry must keep the from-scratch reference wording:\n%s", prompt)
	}
}

// TestW67UncrashedIdenticalResubmissionParks is D3 end to end:
// the review FAIL rewinds fix, and the rewound fix receives the verdict,
// changes nothing and completes — no attempt crashes anywhere. Re-judging
// the identical tree can only repeat the verdict or pass by chance, so the
// review is not dispatched again: the workflow parks blocked, no extra
// rewind slot is charged, and the reason quotes fix's own summary (why it
// changed nothing). The scripted gate tail is what a re-run would consume.
func TestW67UncrashedIdenticalResubmissionParks(t *testing.T) {
	const why = "w67: the finding is a false positive, the guard already exists, so nothing changed"
	plan := append(w222BugFixHead(),
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{
			"verdict":     "fail",
			"fail_reason": "w67: the fix still does not guard the rewind round",
			"findings": []map[string]any{
				{"severity": "blocking", "detail": "w67: the guard is missing"},
			},
		}},
		// the rewound fix completes without touching the repo: the identical tree
		map[string]any{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true,
			"output": map[string]any{"summary": why}},
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		w222Env("verification", false),
	)
	capDir := w222RunPi(t, "w67: an uncrashed identical resubmission must not re-run the gates", plan)

	x := readExecution(t, capDir)
	if x.WorkflowStatus != "blocked" {
		t.Fatalf("workflow_status %q, want blocked (an identical resubmission is a human-visible result): %s",
			x.WorkflowStatus, execDigest(x))
	}
	for _, want := range []string{"gate tree unchanged", "produced no change", `"review"`, "verdict round 1", `"fix"`, why} {
		if !strings.Contains(x.BlockedReason, want) {
			t.Errorf("blocked reason misses %q: %s", want, x.BlockedReason)
		}
	}
	if strings.Contains(x.BlockedReason, "crashed with") {
		t.Errorf("no attempt crashed, yet the reason names one: %s", x.BlockedReason)
	}
	if n, _ := x.RewindStats["count"].(float64); n != 1 {
		t.Errorf("rewind_stats.count = %v, want 1 — the guard charges no extra slot (%v)", x.RewindStats["count"], x.RewindStats)
	}
	if got := strings.Join(w222Order(t, capDir), ","); got != "repro,root-cause,fix,review,fix" {
		t.Errorf("dispatch order %s, want the review rewind and the unchanged fix only — no review re-run", got)
	}
}
