package task

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// fixtureLines answers the non-empty lines of one testdata stream fixture.
func fixtureLines(t *testing.T, name string) [][]byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var lines [][]byte
	for _, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestStreamParserPiChargesFreshTokensOnly pins the token budget's unit on
// the recorded pi research stream of the repro (acceptance 3):
// every usage-bearing message_update record of the 27-call capture, with a
// turn_start (the real record type) opening each call and the
// assistantMessageEvent payloads, which the parser does not count, elided.
// Type and usage are verbatim.
//
// Charging each call's upstream totalTokens, cacheRead included, sums to
// 2,102,358: that crossed the 300,000 default budget 56s into the run and
// killed the agent before it wrote its report. The fresh part of the same
// calls is 174,422, well under the default budget.
func TestStreamParserPiChargesFreshTokensOnly(t *testing.T) {
	p := NewStreamParser(harnessPi)
	for _, line := range fixtureLines(t, "pi-usage-stream.jsonl") {
		p.Feed(line)
	}
	if got, want := p.Tokens(), int64(174422); got != want {
		t.Fatalf("tokens = %d, want %d (fresh part only; the recorded stream's upstream totals sum to 2,102,358, cache reads included)", got, want)
	}
}

// TestStreamParserClaudeChargesFreshTokensOnly pins the same unit on real
// recorded claude records (acceptance 3): two distinct
// assistant messages from the captured claude stream-json run of the repro
// (the first emitted twice, as claude repeats one message's usage per
// content-block record) and the final result aggregate. Upstream cache
// reads are 1,283 + 1,498; the budget must count the fresh
// input + output + cache_creation of each call: 223 + 7,334 = 7,557.
func TestStreamParserClaudeChargesFreshTokensOnly(t *testing.T) {
	p := NewStreamParser(harnessClaude)
	for _, line := range fixtureLines(t, "claude-usage-records.jsonl") {
		p.Feed(line)
	}
	if got, want := p.Tokens(), int64(223+7334); got != want {
		t.Fatalf("tokens = %d, want %d (cache_read 1283+1498 must not be charged)", got, want)
	}
	if p.Text() == "" {
		t.Fatal("the result text must still be captured")
	}
}

func TestStreamParserClaude(t *testing.T) {
	p := NewStreamParser("claude")
	p.Feed([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":3}}}`))
	p.Feed([]byte(`{"type":"result","result":"final","usage":{"input_tokens":999,"output_tokens":999}}`))
	p.Feed([]byte("not json"))
	if p.Tokens() != 17 {
		t.Fatalf("tokens = %d, want 17 (input+output+cache_creation; the result aggregate must not double)", p.Tokens())
	}
	if p.Text() != "final" {
		t.Fatalf("text = %q", p.Text())
	}
}

func TestStreamParserClaudeResultOnlyUsage(t *testing.T) {
	p := NewStreamParser("claude")
	p.Feed([]byte(`{"type":"result","result":"x","usage":{"input_tokens":4,"output_tokens":1}}`))
	if p.Tokens() != 5 {
		t.Fatalf("tokens = %d", p.Tokens())
	}
}

func TestStreamParserPi(t *testing.T) {
	p := NewStreamParser("pi")
	p.Feed([]byte(`{"type":"turn_start"}`))
	p.Feed([]byte(`{"type":"message_update","usage":{"input":40,"output":0,"cacheRead":0,"cacheWrite":0},"assistantMessageEvent":{"type":"text_delta","delta":"a"}}`))
	p.Feed([]byte(`{"type":"message_update","usage":{"input":90,"output":10,"cacheRead":0,"cacheWrite":0},"assistantMessageEvent":{"type":"text_delta","delta":"b"}}`))
	if p.Tokens() != 100 {
		t.Fatalf("running tokens = %d", p.Tokens())
	}
	p.Feed([]byte(`{"type":"message_end","message":{"role":"user","content":"q"}}`))
	p.Feed([]byte(`{"type":"turn_start"}`))
	p.Feed([]byte(`{"type":"message_update","usage":{"input":30,"output":0,"cacheRead":500,"cacheWrite":0}}`))
	p.Feed([]byte(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"answer"}]}}`))
	if p.Tokens() != 130 {
		t.Fatalf("tokens = %d, want 130", p.Tokens())
	}
	if p.Text() != "answer" {
		t.Fatalf("text = %q", p.Text())
	}
}

func TestStreamParserClaudeDedupesByMessageID(t *testing.T) {
	p := NewStreamParser("claude")
	// One API message split over two content-block records repeats usage.
	p.Feed([]byte(`{"type":"assistant","message":{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"a"}],"usage":{"input_tokens":10,"output_tokens":5}}}`))
	p.Feed([]byte(`{"type":"assistant","message":{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"b"}],"usage":{"input_tokens":10,"output_tokens":5}}}`))
	p.Feed([]byte(`{"type":"assistant","message":{"id":"msg_2","role":"assistant","content":[{"type":"text","text":"c"}],"usage":{"input_tokens":2,"output_tokens":1}}}`))
	if p.Tokens() != 18 {
		t.Fatalf("tokens = %d, want 18 (msg_1 counted once)", p.Tokens())
	}
	if p.Text() != "a\n\nb\n\nc" {
		t.Fatalf("text = %q", p.Text())
	}
}

func TestSupportsUsage(t *testing.T) {
	if !SupportsUsage("pi") || !SupportsUsage("claude") || SupportsUsage("x") {
		t.Fatal("SupportsUsage closed set")
	}
}
