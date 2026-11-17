package runtime

// pf101_compact_probe_test.go (polyforge#101, AC7): the compact probe —
// the rule survives where the acceptance demands it:
//
//   - a pi main session carries the rule in its system prompt (the
//     context file pi loads, read back from pi's own session record);
//   - a Claude Code main session, after one `/compact`, still carries
//     the rule in the NEXT request (Claude Code's own session record:
//     the api-request-blob after the compact boundary, and the
//     instructions attachment it re-reads).
//
// The verdict helpers are pure and always tested against inline
// fixtures (the only always-on part). The probes run the REAL harness
// binaries, opt-in like the repo's other real-harness drives (w427):
//   - POLYFORGE_TEST_REAL_PI=1 runs the pi probe. It needs no
//     credential: the probe points pi at its own local fake
//     OpenAI-compatible endpoint, so the session is real (pi loads the
//     context file into the system prompt and records it) while the
//     model is a stub that answers one fixed reply.
//   - The claude probe runs in either mode: POLYFORGE_TEST_PF101_FAKE_CLAUDE=1
//     points Claude Code at the probe's own local fake Anthropic endpoint
//     (no credential; the judged behaviour — the harness's request
//     assembly across a compaction — is harness-side, not model-side);
//     POLYFORGE_TEST_REAL_CLAUDE=1 with POLYFORGE_TEST_CLAUDE_HARNESS_ROOT
//     (the w427 shape) copies the harness root's claude-config
//     settings.json in-process for the real gateway — the credentials
//     never reach a log, an artifact or the recording.
//   - POLYFORGE_TEST_PF101_RECORD_DIR names a directory the probes write
//     their JSON recordings into (versions, argv shape, the verdict and
//     a short excerpt around the rule; never a credential, never a
//     full transcript).
//
// The probes are bounded: every harness run is a foreground
// exec.CommandContext with a deadline, and the local endpoints close
// with the test.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
)

// pf101ClaudeCompactVerdict is what a claude session record says about
// the rule across one compaction.
type pf101ClaudeCompactVerdict struct {
	CompactSeen                bool `json:"compact_seen"`                 // a compact boundary record exists
	PostCompactRequest         bool `json:"post_compact_request"`         // an api-request-blob exists after the boundary
	RuleInPostCompactRequest   bool `json:"rule_in_post_compact_request"` // that request carries the rule bytes
	RuleInPostCompactEphemeral bool `json:"rule_in_post_compact_attachment"`
	Detail                     string
}

// pf101ClaudeCompactVerdict reads a claude session JSONL and answers
// whether, after the LAST compact boundary, the next api-request-blob
// still carries the rule — and whether the instructions attachment
// (the context file Claude Code re-reads) carries it too.
//
// Stub ahead of the fixtures (polyforge#101 u8): the zero-value body
// keeps the first red an assertion, never a compile error.
func pf101ClaudeCompactVerdictOf(raw []byte, rule string) pf101ClaudeCompactVerdict {
	return pf101ClaudeCompactVerdict{}
}

// pf101PiSystemVerdict is what a pi session record says about the rule.
type pf101PiSystemVerdict struct {
	SystemSeen      bool `json:"system_seen"`
	RuleInSystemCtx bool `json:"rule_in_system_context"`
	Detail          string
}

// pf101PiSystemVerdict reads a pi session JSONL and answers whether the
// system message's project-context section (the context file pi loads)
// carries the rule bytes.
//
// Stub ahead of the fixtures (polyforge#101 u8).
func pf101PiSystemVerdictOf(raw []byte, rule string) pf101PiSystemVerdict {
	return pf101PiSystemVerdict{}
}

// TestPF101ClaudeVerdictFromRecords: the verdict reads Claude Code's own
// record shapes — a compact boundary, then an api-request-blob that
// still carries the rule and an instructions attachment that re-reads
// the context file; a compact after which no request carries the rule
// is the negative.
func TestPF101ClaudeVerdictFromRecords(t *testing.T) {
	rule := harness.ConciseRule()
	blobWith := fmt.Sprintf(`{"type":"api-request-blob","sessionId":"s","hash":"h1","message":{"role":"user","content":[{"type":"text","text":"Codebase and user instructions are shown below"}]}}`) + "\n" +
		"PREFIX " + rule
	records := []string{
		`{"type":"user","message":{"role":"user","content":"turn 1"}}`,
		`{"type":"api-request-blob","message":{"role":"user","content":"no rule here yet"}}`,
		`{"type":"system","subtype":"compact_boundary","content":"Conversation compacted"}`,
		`{"type":"attachment","attachment":{"type":"instructions","files":[{"path":"/h/.claude/AGENTS.md","type":"Project","content":"` + strings.ReplaceAll(strings.TrimSuffix(rule, "\n"), "\n", "\\n") + `"}]}}`,
		blobWith,
	}
	v := pf101ClaudeCompactVerdictOf([]byte(strings.Join(records, "\n")), rule)
	if !v.CompactSeen || !v.PostCompactRequest || !v.RuleInPostCompactRequest || !v.RuleInPostCompactEphemeral {
		t.Fatalf("a compacted session whose next request carries the rule: %+v (detail %q)", v, v.Detail)
	}
	// the boundary is the LAST one: a rule before it does not count
	negative := []string{
		`{"type":"api-request-blob","message":{"role":"user","content":"PREFIX ` + strings.ReplaceAll(strings.TrimSuffix(rule, "\n"), "\n", "\\n") + `"}}`,
		`{"type":"system","subtype":"compact_boundary","content":"Conversation compacted"}`,
		`{"type":"api-request-blob","message":{"role":"user","content":"no rule after the boundary"}}`,
	}
	v = pf101ClaudeCompactVerdictOf([]byte(strings.Join(negative, "\n")), rule)
	if !v.CompactSeen || !v.PostCompactRequest || v.RuleInPostCompactRequest || v.RuleInPostCompactEphemeral {
		t.Fatalf("a rule only BEFORE the boundary must not count: %+v (detail %q)", v, v.Detail)
	}
	// no compact at all: not this probe's question
	none := []string{`{"type":"api-request-blob","message":{"role":"user","content":"PREFIX ` + strings.ReplaceAll(strings.TrimSuffix(rule, "\n"), "\n", "\\n") + `"}}`}
	v = pf101ClaudeCompactVerdictOf([]byte(strings.Join(none, "\n")), rule)
	if v.CompactSeen {
		t.Fatalf("no compact boundary, but one was seen: %+v", v)
	}
}

// TestPF101PiVerdictFromSession: the verdict reads pi's own session
// record — the system message's project-context section carrying the
// context file verbatim.
func TestPF101PiVerdictFromSession(t *testing.T) {
	rule := harness.ConciseRule()
	ctx := "<project_instructions path=\"/a/AGENTS.md\">\n" + rule + "</project_instructions>"
	session := []string{
		`{"type":"session","version":3,"id":"s","cwd":"/w"}`,
		`{"type":"message","message":{"role":"system","content":"","sections":{"preamble":"p","project_context":` + quoteJSON(t, ctx) + `}}}`,
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"say OK"}]}}`,
	}
	v := pf101PiSystemVerdictOf([]byte(strings.Join(session, "\n")), rule)
	if !v.SystemSeen || !v.RuleInSystemCtx {
		t.Fatalf("a pi session whose system prompt carries the context file: %+v (detail %q)", v, v.Detail)
	}
	v = pf101PiSystemVerdictOf([]byte(`{"type":"session","version":3}`), rule)
	if v.SystemSeen || v.RuleInSystemCtx {
		t.Fatalf("a session without a system message must not pass: %+v", v)
	}
}

// quoteJSON answers s as a JSON string literal.
func quoteJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// The local fake endpoints the probes point the harnesses at.

// pf101FakeOpenAI answers one fixed reply on /v1/chat/completions,
// streaming or not (pi's openai-completions api).
func pf101FakeOpenAI(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["stream"]; !ok {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"PROBE-REPLY"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"PROBE-REPLY"}}]}`+"\n\n")
		fmt.Fprint(w, "data: "+`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// pf101FakeAnthropic answers one fixed reply on /v1/messages, streaming
// or not (Claude Code's anthropic api).
func pf101FakeAnthropic(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["stream"]; !ok {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"PROBE-REPLY"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range []string{
			`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"m","stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"PROBE-REPLY"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", ev)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// pf101Record writes one probe's recording (never a credential, never a
// full transcript).
func pf101Record(t *testing.T, name string, v any) {
	t.Helper()
	dir := os.Getenv("POLYFORGE_TEST_PF101_RECORD_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("record dir: %v", err)
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), raw, 0o644); err != nil {
		t.Fatalf("write recording: %v", err)
	}
}

// pf101RunOf runs one bounded foreground harness command.
func pf101RunOf(t *testing.T, name string, bin string, args []string, env []string, dir string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("%s: %v (%s)", name, err, out.String())
	}
	return out.String(), code
}

// TestPF101PiMainSessionRuleInSystemPrompt (opt-in,
// POLYFORGE_TEST_REAL_PI=1): a real pi main session whose agent dir's
// AGENTS.md carries the guidance block records the rule in its system
// prompt's project-context section — read back from pi's own session
// file. No credential: pi points at the probe's local fake endpoint.
func TestPF101PiMainSessionRuleInSystemPrompt(t *testing.T) {
	if os.Getenv("POLYFORGE_TEST_REAL_PI") != "1" {
		t.Skip("set POLYFORGE_TEST_REAL_PI=1 to run the real-pi probe (it needs the pi binary on PATH)")
	}
	bin := os.Getenv("POLYFORGE_TEST_PI_BIN")
	if bin == "" {
		bin = "pi"
	}
	srv := pf101FakeOpenAI(t)
	agentDir := t.TempDir()
	sessions := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte(harness.ConciseGuidanceBlock()), 0o644); err != nil {
		t.Fatal(err)
	}
	models := fmt.Sprintf(`{"providers":{"probe":{"baseUrl":%q,"api":"openai-completions","apiKey":"probe","models":[{"id":"probe/model-a"}]}}}`, srv.URL+"/v1")
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "PI_CODING_AGENT_DIR="+agentDir)
	out, code := pf101RunOf(t, "pi", bin, []string{"--session-dir", sessions, "-p", "--mode", "json", "--model", "probe/model-a", "Reply with exactly: PROBE-REPLY"}, env, t.TempDir())
	if code != 0 {
		t.Fatalf("pi: exit %d\n%s", code, out)
	}
	files, _ := filepath.Glob(filepath.Join(sessions, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("pi session files = %v (output %s)", files, out)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	v := pf101PiSystemVerdictOf(raw, harness.ConciseRule())
	if !v.RuleInSystemCtx {
		t.Fatalf("the pi session's system prompt does not carry the rule: %+v\n%s", v, out)
	}
	pf101Record(t, "pf101-pi-main-session", map[string]any{
		"harness": "pi", "binary": bin, "verdict": v,
		"session_file": filepath.Base(files[0]),
	})
}

// TestPF101ClaudeCompactKeepsTheRule (opt-in): a real Claude Code main
// session — its HOME context file carrying the guidance block — after
// one `/compact` still carries the rule in the next request, read back
// from Claude Code's own session record. Either mode:
// POLYFORGE_TEST_PF101_FAKE_CLAUDE=1 (the probe's local fake Anthropic
// endpoint, no credential) or POLYFORGE_TEST_REAL_CLAUDE=1 with
// POLYFORGE_TEST_CLAUDE_HARNESS_ROOT (the w427 shape: the harness
// root's claude-config settings.json is copied in-process for the real
// gateway; its credentials never reach a log or the recording).
func TestPF101ClaudeCompactKeepsTheRule(t *testing.T) {
	fake := os.Getenv("POLYFORGE_TEST_PF101_FAKE_CLAUDE") == "1"
	real := os.Getenv("POLYFORGE_TEST_REAL_CLAUDE") == "1"
	if !fake && !real {
		t.Skip("set POLYFORGE_TEST_PF101_FAKE_CLAUDE=1 (local fake endpoint, no credential) or POLYFORGE_TEST_REAL_CLAUDE=1 with POLYFORGE_TEST_CLAUDE_HARNESS_ROOT (the w427 shape) to run the claude probe")
	}
	if testing.Short() {
		t.Skip("short mode: skipping the claude probe")
	}
	bin := os.Getenv("POLYFORGE_TEST_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	ccDir := t.TempDir() // CLAUDE_CONFIG_DIR: settings + the session record
	home := t.TempDir()  // HOME: the ~/.claude/AGENTS.md claude loads
	work := t.TempDir()  // the session's cwd
	model := os.Getenv("POLYFORGE_TEST_CLAUDE_MODEL")
	if fake {
		srv := pf101FakeAnthropic(t)
		model = "fake/model-x"
		if err := os.WriteFile(filepath.Join(ccDir, "settings.json"),
			[]byte(fmt.Sprintf(`{"env":{"ANTHROPIC_AUTH_TOKEN":"probe","ANTHROPIC_BASE_URL":%q}}`, srv.URL)), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		root := os.Getenv("POLYFORGE_TEST_CLAUDE_HARNESS_ROOT")
		if root == "" {
			t.Skip("set POLYFORGE_TEST_CLAUDE_HARNESS_ROOT to the harness root whose claude-config/settings.json the probe runs with")
		}
		raw, err := os.ReadFile(filepath.Join(root, "claude-config", "settings.json"))
		if err != nil {
			t.Skipf("no claude-config settings.json under %s: %v", root, err)
		}
		if err := os.WriteFile(filepath.Join(ccDir, "settings.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if model == "" {
			model = "claude-opus-5-5"
		}
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "AGENTS.md"), []byte(harness.ConciseGuidanceBlock()), 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := harness.NewSessionID() // the daemon's own uuid minting; any canonical uuid works
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "HOME="+home, "CLAUDE_CONFIG_DIR="+ccDir)
	run := func(args ...string) string {
		full := append([]string{"-p", "--model", model}, args...)
		out, code := pf101RunOf(t, "claude "+args[0], bin, full, env, work)
		if code != 0 {
			t.Fatalf("claude %v: exit %d\n%s", args, code, out)
		}
		return out
	}
	run("--session-id", session, "Reply with exactly: TURN1")
	run("--resume", session, "/compact")
	run("--resume", session, "Reply with exactly: TURN2")

	files, _ := filepath.Glob(filepath.Join(ccDir, "projects", "*", session+".jsonl"))
	if len(files) != 1 {
		t.Fatalf("claude session files = %v", files)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	v := pf101ClaudeCompactVerdictOf(raw, harness.ConciseRule())
	if !v.CompactSeen || !v.RuleInPostCompactRequest {
		t.Fatalf("after one /compact the next request does not carry the rule: %+v (detail %q)", v, v.Detail)
	}
	pf101Record(t, "pf101-claude-compact", map[string]any{
		"harness": "claude", "binary": bin, "mode": map[bool]string{true: "fake-endpoint", false: "real-gateway"}[fake], "verdict": v,
	})
}
