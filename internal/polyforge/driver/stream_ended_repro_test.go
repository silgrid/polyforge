package driver

// stream_ended_repro_test.go: the repro of polyforge#202
// (2026-10-18). An internal-gateway upstream's response stream can end before
// the completion event — the incident's verbatim turn error text is
// "Upstream response stream ended before completion" — and the
// supervisor's rule-1 verdict for the idle error turn is model_error.
// classifyText's rows do not recognize this phrasing (no
// reset/refused/dropped/closed/aborted/eof word, no "incomplete
// JSON"), so verdictCause keeps model_error: the death is treated as
// model-class, burns a candidate (candidate_switched) and restarts
// the WHOLE step from scratch — 6 step deaths on the internal gateway all hit
// this, twice re-running polyforge#199's ~20-minute verification bar.
// Expected per the WI: this error shape is an upstream transient
// like the stream-drop rows supervision.go already backs off — a
// same-model D7 backoff retry, bounded at UpstreamTransientRetryBound.
//
// Both tests fail before the fix, for exactly the reported reason.

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// The incident's error string, verbatim (the internal gateway, 2026-10-18; the pi
// rpc turn ends stopReason=error with this errorMessage, the process
// stays alive and idles, and rule 1 judges model_error).
const streamEndedBeforeCompletion = "Upstream response stream ended before completion"

// TestStreamEndedBeforeCompletionClassifiesTransient: the incident's
// text — on an error turn (the live rpc path) and on an exited
// process's last error alike — classifies as the upstream-transient
// cause, which never advances the candidate chain.
func TestStreamEndedBeforeCompletionClassifiesTransient(t *testing.T) {
	for _, turnErr := range []bool{false, true} {
		got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: streamEndedBeforeCompletion, TurnError: turnErr})
		if got != FailUpstreamTransient {
			t.Errorf("REPORTED FAILURE (polyforge#202): %q (turn error %v) classified %q, want %q — an upstream stream that ends before completion is an upstream transient, not a model fault", streamEndedBeforeCompletion, turnErr, got, FailUpstreamTransient)
		}
	}
	// the live path's shape: rule 1 hands down a model_error verdict
	// with the incident text as the turn error; verdictCause must
	// re-classify it into the backoff-retry class.
	if got := verdictCause(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: streamEndedBeforeCompletion}); got != FailUpstreamTransient {
		t.Errorf("REPORTED FAILURE (polyforge#202): verdictCause(model_error, %q) = %q, want %q — the live error turn burns a candidate instead of backing off", streamEndedBeforeCompletion, got, FailUpstreamTransient)
	}
}

// TestStreamEndedBeforeCompletionLiveTurnRetriesSameModel: ONE live
// error turn carrying the incident's text, then a delivering dispatch.
// Expected: the retry runs on the SAME model after the D7 backoff
// (booked into T), the blocking counter untouched, the attempt
// archived upstream_transient. Observed before the fix: the candidate
// switches on the first error and the step restarts from scratch.
func TestStreamEndedBeforeCompletionLiveTurnRetriesSameModel(t *testing.T) {
	env, waits := runLive(t, []liveCase{
		{name: "stream-ended-before-completion", errMsg: streamEndedBeforeCompletion, retries: 0, cause: FailUpstreamTransient, same: true},
	})
	models := env.models()
	if len(models) != 2 {
		t.Fatalf("dispatched %v, want two (the error turn + the delivery)", models)
	}
	if models[0] != models[1] {
		t.Fatalf("REPORTED FAILURE (polyforge#202): one live error turn %q switched the model immediately (%q -> %q) and restarted the step from scratch — an upstream stream drop must retry the SAME model first", streamEndedBeforeCompletion, models[0], models[1])
	}
	ev := env.log.byKind("step_failed")
	if len(ev) != 1 {
		t.Fatalf("step_failed events %d, want 1", len(ev))
	}
	body := ev[0].body
	if body["cause"] != FailUpstreamTransient {
		t.Fatalf("REPORTED FAILURE (polyforge#202): live error turn %q recorded cause %v, want %q (step_failed %v)", streamEndedBeforeCompletion, body["cause"], FailUpstreamTransient, body)
	}
	if body["disposition"] != "retry_same_model" {
		t.Fatalf("REPORTED FAILURE (polyforge#202): disposition %v, want retry_same_model", body["disposition"])
	}
	if base := supervise.DefaultRetryBackoffBase; len(waits) != 1 || waits[0] != base {
		t.Fatalf("REPORTED FAILURE (polyforge#202): backoff waits %v, want one %v (the same-model retry's first retry-pacing backoff)", waits, base)
	}
	st := env.exec(t).Steps["s"]
	if st.Supervision != nil && st.Supervision.FailureCount != 0 {
		t.Fatalf("REPORTED FAILURE (polyforge#202): the same-model retry moved the blocking counter: %+v", st.Supervision)
	}
	if got := st.BudgetConsumed.WallMs; got < supervise.DefaultRetryBackoffBase.Milliseconds() {
		t.Fatalf("step T %dms, want the %v backoff booked in", got, supervise.DefaultRetryBackoffBase)
	}
	_ = time.Second // keep time imported for the Waits/RateLimitBackoffBase comparisons' readability
}
