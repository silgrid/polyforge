package summarize

// dispatch_cwd_repro_test.go — the part-② reproduction
// (the pi-web- incident): the daemon's non-capsule one-shot claude
// dispatch (the summarize lane's ExecDispatcher) started claude with the
// DAEMON process's working directory, observed as /root in the incident
// ("this workspace has not been trusted. Ignoring 57 permissions.allow
// entries from .claude/settings.json"). The mechanical core is plain in
// Dispatch: it builds the exec.Cmd and never sets cmd.Dir, so the
// dispatched harness inherits whatever directory the daemon process
// runs in — an untrusted workspace root for Claude Code's trust gate,
// which then ignores every pre-authorized permissions.allow entry of
// the settings.json the dispatch itself points CLAUDE_CONFIG_DIR at.
//
// Reported failure: the claude (and, same code path, pi) one-shot
// dispatch runs at the inherited daemon cwd, tripping the workspace
// trust mechanism at /root.
// Expected behavior (the work item's acceptance): the dispatch sets an
// explicit working directory — a capsule repo or a designated agent
// dir — and no longer depends on the daemon root's trust state.
//
// Regression test: on the current tree the dispatched process reports
// the dispatcher's own process cwd (cmd.Dir unset), so the assertion
// below fails with the reported symptom.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDispatchRunsAtAnExplicitWorkingDirectory pins the incident's
// mechanical core: the one-shot harness dispatch must NOT inherit the
// dispatcher's process working directory. A harness stand-in prints its
// own cwd; the dispatch ran un-pinned exactly when that answer is the
// dispatcher process's cwd.
func TestDispatchRunsAtAnExplicitWorkingDirectory(t *testing.T) {
	// The dispatcher process's cwd — the directory an unset cmd.Dir
	// silently inherits (in production: the daemon's, /root in the
	// incident; in this test: the test binary's package dir).
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	daemonCwd, err := filepath.EvalSymlinks(wd)
	if err != nil {
		t.Fatal(err)
	}
	// A harness stand-in that reports its own working directory (the
	// physical path, like the real harness's trust check resolves it).
	bin := scriptBin(t, "pwd -P")
	d := &ExecDispatcher{
		PiBin:           bin,
		ClaudeBin:       func() string { return bin },
		ClaudeConfigDir: filepath.Join(t.TempDir(), "cc"), // absent: --safe-mode
	}
	for _, candidate := range []string{"pi:m", "claude:claude-haiku-5-5"} {
		out, err := d.Dispatch(candidate, "prompt")
		if err != nil {
			t.Fatalf("%s dispatch: %v", candidate, err)
		}
		got := strings.TrimSpace(out)
		if got == "" {
			t.Fatalf("%s dispatch: the stand-in reported no cwd", candidate)
		}
		if got == daemonCwd {
			t.Fatalf("SYMPTOM ( part \u2161, pi-web-): "+
				"the %s one-shot dispatch ran at the dispatcher's INHERITED process "+
				"cwd %q — ExecDispatcher.Dispatch leaves cmd.Dir unset, so in production "+
				"the harness starts at the daemon's working directory (/root in the "+
				"incident), where Claude Code's workspace-trust gate ignores every "+
				"permissions.allow entry of the settings.json the dispatch points "+
				"CLAUDE_CONFIG_DIR at; the dispatch must set an explicit working "+
				"directory", candidate, got)
		}
	}
}
