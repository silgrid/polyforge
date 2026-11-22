package task

// context_test.go: the task stream's context-usage adapter (polyforge#230
// AC1). The expected counts are hand-derived from the fold's written
// specification — the same one TestContextUsagePiSession and
// TestContextUsageClaudeSession state for a session transcript — never from
// the adapter's own output:
//
//   pi: the last valid reply's usage (input+output+cacheRead+cacheWrite when
//       totalTokens is absent) plus a chars/4 estimate of every message after
//       it;
//   claude: the last main-chain reply's input + cache_creation + cache_read +
//       output.
import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/sessionjsonl"
)

// fixedClock answers one instant for every call.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// feedAll feeds every line and answers the last usage.
func feedAll(c *contextTracker, lines []string) *protocol.ContextUsage {
	var u *protocol.ContextUsage
	for _, line := range lines {
		if got, _ := c.feed([]byte(line)); got != nil {
			u = got
		}
	}
	return u
}

func tokensOf(t *testing.T, u *protocol.ContextUsage) int64 {
	t.Helper()
	if u == nil || u.Tokens == nil {
		t.Fatalf("no tokens: %#v", u)
	}
	return *u.Tokens
}

// TestTaskContextPiMessageEnd: a pi task's message_end holds the finished
// assistant message (pi's own stream carries it, message.usage required), and
// it is wrapped into the transcript's `message` record. The first reply's
// usage is 1000+200+3000+0 = 4200 (totalTokens absent: the parts are summed);
// a following 400-char tool result adds ceil(400/4) = 100 → 4300.
func TestTaskContextPiMessageEnd(t *testing.T) {
	clock := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	c := newContextTracker(harnessPi)
	c.now = fixedClock(clock) // the receive time the wrapper stamps

	first := `{"type":"message_end","message":{"role":"assistant","provider":"openai","model":"m1","stopReason":"toolUse","content":[],"usage":{"input":1000,"output":200,"cacheRead":3000,"cacheWrite":0}}}`
	u, changed := c.feed([]byte(first))
	if !changed {
		t.Fatal("the assistant message_end was not a consumed record")
	}
	if got := tokensOf(t, u); got != 4200 {
		t.Fatalf("tokens = %d, want 4200 (1000+200+3000+0; totalTokens absent)", got)
	}
	if u.Model != "openai/m1" {
		t.Fatalf("model = %q, want openai/m1", u.Model)
	}
	if want := clock.Format(time.RFC3339Nano); u.AsOf != want {
		t.Fatalf("as_of = %q, want the receive time %q", u.AsOf, want)
	}

	tool := `{"type":"message_end","message":{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"` + strings.Repeat("a", 400) + `"}]}}`
	u, changed = c.feed([]byte(tool))
	if !changed {
		t.Fatal("the toolResult message_end was not a consumed record")
	}
	if got := tokensOf(t, u); got != 4300 {
		t.Fatalf("tokens = %d, want 4300 (4200 + ceil(400/4) = 100)", got)
	}
	if u.Model != "openai/m1" {
		t.Fatalf("model changed to %q", u.Model)
	}

	// A reply that ended in error does not become the usage: the last valid
	// value stands, with the failed reply's own text estimated after it.
	errReply := `{"type":"message_end","message":{"role":"assistant","provider":"openai","model":"m1","stopReason":"error","content":[{"type":"text","text":"` + strings.Repeat("b", 40) + `"}],"usage":{"input":9,"output":9,"cacheRead":9000,"cacheWrite":0}}}`
	u, _ = c.feed([]byte(errReply))
	if got := tokensOf(t, u); got != 4310 {
		t.Fatalf("after an errored reply: tokens = %d, want 4310 (4300 + ceil(40/4))", got)
	}
}

// TestTaskContextIgnoresPartialUsage: pi's message_update carries the usage
// of an in-flight call at its top level, not as a transcript record; a task's
// context must never be built from it (a partial value is not the fill of the
// conversation), so a stream of updates alone leaves the usage unknown.
func TestTaskContextIgnoresPartialUsage(t *testing.T) {
	c := newContextTracker(harnessPi)
	c.now = fixedClock(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC))
	for _, line := range []string{
		`{"type":"turn_start"}`,
		`{"type":"message_update","usage":{"input":5,"output":5,"cacheRead":0,"cacheWrite":0},"assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`,
		`{"type":"turn_end"}`,
		`not json at all`,
	} {
		if u, changed := c.feed([]byte(line)); changed || u != nil {
			t.Fatalf("%s: got usage %#v changed=%v, want none", line, u, changed)
		}
	}
	if u := c.usage(nil); u != nil {
		t.Fatalf("usage() = %#v, want nil", u)
	}
}

// TestTaskContextClaudeRecords: a claude stream-json record is
// transcript-shaped already but carries no timestamp; the daemon's receive
// time supplies it. Two records of the same message id (claude repeats a
// message's usage per content-block record) with 10+500+20+5 = 535, then a
// new id with 12+530+0+7 = 549: the last value stands (549). A sidechain
// record and an all-zero `<synthetic>` reply change nothing.
func TestTaskContextClaudeRecords(t *testing.T) {
	clock := time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC)
	c := newContextTracker(harnessClaude)
	c.now = fixedClock(clock)

	msg := func(id, model string, input, cacheCreate, cacheRead, output int) string {
		usage := map[string]any{"input_tokens": input, "cache_creation_input_tokens": cacheCreate, "cache_read_input_tokens": cacheRead, "output_tokens": output}
		content := []map[string]any{}
		raw, err := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{
			"id": id, "role": "assistant", "model": model, "content": content, "usage": usage}})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	u, changed := c.feed([]byte(msg("m1", "claude-model-x", 10, 20, 500, 5)))
	if !changed || tokensOf(t, u) != 535 {
		t.Fatalf("first reply: %#v, want 535", u)
	}
	if u.Model != "claude-model-x" || u.AsOf != clock.Format(time.RFC3339Nano) {
		t.Fatalf("model/as_of = %q/%q, want claude-model-x and the receive time", u.Model, u.AsOf)
	}
	u, _ = c.feed([]byte(msg("m1", "claude-model-x", 10, 20, 500, 5)))
	if got := tokensOf(t, u); got != 535 {
		t.Fatalf("the repeated record changed the value: %d", got)
	}
	u, _ = c.feed([]byte(msg("m2", "claude-model-x", 12, 0, 530, 7)))
	if got := tokensOf(t, u); got != 549 {
		t.Fatalf("second reply: %d, want 549 (12+0+530+7)", got)
	}

	sidechain, _ := json.Marshal(map[string]any{"type": "assistant", "isSidechain": true, "message": map[string]any{
		"id": "s1", "role": "assistant", "model": "claude-model-side", "content": []any{},
		"usage": map[string]any{"input_tokens": 900000, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0, "output_tokens": 1}}})
	if u, _ = c.feed(sidechain); tokensOf(t, u) != 549 || u.Model != "claude-model-x" {
		t.Fatalf("a sidechain record changed the value: %#v", u)
	}
	synthetic, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{
		"id": "m3", "role": "assistant", "model": "<synthetic>", "content": []any{},
		"usage": map[string]any{"input_tokens": 0, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0, "output_tokens": 0}}})
	if u, _ = c.feed(synthetic); tokensOf(t, u) != 549 || u.Model != "claude-model-x" {
		t.Fatalf("an all-zero synthetic reply changed the value: %#v", u)
	}
}

// TestTaskContextWindow: the fold is the session package's; the task face
// adds the harness-reported window of the session's model and the percent it
// fills (4300 / 200000 * 100 = 2.15), and nothing when no window was
// reported. A wire value carries the five keys only.
func TestTaskContextWindow(t *testing.T) {
	folded := sessionjsonl.Context{Tokens: 4300, TokensKnown: true, Model: "openai/m1", At: "2026-11-22T10:00:00Z"}
	u := contextUsage(folded, map[string]int64{"openai/m1": 200000})
	if u == nil || u.Tokens == nil || *u.Tokens != 4300 || u.Window == nil || *u.Window != 200000 {
		t.Fatalf("with a reported window: %#v", u)
	}
	if u.Percent == nil || *u.Percent < 2.149 || *u.Percent > 2.151 {
		t.Fatalf("percent = %#v, want 4300/200000*100 = 2.15", u.Percent)
	}
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	want := []string{"as_of", "model", "percent", "tokens", "window"}
	if len(keys) != len(want) {
		t.Fatalf("wire keys = %v, want exactly %v", keys, want)
	}
	for _, k := range want {
		if _, ok := keys[k]; !ok {
			t.Fatalf("wire keys = %v, want %v", keys, want)
		}
	}

	none := contextUsage(folded, nil)
	if none == nil || none.Window != nil || none.Percent != nil || *none.Tokens != 4300 {
		t.Fatalf("without a window: %#v, want tokens only", none)
	}
	if another := contextUsage(folded, map[string]int64{"other/model": 100000}); another.Window != nil {
		t.Fatalf("another model's window applied: %#v", another)
	}
	if unknown := contextUsage(sessionjsonl.Context{Model: "openai/m1"}, nil); unknown == nil || unknown.Tokens != nil {
		t.Fatalf("unknown tokens: %#v, want the model only", unknown)
	}
}