package task

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
)

func fakePi(t *testing.T, body string) *ExecRunner {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "pi")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return &ExecRunner{PiBin: bin, DaemonID: "d"}
}

func piSpec(t *testing.T, max int64) RunSpec {
	c, _ := harness.ParseCandidate("pi:m")
	return RunSpec{Candidate: c, Prompt: "hi", MaxTokens: max, TaskID: "task_0123456789abcdef", WorkDir: t.TempDir()}
}

// containsSeq reports whether args carries seq as a contiguous run.
func containsSeq(args []string, seq ...string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		ok := true
		for j := range seq {
			if args[i+j] != seq[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func TestExecRunnerCompletes(t *testing.T) {
	r := fakePi(t, `cat >/dev/null
echo '{"type":"turn_start"}'
echo '{"type":"message_update","usage":{"input":10,"output":2,"cacheRead":900,"cacheWrite":0}}'
echo '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}'
`)
	res, err := r.Run(context.Background(), piSpec(t, 1000), nil)
	if err != nil || res.Report != "done" || res.Tokens != 12 || res.TokenCapped {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestExecRunnerTokenKill(t *testing.T) {
	r := fakePi(t, `cat >/dev/null
echo '{"type":"message_update","usage":{"input":400,"output":100,"cacheRead":9000,"cacheWrite":0}}'
sleep 30
`)
	start := time.Now()
	res, err := r.Run(context.Background(), piSpec(t, 100), nil)
	if err != nil || !res.TokenCapped {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("token cap did not kill the process promptly")
	}
}

func TestExecRunnerNoUsageAndFailure(t *testing.T) {
	r := fakePi(t, "cat >/dev/null\necho hello\n")
	if _, err := r.Run(context.Background(), piSpec(t, 100), nil); err == nil || !strings.Contains(err.Error(), "no token usage") {
		t.Fatalf("err=%v", err)
	}
	r = fakePi(t, "cat >/dev/null\necho boom >&2\nexit 3\n")
	if _, err := r.Run(context.Background(), piSpec(t, 100), nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err=%v", err)
	}
}

func TestExecRunnerHarnessToolAllowlist(t *testing.T) {
	r := &ExecRunner{}
	pc, _ := harness.ParseCandidate("pi:m")
	pia, err := r.innerArgv(pc)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSeq(pia, "--tools", TaskPiTools) {
		t.Fatalf("pi argv misses the tool allowlist: %v", pia)
	}
	cc, _ := harness.ParseCandidate("claude:m")
	ca, err := r.innerArgv(cc)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSeq(ca, "--tools", TaskClaudeTools) || !containsSeq(ca, "--allowedTools", TaskClaudeTools) {
		t.Fatalf("claude argv misses the tool allowlist: %v", ca)
	}
	for _, banned := range []string{"bypassPermissions", "--dangerously-skip-permissions", "Edit", "Write"} {
		for _, a := range ca {
			if a == banned || strings.Contains(a, banned) {
				t.Fatalf("claude argv carries banned %q: %v", banned, ca)
			}
		}
	}
}

func TestTaskEnvStripsMarkersAndCredentials(t *testing.T) {
	env := taskEnv([]string{"PF_WI_ID=x", "PF_DAEMON_ID=other", "AIHUB_KEY=s", "ANTHROPIC_API_KEY=s",
		"PI_CODING_AGENT_DIR=/inherited-agent", "CLAUDE_CONFIG_DIR=/h/.claude", "PATH=/bin", "HOME=/h"},
		"task_1", "d", "/sock", "/claude-config", "/step-agent")
	j := strings.Join(env, "\n")
	for _, bad := range []string{"PF_WI_ID", "PF_DAEMON_ID=other", "AIHUB_KEY", "ANTHROPIC_API_KEY", "PI_CODING_AGENT_DIR=/inherited-agent", "CLAUDE_CONFIG_DIR=/h/.claude"} {
		if strings.Contains(j, bad) {
			t.Fatalf("env leaks %s: %v", bad, env)
		}
	}
	// HOME is the daemon user's own: a task agent's filesystem trust equals a
	// session agent's (owner ruling 2026-10-26).
	for _, want := range []string{"PATH=/bin", "HOME=/h", "PF_TASK_ID=task_1", "PF_DAEMON_ID=d",
		"PF_DAEMON_SOCKET=/sock", "CLAUDE_CONFIG_DIR=/claude-config", "PI_CODING_AGENT_DIR=/step-agent"} {
		if !strings.Contains(j, want) {
			t.Fatalf("env missing %s in %v", want, env)
		}
	}
}

// TestResolveBinFollowsLookPathAndSymlinks pins carried bar F2 (
// review): the harness binary is resolved with exec.LookPath and then
// filepath.EvalSymlinks. A native-installer `claude` is a stable symlink
// into a versioned directory; execing the bare name (or the link) without
// resolving it leaves the child's own install root behind — and, when the
// name is not on the daemon's PATH at all, the spawn must name the
// configured value rather than a guessed path.
func TestResolveBinFollowsLookPathAndSymlinks(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved // darwin: /var -> /private/var
	}
	real := filepath.Join(root, "releases", "1.2.3", "claude")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(link)+string(os.PathListSeparator)+os.Getenv("PATH"))

	if got := resolveBin("claude"); got != real {
		t.Fatalf("resolveBin(\"claude\") = %q; want the symlink target %q", got, real)
	}
	if got := resolveBin(link); got != real {
		t.Fatalf("resolveBin(%q) = %q; want the symlink target %q", link, got, real)
	}
	if got := resolveBin(real); got != real {
		t.Fatalf("resolveBin(%q) = %q; want it unchanged", real, got)
	}
	if got := resolveBin("/nonexistent/w53-nope"); got != "/nonexistent/w53-nope" {
		t.Fatalf("an unresolvable binary must be answered as configured; got %q", got)
	}
	// The runner execs the resolved target, not the configured name.
	r := &ExecRunner{ClaudeBin: func() string { return "claude" }}
	if got := r.bin(harness.Claude); got != real {
		t.Fatalf("ExecRunner.bin(claude) = %q; want %q", got, real)
	}
}
