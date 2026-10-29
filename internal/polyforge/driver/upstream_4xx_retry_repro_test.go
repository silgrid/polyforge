package driver

// upstream_4xx_retry_repro_test.go: the repro of polyforge#227 — the
// polyforge#222 incident (2026-10-19, e2e-author, glm candidate). The
// gateway answered the model request with a 400 whose body says BOTH
// that the failure came from upstream and that the caller should retry
// — the incident's verbatim text is "400: The model request failed due
// to an upstream error. Please retry." — which matches no verb in the
// #202 word list (upstream + reset/refused/dropped/closed/aborted/
// ended/eof), so classifyText answers nothing: the exit path's
// error-turn fallback keeps exit_model_class and the live path's rule-1
// verdict keeps model_error. The death burned the glm candidate
// (candidate_switched, fresh dispatch) and forced a fresh re-derivation
// of the whole step. Expected per the WI: a 4xx body with BOTH the
// upstream subject AND an explicit retry invitation is an upstream
// transient — the same candidate is retried under the D7 backoff,
// resuming the transcript, bounded at UpstreamTransientRetryBound; a
// plain 4xx (no upstream, or no retry invitation) stays model-class.
//
// The first two tests fail before the fix, for exactly the reported
// reason; the controls pass before and after.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// The incident's error strings, verbatim as the WI quotes them
// (polyforge#222): the status-prefixed form the exit path carried and
// the bare body a 4xx response's error message can equally ride (the
// classification only ever sees text — the digits need not be in it).
const (
	upstream4xxRetryInvitation = "400: The model request failed due to an upstream error. Please retry."
	upstream4xxRetryBody       = "The model request failed due to an upstream error. Please retry."
)

// TestUpstream4xxRetryInvitationClassifiesTransient: the incident's
// texts — on an error turn (the live rpc path) and on an exited
// process's last error alike — classify as the upstream-transient
// cause, which never advances the candidate chain.
func TestUpstream4xxRetryInvitationClassifiesTransient(t *testing.T) {
	for _, text := range []string{upstream4xxRetryInvitation, upstream4xxRetryBody} {
		for _, turnErr := range []bool{false, true} {
			got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: text, TurnError: turnErr})
			if got != FailUpstreamTransient {
				t.Errorf("REPORTED FAILURE (polyforge#227): %q (turn error %v) classified %q, want %q — a 4xx body that names upstream AND invites a retry is an upstream transient, not a model fault", text, turnErr, got, FailUpstreamTransient)
			}
		}
		// the live path's shape: rule 1 hands down a model_error verdict
		// with the incident text as the turn error; verdictCause must
		// re-classify it into the backoff-retry class.
		if got := verdictCause(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: text}); got != FailUpstreamTransient {
			t.Errorf("REPORTED FAILURE (polyforge#227): verdictCause(model_error, %q) = %q, want %q — the live 4xx upstream turn burns a candidate instead of backing off", text, got, FailUpstreamTransient)
		}
		// the driver's own failure never carries a provider body: like
		// every other transient signature, this one is exit-only.
		if got, _, _ := classifyWaitFailure(errWithText("read transcript: " + text)); got == FailUpstreamTransient {
			t.Errorf("driver-local %q classified %q, want not transient", text, got)
		}
	}
}

// TestUpstream4xxRetryInvitationLiveTurnRetriesSameModel: ONE live
// error turn carrying the incident's text, then a delivering dispatch.
// Expected: the retry runs on the SAME model after the D7 backoff
// (booked into T), resuming the transcript, the blocking counter
// untouched, the attempt archived upstream_transient, no candidate burn.
// Observed before the fix: the candidate switches on the first error
// and the step restarts from scratch.
func TestUpstream4xxRetryInvitationLiveTurnRetriesSameModel(t *testing.T) {
	env, waits := runLive(t, []liveCase{
		{name: "upstream-4xx-retry-invitation", errMsg: upstream4xxRetryInvitation, retries: 0, cause: FailUpstreamTransient, same: true},
	})
	models := env.models()
	if len(models) != 2 {
		t.Fatalf("dispatched %v, want two (the error turn + the delivery)", models)
	}
	if models[0] != models[1] {
		t.Fatalf("REPORTED FAILURE (polyforge#227): one live 4xx upstream turn %q switched the model immediately (%q -> %q) and restarted the step from scratch — an upstream 4xx that invites a retry must retry the SAME model first", upstream4xxRetryInvitation, models[0], models[1])
	}
	if sw := env.log.byKind("candidate_switched"); len(sw) != 0 {
		t.Fatalf("REPORTED FAILURE (polyforge#227): the glm candidate was burned on one upstream 4xx retry invitation: %+v", sw)
	}
	ev := env.log.byKind("step_failed")
	if len(ev) != 1 {
		t.Fatalf("step_failed events %d, want 1", len(ev))
	}
	body := ev[0].body
	if body["cause"] != FailUpstreamTransient {
		t.Fatalf("REPORTED FAILURE (polyforge#227): live error turn %q recorded cause %v, want %q (step_failed %v)", upstream4xxRetryInvitation, body["cause"], FailUpstreamTransient, body)
	}
	if body["disposition"] != "retry_same_model" {
		t.Fatalf("REPORTED FAILURE (polyforge#227): disposition %v, want retry_same_model", body["disposition"])
	}
	if base := supervise.DefaultRetryBackoffBase; len(waits) != 1 || waits[0] != base {
		t.Fatalf("REPORTED FAILURE (polyforge#227): backoff waits %v, want one %v (the same-model retry's first retry-pacing backoff)", waits, base)
	}
	st := env.exec(t).Steps["s"]
	if st.Supervision != nil && st.Supervision.FailureCount != 0 {
		t.Fatalf("REPORTED FAILURE (polyforge#227): the same-model retry moved the blocking counter: %+v", st.Supervision)
	}
	if got := st.BudgetConsumed.WallMs; got < supervise.DefaultRetryBackoffBase.Milliseconds() {
		t.Fatalf("REPORTED FAILURE (polyforge#227): step T %dms, want the %v backoff booked in", got, supervise.DefaultRetryBackoffBase)
	}
	// the retry resumes the transcript: the #222 death forced a fresh
	// re-derivation of the whole step instead.
	if sp := env.sp.spawnAt(1); !sp.Resume || sp.Fresh {
		t.Fatalf("REPORTED FAILURE (polyforge#227): the same-model retry must resume the transcript, got %+v", sp)
	}
}

// TestUpstream4xxRetryInvitationControlsStayModelClass: the extension
// is the conjunction — a plain 4xx, a 4xx naming upstream without the
// retry invitation, and a 4xx inviting a retry without the upstream
// subject all stay model-class (per the WI); the throttle and allowance
// controls keep their own classes.
func TestUpstream4xxRetryInvitationControlsStayModelClass(t *testing.T) {
	controls := []struct{ name, text string }{
		{"plain 4xx", "400 invalid request: invalid argument"},
		{"plain 4xx status", "400 Bad Request"},
		{"upstream without retry invitation", "400: The model request failed due to an upstream error."},
		{"retry invitation without upstream", "400: The model request failed. Please retry."},
	}
	for _, tc := range controls {
		for _, turnErr := range []bool{false, true} {
			got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: tc.text, TurnError: turnErr})
			if got == FailUpstreamTransient {
				t.Errorf("%s: %q (turn error %v) classified %q — per the WI a plain 4xx stays model-class; the transient extension needs BOTH the upstream subject AND the retry invitation", tc.name, tc.text, turnErr, got)
			}
		}
		// the live path keeps the model_error verdict: a plain 4xx burns
		// the candidate exactly as before the extension.
		if got := verdictCause(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: tc.text}); got != FailModelError {
			t.Errorf("%s: verdictCause(model_error, %q) = %q, want %q — a plain 4xx stays model-class on the live path", tc.name, tc.text, got, FailModelError)
		}
	}
	// the neighboring classes keep their texts (the extension must not
	// swallow them).
	if got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: "429 Too Many Requests"}); got != FailRateLimited {
		t.Errorf("a throttle must stay a throttle: %q", got)
	}
	if got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: "insufficient quota"}); got != FailExitModel {
		t.Errorf("an allowance problem must stay model-class: %q", got)
	}
}
