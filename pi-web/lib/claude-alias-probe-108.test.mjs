// carries: lib/claude-probe.ts, lib/claude-session.ts, lib/__fixtures__/claude-stub/claude.mjs
// carries: lib/__fixtures__/claude-2.1.312/*, lib/__fixtures__/claude-2.1.322/*
import "#test-env";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { createJiti } from "jiti";
import { readStubLog } from "./__fixtures__/claude-stub-log.mjs";

// #108: an alias session's context meter after a restart. The probe cache
// (lib/claude-probe.ts) hangs on globalThis and lib/claude-session.test.mjs warms it
// before its own assertions, so the cold-cache behaviour needs a file of its own:
// this process's cache starts empty, and each scenario re-colds it before the read
// that must wait for a probe. A scratch agent dir (index), an inherited claude config
// dir, a scratch HOME and a PATH whose `claude` is the stub: nothing here touches
// the operator's ~/.claude.
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-alias-108-"));
const agentDir = path.join(scratch, "agent");
const configDir = path.join(scratch, "claude-config");
const homeDir = path.join(scratch, "home");
const binDir = path.join(scratch, "bin");
const workDir = path.join(scratch, "work");
const stubLog = path.join(scratch, "stub.jsonl");
for (const dir of [agentDir, configDir, binDir, workDir, homeDir]) fs.mkdirSync(dir);
fs.copyFileSync(new URL("./__fixtures__/claude-stub/claude.mjs", import.meta.url), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
// The stub reads the recordings next to its own file.
fs.symlinkSync(fileURLToPath(new URL("./__fixtures__/claude-2.1.312", import.meta.url)), path.join(scratch, "claude-2.1.312"));
fs.symlinkSync(fileURLToPath(new URL("./__fixtures__/claude-2.1.322", import.meta.url)), path.join(scratch, "claude-2.1.322"));
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.HOME = homeDir;
process.env.CLAUDE_CONFIG_DIR = configDir;
process.env.PATH = `${binDir}${path.delimiter}${process.env.PATH}`;
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_WEB_PASSWORD = "not-for-the-child";
process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";

globalThis.__piWebPushNotifier = Promise.resolve({
  getVapidPublicKey: () => "",
  addSubscription: () => {},
  notifySessionComplete: async () => {},
  notifyClaudePermissionPending: async () => {},
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const claude = await jiti.import("./claude-session.ts");
const index = await jiti.import("./claude-index.ts");

test.after(async () => {
  await claude.shutdownAllClaudeSessions();
  fs.rmSync(scratch, { recursive: true, force: true });
});

async function waitFor(predicate, timeoutMs, message) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const value = predicate();
    if (value) return value;
    await delay(25);
  }
  assert.fail(message);
}

const records = () => readStubLog(stubLog);
const probes = () => records().filter((entry) => entry.kind === "probe");

/**
 * The probe cache cold again — the state after the server process restarts. The module
 * keeps the object it installed at import, so the reset mutates it in place.
 */
function coldCache() {
  globalThis.__piWebClaudeProbe.answers.clear();
  globalThis.__piWebClaudeProbe.latest = null;
}

async function newRecord(extra = {}) {
  const record = {
    id: randomUUID(),
    cwd: workDir,
    model: "claude-sonnet-5-5",
    created: new Date().toISOString(),
    modified: new Date().toISOString(),
    firstMessage: "",
    usage: { ...index.EMPTY_CLAUDE_USAGE },
    ...extra,
  };
  await index.upsertClaudeSessionRecord(record);
  return record;
}

// #108 (AC1): a restart leaves the probe cache empty, and a session whose model is one
// of claude's aliases (`haiku` here; `default` too) had no context meter: the window is
// looked up under the full name only claude's initialize answer resolves the alias to.
// get_state now asks once on demand and waits for that probe.
test("a resumed alias session's get_state resolves the window through one on-demand probe", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const contextWindow = 1_000_000;
  // Expected values are derived independently of the implementation: the window is
  // pi's catalog entry of the full name the recorded initialize answer resolves each
  // alias to (claude-haiku-5-5 / claude-opus-5-5), and 1151 is the recorded turn's
  // last reply's own usage — 2+262+840+47 in lib/__fixtures__/claude-2.1.312/transcript.jsonl
  // — which the resumed child reads back from the transcript it wrote.
  const tokens = 1151;
  for (const alias of ["haiku", "default"]) {
    const record = await newRecord({ model: alias });
    const first = claude.startClaudeSession(record);
    const events = [];
    first.onEvent((event) => events.push(event));
    await first.send({ type: "prompt", message: "read the notes" });
    await waitFor(() => events.some((event) => event.type === "prompt_done"), 10_000, "the turn never ended");
    await first.shutdown();
    coldCache();
    const before = probes().length;
    const resumed = claude.startClaudeSession(record);
    const state = await resumed.send({ type: "get_state" });
    assert.deepEqual(state.contextUsage, { tokens, percent: (tokens / contextWindow) * 100, contextWindow }, alias);
    assert.equal(probes().length - before, 1, `${alias}: one on-demand probe`);
    await resumed.shutdown();
  }
});

// #108 (AC2): claude refuses to answer the probe — a claude not logged in, say — and
// the meter answers null. The one attempt is spent with it: the next read re-pulls
// nothing (the stub log's probe count stays).
test("a refused probe leaves get_state null, the one attempt spent", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  process.env.PI_TEST_CLAUDE_STUB_PROBE_MODE = "refuse";
  try {
    coldCache();
    const record = await newRecord({ model: "haiku" });
    const session = claude.startClaudeSession(record);
    const before = probes().length;
    assert.equal((await session.send({ type: "get_state" })).contextUsage, null);
    assert.equal(probes().length - before, 1, "the one on-demand probe");
    assert.equal((await session.send({ type: "get_state" })).contextUsage, null, "the attempt is spent: null again");
    assert.equal(probes().length - before, 1, "no re-pull on the next read");
    await session.shutdown();
  } finally {
    delete process.env.PI_TEST_CLAUDE_STUB_PROBE_MODE;
  }
});
