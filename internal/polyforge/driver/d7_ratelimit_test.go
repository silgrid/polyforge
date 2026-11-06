package driver

// d7_ratelimit_test.go: the D7 classification and backoff ladder of
// polyforge#147 — a transient provider throttle (429 / rate limit / too
// many requests / overloaded) is NOT a model-class death; allowance and
// auth problems (quota, billing, credit, insufficient ..., 401/403) stay
// model-class. What the driver does with a throttle (the same model after
// the backoff, never counted, booked into T) is
// supervision_rules_test.go's TestTransientRetriesSameModelAfterBackoffUncounted.

import (
	"errors"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// TestD7ThrottleTextsAreRateLimitedNotModelClass: only the transient
// throttle signatures classify as the rate-limit cause (also on an
// error turn), and that cause never advances the candidate chain;
// allowance/auth texts stay model-class even next to a 429.
func TestD7ThrottleTextsAreRateLimitedNotModelClass(t *testing.T) {
	for _, text := range []string{
		"provider request failed: HTTP 429 Too Many Requests",
		"error: rate limit exceeded, retry later",
		"upstream error: the model is overloaded",
	} {
		for _, turnErr := range []bool{false, true} {
			got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: text, TurnError: turnErr})
			if got != FailRateLimited {
				t.Errorf("%q (turn error %v) classified %q, want %q", text, turnErr, got, FailRateLimited)
			}
		}
	}
	for _, text := range []string{
		"429: You exceeded your current quota",
		"insufficient credit on this account",
		"billing: credit balance is too low",
		"403 insufficient permissions",
		"401 unauthorized",
	} {
		for _, turnErr := range []bool{false, true} {
			got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: text, TurnError: turnErr})
			if got != FailExitModel {
				t.Errorf("%q (turn error %v) classified %q, want %q", text, turnErr, got, FailExitModel)
			}
		}
		if got, _, _ := classifyWaitFailure(errors.New("write prompt: broken pipe: " + text)); got != FailExitModel {
			t.Errorf("startup %q classified %q, want %q", text, got, FailExitModel)
		}
	}
	if IsModelClass(FailRateLimited) {
		t.Fatalf("a throttle must never advance the candidate pointer")
	}
	// A genuine model fault stays model-class.
	if got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: `model "x/y" not found`}); got != FailExitModel {
		t.Fatalf("model-class classification regressed: %q", got)
	}
}

// TestD7BackoffDoublesAndCaps: the transient backoff is 1s, 2s, 4s, ...
// capped at 3m (design #483 §7: "1 秒到 3 分钟").
func TestD7BackoffDoublesAndCaps(t *testing.T) {
	want := []time.Duration{
		1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, 64 * time.Second, 128 * time.Second,
		180 * time.Second, 180 * time.Second,
	}
	for i, w := range want {
		if got := rateLimitBackoff(i); got != w {
			t.Fatalf("backoff after %d throttles is %v, want %v", i, got, w)
		}
	}
	if got := rateLimitBackoff(40); got != RateLimitBackoffMax || RateLimitBackoffMax != 3*time.Minute {
		t.Fatalf("the backoff must stay capped at 3m: %v (max %v)", got, RateLimitBackoffMax)
	}
}

// TestD7RateLimitStreakResetsOnAnotherCause: the backoff ladder counts
// only the step's TRAILING consecutive transient deaths (a throttle or an
// upstream transient: one ladder, design #483 §7).
func TestD7RateLimitStreakResetsOnAnotherCause(t *testing.T) {
	att := func(cause string) capsule.Attempt {
		return capsule.Attempt{Cause: CauseInfraFailure, FailureCause: cause}
	}
	cases := []struct {
		name string
		in   []capsule.Attempt
		want int
	}{
		{"none", nil, 0},
		{"one", []capsule.Attempt{att(FailRateLimited)}, 1},
		{"three", []capsule.Attempt{att(FailRateLimited), att(FailRateLimited), att(FailRateLimited)}, 3},
		{"interrupted", []capsule.Attempt{att(FailRateLimited), att(FailExitOther), att(FailRateLimited)}, 1},
		{"trailing other", []capsule.Attempt{att(FailRateLimited), att(FailExitModel)}, 0},
		{"an upstream transient climbs the same ladder", []capsule.Attempt{att(FailUpstreamTransient), att(FailRateLimited)}, 2},
		// repo review 2026-10-16 B-22: a relaunch/restart audit entry is not
		// a dispatch outcome and must not reset the ladder
		{"across a restart", []capsule.Attempt{att(FailRateLimited), att(FailRateLimited), att("daemon_restart")}, 2},
		{"across a relaunch", []capsule.Attempt{att(FailRateLimited), att("driver_relaunch"), att(FailRateLimited)}, 2},
	}
	for _, tc := range cases {
		if got := transientStreak(tc.in); got != tc.want {
			t.Errorf("%s: streak %d, want %d", tc.name, got, tc.want)
		}
	}
}
