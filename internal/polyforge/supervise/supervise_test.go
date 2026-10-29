package supervise

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- fixtures: the pinned pi record shapes (see pi.go) ---

const (
	fxResponse   = `{"id":"pf-1","type":"response","command":"prompt","success":true}`
	fxSettled    = `{"type":"agent_settled"}`
	fxAgentStart = `{"type":"agent_start"}`
	fxTurnStart  = `{"type":"turn_start"}`
	fxUIRequest  = `{"type":"extension_ui_request","request":{"method":"setStatus"}}`
	fxThinking   = `{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"hm"}}`
	fxText       = `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"ok"}}`
	fxToolStart  = `{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"go test ./..."}}`
	fxToolEnd    = `{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":{}}`
	fxAgentEndOK = `{"type":"agent_end","messages":[{"role":"assistant","stopReason":"stop"}],"willRetry":false}`
)

func fxAgentEndErr(msg string) string {
	return fmt.Sprintf(`{"type":"agent_end","messages":[{"role":"user"},{"role":"assistant","stopReason":"error","errorMessage":%q}],"willRetry":false}`, msg)
}

// recSink records the standard signals the translator emits.
type recSink struct {
	mu  sync.Mutex
	log []string
}

func (r *recSink) add(s string) { r.mu.Lock(); r.log = append(r.log, s); r.mu.Unlock() }
func (r *recSink) Alive()       { r.add("alive") }
func (r *recSink) Noise()       { r.add("noise") }
func (r *recSink) WaitingModel() {
	r.add("waiting")
}
func (r *recSink) ModelOutput(k OutputKind)  { r.add("output:" + string(k)) }
func (r *recSink) ToolStart(id, n, a string) { r.add("tool_start:" + n + ":" + a) }
func (r *recSink) ToolEnd(id, n string)      { r.add("tool_end:" + n) }
func (r *recSink) TurnEnded(ok bool, e string) {
	r.add(fmt.Sprintf("turn_end:%v:%s", ok, e))
}
func (r *recSink) ErrorSeen(t string)     { r.add("error:" + t) }
func (r *recSink) ProcessExit(i ExitInfo) { r.add(fmt.Sprintf("exit:%d", i.Code)) }

func TestPiTranslatorMapsRecordsToStandardSignals(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string
	}{
		{"ui chatter is noise (F15)", fxUIRequest, []string{"noise"}},
		{"command echo is not work", fxResponse, []string{"noise"}},
		{"boot settle is not work", fxSettled, []string{"noise"}},
		{"unparseable line", `not json`, []string{"noise"}},
		{"agent_start is work", fxAgentStart, []string{"alive"}},
		{"turn_start: waiting for the model", fxTurnStart, []string{"alive", "waiting"}},
		{"thinking_delta", fxThinking, []string{"output:thinking"}},
		{"text_delta", fxText, []string{"output:text"}},
		{"tool start carries name and args", fxToolStart, []string{`tool_start:bash:{"command":"go test ./..."}`}},
		{"tool end", fxToolEnd, []string{"tool_end:bash"}},
		{"agent_end ok", fxAgentEndOK, []string{"turn_end:true:"}},
		{"agent_end with stopReason=error", fxAgentEndErr("429 quota"), []string{"turn_end:false:429 quota"}},
		{"retrying agent_end is not a final error",
			`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"x"}],"willRetry":true}`,
			[]string{"turn_end:true:"}},
		{"assistant message_end error is only noted",
			`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"boom"}}`,
			[]string{"alive", "error:boom"}},
	}
	for _, tc := range cases {
		rs := &recSink{}
		NewPi(rs).Feed([]byte(tc.line))
		if fmt.Sprint(rs.log) != fmt.Sprint(tc.want) {
			t.Errorf("%s: signals %v, want %v", tc.name, rs.log, tc.want)
		}
	}
	rs := &recSink{}
	tr := NewPi(rs)
	tr.Exit(ExitInfo{Code: 3})
	if fmt.Sprint(rs.log) != "[exit:3]" {
		t.Fatalf("exit signal: %v", rs.log)
	}
	if tr.Granularity() != GranularityToken || tr.Name() != "pi" {
		t.Fatalf("pi declares token-level granularity, got %s/%s", tr.Name(), tr.Granularity())
	}
}

// --- rules ---

type fixClock struct{ now time.Time }

func newTrackerAt(c *fixClock, th Thresholds) *Tracker {
	tr := newTracker(1, func() time.Time { return c.now })
	tr.Begin(StepStarted{Step: "s", SpawnN: 1, StartedAt: c.now, Thresholds: th})
	return tr
}

func judge(c *fixClock, tr *Tracker) *StepDead {
	return Judge(tr.Snapshot(c.now), c.now, time.Minute)
}

func (c *fixClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func TestJudgeRuleTable(t *testing.T) {
	X, Y, T := 10*time.Minute, 30*time.Minute, 90*time.Minute
	th := Thresholds{Silence: X, Tool: Y, StepTimeout: T}

	t.Run("healthy streaming dispatch has no verdict", func(t *testing.T) {
		c := &fixClock{now: time.Unix(1000, 0)}
		tr := newTrackerAt(c, th)
		tr.WaitingModel()
		for i := 0; i < 20; i++ {
			c.advance(5 * time.Minute)
			tr.ModelOutput(OutputThinking)
			if v := judge(c, tr); v != nil && c.now.Sub(time.Unix(1000, 0)) < T {
				t.Fatalf("streaming model judged dead at %v: %+v", c.now, v)
			}
		}
	})

	t.Run("rule 0 harness_silent: nothing within X; noise does not count", func(t *testing.T) {
		c := &fixClock{now: time.Unix(1000, 0)}
		tr := newTrackerAt(c, th)
		for i := 0; i < 5; i++ {
			c.advance(time.Minute)
			tr.Noise()
		}
		if judge(c, tr) != nil {
			t.Fatal("before X: no verdict")
		}
		c.advance(6 * time.Minute)
		if v := judge(c, tr); v == nil || v.Cause != CauseHarnessSilent {
			t.Fatalf("verdict %+v, want harness_silent", v)
		}
	})

	t.Run("rule 1 model_error: grace then verdict; cleared by a new turn", func(t *testing.T) {
		c := &fixClock{now: time.Unix(1000, 0)}
		tr := newTrackerAt(c, th)
		tr.WaitingModel()
		tr.TurnEnded(false, "auth failed")
		c.advance(59 * time.Second)
		if judge(c, tr) != nil {
			t.Fatal("inside the 1 minute grace")
		}
		c.advance(2 * time.Second)
		if v := judge(c, tr); v == nil || v.Cause != CauseModelError {
			t.Fatalf("verdict %+v, want model_error", v)
		}
		tr.WaitingModel() // the harness went on
		if v := judge(c, tr); v != nil && v.Cause == CauseModelError {
			t.Fatal("a new turn clears the error")
		}
	})

	t.Run("rule 1 ended_without_result: ok-ended turn, no result after the grace", func(t *testing.T) {
		c := &fixClock{now: time.Unix(1000, 0)}
		tr := newTrackerAt(c, th)
		tr.WaitingModel()
		tr.TurnEnded(true, "")
		c.advance(59 * time.Second)
		if judge(c, tr) != nil {
			t.Fatal("inside the 1 minute grace")
		}
		c.advance(2 * time.Second)
		if v := judge(c, tr); v == nil || v.Cause != CauseEndedWithoutResult {
			t.Fatalf("verdict %+v, want ended_without_result", v)
		}
		tr2 := newTrackerAt(c, th)
		tr2.TurnEnded(true, "")
		c.advance(30 * time.Second)
		tr2.WaitingModel() // the harness went on (a new turn)
		c.advance(2 * time.Minute)
		if v := judge(c, tr2); v != nil && v.Cause == CauseEndedWithoutResult {
			t.Fatal("a new turn clears the ended state")
		}
	})

	t.Run("rule 2 model_hung: waiting, no output for X", func(t *testing.T) {
		c := &fixClock{now: time.Unix(1000, 0)}
		tr := newTrackerAt(c, th)
		tr.WaitingModel()
		c.advance(9 * time.Minute)
		tr.ModelOutput(OutputThinking)
		c.advance(9 * time.Minute)
		if judge(c, tr) != nil {
			t.Fatal("output restarted the window")
		}
		c.advance(2 * time.Minute)
		if v := judge(c, tr); v == nil || v.Cause != CauseModelHung {
			t.Fatalf("verdict %+v, want model_hung", v)
		}
	})

	t.Run("rule 3 tool_hung only past Y; a long tool within Y survives (#83)", func(t *testing.T) {
		c := &fixClock{now: time.Unix(1000, 0)}
		tr := newTrackerAt(c, th)
		tr.WaitingModel()
		tr.ToolStart("c1", "bash", "{}")
		c.advance(25 * time.Minute) // far past X with the model quiet
		if judge(c, tr) != nil {
			t.Fatal("a tool within Y is live work")
		}
		c.advance(6 * time.Minute)
		if v := judge(c, tr); v == nil || v.Cause != CauseToolHung {
			t.Fatalf("verdict %+v, want tool_hung", v)
		}
	})

	t.Run("rule 4 classifies where the time went", func(t *testing.T) {
		c := &fixClock{now: time.Unix(1000, 0)}
		tr := newTrackerAt(c, Thresholds{Silence: time.Hour, Tool: time.Hour * 10, StepTimeout: T})
		tr.WaitingModel()
		tr.ToolStart("c1", "bash", "{}") // closes the model phase
		c.advance(91 * time.Minute)
		if v := judge(c, tr); v == nil || v.Cause != CauseStepTimeoutLongTask {
			t.Fatalf("verdict %+v, want step_timeout_long_task (time in tools)", v)
		}
		tr2 := newTrackerAt(c, Thresholds{Silence: time.Hour * 10, Tool: time.Hour, StepTimeout: T})
		tr2.WaitingModel()
		tr2.ModelOutput(OutputText)
		c.advance(91 * time.Minute)
		tr2.ModelOutput(OutputText)
		if v := judge(c, tr2); v == nil || v.Cause != CauseStepTimeoutSlowModel {
			t.Fatalf("verdict %+v, want step_timeout_slow_model (time waiting on the model)", v)
		}
	})

	t.Run("T accumulates across dispatches of the step", func(t *testing.T) {
		c := &fixClock{now: time.Unix(1000, 0)}
		tr := newTrackerAt(c, Thresholds{Silence: time.Hour * 10, Tool: time.Hour * 10, StepTimeout: T, PriorRuntime: 80 * time.Minute})
		tr.Alive()
		c.advance(9 * time.Minute)
		if judge(c, tr) != nil {
			t.Fatal("89m < 90m")
		}
		c.advance(2 * time.Minute)
		if v := judge(c, tr); v == nil || (v.Cause != CauseStepTimeoutLongTask && v.Cause != CauseStepTimeoutSlowModel) {
			t.Fatalf("verdict %+v, want a step_timeout", v)
		}
	})

	t.Run("priority: rule 0 outranks 4, 1 outranks 2", func(t *testing.T) {
		c := &fixClock{now: time.Unix(1000, 0)}
		tr := newTrackerAt(c, Thresholds{Silence: time.Minute, Tool: time.Minute, StepTimeout: time.Minute})
		c.advance(time.Hour)
		if v := judge(c, tr); v == nil || v.Cause != CauseHarnessSilent {
			t.Fatalf("verdict %+v, want harness_silent first", v)
		}
		tr2 := newTrackerAt(c, Thresholds{Silence: time.Minute, StepTimeout: time.Minute})
		tr2.WaitingModel()
		tr2.TurnEnded(false, "x")
		tr2.WaitingModel()
		tr2.TurnEnded(false, "x")
		c.advance(time.Hour)
		if v := judge(c, tr2); v == nil || v.Cause != CauseModelError {
			t.Fatalf("verdict %+v, want model_error before the timeouts", v)
		}
	})

	t.Run("an exited process is the driver's to classify", func(t *testing.T) {
		c := &fixClock{now: time.Unix(1000, 0)}
		tr := newTrackerAt(c, th)
		tr.ProcessExit(ExitInfo{Code: 1})
		c.advance(10 * time.Hour)
		if judge(c, tr) != nil {
			t.Fatal("no verdict for an exited process")
		}
	})
}

// TestJudgeStepTimeoutBlamesOnlyTheDyingDispatch pins rule 4's attribution
// ( D1): T is cumulative over the whole step, so the dispatch
// that happens to be running when it runs out is the slow-model cause (a
// candidate switch) only when it ran at least half of T itself AND waited on
// the model for at least half of its own runtime. Every other expiry is
// step_timeout_long_task: the step's length, retried on the same model.
func TestJudgeStepTimeoutBlamesOnlyTheDyingDispatch(t *testing.T) {
	const T = 90 * time.Minute
	now := time.Unix(10_000_000, 0)
	cases := []struct {
		name                     string
		prior, age, tools, model time.Duration
		want                     Cause
	}{
		// code attempt 4, verbatim: "step ran 1h30m0s cumulatively
		// (1h17m22s before this dispatch + 12m38s now) … waiting on the model
		// 12m2s". The record's tool time is elided; it is at most the 36s left
		// of the dispatch and any value in [0, 36s] judges the same.
		{"a 12m38s tail of a step that spent 1h17m22s before it",
			77*time.Minute + 22*time.Second, 12*time.Minute + 38*time.Second, 30 * time.Second, 12*time.Minute + 2*time.Second,
			CauseStepTimeoutLongTask},
		// code attempt 1, verbatim: "0s before this dispatch +
		// 1h30m0s now; tools 31m0s, waiting on the model 59m1s".
		{"the whole T in one dispatch, mostly waiting on the model",
			0, 90 * time.Minute, 31 * time.Minute, 59*time.Minute + time.Second,
			CauseStepTimeoutSlowModel},
		{"exactly half of T, exactly half of it on the model",
			45 * time.Minute, 45 * time.Minute, 0, 22*time.Minute + 30*time.Second,
			CauseStepTimeoutSlowModel},
		{"one second under half of T, all of it on the model",
			45*time.Minute + time.Second, 45*time.Minute - time.Second, 0, 45*time.Minute - time.Second,
			CauseStepTimeoutLongTask},
		{"the whole T, the model under half of the dispatch",
			0, 90 * time.Minute, 20 * time.Minute, 44 * time.Minute,
			CauseStepTimeoutLongTask},
		{"the whole T, mostly tools",
			0, 90 * time.Minute, 80 * time.Minute, 10 * time.Minute,
			CauseStepTimeoutLongTask},
		// tool time sums every concurrent tool; the model wait is wall clock,
		// so it is the model's share of the dispatch that decides
		{"parallel tools out-sum the model, the model still holds most of the clock",
			0, 90 * time.Minute, 120 * time.Minute, 50 * time.Minute,
			CauseStepTimeoutSlowModel},
	}
	for _, tc := range cases {
		s := Snapshot{
			Step: "code", SpawnN: 4, Begun: true, StartedAt: now.Add(-tc.age),
			Thresholds: Thresholds{StepTimeout: T, PriorRuntime: tc.prior},
			ModelTime:  tc.model, ToolTime: tc.tools,
		}
		v := Judge(s, now, 0)
		if v == nil || v.Cause != tc.want {
			t.Errorf("%s: verdict %+v, want %s", tc.name, v, tc.want)
			continue
		}
		// the evidence states the split the attribution read, so the
		// failure record says why this dispatch was (not) blamed
		if !strings.Contains(v.Evidence, "of the bound") || !strings.Contains(v.Evidence, "waiting on the model") {
			t.Errorf("%s: evidence does not state the attribution: %s", tc.name, v.Evidence)
		}
	}
}

// --- link: queue, fence, release ---

func TestQueueNeverBlocksNeverDrops(t *testing.T) {
	q := NewQueue[int]()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100000; i++ {
			q.Push(i) // nobody drains: the sender must not block
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Push blocked on an undrained queue")
	}
	got := q.Drain()
	if len(got) != 100000 {
		t.Fatalf("dropped facts: %d of 100000 survived", len(got))
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("order broken at %d: %d", i, v)
		}
	}
	select {
	case <-q.Notify():
	default:
		t.Fatal("a pending wake-up must be signalled")
	}
}

func TestLinkTrackerFenceAndRelease(t *testing.T) {
	l := NewLink("wi")
	t1 := l.Tracker(5)
	if l.Tracker(5) != t1 {
		t.Fatal("one tracker per dispatch")
	}
	t1.Alive()
	l.Release(5)
	late := l.Tracker(5)
	if late == t1 {
		t.Fatal("a released dispatch must not hand its old tracker back")
	}
	late.Alive() // a late record of the dead spawn lands in a detached tracker
	if l.Tracker(5) == late {
		t.Fatal("late records must never resurrect a released dispatch")
	}
	if l.Tracker(4) == l.Tracker(4) {
		t.Fatal("older ordinals are fenced off too")
	}
	if l.Tracker(6) != l.Tracker(6) {
		t.Fatal("a newer dispatch gets its own live tracker")
	}
}

func TestDefaultsFill(t *testing.T) {
	d := Defaults{}.Fill()
	if d.Tool != 30*time.Minute || d.StepTimeout != 90*time.Minute || d.DriverStall != 10*time.Minute || d.ModelErrorGrace != time.Minute {
		t.Fatalf("defaults: %+v", d)
	}
	if got := d.SilenceFor(GranularityToken); got != 10*time.Minute {
		t.Fatalf("token X: %v", got)
	}
	if got := d.SilenceFor(GranularityMessage); got != 30*time.Minute {
		t.Fatalf("message X: %v", got)
	}
	if got := (Defaults{Silence: time.Minute}).SilenceFor(GranularityMessage); got != time.Minute {
		t.Fatalf("an explicit X applies to every harness: %v", got)
	}
}

func TestLinkResetStartsANewDriverEpoch(t *testing.T) {
	l := NewLink("wi")
	l.Release(9) // the old driver's dispatches ran up to ordinal 9
	l.Verdict(StepDead{Step: "s", SpawnN: 9, Cause: CauseModelHung})
	l.Fact(ExecCommitted{Version: 3})
	l.Reset()
	// a re-claimed capsule may restart its ordinals: the fence is gone
	t1 := l.Tracker(1)
	t1.Alive()
	if l.Tracker(1) != t1 {
		t.Fatal("after a reset ordinal 1 gets a live, shared tracker again")
	}
	if vs := l.Verdicts.Drain(); len(vs) != 0 {
		t.Fatalf("stale verdicts must not leak into the new driver: %+v", vs)
	}
	if fs := l.Facts.Drain(); len(fs) != 1 {
		t.Fatalf("facts are kept for the supervisor to consume in order: %+v", fs)
	}
}

func TestTrackerLiveProgressSummaryFilesAndCount(t *testing.T) {
	c := &fixClock{now: time.Unix(1000, 0)}
	tr := newTrackerAt(c, Thresholds{})
	tr.ToolStart("c1", "bash", `{"command":"go test ./internal/..."}`)
	c.advance(5 * time.Second)
	tr.ToolEnd("c1", "bash")
	for _, p := range []string{"a.go", "b.go", "a.go", "c.go", "d.go", "e.go", "f.go"} {
		tr.ToolStart("w-"+p, "edit", `{"path":"`+p+`","old":"x","new":"y"}`)
		tr.ToolEnd("w-"+p, "edit")
	}
	tr.ToolStart("r1", "read", `{"path":"ignored.go"}`) // reads are not changes
	tr.ToolEnd("r1", "read")
	tr.ToolStart("c2", "grep", `{"pattern":"TODO","path":"."}`)
	c.advance(9 * time.Second)
	s := tr.Snapshot(c.now)
	if s.ToolCalls != 10 {
		t.Fatalf("tool calls %d, want 10", s.ToolCalls)
	}
	if len(s.RecentFiles) != 5 || s.RecentFiles[4] != "f.go" {
		t.Fatalf("recent files %v: want the last five unique changed paths, newest last", s.RecentFiles)
	}
	for _, p := range s.RecentFiles {
		if p == "ignored.go" {
			t.Fatal("a read tool must not count as a changed file")
		}
	}
	if len(s.Tools) != 1 || s.Tools[0].Summary != "grep ." {
		t.Fatalf("running tool summary %+v", s.Tools)
	}
}

func TestSummarizeTool(t *testing.T) {
	cases := []struct{ name, args, summary, path string }{
		{"bash", `{"command":"make build"}`, "make build", ""},
		{"bash", `{"argv":["go","test","./..."]}`, "go test ./...", ""},
		{"edit", `{"path":"x/y.go","old":"a","new":"b"}`, "edit x/y.go", "x/y.go"},
		{"write", `{"file_path":"z.md","content":"c"}`, "write z.md", "z.md"},
		{"grep", `{"pattern":"TODO"}`, "grep TODO", ""},
		{"custom", `not json`, "custom", ""},
	}
	for _, tc := range cases {
		s, p := summarizeTool(tc.name, tc.args)
		if s != tc.summary || p != tc.path {
			t.Errorf("%s %s: got (%q, %q), want (%q, %q)", tc.name, tc.args, s, p, tc.summary, tc.path)
		}
	}
}

func TestNewModelActivityClearsStaleLastError(t *testing.T) {
	c := &fixClock{now: time.Unix(1000, 0)}
	tr := newTrackerAt(c, Thresholds{})
	tr.ErrorSeen("429 rate limit (retried)")
	if tr.Snapshot(c.now).LastError == "" {
		t.Fatal("error must be recorded")
	}
	tr.WaitingModel()
	if got := tr.Snapshot(c.now).LastError; got != "" {
		t.Fatalf("a new turn must clear the stale error, got %q", got)
	}
	tr.ErrorSeen("again")
	tr.ModelOutput(OutputThinking)
	if got := tr.Snapshot(c.now).LastError; got != "" {
		t.Fatalf("new model output must clear the stale error, got %q", got)
	}
}
