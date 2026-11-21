package driver

// gateway_transient_test.go: the gateway-layer transient classes of
// polyforge#34 and polyforge#210. The real daemon-event texts (verbatim
// samples, the tickets' backgrounds) all classified model_error before
// their fixes: the
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
//   - a gateway 503 "Service temporarily unavailable" (polyforge#147's
//     glm text: the service subject directly before "temporarily
//     unavailable", never the 503 status alone)
//
// What they pin: the widening stays inside the existing subjects — a
// text that names the MODEL (no upstream/gateway/service subject) keeps
// the model classes, and the already-recognized samples keep theirs.

import (
	"regexp"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// gatewayTransientSamples are the tickets' texts, verbatim.
var gatewayTransientSamples = []struct {
	text string
	want string
}{
	{`Upstream response stream was interrupted`, FailUpstreamTransient},
	{`502: {"message":"Upstream service temporarily unavailable","type":"upstream_error"}`, FailUpstreamTransient},
	{`503 {"error":{"message":"No available accounts: no available accounts","type":"api_error"},"type":"error"}`, FailUpstreamTransient},
	{`The service is currently unable to handle additional requests due to server overload. Please retry later. Request id: 20261114172154000123456789`, FailRateLimited},
	{`The upstream model reported an error after output had started; the output delivered so far is partial.`, FailUpstreamTransient},
	{serviceUnavailable503Body, FailUpstreamTransient},
}

// serviceUnavailable503Body is polyforge#147's gateway text, verbatim
// (the glm-5.3 line, four dispatches, each judged model_error and
// switched to the next candidate): a gateway 503 naming the service
// "temporarily unavailable". The builtin row classifies it
// upstream_transient (polyforge#210) — the owner held the same shape with
// a Host-configured transient_error_pattern until the builtin landed.
const serviceUnavailable503Body = `503: {"message":"Service temporarily unavailable","type":"api_error"}`

// TestGatewayTransientSamplesClassifyTransient: each of the verbatim
// gateway texts classifies as its transient cause on BOTH
// paths — the exit path (classifyWaitFailure, also as a live turn's
// error text) and the live path (verdictCause) — and the transient
// causes never advance the candidate chain.
func TestGatewayTransientSamplesClassifyTransient(t *testing.T) {
	for _, s := range gatewayTransientSamples {
		for _, turnErr := range []bool{false, true} {
			got, _, _, _, _ := classifyWaitFailureWith(&ExitedError{ExitCode: 1, LastError: s.text, TurnError: turnErr}, nil)
			if got != s.want {
				t.Errorf("%q (turn error %v) classified %q, want %q", s.text, turnErr, got, s.want)
			}
		}
		live, _ := verdictCauseWith(supervise.StepDead{Step: "fix", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: s.text}, nil)
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
		if got, _, _, _, _ := classifyWaitFailureWith(errWithText("read transcript: "+s.text), nil); got == FailUpstreamTransient {
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
		// polyforge#210's counter-examples: the service subject must sit
		// directly before "temporarily unavailable" (an inserted word does
		// not count, and "The model service is temporarily unavailable"
		// keeps the model classes), the 503 status alone never counts, the
		// allowance/auth row still wins, and an overload text stays
		// rate_limited (its row is judged first).
		{`The model service is temporarily unavailable`, FailExitModel, FailModelError, true},
		{`503: {"message":"Service temporarily unavailable: insufficient balance","type":"api_error"}`, FailExitModel, FailExitModel, true},
		{`401 {"message":"Service temporarily unavailable: invalid api key"}`, FailExitModel, FailExitModel, true},
		{`402 Payment Required: Service temporarily unavailable`, FailExitModel, FailExitModel, true},
		{`503 Service Unavailable`, FailExitModel, FailModelError, true},
		{`Service is temporarily unavailable`, FailExitModel, FailModelError, true},
		{`Service temporarily unavailable due to overload`, FailRateLimited, FailRateLimited, false},
	} {
		got, _, _, _, _ := classifyWaitFailureWith(&ExitedError{ExitCode: 1, LastError: tc.text, TurnError: true}, nil)
		if got != tc.exitWant {
			t.Errorf("%q classified %q on the exit path, want %q", tc.text, got, tc.exitWant)
		}
		live, _ := verdictCauseWith(supervise.StepDead{Step: "fix", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: tc.text}, nil)
		if live != tc.liveWant {
			t.Errorf("%q classified %q on the live path, want %q", tc.text, live, tc.liveWant)
		}
		if tc.modelClass && !IsModelClass(live) {
			t.Errorf("%q: the model classes must keep switching the candidate, got %q", tc.text, live)
		}
	}
}

// TestServiceTemporarilyUnavailableIsUpstreamTransient: a gateway's
// "Service temporarily unavailable" text — polyforge#147's 503 body, its
// rule-1 Evidence wrapper and nginx's uppercase title form — is
// upstream_transient on the builtin row (classified_by builtin): the same
// model retried with the backoff, never counted, never advancing the
// candidate chain. The Host-configured transient_error_pattern the owner
// ran until the builtin landed classifies the same text with the same
// cause (only the recorded source differs), and the driver's own failure
// carrying the same text stays exit_other (the exit-only rows never
// judge it).
func TestServiceTemporarilyUnavailableIsUpstreamTransient(t *testing.T) {
	wrapped := "turn ended with an error 1m0s ago and no `pf step handoff` followed within the 1m0s grace: " + serviceUnavailable503Body
	for _, x := range []string{
		serviceUnavailable503Body,
		wrapped,
		`<html><head><title>503 Service Temporarily Unavailable</title></head></html>`,
	} {
		if cause, src := classifyTextWith(x, true, nil); cause != FailUpstreamTransient || src != ClassifiedByBuiltin {
			t.Errorf("%q classified (%q, %q), want (%q, %q)", x, cause, src, FailUpstreamTransient, ClassifiedByBuiltin)
		}
		got, _, _, src, _ := classifyWaitFailureWith(&ExitedError{ExitCode: 1, LastError: x, TurnError: true}, nil)
		if got != FailUpstreamTransient || src != ClassifiedByBuiltin {
			t.Errorf("%q exit path classified (%q, %q), want (%q, %q)", x, got, src, FailUpstreamTransient, ClassifiedByBuiltin)
		}
		live, src := verdictCauseWith(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: x}, nil)
		if live != FailUpstreamTransient || src != ClassifiedByBuiltin {
			t.Errorf("%q live path classified (%q, %q), want (%q, %q)", x, live, src, FailUpstreamTransient, ClassifiedByBuiltin)
		}
	}
	if IsModelClass(FailUpstreamTransient) {
		t.Errorf("%q must never advance the candidate pointer", FailUpstreamTransient)
	}
	// the Host rule the owner configured (polyforge#210's interim, the
	// same regex verbatim) answers the same cause for this 503 text —
	// only the recorded source differs (added, not builtin). Not proof
	// that deleting the Host pattern changes no verdict (r1#5): it sits
	// before the rate-limit row, so a text that also hits a throttle
	// signature (an overload conjunction) classifies upstream_transient
	// under it, rate_limited under the builtin alone — both transient,
	// the same disposition and backoff ladder.
	rules := &TextRules{Transient: []*regexp.Regexp{regexp.MustCompile(`(?i)\bservice\W+temporarily\W+unavailable\b`)}}
	if cause, src := classifyTextWith(serviceUnavailable503Body, true, rules); cause != FailUpstreamTransient || src != ClassifiedByAdded {
		t.Errorf("with the Host rule the 503 body classified (%q, %q), want (%q, %q)", cause, src, FailUpstreamTransient, ClassifiedByAdded)
	}
	// the exit-only rows never judge the driver's own failure: the same
	// text in a local read is exit_other, not a transient.
	if got, _, _, _, _ := classifyWaitFailureWith(errWithText("read transcript: "+serviceUnavailable503Body), nil); got != FailExitOther {
		t.Errorf("driver-local 503 body classified %q, want %q", got, FailExitOther)
	}
}
