package driver

// live_error_turn_test.go: polyforge#162. A real `pi --mode rpc` does
// NOT exit after a turn that ends on a provider error (stopReason=error
// + errorMessage): the error rides the ordinary records, the session
// idles, and the dispatch dies by the supervisor's rule-1 verdict
// (model_error) — never by a process exit. These tests drive that path
// end to end inside the package: the REAL pi rpc record sequence (incl.
// pi's own auto-retry rounds for the texts pi treats as retryable) fed
// through the real pi translator into the dispatch's tracker, the real
// rule-1 judgement, and the verdict handed to the running driver over
// the link. The process (a held fake run) stays alive until the verdict
// tears it down.
//
// What they pin: the live-process error turn goes through the SAME D7
// text classification as an exited dispatch (classifyWaitFailure) — a
// throttle or an upstream transient is a same-model retry whose backoff
// is booked into T; allowance/auth texts switch candidates.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// piRec renders one pi rpc stdout record.
func piRec(t *testing.T, rec map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// piErrorTurnRecords is the record sequence of a pi turn that ends on
// errMsg, as agent-session.js emits it: retries rounds that pi itself
// auto-retries (agent_end willRetry=true + auto_retry_start), then the
// final error turn (agent_end willRetry=false + auto_retry_end), after
// which the rpc process stays alive and idle.
func piErrorTurnRecords(t *testing.T, errMsg string, retries int) [][]byte {
	t.Helper()
	msg := map[string]any{"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": errMsg}
	turn := func(willRetry bool) [][]byte {
		return [][]byte{
			piRec(t, map[string]any{"type": "agent_start"}),
			piRec(t, map[string]any{"type": "turn_start"}),
			piRec(t, map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}}),
			piRec(t, map[string]any{"type": "message_end", "message": msg}),
			piRec(t, map[string]any{"type": "turn_end", "message": msg, "toolResults": []any{}}),
			piRec(t, map[string]any{"type": "agent_end", "messages": []any{msg}, "willRetry": willRetry}),
		}
	}
	var out [][]byte
	for i := 1; i <= retries; i++ {
		out = append(out, turn(true)...)
		out = append(out, piRec(t, map[string]any{"type": "auto_retry_start", "attempt": i, "maxAttempts": retries, "delayMs": 2000, "errorMessage": errMsg}))
	}
	out = append(out, turn(false)...)
	if retries > 0 {
		out = append(out, piRec(t, map[string]any{"type": "auto_retry_end", "success": false, "attempt": retries, "finalError": errMsg}))
	}
	return out
}

// liveErrorVerdict replays an error turn into dispatch n's tracker and
// answers the supervisor's judgement one grace later (the process is
// still alive: no exit is recorded).
func liveErrorVerdict(t *testing.T, link *supervise.Link, step string, n int64, errMsg string, retries int) supervise.StepDead {
	t.Helper()
	tr := link.Tracker(n)
	tr.Begin(supervise.StepStarted{Step: step, SpawnN: n, Harness: "pi", StartedAt: time.Now()})
	pi := supervise.NewPi(tr)
	for _, line := range piErrorTurnRecords(t, errMsg, retries) {
		pi.Feed(line)
	}
	grace := time.Second
	v := supervise.Judge(tr.Snapshot(time.Now()), time.Now().Add(2*grace), grace)
	if v == nil || v.Cause != supervise.CauseModelError {
		t.Errorf("%q: the idle error turn must be judged model_error by rule 1, got %+v", errMsg, v)
		return supervise.StepDead{Step: step, SpawnN: n, Cause: supervise.CauseModelError, Evidence: errMsg}
	}
	return *v
}

// liveCase is one provider error text and its D7 expectation.
type liveCase struct {
	name    string
	errMsg  string
	retries int    // pi's own auto-retry rounds before the final error turn
	cause   string // the recorded failure cause
	same    bool   // same-model retry (true) or candidate switch (false)
}

var liveCases = []liveCase{
	{"429", `429 {"type":"error","error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit"}}`, 3, FailRateLimited, true},
	{"overloaded", `529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, 3, FailRateLimited, true},
	{"zh-concurrency", `当前模型并发数过高，请稍后再试`, 0, FailRateLimited, true},
	{"upstream-malformed-tool-call", upstreamTransientToolArgs, 0, FailUpstreamTransient, true},
	{"insufficient-balance", `402 {"error":{"message":"Insufficient balance","type":"insufficient_balance"}}`, 0, FailModelError, false},
	{"401", `401 {"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`, 0, FailModelError, false},
}

// runLive runs the driver over len(errs) live error turns (one per
// dispatch, each judged by rule 1) followed by a delivering dispatch,
// recording the backoff waits the driver imposes.
func runLive(t *testing.T, errs []liveCase) (*supEnv, []time.Duration) {
	t.Helper()
	var mu sync.Mutex
	var waits []time.Duration
	env := newChainEnv(t, func(c *Config) {
		c.Sleep = func(_ context.Context, d time.Duration) {
			mu.Lock()
			waits = append(waits, d)
			mu.Unlock()
		}
	}, "s", "glm-5.3", "kimi-k3", "opus-5")
	for range errs {
		env.sp.script = append(env.sp.script, fakeEntry{hold: true})
	}
	env.sp.script = append(env.sp.script, done(`{"ok":true}`))
	go func() {
		for i, c := range errs {
			waitForSpawns(env, i+1)
			sp := env.sp.spawnAt(i)
			env.link.Verdict(liveErrorVerdict(t, env.link, sp.StepID, sp.SpawnN, c.errMsg, c.retries))
		}
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return env, append([]time.Duration(nil), waits...)
}

// TestLiveErrorTurnGoesThroughD7Classification: one live error turn per
// case, then a delivery. Throttles and upstream transients retry the
// SAME model after the D7 backoff, booked into the step's T, without
// moving the blocking counter; allowance/auth texts switch candidates
// with no backoff.
func TestLiveErrorTurnGoesThroughD7Classification(t *testing.T) {
	for _, c := range liveCases {
		t.Run(c.name, func(t *testing.T) {
			env, waits := runLive(t, []liveCase{c})
			models := env.models()
			if len(models) != 2 {
				t.Fatalf("dispatched %v, want two (the error turn + the delivery)", models)
			}
			ev := env.log.byKind("step_failed")
			if len(ev) != 1 {
				t.Fatalf("step_failed events %d, want 1", len(ev))
			}
			body := ev[0].body
			if body["cause"] != c.cause {
				t.Fatalf("live error turn %q recorded cause %v, want %q (step_failed %v)", c.errMsg, body["cause"], c.cause, body)
			}
			st := env.exec(t).Steps["s"]
			if c.same {
				if models[0] != models[1] {
					t.Fatalf("live error turn %q switched models %q -> %q; D7 wants a same-model retry", c.errMsg, models[0], models[1])
				}
				if body["disposition"] != "retry_same_model" {
					t.Fatalf("disposition %v, want retry_same_model", body["disposition"])
				}
				if len(waits) != 1 || waits[0] != RateLimitBackoffBase {
					t.Fatalf("backoff waits %v, want one %v", waits, RateLimitBackoffBase)
				}
				if got := st.BudgetConsumed.WallMs; got < RateLimitBackoffBase.Milliseconds() {
					t.Fatalf("step T %dms, want the %v backoff booked in", got, RateLimitBackoffBase)
				}
				if st.Supervision != nil && st.Supervision.FailureCount != 0 {
					t.Fatalf("a D7 same-model retry must not move the blocking counter: %+v", st.Supervision)
				}
				return
			}
			if models[0] == models[1] || body["disposition"] != "switch_model" {
				t.Fatalf("live error turn %q: dispatched %v disposition %v, want a candidate switch", c.errMsg, models, body["disposition"])
			}
			if len(waits) != 0 {
				t.Fatalf("a candidate switch must not back off: %v", waits)
			}
		})
	}
}

// TestLiveRateLimitBackoffDoublesIntoT: three consecutive live 429 turns
// stay on the first model, wait 1s, 2s, 4s, and the step's T carries all
// three waits.
func TestLiveRateLimitBackoffDoublesIntoT(t *testing.T) {
	c := liveCases[0]
	env, waits := runLive(t, []liveCase{c, c, c})
	models := env.models()
	for i, m := range models {
		if m != models[0] {
			t.Fatalf("dispatch %d ran on %q, the first on %q — a live throttle switched models (%v)", i, m, models[0], models)
		}
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	if len(waits) != len(want) {
		t.Fatalf("backoff waits %v, want %v", waits, want)
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("backoff %d is %v, want %v", i, waits[i], want[i])
		}
	}
	if got, min := env.exec(t).Steps["s"].BudgetConsumed.WallMs, (7 * time.Second).Milliseconds(); got < min {
		t.Fatalf("step T %dms, want the backoff waits (>= %dms) counted in", got, min)
	}
}

// TestLiveUpstreamTransientBoundSwitches: the upstream-transient bound
// holds on the live path too — two same-model retries, the third
// consecutive malformed tool call switches candidates.
func TestLiveUpstreamTransientBoundSwitches(t *testing.T) {
	var c liveCase
	for _, lc := range liveCases {
		if lc.cause == FailUpstreamTransient {
			c = lc
		}
	}
	env, _ := runLive(t, []liveCase{c, c, c})
	models := env.models()
	if len(models) != 4 {
		t.Fatalf("dispatched %v, want four (three transients + the delivery)", models)
	}
	if models[1] != models[0] || models[2] != models[0] {
		t.Fatalf("the first two live transients must retry the same model: %v", models)
	}
	if models[3] == models[0] {
		t.Fatalf("past the bound (%d) the third live transient must switch candidates: %v", UpstreamTransientRetryBound, models)
	}
}
