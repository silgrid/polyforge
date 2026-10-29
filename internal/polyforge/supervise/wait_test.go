package supervise

// wait_test.go: the wait-call counter (wait.go) — the
// leading-sleep detector, the Tracker's counters and hand-out, and the
// replay of both harness shapes through their real translators: counted,
// and the verdict never changes because of it.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLeadingSleepDetection(t *testing.T) {
	long := "sleep 600 && " + strings.Repeat("x", 200)
	cases := []struct {
		name, tool, command string
		want                bool
		seconds             int64
	}{
		{"bare leading sleep", "bash", "sleep 60", true, 60},
		{"Claude Code's tool name", "Bash", "sleep 60", true, 60},
		{"followed by the check", "bash", "sleep 120 && gh pr checks 7", true, 120},
		{"separator without space", "bash", "sleep 90;make check", true, 90},
		{"newline ends the operands", "bash", "sleep 45\nmake check", true, 45},
		{"surrounding whitespace is trimmed", "bash", "\n\t  sleep 30 ", true, 30},
		{"seconds suffix", "bash", "sleep 5s", true, 5},
		{"minutes suffix", "bash", "sleep 1m", true, 60},
		{"hours suffix", "bash", "sleep 0.5h", true, 1800},
		{"operands sum as sleep(1) does", "bash", "sleep 1m 30s", true, 90},
		{"fraction above the floor rounds", "bash", "sleep 3.5", true, 4},
		{"tab after sleep", "bash", "sleep\t10", true, 10},
		{"trailing comment", "bash", "sleep 20 # let CI start", true, 20},
		{"a redirection's descriptor is not an operand", "bash", "sleep 30 2>/dev/null", true, 30},
		{"stdout descriptor", "bash", "sleep 20 1>out.log", true, 20},
		{"descriptor redirect, then a pipe", "bash", "sleep 30 2>&1 | tee wait.log", true, 30},
		{"input descriptor", "bash", "sleep 15 0</dev/null", true, 15},
		{"redirection without a descriptor", "bash", "sleep 45 >/dev/null", true, 45},
		{"a suffixed operand glued to a redirection stays an operand", "bash", "sleep 30s>/dev/null", true, 30},
		{"argv excerpt is cut, the sleep still counts", "bash", long, true, 600},

		{"the floor itself is not a wait call", "bash", "sleep 3", false, 0},
		{"below the floor", "bash", "sleep 2 && git fetch", false, 0},
		{"zero", "bash", "sleep 0", false, 0},
		{"in a condition loop (legal)", "bash", "until curl -sf localhost:8080; do sleep 30; done", false, 0},
		{"in a for loop (legal)", "bash", "for i in 1 2 3; do make check && break; sleep 60; done", false, 0},
		{"not the first word", "bash", "cd build && sleep 45", false, 0},
		{"in a subshell", "bash", "(sleep 60; make check)", false, 0},
		{"shell-evaluated operand", "bash", "sleep $WAIT", false, 0},
		{"one bad operand refuses all", "bash", "sleep 60 later", false, 0},
		{"no operand", "bash", "sleep", false, 0},
		{"a lone number glued to a redirection is its descriptor", "bash", "sleep 30>/dev/null", false, 0},
		{"no operand before the separator", "bash", "sleep && make", false, 0},
		{"glued operand", "bash", "sleep60", false, 0},
		{"another command named like it", "bash", "sleepy 60", false, 0},
		{"signed operand", "bash", "sleep +60", false, 0},
		{"exponent operand", "bash", "sleep 1e3", false, 0},
		{"infinity", "bash", "sleep infinity", false, 0},
		{"not a bash tool", "read", "sleep 60", false, 0},
	}
	at := time.Unix(1000, 0)
	for _, tc := range cases {
		w, ok := detectWaitCall("c1", tc.tool, tc.command, at)
		if ok != tc.want {
			t.Errorf("%s: %q detected=%v, want %v", tc.name, tc.command, ok, tc.want)
			continue
		}
		if !ok {
			continue
		}
		if w.Seconds != tc.seconds || w.ToolCallID != "c1" || !w.At.Equal(at) {
			t.Errorf("%s: %+v, want %ds from tool call c1", tc.name, w, tc.seconds)
		}
		if n := len([]rune(w.Argv)); n > maxWaitArgv || !strings.HasPrefix(strings.TrimSpace(tc.command), w.Argv) {
			t.Errorf("%s: argv excerpt %q (%d chars) must be the trimmed command's first %d at most", tc.name, w.Argv, n, maxWaitArgv)
		}
	}
	if w, _ := detectWaitCall("c", "bash", "sleep 9 # "+strings.Repeat("é", 300), at); len([]rune(w.Argv)) != maxWaitArgv {
		t.Fatalf("a long multi-byte command is cut at %d characters, got %d", maxWaitArgv, len([]rune(w.Argv)))
	}
	if w, ok := detectWaitCall("c", "bash", "sleep 99999999999999999999", at); !ok || w.Seconds != maxWaitSeconds {
		t.Fatalf("an oversized operand is capped, got %+v ok=%v", w, ok)
	}
}

func TestTrackerCountsAndHandsOutWaitCallsOnce(t *testing.T) {
	c := &fixClock{now: time.Unix(1000, 0)}
	tr := newTrackerAt(c, Thresholds{})
	select {
	case <-tr.WaitCallNotify():
		t.Fatal("no wait call folded yet: no wake-up")
	default:
	}
	tr.ToolStart("c1", "bash", `{"command":"sleep 120 && gh pr checks 7"}`)
	c.advance(2 * time.Minute)
	tr.ToolEnd("c1", "bash")
	tr.ToolStart("c2", "bash", `{"command":"until make check; do sleep 30; done"}`)
	tr.ToolEnd("c2", "bash")
	tr.ToolStart("c3", "bash", `{"argv":["sleep","45"]}`)
	tr.ToolEnd("c3", "bash")
	s := tr.Snapshot(c.now)
	if s.WaitCalls != 2 || s.WaitSeconds != 165 || s.ToolCalls != 3 {
		t.Fatalf("snapshot wait calls %d (%ds), tool calls %d; want 2 (165s) of 3", s.WaitCalls, s.WaitSeconds, s.ToolCalls)
	}
	select {
	case <-tr.WaitCallNotify():
	default:
		t.Fatal("a folded wait call must wake the taker")
	}
	got := tr.TakeWaitCalls()
	if len(got) != 2 || got[0].ToolCallID != "c1" || got[0].WaitCalls != 1 || got[0].WaitSeconds != 120 ||
		got[1].ToolCallID != "c3" || got[1].Seconds != 45 || got[1].WaitCalls != 2 || got[1].WaitSeconds != 165 ||
		!got[0].At.Equal(time.Unix(1000, 0)) || got[0].Argv != "sleep 120 && gh pr checks 7" {
		t.Fatalf("taken wait calls %+v", got)
	}
	if again := tr.TakeWaitCalls(); len(again) != 0 {
		t.Fatalf("a wait call is handed out once, got it again: %+v", again)
	}
	// a taker that never comes: the pending list is bounded, the counters
	// are not
	for i := 0; i < maxPendingWaitCalls+10; i++ {
		tr.ToolStart("x", "bash", `{"command":"sleep 4"}`)
	}
	if n := len(tr.TakeWaitCalls()); n != maxPendingWaitCalls {
		t.Fatalf("pending wait calls %d, want the bound %d", n, maxPendingWaitCalls)
	}
	if s := tr.Snapshot(c.now); s.WaitCalls != 2+maxPendingWaitCalls+10 {
		t.Fatalf("counter %d stopped at the pending bound", s.WaitCalls)
	}
}

// readFixtureLines reads one translator replay fixture.
func readFixtureLines(t *testing.T, name string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return splitJSONLines(raw)
}

// TestWaitCallsReplayBothHarnessShapes replays a pi --mode rpc stream and
// a Claude Code stream-json stream, each carrying leading sleeps, a sleep
// in a condition loop, a sleep under the floor and a sleep that is not the
// first word (and, for Claude, a sub-agent's own sleep), through the real
// translators into a Tracker: only the leading sleeps above the floor
// count. A twin replay with every `sleep` renamed counts nothing, and the
// two are judged alike at every tick — including the ticks where a rule
// does fire — so the counter changes no verdict.
func TestWaitCallsReplayBothHarnessShapes(t *testing.T) {
	cases := []struct {
		fixture string
		newTl   func(Sink) Translator
		// late starts one more leading sleep, in the harness's own shape,
		// after the fixture: it is still running when Y runs out
		late  string
		calls []WaitCall // ToolCallID, Seconds, Argv
	}{
		{"pi-rpc-waitcall.jsonl", func(s Sink) Translator { return NewPi(s) },
			`{"type":"tool_execution_start","toolCallId":"late","toolName":"bash","args":{"command":"sleep 3600"}}`,
			[]WaitCall{
				{ToolCallID: "call_w1", Seconds: 300, Argv: "sleep 300 && gh run view 42 --json status"},
				{ToolCallID: "call_w3", Seconds: 90, Argv: "sleep 1m 30s\n  make check"},
				{ToolCallID: "late", Seconds: 3600, Argv: "sleep 3600"},
			}},
		{"claude-stream-replay.jsonl", func(s Sink) Translator { return NewClaude(s) },
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"late","name":"Bash","input":{"command":"sleep 3600"}}]},"parent_tool_use_id":null}`,
			[]WaitCall{
				{ToolCallID: "toolu_w1", Seconds: 120, Argv: "sleep 120 && gh pr checks 7"},
				{ToolCallID: "toolu_w4", Seconds: 60, Argv: "sleep 1m"},
				{ToolCallID: "late", Seconds: 3600, Argv: "sleep 3600"},
			}},
	}
	def := Defaults{}.Fill()
	th := Thresholds{Silence: DefaultSilence(GranularityToken), Tool: def.Tool, StepTimeout: def.StepTimeout}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			c := &fixClock{now: time.Unix(1000, 0)}
			tr, twin := newTrackerAt(c, th), newTrackerAt(c, th)
			tl, twinTl := tc.newTl(tr), tc.newTl(twin)
			fired := 0
			judgeAlike := func(at time.Time) {
				t.Helper()
				a, b := Judge(tr.Snapshot(at), at, def.ModelErrorGrace), Judge(twin.Snapshot(at), at, def.ModelErrorGrace)
				if (a == nil) != (b == nil) || (a != nil && a.Cause != b.Cause) {
					t.Fatalf("at +%s the wait calls changed the verdict: %+v vs the twin's %+v", at.Sub(time.Unix(1000, 0)), a, b)
				}
				// and exactly: the counters never reach Judge
				s := tr.Snapshot(at)
				s.WaitCalls, s.WaitSeconds = 0, 0
				if z := Judge(s, at, def.ModelErrorGrace); !reflect.DeepEqual(a, z) {
					t.Fatalf("Judge read the wait-call counters: %+v vs %+v", a, z)
				}
				if a != nil {
					fired++
				}
			}
			feed := func(line []byte) {
				c.advance(time.Second)
				tl.Feed(line)
				twinTl.Feed(bytes.ReplaceAll(line, []byte("sleep"), []byte("pause")))
				for _, ahead := range []time.Duration{0, th.Silence, th.Tool, th.StepTimeout} {
					judgeAlike(c.now.Add(ahead))
				}
			}
			for _, line := range readFixtureLines(t, tc.fixture) {
				feed(line)
			}
			feed([]byte(tc.late))
			// a leading sleep that outlives Y is tool_hung like any tool
			at := c.now.Add(th.Tool)
			if v := Judge(tr.Snapshot(at), at, def.ModelErrorGrace); v == nil || v.Cause != CauseToolHung {
				t.Fatalf("a leading sleep running past Y: %+v, want tool_hung like any tool", v)
			}
			if fired == 0 {
				t.Fatal("no rule fired at any tick: the verdict comparison asserts nothing")
			}

			s := tr.Snapshot(c.now)
			var wantSecs int64
			for _, w := range tc.calls {
				wantSecs += w.Seconds
			}
			got := tr.TakeWaitCalls()
			if s.WaitCalls != len(tc.calls) || s.WaitSeconds != wantSecs {
				t.Fatalf("wait calls %d (%ds), want %d (%ds)", s.WaitCalls, s.WaitSeconds, len(tc.calls), wantSecs)
			}
			if len(got) != len(tc.calls) {
				t.Fatalf("taken %+v, want %+v", got, tc.calls)
			}
			for i, w := range tc.calls {
				if got[i].ToolCallID != w.ToolCallID || got[i].Seconds != w.Seconds || got[i].Argv != w.Argv || got[i].WaitCalls != i+1 {
					t.Fatalf("wait call %d = %+v, want %+v", i, got[i], w)
				}
			}
			if ts := twin.Snapshot(c.now); ts.WaitCalls != 0 || ts.ToolCalls != s.ToolCalls {
				t.Fatalf("twin counted %d wait calls of %d tools (orig %d tools)", ts.WaitCalls, ts.ToolCalls, s.ToolCalls)
			}
		})
	}
}

// bashCounter counts the bash tool starts passing through to a Sink.
type bashCounter struct {
	Sink
	bash int
}

func (b *bashCounter) ToolStart(id, name, args string) {
	if isBashTool(name) {
		b.bash++
	}
	b.Sink.ToolStart(id, name, args)
}

// TestWaitCallsRealPiSessionHasNone: the real collected pi session stream
// (the D15 fixture) runs many bash calls and waits with none of them, so
// it counts no wait call — the detector does not fire on real ordinary
// commands.
func TestWaitCallsRealPiSessionHasNone(t *testing.T) {
	raw, err := os.ReadFile(replayFixturePath(t))
	if err != nil {
		t.Fatalf("the D15 replay fixture is missing or unreadable (%v)", err)
	}
	clock := &replayClock{}
	var sink *bashCounter
	bash, waits := 0, 0
	flush := func() {
		if sink != nil {
			bash += sink.bash
			waits += sink.Sink.(*Tracker).Snapshot(clock.now).WaitCalls
		}
	}
	for _, line := range splitJSONLines(raw) {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &kind); err != nil {
			t.Fatalf("replay record is not valid JSON: %v", err)
		}
		if kind.Type == "session" {
			flush()
			sink = &bashCounter{Sink: newTracker(1, clock.get)}
		}
		if sink == nil {
			continue
		}
		clock.now = recordTime(t, line)
		replaySignal(t, sink, line)
	}
	flush()
	if bash < 100 {
		t.Fatalf("the real stream replayed only %d bash calls: too few to say anything", bash)
	}
	if waits != 0 {
		t.Fatalf("the real stream has no leading-sleep call, yet %d wait calls were counted over %d bash calls", waits, bash)
	}
}
