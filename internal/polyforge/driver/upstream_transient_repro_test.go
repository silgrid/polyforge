package driver

// upstream_transient_repro_test.go: the appended item of
// wi_seWTgHeKtaa8 (owner 2026-10-13). An upstream's OCCASIONAL return
// corruption — the #147 incident's exact error text, an
// error-ended turn whose tool-call arguments came back truncated
// ("Upstream tool call has incomplete or invalid JSON arguments") — is
// classified model-class today (classifyWaitFailure: an error turn with
// no throttle/auth text falls to FailExitModel), so the candidate
// pointer advances IMMEDIATELY: the run is re-dispatched on the next
// (most expensive) candidate with no same-model retry. Expected per the
// WI: this class of upstream transient failure retries the SAME model
// (reusing #147's D7 backoff) before any switch; only past the retry
// bound does it count as model_error.
//
// Both tests fail before the fix, for exactly the reported reason.

import (
	"context"
	"testing"
	"time"
)

// The incident's error string, verbatim (polyforge#147, code step,
// dispatch 14, glm-5.3 -> immediately switched to opus-5).
const upstreamTransientToolArgs = "Upstream tool call has incomplete or invalid JSON arguments"

// TestUpstreamTransientToolArgsErrorRetriesSameModel: ONE transient
// upstream format error, then a good dispatch. Expected: the retry runs
// on the SAME model. Observed before the fix: the chain switches to the
// next candidate on the first error.
func TestUpstreamTransientToolArgsErrorRetriesSameModel(t *testing.T) {
	env := newChainEnv(t, func(c *Config) {
		c.Sleep = func(context.Context, time.Duration) {} // the retry's backoff must not sleep the test
	}, "s", "glm-5.3", "opus-5")
	env.sp.script = []fakeEntry{
		exitErr(1, upstreamTransientToolArgs, true),
		done(`{"ok":true}`),
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	models := env.models()
	if len(models) != 2 {
		t.Fatalf("dispatched %d times, want 2 (the failed attempt + the retry)", len(models))
	}
	if models[0] != models[1] {
		t.Fatalf("REPORTED FAILURE (upstream transient): one upstream tool-call format error (%q) switched the model immediately (%q -> %q) — an upstream return corruption is not a model-class fault and must retry the SAME model first", upstreamTransientToolArgs, models[0], models[1])
	}
}

// TestUpstreamTransientRetryBound: THREE consecutive transient upstream
// format errors. Expected per the WI: the first two are same-model
// retries, the third (past the retry bound of 2) switches to the next
// candidate. Observed before the fix: the switch happens on the FIRST
// error already.
func TestUpstreamTransientRetryBound(t *testing.T) {
	env := newChainEnv(t, func(c *Config) {
		c.Sleep = func(context.Context, time.Duration) {}
	}, "s", "glm-5.3", "opus-5")
	env.sp.script = []fakeEntry{
		exitErr(1, upstreamTransientToolArgs, true),
		exitErr(1, upstreamTransientToolArgs, true),
		exitErr(1, upstreamTransientToolArgs, true),
		done(`{"ok":true}`),
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	models := env.models()
	// dispatch 0 fails, 1 and 2 are the same-model retries, dispatch 3
	// runs on the successor after the bound is exceeded.
	if len(models) != 4 {
		t.Fatalf("dispatched %d times (%v), want 4 (three transient errors + the delivery)", len(models), models)
	}
	for i := 0; i < 3; i++ {
		if models[i] != models[0] {
			t.Fatalf("REPORTED FAILURE (upstream transient): dispatch %d ran on %q after only %d transient upstream format errors — the same-model retry bound (2) was never used; the chain switched on the first error (%v)", i, models[i], i, models)
		}
	}
	if models[3] == models[0] {
		t.Fatalf("past the retry bound the transient class must count as model_error and switch candidates; dispatch 3 still ran on %q", models[3])
	}
}
