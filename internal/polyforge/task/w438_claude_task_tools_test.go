package task

// w438_claude_task_tools_test.go: a claude task gets exactly the tools it
// declares, in the step's isolation profile.
//
// Claude Code names the tools a session really has in `system/init.tools`.
// A task declares TaskClaudeTools, or ReadOnlyClaudeTools for the
// read/search-only profile; Claude Code 2.1.319 under --bare (the task's
// token-mode flag before) offers only Read of Read,Glob,Grep.
//
// The replay test builds the task's argv/env in token mode, OAuth mode and
// with no settings.json, for both tool profiles, takes every recording made
// with exactly that profile (testdata/claude-task-init/, recorded from real
// probes) and requires its tools to equal the declared ones, its context to
// be empty and no cross-session inbox. A changed argv with no recording is
// red. The live probe (TestClaudeTaskRealProbe) runs the REAL binary; it is
// opt-in like the step's (POLYFORGE_TEST_REAL_CLAUDE=1,
// POLYFORGE_TEST_CLAUDE_HARNESS_ROOT naming the harness root whose
// claude-config/settings.json it runs with, POLYFORGE_TEST_CLAUDE_RECORD=1
// to (re)write the recordings).

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
	claudeTaskRecordingsDir = "testdata/claude-task-init"
	// the model and effort every recording is made with (the tool surface
	// may depend on the model, so a recording stands for the profile it names)
	claudeTaskProbeModel  = "claude-opus-5-5"
	claudeTaskProbeEffort = "low"
)

// claudeTaskProfiles are a claude task's two tool profiles.
var claudeTaskProfiles = []struct {
	name     string
	readOnly bool
	declared string
}{
	{"standard", false, TaskClaudeTools},
	{"read-only", true, ReadOnlyClaudeTools},
}

func claudeTaskSpec(readOnly bool, workDir, prompt string) RunSpec {
	return RunSpec{
		Candidate: harness.Candidate{Harness: harness.Claude, Model: claudeTaskProbeModel, Effort: claudeTaskProbeEffort},
		Prompt:    prompt, TaskID: "task_0123456789abcdef", WorkDir: workDir, ReadOnlyTools: readOnly,
	}
}

// The task's real tool surface equals the declared one for both profiles,
// in every auth mode, in a clean context with no cross-session inbox — read
// from a real recording of the exact argv/env the runner builds.
func TestClaudeTaskInitToolsEqualDeclared(t *testing.T) {
	// the replayed profile never depends on the test runner's own env
	t.Setenv("DISABLE_AUTOUPDATER", "")
	os.Unsetenv("DISABLE_AUTOUPDATER")
	recs, err := claudeinit.Load(claudeTaskRecordingsDir)
	if err != nil {
		t.Fatal(err)
	}
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
		r := &ExecRunner{ClaudeBin: func() string { return "claude" }, ClaudeConfigDir: ccDir, DaemonID: "d-1"}
		for _, p := range claudeTaskProfiles {
			cmd, err := r.command(context.Background(), claudeTaskSpec(p.readOnly, "/workspace", "p"))
			if err != nil {
				t.Fatalf("%s %s: %v", mode.name, p.name, err)
			}
			argv, env := claudeinit.Profile(cmd)
			matched := claudeinit.Matching(recs, argv, env)
			for _, rec := range matched {
				in, err := claudeinit.Decode(rec.Init)
				if err != nil {
					t.Fatalf("%s: %v", rec.File, err)
				}
				if missing, extra := claudeinit.ToolSurfaceDiff(in.Tools, p.declared); len(missing)+len(extra) > 0 {
					t.Errorf("%s %s: Claude Code %s gives the task the tools %q (%s), not %s: missing %q, extra %q",
						mode.name, p.name, rec.ClaudeCodeVersion, in.Tools, rec.File, p.declared, missing, extra)
				}
				if f := claudeinit.ContextFindings(in, ""); len(f) > 0 {
					t.Errorf("%s %s: %s shows a task context that is not clean: %s", mode.name, p.name, rec.File, strings.Join(f, "; "))
				}
			}
			if len(matched) == 0 {
				t.Errorf("%s %s: no recorded system/init for the task's profile\n argv %q\n env  %q\nrecord one with the live probe: "+
					"POLYFORGE_TEST_REAL_CLAUDE=1 POLYFORGE_TEST_CLAUDE_HARNESS_ROOT=<harness root> POLYFORGE_TEST_CLAUDE_RECORD=1 go test ./internal/polyforge/task -run TestClaudeTaskRealProbe",
					mode.name, p.name, argv, env)
			}
		}
	}
}

// A claude task runs the step's isolation profile in every auth mode — no
// --bare — and carries the isolation environment exactly once whatever the
// daemon inherited; a pi task carries none of it. An unusable settings.json
// refuses the run.
func TestClaudeTaskRunsTheStepProfile(t *testing.T) {
	t.Setenv("DISABLE_AUTOUPDATER", "0")
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/inherited.sock")
	ccDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(ccDir, "settings.json"), []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"tok-SENTINEL-SENTINEL-438"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &ExecRunner{ClaudeBin: func() string { return "claude" }, ClaudeConfigDir: ccDir, DaemonID: "d-1"}
	cmd, err := r.command(context.Background(), claudeTaskSpec(true, t.TempDir(), "p"))
	if err != nil {
		t.Fatal(err)
	}
	iso := runtime.ClaudeIsolationArgs()
	if !slices.Equal(cmd.Args[2:2+len(iso)], iso) || slices.Contains(cmd.Args, "--bare") {
		t.Fatalf("token-mode task argv %q: want %q right after -p and no --bare", cmd.Args, iso)
	}
	if !containsSeq(cmd.Args, runtime.ClaudeToolArgs(ReadOnlyClaudeTools)...) {
		t.Fatalf("task argv %q misses the tool flags", cmd.Args)
	}
	all := strings.Join(cmd.Args, " ") + strings.Join(cmd.Env, " ")
	if strings.Contains(all, "tok-SENTINEL-SENTINEL-438") || strings.Contains(all, "/inherited.sock") {
		t.Fatalf("a settings value or an inherited messaging address reached the task: %q %q", cmd.Args, cmd.Env)
	}
	for _, kv := range runtime.ClaudeIsolationEnv() {
		name, _, _ := strings.Cut(kv, "=")
		n := 0
		for _, e := range cmd.Env {
			if strings.HasPrefix(e, name+"=") {
				n++
				if e != kv {
					t.Errorf("task env %s, want %s", e, kv)
				}
			}
		}
		if n != 1 {
			t.Errorf("task env sets %s %d times, want once", name, n)
		}
	}
	pi, err := r.command(context.Background(), RunSpec{Candidate: harness.Candidate{Harness: harness.Pi, Model: "m"}, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(pi.Env, runtime.ClaudeNoInbox) {
		t.Errorf("a pi task carries the claude isolation env: %q", pi.Env)
	}
	if err := os.WriteFile(filepath.Join(ccDir, "settings.json"), []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.command(context.Background(), claudeTaskSpec(false, t.TempDir(), "p")); err == nil || !strings.Contains(err.Error(), "settings.json") {
		t.Fatalf("an unusable settings.json must refuse the run naming it: %v", err)
	}
}

// The comparison has teeth: the 2.1.319 recording of the read-only profile
// under the old token-mode flag (--bare) is rejected, naming the two tools
// --bare drops.
func TestClaudeTaskInitRejectsBareRecording(t *testing.T) {
	recs, err := claudeinit.Load(claudeTaskRecordingsDir)
	if err != nil {
		t.Fatal(err)
	}
	var bare *claudeinit.Recording
	for _, r := range recs {
		if slices.Contains(r.Argv, "--bare") && slices.Contains(r.Argv, ReadOnlyClaudeTools) {
			bare = &r
			break
		}
	}
	if bare == nil {
		t.Fatal("no --bare read-only recording: the negative control is gone")
	}
	in, err := claudeinit.Decode(bare.Init)
	if err != nil {
		t.Fatal(err)
	}
	missing, extra := claudeinit.ToolSurfaceDiff(in.Tools, ReadOnlyClaudeTools)
	if want := []string{"Glob", "Grep"}; !slices.Equal(missing, want) || len(extra) != 0 {
		t.Fatalf("the --bare read-only recording %q: missing %q extra %q, want missing %q and nothing extra", in.Tools, missing, extra, want)
	}
}

// TestClaudeTaskRealProbe runs the REAL Claude Code with the task's
// argv/env, once per tool profile, with one tiny prompt, and requires
// init.tools to equal the profile's declared tools, a clean context, no
// cross-session inbox (no socket under the run's TMPDIR) and a run that
// reached the model through the harness's own settings.json.
func TestClaudeTaskRealProbe(t *testing.T) {
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
	r := &ExecRunner{ClaudeBin: func() string { return bin }, ClaudeConfigDir: ccDir, DaemonID: "d-probe"}
	for _, p := range claudeTaskProfiles {
		t.Run(p.name, func(t *testing.T) {
			work, childTmp := t.TempDir(), t.TempDir()
			t.Cleanup(func() { os.RemoveAll(filepath.Join(ccDir, "projects", harness.ClaudeProjectDirName(work))) })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			cmd, err := r.command(ctx, claudeTaskSpec(p.readOnly, work, "Reply with the single word OK."))
			if err != nil {
				t.Fatal(err)
			}
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
			if os.Getenv("POLYFORGE_TEST_CLAUDE_RECORD") == "1" {
				argv, env := claudeinit.Profile(cmd)
				name := fmt.Sprintf("claude-%s-%s-%s.json", in.Version, strings.TrimLeft(argv[2], "-"), p.name)
				if err := claudeinit.Write(claudeTaskRecordingsDir, name, argv, env, nil, initRaw, in.Version, "",
					map[string]string{work: "/workspace", ccDir: "/claude-config", childTmp: "<runtime-dir>"}); err != nil {
					t.Fatal(err)
				}
				t.Logf("recorded %s", name)
			}
			if missing, extra := claudeinit.ToolSurfaceDiff(in.Tools, p.declared); len(missing)+len(extra) > 0 {
				t.Errorf("Claude Code %s gives the task the tools %q, not %s: missing %q, extra %q",
					in.Version, in.Tools, p.declared, missing, extra)
			}
			if f := claudeinit.ContextFindings(in, ""); len(f) > 0 {
				t.Errorf("the task's init shows a context that is not clean: %s", strings.Join(f, "; "))
			}
			if socks, err := os.ReadDir(filepath.Join(childTmp, "cc-socks")); err == nil {
				t.Errorf("the task opened a cross-session inbox: cc-socks holds %d entries", len(socks))
			}
			if isErr, text, ok := claudeinit.Result(stdout.Bytes()); runErr != nil || !ok || isErr {
				t.Fatalf("the probe did not reach the model (exit %v, result %q); stderr: %s", runErr, text, stderr.String())
			}
			t.Logf("Claude Code %s, init.tools %q", in.Version, in.Tools)
		})
	}
}
