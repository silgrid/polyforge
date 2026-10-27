// Command fakepi is the v1 e2e stand-in for the real `pi --mode rpc`
// harness binary — envelope edition (polyforge#106: the WI-Agent RPC
// lane is deleted; the Go driver spawns one process per step). The
// daemon derives it as the step process (--pi-bin); it speaks the REAL
// v1 envelope pinned by internal/polyforge/runtime/spawn.go and the
// record shapes frozen in internal/polyforge/observe/stream.go:
//
//   - stdin: one JSON command object per line; prompt commands
//     {"id":..,"type":"prompt","message":..} are the only commands
//     honored;
//   - stdout: response records {"id","type":"response","command":"prompt",
//     "success":true} echo the command id; assistant text rides the
//     message_start/message_update/message_end event burst; tool
//     executions ride tool_execution_start/tool_execution_end;
//   - capsule-side execution state: runtime-state/execution.json is
//     written atomically (tmp+rename) with a MONOTONIC Version per
//     write — the v1 zero-blocking agent contract (v1 ruling C3 (archive/v1-architecture-spec.md)) the
//     observe lane polls;
//   - a harness transcript is appended at the session path the driver's
//     derivation declares (--session on a resume, the token-named
//     capsule copy session/session-<handle>[#N].jsonl otherwise), so
//     crash-recovery scenarios exercise the real resume path.
//
// Scripted behavior: the process reads a JSON plan file at startup
// (FAKEPI_PLANS/<PF_WI_ID>.json, falling back to
// FAKEPI_PLANS/default.json). The plan is re-read on every process
// start, so a scenario rewrites the file to change what a RE-DERIVED
// agent does. The Nth envelope action fires for spawn N (see drive.go).
//
// Besides the action list the plan carries per-derivation-kind modes.
// fail_spawn_resume / fail_spawn_fresh are the must-fail-AT-SPAWN mode:
// the selected derivation exits before reading one byte of stdin, so the
// daemon's initial-prompt injection breaks the pipe and Spawn returns an
// error — the spawn-failure ladder, as opposed to the observe lane's
// agent-death lane that every other death here produces. The original
// derivation is never selected (the flags key on `--session` and on
// the derivation counter PF_SPAWN_N: a fresh RE-derivation carries a
// counter > 1, the original carries 1). drive.go states the mode's
// pipe-capacity contract; it is not optional.
//
// Known deviation from the real harness (inherited from the v0 double):
// stdin EOF does not exit — the process parks alive, which the
// daemon-crash scenarios need (the WI agent must survive its parent's
// death). There is no model behind a turn: every "run" is a canned
// record burst. Because it never exits on its own, it watches the test
// binary instead: with FAKEPI_TEST_PID set (the acceptance TestMain sets
// it), the process exits once that pid is gone — an agent orphaned by a
// killed or timed-out test run (no test cleanup runs then) does not
// linger for hours.
//
// Test-only helper; nothing in production imports or references it.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// journalPath is the fake agent's own assertion ledger inside the
// capsule (logs/ is a committed sync surface, so the journal rides the
// capsule git chain and is assertable from the sealed scene too).
const journalPath = "logs/fakepi.jsonl"

// outMu serializes stdout record emission between the stdin command loop and
// the drive goroutine.
var outMu sync.Mutex

// drv is the drive-mode state (nil never happens here: the v1 fake is
// always a scripted executor).
var drv *driveEnv

func main() {
	// polyforge#159: the same binary is the fake `claude` (claude.go)
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(fakeClaudeVersion + " (Claude Code)")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "-p" {
		// polyforge#224: a -p process with NO step identity (no PF_WI_ID;
		// a claude step spawn always carries --session-id/--resume) is the
		// daemon's one-shot summarize dispatch — pi -p --model <m> /
		// claude -p <ctx> --model <m>, the prompt on stdin.
		if os.Getenv("PF_WI_ID") == "" && claudeArg("--session-id") == "" && claudeArg("--resume") == "" {
			runOneShotSummarize()
			return
		}
		runClaude()
		return
	}
	watchTestBinary()
	drv = newDriveEnv()
	if drv == nil {
		parkForever()
	}
	if drv.mustFail {
		// The must-fail-at-spawn mode (plan.FailSpawnResume /
		// plan.FailSpawnFresh): die at birth. Nothing of this derivation
		// may be observable as a LIVE agent — no agent_settled record, no
		// transcript line, no stdin read — because the daemon's Spawn must
		// fail at prompt injection rather than hand back a working handle.
		// The journal line is the one exception and it is safe: the parent
		// already wrote this derivation's spawn-journal entry BEFORE the
		// prompt, so the failed derivation is on the record either way,
		// and a scenario needs to see that the mode really engaged.
		drv.journal("spawn_fail", map[string]any{
			"pid":     os.Getpid(),
			"handle":  drv.handle,
			"spawn_n": drv.spawnN,
			"resume":  drv.resumed,
			"wi":      drv.wi,
			"mode":    "must_fail_at_spawn",
		})
		os.Exit(9)
	}
	if bad := drv.badModel(); bad != "" {
		// the model-pin failure (F4): the harness cannot resolve the
		// pinned model and exits non-zero at startup with the error on
		// stderr — an exit WITHOUT a result envelope, model-class.
		drv.journal("model_fail", map[string]any{"model": bad, "spawn_n": drv.spawnN, "pid": os.Getpid()})
		fmt.Fprintf(os.Stderr, "error: model %q not found (unknown model); available models: none\n", bad)
		os.Exit(1)
	}
	drv.journal("spawn", map[string]any{
		"pid":     os.Getpid(),
		"handle":  drv.handle,
		"spawn_n": drv.spawnN,
		"resume":  drv.resumed,
		"session": drv.sessionArg,
		"model":   pinnedModelOf(),
		"wi":      drv.wi,
		// v4 acceptance ledger: the derivation's
		// full argv and the agent-directory override the launcher passed, so
		// the e2e suite can pin the step agent's launch configuration
		// (PI_CODING_AGENT_DIR present, --no-extensions absent) through the
		// capsule's committed logs surface, without engine internals.
		"argv":      os.Args,
		"agent_dir": os.Getenv("PI_CODING_AGENT_DIR"),
	})
	go runDriveLoop(drv)

	// RPC startup: the real harness emits agent_settled for the fresh
	// session before any prompt arrives.
	emitRecord(map[string]any{"type": "agent_settled"})
	drv.appendTranscript(map[string]any{"t": "spawn", "at": nowStamp(), "handle": drv.handle, "spawn_n": drv.spawnN, "resume": drv.resumed})

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		var cmd struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &cmd) != nil || cmd.Type != "prompt" {
			continue
		}
		emitRecord(map[string]any{
			"id": cmd.ID, "type": "response", "command": "prompt", "success": true,
		})
		drv.journal("prompt", map[string]any{
			"spawn_n": drv.spawnN,
			// workbench#7: the EWR feedback is the phrase shared by the
			// full-prompt notice and the first same-session retry's short
			// reminder (spawn.go). The old "envelope file is written"
			// wording is gone; keep this in step with spawn.go.
			"ended_without_result_hint": strings.Contains(cmd.Message, "ended without submitting a result"),
			// polyforge#175: a fresh dispatch after a candidate switch
			// (possibly from another harness) carries the previous
			// transcript as read-only reference
			"prompt_has_reference": strings.Contains(cmd.Message, "Previous attempt (read-only reference)"),
		})
		// the full prompt lands beside the journal (polyforge#21: the
		// retry-shape scenarios assert the dispatched prompt; the claude
		// persona already persists fakeclaude-prompt-<N>.txt, this is the
		// pi twin). Best effort: a write failure never affects the run.
		_ = os.WriteFile(filepath.Join("logs", "fakepi-prompt-"+strconv.Itoa(drv.spawnN)+".txt"), []byte(cmd.Message), 0o644)
		// the initial instruction: a plain assistant turn — unless this
		// dispatch's action is the zero-signal harness (Silent: not even
		// the greeting turn; rule 0's shape)
		if !drv.silentThisSpawn() {
			emitAssistantTurn(drv, "fakepi: instruction received; standing by.", "")
		}
	}
	// stdin EOF (the daemon died): park alive — the crash drills need
	// the WI agent to survive the parent's kill.
	parkForever()
}

// emitAssistantTurn streams one complete assistant message as the real
// event sequence. stopReason != "" marks a provider-error turn: the
// failure rides the ordinary records (message_start, message_end AND
// turn_end all carry stopReason=error + errorMessage — there is no
// type:"error" record).
func emitAssistantTurn(d *driveEnv, text, errMsg string) {
	message := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}
	if errMsg != "" {
		message["stopReason"] = "error"
		message["errorMessage"] = errMsg
	}
	usage := map[string]any{
		"input": 0, "output": len(text), "cacheRead": 0, "cacheWrite": 0,
		"totalTokens": len(text),
		"cost":        map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0},
	}
	firstDelta := text
	if len(firstDelta) > 16 { // two deltas: the ack line may straddle chunks
		firstDelta = text[:16]
	}
	emitRecord(map[string]any{"type": "agent_start"})
	emitRecord(map[string]any{"type": "turn_start"})
	emitRecord(map[string]any{"type": "message_start", "message": message})
	emitRecord(map[string]any{"type": "message_update", "usage": usage,
		"assistantMessageEvent": map[string]any{"type": "text_start", "contentIndex": 0}})
	emitRecord(map[string]any{"type": "message_update", "usage": usage,
		"assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": firstDelta}})
	if firstDelta != text {
		emitRecord(map[string]any{"type": "message_update", "usage": usage,
			"assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": text[len(firstDelta):]}})
	}
	emitRecord(map[string]any{"type": "message_update", "usage": usage,
		"assistantMessageEvent": map[string]any{"type": "text_end", "contentIndex": 0, "content": text}})
	emitRecord(map[string]any{"type": "message_end", "message": message})
	emitRecord(map[string]any{"type": "turn_end", "message": message, "toolResults": []any{}})
	emitRecord(map[string]any{"type": "agent_end", "messages": []any{message}, "willRetry": false})
	emitRecord(map[string]any{"type": "agent_settled"})
	if d != nil {
		d.appendTranscript(map[string]any{"t": "turn", "at": nowStamp(), "text": text, "stop_reason": stopOf(errMsg)})
	}
}

// emitErrorRound streams one error-ended assistant turn the way pi does
// when the provider call fails before any content (no deltas), closing
// with agent_end carrying willRetry (true while pi's own auto-retry
// still has attempts left).
func emitErrorRound(errMsg string, willRetry bool) {
	message := map[string]any{"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": errMsg}
	emitRecord(map[string]any{"type": "agent_start"})
	emitRecord(map[string]any{"type": "turn_start"})
	emitRecord(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}})
	emitRecord(map[string]any{"type": "message_end", "message": message})
	emitRecord(map[string]any{"type": "turn_end", "message": message, "toolResults": []any{}})
	emitRecord(map[string]any{"type": "agent_end", "messages": []any{message}, "willRetry": willRetry})
}

// emitOpenTurn opens a turn and never closes it: the in-flight silence
// the hang watchdog discriminates on.
func emitOpenTurn() {
	emitRecord(map[string]any{"type": "agent_start"})
	emitRecord(map[string]any{"type": "turn_start"})
	emitRecord(map[string]any{"type": "message_start", "message": map[string]any{
		"role": "assistant", "stopReason": "pending", "content": []any{},
	}})
}

func stopOf(errMsg string) string {
	if errMsg != "" {
		return "error"
	}
	return "stop"
}

// emitRecord writes ONE protocol record to stdout (JSON + LF).
func emitRecord(rec map[string]any) {
	raw, err := json.Marshal(rec)
	if err != nil {
		return
	}
	outMu.Lock()
	defer outMu.Unlock()
	fmt.Println(string(raw))
}

// watchTestBinary exits the process once the test binary named by
// FAKEPI_TEST_PID is gone (see the package doc). No-op without it.
func watchTestBinary() {
	pid, err := strconv.Atoi(os.Getenv("FAKEPI_TEST_PID"))
	if err != nil || pid <= 1 {
		return
	}
	go func() {
		for {
			if syscall.Kill(pid, 0) == syscall.ESRCH {
				os.Exit(97)
			}
			time.Sleep(time.Second)
		}
	}()
}

func parkForever() {
	for {
		time.Sleep(time.Hour)
	}
}

func nowStamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// capsule spawn journal (runtime-state/spawn-journal.jsonl) is the loose
// identity source for the derivation: the entry the parent wrote for THIS
// process (pid-matched, appended right after fork — before the initial
// prompt) carries the session handle, the derivation counter and the
// transcript path. The deleted PF_SESSION_HANDLE / PF_SPAWN_N env
// injections are re-sourced from here.
type spawnJournalEntry struct {
	PID           int    `json:"pid"`
	Step          string `json:"step_id"`
	WorkItem      string `json:"work_item"`
	SessionHandle string `json:"session_handle"`
	SpawnN        int    `json:"spawn_n"`
	Transcript    string `json:"transcript"`
}

// spawnJournalSelf answers this process's spawn-journal entry: the last
// line of the capsule journal whose pid is ours. The parent appends the
// line right after fork, so a freshly started child can beat the append
// by microseconds — bounded wait, then the last entry stands (the
// derivation lanes are serialized per WI).
func spawnJournalSelf(wi string) (spawnJournalEntry, bool) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		e, ok := lastSpawnEntry(wi)
		if ok && (e.PID == os.Getpid() || time.Now().After(deadline)) {
			return e, true
		}
		if time.Now().After(deadline) {
			return e, ok
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// lastSpawnEntry reads the newest journal line (loose: unparsable lines
// are skipped; a missing journal is not an error — the caller parks).
func lastSpawnEntry(wi string) (spawnJournalEntry, bool) {
	path := filepath.Join("runtime-state", "spawn-journal.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		return spawnJournalEntry{}, false
	}
	var last spawnJournalEntry
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e spawnJournalEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if wi != "" && e.WorkItem != "" && e.WorkItem != wi {
			continue
		}
		last, found = e, true
	}
	return last, found
}

// transcriptPathOf resolves this process's transcript file: the --session
// argv on a resume derivation, else the transcript path the runtime
// declared for this derivation in the spawn journal.
func transcriptPathOf(entry spawnJournalEntry) string {
	args := os.Args
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--session" {
			return args[i+1]
		}
	}
	return entry.Transcript
}
