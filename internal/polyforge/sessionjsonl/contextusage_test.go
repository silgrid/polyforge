package sessionjsonl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSession writes lines (each newline-terminated) to a fresh transcript.
func writeSession(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// readFresh reads the whole of lines with a new reader.
func readFresh(t *testing.T, harness string, lines []string) Context {
	t.Helper()
	return NewContextReader(harness).Read(writeSession(t, lines))
}

func ts(sec string) string { return "2026-01-02T03:04:" + sec + ".000Z" }

// A pi step session: model_change, a user prompt, two replies with usage and a
// trailing tool result; then replies that carry no valid usage, a compaction, and
// a reply after it.
var piLines = []string{
	`{"type":"session","version":3,"id":"s1","timestamp":"` + ts("00") + `","cwd":"/tmp/capsule"}`,
	`{"type":"model_change","id":"e1","parentId":null,"timestamp":"` + ts("01") + `","provider":"example-gateway","modelId":"model-a"}`,
	// "hello" → ceil(5/4) = 2 estimated tokens.
	`{"type":"message","id":"e2","parentId":"e1","timestamp":"` + ts("02") + `","message":{"role":"user","content":[{"type":"text","text":"hello"}],"timestamp":1}}`,
	`{"type":"message","id":"e3","parentId":"e2","timestamp":"` + ts("03") + `","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"a"}}],"provider":"example-gateway","model":"model-a","usage":{"input":100,"output":20,"cacheRead":0,"cacheWrite":0,"totalTokens":120},"stopReason":"toolUse","timestamp":2}}`,
	`{"type":"message","id":"e4","parentId":"e3","timestamp":"` + ts("04") + `","message":{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"abcd"}],"timestamp":3}}`,
	// No totalTokens: the four parts sum to 1600.
	`{"type":"message","id":"e5","parentId":"e4","timestamp":"` + ts("05") + `","message":{"role":"assistant","content":[{"type":"toolCall","id":"c2","name":"bash","arguments":{"command":"ls"}}],"provider":"example-gateway","model":"model-a","usage":{"input":1000,"output":10,"cacheRead":500,"cacheWrite":90,"totalTokens":0},"stopReason":"toolUse","timestamp":4}}`,
	// 40 characters → 10 estimated tokens after the last usage.
	`{"type":"message","id":"e6","parentId":"e5","timestamp":"` + ts("06") + `","message":{"role":"toolResult","toolCallId":"c2","toolName":"bash","content":[{"type":"text","text":"` + strings.Repeat("x", 40) + `"}],"timestamp":5}}`,
}

var piInvalidUsage = []string{
	`{"type":"message","id":"e7","parentId":"e6","timestamp":"` + ts("07") + `","message":{"role":"assistant","content":[],"provider":"example-gateway","model":"model-a","usage":{"input":90000,"output":9,"cacheRead":0,"cacheWrite":0,"totalTokens":90009},"stopReason":"error","timestamp":6}}`,
	`{"type":"message","id":"e8","parentId":"e7","timestamp":"` + ts("08") + `","message":{"role":"assistant","content":[],"provider":"example-gateway","model":"model-a","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0},"stopReason":"stop","timestamp":7}}`,
}

var piCompaction = []string{
	`{"type":"compaction","id":"e9","parentId":"e8","timestamp":"` + ts("09") + `","summary":"the story so far","firstKeptEntryId":"e6","tokensBefore":1610}`,
	`{"type":"message","id":"e10","parentId":"e9","timestamp":"` + ts("10") + `","message":{"role":"user","content":[{"type":"text","text":"go on"}],"timestamp":8}}`,
}

var piAfterCompaction = []string{
	`{"type":"message","id":"e11","parentId":"e10","timestamp":"` + ts("11") + `","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"provider":"example-gateway","model":"model-a","usage":{"input":300,"output":30,"cacheRead":70,"cacheWrite":0,"totalTokens":400},"stopReason":"stop","timestamp":9}}`,
}

func concat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestContextUsagePiSession(t *testing.T) {
	// Before any reply: the estimate of every message, the model from model_change.
	if got, want := readFresh(t, "pi", piLines[:3]), (Context{Tokens: 2, TokensKnown: true, Model: "example-gateway/model-a", At: ts("02")}); got != want {
		t.Fatalf("before any usage: got %+v, want %+v", got, want)
	}

	// The last valid usage (1600, summed when totalTokens is 0) plus the trailing estimate (10).
	want := Context{Tokens: 1610, TokensKnown: true, Model: "example-gateway/model-a", At: ts("06")}
	if got := readFresh(t, "pi", piLines); got != want {
		t.Fatalf("after two replies and a tool result: got %+v, want %+v", got, want)
	}

	// An errored reply and an all-zero usage change nothing.
	if got := readFresh(t, "pi", concat(piLines, piInvalidUsage)); !got.TokensKnown || got.Tokens != 1610 || got.Model != want.Model {
		t.Fatalf("an error reply / zero usage changed the value: got %+v, want tokens 1610", got)
	}

	// After a compaction with no reply since: tokens unknown, the model still known.
	got := readFresh(t, "pi", concat(piLines, piInvalidUsage, piCompaction))
	if got.TokensKnown || got.Tokens != 0 {
		t.Fatalf("after compaction without a reply: got %+v, want tokens unknown", got)
	}
	if got.Model != "example-gateway/model-a" {
		t.Fatalf("after compaction: model %q, want example-gateway/model-a", got.Model)
	}

	// A valid reply after the compaction makes it known again.
	want = Context{Tokens: 400, TokensKnown: true, Model: "example-gateway/model-a", At: ts("11")}
	if got := readFresh(t, "pi", concat(piLines, piInvalidUsage, piCompaction, piAfterCompaction)); got != want {
		t.Fatalf("after a reply following the compaction: got %+v, want %+v", got, want)
	}
}

var claudeLines = []string{
	`{"type":"user","isSidechain":false,"uuid":"u1","timestamp":"` + ts("00") + `","message":{"role":"user","content":"hi"}}`,
	`{"type":"assistant","isSidechain":false,"uuid":"a1","timestamp":"` + ts("01") + `","message":{"id":"m1","role":"assistant","model":"claude-model-x","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":10,"cache_creation_input_tokens":200,"cache_read_input_tokens":3000,"output_tokens":5}}}`,
	`{"type":"assistant","isSidechain":true,"uuid":"s1","timestamp":"` + ts("02") + `","message":{"id":"m2","role":"assistant","model":"claude-model-side","content":[{"type":"text","text":"y"}],"usage":{"input_tokens":900000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}}}`,
	`{"type":"assistant","isSidechain":false,"uuid":"a2","timestamp":"` + ts("03") + `","message":{"id":"m3","role":"assistant","model":"claude-model-x","content":[{"type":"text","text":"z"}],"usage":{"input_tokens":20,"cache_creation_input_tokens":100,"cache_read_input_tokens":4000,"output_tokens":50}}}`,
	`{"type":"assistant","isSidechain":false,"uuid":"a3","timestamp":"` + ts("04") + `","message":{"id":"m4","role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"API error"}],"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`,
}

var claudeCompact = []string{
	`{"type":"system","subtype":"compact_boundary","isSidechain":false,"uuid":"b1","timestamp":"` + ts("05") + `","compactMetadata":{"trigger":"auto","preTokens":4170,"postTokens":900}}`,
}

var claudeCompactNoReport = []string{
	`{"type":"system","subtype":"compact_boundary","isSidechain":false,"uuid":"b2","timestamp":"` + ts("06") + `","compactMetadata":{"trigger":"auto","preTokens":5000}}`,
}

func TestContextUsageClaudeSession(t *testing.T) {
	// The last main-chain reply's four parts (20+100+4000+50); the sidechain and the
	// all-zero synthetic reply are not counted.
	want := Context{Tokens: 4170, TokensKnown: true, Model: "claude-model-x", At: ts("03")}
	if got := readFresh(t, "claude", claudeLines); got != want {
		t.Fatalf("main chain: got %+v, want %+v", got, want)
	}

	// A compaction boundary's postTokens replaces it.
	want = Context{Tokens: 900, TokensKnown: true, Model: "claude-model-x", At: ts("05")}
	if got := readFresh(t, "claude", concat(claudeLines, claudeCompact)); got != want {
		t.Fatalf("after compact_boundary: got %+v, want %+v", got, want)
	}

	// A boundary that reported nothing leaves the tokens unknown.
	got := readFresh(t, "claude", concat(claudeLines, claudeCompact, claudeCompactNoReport))
	if got.TokensKnown || got.Tokens != 0 || got.Model != "claude-model-x" {
		t.Fatalf("after a boundary without postTokens: got %+v, want tokens unknown, model claude-model-x", got)
	}
}

func TestContextUsageUnknown(t *testing.T) {
	dir := t.TempDir()
	if got := NewContextReader("pi").Read(filepath.Join(dir, "missing.jsonl")); got != (Context{}) {
		t.Fatalf("missing pi file: got %+v, want the zero value", got)
	}
	if got := NewContextReader("claude").Read(filepath.Join(dir, "missing.jsonl")); got != (Context{}) {
		t.Fatalf("missing claude file: got %+v, want the zero value", got)
	}
	empty := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"pi", "claude"} {
		if got := NewContextReader(h).Read(empty); got != (Context{}) {
			t.Fatalf("%s empty file: got %+v, want the zero value", h, got)
		}
	}
	noReply := []string{
		claudeLines[0],
		`{"type":"system","subtype":"init","isSidechain":false,"uuid":"y1","timestamp":"` + ts("01") + `","content":"started"}`,
	}
	if got := readFresh(t, "claude", noReply); got != (Context{}) {
		t.Fatalf("claude session without replies: got %+v, want the zero value", got)
	}
}

func TestContextUsageModelIsSanitized(t *testing.T) {
	bad := []string{
		`{"type":"assistant","isSidechain":false,"uuid":"a1","timestamp":"` + ts("01") + `","message":{"role":"assistant","model":"model with spaces\n","usage":{"input_tokens":1,"output_tokens":1}}}`,
	}
	got := readFresh(t, "claude", bad)
	if got.Model != "" || got.Tokens != 2 {
		t.Fatalf("bad model id: got %+v, want empty model and tokens 2", got)
	}
	long := []string{
		`{"type":"assistant","isSidechain":false,"uuid":"a1","timestamp":"` + ts("01") + `","message":{"role":"assistant","model":"` + strings.Repeat("m", 129) + `","usage":{"input_tokens":1,"output_tokens":1}}}`,
	}
	if got := readFresh(t, "claude", long); got.Model != "" {
		t.Fatalf("over-long model id: got %q, want empty", got.Model)
	}
}

func TestContextUsageReadsOnlyAppendedBytes(t *testing.T) {
	for _, tc := range []struct {
		harness string
		lines   []string
	}{
		{"pi", concat(piLines, piInvalidUsage)},
		{"claude", concat(claudeLines, claudeCompact)},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			full := strings.Join(tc.lines, "\n") + "\n"
			want := readFresh(t, tc.harness, tc.lines)

			// The first half ends mid-line: a torn record still being written.
			cut := len(full) / 2
			for full[cut-1] == '\n' {
				cut++
			}
			path := filepath.Join(t.TempDir(), "live.jsonl")
			if err := os.WriteFile(path, []byte(full[:cut]), 0o644); err != nil {
				t.Fatal(err)
			}
			r := NewContextReader(tc.harness)
			r.Read(path)

			// Blank out what was already read (same length) and append the rest: a
			// reader that re-read the file from the start would now see nothing of
			// the first half.
			consumed := strings.LastIndexByte(full[:cut], '\n') + 1
			rewritten := strings.Repeat(" ", consumed-1) + "\n" + full[consumed:]
			if err := os.WriteFile(path, []byte(rewritten), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := r.Read(path); got != want {
				t.Fatalf("incremental read: got %+v, want %+v (the one-shot read)", got, want)
			}

			// Another path starts over.
			other := writeSession(t, tc.lines[:2])
			if got, fresh := r.Read(other), readFresh(t, tc.harness, tc.lines[:2]); got != fresh {
				t.Fatalf("after a path change: got %+v, want %+v", got, fresh)
			}
		})
	}
}
