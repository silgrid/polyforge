// drive.go: the fake agent's scripted executor (polyforge#106: the Go
// driver spawns one process per step and this executor performs the
// matching "envelope" action — under workflow contract 4 the step's wrap
// attrs and its one `pf step handoff`, writeEnvelope). The plan file is a
// JSON document describing what each derivation does on the capsule side —
// run commands, emit record bursts, hand off, park or die. Every action is
// journaled to logs/fakepi.jsonl inside the capsule, which the scenarios
// assert on.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// planAction is one scripted step of a derivation.
type planAction struct {
	Do       string `json:"do"`
	ID       string `json:"id"`       // step id / question id
	Question string `json:"question"` // ask: the question text
	MS       int    `json:"ms"`       // sleep / ui_noise: milliseconds
	Name     string `json:"name"`     // repo: the repos/ subdirectory
	Origin   string `json:"origin"`   // repo: the origin remote URL
	// Envelope mode (v2 Driver): the outcome the envelope action hands
	// off (writeEnvelope translates it into `pf step handoff`).
	Outcome string `json:"outcome,omitempty"` // envelope: completed|needs_input|gate_failed|incomplete|intent

	// --- envelope-mode extensions (polyforge#105, confined to fakepi) ---

	// QuestionID/Question are the needs_input question text, copied
	// onto the result envelope VERBATIM (the driver refuses a
	// needs_input result without question_id/question —
	// internal/polyforge/driver/transition.go). An EMPTY question_id is
	// scriptable on purpose: the empty-id refusal tests script the
	// malformed envelope exactly. The question TEXT rides the existing
	// Question field above (shared with the ask action).
	QuestionID string `json:"question_id,omitempty"`
	// Blocker is an incomplete's obstacle ({reason, evidence,
	// required_action}), copied onto the envelope's top level verbatim
	// (the driver reads it nowhere else).
	Blocker map[string]any `json:"blocker,omitempty"`
	// IntentFile names the capsule-relative file holding the owner's move
	// a hand-off citing the owner carries ({kind, reply_at, reason, to?}),
	// written by the Run commands (a reply_at a real agent reads off
	// runtime-state/asks.jsonl while running); writeEnvelope translates it
	// into the hand-off's status, --to and --per-owner.
	IntentFile string `json:"intent_file,omitempty"`
	// Park is the #113 wedge shape: open a turn, emit nothing further,
	// never write the envelope, never exit — the process sits alive at
	// zero activity while the driver's stepRun.Wait watches a drop path
	// that never changes (repro scaffolding for the step-wedge detection
	// lane; test-only, like all of fakepi).
	Park bool `json:"park,omitempty"` // park: silent live wedge, no envelope, no exit
	// Crash shapes on the envelope action: "before" dies BEFORE the
	// envelope write (the envelope never lands — the kill9 / hang
	// lanes), "after" dies right AFTER writing it (the seal-crash lane:
	// the written envelope must still be collected exactly once). Both
	// work on resume derivations too: the plan file is re-read per
	// process and resume_actions script crash + continuation in one
	// file, deterministically (the Nth envelope action fires for spawn
	// N across fresh and resume dispatches alike).
	Crash string `json:"crash,omitempty"` // "" | "before" | "after"
	// CrashStderr is written to stderr just before a crash:"before" exit
	// (a harness dying WITH a reason: a model-class text such as a
	// provider error makes the driver advance the candidate chain; the
	// bare crash stays an unclassified exit).
	CrashStderr string `json:"crash_stderr,omitempty"`
	// Output replaces the default output payload verbatim (populated
	// error outputs for gate_failed envelopes, schema-shaped payloads,
	// refusal probes).
	Output map[string]any `json:"output,omitempty"`
	// Files writes extra capsule-relative files (path → content) BEFORE
	// the envelope write — the declared-input shape: a later step's
	// declared `inputs:` entry must exist on disk at ITS dispatch, and
	// this is how a scripted step produces it (e.g. the bug-fix lane's
	// root-cause consuming artifacts/repro.md).
	Files map[string]string `json:"files,omitempty"`

	// Run executes shell commands (sh -c, capsule root as cwd, in order)
	// FIRST, before any other part of the action (park, crash, envelope) — the scripted stand-in for the commands a
	// real step agent runs (the deliver step's git/gh calls). Each one is
	// journaled with its exit code and output; a failing command does not
	// stop the rest.
	Run []string `json:"run,omitempty"`
	// World runs shell commands right after Run (sh -c, capsule root as
	// cwd) that stand for the world acting while the step runs — another
	// PR landing on origin — never the step's own: they are never recorded
	// as a tool call of the step.
	World []string `json:"world,omitempty"`
	// OutputFile, when set, replaces the output payload with the JSON
	// object in this capsule-relative file (written by the Run commands:
	// the values a real agent learns while running, e.g. a merge commit).
	OutputFile string `json:"output_file,omitempty"`
	// OpenTurn opens a stdout turn and leaves it open before the
	// envelope write (the F15 record-stream shapes on driver step
	// processes).
	OpenTurn bool `json:"open_turn,omitempty"`
	// Noise streams ui_noise records (kind ui | progress) while the
	// pre-write delay runs (F15 shapes); NoiseMS is the emitter interval
	// (MS keeps its meaning as the pre-write delay).
	Noise   string `json:"noise,omitempty"`    // "" | "ui" | "progress"
	NoiseMS int    `json:"noise_ms,omitempty"` // the noise emitter interval (default 100ms)

	// --- supervision shapes (v2-converge 2.3 incident replays) ---

	// Silent is the zero-signal harness (rule 0, #114): the dispatch
	// emits NO work record at all — not even the boot greeting turn — and
	// parks alive. Combined with Noise "ui" it is the F15 shape (UI
	// chatter only).
	Silent bool `json:"silent,omitempty"`
	// ErrorTurn is the F5 shape: the dispatch emits an assistant turn that
	// ENDS on this provider error (message_start/message_end/turn_end and
	// agent_end all carry stopReason=error), then parks alive without ever
	// writing the envelope.
	ErrorTurn string `json:"error_turn,omitempty"`
	// ErrorRetries prefixes the ErrorTurn with that many rounds of pi's
	// own auto-retry (agent-session.js: the error-ended turn closes with
	// agent_end willRetry=true, then auto_retry_start), ending with
	// auto_retry_end success=false after the final error turn — the
	// sequence a real pi emits for an error text it deems retryable
	// (429, overloaded, ...). The process still parks alive afterwards.
	ErrorRetries int `json:"error_retries,omitempty"`
	// EndTurn is the ok-ended twin of ErrorTurn (rule 1b): the dispatch
	// emits an assistant turn that ends NORMALLY, then parks alive without
	// ever writing the envelope.
	EndTurn bool `json:"end_turn,omitempty"`
	// ToolMS runs one tool execution of this length (tool_execution_start,
	// then tool_execution_end after the delay) before the envelope write:
	// the #83 long-tool shape. ToolHang never ends it (rule 3's shape).
	ToolMS   int    `json:"tool_ms,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
	ToolHang bool   `json:"tool_hang,omitempty"`
	// PoisonGitlink / UnpoisonGitlink run the #48 poison shape (lay or
	// clean an embedded git repository) before the envelope write; the
	// version bump inside arms the observe push round.
	PoisonGitlink   bool `json:"poison_gitlink,omitempty"`
	UnpoisonGitlink bool `json:"unpoison_gitlink,omitempty"`

	// --- claude persona shapes (polyforge#159, claude.go) ---

	// ClaudeError / ClaudeStatus / ClaudeTerminal / ClaudeMessage end the
	// dispatch on a structured Claude Code API failure: an assistant
	// message carrying the error category, then an error result
	// (api_error_status, terminal_reason), then exit 1.
	ClaudeError    string `json:"claude_error,omitempty"`
	ClaudeStatus   int    `json:"claude_status,omitempty"`
	ClaudeTerminal string `json:"claude_terminal,omitempty"`
	ClaudeMessage  string `json:"claude_message,omitempty"`
	// ClaudeRetries emits that many system/api_retry records first
	// (Claude's own bounded retry), with ClaudeStatus / ClaudeError.
	ClaudeRetries int `json:"claude_retries,omitempty"`

	// --- the owner-message relay (workflow contract v2 §5.2, PR 6d) ---

	// OnMessage is the turn this process answers a LATER owner message
	// with — a message the daemon relays into the resident process while
	// it idles after its scripted turn (pi: a later `prompt`; claude: a
	// later stream-json user message): an envelope action played in the
	// SAME dispatch, so its envelope carries this process's spawn ordinal.
	// Absent, a later message gets a plain acknowledgement turn. A message
	// that arrives mid-turn (queued by the harness) is delivered at the
	// turn's next boundary and answered by nothing (the turn goes on).
	OnMessage *planAction `json:"on_message,omitempty"`
}

// plan is the whole scripted behavior of one derivation.
type plan struct {
	Actions []planAction `json:"actions"`
	// ResumeActions, when set, replace Actions for a RESUME derivation
	// (the recovery engine re-derives with --session): a scenario scripts
	// the crash and the continuation in one file, deterministically —
	// no rewrite race against the recovery spawn.
	ResumeActions []planAction `json:"resume_actions"`
	// BadModels lists pinned model ids this harness cannot resolve (F4):
	// a dispatch spawned with `--model <one of them>` prints the error to
	// stderr and exits non-zero at startup.
	BadModels []string `json:"bad_models,omitempty"`
	// FailSpawnResume true makes every RESUME derivation die AT BIRTH:
	// the process exits before it reads one byte of stdin, so the
	// daemon's initial-prompt injection finds no reader, the pipe write
	// fails and Spawn itself returns an error. A derivation that accepts
	// the prompt and dies AFTERWARDS is a materially different lane — a
	// new agent_death recovery, which RESETS the budgets — so this mode
	// must never degrade into that one; see the pipe-capacity contract
	// below.
	FailSpawnResume bool `json:"fail_spawn_resume"`
	// FailSpawnFresh true makes every fresh RE-derivation die the same
	// way. "Re-derivation" is discriminated on the derivation counter the
	// runtime injects as PF_SPAWN_N (> 1 on every derivation after the
	// first): the ORIGINAL derivation carries counter 1 and is NEVER
	// affected by this flag. One plan file therefore scripts a healthy
	// first run AND a must-fail recovery ladder with no rewrite race
	// against the recovery spawn.
	//
	// PIPE-CAPACITY CONTRACT (both flags). "Dies before the prompt
	// lands" is not something this process can win by racing: the daemon
	// writes the prompt a few hundred microseconds after fork/exec,
	// while a Go binary spends about as long in runtime start-up
	// (measured on the ladder's own shape: a child that exits
	// immediately loses the race ~93% of the time against a ~27 KiB
	// prompt). The mode is deterministic only when the daemon's prompt
	// write CANNOT complete into the stdin pipe buffer — i.e. the
	// initial prompt exceeds the pipe capacity (64 KiB on Linux). Then
	// both orderings converge on the same answer: the parent blocks
	// mid-write and this process's exit breaks the pipe (EPIPE), or this
	// process is gone first and the very first write breaks it. A
	// scenario using these flags MUST size the daemon's executor prompt
	// accordingly (test/v1_s17_recovery_budget_test.go does, and asserts
	// it).
	FailSpawnFresh bool `json:"fail_spawn_fresh"`
}

// driveEnv is the shared state of one fake agent process.
type driveEnv struct {
	wi string
	// handle is the PUBLIC session fingerprint (PF_SESSION_HANDLE,
	// display only); spawnN is the v1.5 B2 derivation counter
	// (PF_SPAWN_N).
	handle     string
	spawnN     int
	sessionArg string
	resumed    bool

	mu         sync.Mutex // guards journal, transcript
	plan       *plan
	transcript string
	// mustFail: this derivation is scripted to die at birth, before it
	// reads the daemon's initial prompt (see plan.FailSpawnResume).
	mustFail bool
	// promptTurnDone: the scripted error_turn / end_turn was emitted as a
	// prompt's own turn (the stdin loop's goroutine only).
	promptTurnDone bool
	// promptsSeen counts the prompts this process answered (the stdin
	// loop's goroutine only): 1 is the dispatch's initial prompt, later
	// ones are messages into the resident session.
	promptsSeen int
	// steers are the messages queued while a run is active, delivered at
	// the next turn boundary (drainSteers; guarded by mu).
	steers []queuedMessage
	// msgs is the claude persona's later stdin messages (claude.go): read
	// by the idle message loop, or absorbed into a running turn after its
	// tool call (absorbQueued); absorbed holds the uuids whose `completed`
	// the turn's result is followed by.
	msgs     <-chan claudeUserMessage
	absorbed []string
}

// newDriveEnv assembles the executor from the spawn identity (the capsule
// spawn journal's entry for THIS process — pid-matched; the deleted
// PF_SESSION_HANDLE / PF_SPAWN_N env injections are re-sourced from there)
// + the plan file.
// A missing plan parks the agent loudly (the scenario fails on its own
// bounded wait — never a silent no-op).
func newDriveEnv() *driveEnv {
	wi := os.Getenv("PF_WI_ID")
	entry, ok := spawnJournalSelf(wi)
	spawnN := entry.SpawnN
	if !ok || spawnN <= 0 {
		fmt.Fprintln(os.Stderr, "fakepi: PF_WI_ID unset or the spawn journal carries no derivation for this process; parking")
		return nil
	}
	pl, path := loadPlan(wi)
	if pl == nil {
		fmt.Fprintf(os.Stderr, "fakepi: no plan file for %s (looked at %s); parking\n", wi, path)
		return nil
	}
	d := &driveEnv{
		wi:         wi,
		handle:     entry.SessionHandle,
		spawnN:     spawnN,
		sessionArg: transcriptPathOf(entry),
		resumed:    hasSessionArg(),
		transcript: transcriptPathOf(entry),
	}
	if d.resumed && len(pl.ResumeActions) > 0 {
		pl.Actions = pl.ResumeActions
	}
	d.mustFail = mustFailSpawn(pl, d.resumed, spawnN)
	d.plan = pl
	return d
}

// mustFailSpawn decides whether THIS derivation is the scripted
// must-fail-at-spawn kind: a resume derivation under FailSpawnResume, or
// a fresh RE-derivation (counter > 1) under FailSpawnFresh. The original
// derivation (counter 1) is never selected by either flag.
func mustFailSpawn(pl *plan, resumed bool, spawnN int) bool {
	if resumed {
		return pl.FailSpawnResume
	}
	return pl.FailSpawnFresh && spawnN > 1
}
func hasSessionArg() bool {
	for i, a := range os.Args {
		if a == "--session" && i+1 < len(os.Args) {
			return true
		}
	}
	return false
}

// loadPlan reads the plan file: FAKEPI_PLANS/<wi>.json, falling back to
// FAKEPI_PLANS/default.json. The file is re-read at every process start,
// so a scenario rewrites it to steer a re-derived agent.
func loadPlan(wi string) (*plan, string) {
	dir := os.Getenv("FAKEPI_PLANS")
	if dir == "" {
		return nil, ""
	}
	path := filepath.Join(dir, wi+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		path = filepath.Join(dir, "default.json")
		raw, err = os.ReadFile(path)
		if err != nil {
			return nil, path
		}
	}
	var pl plan
	if json.Unmarshal(raw, &pl) != nil {
		return nil, path
	}
	return &pl, path
}

// ---------------------------------------------------------------------------
// runDriveLoop executes the plan actions in order. The plan is re-read at
// every process start, so a re-derived agent follows whatever the scenario
// wrote most recently.
func runDriveLoop(d *driveEnv) {
	// Boot settle: a real harness spends its first moments loading
	// context before the first action; the pause also keeps an
	// immediately-flushing plan clear of the spawn-window races (the
	// monitor adopts the new handle on the RTRecovered attach, and the
	// runtime finishes its RECOVERING→RUNNING transition, both of which
	// lag a just-spawned process by microseconds).
	sleepMS(250)
	// polyforge#106: envelope mode IS the mode. The WI-Agent RPC lane
	// this loop used to interpret (the scripted step/ask/wrap/repo
	// action vocabulary of the deleted engine) is gone: each step spawn
	// executes ONLY the envelope action matching its PF_SPAWN_N
	// (the Driver spawns each step as a separate process; all share this
	// plan file). The Nth envelope action in the plan fires for spawn N —
	// across fresh AND resume dispatches alike (every re-dispatch takes
	// the next ordinal, so resume_actions script crash + continuation in
	// one file, deterministically). No matching envelope action for this
	// spawn: park.
	envIdx := 0
	for _, a := range d.plan.Actions {
		if a.Do == "envelope" {
			envIdx++
			if envIdx == int(d.spawnNumber()) {
				d.runEnvelopeAction(a)
				parkForever()
				return
			}
		}
	}
	parkForever()
}

func sleepMS(n int) {
	if n <= 0 {
		n = 50
	}
	time.Sleep(time.Duration(n) * time.Millisecond)
}

// runEnvelopeAction performs one envelope action in full: the optional
// pre-write side effects (turn/noise records, repo, poison shapes, the
// ms delay), the crash shapes, and the envelope write itself (the wrap
// attrs and the one `pf step handoff`, writeEnvelope).
func (d *driveEnv) runEnvelopeAction(act planAction) {
	// Scripted commands run first, whatever the action does afterwards
	// (park, crash, envelope) — except for a scripted turn (end_turn /
	// error_turn), whose commands the prompt handler ran inside that
	// turn (emitScriptedTurn).
	if act.ErrorTurn == "" && !act.EndTurn {
		d.runCommands(act.Run)
	}
	d.runWorld(act.World)
	if act.Silent {
		// The zero-signal harness: no record at all (UI noise optionally),
		// no envelope, no exit.
		d.journal("silent", map[string]any{"step": act.ID, "pid": os.Getpid(), "spawn_n": d.spawnN})
		if act.Noise != "" {
			d.startNoise(act.Noise, act.NoiseMS)
		}
		parkForever()
		return
	}
	if act.ErrorTurn != "" || act.EndTurn {
		// F5 / rule 1b: the turn IS the prompt's answer — the prompt
		// handler emits it (answerPrompt → emitScriptedTurn), then nothing.
		parkForever()
		return
	}
	if act.ToolMS > 0 || act.ToolHang {
		d.emitToolRun(act.ToolName, act.ToolMS, act.ToolHang)
	}
	if act.Park {
		// The silent-live wedge (polyforge#113's e2e-author shape): a
		// real harness whose provider call for the next turn never
		// returns. Open the turn (the in-flight silence the hang
		// watchdog discriminates on), then sit: no envelope write, no
		// exit, no further stdout record, zero activity — indefinitely.
		d.journal("park", map[string]any{
			"step": act.ID, "pid": os.Getpid(), "spawn_n": d.spawnN,
		})
		emitOpenTurn()
		parkForever()
		return
	}
	if act.OpenTurn {
		d.beginOpenTurn("open_turn")
	}
	if act.Noise != "" {
		d.startNoise(act.Noise, act.NoiseMS)
	}
	if act.Name != "" {
		d.makeRepo(act.Name, act.Origin)
	}
	if act.PoisonGitlink {
		// No execution.json write here: on the driver lane the DRIVER is
		// execution.json's single writer, and the envelope this action
		// precedes IS the version bump (the push lane's commit boundary).
		d.poisonGitlink()
	}
	if act.UnpoisonGitlink {
		d.unpoisonGitlink()
	}
	sleepMS(act.MS) // the delayed-envelope-write shape (the hang lane)
	// Extra capsule-relative files, verbatim content (the declared-input
	// producer: a downstream step's declared inputs must exist at its
	// dispatch).
	// The step's own report artifact, artifacts/<step>.md: written unless
	// a plan wrote it.
	if act.ID != "" {
		own := filepath.Join(d.capsuleRoot(), "artifacts", act.ID+".md")
		if _, err := os.Stat(own); os.IsNotExist(err) {
			if os.MkdirAll(filepath.Dir(own), 0o755) == nil {
				_ = os.WriteFile(own, []byte("# "+act.ID+"\n\nfakepi report\n"), 0o644)
			}
		}
	}
	for p, content := range act.Files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(d.capsuleRoot(), p)), 0o755); err == nil {
			if err := os.WriteFile(filepath.Join(d.capsuleRoot(), p), []byte(content), 0o644); err != nil {
				d.journal("file_error", map[string]any{"path": p, "err": err.Error()})
			} else {
				d.journal("file", map[string]any{"path": p, "bytes": len(content)})
			}
		} else {
			d.journal("file_error", map[string]any{"path": p, "err": err.Error()})
		}
	}
	if act.Crash == "before" {
		d.journal("crash_before", map[string]any{"step": act.ID, "outcome": act.Outcome})
		if act.CrashStderr != "" {
			fmt.Fprintln(os.Stderr, act.CrashStderr)
		}
		os.Exit(101) // die before the envelope write: the envelope never lands
	}
	if act.Crash == "after" {
		// The crash lands on cue, never pre-empted by the teardown: the
		// driver tears the process down (stdin closed, then SIGTERM to its
		// group) the moment its ledger poll finds the recorded hand-off,
		// which can come before this process is back from `pf step
		// handoff` to journal and die by itself (the v0.2.9 release
		// gate's red S17). Caught, the SIGTERM waits for the exit below.
		signal.Notify(make(chan os.Signal, 1), syscall.SIGTERM)
	}
	d.writeEnvelope(act)
	if act.Crash == "after" {
		d.journal("crash_after", map[string]any{"step": act.ID, "outcome": act.Outcome})
		os.Exit(101) // die after the write: the envelope is on the drop path
	}
	// The agent's run ends after its hand-off attempt, as a real agent's
	// does: the scripted commands opened a run (agent_start … turn_end,
	// runCommands) that only an agent_end closes. Under contract 3 the
	// collected envelope ended the dispatch whatever the stream said;
	// under contract 4 a refused hand-off leaves the dispatch to the
	// supervision, whose ended-without-hand-off rule keys on the turn's end.
	emitRecord(map[string]any{"type": "agent_end", "willRetry": false,
		"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{}}}})
}

// pinnedModelOf reads the --model argv the runtime pinned this derivation
// with ("" when the spawn carried no pin — the no-chain shape).
func pinnedModelOf() string {
	args := os.Args
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--model" {
			return args[i+1]
		}
	}
	return ""
}

func (d *driveEnv) beginOpenTurn(journalKind string) {
	d.journal(journalKind, map[string]any{"at": nowStamp()})
	emitOpenTurn()
}

// startNoise launches the F15 background record emitter: every interval
// milliseconds one record of the chosen family lands on stdout — kind
// ""/"ui" (default): the extension_ui_request shape a background-task
// extension's UI updates produce continuously (the pilot probe measured
// it dominating a stuck window's record stream — under the old progress
// semantics it kept the stream forever fresh); kind "progress": an
// ordinary message_update delta of the open turn — legal slow work
// (shape B: a turn streaming for a long time keeps proving life).
func (d *driveEnv) startNoise(kind string, intervalMS int) {
	if kind == "" {
		kind = "ui"
	}
	if intervalMS <= 0 {
		intervalMS = 100
	}
	d.journal("ui_noise", map[string]any{"kind": kind, "interval_ms": intervalMS})
	go func() {
		seq := 0
		for {
			if kind == "progress" {
				emitRecord(map[string]any{"type": "message_update",
					"assistantMessageEvent": map[string]any{
						"type": "text_delta", "contentIndex": 0, "delta": " still working",
					}})
			} else {
				emitRecord(map[string]any{"type": "extension_ui_request",
					"request": map[string]any{"method": "setStatus", "payload": "tick"},
					"seq":     seq})
			}
			seq++
			sleepMS(intervalMS)
		}
	}()
}

// badModel answers the pinned model this derivation was spawned with when
// the plan declares it unresolvable ("" otherwise).
func (d *driveEnv) badModel() string {
	m := pinnedModelOf()
	if m == "" {
		return ""
	}
	for _, b := range d.plan.BadModels {
		if b == m {
			return m
		}
	}
	return ""
}

// currentAction answers the envelope action this derivation executes
// (the Nth envelope action fires for spawn N); nil when none.
func (d *driveEnv) currentAction() *planAction {
	idx := 0
	for i := range d.plan.Actions {
		if d.plan.Actions[i].Do == "envelope" {
			idx++
			if idx == int(d.spawnNumber()) {
				return &d.plan.Actions[i]
			}
		}
	}
	return nil
}

// promptTurnAction answers this derivation's action when its scripted
// turn is the answer to the prompt (error_turn / end_turn); nil otherwise.
func (d *driveEnv) promptTurnAction() *planAction {
	a := d.currentAction()
	if a == nil || a.Silent || (a.ErrorTurn == "" && !a.EndTurn) {
		return nil
	}
	return a
}

// emitScriptedTurn emits an error_turn / end_turn action's one turn:
// F5, a turn that ends on a fatal provider error (after pi's own
// auto-retry rounds when scripted), or rule 1b, a turn that ends normally
// without a result. Nothing follows it.
func (d *driveEnv) emitScriptedTurn(act planAction) {
	if act.ErrorTurn != "" {
		d.journal("error_turn", map[string]any{"step": act.ID, "error": act.ErrorTurn, "spawn_n": d.spawnN})
		for i := 1; i <= act.ErrorRetries; i++ {
			emitErrorRound(act.ErrorTurn, true)
			emitRecord(map[string]any{"type": "auto_retry_start", "attempt": i, "maxAttempts": act.ErrorRetries,
				"delayMs": 10, "errorMessage": act.ErrorTurn})
		}
		emitAssistantTurn(d, "the provider connection dropped mid-reply", act.ErrorTurn)
		if act.ErrorRetries > 0 {
			emitRecord(map[string]any{"type": "auto_retry_end", "success": false, "attempt": act.ErrorRetries,
				"finalError": act.ErrorTurn})
		}
		return
	}
	// an end_turn action's scripted commands are the tool calls the turn
	// makes before it ends (`pf step ask "<question>"` then the turn's end is
	// the v2 ask shape, PR 6c); the drive loop skips them for this action
	d.runCommands(act.Run)
	d.journal("end_turn", map[string]any{"step": act.ID, "spawn_n": d.spawnN})
	emitAssistantTurn(d, "I have finished my reply.", "")
}

// silentThisSpawn reports whether this derivation is the zero-signal
// harness: it must not even answer the initial instruction.
func (d *driveEnv) silentThisSpawn() bool {
	a := d.currentAction()
	return a != nil && a.Silent
}

// emitToolRun plays one tool execution as the real stream does: a model
// reply that ends in a tool call, tool_execution_start, the run, and
// tool_execution_end (unless hanging).
func (d *driveEnv) emitToolRun(name string, ms int, hang bool) {
	if name == "" {
		name = "bash"
	}
	msg := map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{}}
	emitRecord(map[string]any{"type": "agent_start"})
	emitRecord(map[string]any{"type": "turn_start"})
	emitRecord(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "stopReason": "pending", "content": []any{}}})
	emitRecord(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "thinking_delta", "delta": "running the tool"}})
	emitRecord(map[string]any{"type": "message_end", "message": msg})
	emitRecord(map[string]any{"type": "tool_execution_start", "toolCallId": "call_1", "toolName": name,
		"args": map[string]any{"command": "fakepi " + name}})
	d.journal("tool_start", map[string]any{"name": name, "ms": ms, "hang": hang})
	if hang {
		parkForever()
		return
	}
	sleepMS(ms)
	emitRecord(map[string]any{"type": "tool_execution_end", "toolCallId": "call_1", "toolName": name, "result": map[string]any{}})
	emitRecord(map[string]any{"type": "turn_end", "message": msg, "toolResults": []any{}})
	d.journal("tool_end", map[string]any{"name": name})
	// the assistant turn's tool calls are done: a steering message queued
	// meanwhile is handed to the model here, before the next model call
	d.drainSteers()
}

// appendTranscript lands one JSONL line on the harness transcript — the
// recovery engine's resume discriminator reads this file (exists,
// non-empty, first line parses as a JSON record).
//
// polyforge#224 (owner ruling (a) chain, refined by the review verdict):
// a FRESH dispatch's line ALSO lands in the harness session store — a
// per-process file under $PI_CODING_AGENT_SESSION_DIR — mirroring the
// real pi, which writes its sessions flat into that dir when no
// --session resume path was passed (the store the daemon's
// live-transcript locator scans; the fake used to write ONLY the
// capsule session copy, so the store stayed empty and no live
// transcript could ever be located for a fake-driven dispatch). A
// RESUMED dispatch mirrors the real pi the other way: pi's
// SessionManager.open appends to the --session path (the capsule
// transcript) and never writes a store file — so the resumed fake
// appends to the capsule copy alone, and the store carries no file for
// it. The capsule session copy stays the resume path's source
// (unchanged); the store file is the FRESH dispatch's live transcript.
// A claude step's env carries no PI_CODING_AGENT_SESSION_DIR (only the
// claude-config dir), so the store write is a no-op there, exactly as
// for a real claude dispatch.
func (d *driveEnv) appendTranscript(rec map[string]any) {
	d.mu.Lock()
	path := d.transcript
	d.mu.Unlock()
	if path == "" {
		return
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(raw, '\n'))
	f.Close()
	// the session-store mirror (the FRESH dispatch's live transcript): a
	// resumed dispatch appends to the capsule file itself (the --session
	// path above) and never writes a store file, exactly as the real pi
	if d.resumed {
		return
	}
	if store := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); store != "" {
		live := filepath.Join(store, fmt.Sprintf("fakepi-%d.jsonl", os.Getpid()))
		if err := os.MkdirAll(store, 0o755); err != nil {
			return
		}
		lf, err := os.OpenFile(live, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		_, _ = lf.Write(append(raw, '\n'))
		lf.Close()
	}
}

// journal appends one record to the capsule's assertion ledger.
func (d *driveEnv) journal(kind string, body map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	body = withKind(body, kind)
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(journalPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(raw, '\n'))
}

func withKind(body map[string]any, kind string) map[string]any {
	if body == nil {
		body = map[string]any{}
	}
	out := make(map[string]any, len(body)+2)
	for k, v := range body {
		out[k] = v
	}
	out["type"] = kind
	out["at"] = nowStamp()
	return out
}

// poisonGitlink lays an embedded git repository (with its own commit —
// the shape `git add -A` stages as a mode-160000 gitlink) OUTSIDE the
// gitignored repos/ tree.
func (d *driveEnv) poisonGitlink() {
	dir := filepath.Join("poison")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "fakepi: poison: git %v: %v: %s\n", args, err, out)
			return err
		}
		return nil
	}
	ok := run("init", "-q", "-b", "main", ".") == nil &&
		os.WriteFile(filepath.Join(dir, "inner.txt"), []byte("embedded history for "+d.wi+"\n"), 0o644) == nil &&
		run("-c", "user.name=fake-agent", "-c", "user.email=fake-agent@invalid", "add", "-A") == nil &&
		run("-c", "user.name=fake-agent", "-c", "user.email=fake-agent@invalid", "commit", "-q", "-m", "embedded history") == nil
	d.journal("poison_gitlink", map[string]any{"path": "poison", "laid": ok})
}

// unpoisonGitlink performs the refusal message's documented cleanup —
// delete the embedded repository's .git: the formerly embedded files
// become plain untracked work.
func (d *driveEnv) unpoisonGitlink() {
	err := os.RemoveAll(filepath.Join("poison", ".git"))
	// the staged gitlink (if the index still carried one) goes with it
	d.journal("unpoison_gitlink", map[string]any{"path": "poison", "err": errString(err)})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// makeRepo lays a real git worktree under repos/<name> with an origin
// remote — a scenario's inventory repo (the daemon never pushes it;
// delivery is the workflows' deliver step, polyforge#166).
// When an origin carries a main baseline, the worktree descends from it
// (the real-agent shape: clone the repo, work on top) so the origin's
// history and the delivered branch share a root — github's PR API refuses
// to open a PR for a head with no history in common with the base. An
// origin without a main (s04's fresh empty bare repo) keeps today's
// fresh-init worktree.
func (d *driveEnv) makeRepo(name, origin string) {
	if name == "" {
		name = "demo"
	}
	dir := filepath.Join("repos", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "fakepi: repo %s: git %v: %v: %s\n", name, args, err, out)
			return err
		}
		return nil
	}
	if err := run("init", "-q", "-b", "main"); err != nil {
		return
	}
	if origin != "" {
		_ = run("remote", "add", "origin", origin)
		if run("fetch", "--quiet", "origin", "main") == nil {
			_ = run("checkout", "--quiet", "-B", "main", "FETCH_HEAD")
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "work.txt"), []byte("fake agent repo work for "+d.wi+"\n"), 0o644)
	_ = run("add", "-A")
	_ = run("-c", "user.name=fake-agent", "-c", "user.email=fake-agent@invalid",
		"commit", "-q", "-m", "repo work "+d.wi)
	d.journal("repo", map[string]any{"name": name, "origin": origin})
}

// writeEnvelope performs the action's result the way a workflow
// contract 4 step does: the wrap attrs it registers (`pf step hub
// wrap-attrs`), then one `pf step handoff` — the daemon checks and records
// it on the call ledger, which is the only result the driver reads. The
// action keeps its envelope-era script (the plan files' `outcome`,
// `intent_file`, `blocker`, `output`, `output_file`), translated:
//
//   - outcome completed (or none) → done, except that a gate whose output
//     carries `verdict: fail` hands off fail (the scripted agent routes on
//     the verdict its own check computed, as a real gate agent does; under
//     contract 3 the driver bound such an envelope to gate_failed);
//     gate_failed → fail (an intent
//     waive → waive, one_more_round → fail citing the owner); incomplete
//     → blocked (the blocker's reason as --reason); intent cancel →
//     cancel, rewind → rewind --to, retry → rewind --to the step itself,
//     each citing the intent's reply_at (--per-owner); needs_input (the
//     v1 question) is a status the command refuses — the scripted
//     refusal shape;
//   - an output's landing / integration_batch → `pf step hub wrap-attrs
//     integration` / `integration_batch`;
//   - the body carries every section the command checks for (the seven,
//     and 回应上一轮 for a re-run) in both agent languages, the output's
//     summary in its Conclusion and 结论.
//
// Every command is journaled with its exit code and output.
func (d *driveEnv) writeEnvelope(act planAction) {
	outcome := act.Outcome
	if outcome == "" {
		outcome = "completed"
	}
	stepID := act.ID
	output := act.Output
	if fromFile := d.scriptedFile(act.OutputFile, "output"); fromFile != nil {
		output = fromFile
	}
	for field, name := range map[string]string{"landing": "integration", "integration_batch": "integration_batch"} {
		if v, ok := output[field]; ok {
			raw, _ := json.Marshal(v)
			d.pfStep("hub_wrap_attrs", "hub", "wrap-attrs", name, string(raw))
		}
	}
	args := []string{"handoff"}
	reason := ""
	if outcome == "completed" && str(output, "verdict") == "fail" && d.gate(stepID) {
		outcome = "gate_failed"
	}
	switch outcome {
	case "completed":
		args = append(args, "--status", "done")
	case "gate_failed":
		in := d.scriptedFile(act.IntentFile, "intent")
		switch str(in, "kind") {
		case "waive":
			args = append(args, "--status", "waive", "--per-owner", str(in, "reply_at"))
		case "one_more_round":
			args = append(args, "--status", "fail", "--per-owner", str(in, "reply_at"))
		default:
			args = append(args, "--status", "fail")
		}
		reason = str(output, "fail_reason")
		if reason == "" {
			reason = "scripted gate failure (fakepi)"
		}
	case "incomplete":
		args = append(args, "--status", "blocked")
		reason = str(act.Blocker, "reason")
	case "intent":
		in := d.scriptedFile(act.IntentFile, "intent")
		switch str(in, "kind") {
		case "cancel":
			args = append(args, "--status", "cancel")
		case "retry":
			args = append(args, "--status", "rewind", "--to", stepID)
		default:
			args = append(args, "--status", "rewind", "--to", str(in, "to"))
		}
		args = append(args, "--per-owner", str(in, "reply_at"))
		reason = str(in, "reason")
	default:
		args = append(args, "--status", outcome)
	}
	if reason = oneLineText(reason); reason != "" {
		args = append(args, "--reason", reason)
	}
	summary := str(output, "summary")
	if summary == "" {
		summary = "envelope action"
	}
	// every heading in both agent languages, so the body passes the check
	// whichever language the dispatch was told
	body := "### Reply to the previous round\n\n(fakepi) every point answered in place.\n\n" +
		"### Conclusion\n\n" + summary + "\n\n" +
		"### The question this step answers\n\n(fakepi) step " + stepID + ".\n\n" +
		"### What was done\n\n(fakepi) the scripted action.\n\n" +
		"### Decisions and why\n\nnone\n\n" +
		"### Verification\n\nnone\n\n" +
		"### Problems and risks found\n\nnone\n\n" +
		"### For downstream, and what is open\n\nnone\n\n" +
		"### 回应上一轮\n\n(fakepi) every point answered in place.\n\n" +
		"### 结论\n\n" + summary + "\n\n" +
		"### 这一步要回答什么\n\n(fakepi) step " + stepID + ".\n\n" +
		"### 做了什么\n\n(fakepi) the scripted action.\n\n" +
		"### 关键决定和理由\n\nnone\n\n" +
		"### 验证\n\nnone\n\n" +
		"### 发现的问题和风险\n\nnone\n\n" +
		"### 给下游与未决\n\nnone\n"
	// the body rides stdin (`--body -`): the daemon may end this process
	// the moment it records the hand-off, so a temp file's cleanup could
	// never run
	args = append(args, "--body", "-")
	d.pfStepIn("handoff", body, args...)
	d.journal("envelope", map[string]any{"step": stepID, "outcome": outcome, "args": args, "crash": act.Crash})
}

// pfStep runs one `pf step <args>` from the capsule root and journals it
// (kind "step_<what>"). The binary is the harness's own pf (harnessPf):
// exec.Command resolves a bare name on this process's PATH when it is
// built, not on cmd.Env's, so a bare "pf" would run the machine's
// installed CLI (or none).
func (d *driveEnv) pfStep(what string, args ...string) {
	d.pfStepIn(what, "", args...)
}

// pfStepIn is pfStep with stdin (a `-` argument's content).
func (d *driveEnv) pfStepIn(what, stdin string, args ...string) {
	cmd := exec.Command(harnessPf(), append([]string{"step"}, args...)...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Dir = d.capsuleRoot()
	cmd.Env = harnessEnv() // a bare pf is the binary under test
	out, err := cmd.CombinedOutput()
	d.journal("step_"+what, map[string]any{"args": args, "exit_code": exitOf(err), "output": string(out)})
}

// str answers m[k] as a string ("" when absent or not one).
func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// oneLineText collapses s to one line (the hand-off's --reason is one).
func oneLineText(s string) string {
	out := []rune{}
	for _, r := range s {
		if r == '\n' || r == '\r' {
			r = ' '
		}
		out = append(out, r)
	}
	return string(out)
}

// scriptedFile answers the JSON object in the capsule-relative file an
// action names for field (written by its Run commands); nil when none is
// named, or when the file cannot be read (journaled <field>_file_error).
func (d *driveEnv) scriptedFile(file, field string) map[string]any {
	if file == "" {
		return nil
	}
	var fromFile map[string]any
	raw, err := os.ReadFile(filepath.Join(d.capsuleRoot(), file))
	if err == nil {
		err = json.Unmarshal(raw, &fromFile)
	}
	if err != nil {
		d.journal(field+"_file_error", map[string]any{"path": file, "err": err.Error()})
		return nil
	}
	return fromFile
}

// runCommands executes an action's Run commands (see planAction.Run).
func (d *driveEnv) runCommands(cmds []string) {
	for i, c := range cmds {
		// polyforge#224 (the fixture-fidelity fix, the owner ruling (a)
		// chain): each scripted command is a TOOL EXECUTION in the record
		// stream — the real agent's bash tool emits
		// tool_execution_start/end around every command it runs, and the
		// supervision's ended_without_result grace (60 s after the last
		// turn end) would otherwise kill a dispatch whose scripted command
		// merely takes longer than that (the w224 growth lever sleeps 75 s
		// past a 60 s interval): the tool window (30 m) governs a running
		// command, exactly as for a real agent. The records mirror
		// emitToolRun (records only — the transcript is untouched: the
		// fake's transcript grows only from its own turns, so a scenario's
		// growth lever stays the lever's own writer).
		callID := fmt.Sprintf("call_run_%d", i+1)
		emitRecord(map[string]any{"type": "agent_start"})
		emitRecord(map[string]any{"type": "turn_start"})
		emitRecord(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "stopReason": "pending", "content": []any{}}})
		emitRecord(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "thinking_delta", "delta": "running the tool"}})
		emitRecord(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{}}})
		emitRecord(map[string]any{"type": "tool_execution_start", "toolCallId": callID, "toolName": "bash",
			"args": map[string]any{"command": c}})
		cmd := exec.Command("sh", "-c", c)
		cmd.Dir = d.capsuleRoot()
		cmd.Env = harnessEnv() // a bare pf is the binary under test
		out, err := cmd.CombinedOutput()
		emitRecord(map[string]any{"type": "tool_execution_end", "toolCallId": callID, "toolName": "bash", "result": map[string]any{}})
		emitRecord(map[string]any{"type": "turn_end", "message": map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{}}, "toolResults": []any{}})
		d.journal("run", map[string]any{"cmd": c, "exit_code": exitOf(err), "output": string(out)})
	}
}

// capsuleRoot answers the capsule directory: the fakepi runs with the
// capsule as its cwd.
func (d *driveEnv) capsuleRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func parseIntDefault(s string, def int64) int64 {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	return def
}

// spawnNumber answers this derivation's counter (the spawn-journal
// identity; 1 when unset).
func (d *driveEnv) spawnNumber() int64 {
	if d.spawnN > 0 {
		return int64(d.spawnN)
	}
	return 1
}

// runWorld runs an action's World commands (planAction.World): unrecorded.
func (d *driveEnv) runWorld(cmds []string) {
	for _, c := range cmds {
		cmd := exec.Command("sh", "-c", c)
		cmd.Dir = d.capsuleRoot()
		cmd.Env = harnessEnv() // a bare pf is the binary under test
		out, err := cmd.CombinedOutput()
		d.journal("world", map[string]any{"cmd": c, "exit_code": exitOf(err), "output": string(out)})
	}
}
