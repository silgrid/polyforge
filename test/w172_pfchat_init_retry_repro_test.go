package test

// w172 (polyforge#172, root-cause lane step "repro"): the pf-chat
// extension's init-retry gap. After a machine restart where pi-web comes
// back before the pf daemon (tmux while-true revives pi-web, the daemon
// has no supervision), session_start's `pf chat init` fails, the
// extension hits `if (!chatId) return` and PERMANENTLY gives up: even
// after the daemon is revived, no retry, no resident watcher, no status
// widget, no PF_CHAT_ID injection — while the very same file restarts its
// watcher subprocess every RESTART_MS (5s). This test drives the REAL
// extension file (skills/polyforge/extensions/pf-chat.ts, copied
// verbatim into a temp dir) through a mock pi ExtensionAPI, with a fake
// `pf` on PATH that simulates the daemon being down during session_start
// and up afterwards. It asserts the recovery the report demands — once
// the daemon answers, the extension must come back within a few
// RESTART_MS beats. On the current code it fails for exactly the
// reported reason: chat init is attempted once and never again.
//
// Requires node >= 23.6 (type stripping) and the installed
// @earendil-works/pi-coding-agent package (resolved via `npm root -g`);
// skipped, loudly, when either is missing.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const w172ExtSrc = "../skills/polyforge/extensions/pf-chat.ts"

const w172FakePf = `#!/bin/sh
# fake pf for the w172 repro: daemon state in $PF_STATE ("down"/"up"),
# every invocation appended to $PF_JOURNAL.
echo "$1 $2 $3 $4" >> "$PF_JOURNAL"
state=$(cat "$PF_STATE" 2>/dev/null || echo down)
case "$1" in
  chat)
    if [ "$state" = "up" ]; then
      echo "chat_w172repro0001"
      exit 0
    fi
    echo "pf: daemon down (connection refused)" >&2
    exit 1
    ;;
  watch)
    if [ "$2" = "--chat" ] && [ "$4" = "--statusline" ]; then
      if [ "$state" = "up" ]; then
        printf 'WI wi_OoTs6rMMDG step repro: running\n'
        exit 0
      fi
      exit 1
    fi
    # resident watcher: stay alive while the daemon is up
    if [ "$state" = "up" ]; then
      while :; do sleep 1; done
    fi
    exit 1
    ;;
esac
exit 1
`

const w172Harness = `
import { spawn } from "node:child_process";
import { mkdirSync, writeFileSync, readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const work = process.env.W172_WORK;
const stateFile = join(work, "daemon-state");
const journalFile = join(work, "pf-journal.log");
mkdirSync(work, { recursive: true });
try { readFileSync(stateFile); } catch { writeFileSync(stateFile, "down"); }
try { readFileSync(journalFile); } catch { writeFileSync(journalFile, ""); }

const env = {
  ...process.env,
  PATH: join(work, "bin") + ":" + process.env.PATH,
  PF_STATE: stateFile,
  PF_JOURNAL: journalFile,
};
// The extension ignores step agents (PF_WI_ID in env); this harness
// simulates the OWNER's pi session, so drop it.
delete env.PF_WI_ID;
delete process.env.PF_WI_ID;
Object.assign(process.env, { PATH: env.PATH, PF_STATE: env.PF_STATE, PF_JOURNAL: env.PF_JOURNAL });

// ---- mock pi ExtensionAPI ----
const handlers = {};
const ui = {
  widget: undefined,
  setWidget(_k, content) { this.widget = content; },
};
const sent = [];
const pi = {
  on: (ev, fn) => { handlers[ev] = fn; },
  exec: (cmd, args) => new Promise((resolve) => {
    const child = spawn(cmd, args, { env, stdio: ["ignore", "pipe", "pipe"] });
    let out = "", err = "";
    child.stdout.on("data", (d) => (out += d));
    child.stderr.on("data", (d) => (err += d));
    child.on("exit", (code) => resolve({ code: code ?? -1, stdout: out, stderr: err }));
    child.on("error", (e) => resolve({ code: -1, stdout: "", stderr: String(e) }));
  }),
  sendMessage: (msg, opts) => { sent.push({ msg, opts }); },
};
const ctx = {
  sessionManager: { getSessionId: () => "sess_w172_repro" },
  ui,
};

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---- load the real extension (byte-identical copy) ----
const mod = await import("./pf-chat.ts");
await mod.default(pi);

const journalLines = () =>
  readFileSync(journalFile, "utf8").split("\n").filter((l) => l.trim() !== "");
const snapshot = () => {
  const lines = journalLines();
  return {
    chatInitAttempts: lines.filter((l) => l.startsWith("chat init")).length,
    watchSpawns: lines.filter((l) => l.startsWith("watch --chat ") && !l.includes("--statusline")).length,
    statuslinePolls: lines.filter((l) => l.includes("--statusline")).length,
    widgetSet: ui.widget !== undefined,
  };
};

const verdict = { phase1: {}, phase2: {}, bashCommandAfterRevival: "", recovered: false };

// ---- phase 1: session_start while the daemon is down ----
await handlers["session_start"]({}, ctx);
await sleep(1500);
verdict.phase1 = snapshot();

// ---- phase 2: operator revives the daemon; allow 2.6x RESTART_MS (5s) ----
// Polls for the recovery the verdict demands (chat init retried, resident
// watcher spawned, widget set) and stops at the first moment it holds,
// never later than 13s after the revival.
writeFileSync(stateFile, "up");
const revivedBy = Date.now() + 13000;
for (;;) {
  const s = snapshot();
  if (s.chatInitAttempts >= 2 && s.watchSpawns >= 1 && s.widgetSet) break;
  if (Date.now() >= revivedBy) break;
  await sleep(100);
}
const bashEvent = { toolName: "bash", input: { command: "echo hi" } };
await handlers["tool_call"](bashEvent, {});
verdict.phase2 = snapshot();
verdict.bashCommandAfterRevival = bashEvent.input.command;
verdict.recovered =
  verdict.phase2.chatInitAttempts >= 2 &&
  verdict.phase2.watchSpawns >= 1 &&
  verdict.phase2.widgetSet &&
  bashEvent.input.command.startsWith("PF_CHAT_ID=");

// clean shutdown (kills any resident watcher the extension spawned)
if (handlers["session_shutdown"]) await handlers["session_shutdown"]({}, ctx);

console.log(JSON.stringify(verdict));
process.exit(verdict.recovered ? 0 : 1);
`

func w172NodeMajor(t *testing.T, nodeBin string) int {
	t.Helper()
	out, err := exec.Command(nodeBin, "--version").Output()
	if err != nil {
		t.Skipf("node --version failed: %v", err)
	}
	m := regexp.MustCompile(`v(\d+)\.`).FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		t.Skipf("unparseable node version %q", string(out))
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func w172GlobalNodeModules(t *testing.T) string {
	t.Helper()
	candidates := []string{}
	if out, err := exec.Command("npm", "root", "-g").Output(); err == nil {
		candidates = append(candidates, strings.TrimSpace(string(out)))
	}
	candidates = append(candidates, "/usr/lib/node_modules", "/usr/local/lib/node_modules")
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(filepath.Join(c, "@earendil-works", "pi-coding-agent")); err == nil && st.IsDir() {
			return c
		}
	}
	t.Skipf("installed @earendil-works/pi-coding-agent package not found (tried %v); cannot load pf-chat.ts", candidates)
	return ""
}

func TestW172PfChatInitRetryAfterDaemonRevival(t *testing.T) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not found: %v", err)
	}
	if major := w172NodeMajor(t, nodeBin); major < 23 {
		t.Skipf("node %d lacks unflagged type stripping (need >= 23.6)", major)
	}
	nmRoot := w172GlobalNodeModules(t)
	piPkg := filepath.Join(nmRoot, "@earendil-works", "pi-coding-agent")

	extSrc, err := os.ReadFile(w172ExtSrc)
	if err != nil {
		t.Fatalf("read extension source %s: %v", w172ExtSrc, err)
	}

	work := t.TempDir()
	// byte-identical copy of the real extension (import resolves its
	// @earendil-works/pi-coding-agent dependency from the symlink below)
	if err := os.WriteFile(filepath.Join(work, "pf-chat.ts"), extSrc, 0o644); err != nil {
		t.Fatal(err)
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
	if err := os.WriteFile(filepath.Join(work, "bin", "pf"), []byte(w172FakePf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "harness.mjs"), []byte(w172Harness), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodeBin, "harness.mjs")
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "W172_WORK="+work)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()

	var v struct {
		Phase1 struct {
			ChatInitAttempts int  `json:"chatInitAttempts"`
			WatchSpawns      int  `json:"watchSpawns"`
			StatuslinePolls  int  `json:"statuslinePolls"`
			WidgetSet        bool `json:"widgetSet"`
		} `json:"phase1"`
		Phase2 struct {
			ChatInitAttempts int  `json:"chatInitAttempts"`
			WatchSpawns      int  `json:"watchSpawns"`
			StatuslinePolls  int  `json:"statuslinePolls"`
			WidgetSet        bool `json:"widgetSet"`
		} `json:"phase2"`
		BashCommandAfterRevival string `json:"bashCommandAfterRevival"`
		Recovered               bool   `json:"recovered"`
	}
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &v); jsonErr != nil {
		t.Fatalf("harness output not JSON (node exit=%v): %v\nstdout:\n%s\nstderr:\n%s", runErr, jsonErr, out.String(), errb.String())
	}

	// Phase 1 pins the reported path itself: the daemon is down at
	// session_start, so exactly one `pf chat init` is attempted and fails.
	if v.Phase1.ChatInitAttempts != 1 {
		t.Errorf("phase1: expected exactly 1 `pf chat init` attempt while the daemon is down, got %d (harness did not exercise the reported path)", v.Phase1.ChatInitAttempts)
	}
	if v.Phase1.WidgetSet {
		t.Errorf("phase1: widget set while daemon down — unexpected")
	}

	// Phase 2 is the report: after the daemon is revived, the extension
	// must recover within a few RESTART_MS beats.
	if v.Phase2.ChatInitAttempts < 2 {
		t.Errorf("phase2: within 13s (>2x RESTART_MS=5s) of the daemon's revival `pf chat init` was attempted only %d time(s) total — the extension gave up permanently after the first failure (init has no retry loop)", v.Phase2.ChatInitAttempts)
	}
	if v.Phase2.WatchSpawns < 1 {
		t.Errorf("phase2: no resident `pf watch --chat <id>` spawned after daemon revival")
	}
	if !v.Phase2.WidgetSet {
		t.Errorf("phase2: status widget never populated after daemon revival (status bar stays blank — the reported symptom)")
	}
	if !strings.HasPrefix(v.BashCommandAfterRevival, "PF_CHAT_ID=") {
		t.Errorf("phase2: bash tool_call command after revival is %q — PF_CHAT_ID injection is gone", v.BashCommandAfterRevival)
	}
	if !v.Recovered {
		t.Logf("w172 verdict JSON: %s", strings.TrimSpace(out.String()))
	}
}
