// carries: lib/claude-session.ts, lib/claude-adapter.ts, lib/__fixtures__/claude-stub/*, lib/__fixtures__/claude-2.1.319/*
// AC4 on the server: a message sent while a claude turn runs goes to
// claude's stdin at once; claude absorbs it into the running turn or queues it as its
// next turn (the real 2.1.319 recordings steer-absorbed/ and steer-queued/, replayed by
// the unit stub); the page hears it queued, then delivered with that outcome, and the
// turn ends once, at the right result. History keeps both outcomes.
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
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-steer-395-"));
const agentDir = path.join(scratch, "agent");
const configDir = path.join(scratch, "claude-config");
const homeDir = path.join(scratch, "home");
const binDir = path.join(scratch, "bin");
const workDir = path.join(scratch, "work");
const stubLog = path.join(scratch, "stub.jsonl");
for (const dir of [agentDir, binDir, workDir, homeDir]) fs.mkdirSync(dir);
fs.copyFileSync(path.join(here, "__fixtures__", "claude-stub", "claude.mjs"), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
for (const name of ["claude-2.1.312", "claude-2.1.319"]) fs.symlinkSync(path.join(here, "__fixtures__", name), path.join(scratch, name));
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.HOME = homeDir;
process.env.CLAUDE_CONFIG_DIR = configDir;
process.env.PATH = `${binDir}${path.delimiter}${process.env.PATH}`;
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";

const jiti = createJiti(import.meta.url, { alias: { "@": path.dirname(here) }, interopDefault: true });
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
const stdinOf = (pid) => records().filter((entry) => entry.kind === "stdin" && entry.pid === pid).map((entry) => JSON.parse(entry.line));
const recordedStream = (name) => fs.readFileSync(path.join(here, "__fixtures__", "claude-2.1.319", name, "stream.jsonl"), "utf8").split("\n").filter(Boolean);
const recordedStdin = (name) => fs.readFileSync(path.join(here, "__fixtures__", "claude-2.1.319", name, "stdin.jsonl"), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
const count = (events, type) => events.filter((event) => event.type === type).length;
const userText = (message) => (typeof message?.content === "string" ? message.content : "");
const delivered = (events, text) => events.find((event) => event.type === "message_end" && event.message?.role === "user" && userText(event.message) === text);

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

/** Starts the recording's first turn, waits until it runs, and sends the second message mid-turn. */
async function steer(mode, until) {
  process.env.PI_TEST_CLAUDE_STUB_MODE = mode;
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  await session.send({ type: "prompt", message: "first" });
  await waitFor(() => events.some(until), 5_000, "the first turn did not get going");
  assert.equal(session.isRunning(), true);
  await assert.doesNotReject(session.send({ type: "prompt", message: "second" }), "a message sent while the turn runs was refused");
  return { record, session, events };
}

test("AC4 steer-absorbed: the mid-turn message goes to stdin, waits in the queue, is delivered as taken into the running turn, and the turn ends once", async () => {
  const { record, session, events } = await steer("steer-absorbed", (event) => event.type === "tool_execution_start");
  // send() resolves once the line is written to the pipe; the stub logs it
  // when it reads it, in its own process — wait for that record, never read
  // the log at once (a loaded host left "second" unread there).
  const stdin = await waitFor(() => { const lines = stdinOf(session.pid); return lines.length >= 2 && lines; }, 5_000, "the mid-turn message never reached claude's stdin");
  assert.deepEqual(stdin.map((line) => [line.type, typeof line.message?.content === "string" ? line.message.content : null]), [["user", "first"], ["user", "second"]]);
  assert.notEqual(stdin[0].uuid, stdin[1].uuid);
  const queued = events.find((event) => event.type === "queue_update");
  assert.deepEqual(queued && { steering: queued.steering, followUp: queued.followUp }, { steering: ["second"], followUp: [] }, "the page was not told the message waits");
  await waitFor(() => count(events, "agent_settled") > 0, 5_000, "the turn never ended");
  const message = delivered(events, "second");
  assert.ok(message, "the message was never delivered as a user message");
  assert.equal(message.message.claudeDelivery, "absorbed");
  const at = events.indexOf(message);
  assert.ok(events.slice(0, at).some((event) => event.type === "queue_update"), "delivered before it was announced queued");
  assert.deepEqual(events.slice(at).find((event) => event.type === "queue_update")?.steering, [], "the queue still lists the delivered message");
  const lastReply = events.findLastIndex((event) => event.type === "message_end" && event.message?.role === "assistant");
  assert.ok(at < lastReply, "the absorbed message is not shown before the reply that took it in");
  await delay(300);
  assert.equal(count(events, "agent_settled"), 1, "one turn, one end");
  assert.equal(count(events, "agent_start"), 1);
  assert.equal(session.isRunning(), false);
  // History: claude stores an absorbed message as a queued_command attachment, shown absorbed.
  const recordedText = userText(recordedStdin("steer-absorbed")[1].message);
  const history = claude.readClaudeHistory(record.id).messages.filter((entry) => entry.role === "user" && userText(entry) === recordedText);
  assert.equal(history.length, 1, "history lost the absorbed message");
  assert.equal(history[0].claudeDelivery, "absorbed");
  await session.shutdown();
});

test("AC4 steer-queued: the mid-turn message waits for the first result, is delivered as queued, runs as claude's next turn, and the run ends at its result", async () => {
  const { record, session, events } = await steer("steer-queued", (event) => event.type === "message_update");
  assert.deepEqual(events.find((event) => event.type === "queue_update")?.steering, ["second"]);
  const state = await session.send({ type: "get_state" });
  assert.equal(state.isStreaming, true);
  await waitFor(() => count(events, "agent_settled") > 0, 5_000, "the run never ended");
  await delay(300);
  const results = events.filter((event) => event.type === "agent_end").length;
  assert.equal(results, 2, "two claude turns ran");
  assert.equal(count(events, "agent_settled"), 1, "the first result ended the run while claude still held the queued message");
  const message = delivered(events, "second");
  assert.ok(message, "the queued message was never delivered");
  assert.equal(message.message.claudeDelivery, "queued");
  const firstEnd = events.findIndex((event) => event.type === "agent_end");
  assert.ok(events.indexOf(message) > firstEnd, "the queued message was delivered before the turn it waited behind ended");
  assert.equal(session.isRunning(), false);
  await assert.doesNotReject(session.send({ type: "prompt", message: "third" }));
  // History: a user entry claude dequeued after a turn it waited behind is shown queued.
  const texts = recordedStdin("steer-queued").map((line) => userText(line.message));
  const users = claude.readClaudeHistory(record.id).messages.filter((entry) => entry.role === "user");
  assert.deepEqual(users.map((entry) => [userText(entry), entry.claudeDelivery]), [[texts[0], undefined], [texts[1], "queued"]]);
  await session.shutdown();
});

test("AC4: while a message waits, get_state lists it in pi's queue; a compaction still refuses a message", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "steer-queued";
  // The pace keeps "second" queued while get_state answers: the recording
  // dequeues it about 340 lines into its second segment, a ~6.8 s window at
  // 20 ms a line. The run's bound follows from the same pace: the paced
  // replay of the whole recording, three times over, plus 10 s.
  const paceMs = 20;
  process.env.PI_TEST_CLAUDE_STUB_PACE_MS = String(paceMs);
  const replayLines = recordedStream("steer-queued").length;
  const runBoundMs = 3 * replayLines * paceMs + 10_000;
  try {
    const session = claude.startClaudeSession(await newRecord());
    const events = collect(session);
    await session.send({ type: "prompt", message: "first" });
    await waitFor(() => events.some((event) => event.type === "message_update"), 5_000, "the first turn did not get going");
    await assert.doesNotReject(session.send({ type: "prompt", message: "second" }), "a message sent while the turn runs was refused");
    const state = await session.send({ type: "get_state" });
    assert.deepEqual(state.queuedMessages, { steering: ["second"], followUp: [] });
    assert.equal(state.pendingMessageCount, 1);
    await waitFor(() => count(events, "agent_settled") > 0, runBoundMs, `the run never ended within ${runBoundMs} ms (${replayLines} lines paced at ${paceMs} ms)`);
    assert.deepEqual((await session.send({ type: "get_state" })).queuedMessages, { steering: [], followUp: [] });
    await session.shutdown();
  } finally {
    delete process.env.PI_TEST_CLAUDE_STUB_PACE_MS;
  }
  process.env.PI_TEST_CLAUDE_STUB_MODE = "hang";
  const compacting = claude.startClaudeSession(await newRecord());
  const pending = compacting.send({ type: "compact" }).catch(() => {});
  await assert.rejects(compacting.send({ type: "prompt", message: "during compaction" }), /compact/i);
  await compacting.send({ type: "abort" });
  await pending;
  await compacting.shutdown();
});
