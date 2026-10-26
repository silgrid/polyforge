package test

// w174 (polyforge#174): the harness adapters run `pf daemon ensure` at
// the start of a HUMAN's session — and never in a step agent.
//
//   - pi: skills/polyforge/extensions/pf-chat.ts (the REAL file, copied
//     verbatim) through a mock pi ExtensionAPI, the w172 harness shape: a
//     failed first `pf chat init` runs ensure once, then init succeeds;
//     a failed ensure falls back to the RESTART_MS retry loop without
//     re-running ensure; with PF_WI_ID (a step agent) nothing registers.
//   - Claude Code: skills/polyforge/hooks/claude-session-start.sh (the
//     REAL script): ensure first, then init; a failing or hanging ensure
//     is silent and bounded; PF_WI_ID makes it a no-op.
//
// A fake pf on PATH plays the daemon (state file "down"/"up") and
// journals every call; nothing real is started.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const w174FakePf = `#!/bin/sh
# fake pf for w174: daemon state in $PF_STATE ("down"/"up"); every call
# journaled; "daemon ensure" behaves per $PF_ENSURE (up|fail|hang)
echo "$*" >> "$PF_JOURNAL"
state=$(cat "$PF_STATE" 2>/dev/null || echo down)
case "$1" in
  daemon)
    [ "$2" = ensure ] || exit 2
    case "${PF_ENSURE:-up}" in
      up) echo up > "$PF_STATE"; echo "daemon m-x answers"; exit 0 ;;
      hang) exec sleep 60 ;;
      *) echo "pf daemon ensure: failed" >&2; exit 1 ;;
    esac
    ;;
  chat)
    if [ "$state" = "up" ]; then echo "chat_w174ensure0001"; exit 0; fi
    echo "pf: daemon down (connection refused)" >&2
    exit 1
    ;;
  watch)
    if [ "$2" = "--chat" ] && [ "$4" = "--statusline" ]; then
      [ "$state" = "up" ] && { printf 'WI polyforge#174 step x: running\n'; exit 0; }
      exit 1
    fi
    if [ "$state" = "up" ]; then while :; do sleep 1; done; fi
    exit 1
    ;;
esac
exit 1
`

const w174PiHarness = `
import { spawn } from "node:child_process";
import { writeFileSync, readFileSync } from "node:fs";
import { join } from "node:path";

const work = process.env.W174_WORK;
const stateFile = join(work, "daemon-state");
const journalFile = join(work, "pf-journal.log");
writeFileSync(stateFile, "down");
writeFileSync(journalFile, "");
const env = { ...process.env, PATH: join(work, "bin") + ":" + process.env.PATH, PF_STATE: stateFile, PF_JOURNAL: journalFile };
Object.assign(process.env, { PATH: env.PATH, PF_STATE: stateFile, PF_JOURNAL: journalFile });
const stepAgent = process.env.W174_SCENARIO === "step-agent";
if (stepAgent) { env.PF_WI_ID = "wi_step"; process.env.PF_WI_ID = "wi_step"; }
else { delete env.PF_WI_ID; delete process.env.PF_WI_ID; }

const handlers = {};
const ui = { widget: undefined, setWidget(_k, c) { this.widget = c; } };
// inflight counts pi.exec children not yet exited: the extension's code
// after an awaited exec runs in the microtasks of that exit, so a
// macrotask that sees 0 sees every follow-up exec already spawned.
let inflight = 0;
const pi = {
  on: (ev, fn) => { handlers[ev] = fn; },
  exec: (cmd, args, opts) => new Promise((resolve) => {
    inflight++;
    let done = false;
    const settle = (r) => { if (!done) { done = true; inflight--; resolve(r); } };
    const child = spawn(cmd, args, { env, stdio: ["ignore", "pipe", "pipe"] });
    let out = "", err = "", timer;
    if (opts && opts.timeout) timer = setTimeout(() => child.kill("SIGKILL"), opts.timeout);
    child.stdout.on("data", (d) => (out += d));
    child.stderr.on("data", (d) => (err += d));
    child.on("exit", (code) => { clearTimeout(timer); settle({ code: code ?? -1, stdout: out, stderr: err }); });
    child.on("error", (e) => settle({ code: -1, stdout: "", stderr: String(e) }));
  }),
  sendMessage: () => {},
};
const ctx = { sessionManager: { getSessionId: () => "sess_w174" }, ui };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const mod = await import("./pf-chat.ts");
await mod.default(pi);

const lines = () => readFileSync(journalFile, "utf8").split("\n").filter((l) => l.trim() !== "");
const verdict = { handlers: Object.keys(handlers).sort(), sessionStartMs: 0, at1500: {}, final: {}, bash: "" };
const snap = () => {
  const l = lines();
  return {
    inits: l.filter((x) => x.startsWith("chat init")).length,
    ensures: l.filter((x) => x.startsWith("daemon ensure")).length,
    firstEnsureBeforeSecondInit: (() => {
      const e = l.findIndex((x) => x.startsWith("daemon ensure"));
      const inits = l.map((x, i) => [x, i]).filter(([x]) => x.startsWith("chat init")).map(([, i]) => i);
      return e >= 0 && inits.length >= 2 && inits[0] < e && e < inits[1];
    })(),
    widget: ui.widget !== undefined,
  };
};
if (!stepAgent) {
  const t0 = Date.now();
  await handlers["session_start"]({}, ctx);
  verdict.sessionStartMs = Date.now() - t0;
  await sleep(1500);
  verdict.at1500 = snap();
  if (process.env.W174_SCENARIO === "ensure-fails") {
    // the failed ensure hands over to the RESTART_MS (5s) retry loop: wait
    // (at most 12s more; the third init lands about 10s in, so the cap
    // leaves room under load) until its second retry — the third init —
    // has run to its end with nothing left in flight, so a retry that
    // re-ran ensure, on the first retry or a later one, has spawned it
    const by = Date.now() + 12000;
    while (Date.now() < by && !(snap().inits >= 3 && inflight === 0)) await sleep(100);
  } else {
    await sleep(2500);
  }
  const ev = { toolName: "bash", input: { command: "echo hi" } };
  await handlers["tool_call"](ev, {});
  verdict.bash = ev.input.command;
  verdict.final = snap();
  await handlers["session_shutdown"]({}, ctx);
} else {
  await sleep(500);
  verdict.final = snap();
}
console.log(JSON.stringify(verdict));
process.exit(0);
`

type w174PiVerdict struct {
	Handlers       []string `json:"handlers"`
	SessionStartMs int      `json:"sessionStartMs"`
	At1500         w174Snap `json:"at1500"`
	Final          w174Snap `json:"final"`
	Bash           string   `json:"bash"`
}

type w174Snap struct {
	Inits                       int  `json:"inits"`
	Ensures                     int  `json:"ensures"`
	FirstEnsureBeforeSecondInit bool `json:"firstEnsureBeforeSecondInit"`
	Widget                      bool `json:"widget"`
}

// w174RunPiHarness drives the real pf-chat.ts through one scenario.
func w174RunPiHarness(t *testing.T, scenario, ensureMode string) w174PiVerdict {
	t.Helper()
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not found: %v", err)
	}
	if major := w172NodeMajor(t, nodeBin); major < 23 {
		t.Skipf("node %d lacks unflagged type stripping (need >= 23.6)", major)
	}
	piPkg := filepath.Join(w172GlobalNodeModules(t), "@earendil-works", "pi-coding-agent")
	extSrc, err := os.ReadFile(w172ExtSrc)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	for p, body := range map[string]string{"pf-chat.ts": string(extSrc), "harness.mjs": w174PiHarness} {
		if err := os.WriteFile(filepath.Join(work, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ea := filepath.Join(work, "node_modules", "@earendil-works")
	if err := os.MkdirAll(ea, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(piPkg, filepath.Join(ea, "pi-coding-agent")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "bin", "pf"), []byte(w174FakePf), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodeBin, "harness.mjs")
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "W174_WORK="+work, "W174_SCENARIO="+scenario, "PF_ENSURE="+ensureMode)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	var v w174PiVerdict
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &v); err != nil {
		t.Fatalf("harness output not JSON (exit %v): %v\nstdout %s\nstderr %s", runErr, err, out.String(), errb.String())
	}
	return v
}

func TestW174PfChatEnsureThenInit(t *testing.T) {
	v := w174RunPiHarness(t, "ensure-heals", "up")
	if v.SessionStartMs > 1000 {
		t.Errorf("session_start blocked %dms: ensure must run in the background", v.SessionStartMs)
	}
	if v.At1500.Ensures != 1 || v.At1500.Inits != 2 || !v.At1500.FirstEnsureBeforeSecondInit {
		t.Fatalf("daemon down at session_start: want init (fail) -> one ensure -> init within 1.5s, got %+v", v.At1500)
	}
	if v.Final.Ensures != 1 || v.Final.Inits != 2 || !v.Final.Widget || !strings.HasPrefix(v.Bash, "PF_CHAT_ID=chat_w174ensure0001 ") {
		t.Fatalf("after ensure the session must be wired (one ensure, init ok, widget, PF_CHAT_ID): %+v bash %q", v.Final, v.Bash)
	}
}

func TestW174PfChatEnsureFailureFallsBackToRetryLoop(t *testing.T) {
	v := w174RunPiHarness(t, "ensure-fails", "fail")
	if v.Final.Ensures != 1 {
		t.Fatalf("ensure must run once per session start, ran %d times", v.Final.Ensures)
	}
	if v.Final.Inits < 2 {
		t.Fatalf("after a failed ensure the RESTART_MS init retry loop must keep going: %+v", v.Final)
	}
	if strings.HasPrefix(v.Bash, "PF_CHAT_ID=") {
		t.Fatalf("no daemon, yet a chat id was injected: %q", v.Bash)
	}
}

// TestW174StepAgentPfChatNeverEnsures: a pi step agent (PF_WI_ID set)
// registers no handler, so it can never run ensure (the daemon also never
// loads the operator's packages into a step: runtime's
// TestW174StepAgentsCannotReachTheEnsureAdapters).
func TestW174StepAgentPfChatNeverEnsures(t *testing.T) {
	v := w174RunPiHarness(t, "step-agent", "up")
	if len(v.Handlers) != 0 || v.Final.Ensures != 0 || v.Final.Inits != 0 {
		t.Fatalf("pf-chat in a step agent registered %v and ran ensure %d / init %d times", v.Handlers, v.Final.Ensures, v.Final.Inits)
	}
}

// ---------------------------------------------------------------------------
// Claude Code SessionStart hook
// ---------------------------------------------------------------------------

const w174HookSrc = "../skills/polyforge/hooks/claude-session-start.sh"

type w174HookRun struct {
	out     string
	code    int
	took    time.Duration
	journal []string
	envFile string
}

func w174RunHook(t *testing.T, ensureMode string, extraEnv ...string) w174HookRun {
	t.Helper()
	return w174RunHookWith(t, nil, ensureMode, extraEnv...)
}

// w174RunHookWith is w174RunHook with prep run on the hook's bin dir (the
// head of its PATH) before the hook starts.
func w174RunHookWith(t *testing.T, prep func(bin string), ensureMode string, extraEnv ...string) w174HookRun {
	t.Helper()
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if prep != nil {
		prep(filepath.Join(work, "bin"))
	}
	if err := os.WriteFile(filepath.Join(work, "bin", "pf"), []byte(w174FakePf), 0o755); err != nil {
		t.Fatal(err)
	}
	// a fake tmux: has-session answers per $FAKE_TMUX_LOOP (1 = the
	// polyforge-daemon loop exists); the real tmux is never reached
	fakeTmux := "#!/bin/sh\necho \"tmux $*\" >> \"$PF_JOURNAL\"\n[ \"$1\" = has-session ] && [ \"$3\" = =polyforge-daemon ] && [ \"${FAKE_TMUX_LOOP:-0}\" = 1 ] && exit 0\nexit 1\n"
	if err := os.WriteFile(filepath.Join(work, "bin", "tmux"), []byte(fakeTmux), 0o755); err != nil {
		t.Fatal(err)
	}
	journal, state, envFile := filepath.Join(work, "journal"), filepath.Join(work, "state"), filepath.Join(work, "claude-env")
	if err := os.WriteFile(state, []byte("down"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", w174HookSrc)
	cmd.Stdin = strings.NewReader(`{"session_id":"0b9f6a52-3c1d-4e8f-9a7b-1c2d3e4f5a6b","source":"startup","hook_event_name":"SessionStart"}`)
	cmd.Env = append([]string{
		"PATH=" + filepath.Join(work, "bin") + ":/usr/bin:/bin",
		"HOME=" + work, "PF_STATE=" + state, "PF_JOURNAL=" + journal, "PF_ENSURE=" + ensureMode,
		"CLAUDE_ENV_FILE=" + envFile,
		"TMUX_TMPDIR=" + t.TempDir(),
	}, extraEnv...)
	start := time.Now()
	out, err := cmd.Output()
	r := w174HookRun{out: string(out), took: time.Since(start)}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			r.code = ee.ExitCode()
		} else {
			t.Fatalf("hook: %v", err)
		}
	}
	raw, _ := os.ReadFile(journal)
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if !strings.HasPrefix(l, "tmux ") { // the pf calls only
			r.journal = append(r.journal, l)
		}
	}
	if len(r.journal) == 0 {
		r.journal = []string{""}
	}
	envRaw, _ := os.ReadFile(envFile)
	r.envFile = string(envRaw)
	return r
}

func TestW174ClaudeHookEnsuresThenInits(t *testing.T) {
	r := w174RunHook(t, "up")
	if r.code != 0 {
		t.Fatalf("hook exit %d", r.code)
	}
	if len(r.journal) < 2 || !strings.HasPrefix(r.journal[0], "daemon ensure --timeout") || !strings.HasPrefix(r.journal[1], "chat init --harness claude") {
		t.Fatalf("hook must run ensure, then chat init: journal %q", r.journal)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(r.out), &doc); err != nil || !strings.Contains(r.out, "chat_w174ensure0001") {
		t.Fatalf("hook stdout is not the chat-id JSON object: %q (%v)", r.out, err)
	}
	if r.envFile != "export PF_CHAT_ID=chat_w174ensure0001\n" {
		t.Fatalf("CLAUDE_ENV_FILE = %q", r.envFile)
	}
}

// TestW174ClaudeHookSkipsEnsureWhileTheLoopExists: with the restart loop
// present the hook does not wait on ensure (the loop owns the daemon).
func TestW174ClaudeHookSkipsEnsureWhileTheLoopExists(t *testing.T) {
	r := w174RunHook(t, "hang", "FAKE_TMUX_LOOP=1")
	if r.took > 5*time.Second {
		t.Fatalf("the hook waited %s although the loop exists", r.took)
	}
	for _, l := range r.journal {
		if strings.HasPrefix(l, "daemon ensure") {
			t.Fatalf("the hook ran ensure although the loop exists: %q", r.journal)
		}
	}
	if len(r.journal) == 0 || !strings.HasPrefix(r.journal[0], "chat init") {
		t.Fatalf("the hook must still ask for the chat id: %q", r.journal)
	}
}

func TestW174ClaudeHookSilentWhenEnsureFails(t *testing.T) {
	r := w174RunHook(t, "fail")
	if r.code != 0 || r.out != "" {
		t.Fatalf("a failed ensure must leave the hook silent with exit 0: exit %d stdout %q", r.code, r.out)
	}
	if r.envFile != "" {
		t.Fatalf("no chat id, yet CLAUDE_ENV_FILE was written: %q", r.envFile)
	}
}

// TestW174ClaudeHookBoundedWhenEnsureHangs: the hook wraps ensure in
// `timeout 20`, and a hanging ensure killed by it leaves the hook silent
// with exit 0, still asking for the chat id afterwards. A `timeout` shim
// first on the hook's PATH journals the argv the hook passed, then runs
// the REAL timeout(1) with a 3s bound in place of the 20 — the kill and
// the hook's handling of it stay real; the 20 is pinned by the argv.
func TestW174ClaudeHookBoundedWhenEnsureHangs(t *testing.T) {
	realTimeout, err := exec.LookPath("timeout")
	if err != nil {
		t.Skip("no timeout(1) on this machine")
	}
	shim := func(bin string) {
		body := "#!/bin/sh\necho \"timeout $*\" >> \"$PF_JOURNAL\"\nshift\nexec '" + realTimeout + "' 3 \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, "timeout"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := w174RunHookWith(t, shim, "hang")
	if r.code != 0 || r.out != "" {
		t.Fatalf("a hanging ensure must leave the hook silent with exit 0: exit %d stdout %q", r.code, r.out)
	}
	if r.took > 25*time.Second {
		t.Fatalf("the hook waited %s on a hanging ensure: the bound is 20s", r.took)
	}
	if len(r.journal) < 3 || r.journal[0] != "timeout 20 pf daemon ensure --timeout 15s" ||
		r.journal[1] != "daemon ensure --timeout 15s" || !strings.HasPrefix(r.journal[2], "chat init --harness claude") {
		t.Fatalf("the hook must run ensure under `timeout 20`, then (ensure killed) still ask for the chat id: journal %q", r.journal)
	}
}

// TestW174ClaudeStepHookNeverEnsures: a claude step (PF_WI_ID set; and
// --bare / --safe-mode skip hooks altogether) never reaches pf.
func TestW174ClaudeStepHookNeverEnsures(t *testing.T) {
	r := w174RunHook(t, "up", "PF_WI_ID=wi_step")
	if r.code != 0 || r.out != "" || (len(r.journal) > 0 && r.journal[0] != "") {
		t.Fatalf("the hook ran in a step agent: exit %d stdout %q journal %q", r.code, r.out, r.journal)
	}
}
