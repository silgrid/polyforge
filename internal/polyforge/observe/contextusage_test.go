package observe

// contextusage_test.go: the status and watch faces read the current
// dispatch's step-session context usage, from the session
// transcript the dispatch's harness writes, and follow the dispatch.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

const ctxTS = "2026-01-02T03:04:05.000Z"

// piUsageLine is a pi reply whose usage totals tokens.
func piUsageLine(tokens int64) string {
	return fmt.Sprintf(`{"type":"message","id":"r1","timestamp":%q,"message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"provider":"example-gateway","model":"model-a","usage":{"input":%d,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":%d},"stopReason":"stop"}}`+"\n", ctxTS, tokens, tokens)
}

// claudeUsageLine is a main-chain claude reply counting tokens (all input).
func claudeUsageLine(model string, tokens int64) string {
	return fmt.Sprintf(`{"type":"assistant","isSidechain":false,"uuid":"a1","timestamp":%q,"message":{"id":"m1","role":"assistant","model":%q,"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`+"\n", ctxTS, model, tokens)
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		t.Fatal(err)
	}
}

// ctxIs reports whether c reads tokens/model/as_of exactly.
func ctxIs(c *protocol.ContextUsage, tokens int64, model string) bool {
	return c != nil && c.Tokens != nil && *c.Tokens == tokens && c.Model == model && c.AsOf == ctxTS
}

func TestContextUsageFollowsTheDispatch(t *testing.T) {
	dir := t.TempDir()
	piPath := filepath.Join(dir, "pi-session.jsonl")
	claudePath := filepath.Join(dir, "claude-session.jsonl")
	writeFile(t, piPath, piUsageLine(1200))
	writeFile(t, claudePath, claudeUsageLine("claude-model-x", 3215))
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, func(c *monitorCfg) {
		c.locateLive = func(capsuleDir string, spawnAt time.Time, harness, session string) string {
			switch {
			case harness == "pi":
				return piPath
			case harness == "claude" && session == "sess-2":
				return claudePath
			}
			return ""
		}
	})
	th := supervise.Thresholds{Silence: time.Hour, Tool: time.Hour, StepTimeout: time.Hour}
	both := func(what string, ok func(*protocol.ContextUsage) bool) {
		t.Helper()
		soon(t, 5*time.Second, what, func() bool {
			return ok(m.activity().Context) && ok(m.liveProgress().Context)
		})
	}

	m.link.Fact(supervise.StepStarted{Step: "build", SpawnN: 1, Model: "model-a", Harness: "pi", StartedAt: m.link.Clock(), Thresholds: th})
	both("dispatch 1 reads its pi session", func(c *protocol.ContextUsage) bool { return ctxIs(c, 1200, "example-gateway/model-a") })

	// 40 characters after the last usage: pi's estimate adds 10.
	appendFile(t, piPath, `{"type":"message","id":"t1","timestamp":"`+ctxTS+`","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"0123456789012345678901234567890123456789"}]}}`+"\n")
	both("an appended record is read on the next read", func(c *protocol.ContextUsage) bool { return ctxIs(c, 1210, "example-gateway/model-a") })

	// The next step's dispatch reads its own (claude) session.
	m.link.Fact(supervise.StepStarted{Step: "review", SpawnN: 2, Model: "claude-model-x", Harness: "claude", HarnessSession: "sess-2", StartedAt: m.link.Clock(), Thresholds: th})
	both("dispatch 2 reads the claude session", func(c *protocol.ContextUsage) bool { return ctxIs(c, 3215, "claude-model-x") })

	m.link.Fact(supervise.StepEnded{Step: "review", SpawnN: 2, Outcome: supervise.OutcomeResult})
	both("no dispatch: no context", func(c *protocol.ContextUsage) bool { return c == nil })
}

func TestContextUsageUnknownWithoutTranscript(t *testing.T) {
	th := supervise.Thresholds{Silence: time.Hour, Tool: time.Hour, StepTimeout: time.Hour}
	for _, tc := range []struct {
		name   string
		locate func(string, time.Time, string, string) string
	}{
		{"nothing located", func(string, time.Time, string, string) string { return "" }},
		{"no locator", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMonitor(t, newFakeHandle("att_1"), func(c *monitorCfg) { c.locateLive = tc.locate })
			m.link.Fact(supervise.StepStarted{Step: "build", SpawnN: 1, Model: "m", Harness: "pi", StartedAt: m.link.Clock(), Thresholds: th})
			soon(t, 5*time.Second, "dispatch adopted", func() bool { return m.activity().InDispatch && m.liveProgress().InDispatch })
			if c := m.activity().Context; c != nil {
				t.Fatalf("activity context without a transcript: %+v, want nil", c)
			}
			if c := m.liveProgress().Context; c != nil {
				t.Fatalf("progress context without a transcript: %+v, want nil", c)
			}
		})
	}
}

// TestContextUsagePercentFromReportedWindow: when the dispatch's harness
// reported its model's context window (claude's result.modelUsage, through
// the tracker), the reading carries the window and the percent; another
// model's window does not apply, and unknown tokens leave the percent out.
func TestContextUsagePercentFromReportedWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-session.jsonl")
	writeFile(t, path, claudeUsageLine("claude-model-x", 50000))
	m, _ := newTestMonitor(t, newFakeHandle("att_1"), func(c *monitorCfg) {
		c.locateLive = func(string, time.Time, string, string) string { return path }
	})
	th := supervise.Thresholds{Silence: time.Hour, Tool: time.Hour, StepTimeout: time.Hour}
	m.link.Fact(supervise.StepStarted{Step: "build", SpawnN: 1, Model: "claude-model-x", Harness: "claude", HarnessSession: "sess-1", StartedAt: m.link.Clock(), Thresholds: th})
	both := func(what string, ok func(*protocol.ContextUsage) bool) {
		t.Helper()
		soon(t, 5*time.Second, what, func() bool {
			return ok(m.activity().Context) && ok(m.liveProgress().Context)
		})
	}
	both("no window reported yet", func(c *protocol.ContextUsage) bool {
		return ctxIs(c, 50000, "claude-model-x") && c.Window == nil && c.Percent == nil
	})

	tr := m.link.Tracker(1)
	tr.ContextWindow("claude-model-other", 100000)
	both("another model's window does not apply", func(c *protocol.ContextUsage) bool {
		return ctxIs(c, 50000, "claude-model-x") && c.Window == nil && c.Percent == nil
	})

	tr.ContextWindow("claude-model-x", 200000)
	both("the reported window and the percent", func(c *protocol.ContextUsage) bool {
		return ctxIs(c, 50000, "claude-model-x") && c.Window != nil && *c.Window == 200000 && c.Percent != nil && *c.Percent == 25
	})

	// a compaction that reported nothing: the window stays, no percent
	appendFile(t, path, `{"type":"system","subtype":"compact_boundary","isSidechain":false,"uuid":"b1","timestamp":"`+ctxTS+`","compactMetadata":{"trigger":"auto","preTokens":50000}}`+"\n")
	both("unknown tokens: the window only", func(c *protocol.ContextUsage) bool {
		return c != nil && c.Tokens == nil && c.Percent == nil && c.Window != nil && *c.Window == 200000 && c.Model == "claude-model-x"
	})
}
