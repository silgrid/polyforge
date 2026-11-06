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
// The test failed before the fix, for exactly the reported reason. (What
// the driver does with an upstream_transient is supervision_rules_test.go's.)

import (
	"testing"

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
