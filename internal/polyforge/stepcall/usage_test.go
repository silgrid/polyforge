package stepcall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeUsageFixture(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUsageSummaryPiAndClaude(t *testing.T) {
	pi := writeUsageFixture(t, `{"type":"message","message":{"role":"assistant","usage":{"input":10,"cacheRead":3,"cacheWrite":2,"output":4}}}`)
	claude := writeUsageFixture(t, `{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":5,"cache_read_input_tokens":7,"cache_creation_input_tokens":2,"output_tokens":3}}}`)
	got := ReadUsage([]string{pi, claude}, true)
	if !got.Known || got.Input != 15 || got.CacheRead != 10 || got.CacheWrite != 4 || got.Output != 7 || got.ContextPeak != 15 {
		t.Fatalf("usage = %+v", got)
	}
}

func TestUsageSummaryClaudeDeduplicatesMessageID(t *testing.T) {
	p := writeUsageFixture(t,
		`{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":10,"cache_read_input_tokens":20,"cache_creation_input_tokens":3,"output_tokens":4}}}`,
		`{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":10,"cache_read_input_tokens":20,"cache_creation_input_tokens":3,"output_tokens":4}}}`,
		`{"type":"assistant","message":{"id":"m2","usage":{"input_tokens":2,"cache_read_input_tokens":1,"cache_creation_input_tokens":0,"output_tokens":1}}}`)
	got := ReadUsage([]string{p}, false)
	if !got.Known || got.Input != 12 || got.CacheRead != 21 || got.CacheWrite != 3 || got.Output != 5 || got.ContextPeak != 33 {
		t.Fatalf("usage = %+v", got)
	}
}

func TestUsageSummaryUnknown(t *testing.T) {
	got := ReadUsage([]string{filepath.Join(t.TempDir(), "missing.jsonl")}, true)
	if got.Known {
		t.Fatalf("missing usage must be unknown: %+v", got)
	}
	empty := writeUsageFixture(t, `{"type":"assistant","message":{"id":"empty","usage":{}}}`)
	got = ReadUsage([]string{empty}, false)
	if got.Known {
		t.Fatalf("empty usage must be unknown: %+v", got)
	}
}

func TestUsageSummaryFiltersResumeToCurrentDispatch(t *testing.T) {
	p := writeUsageFixture(t,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:00Z","message":{"id":"old","usage":{"input_tokens":10,"cache_read_input_tokens":1,"cache_creation_input_tokens":2,"output_tokens":3}}}`,
		`{"type":"assistant","timestamp":"2026-01-01T00:01:00Z","message":{"id":"new","usage":{"input_tokens":20,"cache_read_input_tokens":2,"cache_creation_input_tokens":4,"output_tokens":5}}}`)
	got := ReadUsage([]string{p}, false, "2026-01-01T00:01:00Z")
	if !got.Known || got.Input != 20 || got.CacheRead != 2 || got.CacheWrite != 4 || got.Output != 5 || got.ContextPeak != 26 {
		t.Fatalf("filtered usage = %+v", got)
	}
}

func TestRenderSectionUsageScope(t *testing.T) {
	h := Header{Step: "build", Round: 1, Spawn: 1, Status: StatusDone,
		Language: LangZhCN, Usage: UsageSummary{Known: true, Input: 12, CacheRead: 3, CacheWrite: 4, Output: 5, ContextPeak: 19, IncludesSubagents: true}}
	s := RenderSection(h, "### 结论\n通过\n### 这一步要回答什么\nx\n### 做了什么\nx\n### 关键决定和理由\nx\n### 验证\nx\n### 发现的问题和风险\nnone\n### 给下游与未决\nnone")
	if !strings.Contains(s, "| 子 agent 用量 | 包含 |") || strings.Contains(s, "| 子 agent 用量 | 不包含 |") {
		t.Fatalf("scope row is not exact: %s", s)
	}
	unknown := RenderSection(Header{Language: LangZhCN, Usage: UsageSummary{}}, "### 结论\nx\n### 这一步要回答什么\nx\n### 做了什么\nx\n### 关键决定和理由\nx\n### 验证\nx\n### 发现的问题和风险\nnone\n### 给下游与未决\nnone")
	if !strings.Contains(unknown, "| Token 用量 | 未知 |") || strings.Contains(unknown, "输入 0") {
		t.Fatalf("unknown usage row is wrong: %s", unknown)
	}
	without := RenderSection(Header{Language: LangZhCN, Usage: UsageSummary{Known: true, IncludesSubagents: false}}, "### 结论\nx\n### 这一步要回答什么\nx\n### 做了什么\nx\n### 关键决定和理由\nx\n### 验证\nx\n### 发现的问题和风险\nnone\n### 给下游与未决\nnone")
	if !strings.Contains(without, "| 子 agent 用量 | 不包含 |") {
		t.Fatalf("excluded scope row is missing: %s", without)
	}
}
