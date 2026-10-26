package test

// v2_live_error_turn_d7_test.go: polyforge#162 end to end. The real
// `pf daemon` drives the fakepi step process through a provider error
// turn that it SURVIVES — the real pi rpc shape: the error rides the
// ordinary records (stopReason=error + errorMessage), pi's own
// auto-retry rounds come first for the texts pi deems retryable, and
// the process then idles alive, so the dispatch ends by the
// supervisor's rule-1 verdict (model_error), never by an exit. The D7
// classification must still apply: a throttle / upstream transient is
// a same-model retry with the backoff booked into T; an allowance/auth
// text switches candidates.

import (
	"strings"
	"testing"
	"time"
)

func TestV2LiveErrorTurnGoesThroughD7(t *testing.T) {
	cases := []struct {
		name    string
		errMsg  string
		retries int
		cause   string
		same    bool
	}{
		{"429", `429 {"type":"error","error":{"type":"rate_limit_error","message":"rate limit exceeded"}}`, 2, "rate_limited", true},
		{"zh-concurrency", "当前模型并发数过高，请稍后再试", 0, "rate_limited", true},
		{"upstream-malformed-tool-call", "Upstream tool call has incomplete or invalid JSON arguments", 0, "upstream_transient", true},
		{"insufficient-balance", `402 {"error":{"message":"Insufficient balance"}}`, 0, "model_error", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inst := newV1Install(t)
			wi := inst.newWI(t, "#162: live error turn "+c.name,
				plan([]map[string]any{
					{"do": "envelope", "id": "grill", "error_turn": c.errMsg, "error_retries": c.retries},
					{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
				}), nil)
			d := newV1Daemon(t, inst, optSupervision(supBounds{modelErrorGrace: time.Second}))
			d.start()
			t.Cleanup(func() { killOrphans(wi) })
			d.pf(t, "submit", wi)

			inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
			vs := verdicts(t, d, wi)
			if len(vs) != 1 || vs[0].Cause != "model_error" || !strings.Contains(vs[0].Evidence, c.errMsg) {
				t.Fatalf("verdicts %+v, want one rule-1 model_error naming the live error turn", vs)
			}
			fails := stepFailures(t, d, wi)
			if len(fails) != 1 || fails[0].Cause != c.cause {
				t.Fatalf("failure records %+v, want one %s", fails, c.cause)
			}
			models := spawnModels(t, d.capsuleDir(wi))
			if len(models) < 2 {
				t.Fatalf("spawned models %v, want the error dispatch and its retry", models)
			}
			x := readExecution(t, d.capsuleDir(wi))
			wall, _ := x.Steps["grill"].BudgetConsumed["wall_ms"].(float64)
			if c.same {
				if fails[0].Disposition != "retry_same_model" || models[0] != models[1] {
					t.Fatalf("live %s: disposition %q, models %v — want a same-model retry", c.name, fails[0].Disposition, models)
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
				if backoff != 1000 {
					t.Fatalf("live %s: backoff_ms %d, want the first D7 backoff (1000)", c.name, backoff)
				}
				// T carries the error dispatch (>= the 1s grace) plus the
				// 1s backoff it imposed
				if wall < 2000 {
					t.Fatalf("live %s: step T %vms, want the dispatch and the 1s D7 backoff booked in", c.name, wall)
				}
				return
			}
			if fails[0].Disposition != "switch_model" || models[0] == models[1] || fails[0].From == fails[0].To {
				t.Fatalf("live %s: failure %+v, models %v — want a candidate switch", c.name, fails[0], models)
			}
		})
	}
}
