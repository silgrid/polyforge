package driver

// waitcall_test.go: the driver's wait_call relay: one
// durable event per wait call the dispatch's tracker folds — recorded
// while the dispatch runs, recorded once, and never a verdict.

import (
	"context"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestWaitCallEventEmittedOncePerCall(t *testing.T) {
	env := newSupEnv(t, nil, spec("s"))
	held := done(`{}`)
	held.hold = true
	env.sp.script = []fakeEntry{held}
	runDone := make(chan error, 1)
	go func() { runDone <- env.d.Run(context.Background()) }()
	waitForSpawns(env, 1)
	tr := env.link.Tracker(1) // the tracker the dispatch's translator feeds

	// a leading sleep: recorded while the dispatch is still running
	tr.ToolStart("c1", "bash", `{"command":"sleep 120 && gh pr checks 7"}`)
	waitUntil(t, "the live wait_call event", func() bool { return len(env.log.byKind(EvWaitCall)) == 1 })
	tr.ToolEnd("c1", "bash")
	// a sleep in a condition loop and a short settle delay: not wait calls
	tr.ToolStart("c2", "bash", `{"command":"until make check; do sleep 30; done"}`)
	tr.ToolEnd("c2", "bash")
	tr.ToolStart("c3", "Bash", `{"command":"sleep 2 && git fetch"}`)
	tr.ToolEnd("c3", "Bash")
	// a second leading sleep (Claude Code's tool name), then the dispatch
	// delivers at once: the relay's last take still records it
	tr.ToolStart("c4", "Bash", `{"command":"sleep 1m"}`)
	_ = env.sp.runs[0].Terminate(0) // releases the held Wait with its result
	if err := <-runDone; err != nil {
		t.Fatalf("Run: %v", err)
	}

	evs := env.log.byKind(EvWaitCall)
	if len(evs) != 2 {
		t.Fatalf("wait_call events %d, want one per wait call (2): %+v", len(evs), evs)
	}
	want := []map[string]any{
		{"step": "s", "spawn_n": int64(1), "seconds": int64(120), "argv": "sleep 120 && gh pr checks 7",
			"tool_call_id": "c1", "wait_calls": 1, "wait_seconds": int64(120)},
		{"step": "s", "spawn_n": int64(1), "seconds": int64(60), "argv": "sleep 1m",
			"tool_call_id": "c4", "wait_calls": 2, "wait_seconds": int64(180)},
	}
	for i, w := range want {
		for k, v := range w {
			if got := evs[i].body[k]; got != v {
				t.Fatalf("wait_call %d field %s = %#v, want %#v (body %+v)", i, k, got, v, evs[i].body)
			}
		}
		if at, _ := evs[i].body["at"].(string); at == "" {
			t.Fatalf("wait_call %d carries no start instant: %+v", i, evs[i].body)
		}
	}
	// counted and surfaced only: no verdict, no failure, the step delivered
	if n := len(env.log.byKind("step_dead")); n != 0 {
		t.Fatalf("a wait call produced %d step_dead events", n)
	}
	st := env.exec(t).Steps["s"]
	if st.Status != capsule.StepDone || st.Supervision != nil {
		t.Fatalf("a dispatch with wait calls must deliver like any other: %+v", st)
	}
}

func TestWaitCallFoldedAsTheDispatchEndsIsRecordedOnce(t *testing.T) {
	env := newSupEnv(t, nil, spec("s"))
	e := done(`{}`)
	e.act = func() { // the step's last tool call lands just before its result
		env.link.Tracker(1).ToolStart("c9", "bash", `{"command":"sleep 10"}`)
	}
	env.sp.script = []fakeEntry{e}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	evs := env.log.byKind(EvWaitCall)
	if len(evs) != 1 || evs[0].body["tool_call_id"] != "c9" || evs[0].body["seconds"] != int64(10) {
		t.Fatalf("the late wait call must be recorded exactly once: %+v", evs)
	}
	// recorded before the dispatch's end is: every wait_call precedes the
	// step's next transition in the log
	if len(env.log.byKind("step_done")) != 1 {
		t.Fatalf("the step must be done once: %+v", env.log.entries)
	}
	env.log.mu.Lock()
	defer env.log.mu.Unlock()
	seenWait := false
	for _, le := range env.log.entries {
		if le.kind == EvWaitCall {
			seenWait = true
		}
		if le.kind == "step_done" && !seenWait {
			t.Fatalf("step_done logged before the dispatch's wait_call: %+v", env.log.entries)
		}
	}
}

// TestWaitCallRelayFinalTakeRecordsWhatTheWakeMissed pins the relay's last
// take deterministically: a wait call whose wake-up the relay goroutine
// never sees (consumed here, before the relay starts) is still recorded
// by stop, once.
func TestWaitCallRelayFinalTakeRecordsWhatTheWakeMissed(t *testing.T) {
	env := newSupEnv(t, nil, spec("s"))
	tr := env.link.Tracker(7)
	tr.ToolStart("c1", "bash", `{"command":"sleep 30"}`)
	<-tr.WaitCallNotify() // the wake-up is gone: only the final take can find the call
	stop := env.d.relayWaitCalls("s", 7)
	stop()
	evs := env.log.byKind(EvWaitCall)
	if len(evs) != 1 || evs[0].body["tool_call_id"] != "c1" || evs[0].body["spawn_n"] != int64(7) {
		t.Fatalf("the final take must record the call once: %+v", evs)
	}
	// a driver without a link relays nothing and stops cleanly
	bare := newSupEnv(t, func(c *Config) { c.Link = nil }, spec("s"))
	bare.d.relayWaitCalls("s", 1)()
}
