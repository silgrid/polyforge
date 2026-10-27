package task

import "testing"

func TestStreamParserClaude(t *testing.T) {
	p := NewStreamParser("claude")
	p.Feed([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":3}}}`))
	p.Feed([]byte(`{"type":"result","result":"final","usage":{"input_tokens":999,"output_tokens":999}}`))
	p.Feed([]byte("not json"))
	if p.Tokens() != 20 {
		t.Fatalf("tokens = %d, want 20 (result aggregate must not double)", p.Tokens())
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
	p.Feed([]byte(`{"type":"message_update","usage":{"totalTokens":40},"assistantMessageEvent":{"type":"text_delta","delta":"a"}}`))
	p.Feed([]byte(`{"type":"message_update","usage":{"totalTokens":100},"assistantMessageEvent":{"type":"text_delta","delta":"b"}}`))
	if p.Tokens() != 100 {
		t.Fatalf("running tokens = %d", p.Tokens())
	}
	p.Feed([]byte(`{"type":"message_end","message":{"role":"user","content":"q"}}`))
	p.Feed([]byte(`{"type":"turn_start"}`))
	p.Feed([]byte(`{"type":"message_update","usage":{"totalTokens":30}}`))
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
