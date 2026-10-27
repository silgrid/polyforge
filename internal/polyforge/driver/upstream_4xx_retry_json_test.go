package driver

// upstream_4xx_retry_json_test.go: pins the polyforge#222 incident text
// verbatim as pi recorded it (JSON-wrapped gateway body, from the #222
// session transcript) — the repro (upstream_4xx_retry_repro_test.go)
// covers only the unwrapped renderings. The JSON form also carries
// invalid_request_error, which must not pull it into model-class.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

const upstream4xxRetryJSON = `400: {"message":"The model request failed due to an upstream error. Please retry.","type":"invalid_request_error"}`

func TestUpstream4xxRetryInvitationJSONBody(t *testing.T) {
	for _, turnErr := range []bool{false, true} {
		got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: upstream4xxRetryJSON, TurnError: turnErr})
		if got != FailUpstreamTransient {
			t.Errorf("%q (turn error %v) classified %q, want %q", upstream4xxRetryJSON, turnErr, got, FailUpstreamTransient)
		}
	}
	if got := verdictCause(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: upstream4xxRetryJSON}); got != FailUpstreamTransient {
		t.Errorf("verdictCause(model_error, %q) = %q, want %q", upstream4xxRetryJSON, got, FailUpstreamTransient)
	}
	// the same JSON body without the retry invitation stays model-class.
	plain := `400: {"message":"The model request failed due to an upstream error.","type":"invalid_request_error"}`
	if got := verdictCause(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: plain}); got != FailModelError {
		t.Errorf("verdictCause(model_error, %q) = %q, want %q", plain, got, FailModelError)
	}

	// the live path: same model, resumed, no candidate burn.
	env, _ := runLive(t, []liveCase{
		{name: "upstream-4xx-retry-json", errMsg: upstream4xxRetryJSON, retries: 0, cause: FailUpstreamTransient, same: true},
	})
	models := env.models()
	if len(models) != 2 || models[0] != models[1] {
		t.Fatalf("dispatched %v, want the same model twice", models)
	}
	if sw := env.log.byKind("candidate_switched"); len(sw) != 0 {
		t.Fatalf("candidate burned: %+v", sw)
	}
	if sp := env.sp.spawnAt(1); !sp.Resume || sp.Fresh {
		t.Fatalf("the same-model retry must resume the transcript, got %+v", sp)
	}
}
