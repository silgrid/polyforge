package driver

// candidates_test.go: the B-chain recomputation and the model-class
// predicate. The pinned tables are the policy: model-class failures
// alone advance the pointer, a rewind resets it, the list end caps it —
// and host/operator/schema failures never move it.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// att is one archived infra_failure attempt of a dispatch that ran model m.
func att(cause, m string) capsule.Attempt {
	return capsule.Attempt{Cause: CauseInfraFailure, FailureCause: cause, Candidate: m}
}

// persistedAnchor emulates Step.Candidate for a fixture archive: the model
// the step's last dispatch ran (the newest stamped attempt), reset to ""
// by a rewind archive (the era reset).
func persistedAnchor(attempts []capsule.Attempt) string {
	for i := len(attempts) - 1; i >= 0; i-- {
		switch attempts[i].Cause {
		case CauseGateFailRewind, CauseGateFailRewindTextOnly, CauseGateFailRewindMechanical, CauseSupersededByRewind:
			return ""
		}
		if attempts[i].Candidate != "" {
			return attempts[i].Candidate
		}
	}
	return ""
}

func TestCandidatePointer(t *testing.T) {
	list3 := []string{"m1", "m2", "m3"}
	list2 := []string{"m1", "m2"}
	rewind := capsule.Attempt{Cause: CauseGateFailRewind}

	cases := []struct {
		name       string
		attempts   []capsule.Attempt
		candidates []string
		want       int
	}{
		{"no chain", []capsule.Attempt{att(FailExitModel, "m1")}, nil, -1},
		{"no attempts", nil, list3, 0},
		{"first model failure advances to 1", []capsule.Attempt{att(FailExitModel, "m1")}, list2, 1},
		{"the second failure ran on m2 and advances to m3",
			[]capsule.Attempt{att(FailExitModel, "m1"), att(FailExitModel, "m2")}, list3, 2},
		{"cap at last candidate",
			[]capsule.Attempt{att(FailExitModel, "m1"), att(FailExitModel, "m2"), att(FailExitModel, "m3"), att(FailExitModel, "m3")}, list3, 2},
		{"cap with a single candidate", []capsule.Attempt{att(FailExitModel, "m1")}, []string{"m1"}, 0},
		{"host failure never advances", []capsule.Attempt{att(FailExitHost, "m1")}, list2, 0},
		{"other failure never advances", []capsule.Attempt{att(FailExitOther, "m1")}, list2, 0},
		{"tool_hung failure never advances", []capsule.Attempt{att(FailToolHung, "m1")}, list2, 0},
		{"empty cause never advances", []capsule.Attempt{att("", "m1")}, list2, 0},
		{"a non-advancing failure after the advance stays on the new model",
			[]capsule.Attempt{att(FailExitModel, "m1"), att(FailToolHung, "m2")}, list3, 1},
		{"rewind resets to the head",
			[]capsule.Attempt{att(FailExitModel, "m1"), att(FailExitModel, "m2"), rewind}, list3, 0},
		{"a failure after the rewind advances from the head",
			[]capsule.Attempt{att(FailExitModel, "m1"), rewind, att(FailExitModel, "m1")}, list3, 1},
		{"unknown cause ignored",
			[]capsule.Attempt{{Cause: "something_new"}, att(FailExitModel, "m1")}, list2, 1},
		{"superseded_by_rewind resets like gate_fail_rewind",
			[]capsule.Attempt{att(FailExitModel, "m1"), {Cause: CauseSupersededByRewind}}, list2, 0},
		{"light rewind resets (a light rewind never burns a candidate)",
			[]capsule.Attempt{att(FailExitModel, "m1"), {Cause: CauseGateFailRewindTextOnly, RewindGate: "review"}}, list2, 0},
		{"mechanical light rewind resets",
			[]capsule.Attempt{att(FailExitModel, "m1"), {Cause: CauseGateFailRewindMechanical}}, list2, 0},
		// polyforge#33: identity, not index
		{"the list reshaped around the current model keeps it",
			[]capsule.Attempt{att(FailToolHung, "m2")}, []string{"x", "m2", "m1"}, 1},
		{"a model failure advances from the current model's NEW index",
			[]capsule.Attempt{att(FailExitModel, "m2")}, []string{"x", "m2", "m1"}, 2},
		{"the current model removed from the list: the new head",
			[]capsule.Attempt{att(FailToolHung, "m2")}, []string{"x", "y"}, 0},
		{"a removed model that also failed: still the new head",
			[]capsule.Attempt{att(FailExitModel, "m2")}, []string{"x", "y"}, 0},
		{"pre-upgrade attempts carry no candidate: the head",
			[]capsule.Attempt{{Cause: CauseInfraFailure, FailureCause: FailExitModel}}, list3, 0},
		// polyforge#33: the ended_without_result rotation
		{"one EWR does not advance", []capsule.Attempt{att(FailEndedWithoutResult, "m1")}, list2, 0},
		{"two EWRs on one model advance",
			[]capsule.Attempt{att(FailEndedWithoutResult, "m1"), att(FailEndedWithoutResult, "m1")}, list2, 1},
		{"two EWRs at the chain end stay on the end model",
			[]capsule.Attempt{att(FailEndedWithoutResult, "m2"), att(FailEndedWithoutResult, "m2")}, list2, 1},
		{"the owner appended a candidate after the chain-end EWRs: the pointer moves on",
			[]capsule.Attempt{att(FailEndedWithoutResult, "m2"), att(FailEndedWithoutResult, "m2")}, list3, 2},
		// polyforge#33 B1: the crash window — neutral attempts archived
		// after a switching failure keep its pending advancement
		{"switch window: daemon_restart on the dead model",
			[]capsule.Attempt{att(FailModelError, "m1"), att(FailDaemonRestart, "m1")}, list2, 1},
		{"switch window: EWR streak then daemon_restart",
			[]capsule.Attempt{att(FailEndedWithoutResult, "m1"), att(FailEndedWithoutResult, "m1"), att(FailDaemonRestart, "m1")}, list2, 1},
		{"switch window: daemon_restart then driver_relaunch",
			[]capsule.Attempt{att(FailModelError, "m1"), att(FailDaemonRestart, "m1"), att(FailDriverRelaunch, "m1")}, list2, 1},
		{"a neutral failure on the new model: the switch was made",
			[]capsule.Attempt{att(FailModelError, "m1"), att(FailRateLimited, "m2")}, list2, 1},
	}
	for _, tc := range cases {
		got := CandidatePointer(persistedAnchor(tc.attempts), tc.attempts, nil, tc.candidates)
		if got != tc.want {
			t.Errorf("%s: CandidatePointer = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestSameCandidateEwrStreak(t *testing.T) {
	ewr := func(m string) capsule.Attempt { return att(FailEndedWithoutResult, m) }
	cases := []struct {
		name     string
		attempts []capsule.Attempt
		cand     string
		answers  []capsule.ArchivedAnswer
		want     int
	}{
		{"none", nil, "a", nil, 0},
		{"two plain", []capsule.Attempt{ewr("a"), ewr("a")}, "a", nil, 2},
		{"the pi-web- interleaving: neutral causes neither count nor break",
			[]capsule.Attempt{ewr("a"), att(FailRateLimited, "a"), att(FailUpstreamTransient, "a"),
				att(FailDaemonRestart, "a"), att(FailDriverRelaunch, "a"), ewr("a")}, "a", nil, 2},
		{"another counted failure breaks", []capsule.Attempt{ewr("a"), att(FailExitOther, "a"), ewr("a")}, "a", nil, 1},
		{"a model-class failure breaks", []capsule.Attempt{ewr("a"), att(FailExitModel, "a"), ewr("a")}, "a", nil, 1},
		{"a different candidate ends the streak (a switch happened)",
			[]capsule.Attempt{ewr("a"), ewr("a"), ewr("b")}, "b", nil, 1},
		{"an accepted result breaks",
			[]capsule.Attempt{ewr("a"), {Cause: "accepted"}, ewr("a")}, "a", nil, 1},
		{"a rewind breaks",
			[]capsule.Attempt{ewr("a"), {Cause: CauseGateFailRewind}, ewr("a")}, "a", nil, 1},
		{"an answered question breaks (an earlier answer era)",
			[]capsule.Attempt{
				{Cause: CauseInfraFailure, FailureCause: FailEndedWithoutResult, Candidate: "a", StartedAt: "2026-01-01T00:00:00Z"},
				{Cause: CauseInfraFailure, FailureCause: FailEndedWithoutResult, Candidate: "a", StartedAt: "2026-01-01T00:10:00Z"}},
			"a", []capsule.ArchivedAnswer{{AnsweredAt: "2026-01-01T00:05:00Z"}}, 1},
	}
	for _, tc := range cases {
		if got := SameCandidateEwrStreak(tc.attempts, tc.cand, tc.answers); got != tc.want {
			t.Errorf("%s: streak = %d, want %d", tc.name, got, tc.want)
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
	first := CandidatePointer(persistedAnchor(attempts), attempts, nil, candidates)
	second := CandidatePointer(persistedAnchor(attempts), attempts, nil, candidates)
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

// composeOf is the test double of the runtime's dispatch-time composition:
// a fixed answer.
func composeOf(m map[string][]string) func() (map[string][]string, error) {
	return func() (map[string][]string, error) { return m, nil }
}
