package supervise

// subagent_test.go: a sub-agent call is one tool call of the step agent
// (Claude's Agent tool, pi's subagent tool) that runs for as long as the
// sub-agent works. Rule 3 judges it on its silence — the time since its
// last inner activity — not on its total run: a sub-agent still working
// is never tool_hung, one that stopped for longer than the tool window Y
// is (AC7). The replays feed each harness's records of one
// sub-agent call at a fixed cadence well past Y in total.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// lifeOnly is the twin sink of the negative control: it folds a
// sub-agent's inner activity into plain life, as before, so
// the same replay shows what rule 3 decided without the tool clock.
type lifeOnly struct{ *Tracker }

func (l lifeOnly) ToolActive(string) { l.Tracker.Alive() }

func TestSubagentCallIsJudgedOnItsSilence(t *testing.T) {
	const (
		X, Y    = 10 * time.Minute, 30 * time.Minute
		cadence = 8 * time.Minute // between two inner records: below Y
	)
	th := Thresholds{Silence: X, Tool: Y, StepTimeout: 24 * time.Hour}
	cases := []struct {
		name, fixture, tool string
		newTl               func(Sink) Translator
	}{
		{"claude Agent", "claude-subagent-call.jsonl", "Agent", func(s Sink) Translator { return NewClaude(s) }},
		{"pi subagent", "pi-subagent-call.jsonl", "subagent", func(s Sink) Translator { return NewPi(s) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &fixClock{now: time.Unix(1000, 0)}
			tr, twin := newTrackerAt(c, th), newTrackerAt(c, th)
			tl, twinTl := tc.newTl(tr), tc.newTl(lifeOnly{twin})
			var started time.Time
			twinHung := false
			inner := 0
			for _, line := range readFixtureLines(t, tc.fixture) {
				running := len(tr.Snapshot(c.now).Tools) > 0
				if running {
					c.advance(cadence)
					inner++
				} else {
					c.advance(time.Second)
				}
				tl.Feed(line)
				twinTl.Feed(line)
				if started.IsZero() && len(tr.Snapshot(c.now).Tools) > 0 {
					started = c.now
				}
				// healthy now and right before the next record, however
				// long the call has run in total
				for _, at := range []time.Time{c.now, c.now.Add(cadence - time.Second)} {
					if v := Judge(tr.Snapshot(at), at, time.Minute); v != nil {
						t.Fatalf("the working sub-agent was judged dead at +%s of its call: %+v", at.Sub(started), v)
					}
					if v := Judge(twin.Snapshot(at), at, time.Minute); v != nil && v.Cause == CauseToolHung {
						twinHung = true
					}
				}
			}
			if started.IsZero() || inner < 5 {
				t.Fatalf("the fixture never started the %s call (%d inner records)", tc.tool, inner)
			}
			if total := c.now.Sub(started); total < 2*Y {
				t.Fatalf("the replay ran the call %s, not past 2×Y: it proves nothing", total)
			}
			// negative control: the inner records as plain life (the old
			// rule) leave the call tool_hung once it ran Y in total
			if !twinHung {
				t.Fatal("negative control: without the tool clock the same replay must be tool_hung")
			}
			// the sub-agent stops: its last inner record is the clock
			last := c.now
			if v := Judge(tr.Snapshot(last.Add(Y-time.Second)), last.Add(Y-time.Second), time.Minute); v != nil {
				t.Fatalf("silent for less than Y: healthy, got %+v", v)
			}
			v := Judge(tr.Snapshot(last.Add(Y)), last.Add(Y), time.Minute)
			if v == nil || v.Cause != CauseToolHung {
				t.Fatalf("silent for Y after its last inner record: want tool_hung, got %+v", v)
			}
			for _, want := range []string{tc.tool, "silent for 30m0s", "last inner activity"} {
				if !strings.Contains(v.Evidence, want) {
					t.Fatalf("evidence %q does not name %q", v.Evidence, want)
				}
			}
		})
	}
}

// A pi tool's own streamed output (bash's tool_execution_update) is life
// only, as before: rule 3 still judges a plain tool on its whole run.
func TestPiPlainToolUpdatesKeepTheWholeRunClock(t *testing.T) {
	const Y = 30 * time.Minute
	c := &fixClock{now: time.Unix(1000, 0)}
	tr := newTrackerAt(c, Thresholds{Silence: 10 * time.Minute, Tool: Y, StepTimeout: 24 * time.Hour})
	tl := NewPi(tr)
	tl.Feed([]byte(`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"make test"}}`))
	for i := 0; i < 6; i++ {
		c.advance(5 * time.Minute)
		tl.Feed([]byte(`{"type":"tool_execution_update","toolCallId":"c1","toolName":"bash","partialResult":{"content":[{"type":"text","text":"ok"}]}}`))
	}
	if v := judge(c, tr); v == nil || v.Cause != CauseToolHung {
		t.Fatalf("a bash call streaming output for Y is still tool_hung, got %+v", v)
	}
}

// Two sub-agents in parallel: the one still working keeps its clock
// fresh, the silent one is judged on its own silence.
func TestParallelSubagentsAreJudgedEachOnItsOwnSilence(t *testing.T) {
	const Y = 30 * time.Minute
	c := &fixClock{now: time.Unix(1000, 0)}
	tr := newTrackerAt(c, Thresholds{Silence: 10 * time.Minute, Tool: Y, StepTimeout: 24 * time.Hour})
	tr.ToolStart("a", "Agent", `{"description":"unit u1"}`)
	tr.ToolStart("b", "Agent", `{"description":"unit u2"}`)
	tr.ToolActive("b")
	for i := 0; i < 5; i++ {
		c.advance(5 * time.Minute)
		tr.ToolActive("a")
		if v := judge(c, tr); v != nil {
			t.Fatalf("at +%dm both are inside Y of their last activity: %+v", 5*(i+1), v)
		}
	}
	c.advance(5 * time.Minute) // b silent for 30m, a for 5m
	v := judge(c, tr)
	if v == nil || v.Cause != CauseToolHung || !strings.Contains(v.Evidence, "unit u2") {
		t.Fatalf("the silent sub-agent b is tool_hung, got %+v", v)
	}
	// an inner record for a call that is not running is life only
	tr2 := newTrackerAt(c, Thresholds{Silence: 10 * time.Minute, Tool: Y})
	tr2.ToolActive("gone")
	if s := tr2.Snapshot(c.now); s.WorkSignals != 1 || len(s.Tools) != 0 {
		t.Fatalf("an orphan inner record is one work signal and no tool: %+v", s)
	}
}

// The translators route a sub-agent's inner records to its call.
func TestTranslatorsRouteInnerRecordsToTheirCall(t *testing.T) {
	rs := &recSink{}
	NewClaude(rs).Feed([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"x"}]},"parent_tool_use_id":"toolu_9"}`))
	NewPi(rs).Feed([]byte(`{"type":"tool_execution_update","toolCallId":"call_9","toolName":"subagent","partialResult":{}}`))
	NewPi(rs).Feed([]byte(`{"type":"tool_execution_update","toolCallId":"call_8","toolName":"bash","partialResult":{}}`))
	if got := strings.Join(rs.log, ","); got != "tool_active:toolu_9,tool_active:call_9,alive" {
		t.Fatalf("signals %s", got)
	}
}

// TestClaudeRealAgentCallReplay replays a REAL Claude Code stream
// (claude 2.1.319, the step profile's flags with --safe-mode: the Agent
// tool with subagent_type general-purpose running one Bash command; temp
// paths normalized, and the two API message ids respelled to synthetic
// msg_<24 digits> values, one-to-one, so a secret scanner reads no key under
// `api_message_id`). Its inner records carry the Agent call's id as
// parent_tool_use_id: each is that call's inner activity, the inner Bash
// is never a tool of the step, and with the inner records spread 20m
// apart the call runs past 2×Y without being judged — the same stream as
// plain life (the twin) is tool_hung.
func TestClaudeRealAgentCallReplay(t *testing.T) {
	const Y, gap = 30 * time.Minute, 20 * time.Minute
	lines := readFixtureLines(t, "claude-real-agent-call.jsonl")
	rs := &recSink{}
	tl := NewClaude(rs)
	for _, l := range lines {
		tl.Feed(l)
	}
	var agent string
	starts, active := 0, 0
	for _, s := range rs.log {
		switch {
		case strings.HasPrefix(s, "tool_start:Agent:"):
			starts++
		case strings.HasPrefix(s, "tool_start:"):
			t.Fatalf("an inner tool became a tool of the step: %s", s)
		case strings.HasPrefix(s, "tool_active:"):
			active++
			agent = strings.TrimPrefix(s, "tool_active:")
		}
	}
	if starts != 1 || active != 3 || !strings.HasPrefix(agent, "toolu_") {
		t.Fatalf("want one Agent call with 3 inner records routed to it, got %d starts, %d inner (%q): %v", starts, active, agent, rs.log)
	}

	c := &fixClock{now: time.Unix(1000, 0)}
	th := Thresholds{Silence: 10 * time.Minute, Tool: Y, StepTimeout: 24 * time.Hour}
	tr, twin := newTrackerAt(c, th), newTrackerAt(c, th)
	replay, life := NewClaude(tr), NewClaude(lifeOnly{twin})
	var started time.Time
	twinHung := false
	for _, l := range lines {
		if strings.Contains(string(l), `"parent_tool_use_id":"toolu_`) {
			c.advance(gap)
		} else {
			c.advance(time.Second)
		}
		replay.Feed(l)
		life.Feed(l)
		if len(tr.Snapshot(c.now).Tools) == 0 {
			continue
		}
		if started.IsZero() {
			started = c.now
		}
		for _, at := range []time.Time{c.now, c.now.Add(gap - time.Second)} {
			if v := Judge(tr.Snapshot(at), at, time.Minute); v != nil {
				t.Fatalf("the working Agent call was judged at +%s: %+v", at.Sub(started), v)
			}
			if v := Judge(twin.Snapshot(at), at, time.Minute); v != nil && v.Cause == CauseToolHung {
				twinHung = true
			}
		}
	}
	if total := c.now.Sub(started); total < 2*Y || !twinHung {
		t.Fatalf("the replay must run the call past 2×Y (%s) and the twin must be tool_hung (%v)", total, twinHung)
	}
}

// A Claude tool heartbeat — the real shape: a tool_progress record every
// 30s whose parent_tool_use_id is the running call's OWN id
// (`"tool_use_id":"<id>-heartbeat-N","heartbeat":true`) — is life only:
// it never restarts rule 3's clock, so a hung Bash is tool_hung at Y as
// before, and so is an Agent call whose sub-agent stopped
// while the harness still beats for it.
func TestClaudeHeartbeatNeverExtendsATool(t *testing.T) {
	const Y = 30 * time.Minute
	for _, tool := range []string{"Bash", "Agent"} {
		t.Run(tool, func(t *testing.T) {
			c := &fixClock{now: time.Unix(1000, 0)}
			tr := newTrackerAt(c, Thresholds{Silence: 10 * time.Minute, Tool: Y, StepTimeout: 24 * time.Hour})
			tl := NewClaude(tr)
			const id = "toolu_01XvWXEyemL3Lgb5QXAEM1Qk"
			tl.Feed([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"` + tool + `","input":{"command":"sleep 3600"}}]},"parent_tool_use_id":null}`))
			for i := 0; i < 62; i++ { // 31 minutes of heartbeats, 30s apart
				c.advance(30 * time.Second)
				tl.Feed([]byte(fmt.Sprintf(`{"type":"tool_progress","tool_use_id":"%s-heartbeat-%d","tool_name":"%s","parent_tool_use_id":"%s","elapsed_time_seconds":%d,"heartbeat":true,"session_id":"2398d2de-4464-4e31-a4fa-1d29c29403b1","uuid":"838a71e6-2bce-4a69-9a9d-81b9a62b3f9e"}`, id, i, tool, id, 30*(i+1))))
			}
			if v := judge(c, tr); v == nil || v.Cause != CauseToolHung {
				t.Fatalf("a %s call that only heartbeats for 31m must be tool_hung, got %+v", tool, v)
			}
		})
	}
}

// Defence in depth: inner activity reported for a call that is no
// sub-agent call (any tool but Claude's Agent/Task and pi's subagent)
// is life only.
func TestToolActiveMovesOnlyASubagentCall(t *testing.T) {
	const Y = 30 * time.Minute
	c := &fixClock{now: time.Unix(1000, 0)}
	tr := newTrackerAt(c, Thresholds{Silence: 10 * time.Minute, Tool: Y, StepTimeout: 24 * time.Hour})
	tr.ToolStart("c1", "bash", `{"command":"make test"}`)
	for i := 0; i < 7; i++ {
		c.advance(5 * time.Minute)
		tr.ToolActive("c1")
	}
	if v := judge(c, tr); v == nil || v.Cause != CauseToolHung {
		t.Fatalf("a bash call is judged on its whole run whatever reports for it, got %+v", v)
	}
}

// parallelCall is one harness's records of a turn that starts a plain
// tool and a sub-agent call side by side: the plain tool returns first,
// the sub-agent keeps working, then returns too.
type parallelCall struct {
	name, subagent string
	newTl          func(Sink) Translator
	start          []string // both calls start in the same turn
	plainReturns   []string
	inner          []string // one inner record of the sub-agent
	subagentEnds   []string // the sub-agent returns; the model is next
}

func parallelCalls() []parallelCall {
	return []parallelCall{
		{
			name: "claude", subagent: "Agent",
			newTl: func(s Sink) Translator { return NewClaude(s) },
			start: []string{
				`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"go test ./..."}},{"type":"tool_use","id":"toolu_agent","name":"Agent","input":{"description":"self-review","subagent_type":"general-purpose"}}]},"parent_tool_use_id":null}`,
			},
			plainReturns: []string{
				`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bash","content":"ok"}]},"parent_tool_use_id":null}`,
			},
			inner: []string{
				`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"reading the diff"}]},"parent_tool_use_id":"toolu_agent"}`,
			},
			subagentEnds: []string{
				`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_agent","content":"no findings"}]},"parent_tool_use_id":null}`,
			},
		},
		{
			name: "pi", subagent: "subagent",
			newTl: func(s Sink) Translator { return NewPi(s) },
			start: []string{
				`{"type":"tool_execution_start","toolCallId":"call_bash","toolName":"bash","args":{"command":"go test ./..."}}`,
				`{"type":"tool_execution_start","toolCallId":"call_sub","toolName":"subagent","args":{"task":"self-review"}}`,
			},
			plainReturns: []string{
				`{"type":"tool_execution_end","toolCallId":"call_bash","toolName":"bash"}`,
			},
			inner: []string{
				`{"type":"tool_execution_update","toolCallId":"call_sub","toolName":"subagent","partialResult":{}}`,
			},
			subagentEnds: []string{
				`{"type":"tool_execution_end","toolCallId":"call_sub","toolName":"subagent"}`,
				`{"type":"message_end","message":{"role":"toolResult"}}`,
				`{"type":"turn_end","message":{"role":"assistant","stopReason":"toolUse"}}`,
				`{"type":"turn_start"}`,
			},
		},
	}
}

func feedAll(tl Translator, lines []string) {
	for _, l := range lines {
		tl.Feed([]byte(l))
	}
}

// A plain tool (Bash) and a foreground sub-agent run in parallel in one
// turn; the plain tool returns first while the sub-agent keeps working
// past X. Nobody is waiting for the model while a call still runs: no
// model_hung. Once the sub-agent returns, the model wait starts from that
// moment and is judged at X as before.
func TestParallelToolThenWorkingSubagentIsNeverModelHung(t *testing.T) {
	const X, Y = 10 * time.Minute, 30 * time.Minute
	for _, pc := range parallelCalls() {
		t.Run(pc.name, func(t *testing.T) {
			c := &fixClock{now: time.Unix(1000, 0)}
			tr := newTrackerAt(c, Thresholds{Silence: X, Tool: Y, StepTimeout: 24 * time.Hour})
			tl := pc.newTl(tr)
			feedAll(tl, pc.start)
			c.advance(8 * time.Second)
			feedAll(tl, pc.plainReturns)
			returned := c.now
			if n := len(tr.Snapshot(c.now).Tools); n != 1 {
				t.Fatalf("after the plain tool returned, the sub-agent call is the one running tool: %d running", n)
			}
			for i := 0; i < 15; i++ { // inner records 1m apart, 15m in total
				for _, at := range []time.Time{c.now, c.now.Add(time.Minute - time.Second)} {
					if v := Judge(tr.Snapshot(at), at, time.Minute); v != nil {
						t.Fatalf("the sub-agent still works, judged at +%s after the plain tool returned: %+v", at.Sub(returned), v)
					}
				}
				c.advance(time.Minute)
				feedAll(tl, pc.inner)
			}
			c.advance(time.Second)
			feedAll(tl, pc.subagentEnds)
			ended := c.now
			if at := ended.Add(X - time.Second); Judge(tr.Snapshot(at), at, time.Minute) != nil {
				t.Fatalf("the model wait starts when the last tool returned: healthy for X-1s, got %+v", Judge(tr.Snapshot(at), at, time.Minute))
			}
			at := ended.Add(X)
			v := Judge(tr.Snapshot(at), at, time.Minute)
			if v == nil || v.Cause != CauseModelHung {
				t.Fatalf("every tool returned and the model is silent for X: want model_hung, got %+v", v)
			}
			if !strings.Contains(v.Evidence, "waiting for the model for 10m0s") {
				t.Fatalf("model wait measured from the last tool's return: evidence %q", v.Evidence)
			}
		})
	}
}

// The same parallel turn, but the sub-agent stops producing records:
// rule 3 still catches it, tool_hung at Y after its last inner record.
func TestParallelToolThenSilentSubagentIsToolHung(t *testing.T) {
	const X, Y = 10 * time.Minute, 30 * time.Minute
	for _, pc := range parallelCalls() {
		t.Run(pc.name, func(t *testing.T) {
			c := &fixClock{now: time.Unix(1000, 0)}
			tr := newTrackerAt(c, Thresholds{Silence: X, Tool: Y, StepTimeout: 24 * time.Hour})
			tl := pc.newTl(tr)
			feedAll(tl, pc.start)
			c.advance(8 * time.Second)
			feedAll(tl, pc.plainReturns)
			for i := 0; i < 12; i++ { // works past X, then stops
				c.advance(time.Minute)
				feedAll(tl, pc.inner)
			}
			last := c.now
			for _, d := range []time.Duration{X, Y - time.Second} {
				if v := Judge(tr.Snapshot(last.Add(d)), last.Add(d), time.Minute); v != nil {
					t.Fatalf("silent for %s, less than Y: healthy, got %+v", d, v)
				}
			}
			v := Judge(tr.Snapshot(last.Add(Y)), last.Add(Y), time.Minute)
			if v == nil || v.Cause != CauseToolHung {
				t.Fatalf("the sub-agent silent for Y: want tool_hung, got %+v", v)
			}
			for _, want := range []string{pc.subagent, "silent for 30m0s", "last inner activity"} {
				if !strings.Contains(v.Evidence, want) {
					t.Fatalf("evidence %q does not name %q", v.Evidence, want)
				}
			}
		})
	}
}
