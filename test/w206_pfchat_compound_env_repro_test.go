package test

// w206 (polyforge#206, root-cause lane step "repro"): pf-chat.ts env
// injection does not survive compound commands. The extension's tool_call
// hook rewrites every bash command to `PF_CHAT_ID=<id> <command>`, and pi's
// bash tool runs that whole string as ONE `bash -c` invocation — so the
// leading assignment can only ever reach the FIRST simple command of the
// string. When the model runs a compound command like
// `echo step done; pf submit`, the `pf submit` part executes chat-less:
// the real pf then loses the chat routing (#199/#202/#204 submitted
// chat-less; pi#111's needs_input fanned out to 8 subscribed chats).
//
// This test drives the REAL extension file (skills/polyforge/extensions/
// pf-chat.ts, copied verbatim into a temp dir) through a mock pi
// ExtensionAPI, with a fake `pf` on PATH that journals every invocation
// together with the PF_CHAT_ID it did (or did not) see. After a successful
// session_start it fires tool_call for a compound command (`cmd1; pf
// submit`) and a simple one (`pf submit`), then replays each mutated
// command string exactly the way pi's bash tool does (`bash -c <string>`).
// It asserts the chat id reaches the `pf submit` invocation in BOTH shapes
// — the simple one as a control that pins the injection itself is active.
// On the current code it fails for exactly the reported reason: the
// compound command's `pf submit` observes PF_CHAT_ID unset.
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

const w206ExtSrc = "../skills/polyforge/extensions/pf-chat.ts"

const w206ChatID = "chat_w206repro0001"

const w206FakePf = `#!/bin/sh
# fake pf for the w206 repro: every invocation journaled as
# "<args...> PF_CHAT_ID=<value|UNSET>"; ` + "`chat init`" + ` answers a fixed id.
printf '%s PF_CHAT_ID=%s\n' "$*" "${PF_CHAT_ID-UNSET}" >> "$PF_JOURNAL"
case "$1" in
  chat)
    echo "` + w206ChatID + `"
    exit 0
    ;;
  watch)
    if [ "$2" = "--chat" ] && [ "$4" = "--statusline" ]; then
      printf 'WI wi_w206 step repro: running\n'
      exit 0
    fi
    # resident watcher: stay alive until the extension stops it
    while :; do sleep 1; done
    ;;
esac
exit 0
`

const w206Harness = `
import { spawn } from "node:child_process";
import { mkdirSync, writeFileSync, readFileSync } from "node:fs";
import { join } from "node:path";

const work = process.env.W206_WORK;
const journalFile = join(work, "pf-journal.log");
mkdirSync(work, { recursive: true });
try { readFileSync(journalFile); } catch { writeFileSync(journalFile, ""); }

// Simulate the OWNER's pi session: no PF_WI_ID (that would make the
// extension return early) and no ambient PF_CHAT_ID — the extension's
// per-command prefix is the only delivery mechanism for the chat id.
delete process.env.PF_WI_ID;
delete process.env.PF_CHAT_ID;

const env = {
  ...process.env,
  PATH: join(work, "bin") + ":" + process.env.PATH,
  PF_JOURNAL: journalFile,
};
Object.assign(process.env, { PATH: env.PATH, PF_JOURNAL: env.PF_JOURNAL });

// ---- mock pi ExtensionAPI (same shape as the w172 repro) ----
const handlers = {};
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
  sendMessage: () => {},
};
const ctx = {
  sessionManager: { getSessionId: () => "sess_w206_repro" },
  ui: { setWidget() {} },
};

// ---- load the real extension (byte-identical copy) ----
const mod = await import("./pf-chat.ts");
await mod.default(pi);

// session_start resolves only after "pf chat init" succeeded, so the
// extension's chatId is set before any tool_call fires.
await handlers["session_start"]({}, ctx);

// fireToolCall mutates event.input.command like pi does, then replays the
// mutated string exactly the way pi's bash tool executes it: ONE shell
// invocation, "bash -c <string>" (pi utils/shell.ts getBashShellConfig).
const fireToolCall = async (raw) => {
  const event = { toolName: "bash", input: { command: raw } };
  await handlers["tool_call"](event, {});
  const mutated = event.input.command;
  await new Promise((resolve) => {
    const child = spawn("bash", ["-c", mutated], { env, stdio: ["ignore", "ignore", "inherit"] });
    child.on("exit", () => resolve());
    child.on("error", () => resolve());
  });
  return mutated;
};

const mutatedCompound = await fireToolCall("echo step done; pf submit");
const mutatedSimple = await fireToolCall("pf submit");

if (handlers["session_shutdown"]) await handlers["session_shutdown"]({}, ctx);

const lines = readFileSync(journalFile, "utf8").split("\n").filter((l) => l.trim() !== "");
const submitEnvs = lines
  .filter((l) => l.startsWith("submit "))
  .map((l) => (l.match(/PF_CHAT_ID=(.*)$/) || [])[1]);

const verdict = {
  chatInitOk: lines.some((l) => l.startsWith("chat init")),
  compound: { mutated: mutatedCompound, submitEnv: submitEnvs[0] },
  simple: { mutated: mutatedSimple, submitEnv: submitEnvs[1] },
  journal: lines,
};
console.log(JSON.stringify(verdict));
`

func w206NodeMajor(t *testing.T, nodeBin string) int {
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

func w206GlobalNodeModules(t *testing.T) string {
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

func TestW206PfChatCompoundCommandCarriesChatId(t *testing.T) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not found: %v", err)
	}
	if major := w206NodeMajor(t, nodeBin); major < 23 {
		t.Skipf("node %d lacks unflagged type stripping (need >= 23.6)", major)
	}
	nmRoot := w206GlobalNodeModules(t)
	piPkg := filepath.Join(nmRoot, "@earendil-works", "pi-coding-agent")

	extSrc, err := os.ReadFile(w206ExtSrc)
	if err != nil {
		t.Fatalf("read extension source %s: %v", w206ExtSrc, err)
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
	if err := os.WriteFile(filepath.Join(work, "bin", "pf"), []byte(w206FakePf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "harness.mjs"), []byte(w206Harness), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodeBin, "harness.mjs")
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "W206_WORK="+work)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()

	var v struct {
		ChatInitOk bool `json:"chatInitOk"`
		Compound   struct {
			Mutated   string `json:"mutated"`
			SubmitEnv string `json:"submitEnv"`
		} `json:"compound"`
		Simple struct {
			Mutated   string `json:"mutated"`
			SubmitEnv string `json:"submitEnv"`
		} `json:"simple"`
		Journal []string `json:"journal"`
	}
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &v); jsonErr != nil {
		t.Fatalf("harness output not JSON (node exit=%v): %v\nstdout:\n%s\nstderr:\n%s", runErr, jsonErr, out.String(), errb.String())
	}

	// Harness sanity: the reported path was exercised — chat init succeeded
	// and the injection itself fired on both commands.
	if !v.ChatInitOk {
		t.Fatalf("`pf chat init` never ran — harness did not exercise the reported path (journal: %v)", v.Journal)
	}
	if !strings.HasPrefix(v.Compound.Mutated, "PF_CHAT_ID="+w206ChatID+" ") {
		t.Fatalf("compound bash command was not prefixed with the chat id: got %q", v.Compound.Mutated)
	}
	if !strings.HasPrefix(v.Simple.Mutated, "PF_CHAT_ID="+w206ChatID+" ") {
		t.Fatalf("simple bash command was not prefixed with the chat id: got %q", v.Simple.Mutated)
	}

	// Control: for a simple command the prefix does its job — `pf` sees the
	// chat id. If this fails, injection is broken in general, which is NOT
	// the reported failure.
	if v.Simple.SubmitEnv != w206ChatID {
		t.Errorf("control `pf submit` (simple command) saw PF_CHAT_ID=%q, want %q — injection broken for simple commands too (not the reported shape)", v.Simple.SubmitEnv, w206ChatID)
	}

	// The report: in a compound command `cmd1; pf submit` the trailing pf
	// must still observe the chat id — the whole command string carries it.
	if v.Compound.SubmitEnv != w206ChatID {
		t.Errorf("REPORTED FAILURE: bash command %q was executed as one `bash -c` string, but the `pf submit` half observed PF_CHAT_ID=%q (want %q) — pf ran chat-less: origin loses the chat id and Passive routing fans decisions out to every subscribed chat",
			v.Compound.Mutated, v.Compound.SubmitEnv, w206ChatID)
		t.Logf("w206 verdict JSON: %s", strings.TrimSpace(out.String()))
	}
}
