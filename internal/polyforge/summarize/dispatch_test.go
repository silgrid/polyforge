package summarize

// dispatch_test.go: the one-shot dispatch seam's contract — the argv
// shape (print mode first, the claude context flag per the auth-mode
// ruling, then exactly the model; the PROMPT rides stdin, never argv),
// the claude context flag pick from the harness config's settings.json
// env block (the same helpers the step spawn uses), the 5-minute bound
// (a hung call times out into an error, never a stall), the raw failure
// text (the harness's own stderr wording, never faked), and the prompt's
// bounds (the ≤200-record / 32 KB tail, oldest first; the held line
// whitespace-collapsed and hard-capped at 200 characters).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
)

// The argv shape: print mode first, then the claude context flag, then
// exactly the candidate's model id. The prompt never appears in argv —
// the argv is exactly this shape, nothing else.
func TestDispatchArgsShape(t *testing.T) {
	pi, err := harness.ParseCandidate("pi:example-gateway/model-a")
	if err != nil {
		t.Fatal(err)
	}
	if got := dispatchArgs(pi, ""); strings.Join(got, " ") != "-p --model example-gateway/model-a" {
		t.Fatalf("pi argv = %v", got)
	}
	cl, err := harness.ParseCandidate("claude:claude-opus-5-5")
	if err != nil {
		t.Fatal(err)
	}
	if got := dispatchArgs(cl, "--bare"); strings.Join(got, " ") != "-p --bare --model claude-opus-5-5" {
		t.Fatalf("claude argv = %v", got)
	}
	if got := dispatchArgs(cl, "--safe-mode"); strings.Join(got, " ") != "-p --safe-mode --model claude-opus-5-5" {
		t.Fatalf("claude safe-mode argv = %v", got)
	}
}

// The claude context flag per the 2026-10-19 ruling: --bare in token
// auth mode (a non-empty ANTHROPIC_AUTH_TOKEN or ANTHROPIC_API_KEY in
// the settings env block), --safe-mode in OAuth mode (no token key, or
// no settings file at all); a malformed settings.json is an error.
func TestClaudeContextFlagPerAuthMode(t *testing.T) {
	ccDir := t.TempDir()
	if flag, err := claudeContextFlag(filepath.Join(ccDir, "absent")); err != nil || flag != "--safe-mode" {
		t.Fatalf("no settings file: %q, %v — want --safe-mode", flag, err)
	}
	token := filepath.Join(ccDir, "token")
	if err := os.MkdirAll(token, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(token, "settings.json"),
		[]byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"t","ANTHROPIC_BASE_URL":"https://gw.example"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if flag, err := claudeContextFlag(token); err != nil || flag != "--bare" {
		t.Fatalf("token mode: %q, %v — want --bare", flag, err)
	}
	oauth := filepath.Join(ccDir, "oauth")
	if err := os.MkdirAll(oauth, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oauth, "settings.json"), []byte(`{"env":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if flag, err := claudeContextFlag(oauth); err != nil || flag != "--safe-mode" {
		t.Fatalf("oauth mode: %q, %v — want --safe-mode", flag, err)
	}
	bad := filepath.Join(ccDir, "bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "settings.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := claudeContextFlag(bad); err == nil || !strings.Contains(err.Error(), "settings.json") {
		t.Fatalf("a malformed settings.json must be an error naming the file: %v", err)
	}
}

// scriptBin writes an executable shell script and answers its path.
func scriptBin(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake-harness")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// The prompt rides STDIN, never argv: a harness stand-in that answers
// exactly its stdin proves the roundtrip; the argv (pinned above)
// carries no prompt bytes.
func TestDispatchPromptRidesStdin(t *testing.T) {
	bin := scriptBin(t, "cat")
	d := &ExecDispatcher{PiBin: bin}
	prompt := strings.Repeat("transcript tail line\n", 500) // ~8.5 KB — over any per-arg doubt
	out, err := d.Dispatch("pi:m", prompt)
	if err != nil {
		t.Fatal(err)
	}
	if out != prompt {
		t.Fatalf("the prompt did not ride stdin verbatim: %d bytes back, %d sent", len(out), len(prompt))
	}
}

// One call is bounded by the timeout: a hung harness times out into an
// error (the harness's own exit wording), never a stall — the caller
// lands it in summary_error like any other failure.
func TestDispatchTimeoutBoundsHungCall(t *testing.T) {
	// exec sleep: the hung process IS the dispatched pid (no orphaned
	// grandchild holding the pipe) — the kill lands on it and the
	// dispatch answers at the bound. A hung process whose children hold
	// the pipe stalls only the background landing, never any snapshot
	// response (the lane's interval gate keeps spacing calls).
	bin := scriptBin(t, "exec sleep 30")
	d := &ExecDispatcher{PiBin: bin, Timeout: 60 * time.Millisecond}
	began := time.Now()
	if _, err := d.Dispatch("pi:m", "prompt"); err == nil {
		t.Fatal("a hung dispatch must time out")
	} else if !strings.Contains(err.Error(), "dispatch failed") {
		t.Fatalf("the timeout error must be the raw dispatch failure: %v", err)
	}
	if el := time.Since(began); el > 5*time.Second {
		t.Fatalf("the hung call was not bounded: %v", el)
	}
}

// A nonzero exit answers an error whose text carries the harness's RAW
// stderr wording — never faked, never collapsed.
func TestDispatchFailureIsRawText(t *testing.T) {
	bin := scriptBin(t, "echo 'harness exploded: model refused' >&2\nexit 1")
	d := &ExecDispatcher{PiBin: bin}
	_, err := d.Dispatch("pi:m", "prompt")
	if err == nil {
		t.Fatal("a nonzero exit must fail the dispatch")
	}
	if !strings.Contains(err.Error(), "harness exploded: model refused") {
		t.Fatalf("the failure text must carry the harness's raw stderr: %q", err.Error())
	}
}

// The dispatch carries NO step identity: the inherited daemon env is
// stripped of every step/harness-context marker — the acceptance
// persona discriminates on PF_WI_ID, and a daemon started from inside a
// session (the e2e daemon under a step) inherits markers its allow-list
// env would never carry.
func TestDispatchEnvStripsStepMarkers(t *testing.T) {
	got := dispatchEnv([]string{
		"PATH=/usr/bin", "PF_WI_ID=wi_x", "PF_DAEMON_ID=m-x", "PF_DAEMON_SOCKET=/x.sock",
		"PI_CODING_AGENT_DIR=/agent", "PI_CODING_AGENT_SESSION_DIR=/store",
		"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=chat", "CLAUDE_CONFIG_DIR=/inherited",
		"ANTHROPIC_AUTH_TOKEN=inherited-secret", "HOME=/root",
	})
	want := []string{"PATH=/usr/bin", "HOME=/root"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("dispatchEnv = %v, want %v (every marker stripped)", got, want)
	}
}

// The real dispatched process sees no marker either: a harness stand-in
// reports what its environment carries, run from a test process whose
// own environment carries all of them.
func TestDispatchCarriesNoStepIdentity(t *testing.T) {
	for _, kv := range []string{
		"PF_WI_ID=wi_under_test", "PF_DAEMON_ID=m_under_test",
		"PI_CODING_AGENT_DIR=/under/test", "PI_CODING_AGENT_SESSION_DIR=/under/test/store",
		"CLAUDECODE=1", "CLAUDE_CONFIG_DIR=/under/test", "ANTHROPIC_AUTH_TOKEN=none",
	} {
		name, val, _ := strings.Cut(kv, "=")
		t.Setenv(name, val)
	}
	bin := scriptBin(t, `for n in PF_WI_ID PF_DAEMON_ID PI_CODING_AGENT_DIR PI_CODING_AGENT_SESSION_DIR CLAUDECODE CLAUDE_CONFIG_DIR ANTHROPIC_AUTH_TOKEN; do eval "v=\$$n"; if [ -n "$v" ]; then echo "MARKED $n"; fi; done; echo CLEAN`)
	d := &ExecDispatcher{PiBin: bin}
	out, err := d.Dispatch("pi:m", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "MARKED") {
		t.Fatalf("the dispatched process carried a step/harness marker:\n%s", out)
	}
	if !strings.Contains(out, "CLEAN") {
		t.Fatalf("the dispatched process did not run: %q", out)
	}
}

// The prompt bounds: at most tailRecords records (the newest), at most
// tailBytes bytes (the newest bytes under the cap, oldest records drop
// first), oldest first in the prompt.
func TestBuildPromptBounds(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "live.jsonl")
	// 400 records: the prompt must carry only the last 200
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString(strings.Repeat("x", 8))
		b.WriteByte('\n')
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt, err := BuildPrompt(p, "中文")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(strings.SplitN(prompt, "oldest first):\n", 2)[1]), "\n")
	if len(lines) != tailRecords {
		t.Fatalf("the prompt carries %d records, want the newest %d", len(lines), tailRecords)
	}
	// the byte cap: one huge record under a tiny cap drops the OLDEST
	// records and keeps the newest bytes
	var c strings.Builder
	c.WriteString(strings.Repeat("o", 30000)) // an old record over half the byte cap
	c.WriteByte('\n')
	c.WriteString(strings.Repeat("n", 30000)) // the newest record
	c.WriteByte('\n')
	if err := os.WriteFile(p, []byte(c.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt, err = BuildPrompt(p, "中文")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, strings.Repeat("o", 100)) {
		t.Fatal("the byte cap must drop the oldest records, not keep them")
	}
	if !strings.Contains(prompt, strings.Repeat("n", 100)) {
		t.Fatal("the byte cap must keep the newest bytes")
	}
}

// The held line: the answer whitespace-collapsed to ONE line (no
// markdown, no newlines) and hard-capped at lineMaxChars characters.
// The prompt asks for the job's narration language verbatim and keeps
// identifiers, paths and commands as written (polyforge#243); it no
// longer defers to the transcript's own language.
func TestBuildPromptLanguage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(p, []byte(`{"type":"message"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"中文", "English (US)"} {
		prompt, err := BuildPrompt(p, lang)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(prompt, "Answer in "+lang+".") {
			t.Fatalf("the prompt does not ask for %q: %q", lang, prompt)
		}
		if !strings.Contains(prompt, "Keep code identifiers, file paths and commands as written.") {
			t.Fatalf("the prompt lacks the keep-identifiers clause: %q", prompt)
		}
		if strings.Contains(prompt, "the transcript's own language") {
			t.Fatalf("the prompt still defers to the transcript's language: %q", prompt)
		}
	}
}

func TestHoldLineCollapsesAndCaps(t *testing.T) {
	if got := HoldLine("  one   line\t\n  with   weird\n\nwhitespace  "); got != "one line with weird whitespace" {
		t.Fatalf("whitespace collapse: %q", got)
	}
	if got := HoldLine("a\nb\nc"); got != "a b c" {
		t.Fatalf("newlines must collapse: %q", got)
	}
	long := strings.Repeat("字", 300) // multi-byte: the cap is CHARACTERS
	held := HoldLine(long)
	if r := []rune(held); len(r) != lineMaxChars {
		t.Fatalf("the held line must cap at %d characters, got %d", lineMaxChars, len(r))
	}
	if held != string([]rune(long)[:lineMaxChars]) {
		t.Fatalf("the cap must cut the line, not transform it: %q", held)
	}
}

// The configured working directory (the daemon binds
// <harness-root>/summarize-cwd,) is where both harnesses
// run, created 0700 when missing — never the dispatcher's inherited cwd.
func TestDispatchRunsAtTheConfiguredDir(t *testing.T) {
	bin := scriptBin(t, "pwd -P")
	dir := filepath.Join(t.TempDir(), "summarize-cwd")
	d := &ExecDispatcher{PiBin: bin, ClaudeBin: func() string { return bin }, ClaudeConfigDir: filepath.Join(t.TempDir(), "cc"), Dir: dir}
	for _, candidate := range []string{"pi:m", "claude:claude-haiku-5-5"} {
		out, err := d.Dispatch(candidate, "prompt")
		if err != nil {
			t.Fatalf("%s dispatch: %v", candidate, err)
		}
		want, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(out); got != want {
			t.Fatalf("%s dispatch ran at %q, want the configured %q", candidate, got, want)
		}
	}
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("configured dir = %v, %v; want created 0700", st, err)
	}
}
