package daemon

// w41_live_claude_bin_test.go: the internal pin for F2. After
// the exact live apply a reload performs, BOTH daemon-side consumers of the
// claude binary follow it: the /status harness view (the fact `pf ops status`
// and `pf doctor`'s harness claude row judge) and the summarize lane's
// production exec dispatcher (a dispatch runs the script the live state
// names, not the startup copy the lane was built with).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/summarize"
)

func w41ClaudeScript(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho "+name+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestW41ReloadedClaudeBinReachesBothDaemonConsumers(t *testing.T) {
	dir := t.TempDir()
	a := w41ClaudeScript(t, dir, "claude-a")
	b := w41ClaudeScript(t, dir, "claude-b")

	// the startup value seeds the live state only; the production dispatcher
	// (Config.SummarizeDispatcher nil) is built from it.
	td := newTestDaemon(t, func(c *Config) { c.ClaudeBin = a })
	if got := td.harnessStatuses()["claude"].Bin; got != a {
		t.Fatalf("premise: the harness view starts at the startup binary %q, got %q", a, got)
	}

	transcript := filepath.Join(dir, "dispatch.jsonl")
	if err := os.WriteFile(transcript, []byte("{\"t\":\"turn\",\"text\":\"hello\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wi := "wi_w41_live"
	models := []string{"claude:claude-haiku-5-5"}
	demand := func(token string) {
		t.Helper()
		st, err := os.Stat(transcript)
		if err != nil {
			t.Fatal(err)
		}
		td.lane.Marked(wi, models, "", 0, summarize.Dispatch{Token: token, Path: transcript, Size: st.Size()})
	}
	await := func(want string) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			p := td.lane.Projection(wi)
			if p.Summary != nil && strings.TrimSpace(*p.Summary) == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("the summarize lane never dispatched through %q: %+v", want, p)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	demand("w41#1")
	await("claude-a")

	// the exact call POST /config/reload makes for a claude_bin change
	if err := td.applyLive("claude_bin", cli.Config{ClaudeBin: b}); err != nil {
		t.Fatalf("applyLive claude_bin: %v", err)
	}
	if got := td.harnessStatuses()["claude"].Bin; got != b {
		t.Fatalf("after `claude_bin: applied (live)` the harness view still names %q, want %q", got, b)
	}

	// grow the transcript and demand again: the lane must dispatch through
	// the reloaded binary, not the copy it read at construction.
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"t\":\"turn\",\"text\":\"again\"}\n"); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	demand("w41#1")
	await("claude-b")
}
