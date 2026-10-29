// claude.go: the fake `claude` persona of this test binary
// (polyforge#159). The daemon runs it as a claude step process
// (--claude-bin pointing at this same binary): argv[1] == "-p" selects
// the persona, `--version` answers a version line. It speaks the
// headless Claude Code surface the runtime's claude spawner pins:
//
//   - stdin: the whole step prompt, read to EOF;
//   - stdout: stream-json records (system/init, stream_event deltas,
//     assistant tool_use, user tool_result, system/api_retry, result);
//   - the native session file $CLAUDE_CONFIG_DIR|$HOME/.claude/projects/
//     <dashed cwd>/<uuid>.jsonl (--session-id fresh, --resume continues
//     the same file) — the daemon copies it into the capsule;
//   - after the result record the process EXITS (unlike pi).
//
// The scripted behavior is the SAME plan file and envelope-action
// indexing as the pi persona (the Nth envelope action fires for spawn
// N); the claude-only shapes ride planAction.Claude*. Every derivation
// journals its argv and the env facts (the token only as set/unset) to
// logs/fakeclaude.jsonl, and its prompt to logs/fakeclaude-prompt-<N>.txt.
//
// The persona never calls a model and never touches a real credential.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const claudeJournalPath = "logs/fakeclaude.jsonl"

// fakeClaudeVersion is what `--version` answers (the verified version).
const fakeClaudeVersion = "2.1.296"

func claudeArg(name string) string {
	for i := 0; i+1 < len(os.Args); i++ {
		if os.Args[i] == name {
			return os.Args[i+1]
		}
	}
	return ""
}

func claudeHasFlag(name string) bool {
	for _, a := range os.Args {
		if a == name {
			return true
		}
	}
	return false
}

func claudeJournal(kind string, body map[string]any) {
	body = withKind(body, kind)
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(claudeJournalPath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(claudeJournalPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(raw, '\n'))
}

// claudeNativeSession answers the native session file of uuid for the
// process cwd under the step's CLAUDE_CONFIG_DIR (polyforge#171: the
// claude-config dir the spawner hard-set; projects/<dashed cwd>/<uuid>.
// jsonl) — exactly the path the runtime's locator then reads by.
func claudeNativeSession(uuid string) string {
	root := os.Getenv("CLAUDE_CONFIG_DIR")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".claude")
	}
	cwd, _ := os.Getwd()
	dashed := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, cwd)
	return filepath.Join(root, "projects", dashed, uuid+".jsonl")
}

// claudeEnvironFacts reads the RAW environ (polyforge#171 step-harness
// acceptance evidence): how many entries carry a given name, so the test
// can pin the zero-credential injection and the no-duplicate env
// contract (os.Getenv only sees the first value; ordinary reads cannot
// count).
func claudeEnvironFacts() (claudeConfigDirCount, anthropicAuthTokenCount, nonessentialOff int, claudeConfigDirVal string) {
	for _, kv := range os.Environ() {
		name, val, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch name {
		case "CLAUDE_CONFIG_DIR":
			claudeConfigDirCount++
			claudeConfigDirVal = val
		case "ANTHROPIC_AUTH_TOKEN":
			anthropicAuthTokenCount++
		case "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC":
			nonessentialOff = 1
		}
	}
	return
}

// claudeSettingsEnvFacts answers the env-block facts of the settings.json
// the step's CLAUDE_CONFIG_DIR names (what real claude applies from the
// authority layer polyforge#171 ruling 1): which of the pairing keys
// exist. Values are never journaled — set/unset only. An unreadable or
// malformed settings.json: unset facts (the fake never judges what it
// cannot read).
func claudeSettingsEnvFacts() (authTokenSet, baseUrlSet, cfgDirSet bool) {
	raw, err := os.ReadFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json"))
	if err != nil {
		return
	}
	var doc struct {
		Env map[string]any `json:"env"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.Env == nil {
		return
	}
	if doc.Env["ANTHROPIC_AUTH_TOKEN"] != nil {
		authTokenSet = true
	}
	if doc.Env["ANTHROPIC_BASE_URL"] != nil {
		baseUrlSet = true
	}
	if doc.Env["CLAUDE_CONFIG_DIR"] != nil {
		cfgDirSet = true
	}
	return
}

func emitClaude(rec map[string]any) {
	if _, ok := rec["parent_tool_use_id"]; !ok && rec["type"] != "result" && rec["type"] != "system" {
		rec["parent_tool_use_id"] = nil
	}
	emitRecord(rec)
}

// runClaude is the persona's main.
func runClaude() {
	watchTestBinary()
	resume := claudeArg("--resume")
	session := claudeArg("--session-id")
	if resume != "" {
		session = resume
	}
	prompt, _ := io.ReadAll(os.Stdin) // the whole prompt, then EOF
	wi := os.Getenv("PF_WI_ID")
	entry, ok := spawnJournalSelf(wi)
	settingsTokenSet, settingsBaseURLSet, settingsCfgDirSet := claudeSettingsEnvFacts()
	argv := append([]string(nil), os.Args[1:]...)
	clCfgDirCount, clAuthTokenCount, clNonessentialOff, clCfgDirVal := claudeEnvironFacts()
	claudeJournal("spawn", map[string]any{
		"pid":             os.Getpid(),
		"spawn_n":         entry.SpawnN,
		"step":            entry.Step,
		"argv":            argv,
		"session":         session,
		"resume":          resume != "",
		"model":           claudeArg("--model"),
		"auth_token_set":  os.Getenv("ANTHROPIC_AUTH_TOKEN") != "",
		"background_off":  os.Getenv("CLAUDE_CODE_DISABLE_BACKGROUND_TASKS") == "1",
		"autoupdater_off": os.Getenv("DISABLE_AUTOUPDATER") == "1",
		"startup_results": os.Getenv("CLAUDE_CODE_STARTUP_FAILURE_RESULTS") == "1",
		"pi_agent_dir":    os.Getenv("PI_CODING_AGENT_DIR"),
		// the Bash tool timeouts the adapter derived (raw values)
		"bash_default_timeout_ms": os.Getenv("BASH_DEFAULT_TIMEOUT_MS"),
		"bash_max_timeout_ms":     os.Getenv("BASH_MAX_TIMEOUT_MS"),
		// polyforge#171 acceptance evidence: raw-environ counts (no
		// duplicates, no injected token), the hard-set config dir value,
		// the NONESSENTIAL switch and what the settings.json authority
		// layer carries (set/unset only)
		"env_count_claude_config_dir": clCfgDirCount,
		"env_count_auth_token":        clAuthTokenCount,
		"claude_config_dir":           clCfgDirVal,
		"nonessential_off":            clNonessentialOff == 1,
		"settings_auth_token_set":     settingsTokenSet,
		"settings_base_url_set":       settingsBaseURLSet,
		"settings_cfg_dir_set":        settingsCfgDirSet,
		"prompt_bytes":                len(prompt),
		"prompt_has_handoff":          strings.Contains(string(prompt), "# Handoff from step"),
		"prompt_has_reference":        strings.Contains(string(prompt), "Previous attempt (read-only reference)"),
		"wi":                          wi,
	})
	if ok && entry.SpawnN > 0 {
		_ = os.WriteFile(filepath.Join("logs", "fakeclaude-prompt-"+strconv.Itoa(entry.SpawnN)+".txt"), prompt, 0o644)
	}
	if !ok || entry.SpawnN <= 0 || session == "" {
		fmt.Fprintln(os.Stderr, "fakeclaude: no spawn-journal identity or session id; exiting")
		os.Exit(2)
	}
	pl, path := loadPlan(wi)
	if pl == nil {
		fmt.Fprintf(os.Stderr, "fakeclaude: no plan file for %s (looked at %s)\n", wi, path)
		os.Exit(2)
	}
	if resume != "" && len(pl.ResumeActions) > 0 {
		pl.Actions = pl.ResumeActions
	}
	native := claudeNativeSession(session)
	d := &driveEnv{
		wi:         wi,
		handle:     entry.SessionHandle,
		spawnN:     entry.SpawnN,
		sessionArg: session,
		resumed:    resume != "",
		transcript: native, // the native session; the daemon copies it into the capsule
		plan:       pl,
	}
	d.appendTranscript(map[string]any{"type": "user", "sessionId": session, "spawn_n": d.spawnN, "at": nowStamp(), "message": map[string]any{"role": "user", "content": "(step prompt)"}})

	emitClaude(map[string]any{"type": "system", "subtype": "init", "session_id": session,
		"tools": []string{"Bash", "Read", "Edit", "Write", "Glob", "Grep", "TodoWrite", "Agent"},
		"model": claudeArg("--model"), "permissionMode": "dontAsk", "mcp_servers": []any{}})

	if bad := d.badModel(); bad != "" {
		// an unknown pinned model: the first request 404s
		claudeJournal("model_fail", map[string]any{"model": bad, "spawn_n": d.spawnN})
		claudeFail("model_not_found", 404, "", fmt.Sprintf("There's an issue with the selected model (%s). It may not exist or you may not have access to it.", bad))
	}
	act := d.currentAction()
	if act == nil {
		claudeAssistantText("fakeclaude: no envelope action for this spawn")
		claudeResultOK(d)
	}
	d.runClaudeAction(*act)
}

// runClaudeAction plays one envelope action as a Claude Code turn.
func (d *driveEnv) runClaudeAction(act planAction) {
	if act.Silent {
		// the zero-signal harness: init only, then nothing, never exits
		claudeJournal("silent", map[string]any{"step": act.ID, "spawn_n": d.spawnN})
		parkForever()
	}
	for i := 1; i <= act.ClaudeRetries; i++ {
		emitClaude(map[string]any{"type": "system", "subtype": "api_retry", "attempt": i, "max_retries": 10,
			"retry_delay_ms": 10, "error_status": act.ClaudeStatus, "error": act.ClaudeError})
		time.Sleep(10 * time.Millisecond)
	}
	if act.ClaudeError != "" || act.ClaudeTerminal != "" {
		claudeJournal("error_result", map[string]any{"step": act.ID, "spawn_n": d.spawnN, "error": act.ClaudeError, "status": act.ClaudeStatus})
		claudeFail(act.ClaudeError, act.ClaudeStatus, act.ClaudeTerminal, act.ClaudeMessage)
	}
	emitClaude(map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant"}}})
	emitClaude(map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "index": 0,
		"delta": map[string]any{"type": "text_delta", "text": "Working on " + act.ID}}})
	emitClaude(map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_stop"}})
	if act.EndTurn {
		// the turn ends normally WITHOUT the envelope: result + exit 0
		claudeJournal("end_turn", map[string]any{"step": act.ID, "spawn_n": d.spawnN})
		claudeAssistantText("I have finished my reply.")
		claudeResultOK(d)
	}
	runEvidence := d.runCommands(act.Run, act.NoStartedAt)
	if act.ToolMS > 0 || act.ToolHang {
		claudeToolUse("toolu_wait", "Bash", map[string]any{"command": "sleep"})
		if act.ToolHang {
			parkForever()
		}
		sleepMS(act.ToolMS)
		claudeToolResult("toolu_wait")
	}
	sleepMS(act.MS)
	for p, content := range act.Files {
		full := filepath.Join(d.capsuleRoot(), p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err == nil {
			_ = os.WriteFile(full, []byte(content), 0o644)
		}
	}
	if act.Crash == "before" {
		claudeJournal("crash_before", map[string]any{"step": act.ID, "spawn_n": d.spawnN})
		if act.CrashStderr != "" {
			fmt.Fprintln(os.Stderr, act.CrashStderr)
		}
		os.Exit(101)
	}
	drop := filepath.Join("runtime-state", "pending-results", strconv.Itoa(d.spawnN)+".json")
	claudeToolUse("toolu_env", "Write", map[string]any{"file_path": drop, "content": "{...}"})
	d.writeEnvelope(act, runEvidence)
	claudeToolResult("toolu_env")
	claudeJournal("envelope", map[string]any{"step": act.ID, "outcome": act.Outcome, "spawn_n": d.spawnN})
	if act.Crash == "after" {
		os.Exit(101)
	}
	claudeAssistantText("Result envelope written.")
	claudeResultOK(d)
}

// runOneShotSummarize is the daemon's one-shot summarize dispatch
// persona (polyforge#224): `pi -p --model <m>` / `claude -p <ctx> --model
// <m>` with the prompt on stdin — argv[1] == "-p" with NO step identity
// (no PF_WI_ID; a claude step spawn always carries --session-id/--resume)
// selects it. The real harness answers ONE plain line on stdout; this
// fake answers one line narrating the dispatch facts the acceptance
// suite pins — the model, the stdin prompt byte count (the prompt never
// rides argv), the context flag, its own pid — or fails RAW on stderr
// for a model whose name carries "fail", or delays a "slow" model 12 s
// before answering (an in-flight call). It never contacts a model, a
// URL or a credential.
func runOneShotSummarize() {
	watchTestBinary()
	prompt, _ := io.ReadAll(os.Stdin)
	model := claudeArg("--model")
	ctx := "none"
	if claudeHasFlag("--bare") {
		ctx = "--bare"
	} else if claudeHasFlag("--safe-mode") {
		ctx = "--safe-mode"
	}
	argvPrompt := "no-prompt"
	if len(prompt) >= 32 {
		head := string(prompt[:32])
		for _, a := range os.Args[1:] {
			if len(a) >= 32 && strings.Contains(a, head) {
				argvPrompt = "carries-prompt"
			}
		}
	}
	if strings.Contains(model, "fail") {
		fmt.Fprintf(os.Stderr, "w224 fake summarize dispatch failed: model=%s, %d prompt bytes, exit 1 by plan, pid=%d",
			model, len(prompt), os.Getpid())
		os.Exit(1)
	}
	if strings.Contains(model, "slow") {
		time.Sleep(12 * time.Second)
	}
	fmt.Printf("w224 fake summary: model=%s, %d prompt bytes on stdin, ctx=%s, argv=%s, pid=%d\n",
		model, len(prompt), ctx, argvPrompt, os.Getpid())
	os.Exit(0)
}

func claudeToolUse(id, name string, input map[string]any) {
	emitClaude(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant",
		"content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}}})
}

func claudeToolResult(id string) {
	emitClaude(map[string]any{"type": "user", "message": map[string]any{"role": "user",
		"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "ok"}}}})
}

func claudeAssistantText(text string) {
	emitClaude(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant",
		"content": []any{map[string]any{"type": "text", "text": text}}}})
}

// claudeResultOK closes a successful turn and exits 0 (claude -p does).
func claudeResultOK(d *driveEnv) {
	d.appendTranscript(map[string]any{"type": "assistant", "spawn_n": d.spawnN, "at": nowStamp(), "message": map[string]any{"role": "assistant", "content": "done"}})
	emitRecord(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": "done", "session_id": d.sessionArg})
	os.Exit(0)
}

// claudeFail emits an API-error assistant message and the error result,
// then exits 1 — the shape Claude Code ends a turn it gave up on.
func claudeFail(category string, status int, terminal, message string) {
	if message == "" {
		message = "API Error: " + category
	}
	if category != "" {
		emitClaude(map[string]any{"type": "assistant", "error": category, "message": map[string]any{"role": "assistant",
			"content": []any{map[string]any{"type": "text", "text": message}}}})
	}
	res := map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "result": message}
	if status > 0 {
		res["api_error_status"] = status
	}
	if terminal != "" {
		res["terminal_reason"] = terminal
	}
	emitRecord(res)
	os.Exit(1)
}
