package driver

// gateway_transient_test.go: the gateway-layer transient classes of
// polyforge#34. Five real daemon-event texts (verbatim samples, the
// ticket's background) all classified model_error before the fix: the
// daemon switched the candidate at once and opened a fresh session,
// losing the interrupted session's progress for failures a same-model
// backoff retry survives. The samples:
//
//   - "Upstream response stream was interrupted" (39 historical events:
//     the recognized "ended before completion" shape, verb not in list)
//   - a gateway 502 "Upstream service temporarily unavailable"
//   - a gateway 503 "No available accounts" (its upstream pool is dry)
//   - a gateway "server overload. Please retry later." text
//   - "The upstream model reported an error after output had started;
//     the output delivered so far is partial." (polyforge#33's incident:
//     the turn had already delivered partial output when the upstream
//     errored)
//
// What they pin: the widening stays inside the existing subjects — a
// text that names the MODEL (no upstream/gateway/service subject) keeps
// the model classes, and the already-recognized samples keep theirs.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// gatewayTransientSamples are the ticket's five texts, verbatim.
var gatewayTransientSamples = []struct {
	text string
	want string
}{
	{`Upstream response stream was interrupted`, FailUpstreamTransient},
	{`502: {"message":"Upstream service temporarily unavailable","type":"upstream_error"}`, FailUpstreamTransient},
	{`503 {"error":{"message":"No available accounts: no available accounts","type":"api_error"},"type":"error"}`, FailUpstreamTransient},
	{`The service is currently unable to handle additional requests due to server overload. Please retry later. Request id: 20261114172154000123456789`, FailRateLimited},
	{`The upstream model reported an error after output had started; the output delivered so far is partial.`, FailUpstreamTransient},
}

// TestGatewayTransientSamplesClassifyTransient: each of the five
// verbatim gateway texts classifies as its transient cause on BOTH
// paths — the exit path (classifyWaitFailure, also as a live turn's
// error text) and the live path (verdictCause) — and the transient
// causes never advance the candidate chain.
func TestGatewayTransientSamplesClassifyTransient(t *testing.T) {
	for _, s := range gatewayTransientSamples {
		for _, turnErr := range []bool{false, true} {
			got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: s.text, TurnError: turnErr})
			if got != s.want {
				t.Errorf("%q (turn error %v) classified %q, want %q", s.text, turnErr, got, s.want)
			}
		}
		live := verdictCause(supervise.StepDead{Step: "fix", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: s.text})
		if live != s.want {
			t.Errorf("the live path classified %q as %q, want %q (the exit path's class)", s.text, live, s.want)
		}
		if IsModelClass(s.want) {
			t.Errorf("%q: %q must never advance the candidate pointer", s.text, s.want)
		}
	}
	// the exit-only rows never classify the driver's own failure: a
	// stream drop in a local read is a local I/O error, not transient.
	for _, s := range gatewayTransientSamples {
		if s.want != FailUpstreamTransient {
			continue
		}
		if got, _, _ := classifyWaitFailure(errWithText("read transcript: " + s.text)); got == FailUpstreamTransient {
			t.Errorf("driver-local %q classified %q, want not transient", s.text, got)
		}
	}
}

// TestGatewayWideningKeepsTheEstablishedClasses: the widening keeps the
// recognized samples' classes (AC2) and keeps model-subject texts model
// class (AC3): it takes nothing the old table did not already hand to
// the model classes.
func TestGatewayWideningKeepsTheEstablishedClasses(t *testing.T) {
	// exit path (a live turn's error text) and live path, one want per
	// text; the gateway-502 forbidden shape keeps the fallback classes
	// it always had: exit_model_class on exit, model_error live. The
	// modelClass rows assert the candidate switch; the transient/rate
	// rows keep their classes (AC2), the model-subject rows keep theirs
	// (AC3).
	for _, tc := range []struct {
		text       string
		exitWant   string
		liveWant   string
		modelClass bool
	}{
		{`Upstream response stream ended before completion`, FailUpstreamTransient, FailUpstreamTransient, false},
		{`429 {"type":"error","error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit"}}`, FailRateLimited, FailRateLimited, false},
		{`529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, FailRateLimited, FailRateLimited, false},
		{`当前模型并发数过高，请稍后再试`, FailRateLimited, FailRateLimited, false},
		{`402 {"error":{"message":"Insufficient balance","type":"insufficient_balance"}}`, FailExitModel, FailExitModel, true},
		{`401 {"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`, FailExitModel, FailExitModel, true},
		{`Upstream access forbidden, please contact administrator`, FailExitModel, FailModelError, true},
		// no upstream/gateway subject: the model's own texts stay model
		// class — the widening must not swallow them
		{`the model ended the turn`, FailExitModel, FailModelError, true},
		{`The model is temporarily unavailable, please try again`, FailExitModel, FailModelError, true},
	} {
		got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: tc.text, TurnError: true})
		if got != tc.exitWant {
			t.Errorf("%q classified %q on the exit path, want %q", tc.text, got, tc.exitWant)
		}
		live := verdictCause(supervise.StepDead{Step: "fix", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: tc.text})
		if live != tc.liveWant {
			t.Errorf("%q classified %q on the live path, want %q", tc.text, live, tc.liveWant)
		}
		if tc.modelClass && !IsModelClass(live) {
			t.Errorf("%q: the model classes must keep switching the candidate, got %q", tc.text, live)
		}
	}
}
