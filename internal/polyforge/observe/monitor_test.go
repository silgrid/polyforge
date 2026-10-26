package observe

// monitor_test.go: the supervisor surface — driver death and the storm
// guard, the rules of v2-converge 2.3 applied through the driver's facts
// and the harness Tracker (each historical incident replayed), the
// verdict fence (one verdict per dispatch, silence until step_ended,
// state per dispatch), driver_stalled with its human-wait exemption, and
// the select-never-blocks proofs.
//
// The non-blocking test is the MUTATION-HONEST kind: the fake push
// blocks on a channel the TEST controls. If an implementation regressed
// to calling it inline in a select case, the "monitor still processes
// facts meanwhile" assertion would time out and the suite would go red.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func TestDeathDetectionSendsNeedsRecovery(t *testing.T) {
	h := newFakeHandle("att_1")
	m, ob := newTestMonitor(t, h, nil)

	h.die()

	msg := waitOB(t, ob, "WINeedsRecovery(death)")
	if msg.Kind != protocol.OBNeedsRecovery {
		t.Fatalf("kind: got %s", msg.Kind)
	}
	if msg.Reason != protocol.RecoverDeath {
		t.Fatalf("reason: got %s, want %s", msg.Reason, protocol.RecoverDeath)
	}
	if msg.SpawnN != 1 {
		t.Fatalf("spawn fencing: got %d", msg.SpawnN)
	}
	if msg.WorkItem != "wi_test" {
		t.Fatalf("work item: got %s", msg.WorkItem)
	}
	soon(t, 500*time.Millisecond, "needs_recovery event written", func() bool {
		return len(eventsOf(m, EvNeedsRecovery)) == 1
	})
}

func TestStormGuardSingleNeedsRecoveryPerAttempt(t *testing.T) {
	h := newFakeHandle("att_1")
	m, ob := newTestMonitor(t, h, nil)

	// Signal 1: the driver died.
	h.die()
	msg := waitOB(t, ob, "first WINeedsRecovery")
	if msg.Reason != protocol.RecoverDeath {
		t.Fatalf("reason: got %s", msg.Reason)
	}

	// Signal 2..n: everything a dying driver can still emit — a dispatch
	// whose harness errored. Exactly ONE WINeedsRecovery may leave the
	// monitor for this attempt ("已发后静默到恢复完成").
	tr := startDispatch(m, 1, "code", supervise.Thresholds{Silence: time.Hour})
	tr.WaitingModel()
	tr.TurnEnded(false, "upstream API error (401)")
	expectNoOB(t, ob, 250*time.Millisecond, "storm-suppressed duplicate signals")

	// Recovery: a NEW attempt re-arms the guard. Its death is reported
	// again — with the new derivation counter.
	h2 := newFakeHandle("att_2")
	m.rtQ.Push(Attach{Msg: protocol.RTMsg{Kind: protocol.RTRecovered, WorkItem: "wi_test", SpawnN: 2}, Handle: h2})
	soon(t, 500*time.Millisecond, "recovery adopted", func() bool {
		return m.currentSpawn() == 2
	})
	h2.die()
	msg2 := waitOB(t, ob, "WINeedsRecovery for the new attempt")
	if msg2.SpawnN != 2 {
		t.Fatalf("new derivation must fence its own messages: %+v", msg2)
	}
	// Exactly two messages total for the whole scenario.
	expectNoOB(t, ob, 250*time.Millisecond, "no third message")
}

// A daemon-initiated close arms the sealing suppression (markClosing):
// the driver's exit that follows is EXPECTED, not a death.
func TestExpectedExitAfterMarkClosingIsSilent(t *testing.T) {
	h := newFakeHandle("att_1")
	m, ob := newTestMonitor(t, h, nil)

	m.markClosing()
	h.die()
	expectNoOB(t, ob, 250*time.Millisecond, "post-close exit")
}

// ---------------------------------------------------------------------------
// the rules, one dispatch at a time (the pure rule table lives in
// package supervise; these prove the supervisor applies it per dispatch
// and speaks once).

func TestRuleHarnessSilentZeroSignalsAfterSpawn(t *testing.T) {
	// incident class: #114 zero events — the harness is stuck at startup
	// (or its stdout is held by a grandchild) and only ever times out.
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	startDispatch(m, 1, "grill", supervise.Thresholds{Silence: 60 * time.Millisecond})

	v := nextVerdict(t, m, "harness_silent")
	dead, ok := v.(supervise.StepDead)
	if !ok || dead.Cause != supervise.CauseHarnessSilent || dead.SpawnN != 1 || dead.Step != "grill" {
		t.Fatalf("verdict = %+v, want step_dead harness_silent for grill@1", v)
	}
	// the supervisor stays silent for that spawn until step_ended
	expectNoVerdict(t, m, 150*time.Millisecond, "after the verdict")
}

func TestRuleHarnessSilentUINoiseDoesNotCountAsWork(t *testing.T) {
	// F15: a background extension streams UI refreshes that kept the old
	// "any record is life" rule fresh forever. Noise is not work.
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	tr := startDispatch(m, 1, "grill", supervise.Thresholds{Silence: 80 * time.Millisecond})
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				tr.Noise()
			}
		}
	}()
	dead, ok := nextVerdict(t, m, "harness_silent through noise").(supervise.StepDead)
	if !ok || dead.Cause != supervise.CauseHarnessSilent {
		t.Fatalf("verdict = %+v, want harness_silent (UI noise is silence)", dead)
	}
	if !strings.Contains(dead.Evidence, "non-work records") {
		t.Fatalf("evidence should say noise was seen: %q", dead.Evidence)
	}
}

func TestRuleModelErrorAfterGraceNotBefore(t *testing.T) {
	// F5: the turn ENDED on a fatal provider error and the session sits
	// idle forever. Rule 1 judges it one grace after the error — not
	// instantly, not never.
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, func(c *monitorCfg) { c.ModelErrorGrace = 150 * time.Millisecond })
	tr := startDispatch(m, 1, "fix", supervise.Thresholds{Silence: time.Hour, Tool: time.Hour, StepTimeout: time.Hour})
	tr.WaitingModel()
	tr.ModelOutput(supervise.OutputText)
	tr.TurnEnded(false, "upstream API error 529")
	t0 := time.Now()
	expectNoVerdict(t, m, 60*time.Millisecond, "inside the grace")
	dead, ok := nextVerdict(t, m, "model_error").(supervise.StepDead)
	if !ok || dead.Cause != supervise.CauseModelError {
		t.Fatalf("verdict = %+v, want model_error", dead)
	}
	if since := time.Since(t0); since < 120*time.Millisecond {
		t.Fatalf("model_error came after only %v: the grace was not honoured", since)
	}
	if !strings.Contains(dead.Evidence, "529") {
		t.Fatalf("evidence must carry the error text: %q", dead.Evidence)
	}
}

func TestRuleModelErrorClearedByRecovery(t *testing.T) {
	// the harness went on (auto-retry): no verdict
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, func(c *monitorCfg) { c.ModelErrorGrace = 100 * time.Millisecond })
	tr := startDispatch(m, 1, "fix", supervise.Thresholds{Silence: time.Hour})
	tr.TurnEnded(false, "429")
	time.Sleep(30 * time.Millisecond)
	tr.WaitingModel() // a new turn: the harness retried
	expectNoVerdict(t, m, 200*time.Millisecond, "after the harness went on")
}

func TestRuleModelHungWaitingForModelNoOutput(t *testing.T) {
	// #114: the provider call never returns mid-step.
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	tr := startDispatch(m, 1, "fix", supervise.Thresholds{Silence: 90 * time.Millisecond, Tool: time.Hour, StepTimeout: time.Hour})
	tr.WaitingModel()
	dead, ok := nextVerdict(t, m, "model_hung").(supervise.StepDead)
	if !ok || dead.Cause != supervise.CauseModelHung {
		t.Fatalf("verdict = %+v, want model_hung", dead)
	}
}

func TestRuleModelHungNotWhileTokensFlow(t *testing.T) {
	// token-level granularity: a long reply that keeps streaming is alive
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	tr := startDispatch(m, 1, "fix", supervise.Thresholds{Silence: 90 * time.Millisecond})
	tr.WaitingModel()
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		tr.ModelOutput(supervise.OutputThinking)
		time.Sleep(10 * time.Millisecond)
	}
	expectNoVerdict(t, m, 10*time.Millisecond, "a streaming model is not hung")
}

func TestRuleLongToolWithinYSurvives(t *testing.T) {
	// #83: a long build/edit is live work, not a hang — the old lane
	// killed it deterministically; now it only matters past Y.
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	tr := startDispatch(m, 1, "fix", supervise.Thresholds{Silence: 50 * time.Millisecond, Tool: time.Hour, StepTimeout: time.Hour})
	tr.WaitingModel()
	tr.ToolStart("c1", "bash", `{"command":"go test ./..."}`)
	expectNoVerdict(t, m, 300*time.Millisecond, "a tool running within Y with the model quiet")
}

func TestRuleToolHungBeyondY(t *testing.T) {
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	tr := startDispatch(m, 1, "fix", supervise.Thresholds{Silence: time.Hour, Tool: 80 * time.Millisecond, StepTimeout: time.Hour})
	tr.ToolStart("c1", "bash", `{"command":"sleep infinity"}`)
	dead, ok := nextVerdict(t, m, "tool_hung").(supervise.StepDead)
	if !ok || dead.Cause != supervise.CauseToolHung {
		t.Fatalf("verdict = %+v, want tool_hung", dead)
	}
	if !strings.Contains(dead.Evidence, "bash") || !strings.Contains(dead.Evidence, "sleep infinity") {
		t.Fatalf("evidence must name the tool and its args: %q", dead.Evidence)
	}
}

func TestRuleStepTimeoutTrickleWedgePastT(t *testing.T) {
	// #117/#124: the step trickles one progress record per window forever;
	// no silence rule ever fires. Only the CUMULATIVE bound can — and it
	// counts the runtime earlier dispatches of the step already spent.
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	tr := startDispatch(m, 1, "fix", supervise.Thresholds{
		Silence: time.Hour, Tool: time.Hour,
		StepTimeout: 300 * time.Millisecond, PriorRuntime: 200 * time.Millisecond,
	})
	tr.WaitingModel()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
				tr.ModelOutput(supervise.OutputText) // the trickle
			}
		}
	}()
	t0 := time.Now()
	dead, ok := nextVerdict(t, m, "step_timeout").(supervise.StepDead)
	if !ok || dead.Cause != supervise.CauseStepTimeoutSlowModel {
		t.Fatalf("verdict = %+v, want step_timeout_slow_model (the time went to the model)", dead)
	}
	if since := time.Since(t0); since > 250*time.Millisecond {
		t.Fatalf("T accumulates across dispatches: fired after %v, want ~100ms (200ms already spent)", since)
	}
}

func TestRuleTwoRulesAtOnceOneVerdictLowestNumberWins(t *testing.T) {
	// harness never worked (rule 0) AND the step is already over T
	// (rule 4): exactly ONE verdict, the lower-numbered rule's.
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	startDispatch(m, 1, "fix", supervise.Thresholds{
		Silence: 40 * time.Millisecond, StepTimeout: 40 * time.Millisecond,
	})
	dead, ok := nextVerdict(t, m, "priority").(supervise.StepDead)
	if !ok || dead.Cause != supervise.CauseHarnessSilent {
		t.Fatalf("verdict = %+v, want harness_silent (rule 0 outranks rule 4)", dead)
	}
	expectNoVerdict(t, m, 150*time.Millisecond, "no second verdict for the same dispatch")
}

func TestStateIsPerDispatchDroppedOnStepEnded(t *testing.T) {
	// #124: turn/tool state of one step must never survive into the next.
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	th := supervise.Thresholds{Silence: time.Hour, Tool: 100 * time.Millisecond, StepTimeout: time.Hour}
	tr1 := startDispatch(m, 1, "a", th)
	tr1.ToolStart("c1", "bash", "{}")
	soon(t, time.Second, "dispatch 1 adopted", func() bool { return m.activity().InDispatch })
	// the step ended before Y; the next dispatch starts clean
	m.link.Fact(supervise.StepEnded{Step: "a", SpawnN: 1, Outcome: supervise.OutcomeResult})
	tr2 := startDispatch(m, 2, "b", th)
	tr2.Alive()
	expectNoVerdict(t, m, 300*time.Millisecond, "a tool of the ended dispatch must not poison the next")
	// late records of the dead spawn are ignored, never resurrected
	if got := m.link.Tracker(1); got.Snapshot(time.Now()).Begun {
		t.Fatal("a released dispatch must not come back with state")
	}
}

func TestVerdictForNewDispatchAfterStepEnded(t *testing.T) {
	// one verdict per DISPATCH: a new dispatch is judged afresh
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	startDispatch(m, 1, "a", supervise.Thresholds{Silence: 40 * time.Millisecond})
	if _, ok := nextVerdict(t, m, "first").(supervise.StepDead); !ok {
		t.Fatal("no first verdict")
	}
	m.link.Fact(supervise.StepEnded{Step: "a", SpawnN: 1, Outcome: supervise.OutcomeExitedWithoutResult})
	startDispatch(m, 2, "a", supervise.Thresholds{Silence: 40 * time.Millisecond})
	dead, ok := nextVerdict(t, m, "second").(supervise.StepDead)
	if !ok || dead.SpawnN != 2 {
		t.Fatalf("second dispatch verdict = %+v", dead)
	}
}

func TestNoVerdictsWhileRecovering(t *testing.T) {
	h := newFakeHandle("att_1")
	m, ob := newTestMonitor(t, h, nil)
	startDispatch(m, 1, "a", supervise.Thresholds{Silence: 40 * time.Millisecond})
	h.die()
	waitOB(t, ob, "death")
	expectNoVerdict(t, m, 200*time.Millisecond, "the driver is being relaunched")
}

// ---------------------------------------------------------------------------
// the driver between steps

func TestDriverStalledBetweenStepsEscalatesToRecovery(t *testing.T) {
	// the driver is neither in a step nor waiting on a human past N
	// (e.g. a push child stuck holding the lock)
	h := newFakeHandle("att_1")
	m, ob := newTestMonitor(t, h, func(c *monitorCfg) { c.DriverStall = 80 * time.Millisecond })
	st, ok := nextVerdict(t, m, "driver_stalled").(supervise.DriverStalled)
	if !ok || st.Duration < 80*time.Millisecond {
		t.Fatalf("verdict = %+v, want driver_stalled", st)
	}
	msg := waitOB(t, ob, "driver_stalled recovery")
	if msg.Reason != protocol.RecoverDriverStalled || msg.Kind != protocol.OBNeedsRecovery {
		t.Fatalf("OB message = %+v", msg)
	}
	expectNoOB(t, ob, 150*time.Millisecond, "one escalation per gap")
}

func TestNoTimersWhileWaitingForHuman(t *testing.T) {
	// #99: a parked question has no step process — no dispatch timer and
	// no stall clock may run, however long the human takes.
	h := newFakeHandle("att_1")
	m, ob := newTestMonitor(t, h, func(c *monitorCfg) { c.DriverStall = 60 * time.Millisecond })
	tr := startDispatch(m, 1, "grill", supervise.Thresholds{Silence: 60 * time.Millisecond, Tool: 60 * time.Millisecond, StepTimeout: 60 * time.Millisecond})
	tr.Alive()
	m.link.Fact(supervise.StepEnded{Step: "grill", SpawnN: 1, Outcome: supervise.OutcomeResult})
	m.link.Fact(supervise.StepWaitingHuman{Step: "grill", SpawnN: 1, QuestionID: "q1"})
	expectNoVerdict(t, m, 400*time.Millisecond, "waiting on a human")
	expectNoOB(t, ob, 10*time.Millisecond, "no stall while a human is asked")
	// the human answers: the driver is between steps again and the clock
	// starts from the answer
	m.link.Fact(supervise.StepAnswered{Step: "grill", SpawnN: 1, QuestionID: "q1"})
	if _, ok := nextVerdict(t, m, "stall after the answer").(supervise.DriverStalled); !ok {
		t.Fatal("expected driver_stalled once the driver idles after the answer")
	}
}

// ---------------------------------------------------------------------------
// select non-blocking proofs (mutation-honest: a blocking implementation
// turns these red through the soon() timeout)

func TestMonitorNotBlockedByPushInFlight(t *testing.T) {
	h := newFakeHandle("att_1")
	release := make(chan struct{})
	pushes := make(chan struct{}, 16)
	m, _ := newTestMonitor(t, h, func(c *monitorCfg) {
		c.push = func(ctx context.Context, o gitops.Opts, message string) (string, bool, gitops.PushTimings, error) {
			pushes <- struct{}{}
			<-release // the "long" push
			return "sha_slow", true, gitops.PushTimings{}, nil
		}
	})

	// execution.json v1 commits → an async push starts and parks.
	m.writeExec(t, 1, "code")
	soon(t, 500*time.Millisecond, "push started", func() bool { return len(pushes) > 0 })

	// The monitor must keep consuming facts while the push is in flight.
	m.link.Fact(supervise.StepStarted{Step: "code", SpawnN: 1, StartedAt: time.Now()})
	soon(t, 300*time.Millisecond, "fact consumed while the push is in flight", func() bool {
		return m.activity().InDispatch
	})

	// Only ONE push may be in flight per WI: the parked push holds the
	// gate, and a v2 bump must NOT start a second one yet.
	m.writeExec(t, 2, "code")
	time.Sleep(80 * time.Millisecond)
	if got := len(pushes); got != 1 {
		t.Fatalf("pushes started: got %d, want 1 (single in-flight gate)", got)
	}

	close(release)
	soon(t, 500*time.Millisecond, "first push settles", func() bool {
		return len(eventsOf(m, EvPublished)) >= 1
	})
	// Now the still-dirty v2 gets its turn.
	soon(t, 500*time.Millisecond, "second push after gate freed", func() bool {
		return len(pushes) >= 2
	})
}

func TestPushIsEventTriggeredNotPolled(t *testing.T) {
	// the supervisor no longer polls execution.json: a file rewrite with
	// no exec_committed fact publishes nothing, the fact does
	h := newFakeHandle("att_1")
	pushes := make(chan struct{}, 4)
	m, _ := newTestMonitor(t, h, func(c *monitorCfg) {
		c.push = func(ctx context.Context, o gitops.Opts, message string) (string, bool, gitops.PushTimings, error) {
			pushes <- struct{}{}
			return "sha", true, gitops.PushTimings{}, nil
		}
	})
	soon(t, time.Second, "supervisor running", func() bool { return len(eventsOf(m, EvJobStarted)) == 1 })
	time.Sleep(50 * time.Millisecond) // past the seed read
	rawWrite(t, m, 5, "code")         // file only, no fact
	time.Sleep(150 * time.Millisecond)
	if len(pushes) != 0 {
		t.Fatal("a silent file rewrite must not trigger a push: the supervisor does not poll")
	}
	m.link.Fact(supervise.ExecCommitted{Version: 5, Step: "code"})
	soon(t, time.Second, "push on the commit-boundary fact", func() bool { return len(pushes) == 1 })
}

func TestTranscriptSourceFollowsLatestStepStarted(t *testing.T) {
	// F3: the sync sources from the latest step_started.transcript_path
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	when := time.Now()
	m.link.Fact(supervise.StepStarted{Step: "a", SpawnN: 1, TranscriptPath: "/cap/session/one.jsonl", StartedAt: when})
	m.link.Fact(supervise.StepStarted{Step: "a", SpawnN: 2, TranscriptPath: "/cap/session/two.jsonl", StartedAt: when, Resumed: true})
	soon(t, time.Second, "both facts consumed", func() bool {
		return m.activity().SpawnN == 2
	})
	snap := m.transcriptSnapshot()
	if snap.dst != "/cap/session/two.jsonl" || snap.src != "/cap/session/two.jsonl" {
		t.Fatalf("transcript snapshot = %+v, want the latest dispatch's (resumed: src == dst)", snap)
	}
}

func TestLiveProgressFaceReadsTheCurrentDispatch(t *testing.T) {
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	if p := m.liveProgress(); p.InDispatch {
		t.Fatalf("no dispatch yet: %+v", p)
	}
	tr := startDispatch(m, 1, "fix", supervise.Thresholds{Silence: time.Hour})
	soon(t, time.Second, "dispatch adopted", func() bool { return m.liveProgress().InDispatch })
	tr.ToolStart("c1", "edit", `{"path":"internal/a.go"}`)
	tr.ToolEnd("c1", "edit")
	tr.ToolStart("c2", "bash", `{"command":"go test ./..."}`)
	p := m.liveProgress()
	if p.Step != "fix" || p.Command != "go test ./..." || p.ToolCalls != 2 ||
		len(p.RecentFiles) != 1 || p.RecentFiles[0] != "internal/a.go" {
		t.Fatalf("live progress %+v", p)
	}
	m.link.Fact(supervise.StepEnded{Step: "fix", SpawnN: 1, Outcome: supervise.OutcomeResult})
	soon(t, time.Second, "dispatch dropped", func() bool { return !m.liveProgress().InDispatch })
}

// polyforge#176: the in-flight dispatch's activity names its candidate,
// effort included (the ops status CANDIDATE column).
func TestActivityCarriesCandidateWithEffort(t *testing.T) {
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	th := supervise.Thresholds{Silence: time.Hour, StepTimeout: time.Hour}
	m.link.Fact(supervise.StepStarted{
		Step: "fix", SpawnN: 1, Model: "claude-opus-5-5", Effort: "high", Harness: "claude",
		StartedAt: m.link.Clock(), Thresholds: th,
	})
	soon(t, time.Second, "dispatch 1 adopted", func() bool { return m.activity().InDispatch })
	if got := m.activity().Candidate; got != "claude:claude-opus-5-5:high" {
		t.Fatalf("candidate %q, want claude:claude-opus-5-5:high", got)
	}
	m.link.Fact(supervise.StepEnded{Step: "fix", SpawnN: 1, Outcome: supervise.OutcomeResult})
	startDispatch(m, 2, "b", th) // model m on pi, no effort
	soon(t, time.Second, "dispatch 2 adopted", func() bool { return m.activity().SpawnN == 2 })
	if got := m.activity().Candidate; got != "pi:m" {
		t.Fatalf("candidate %q, want pi:m", got)
	}
}

// F10: an id-less pending question in execution.json is unanswerable;
// the commit-boundary read names it with one pending_invalid event.
func TestIDLessPendingQuestionWritesPendingInvalid(t *testing.T) {
	m, _ := newTestMonitor(t, newFakeHandle("att_1"), nil)
	writePendingQuestion(t, m, "", "which?", "grill")
	soon(t, time.Second, "the pending_invalid event", func() bool { return len(eventsOf(m, "pending_invalid")) >= 1 })
}

// A daemon-initiated close stays quiet even when a recovery it was queued
// behind adopts a new derivation: that derivation's expected exit raises
// nothing either.
func TestMarkClosingOutlivesARecovery(t *testing.T) {
	h := newFakeHandle("att_1")
	m, ob := newTestMonitor(t, h, nil)
	m.markClosing()
	h2 := newFakeHandle("att_2")
	m.rtQ.Push(Attach{Msg: protocol.RTMsg{Kind: protocol.RTRecovered, WorkItem: "wi_test", SpawnN: 2}, Handle: h2})
	soon(t, time.Second, "recovery adopted", func() bool { return m.currentSpawn() == 2 })
	h2.die()
	expectNoOB(t, ob, 200*time.Millisecond, "the closed derivation's exit")
	if n := len(eventsOf(m, EvNeedsRecovery)); n != 0 {
		t.Fatalf("needs_recovery events after MarkClosing: %d", n)
	}
}

// A monitor told to stop leaves the link's facts to its successor, even
// when its loop picks the facts case before the stop case.
func TestStoppingMonitorLeavesFactsToItsSuccessor(t *testing.T) {
	m, _ := newTestMonitor(t, newFakeHandle("att_1"), nil)
	m.requestStop()
	soon(t, time.Second, "the monitor stopped", func() bool { return len(eventsOf(m, EvJobStopped)) == 1 })
	m.link.Fact(supervise.StepStarted{Step: "next", SpawnN: 7, StartedAt: time.Now()})
	m.drainFacts() // the select's facts case, taken after the stop
	if facts := m.link.Facts.Drain(); len(facts) != 1 {
		t.Fatalf("the stopping monitor consumed the successor's facts: %d left", len(facts))
	}
}
