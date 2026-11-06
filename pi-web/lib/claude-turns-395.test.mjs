// carries: lib/claude-session.ts, lib/claude-adapter.ts, lib/__fixtures__/claude-stub/*, lib/__fixtures__/claude-2.1.319/*
// a claude turn ends on claude's own signals, never by counting `init`s
// (AC2), and Stop with claude idle reaches neither claude nor its process group (AC3).
// Both replay the real 2.1.319 recordings (lib/__fixtures__/claude-2.1.319/README.md)
// through the unit stub, stdin line by stdin line.
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

const here = path.dirname(fileURLToPath(import.meta.url));
// A scratch agent dir, config dir and HOME, and a PATH whose `claude` is the unit stub.
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-turns-395-"));
const agentDir = path.join(scratch, "agent");
const configDir = path.join(scratch, "claude-config");
const homeDir = path.join(scratch, "home");
const binDir = path.join(scratch, "bin");
const workDir = path.join(scratch, "work");
const stubLog = path.join(scratch, "stub.jsonl");
for (const dir of [agentDir, binDir, workDir, homeDir]) fs.mkdirSync(dir);
fs.copyFileSync(path.join(here, "__fixtures__", "claude-stub", "claude.mjs"), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
// The stub reads the recordings next to its own directory.
for (const name of ["claude-2.1.312", "claude-2.1.319"]) fs.symlinkSync(path.join(here, "__fixtures__", name), path.join(scratch, name));
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.HOME = homeDir;
process.env.CLAUDE_CONFIG_DIR = configDir;
process.env.PATH = `${binDir}${path.delimiter}${process.env.PATH}`;
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";

const jiti = createJiti(import.meta.url, { alias: { "@": path.dirname(here) }, interopDefault: true });
const claude = await jiti.import("./claude-session.ts");
const adapter = await jiti.import("./claude-adapter.ts");
const index = await jiti.import("./claude-index.ts");
const { CLAUDE_TASK_NOTIFICATION_TYPE } = await jiti.import("./claude-subagent-view.ts");

test.after(async () => {
  await claude.shutdownAllClaudeSessions();
  for (const entry of records().filter((record) => record.kind === "grandchild")) {
    try { process.kill(entry.grandchildPid, "SIGKILL"); } catch { /* gone */ }
  }
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
const stdinOf = (pid) => records().filter((entry) => entry.kind === "stdin" && entry.pid === pid).map((entry) => JSON.parse(entry.line));
const alive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
};
const isAnchor = (event) => event.type === "message_start" && event.message?.role === "custom" && event.message?.customType === CLAUDE_TASK_NOTIFICATION_TYPE;
const jsonl = (file) => fs.readFileSync(path.join(here, "__fixtures__", "claude-2.1.319", file), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));

async function newRecord() {
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
  return record;
}
function collect(session) {
  const events = [];
  session.onEvent((event) => events.push(event));
  return events;
}
const count = (events, type) => events.filter((event) => event.type === type).length;

/** ①: the first turn (its background run's notification absorbed), then the second message, each to its end. */
async function replayAbsorbedNotification(session, events) {
  await session.send({ type: "prompt", message: "first" });
  await waitFor(() => count(events, "agent_settled") === 1, 5_000, "the first turn did not end at its result");
  await session.send({ type: "prompt", message: "second" });
}

test("AC2 translator: the absorbed notification opens no turn — the second message's init brings no follow-up anchor", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 0);
  const stream = jsonl("absorbed-notification/stream.jsonl");
  const secondInit = stream.findLastIndex((event) => event.type === "system" && event.subtype === "init");
  const out = stream.map((event) => translator.push(event));
  assert.ok(stream.slice(0, secondInit).some((event) => event.type === "system" && event.subtype === "task_notification"), "the recording holds no notification before the second turn");
  assert.ok(!out[secondInit].events.some(isAnchor), "the user's own turn was announced as claude's follow-up to the absorbed notification");
  assert.equal(out.filter((translation) => translation.turnEnd).length, 2, "two results, two turn ends");
});

test("AC2 session: replaying ①, the second message's turn ends at its one result; the next prompt is taken", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "absorbed-notification";
  const session = claude.startClaudeSession(await newRecord());
  const events = collect(session);
  await replayAbsorbedNotification(session, events);
  await waitFor(() => count(events, "agent_settled") === 2, 5_000, "the second message's turn never ended: an absorbed notification was counted as an owed result");
  assert.equal(session.isRunning(), false);
  assert.equal(count(events, "prompt_done"), 2);
  assert.equal(count(events, "agent_start"), 2, "one turn per message, no turn of claude's own");
  assert.ok(!events.some(isAnchor), "no follow-up anchor: claude ran no follow-up turn");
  await assert.doesNotReject(session.send({ type: "prompt", message: "third" }), "the input stayed locked");
  await session.shutdown();
});

test("AC3: Stop with claude idle after ① reaches neither claude nor the process group; the background run lives on", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "absorbed-notification";
  process.env.PI_TEST_CLAUDE_STUB_GRANDCHILD = "1";
  try {
    const session = claude.startClaudeSession(await newRecord());
    const events = collect(session);
    const pid = session.pid;
    const grandchild = await waitFor(() => records().find((entry) => entry.kind === "grandchild" && entry.pid === pid), 5_000, "the stub started no background process");
    await replayAbsorbedNotification(session, events);
    // Give a wrongly-held turn every chance to show: Stop, then wait out the SIGINT deadline.
    await waitFor(() => stdinOf(pid).length === 2, 5_000, "the second message did not reach claude");
    await delay(200);
    await session.send({ type: "abort" });
    await delay(claude.CLAUDE_INTERRUPT_DEADLINE_MS + 1_500);
    assert.deepEqual(stdinOf(pid).map((line) => line.type), ["user", "user"], "Stop wrote to an idle claude (an idle interrupt stops its background agents, idle-interrupt/)");
    assert.ok(!records().some((entry) => entry.kind === "signal" && entry.pid === pid), "the process group was signalled");
    assert.equal(alive(pid), true, "the claude process exited");
    assert.equal(alive(grandchild.grandchildPid), true, "the background process was killed");
    assert.equal(session.isAlive(), true);
    assert.equal(session.isRunning(), false);
    await session.shutdown();
  } finally {
    delete process.env.PI_TEST_CLAUDE_STUB_GRANDCHILD;
  }
});

test("AC3: Stop while a turn really runs is unchanged — the interrupt goes to claude, and a turn still running at the deadline gets SIGINT", async () => {
  // steer-absorbed's first segment ends mid-turn (a foreground tool call), so the turn stays open.
  process.env.PI_TEST_CLAUDE_STUB_MODE = "steer-absorbed";
  const session = claude.startClaudeSession(await newRecord());
  const events = collect(session);
  const pid = session.pid;
  await session.send({ type: "prompt", message: "long" });
  await waitFor(() => events.some((event) => event.type === "tool_execution_start"), 5_000, "the turn did not reach its tool call");
  assert.equal(session.isRunning(), true);
  await session.send({ type: "abort" });
  await waitFor(() => stdinOf(pid).some((line) => line.type === "control_request" && line.request?.subtype === "interrupt"), 2_000, "Stop sent no interrupt to a running turn");
  // The stub answers the interrupt but never ends the turn: the deadline signals the group.
  await waitFor(() => records().some((entry) => entry.kind === "signal" && entry.pid === pid && entry.signal === "SIGINT"), claude.CLAUDE_INTERRUPT_DEADLINE_MS + 3_000, "no SIGINT at the deadline");
  await waitFor(() => !session.isAlive(), 5_000, "the signalled child did not exit");
});
