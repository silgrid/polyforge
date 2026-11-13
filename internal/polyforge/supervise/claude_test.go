package supervise

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"
)

// TestClaudeTranslatorReplay replays a hand-written stream-json session
// (polyforge#159 design §2.3) and pins the standard signals.
func TestClaudeTranslatorReplay(t *testing.T) {
	f, err := os.Open("testdata/claude-stream-replay.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sink := &recSink{}
	tl := NewClaude(sink)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		tl.Feed(sc.Bytes())
	}
	tl.Exit(ExitInfo{Code: 0})
	want := []string{
		"alive", "waiting", // init
		"alive", "alive", // message_start, content_block_start
		"output:thinking",
		"alive", // ping: life, never output
		"output:text",
		"output:toolcall",
		"alive",                                                // message_stop
		"alive", `tool_start:Bash:{"command":"go test ./..."}`, // assistant tool_use
		"tool_end:", "waiting", // tool_result → next request
		// the wait-call records (wait.go): a translator hands them on like
		// any tool call; the Tracker is what counts them
		"alive", `tool_start:Bash:{"command":"sleep 120 && gh pr checks 7","description":"wait for CI"}`,
		"tool_end:", "waiting",
		"alive", `tool_start:Bash:{"command":"for i in 1 2 3 4 5; do gh pr checks 7 && break; sleep 30; done"}`,
		"tool_end:", "waiting",
		"alive", `tool_start:Bash:{"command":"sleep 2 && git fetch origin"}`,
		"tool_end:", "waiting",
		"alive", `tool_start:Bash:{"command":"sleep 1m"}`,
		"tool_end:", "waiting",
		"alive", `tool_start:Agent:{"description":"review","prompt":"review the diff"}`,
		// sub-agent internals (its own sleep included): the Agent call's
		// inner activity, never tools of the step
		"tool_active:toolu_2", "tool_active:toolu_2", "tool_active:toolu_2",
		"tool_end:", "waiting",
		"alive", "waiting", "error:claude-code rate limit (429) (HTTP 429)", // api_retry
		"noise", "noise", "noise", // rate_limit_event, task_started, garbage
		"turn_end:true:",
		"exit:0",
	}
	if strings.Join(sink.log, "\n") != strings.Join(want, "\n") {
		t.Fatalf("signals\n got %q\nwant %q", sink.log, want)
	}
	if tl.Name() != "claude" || tl.Granularity() != GranularityToken {
		t.Fatal("name/granularity")
	}
}

func TestClaudeErrorResult(t *testing.T) {
	sink := &recSink{}
	tl := NewClaude(sink)
	tl.Feed([]byte(`{"type":"assistant","error":"rate_limit","message":{"content":[{"type":"text","text":"API Error: 429"}]}}`))
	tl.Feed([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"","api_error_status":429,"terminal_reason":"api_error"}`))
	got := sink.log[len(sink.log)-1]
	if got != "turn_end:false:claude-code rate limit (429) (HTTP 429, terminal_reason=api_error): API Error: 429" {
		t.Fatalf("result signal %q", got)
	}
}

func TestClaudeCanonicalError(t *testing.T) {
	cases := map[string]string{
		ClaudeCanonicalError("overloaded", 0, "", ""):                        "claude-code overloaded (529)",
		ClaudeCanonicalError("", 503, "", "x"):                               "claude-code upstream connection dropped (HTTP 5xx) (HTTP 503): x",
		ClaudeCanonicalError("", 0, "malformed_tool_use_exhausted", ""):      "claude-code malformed tool call arguments (terminal_reason=malformed_tool_use_exhausted)",
		ClaudeCanonicalError("invalid_request", 400, "prompt_too_long", "p"): "claude-code invalid_request (HTTP 400, terminal_reason=prompt_too_long): p",
		ClaudeCanonicalError("", 0, "", ""):                                  "claude-code error",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

// TestClaudeResultReportsContextWindow: a result's modelUsage names claude's
// own context window per model; the tracker keeps the latest
// positive report of each and its snapshot carries them. The turn signal
// still comes last.
func TestClaudeResultReportsContextWindow(t *testing.T) {
	c := &fixClock{now: time.Unix(1000, 0)}
	tr := newTrackerAt(c, Thresholds{Silence: time.Hour, Tool: time.Hour, StepTimeout: time.Hour})
	tl := NewClaude(tr)
	if w := tr.Snapshot(c.now).ContextWindows; len(w) != 0 {
		t.Fatalf("no result yet: windows %v, want none", w)
	}
	tl.Feed([]byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok","modelUsage":{"claude-model-x":{"inputTokens":10,"outputTokens":5,"contextWindow":200000},"claude-model-small":{"inputTokens":1,"contextWindow":0},"claude-model-odd":{"contextWindow":"big"}}}`))
	w := tr.Snapshot(c.now).ContextWindows
	if len(w) != 1 || w["claude-model-x"] != 200000 {
		t.Fatalf("after the first result: windows %v, want only claude-model-x=200000", w)
	}
	// a later result (an error turn reports it too) replaces the value
	tl.Feed([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom","modelUsage":{"claude-model-x":{"contextWindow":1000000}}}`))
	if w := tr.Snapshot(c.now).ContextWindows; len(w) != 1 || w["claude-model-x"] != 1000000 {
		t.Fatalf("after the second result: windows %v, want claude-model-x=1000000", w)
	}

	sink := &recSink{}
	NewClaude(sink).Feed([]byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok","modelUsage":{"claude-model-x":{"contextWindow":200000}}}`))
	if want := []string{"context_window:claude-model-x:200000", "turn_end:true:"}; strings.Join(sink.log, "|") != strings.Join(want, "|") {
		t.Fatalf("signals %v, want %v", sink.log, want)
	}
}
