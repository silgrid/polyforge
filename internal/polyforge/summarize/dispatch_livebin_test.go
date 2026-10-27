package summarize

// dispatch_livebin_test.go: the claude binary is read from the dispatcher's
// source AT EACH DISPATCH, never from a construction-time copy (workbench#41
// F2). The source changes between two dispatches and the script that runs is
// the observable fact — so a daemon reload of claude_bin reaches the
// summarize lane without a restart.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecDispatcherResolvesClaudeBinAtDispatch(t *testing.T) {
	dir := t.TempDir()
	script := func(name, line string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho "+line+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := script("claude-a", "claude-a")
	b := script("claude-b", "claude-b")

	cur := a
	d := &ExecDispatcher{
		PiBin:           a,
		ClaudeBin:       func() string { return cur },
		ClaudeConfigDir: filepath.Join(dir, "cc"), // absent: --safe-mode
	}
	out, err := d.Dispatch("claude:claude-haiku-5-5", "prompt")
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	if got := strings.TrimSpace(out); got != "claude-a" {
		t.Fatalf("first dispatch ran %q, want the source's first answer \"claude-a\"", got)
	}

	// the live source changes — exactly what a reloaded claude_bin does
	cur = b
	out, err = d.Dispatch("claude:claude-haiku-5-5", "prompt")
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if got := strings.TrimSpace(out); got != "claude-b" {
		t.Fatalf("second dispatch ran %q, want the reloaded \"claude-b\" — "+
			"the dispatcher must read its claude source at dispatch time, not at construction", got)
	}
}
