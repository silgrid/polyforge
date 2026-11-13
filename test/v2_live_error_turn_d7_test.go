package test

// v2_live_error_turn_d7_test.go: polyforge#162 end to end. The real
// `pf daemon` drives the fakepi step process through a provider error
// turn that it SURVIVES — the real pi rpc shape: the error rides the
// ordinary records (stopReason=error + errorMessage), pi's own
// auto-retry rounds come first for the texts pi deems retryable, and
// the process then idles alive, so the dispatch ends by the
// supervisor's rule-1 verdict (model_error), never by an exit. The D7
// classification must still apply: a throttle is a same-model retry with
// the backoff booked into T.
//
// This is the transient rule's one e2e smoke (design #483 §7,
//): the other texts' classes on the live path are the
// driver's TestLiveErrorTurnGoesThroughD7Classification, the rule's
// decision its TestTransientRetriesSameModelAfterBackoffUncounted.

import (
	"strings"
	"testing"
	"time"
)

func TestV2LiveErrorTurnGoesThroughD7(t *testing.T) {
	// pi auto-retries a throttle twice before the final error turn
	const errMsg = `429 {"type":"error","error":{"type":"rate_limit_error","message":"rate limit exceeded"}}`
	inst := newV1Install(t)
	wi := inst.newWI(t, "#162: live error turn 429",
		plan([]map[string]any{
			{"do": "envelope", "id": "fix", "error_turn": errMsg, "error_retries": 2},
			{"do": "envelope", "id": "fix", "outcome": "completed"},
		}), nil)
	// a 2s grace, as the other rule-1 e2e scenarios use: the verdict must
	// not hinge on a loaded host scheduling the error turn inside a 1s
	// window
	grace := 2 * time.Second
	d := newV1Daemon(t, inst, optSupervision(supBounds{modelErrorGrace: grace}))
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	vs := verdicts(t, d, wi)
	if len(vs) != 1 || vs[0].Cause != "model_error" || !strings.Contains(vs[0].Evidence, errMsg) {
		t.Fatalf("verdicts %+v, want one rule-1 model_error naming the live error turn", vs)
	}
	fails := stepFailures(t, d, wi)
	if len(fails) != 1 || fails[0].Cause != "rate_limited" {
		t.Fatalf("failure records %+v, want one rate_limited", fails)
	}
	models := spawnModels(t, d.capsuleDir(wi))
	if len(models) < 2 || fails[0].Disposition != "retry_same_model" || models[0] != models[1] {
		t.Fatalf("disposition %q, models %v — want a same-model retry", fails[0].Disposition, models)
	}
	var backoff int64
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind == "step_failed" {
			var b struct {
				BackoffMs int64 `json:"backoff_ms"`
			}
			eventBody(t, e, &b)
			backoff = b.BackoffMs
		}
	}
	// a throttle waits the first transient backoff (1s, workflow contract
	// 4, design #483 §7)
	const want = int64(1000)
	if backoff != want {
		t.Fatalf("backoff_ms %d, want the first backoff (%d)", backoff, want)
	}
	// T carries the error dispatch (>= the grace) plus the backoff it
	// imposed
	x := readExecution(t, d.capsuleDir(wi))
	if wall, _ := x.Steps["fix"].BudgetConsumed["wall_ms"].(float64); wall < float64(grace.Milliseconds()+want) {
		t.Fatalf("step T %vms, want the dispatch and the %dms backoff booked in", wall, want)
	}
}
