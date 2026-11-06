package driver

// candidates_test.go: the B-chain recomputation and the model-class
// predicate. The pinned tables are the policy: model-class failures
// alone advance the pointer, a rewind resets it, the list end caps it —
// and host/operator/schema failures never move it.

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// att is one archived infra_failure attempt of a dispatch that ran model m.
func att(cause, m string) capsule.Attempt {
	return capsule.Attempt{Cause: CauseInfraFailure, FailureCause: cause, Candidate: m}
}

// persistedAnchor emulates Step.Candidate for a fixture archive: the model
// the step's last dispatch ran (the newest stamped attempt), reset to ""
// by a rewind archive (the era reset — the driver writes it for a gate or
// a superseded step; a resuming rewind target keeps its anchor,
// TestRewindResumeKeepsTheCandidate).
func persistedAnchor(attempts []capsule.Attempt) string {
	for i := len(attempts) - 1; i >= 0; i-- {
		switch attempts[i].Cause {
		case CauseGateFailRewind, CauseSupersededByRewind:
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
		// contract 4 (design #483 §7): an EWR never rotates
		{"one EWR does not advance", []capsule.Attempt{att(FailEndedWithoutResult, "m1")}, list2, 0},
		{"two EWRs on one model do not advance either",
			[]capsule.Attempt{att(FailEndedWithoutResult, "m1"), att(FailEndedWithoutResult, "m1")}, list2, 0},
		// polyforge#33 B1: the crash window — neutral attempts archived
		// after a switching failure keep its pending advancement
		{"switch window: daemon_restart on the dead model",
			[]capsule.Attempt{att(FailModelError, "m1"), att(FailDaemonRestart, "m1")}, list2, 1},
		{"switch window: daemon_restart then driver_relaunch",
			[]capsule.Attempt{att(FailModelError, "m1"), att(FailDaemonRestart, "m1"), att(FailDriverRelaunch, "m1")}, list2, 1},
		{"a neutral failure on the new model: the switch was made",
			[]capsule.Attempt{att(FailModelError, "m1"), att(FailRateLimited, "m2")}, list2, 1},
		// contract 4: every model-class death advances at once, model_error
		// included (no same-candidate resume of a 5xx)
		{"one model_error advances", []capsule.Attempt{att(FailModelError, "m1")}, list2, 1},
		{"a model_error on the switched-to model advances again",
			[]capsule.Attempt{att(FailModelError, "m1"), att(FailModelError, "m2")}, list3, 2},
		{"every other model-class cause advances at once",
			[]capsule.Attempt{att(FailModelHung, "m1")}, list2, 1},
		{"a model_error at the chain end stays on the end model",
			[]capsule.Attempt{att(FailModelError, "m2")}, list2, 1},
		{"the owner appended a candidate after the chain-end death: the pointer moves on",
			[]capsule.Attempt{att(FailModelError, "m2")}, list3, 2},
		{"a counted non-model failure after the death: the switch was made",
			[]capsule.Attempt{att(FailModelError, "m1"), att(FailExitOther, "m2")}, list3, 1},
	}
	for _, tc := range cases {
		got := CandidatePointer(persistedAnchor(tc.attempts), tc.attempts, tc.candidates)
		if got != tc.want {
			t.Errorf("%s: CandidatePointer = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestClassStreak pins the supervision streaks' ledger (classStreak,
// design #483 §7): the trailing consecutive deaths of one class within the
// streak era — across candidates (a contract 4 streak is the step's, not
// a model's) — with the transient and audit-only entries skipped.
func TestClassStreak(t *testing.T) {
	ewr := func(m string) capsule.Attempt { return att(FailEndedWithoutResult, m) }
	isEwr := func(c string) bool { return c == FailEndedWithoutResult }
	cases := []struct {
		name     string
		attempts []capsule.Attempt
		in       func(string) bool
		era      time.Time
		want     int
	}{
		{"none", nil, isEwr, time.Time{}, 0},
		{"two plain", []capsule.Attempt{ewr("a"), ewr("a")}, isEwr, time.Time{}, 2},
		{"the pi-web- interleaving: transient and audit-only entries neither count nor break",
			[]capsule.Attempt{ewr("a"), att(FailRateLimited, "a"), att(FailUpstreamTransient, "a"),
				att(FailDaemonRestart, "a"), att(FailDriverRelaunch, "a"), ewr("a")}, isEwr, time.Time{}, 2},
		{"another class breaks", []capsule.Attempt{ewr("a"), att(FailExitOther, "a"), ewr("a")}, isEwr, time.Time{}, 1},
		{"a model-class failure breaks", []capsule.Attempt{ewr("a"), att(FailExitModel, "a"), ewr("a")}, isEwr, time.Time{}, 1},
		{"a different candidate does not end it", []capsule.Attempt{ewr("a"), ewr("a"), ewr("b")}, isEwr, time.Time{}, 3},
		{"an accepted result breaks",
			[]capsule.Attempt{ewr("a"), {Cause: "accepted"}, ewr("a")}, isEwr, time.Time{}, 1},
		{"a rewind breaks",
			[]capsule.Attempt{ewr("a"), {Cause: CauseGateFailRewind}, ewr("a")}, isEwr, time.Time{}, 1},
		{"the owner's continue breaks (an earlier era)",
			[]capsule.Attempt{
				{Cause: CauseInfraFailure, FailureCause: FailEndedWithoutResult, Candidate: "a", StartedAt: "2026-01-01T00:00:00Z"},
				{Cause: CauseInfraFailure, FailureCause: FailEndedWithoutResult, Candidate: "a", StartedAt: "2026-01-01T00:10:00Z"}},
			isEwr, time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC), 1},
		{"the other class counts every other cause together",
			[]capsule.Attempt{att(FailExitHost, "a"), att(FailHarnessSilent, "a"), att(FailExitOther, "a")}, otherCause, time.Time{}, 3},
		{"the other class excludes an EWR, a timeout and a model-class death",
			[]capsule.Attempt{att(FailExitOther, "a"), att(FailToolHung, "a")}, otherCause, time.Time{}, 0},
	}
	for _, tc := range cases {
		if got := classStreak(tc.attempts, tc.era, tc.in); got != tc.want {
			t.Errorf("%s: streak = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestModelClassStreak(t *testing.T) {
	me := func(m string) capsule.Attempt { return att(FailModelError, m) }
	cases := []struct {
		name     string
		attempts []capsule.Attempt
		cand     string
		era      time.Time
		want     int
	}{
		{"none", nil, "a", time.Time{}, 0},
		{"one", []capsule.Attempt{me("a")}, "a", time.Time{}, 1},
		{"any model-class causes count together", []capsule.Attempt{att(FailExitModel, "a"), me("a")}, "a", time.Time{}, 2},
		{"neutral causes neither count nor break",
			[]capsule.Attempt{me("a"), att(FailRateLimited, "a"), att(FailUpstreamTransient, "a"),
				att(FailDaemonRestart, "a"), att(FailDriverRelaunch, "a"), me("a")}, "a", time.Time{}, 2},
		{"an EWR breaks", []capsule.Attempt{me("a"), att(FailEndedWithoutResult, "a"), me("a")}, "a", time.Time{}, 1},
		{"another candidate ends the streak", []capsule.Attempt{me("a"), me("b")}, "b", time.Time{}, 1},
		{"an accepted result breaks", []capsule.Attempt{me("a"), {Cause: "accepted"}, me("a")}, "a", time.Time{}, 1},
		{"the owner's continue breaks (an earlier era)",
			[]capsule.Attempt{
				{Cause: CauseInfraFailure, FailureCause: FailModelError, Candidate: "a", StartedAt: "2026-01-01T00:00:00Z"},
				{Cause: CauseInfraFailure, FailureCause: FailModelError, Candidate: "a", StartedAt: "2026-01-01T00:10:00Z"}},
			"a", time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC), 1},
	}
	for _, tc := range cases {
		if got := ModelClassStreak(tc.attempts, tc.cand, tc.era); got != tc.want {
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
	first := CandidatePointer(persistedAnchor(attempts), attempts, candidates)
	second := CandidatePointer(persistedAnchor(attempts), attempts, candidates)
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
