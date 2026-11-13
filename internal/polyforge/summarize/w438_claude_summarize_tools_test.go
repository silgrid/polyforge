package summarize

// w438_claude_summarize_tools_test.go: a claude summarize dispatch has no
// tools at all (owner ruling 2026-11-10).
//
// The dispatch's prompt carries an agent-written transcript tail, which
// may carry an injection; the lane needs no tool (the tail rides stdin, the
// answer is one line of text), so its tool surface is explicitly empty.
// Claude Code names the tools a session really has in `system/init.tools`;
// 2.1.319 under --bare with no --tools offers Bash, Edit and Read.
//
// The dispatch runs in text mode, which has no init to read: a recording
// (testdata/claude-summarize-init/, from real probes) carries the
// dispatch's exact argv/env plus the flags the probe appended to read the
// init (`observed_with`). The replay test builds the dispatch's argv/env in
// token mode, OAuth mode and with no settings.json, takes every recording
// made with exactly that profile and requires an empty tool list and no
// cross-session inbox; a changed argv with no recording is red. The live
// probe (TestClaudeSummarizeRealProbe) is opt-in like the step's
// (POLYFORGE_TEST_REAL_CLAUDE=1, POLYFORGE_TEST_CLAUDE_HARNESS_ROOT naming
// the harness root, POLYFORGE_TEST_CLAUDE_RECORD=1 to (re)write).

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/claudeinit"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

const (
	claudeSummarizeRecordingsDir = "testdata/claude-summarize-init"
	claudeSummarizeProbeModel    = "claude-opus-5-5"
)

// claudeSummarizeObserve are the flags the probe appends to the dispatch's
// argv to read its `system/init` (the dispatch itself prints plain text).
var claudeSummarizeObserve = []string{"--output-format", "stream-json", "--verbose"}

// The dispatch's real tool surface is empty, with no cross-session inbox, in
// every auth mode — read from a real recording of the exact argv/env the
// dispatcher builds.
func TestClaudeSummarizeInitHasNoTools(t *testing.T) {
	// the replayed profile never depends on the test runner's own env
	t.Setenv("DISABLE_AUTOUPDATER", "")
	os.Unsetenv("DISABLE_AUTOUPDATER")
	recs, err := claudeinit.Load(claudeSummarizeRecordingsDir)
	if err != nil {
		t.Fatal(err)
	}
	c := harness.Candidate{Harness: harness.Claude, Model: claudeSummarizeProbeModel}
	for _, mode := range []struct{ name, settings string }{
		{"token mode", `{"env":{"ANTHROPIC_AUTH_TOKEN":"tok-SENTINEL-SENTINEL-438","ANTHROPIC_BASE_URL":"https://gw.example"}}`},
		{"OAuth mode", `{"env":{}}`},
		{"no settings.json", ""},
	} {
		ccDir := filepath.Join(t.TempDir(), "claude-config")
		if mode.settings != "" {
			if err := os.MkdirAll(ccDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(ccDir, "settings.json"), []byte(mode.settings), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		d := &ExecDispatcher{ClaudeBin: func() string { return "claude" }, ClaudeConfigDir: ccDir}
		cmd, err := d.command(context.Background(), c, "p")
		if err != nil {
			t.Fatalf("%s: %v", mode.name, err)
		}
		argv, env := claudeinit.Profile(cmd)
		matched := claudeinit.Matching(recs, argv, env)
		for _, r := range matched {
			if !slices.Equal(r.ObservedWith, claudeSummarizeObserve) {
				t.Errorf("%s: %s was observed with %q, not %q", mode.name, r.File, r.ObservedWith, claudeSummarizeObserve)
			}
			in, err := claudeinit.Decode(r.Init)
			if err != nil {
				t.Fatalf("%s: %v", r.File, err)
			}
			if _, extra := claudeinit.ToolSurfaceDiff(in.Tools, ""); len(extra) > 0 {
				t.Errorf("%s: Claude Code %s gives the summarize dispatch the tools %q (%s), want none",
					mode.name, r.ClaudeCodeVersion, extra, r.File)
			}
			if f := claudeinit.InboxFinding(in); f != "" {
				t.Errorf("%s: %s: the dispatch opened %s", mode.name, r.File, f)
			}
		}
		if len(matched) == 0 {
			t.Errorf("%s: no recorded system/init for the dispatch's profile\n argv %q\n env  %q\nrecord one with the live probe: "+
				"POLYFORGE_TEST_REAL_CLAUDE=1 POLYFORGE_TEST_CLAUDE_HARNESS_ROOT=<harness root> POLYFORGE_TEST_CLAUDE_RECORD=1 go test ./internal/polyforge/summarize -run TestClaudeSummarizeRealProbe",
				mode.name, argv, env)
		}
	}
}

// A claude dispatch carries the isolation environment (no cross-session
// inbox) exactly once whatever the daemon inherited, and its config dir; a
// pi dispatch carries none of it.
func TestClaudeSummarizeDispatchEnv(t *testing.T) {
	t.Setenv("DISABLE_AUTOUPDATER", "0")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "inherited-token")
	ccDir := t.TempDir()
	d := &ExecDispatcher{ClaudeBin: func() string { return "claude" }, ClaudeConfigDir: ccDir}
	cmd, err := d.command(context.Background(), harness.Candidate{Harness: harness.Claude, Model: "m"}, "p")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(cmd.Env, " "), "inherited-token") {
		t.Fatalf("an inherited messaging token reached the dispatch: %q", cmd.Env)
	}
	for _, kv := range append(runtime.ClaudeIsolationEnv(), "CLAUDE_CONFIG_DIR="+ccDir) {
		name, _, _ := strings.Cut(kv, "=")
		n := 0
		for _, e := range cmd.Env {
			if strings.HasPrefix(e, name+"=") {
				n++
				if e != kv {
					t.Errorf("dispatch env %s, want %s", e, kv)
				}
			}
		}
		if n != 1 {
			t.Errorf("dispatch env sets %s %d times, want once", name, n)
		}
	}
	pi, err := d.command(context.Background(), harness.Candidate{Harness: harness.Pi, Model: "m"}, "p")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(pi.Env, runtime.ClaudeNoInbox) {
		t.Errorf("a pi dispatch carries the claude isolation env: %q", pi.Env)
	}
}

// The comparison has teeth: the 2.1.319 recording of the old token-mode
// dispatch (--bare, no --tools) is rejected, naming the three tools it got.
func TestClaudeSummarizeInitRejectsDefaultTools(t *testing.T) {
	recs, err := claudeinit.Load(claudeSummarizeRecordingsDir)
	if err != nil {
		t.Fatal(err)
	}
	var old *claudeinit.Recording
	for _, r := range recs {
		if slices.Contains(r.Argv, "--bare") && !slices.Contains(r.Argv, "--tools") {
			old = &r
			break
		}
	}
	if old == nil {
		t.Fatal("no --bare recording without --tools: the negative control is gone")
	}
	in, err := claudeinit.Decode(old.Init)
	if err != nil {
		t.Fatal(err)
	}
	if _, extra := claudeinit.ToolSurfaceDiff(in.Tools, ""); !slices.Equal(extra, []string{"Bash", "Edit", "Read"}) {
		t.Fatalf("the --bare recording without --tools %q: extra %q, want Bash, Edit and Read", in.Tools, extra)
	}
}

// TestClaudeSummarizeRealProbe runs the REAL Claude Code with the
// dispatch's argv/env (token or OAuth mode, whichever the harness root's
// claude-config holds) and one tiny prompt, plus the flags that print the
// init. It requires an empty init.tools, no cross-session inbox (no socket
// under the run's TMPDIR), a run that reached the model, and that the call
// left no session record under the config dir's projects/ (the dispatch
// runs with --no-session-persistence).
func TestClaudeSummarizeRealProbe(t *testing.T) {
	if os.Getenv("POLYFORGE_TEST_REAL_CLAUDE") != "1" {
		t.Skip("this probe launches a REAL claude process with the harness root's credentials; set POLYFORGE_TEST_REAL_CLAUDE=1 to run it")
	}
	bin := os.Getenv("POLYFORGE_TEST_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	root := os.Getenv("POLYFORGE_TEST_CLAUDE_HARNESS_ROOT")
	if root == "" {
		t.Skip("set POLYFORGE_TEST_CLAUDE_HARNESS_ROOT to the harness root whose claude-config/settings.json the probe runs with")
	}
	ccDir := filepath.Join(root, "claude-config")
	if _, err := os.Stat(filepath.Join(ccDir, "settings.json")); err != nil {
		t.Skipf("no claude-config settings.json under %s: %v", ccDir, err)
	}
	cwd, childTmp := t.TempDir(), t.TempDir()
	projDirs := []string{filepath.Join(ccDir, "projects", harness.ClaudeProjectDirName(cwd))}
	if real, err := filepath.EvalSymlinks(cwd); err == nil && real != cwd {
		projDirs = append(projDirs, filepath.Join(ccDir, "projects", harness.ClaudeProjectDirName(real)))
	}
	t.Cleanup(func() {
		for _, p := range projDirs {
			os.RemoveAll(p)
		}
	})
	d := &ExecDispatcher{ClaudeBin: func() string { return bin }, ClaudeConfigDir: ccDir}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd, err := d.command(ctx, harness.Candidate{Harness: harness.Claude, Model: claudeSummarizeProbeModel},
		"Reply with the single word OK.")
	if err != nil {
		t.Fatal(err)
	}
	argv, env := claudeinit.Profile(cmd) // the dispatch's own, before the observation flags
	cmd.Args = append(cmd.Args, claudeSummarizeObserve...)
	cmd.Dir = cwd
	cmd.Env = append(cmd.Env, "TMPDIR="+childTmp) // claude's own scratch goes with the test
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	initRaw := claudeinit.FirstInit(stdout.Bytes())
	if initRaw == nil {
		t.Fatalf("no system/init record (exit %v); stderr: %s", runErr, stderr.String())
	}
	in, err := claudeinit.Decode(initRaw)
	if err != nil {
		t.Fatal(err)
	}
	isErr, text, ok := claudeinit.Result(stdout.Bytes())
	reached := runErr == nil && ok && !isErr
	if os.Getenv("POLYFORGE_TEST_CLAUDE_RECORD") == "1" {
		tools := "default-tools"
		if i := slices.Index(argv, "--tools"); i >= 0 && i+1 < len(argv) && argv[i+1] == "" {
			tools = "no-tools"
		}
		name := fmt.Sprintf("claude-%s-%s-%s.json", in.Version, strings.TrimLeft(argv[2], "-"), tools)
		note := ""
		if !reached {
			note = fmt.Sprintf("the run did not reach the model (result %q): the init precedes authentication", text)
		}
		if err := claudeinit.Write(claudeSummarizeRecordingsDir, name, argv, env, claudeSummarizeObserve, initRaw, in.Version, note,
			map[string]string{cwd: "/summarize-cwd", ccDir: "/claude-config", childTmp: "<runtime-dir>"}); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %s", name)
	}
	if _, extra := claudeinit.ToolSurfaceDiff(in.Tools, ""); len(extra) > 0 {
		t.Errorf("Claude Code %s gives the summarize dispatch the tools %q, want none", in.Version, extra)
	}
	if f := claudeinit.InboxFinding(in); f != "" {
		t.Errorf("the dispatch opened %s", f)
	}
	if socks, err := os.ReadDir(filepath.Join(childTmp, "cc-socks")); err == nil {
		t.Errorf("the dispatch opened a cross-session inbox: cc-socks holds %d entries", len(socks))
	}
	if !reached {
		t.Fatalf("the probe did not reach the model (exit %v, result %q); stderr: %s", runErr, text, stderr.String())
	}
	for _, p := range projDirs {
		var left []string
		_ = filepath.WalkDir(p, func(f string, e os.DirEntry, err error) error {
			if err == nil && !e.IsDir() {
				left = append(left, f)
			}
			return nil
		})
		if len(left) > 0 {
			t.Errorf("the dispatch left a session record under %s: %q", p, left)
		}
	}
	t.Logf("Claude Code %s, init.tools %q, answer %q", in.Version, in.Tools, text)
}
