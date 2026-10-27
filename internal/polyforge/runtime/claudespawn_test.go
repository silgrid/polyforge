package runtime

// claudespawn_test.go: the polyforge#159 Claude Code step spawner — the
// argv/env golden (clean context flags, the tool allowlist, background
// off, the zero credential injection (polyforge#171): the token never rides env or argv from the adapter) and the stdin contract
// (the prompt is written, then stdin is closed). No real `claude` runs:
// the binary is a shell stand-in.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
)

const testSession = "0b9f6a52-3c1d-4e8f-9a7b-1c2d3e4f5a6b"

func TestClaudeBuildCmdArgvEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	os.Unsetenv("ANTHROPIC_AUTH_TOKEN")
	sp := &claudeSpawner{cfg: &Config{ClaudeBin: "/opt/claude", MachineID: "m-1", DaemonSocket: "/d/daemon.sock"}}
	cmd, err := sp.buildCmd(SpawnParams{WorkItemID: "wi_1", CapsuleDir: "/caps", Model: "claude-opus-5-5", HarnessSession: testSession})
	if err != nil {
		t.Fatal(err)
	}
	// a bare Config has no harness root: tokenless, so OAuth mode
	want := []string{"/opt/claude", "-p", "--safe-mode",
		"--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--session-id", testSession,
		"--model", "claude-opus-5-5",
		"--tools", ClaudeStepTools, "--allowedTools", ClaudeStepTools,
		"--permission-mode", "dontAsk", "--permission-prompts", "none",
		"--strict-mcp-config", "--disable-slash-commands"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv\n got %q\nwant %q", cmd.Args, want)
	}
	if cmd.Dir != "/caps" {
		t.Fatalf("cwd %q", cmd.Dir)
	}
	argv := strings.Join(cmd.Args, " ")
	for _, banned := range []string{"tok-SENTINEL-159", "--setting-sources", "--fallback-model", "--mcp-config", "--dangerously", "bypassPermissions", "--agents", "--system-prompt", "--add-dir", "--plugin-dir"} {
		if strings.Contains(argv, banned) {
			t.Fatalf("argv carries %q: %s", banned, argv)
		}
	}
	tools := map[string]bool{}
	for _, n := range strings.Split(ClaudeStepTools, ",") {
		tools[n] = true
	}
	for _, bg := range ClaudeBackgroundTools {
		if tools[bg] {
			t.Fatalf("background tool %s is in the step allowlist", bg)
		}
	}
	env := strings.Join(cmd.Env, "\n")
	for _, kv := range []string{"PF_WI_ID=wi_1", "PF_DAEMON_ID=m-1", "PF_DAEMON_SOCKET=/d/daemon.sock", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1", "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_STARTUP_FAILURE_RESULTS=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"} {
		if !strings.Contains(env, kv) {
			t.Fatalf("env lacks %s", kv)
		}
	}
	if strings.Contains(env, "CLAUDE_CONFIG_DIR=") {
		t.Fatalf("a bare Config has no claude config dir to hard-set: %s", env)
	}
	// polyforge#171 zero-credential injection (closing out #159's toml token):
	// no token ever rides the env from the adapter — the settings.json
	// authority under claude-config is claude's own business.
	if strings.Contains(env, "ANTHROPIC_AUTH_TOKEN=tok-SENTINEL-159") {
		t.Errorf("env carries the injected token: %s", env)
	}
	if strings.Contains(env, "PI_CODING_AGENT_DIR") {
		t.Fatal("a claude step gets no pi agent dir")
	}

	// resume: --resume <uuid>, no --session-id; no model → no --model
	cmd, err = sp.buildCmd(SpawnParams{CapsuleDir: "/caps", Resume: true, HarnessSession: testSession})
	if err != nil {
		t.Fatal(err)
	}
	argv = strings.Join(cmd.Args, " ")
	if !strings.Contains(argv, "--resume "+testSession) || strings.Contains(argv, "--session-id") || strings.Contains(argv, "--model") {
		t.Fatalf("resume argv %s", argv)
	}
	if n := strings.Count(argv, "--safe-mode"); n != 1 {
		t.Fatalf("resume argv carries --safe-mode %d times, want 1: %s", n, argv)
	}

	// an invalid session id is refused (the id may come from the journal)
	if _, err := sp.buildCmd(SpawnParams{CapsuleDir: "/caps", HarnessSession: "../x"}); err == nil {
		t.Fatal("invalid session id accepted")
	}
}

// polyforge#223: the context flag follows the auth mode of the
// claude-config settings.json env block, read at every spawn (fresh and
// resume): token mode → --bare only; tokenless (empty value, missing
// file, no harness root) → --safe-mode only; an unusable file refuses
// the spawn. No value from the file reaches the argv, env or error.
func TestClaudeContextFlagByAuthMode(t *testing.T) {
	const secret = "tok-SENTINEL-223"
	root := t.TempDir()
	cfg := &Config{HarnessRoot: root}
	ccDir := cfg.ClaudeConfigDir()
	if err := os.MkdirAll(ccDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(ccDir, "settings.json")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(settings, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sp := &claudeSpawner{cfg: cfg}
	check := func(name, want, banned string) {
		t.Helper()
		for _, resume := range []bool{false, true} {
			cmd, err := sp.buildCmd(SpawnParams{CapsuleDir: "/caps", Resume: resume, HarnessSession: testSession})
			if err != nil {
				t.Fatalf("%s (resume=%v): %v", name, resume, err)
			}
			if n := slices.Index(cmd.Args, want); n != 2 || strings.Count(strings.Join(cmd.Args, " "), want) != 1 {
				t.Fatalf("%s (resume=%v): want %s exactly once right after -p: %q", name, resume, want, cmd.Args)
			}
			if slices.Contains(cmd.Args, banned) {
				t.Fatalf("%s (resume=%v): argv carries %s: %q", name, resume, banned, cmd.Args)
			}
			if all := strings.Join(cmd.Args, " ") + strings.Join(cmd.Env, " "); strings.Contains(all, secret) {
				t.Fatalf("%s: a settings value reached the argv/env", name)
			}
		}
	}

	check("missing settings.json", "--safe-mode", "--bare")
	write(`{"env":{"ANTHROPIC_AUTH_TOKEN":"` + secret + `","ANTHROPIC_BASE_URL":"https://gw.example"}}`)
	check("auth token", "--bare", "--safe-mode")
	write(`{"env":{"ANTHROPIC_API_KEY":"` + secret + `"}}`)
	check("api key", "--bare", "--safe-mode")
	write(`{"env":{"ANTHROPIC_AUTH_TOKEN":"","ANTHROPIC_API_KEY":"","ANTHROPIC_BASE_URL":"https://x"}}`)
	check("empty token values", "--safe-mode", "--bare")
	write(`{"env":{}}`)
	check("empty env block", "--safe-mode", "--bare")

	for body, frag := range map[string]string{
		`{"env":{"ANTHROPIC_AUTH_TOKEN":"` + secret + `"`: "does not parse as JSON",
		`{"model":"x"}`:                      "no `env` key",
		`{"env":{"ANTHROPIC_AUTH_TOKEN":7}}`: "`env` string map",
		`[]`:                                 "`env` string map",
	} {
		write(body)
		_, err := sp.buildCmd(SpawnParams{CapsuleDir: "/caps", HarnessSession: testSession})
		if err == nil || !strings.Contains(err.Error(), frag) || !strings.Contains(err.Error(), settings) {
			t.Fatalf("settings %q: err %v, want a refusal naming %s and %q", body, err, settings, frag)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("the refusal carries a settings value: %v", err)
		}
	}
	if os.Geteuid() != 0 {
		write(`{"env":{}}`)
		if err := os.Chmod(settings, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := sp.buildCmd(SpawnParams{CapsuleDir: "/caps", HarnessSession: testSession}); err == nil || !strings.Contains(err.Error(), "unreadable") {
			t.Fatalf("unreadable settings.json: err %v", err)
		}
	}
}

// polyforge#171 zero-credential injection (closing out #159's toml token)
// + env hygiene: the adapter never injects ANTHROPIC_AUTH_TOKEN from any
// config, and strips every inherited ANTHROPIC_* (the daemon's
// environment) — claude-config/settings.json is the only source.
func TestClaudeTokenPrecedence(t *testing.T) {
	for _, k := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
		t.Setenv(k, "junk")
	}
	sp := &claudeSpawner{cfg: &Config{}}
	cmd, _ := sp.buildCmd(SpawnParams{CapsuleDir: "/c", HarnessSession: testSession})
	for _, kv := range cmd.Env {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "ANTHROPIC_") {
			t.Fatalf("the claude step inherited %s", name)
		}
	}
}

// env hygiene: pi steps keep the inherited environment unchanged (pi
// authenticates from its own auth.json; nothing ANTHROPIC_* is stripped
// there).
func TestPiStepEnvKeepsInheritedAnthropic(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "junk")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "junk")
	cmd := newPiSpawner(&Config{}).buildCmd(SpawnParams{CapsuleDir: "/c", SpawnN: 1})
	for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"} {
		if !slices.Contains(cmd.Env, k+"=junk") {
			t.Fatalf("the pi step env lost the inherited %s", k)
		}
	}
}

// The prompt rides stdin and stdin is closed afterwards (the stand-in's
// `cat` only finishes on EOF); the spawn journal is written first.
func TestClaudeSpawnWritesPromptAndClosesStdin(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "stdin.txt")
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\ncat > " + out + ".tmp && mv " + out + ".tmp " + out + "\necho '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var journaled bool
	var lines []string
	sp := DefaultSpawner(&Config{ClaudeBin: bin})
	h, err := sp.Spawn(context.Background(), SpawnParams{
		CapsuleDir: dir, InitialPrompt: "do the step", Harness: harness.Claude, HarnessSession: testSession,
		Journal:  func(int, string) error { journaled = true; return nil },
		OnRecord: func(l string) { lines = append(lines, l) },
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !h.Done() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !h.Done() {
		_ = h.Terminate(time.Second)
		t.Fatal("the stand-in never saw stdin EOF")
	}
	got, err := os.ReadFile(out)
	if err != nil || string(got) != "do the step" {
		t.Fatalf("stdin %q (%v)", got, err)
	}
	if !journaled {
		t.Fatal("spawn journal not written")
	}
	if len(lines) != 1 || !strings.Contains(lines[0], `"result"`) {
		t.Fatalf("stdout records %q", lines)
	}
}

func TestHarnessSpawnerRefusesUnknownHarness(t *testing.T) {
	if _, err := DefaultSpawner(&Config{}).Spawn(context.Background(), SpawnParams{Harness: "codex", InitialPrompt: "x"}); err == nil {
		t.Fatal("unknown harness accepted")
	}
}

// The seal/pause sync copies a claude dispatch's NATIVE session (found
// by the journaled uuid) onto that dispatch's own capsule file — on a
// resumed dispatch too (the native file is outside the capsule).
func TestSyncLiveTranscriptClaude(t *testing.T) {
	// polyforge#171: the locator takes the claude-config dir explicitly
	// off the runtime config's harness root; the daemon process env is
	// never consulted (the child env is the only place that matters).
	root := t.TempDir()
	capDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(capDir, "session"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "elsewhere"))
	native := filepath.Join(root, "claude-config", "projects", harness.ClaudeProjectDirName(capDir), testSession+".jsonl")
	if err := os.MkdirAll(filepath.Dir(native), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte("{\"type\":\"user\"}\n{\"type\":\"assistant\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := sessionPathFor(capDir, "fp#2")
	line := `{"step_id":"fix","spawn_token":"fp#2","spawn_n":3,"resume":true,"started_at":"2026-10-15T00:00:00Z","transcript":"` + dst + `","harness":"claude","harness_session":"` + testSession + `"}` + "\n"
	if err := os.MkdirAll(filepath.Join(capDir, "runtime-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(capDir, "runtime-state", "spawn-journal.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runtime{cfg: Config{HarnessRoot: root}}
	r.syncLiveTranscript(capDir)
	got, err := os.ReadFile(dst)
	if err != nil || !strings.Contains(string(got), `"assistant"`) {
		t.Fatalf("capsule copy %q (%v)", got, err)
	}
}

// Inherited Claude Code session markers and switches never reach the
// step; the adapter's own switches do — CLAUDE_CONFIG_DIR is hard-set by
// the adapter (polyforge#171: the claude-config dir of the harness
// root), an inherited operator value is stripped and never duplicated,
// and NONESSENTIAL traffic is switched off (value semantics: existence
// is ON, so "1" is the adapter's form).
func TestClaudeEnvStripsInheritedClaudeCode(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("CLAUDE_CODE_DISABLE_BACKGROUND_TASKS", "0")
	t.Setenv("CLAUDE_CODE_SSE_PORT", "1234")
	t.Setenv("CLAUDE_CONFIG_DIR", "/cfg")
	t.Setenv("ANTHROPIC_BASE_URL", "http://x")
	root := t.TempDir()
	sp := &claudeSpawner{cfg: &Config{HarnessRoot: root}}
	cmd, err := sp.buildCmd(SpawnParams{CapsuleDir: "/c", HarnessSession: testSession})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string][]string{}
	for _, kv := range cmd.Env {
		name, val, _ := strings.Cut(kv, "=")
		seen[name] = append(seen[name], val)
	}
	for _, gone := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT"} {
		if _, ok := seen[gone]; ok {
			t.Fatalf("%s leaked into the step env", gone)
		}
	}
	// the adapter's hard-set wins, exactly one entry (the operator's
	// inherited value is stripped, never duplicated)
	if v := seen["CLAUDE_CONFIG_DIR"]; len(v) != 1 || v[0] != filepath.Join(root, "claude-config") {
		t.Fatalf("CLAUDE_CONFIG_DIR entries %v, want exactly one %q", v, filepath.Join(root, "claude-config"))
	}
	if v := seen["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"]; len(v) != 1 || v[0] != "1" {
		t.Fatalf("NONESSENTIAL switch %v", v)
	}
	if v := seen["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"]; len(v) != 1 || v[0] != "1" {
		t.Fatalf("background switch %v, want exactly the adapter's 1", v)
	}
	if len(seen["CLAUDE_CODE_STARTUP_FAILURE_RESULTS"]) != 1 {
		t.Fatalf("CLAUDE_CODE_STARTUP_FAILURE_RESULTS = %v", seen["CLAUDE_CODE_STARTUP_FAILURE_RESULTS"])
	}
	if _, ok := seen["ANTHROPIC_BASE_URL"]; ok {
		t.Fatal("ANTHROPIC_BASE_URL leaked into the step env")
	}
}
