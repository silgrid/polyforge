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
}

func TestRenderSectionUsageScope(t *testing.T) {
	h := Header{Step: "build", Round: 1, Spawn: 1, Status: StatusDone,
		Language: LangZhCN, Usage: UsageSummary{Known: true, Input: 12, CacheRead: 3, CacheWrite: 4, Output: 5, ContextPeak: 19, IncludesSubagents: true}}
	s := RenderSection(h, "### 结论\n通过\n### 这一步要回答什么\nx\n### 做了什么\nx\n### 关键决定和理由\nx\n### 验证\nx\n### 发现的问题和风险\nnone\n### 给下游与未决\nnone")
	for _, want := range []string{"Token 用量", "输入 12", "缓存读 3", "缓存写 4", "输出 5", "上下文峰值 19", "子 agent 用量", "包含"} {
		if !strings.Contains(s, want) {
			t.Fatalf("handoff missing %q:\n%s", want, s)
		}
	}
}
