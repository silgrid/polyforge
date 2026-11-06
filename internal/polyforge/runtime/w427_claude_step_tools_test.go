package runtime

// w427_claude_step_tools_test.go: a claude step gets exactly its declared
// tool surface, in a clean context.
//
// Claude Code names the tools a session really has in the first record
// of its stream, `system/init.tools`; the step declares ClaudeStepTools.
// The two drifted apart without a sound: Claude Code 2.1.319 under
// --bare offers Bash, Edit and Read whatever --tools names, and offers
// TodoWrite only while its Tasks family is switched off.
//
// The replay test reads `system/init` records recorded from real probes
// (testdata/claude-step-init/*.json). A recording carries the argv and the
// adapter's env switches it ran with; the test builds the step's argv/env
// in token mode and in OAuth mode, takes every recording made with exactly
// that profile, and requires its tools to equal ClaudeStepTools and its
// context to be empty. A changed argv with no recording, or a recording
// whose tools differ — a Claude Code upgrade that narrows the surface,
// re-recorded by the live probe below — is red.
//
// The live probe (TestClaudeStepRealProbe) runs the REAL binary with the
// step's argv/env in a capsule planted with everything a clean step must
// not see, and reads Claude Code's own session record of every request
// body it sent. OPT-IN like the repo's other real-harness drives: it
// burns model tokens with the harness root's credentials, so it runs only
// with POLYFORGE_TEST_REAL_CLAUDE=1 and POLYFORGE_TEST_CLAUDE_HARNESS_ROOT
// naming the harness root; POLYFORGE_TEST_CLAUDE_RECORD=1 (re)writes
// the recording for the profile it ran.

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/claudeinit"
)

// The step's model and effort every recording is made with: the tool
// surface depends on the model (Claude Code offers its todo tools per
// model family), so a recording stands for the profile it names.
const (
	claudeProbeModel  = "claude-opus-5-5"
	claudeProbeEffort = "low"
)

// claudeInitRecordingsDir holds the recorded `system/init` records.
const claudeInitRecordingsDir = "testdata/claude-step-init"

// claudeProbeAgent is the custom agent the live probe plants in the
// capsule's .claude/agents/: a clean step lists only Claude Code's own.
const claudeProbeAgent = "probe-agent"

// claudeProbeParams answers the spawn parameters every recording and the
// replay use (a fresh dispatch; --resume differs only in its session flag,
// pinned by the argv golden).
func claudeProbeParams(capsuleDir, session string) SpawnParams {
	return SpawnParams{WorkItemID: "wi_probe", CapsuleDir: capsuleDir, Model: claudeProbeModel,
		Effort: claudeProbeEffort, HarnessSession: session}
}

func loadClaudeInitRecordings(t *testing.T) []claudeinit.Recording {
	t.Helper()
	recs, err := claudeinit.Load(claudeInitRecordingsDir)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func decodeClaudeInit(t *testing.T, raw json.RawMessage, where string) claudeinit.Init {
	t.Helper()
	in, err := claudeinit.Decode(raw)
	if err != nil || len(in.Tools) == 0 {
		t.Fatalf("%s: no system/init tools (%v)", where, err)
	}
	return in
}

// The step's real tool surface equals the declared one, in token mode and
// in OAuth mode, and its context is empty — read from a real recording of
// the exact argv/env the spawner builds.
func TestClaudeStepInitToolsEqualDeclared(t *testing.T) {
	recs := loadClaudeInitRecordings(t)
	for _, mode := range []struct{ name, settings string }{
		{"token mode", `{"env":{"ANTHROPIC_AUTH_TOKEN":"tok-SENTINEL-SENTINEL-427","ANTHROPIC_BASE_URL":"https://gw.example"}}`},
		{"OAuth mode", `{"env":{}}`},
		{"no settings.json", ""},
	} {
		cfg := &Config{HarnessRoot: t.TempDir(), MachineID: "m-1"}
		if mode.settings != "" {
			if err := os.MkdirAll(cfg.ClaudeConfigDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cfg.ClaudeConfigDir(), "settings.json"), []byte(mode.settings), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cmd, err := (&claudeSpawner{cfg: cfg}).buildCmd(claudeProbeParams("/capsule", testSession))
		if err != nil {
			t.Fatalf("%s: %v", mode.name, err)
		}
		argv, env := claudeinit.Profile(cmd)
		matched := claudeinit.Matching(recs, argv, env)
		for _, r := range matched {
			in := decodeClaudeInit(t, r.Init, r.File)
			if missing, extra := claudeinit.ToolSurfaceDiff(in.Tools, ClaudeStepTools); len(missing)+len(extra) > 0 {
				t.Errorf("%s: Claude Code %s gives the step the tools %q (%s), not ClaudeStepTools %s: missing %q, extra %q",
					mode.name, r.ClaudeCodeVersion, in.Tools, r.File, ClaudeStepTools, missing, extra)
			}
			for _, bg := range ClaudeBackgroundTools {
				if slices.Contains(in.Tools, bg) {
					t.Errorf("%s: %s offers the background tool %s", mode.name, r.File, bg)
				}
			}
			// a context finding includes a cross-session inbox
			if f := claudeinit.ContextFindings(in, claudeProbeAgent); len(f) > 0 {
				t.Errorf("%s: %s shows a step context that is not clean: %s", mode.name, r.File, strings.Join(f, "; "))
			}
		}
		if len(matched) == 0 {
			t.Errorf("%s: no recorded system/init for the step's profile\n argv %q\n env  %q\nrecord one with the live probe: "+
				"POLYFORGE_TEST_REAL_CLAUDE=1 POLYFORGE_TEST_CLAUDE_HARNESS_ROOT=<harness root> POLYFORGE_TEST_CLAUDE_RECORD=1 go test ./internal/polyforge/runtime -run TestClaudeStepRealProbe",
				mode.name, argv, env)
		}
	}
}

// The comparison has teeth: the 2.1.319 recording of the old token-mode
// profile (--bare) is rejected, naming the five tools --bare drops.
func TestClaudeInitToolsRejectsBareRecording(t *testing.T) {
	var bare *claudeinit.Recording
	for _, r := range loadClaudeInitRecordings(t) {
		if slices.Contains(r.Argv, "--bare") {
			bare = &r
			break
		}
	}
	if bare == nil {
		t.Fatal("no --bare recording: the negative control is gone")
	}
	in := decodeClaudeInit(t, bare.Init, bare.File)
	missing, extra := claudeinit.ToolSurfaceDiff(in.Tools, ClaudeStepTools)
	if want := []string{"Agent", "Glob", "Grep", "TodoWrite", "Write"}; !slices.Equal(missing, want) || len(extra) != 0 {
		t.Fatalf("the --bare recording %q: missing %q extra %q, want missing %q and nothing extra", in.Tools, missing, extra, want)
	}
	// and the legacy init name is read as the tool --tools names
	if m, e := claudeinit.ToolSurfaceDiff([]string{"Task", "Bash"}, "Bash,Agent"); len(m)+len(e) != 0 {
		t.Fatalf("Task must read as Agent: missing %q extra %q", m, e)
	}
	if _, e := claudeinit.ToolSurfaceDiff([]string{"Bash", "Bash"}, "Bash"); !slices.Equal(e, []string{"Bash"}) {
		t.Fatalf("a tool listed twice must be extra: %q", e)
	}
	// an empty declaration is the empty surface (the summarize lane's)
	if m, e := claudeinit.ToolSurfaceDiff([]string{"Read"}, ""); len(m) != 0 || !slices.Equal(e, []string{"Read"}) {
		t.Fatalf("against an empty declaration every tool is extra: missing %q extra %q", m, e)
	}
	// and a recording's cross-session inbox is a context finding
	if f := claudeinit.ContextFindings(claudeinit.Init{MessagingSocketPath: "/r/cc-socks/1.sock"}, ""); len(f) != 1 {
		t.Fatalf("an inbox must be one context finding: %q", f)
	}
}

// The live probe's planted codewords: one per thing a clean step must not
// load. None may appear in any request body Claude Code sent for the step
// or for the sub-agent it dispatched.
var claudeProbeCodewords = map[string]string{
	"ancestor AGENTS.md":           "PELICAN-W427-ANCESTOR-AGENTS",
	"ancestor .claude/AGENTS.md":   "CRANE-W427-ANCESTOR-DOTCLAUDE-AGENTS",
	"cwd AGENTS.md":                "WALRUS-W427-CWD-AGENTS",
	"nested AGENTS.md (on Read)":   "GECKO-W427-NESTED-AGENTS",
	"nested CLAUDE.md (on Read)":   "HERON-W427-NESTED-CLAUDE",
	"auto-memory MEMORY.md":        "MARMOT-W427-AUTO-MEMORY",
	"capsule skill":                "IBIS-W427-SKILL",
	"capsule custom agent":         "LYNX-W427-AGENT",
	"capsule custom slash command": "NEWT-W427-COMMAND",
}

const claudeProbePrompt = `Step 1, without using any tool: output MAIN: <every codeword any instruction, memory or system text you were given names, comma-separated, or NONE>. ` +
	`Step 2: use the Read tool on sub/file.txt, then output AFTER-READ: <every codeword now in your context, or NONE>. ` +
	`Step 3: call the Agent tool exactly once, in the foreground, subagent_type "general-purpose", with the prompt ` +
	`"Step 1, without any tool: answer SUB: <every codeword any instruction, memory or system text you were given names, or NONE>. ` +
	`Step 2: Read the file sub/file.txt in the current directory, then answer SUB-AFTER-READ: <every codeword now in your context, or NONE>." ` +
	`Step 4: use the Bash tool to run exactly this command: env | cut -d= -f1 | grep ^CLAUDE_CODE_MESSAGING_ > ../inbox-vars; true ` +
	`Finally output the MAIN, AFTER-READ, SUB and SUB-AFTER-READ lines.`

func newProbeSession(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func writeProbeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestClaudeStepRealProbe runs the real Claude Code with the step's argv
// and env (token or OAuth mode, whichever the harness root's
// claude-config holds) in a capsule planted with: AGENTS.md above it
// (plain and under .claude/, with no CLAUDE.md anywhere up the path, so
// the built-in cc-plugin-agents-md would load them), AGENTS.md in it, a
// nested AGENTS.md and CLAUDE.md beside the file it Reads, an auto-memory
// MEMORY.md, a skill, a custom agent and command, hooks, an MCP server,
// and project/local settings that redirect the API endpoint and deny
// reading the file; its Bash lists the step's messaging variables.
// It requires: init.tools equal ClaudeStepTools; none of the
// planted context in any request body of the step or of its sub-agent
// (Claude Code's own session record, `api-request-blob`); no hook or MCP
// server ran; the run reached the model through the harness's own
// settings.json (the redirect was ignored) and Read was not denied; the
// memory dir holds only what was planted; the step opened no cross-session
// inbox (no socket under its TMPDIR) and its Bash saw no messaging variable.
func TestClaudeStepRealProbe(t *testing.T) {
	if os.Getenv("POLYFORGE_TEST_REAL_CLAUDE") != "1" {
		t.Skip("this probe launches a REAL claude process with the harness root's credentials; set POLYFORGE_TEST_REAL_CLAUDE=1 to run it")
	}
	if testing.Short() {
		t.Skip("short mode: skipping the real-claude probe")
	}
	bin := os.Getenv("POLYFORGE_TEST_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	// the package's TestMain points HOME at a temp dir, so the root is
	// always named: the daemon's is <data-dir> unless --harness-root says
	// otherwise (usually ~/.local/share/polyforge)
	root := os.Getenv("POLYFORGE_TEST_CLAUDE_HARNESS_ROOT")
	if root == "" {
		t.Skip("set POLYFORGE_TEST_CLAUDE_HARNESS_ROOT to the harness root whose claude-config/settings.json the probe runs with")
	}
	cfg := &Config{ClaudeBin: bin, HarnessRoot: root, MachineID: "probe"}
	ccDir := cfg.ClaudeConfigDir()
	if _, err := os.Stat(filepath.Join(ccDir, "settings.json")); err != nil {
		t.Skipf("no claude-config settings.json under %s: %v", ccDir, err)
	}

	// the planted tree: base/ is the capsule's ancestor, base/ws/cap the capsule
	base := t.TempDir()
	capDir := filepath.Join(base, "ws", "cap")
	cw := claudeProbeCodewords
	writeProbeFile(t, filepath.Join(base, "AGENTS.md"), "The ancestor AGENTS codeword is "+cw["ancestor AGENTS.md"]+".\n")
	writeProbeFile(t, filepath.Join(base, ".claude", "AGENTS.md"), "The ancestor dot-claude AGENTS codeword is "+cw["ancestor .claude/AGENTS.md"]+".\n")
	writeProbeFile(t, filepath.Join(capDir, "AGENTS.md"), "The cwd AGENTS codeword is "+cw["cwd AGENTS.md"]+".\n")
	writeProbeFile(t, filepath.Join(capDir, "sub", "AGENTS.md"), "The nested AGENTS codeword is "+cw["nested AGENTS.md (on Read)"]+".\n")
	writeProbeFile(t, filepath.Join(capDir, "sub", "CLAUDE.md"), "The nested CLAUDE codeword is "+cw["nested CLAUDE.md (on Read)"]+".\n")
	writeProbeFile(t, filepath.Join(capDir, "sub", "file.txt"), "hello\n")
	writeProbeFile(t, filepath.Join(capDir, ".claude", "skills", "probe-skill", "SKILL.md"),
		"---\nname: probe-skill\ndescription: "+cw["capsule skill"]+" probe skill\n---\nSay it.\n")
	writeProbeFile(t, filepath.Join(capDir, ".claude", "agents", claudeProbeAgent+".md"),
		"---\nname: "+claudeProbeAgent+"\ndescription: "+cw["capsule custom agent"]+" probe agent\n---\nSay it.\n")
	writeProbeFile(t, filepath.Join(capDir, ".claude", "commands", "probe-cmd.md"),
		"---\ndescription: "+cw["capsule custom slash command"]+" probe command\n---\nSay it.\n")
	marker := filepath.Join(base, "hook-fired")
	hook := func(ev string) string {
		return `[{"hooks":[{"type":"command","command":"echo ` + ev + ` >> ` + marker + `"}]}]`
	}
	writeProbeFile(t, filepath.Join(capDir, ".claude", "settings.json"),
		`{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9"},"hooks":{"SessionStart":`+hook("SessionStart")+
			`,"UserPromptSubmit":`+hook("UserPromptSubmit")+`,"PreToolUse":`+hook("PreToolUse")+`,"SubagentStart":`+hook("SubagentStart")+`}}`)
	writeProbeFile(t, filepath.Join(capDir, ".claude", "settings.local.json"), `{"permissions":{"deny":["Read(**/file.txt)"]}}`)
	mcpMarker := filepath.Join(base, "mcp-started")
	writeProbeFile(t, filepath.Join(capDir, ".mcp.json"),
		`{"mcpServers":{"probe-mcp":{"command":"sh","args":["-c","echo mcp >> `+mcpMarker+`; sleep 5"]}}}`)
	if out, err := exec.Command("git", "init", "-q", capDir).CombinedOutput(); err != nil {
		t.Fatalf("git init the capsule: %v %s", err, out)
	}
	projDir := filepath.Join(ccDir, "projects", harness.ClaudeProjectDirName(capDir))
	memBody := "- The memory codeword is " + cw["auto-memory MEMORY.md"] + ".\n"
	writeProbeFile(t, filepath.Join(projDir, "memory", "MEMORY.md"), memBody)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("kept the probe's session record for inspection: %s", projDir)
			return
		}
		os.RemoveAll(projDir)
	})

	session := newProbeSession(t)
	cmd, err := (&claudeSpawner{cfg: cfg}).buildCmd(claudeProbeParams(capDir, session))
	if err != nil {
		t.Fatal(err)
	}
	// the prompt as the step's first stream-json user message; EOF after it
	// ends the process once the turn is over (the interactive contract is
	// claude_interactive_test.go's probe)
	frame, err := claudeUserMessage(claudeInitMessageID(1), claudeProbePrompt)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = bytes.NewReader(frame)
	childTmp := t.TempDir()
	cmd.Env = append(cmd.Env, "TMPDIR="+childTmp) // claude's own scratch (claude-<uid>/) goes with the test
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(8 * time.Minute):
		cmd.Process.Kill()
		<-done
		t.Fatalf("the probe ran past 8m; stderr: %s", stderr.String())
	}

	var initRaw json.RawMessage
	var result struct {
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	var denied []string
	sc := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var rec struct {
			Type     string `json:"type"`
			Subtype  string `json:"subtype"`
			ToolName string `json:"tool_name"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		switch {
		case rec.Type == "system" && rec.Subtype == "init" && initRaw == nil:
			initRaw = append(json.RawMessage(nil), sc.Bytes()...)
		case rec.Type == "system" && rec.Subtype == "permission_denied":
			denied = append(denied, rec.ToolName)
		case rec.Type == "result":
			_ = json.Unmarshal(sc.Bytes(), &result)
		}
	}
	if initRaw == nil {
		t.Fatalf("no system/init record (exit %v); stderr: %s", runErr, stderr.String())
	}
	in := decodeClaudeInit(t, initRaw, "the probe's stream")
	if os.Getenv("POLYFORGE_TEST_CLAUDE_RECORD") == "1" {
		argv, env := claudeinit.Profile(cmd)
		name := fmt.Sprintf("claude-%s-%s.json", in.Version, strings.TrimLeft(argv[2], "-"))
		if err := claudeinit.Write(claudeInitRecordingsDir, name, argv, env, nil, initRaw, in.Version, "",
			map[string]string{capDir: "/capsule", base: "/capsule-parent", ccDir: "/claude-config", childTmp: "<runtime-dir>"}); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %s", name)
	}

	if missing, extra := claudeinit.ToolSurfaceDiff(in.Tools, ClaudeStepTools); len(missing)+len(extra) > 0 {
		t.Errorf("Claude Code %s gives the step the tools %q, not ClaudeStepTools %s: missing %q, extra %q",
			in.Version, in.Tools, ClaudeStepTools, missing, extra)
	}
	if f := claudeinit.ContextFindings(in, claudeProbeAgent); len(f) > 0 {
		t.Errorf("the step's init shows a context that is not clean: %s", strings.Join(f, "; "))
	}
	if runErr != nil || result.IsError {
		t.Fatalf("the probe did not reach the model through the harness's settings.json (exit %v, result %q): "+
			"a capsule's project settings may have redirected it; stderr: %s", runErr, result.Result, stderr.String())
	}
	if len(denied) > 0 {
		t.Errorf("tools denied by a capsule's local settings: %q", denied)
	}
	for _, m := range []string{marker, mcpMarker} {
		if b, err := os.ReadFile(m); err == nil {
			t.Errorf("%s exists: a capsule hook or MCP server ran (%q)", filepath.Base(m), strings.Fields(string(b)))
		}
	}
	// no cross-session inbox — no socket under the step's
	// TMPDIR, and the step's Bash saw no messaging variable to reach one
	if socks, err := os.ReadDir(filepath.Join(childTmp, "cc-socks")); err == nil {
		t.Errorf("the step opened a cross-session inbox: %s/cc-socks holds %d entries", childTmp, len(socks))
	}
	if b, err := os.ReadFile(filepath.Join(base, "ws", "inbox-vars")); err != nil {
		t.Errorf("the step's Bash did not list its messaging variables (%v): the inbox went unprobed from the Bash side", err)
	} else if vars := strings.Fields(string(b)); len(vars) > 0 {
		t.Errorf("the step's Bash sees the messaging variables %q", vars)
	}
	ents, _ := os.ReadDir(filepath.Join(projDir, "memory"))
	if len(ents) != 1 || ents[0].Name() != "MEMORY.md" {
		t.Errorf("the auto-memory dir holds %v, want only the planted MEMORY.md", ents)
	} else if b, _ := os.ReadFile(filepath.Join(projDir, "memory", "MEMORY.md")); string(b) != memBody {
		t.Errorf("the planted MEMORY.md was rewritten: %q", b)
	}

	// what was sent: Claude Code's own session record of every request
	// body (api-request-blob), for the step and for each sub-agent
	mainRec := harness.LocateClaudeSession(session, ccDir, capDir)
	if mainRec == "" {
		t.Fatalf("no session record for %s under %s", session, projDir)
	}
	sent := map[string]string{"the step": mainRec}
	for _, f := range harness.ClaudeSubagentFiles(session, ccDir, capDir) {
		if strings.HasSuffix(f, ".jsonl") {
			sent["sub-agent "+filepath.Base(f)] = f
		}
	}
	if len(sent) < 2 {
		t.Errorf("the step dispatched no sub-agent (no subagents/*.jsonl): the sub-agent's context went unprobed")
	}
	texts := map[string]string{"the step's stream": stdout.String()}
	for who, f := range sent {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(b, []byte(`"type":"api-request-blob"`)) {
			t.Errorf("%s: the session record %s carries no request body (api-request-blob): nothing shows what was sent", who, f)
		}
		texts[who] = string(b)
	}
	for who, text := range texts {
		for what, word := range cw {
			if strings.Contains(text, word) {
				t.Errorf("%s carries the %s (%s)", who, what, word)
			}
		}
	}
	t.Logf("Claude Code %s, init.tools %q, answer %q", in.Version, in.Tools, result.Result)
}
