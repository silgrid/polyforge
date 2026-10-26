package driver

// candidates_test.go: the B-chain recomputation and the model-class
// predicate. The pinned tables are the policy: model-class failures
// alone advance the pointer, a rewind resets it, the list end caps it —
// and host/operator/schema failures never move it.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestCandidatePointer(t *testing.T) {
	prov := func() capsule.Attempt {
		return capsule.Attempt{Cause: CauseInfraFailure, FailureCause: FailExitModel}
	}
	cls := func(c string) capsule.Attempt {
		return capsule.Attempt{Cause: CauseInfraFailure, FailureCause: c}
	}
	rewind := capsule.Attempt{Cause: CauseGateFailRewind}

	cases := []struct {
		name       string
		attempts   []capsule.Attempt
		candidates []string
		want       int
	}{
		{"no chain", []capsule.Attempt{prov(), prov()}, nil, -1},
		{"no attempts", nil, []string{"m1", "m2", "m3"}, 0},
		{"first model failure advances to 1", []capsule.Attempt{prov()}, []string{"m1", "m2"}, 1},
		{"two model failures advance to 2", []capsule.Attempt{prov(), prov()}, []string{"m1", "m2", "m3"}, 2},
		{"cap at last candidate", []capsule.Attempt{prov(), prov(), prov(), prov(), prov()},
			[]string{"m1", "m2", "m3"}, 2},
		{"cap with a single candidate", []capsule.Attempt{prov(), prov()}, []string{"m1"}, 0},
		{"host failure never advances", []capsule.Attempt{cls(FailExitHost)}, []string{"m1", "m2"}, 0},
		{"other failure never advances", []capsule.Attempt{cls(FailExitOther)}, []string{"m1", "m2"}, 0},
		{"tool_hung failure never advances", []capsule.Attempt{cls(FailToolHung)}, []string{"m1", "m2"}, 0},
		{"empty cause never advances", []capsule.Attempt{cls("")}, []string{"m1", "m2"}, 0},
		{"mixed classes count model-class only",
			[]capsule.Attempt{cls(FailExitHost), prov(), cls(FailToolHung), prov(), cls(FailExitOther), prov()},
			[]string{"m1", "m2", "m3", "m4"}, 3},
		{"rewind resets the count",
			[]capsule.Attempt{prov(), prov(), rewind, prov()},
			[]string{"m1", "m2", "m3"}, 1},
		{"rewind to head with no later failures",
			[]capsule.Attempt{prov(), prov(), prov(), rewind},
			[]string{"m1", "m2"}, 0},
		{"non-advancing failures after rewind keep the head",
			[]capsule.Attempt{prov(), rewind, cls(FailExitHost), cls(FailExitOther)},
			[]string{"m1", "m2"}, 0},
		{"unknown cause ignored",
			[]capsule.Attempt{{Cause: "something_new"}, prov()},
			[]string{"m1", "m2"}, 1},
		{"superseded_by_rewind resets like gate_fail_rewind",
			[]capsule.Attempt{prov(), prov(), {Cause: CauseSupersededByRewind}, prov()},
			[]string{"m1", "m2", "m3"}, 1},
		{"superseded_by_rewind to head with no later failures",
			[]capsule.Attempt{prov(), prov(), {Cause: CauseSupersededByRewind}},
			[]string{"m1", "m2"}, 0},
		{"rewind archive still resets",
			[]capsule.Attempt{prov(), capsule.Attempt{Cause: CauseGateFailRewind, FailureCause: FailExitModel}, prov()},
			[]string{"m1", "m2"}, 1},
		// polyforge#222: the light rewind archive resets like every rewind
		// archive — a light rewind never burns a candidate (INV-1g).
		{"light rewind resets the count",
			[]capsule.Attempt{prov(), prov(), {Cause: CauseGateFailRewindTextOnly, RewindGate: "review"}, prov()},
			[]string{"m1", "m2", "m3"}, 1},
		{"light rewind to head with no later failures",
			[]capsule.Attempt{prov(), prov(), {Cause: CauseGateFailRewindTextOnly}},
			[]string{"m1", "m2"}, 0},
		{"light rewind archive still resets",
			[]capsule.Attempt{prov(), capsule.Attempt{Cause: CauseGateFailRewindTextOnly, FailureCause: FailExitModel}, prov()},
			[]string{"m1", "m2"}, 1},
	}
	for _, tc := range cases {
		got := CandidatePointer(tc.attempts, tc.candidates)
		if got != tc.want {
			t.Errorf("%s: CandidatePointer = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestCandidatePointerIsPure(t *testing.T) {
	// The recomputation must derive the same ordinal from the same
	// history twice — and must not mutate its input.
	attempts := []capsule.Attempt{
		{Cause: CauseInfraFailure, FailureCause: FailExitModel},
		{Cause: CauseGateFailRewind},
		{Cause: CauseInfraFailure, FailureCause: FailExitModel},
	}
	candidates := []string{"m1", "m2", "m3"}
	first := CandidatePointer(attempts, candidates)
	second := CandidatePointer(attempts, candidates)
	if first != second {
		t.Fatalf("recomputation drifted: %d then %d", first, second)
	}
	if attempts[1].FailureCause != "" || attempts[0].Cause != CauseInfraFailure {
		t.Fatalf("CandidatePointer must not mutate its input: %+v", attempts)
	}
}

func TestIsModelClass(t *testing.T) {
	for _, c := range []string{FailModelError, FailModelHung, FailStepTimeoutSlowModel, FailExitModel} {
		if !IsModelClass(c) {
			t.Errorf("%q must be model-class", c)
		}
	}
	for _, c := range []string{"", FailExitHost, FailExitOther, FailToolHung, FailEndedWithoutResult, FailHarnessSilent, FailStepTimeoutLongTask, "daemon_restart", "rate limit hit"} {
		if IsModelClass(c) {
			t.Errorf("%q must not be model-class", c)
		}
	}
}
