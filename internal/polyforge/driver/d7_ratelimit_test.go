package driver

// d7_ratelimit_test.go: the D7 semantics of polyforge#147 — a transient
// provider throttle (429 / rate limit / too many requests / overloaded)
// is NOT a model-class death. The driver waits out an exponential
// backoff and retries the SAME model, with no retry-count bound of its
// own: the step's cumulative runtime T (which the backoff waits count
// into) is the only limit. Allowance and auth problems (quota, billing,
// credit, insufficient ..., 401/403) stay model-class and switch.

import (
	"context"
	"errors"
	"sync"
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

// TestD7BackoffDoublesAndCaps: the backoff curve is 1s, 2s, 4s, ...
// capped at 300s (the spec's assumption-2 numbers).
func TestD7BackoffDoublesAndCaps(t *testing.T) {
	want := []time.Duration{
		1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, 64 * time.Second, 128 * time.Second,
		256 * time.Second, 300 * time.Second, 300 * time.Second,
	}
	for i, w := range want {
		if got := rateLimitBackoff(i); got != w {
			t.Fatalf("backoff after %d throttles is %v, want %v", i, got, w)
		}
	}
	if got := rateLimitBackoff(40); got != RateLimitBackoffMax {
		t.Fatalf("the backoff must stay capped: %v", got)
	}
}

// TestD7RateLimitStreakResetsOnAnotherCause: the backoff ladder counts
// only the step's TRAILING consecutive throttles.
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
		// repo review 2026-10-16 B-22: a relaunch/restart audit entry is not
		// a dispatch outcome and must not reset the ladder
		{"across a restart", []capsule.Attempt{att(FailRateLimited), att(FailRateLimited), att("daemon_restart")}, 2},
		{"across a relaunch", []capsule.Attempt{att(FailRateLimited), att("driver_relaunch"), att(FailRateLimited)}, 2},
	}
	for _, tc := range cases {
		if got := rateLimitStreak(tc.in); got != tc.want {
			t.Errorf("%s: streak %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestD7RateLimitRetriesSameModelWithoutCountBound: five consecutive
// throttles keep the ORIGINAL model (the chain has a successor and must
// not be touched), never block on the failure counter (five is past the
// bound of three), and each re-dispatch waits the doubled backoff.
func TestD7RateLimitRetriesSameModelWithoutCountBound(t *testing.T) {
	var mu sync.Mutex
	var waits []time.Duration
	env := newChainEnv(t, func(c *Config) {
		c.Sleep = func(_ context.Context, d time.Duration) {
			mu.Lock()
			waits = append(waits, d)
			mu.Unlock()
		}
	}, "s", "a", "b", "c")
	throttle := exitErr(1, "provider request failed: HTTP 429 Too Many Requests (rate limit)", false)
	env.sp.script = []fakeEntry{throttle, throttle, throttle, throttle, throttle, done(`{"ok":true}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	x := env.exec(t)
	if x.WorkflowStatus != StatusCompleted {
		t.Fatalf("the throttled step must survive its retries: status %q (%q)", x.WorkflowStatus, x.BlockedReason)
	}
	models := env.models()
	if len(models) != 6 {
		t.Fatalf("dispatches %v, want six (five throttled + the delivery)", models)
	}
	for i, m := range models {
		if m != models[0] {
			t.Fatalf("dispatch %d ran on %q, the first on %q — a throttle switched models", i, m, models[0])
		}
	}
	st := x.Steps["s"]
	throttles := 0
	for _, a := range st.Attempts {
		if a.Cause != CauseInfraFailure {
			continue
		}
		if a.FailureCause != FailRateLimited {
			t.Fatalf("attempt %+v, want the rate-limit cause", a)
		}
		throttles++
	}
	if throttles != 5 {
		t.Fatalf("archived throttles %d, want five (%+v)", throttles, st.Attempts)
	}
	if st.Supervision != nil && st.Supervision.FailureCount != 0 {
		t.Fatalf("a throttle must not move the blocking failure counter: %+v", st.Supervision)
	}
	// Only timeout-class deaths relax T.
	if st.Supervision != nil && st.Supervision.RelaxedWallMs != 0 {
		t.Fatalf("a throttle relaxed T to %dms", st.Supervision.RelaxedWallMs)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	if len(waits) != len(want) {
		t.Fatalf("backoff waits %v, want %v", waits, want)
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("backoff %d is %v, want %v", i, waits[i], want[i])
		}
	}
}

// TestD7RateLimitDispositionIsSameModelRetry: the step_failed event of
// a throttle reports retry_same_model (no from/to pair) and carries the
// backoff it imposed.
func TestD7RateLimitDispositionIsSameModelRetry(t *testing.T) {
	env := newChainEnv(t, func(c *Config) {
		c.Sleep = func(context.Context, time.Duration) {}
	}, "s", "a", "b")
	env.sp.script = []fakeEntry{
		exitErr(1, "429 Too Many Requests", false),
		done(`{"ok":true}`),
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ev := env.log.byKind("step_failed")
	if len(ev) != 1 {
		t.Fatalf("step_failed events: %d", len(ev))
	}
	body := ev[0].body
	if body["cause"] != FailRateLimited {
		t.Fatalf("cause %v, want %q", body["cause"], FailRateLimited)
	}
	if body["disposition"] != "retry_same_model" {
		t.Fatalf("disposition %v, want retry_same_model", body["disposition"])
	}
	if _, ok := body["to_model"]; ok {
		t.Fatalf("a throttle must not switch candidates: %v", body)
	}
	if ms, _ := body["backoff_ms"].(int64); ms != RateLimitBackoffBase.Milliseconds() {
		t.Fatalf("backoff_ms %v, want %d", body["backoff_ms"], RateLimitBackoffBase.Milliseconds())
	}
}

// TestD7AllowanceAndAuthFailuresSwitchCandidates: "exceeded your current
// quota" and "403 insufficient permissions" are not throttles — another
// model/provider may work, so they advance the candidate chain with no
// backoff.
func TestD7AllowanceAndAuthFailuresSwitchCandidates(t *testing.T) {
	for _, text := range []string{
		"429 You exceeded your current quota, please check your plan and billing details",
		"403 Forbidden: insufficient permissions for this model",
	} {
		var slept []time.Duration
		env := newChainEnv(t, func(c *Config) {
			c.Sleep = func(_ context.Context, d time.Duration) { slept = append(slept, d) }
		}, "s", "a", "b")
		env.sp.script = []fakeEntry{exitErr(1, text, true), done(`{"ok":true}`)}
		if err := env.d.Run(context.Background()); err != nil {
			t.Fatalf("%q: Run: %v", text, err)
		}
		if m := env.models(); len(m) != 2 || m[0] == m[1] {
			t.Fatalf("%q must switch candidates: dispatched %v", text, m)
		}
		ev := env.log.byKind("step_failed")
		if len(ev) != 1 || ev[0].body["cause"] != FailExitModel || ev[0].body["disposition"] != "switch_model" {
			t.Fatalf("%q: step_failed %v, want a model-class switch", text, ev)
		}
		if len(slept) != 0 {
			t.Fatalf("%q: a candidate switch must not back off: %v", text, slept)
		}
	}
}

// TestD7BackoffCountsIntoT: the owner bounds same-model retries only by
// the step's cumulative T, so every backoff wait is booked into the
// step's consumed runtime (and thus into the next dispatch's prior
// runtime) — three throttles add at least 1s+2s+4s.
func TestD7BackoffCountsIntoT(t *testing.T) {
	env := newChainEnv(t, func(c *Config) {
		c.Sleep = func(context.Context, time.Duration) {}
	}, "s", "a", "b")
	throttle := exitErr(1, "429 Too Many Requests", false)
	env.sp.script = []fakeEntry{throttle, throttle, throttle, done(`{"ok":true}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := (1*time.Second + 2*time.Second + 4*time.Second).Milliseconds()
	if got := env.exec(t).Steps["s"].BudgetConsumed.WallMs; got < want {
		t.Fatalf("step T %dms, want the backoff waits (>= %dms) counted in", got, want)
	}
}
