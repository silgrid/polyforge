package driver

// authored_skip_test.go: a skipped authored scenario in
// the verification report blocks the automatic merge and routes the
// delivery to the human, the reason naming the skipped title — the same
// "merge condition unmet → needs_input" route as scope_adequate=false.
// An empty scenarios list (the owner-approved skipped-with-rationale
// mode) triggers nothing.

import (
	"strings"
	"testing"
)

func TestAuthoredScenarioSkipBlocksAutoMerge(t *testing.T) {
	r := deliverRepo{Name: "demo", Branch: "polyforge/polyforge-166",
		PRURL: "https://github.com/o/r/pull/1", Merged: ptrTrue(), MergeCommit: "abc", TestsPassed: ptrTrue()}
	skip := func(scenarios ...authoredScenario) ResultSpec {
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0)
		spec.VerifiedScenarios = scenarios
		return spec
	}
	t.Run("a skipped scenario blocks and names its title", func(t *testing.T) {
		spec := skip(authoredScenario{Title: "w69 authored skip probe", Status: "skipped"})
		why := mergeBlocker(spec, r, "polyforge/polyforge-166", ev(goodPush))
		if why == "" || !strings.Contains(why, "w69 authored skip probe") {
			t.Fatalf("the blocker must name the skipped scenario's title, got %q", why)
		}
	})
	t.Run("a skip with detail still blocks", func(t *testing.T) {
		spec := skip(authoredScenario{Title: "probe with detail", Status: "skipped"})
		if why := mergeBlocker(spec, r, "polyforge/polyforge-166", ev(goodPush)); why == "" {
			t.Fatal("a skipped scenario with a detail must still block the automatic merge")
		}
	})
	t.Run("passing and failing scenarios do not block", func(t *testing.T) {
		spec := skip(authoredScenario{Title: "passed one", Status: "pass"}, authoredScenario{Title: "failed one", Status: "fail"})
		if why := mergeBlocker(spec, r, "polyforge/polyforge-166", ev(goodPush)); why != "" {
			t.Fatalf("only a skipped scenario blocks, got %q", why)
		}
	})
	t.Run("the owner-approved empty list triggers nothing", func(t *testing.T) {
		spec := skip()
		if why := mergeBlocker(spec, r, "polyforge/polyforge-166", ev(goodPush)); why != "" {
			t.Fatalf("an empty scenarios list must not block, got %q", why)
		}
	})
	t.Run("the whole deliver envelope is refused until a human answers", func(t *testing.T) {
		spec := skip(authoredScenario{Title: "w69 authored skip probe", Status: "skipped"})
		err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, ev(goodPush)), spec)
		if err == nil || !strings.Contains(err.Error(), "w69 authored skip probe") || !strings.Contains(err.Error(), "needs_input") {
			t.Fatalf("the skipped scenario must refuse the completed deliver and route to the human, got %v", err)
		}
		spec.HumanAnswers = 1
		if err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, ev(goodPush)), spec); err != nil {
			t.Fatalf("after a human answer the delivery proceeds: %v", err)
		}
	})
	t.Run("scope_adequate=false keeps its own route", func(t *testing.T) {
		spec := skip(authoredScenario{Title: "w69 authored skip probe", Status: "skipped"})
		f := false
		spec.VerificationScoped = true
		spec.ReviewScopeAdequate = &f
		why := mergeBlocker(spec, r, "polyforge/polyforge-166", ev(goodPush))
		if !strings.Contains(why, "scope_adequate") {
			t.Fatalf("the scope answer keeps precedence in the same function, got %q", why)
		}
	})
}
