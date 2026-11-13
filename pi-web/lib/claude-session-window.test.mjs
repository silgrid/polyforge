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

// the header's context meter divides by the window claude itself reports
// for the session's model (`result.modelUsage[<model>].contextWindow`), not pi's catalog
// entry, once the child reported one. Every 2.1.312 recording reports 1,000,000, the
// catalog's figure, so the recordings are copied into a scratch dir with that window
// rewritten to 200,000 — what an account without the long context gets — and the
// unit-test stub replays the copy (it reads the recordings next to its own file).
// Nothing here touches the operator's ~/.claude or a network.
const REPORTED_WINDOW = 200_000;
const CATALOG_WINDOW = 1_000_000;

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-window-"));
const agentDir = path.join(scratch, "agent");
const configDir = path.join(scratch, "claude-config");
const homeDir = path.join(scratch, "home");
const binDir = path.join(scratch, "bin");
const workDir = path.join(scratch, "work");
for (const dir of [agentDir, binDir, workDir, homeDir]) fs.mkdirSync(dir);
fs.copyFileSync(new URL("./__fixtures__/claude-stub/claude.mjs", import.meta.url), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
const recordings = path.join(scratch, "claude-2.1.312");
fs.cpSync(fileURLToPath(new URL("./__fixtures__/claude-2.1.312", import.meta.url)), recordings, { recursive: true });
const compactStream = path.join(recordings, "compact", "stream.jsonl");
const recorded = fs.readFileSync(compactStream, "utf8");
const rewritten = recorded.replaceAll(`"contextWindow":${CATALOG_WINDOW}`, `"contextWindow":${REPORTED_WINDOW}`);
fs.writeFileSync(compactStream, rewritten);

process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.HOME = homeDir;
process.env.CLAUDE_CONFIG_DIR = configDir;
process.env.PATH = `${binDir}${path.delimiter}${process.env.PATH}`;
process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";
// Each stdin line replays the next segment of the (rewritten) compact/ recording: reply, reply, /compact.
process.env.PI_TEST_CLAUDE_STUB_MODE = "compact";

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

const usage = (tokens, contextWindow) => (tokens === null
  ? { tokens: null, percent: null, contextWindow }
  : { tokens, percent: (tokens / contextWindow) * 100, contextWindow });

test("the rewritten recording reports the smaller window in every result, and nothing else changed", () => {
  const windows = rewritten.split("\n").filter(Boolean).map((line) => JSON.parse(line))
    .filter((event) => event.type === "result")
    .map((event) => Object.values(event.modelUsage).map((entry) => entry.contextWindow));
  assert.deepEqual(windows, [[REPORTED_WINDOW], [REPORTED_WINDOW], [REPORTED_WINDOW]]);
  assert.equal(rewritten.replaceAll(`"contextWindow":${REPORTED_WINDOW}`, `"contextWindow":${CATALOG_WINDOW}`), recorded);
  assert.equal(claude.claudeContextWindow("claude-haiku-5-5"), CATALOG_WINDOW, "the catalog's window is the one claude does not report here");
});

test("get_state's window is claude's reported one once the child reported it: replies, a compaction, a model switch, a resumed child", async () => {
  const record = {
    id: randomUUID(),
    cwd: workDir,
    model: "claude-haiku-5-5",
    created: new Date().toISOString(),
    modified: new Date().toISOString(),
    firstMessage: "",
    usage: { ...index.EMPTY_CLAUDE_USAGE },
  };
  await index.upsertClaudeSessionRecord(record);
  const contextUsage = async (session) => (await session.send({ type: "get_state" })).contextUsage;

  const session = claude.startClaudeSession(record);
  const events = [];
  session.onEvent((event) => events.push(event));
  assert.deepEqual(await contextUsage(session), usage(null, CATALOG_WINDOW), "nothing reported yet: no count, the catalog's window");

  const turn = async (message, done) => {
    const before = events.filter((event) => event.type === done).length;
    await session.send({ type: "prompt", message });
    await waitFor(() => events.filter((event) => event.type === done).length > before, 10_000, `${message}: no ${done}`);
  };
  await turn("first", "prompt_done");
  assert.deepEqual(await contextUsage(session), usage(16688, REPORTED_WINDOW), "the first result's window replaces the catalog's");
  await turn("second", "prompt_done");
  assert.deepEqual(await contextUsage(session), usage(16739, REPORTED_WINDOW));

  assert.deepEqual(await session.send({ type: "compact" }), { tokensBefore: 16739 });
  assert.equal(events.filter((event) => event.type === "compaction_end").length, 1);
  assert.deepEqual(await contextUsage(session), usage(2978, REPORTED_WINDOW), "after /compact: the size claude left over its own window");

  // A window is per model: one this child has not reported for falls back to the catalog's.
  assert.deepEqual(await session.send({ type: "set_model", model: "claude-sonnet-5-5" }), { model: "claude-sonnet-5-5" });
  assert.deepEqual(await contextUsage(session), usage(2978, CATALOG_WINDOW), "sonnet: not reported by this child");
  assert.deepEqual(await session.send({ type: "set_model", model: "claude-haiku-5-5" }), { model: "claude-haiku-5-5" });
  assert.deepEqual(await contextUsage(session), usage(2978, REPORTED_WINDOW), "back on haiku: its reported window again");
  await session.shutdown();

  // The window is the child's report, not persisted: a resumed child shows the catalog's until its first result.
  const resumed = claude.startClaudeSession(record);
  assert.deepEqual(await contextUsage(resumed), usage(2978, CATALOG_WINDOW), "a resumed child before its first result");
  await resumed.shutdown();
});
