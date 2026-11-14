import "#test-env";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { createJiti } from "jiti";
import { readStubLog } from "../../../lib/__fixtures__/claude-stub-log.mjs";

process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-routes-"));
const agentDir = path.join(scratch, "agent");
const binDir = path.join(scratch, "bin");
const project = path.join(scratch, "project");
const stubLog = path.join(scratch, "stub.jsonl");
fs.mkdirSync(binDir);
fs.mkdirSync(project);
fs.copyFileSync(new URL("../../../lib/__fixtures__/claude-stub/claude.mjs", import.meta.url), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
fs.symlinkSync(decodeURIComponent(new URL("../../../lib/__fixtures__/claude-2.1.312", import.meta.url).pathname), path.join(scratch, "claude-2.1.312"));
process.env.PI_CODING_AGENT_DIR = agentDir;
// The inherited claude config dir the stub writes its transcripts under: never the operator's.
process.env.CLAUDE_CONFIG_DIR = path.join(scratch, "claude-config");
const originalPath = process.env.PATH;
process.env.PATH = `${binDir}${path.delimiter}${originalPath}`;
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
// The claude routes spawn no pf (retired the chat's `pf watch`); a stray spawn
// would still never reach the operator's pf or daemon.
process.env.PF_BIN = path.join(scratch, "no-pf");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const command = await jiti.import("./[id]/route.ts");
const events = await jiti.import("./[id]/events/route.ts");
const lease = await jiti.import("./[id]/lease/route.ts");
const running = await jiti.import("./running/route.ts");
const state = await jiti.import("../sessions/[id]/state/route.ts");
const index = await jiti.import("../../../lib/claude-index.ts");
const claude = await jiti.import("../../../lib/claude-session.ts");

test.after(async () => {
  await claude.shutdownAllClaudeSessions();
  process.env.PATH = originalPath;
  fs.rmSync(scratch, { recursive: true, force: true });
});

const params = (id) => ({ params: Promise.resolve({ id }) });
const launches = () => readStubLog(stubLog).filter((entry) => entry.kind === "launch");

async function newRecord() {
  const record = {
    id: randomUUID(),
    cwd: project,
    model: "claude-sonnet-5-5",
    created: new Date().toISOString(),
    modified: new Date().toISOString(),
    firstMessage: "",
    usage: { ...index.EMPTY_CLAUDE_USAGE },
  };
  await index.upsertClaudeSessionRecord(record);
  return record;
}

const send = (id, body) => command.POST(new Request(`http://localhost/api/agent/${id}`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
}), params(id));

/** Reads an events stream until `until` matches a data event (or the deadline), then cancels it. */
async function readEvents(id, until, timeoutMs = 10_000) {
  const controller = new AbortController();
  const response = await events.GET(new Request(`http://localhost/api/agent/${id}/events`, { signal: controller.signal }), params(id));
  assert.equal(response.status, 200);
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  const seen = [];
  let buffer = "";
  const deadline = Date.now() + timeoutMs;
  try {
    while (Date.now() < deadline) {
      const { value, done } = await Promise.race([reader.read(), delay(deadline - Date.now()).then(() => ({ done: true }))]);
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let split;
      while ((split = buffer.indexOf("\n\n")) >= 0) {
        const chunk = buffer.slice(0, split);
        buffer = buffer.slice(split + 2);
        if (!chunk.startsWith("data: ")) continue;
        const event = JSON.parse(chunk.slice(6));
        seen.push(event);
        if (until(event, seen)) return seen;
      }
    }
  } finally {
    controller.abort();
    await reader.cancel().catch(() => {});
  }
  assert.fail(`the stream ended without the expected event: ${seen.map((event) => event.type).join(",")}`);
}

async function waitFor(predicate, timeoutMs, message) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const value = await predicate();
    if (value) return value;
    await delay(25);
  }
  assert.fail(message);
}

test("a claude id round trip: the events stream starts the child, a prompt streams back to agent_settled", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  const stream = readEvents(record.id, (event) => event.type === "prompt_done");
  await waitFor(() => claude.getClaudeSession(record.id), 5_000, "opening the stream started no child");
  const response = await send(record.id, { type: "prompt", message: "hello" });
  assert.equal(response.status, 200);
  const seen = await stream;
  const types = seen.map((event) => event.type);
  assert.equal(types[0], "connected");
  assert.ok(types.includes("agent_start"));
  assert.ok(types.includes("message_update"));
  assert.ok(types.indexOf("agent_settled") > types.indexOf("agent_end"));
  const ended = seen.filter((event) => event.type === "message_end").map((event) => event.message.role);
  assert.deepEqual(ended, ["user", "assistant", "toolResult", "assistant"]);
  const live = await (await state.GET(new Request("http://localhost"), params(record.id))).json();
  assert.equal(live.running, true);
  assert.deepEqual(live.state.model, { id: "claude-sonnet-5-5", provider: "claude" });
  const agentState = await (await command.GET(new Request(`http://localhost/api/agent/${record.id}`), params(record.id))).json();
  assert.equal(agentState.running, true);
  await claude.getClaudeSession(record.id).shutdown();
  assert.deepEqual(await (await state.GET(new Request("http://localhost"), params(record.id))).json(), { running: false });
});

test("pi-only commands are refused for a claude id and start nothing; a malformed switch is 400 too", async () => {
  const record = await newRecord();
  const before = launches().length;
  // `fork` is a claude command now (its malformed form is 400, below).
  for (const type of ["steer", "navigate_tree", "set_thinking_level", "set_tools", "follow_up", "reload"]) {
    const response = await send(record.id, { type });
    assert.equal(response.status, 400, type);
    assert.match((await response.json()).error, /not available for claude sessions/);
  }
  // rewrote #250's "set_model is refused": a switch must name a valid claude model name / a claude effort.
  // an alias ("opus") is a valid name now, so the malformed switches name invalid ones.
  for (const body of [{ type: "set_model", model: "-bad" }, { type: "set_model", provider: "e2e", modelId: "model 0" }, { type: "set_effort", effort: "off" }, { type: "set_effort" }]) {
    assert.equal((await send(record.id, body)).status, 400, JSON.stringify(body));
  }
  assert.equal(launches().length, before);
  assert.equal(claude.getClaudeSession(record.id), undefined, "a refused command launches no child");
  assert.equal(index.getClaudeSessionRecord(record.id).model, "claude-sonnet-5-5");
});

// moving a subagent to the background is only meaningful for a running child.
test("a background_task with no live claude child is refused and starts none", async () => {
  const record = await newRecord();
  const before = launches().length;
  for (const body of [{ type: "background_task", toolCallId: "toolu_x" }, { type: "background_task" }]) {
    const response = await send(record.id, body);
    assert.equal(response.status, 400, JSON.stringify(body));
    assert.match((await response.json()).error, /no live claude child/, JSON.stringify(body));
  }
  assert.equal(launches().length, before, "a refused background_task spawned a child");
  assert.equal(claude.getClaudeSession(record.id), undefined);
});

test("with no live child set_model / set_effort only write the index and spawn nothing", async () => {
  const record = await newRecord();
  const before = launches().length;
  const model = await send(record.id, { type: "set_model", provider: "claude", modelId: "claude-haiku-5-5", model: "claude-haiku-5-5" });
  assert.equal(model.status, 200);
  assert.deepEqual((await model.json()).data, { model: "claude-haiku-5-5", pending: true });
  const effort = await send(record.id, { type: "set_effort", effort: "high" });
  assert.deepEqual((await effort.json()).data, { effort: "high", pending: true });
  assert.equal(launches().length, before, "the switch spawned a child");
  const stored = index.getClaudeSessionRecord(record.id);
  assert.equal(stored.model, "claude-haiku-5-5");
  assert.equal(stored.effort, "high");
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  await readEvents(record.id, (event) => event.type === "connected");
  const launch = await waitFor(() => launches()[before], 5_000, "opening the stream started no child");
  assert.equal(launch.argv[launch.argv.indexOf("--model") + 1], "claude-haiku-5-5");
  assert.equal(launch.argv[launch.argv.indexOf("--effort") + 1], "high");
  await claude.getClaudeSession(record.id)?.shutdown();
});

test("a live set_model is claude's answer: accepted switches in place, a running turn refuses it", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  await readEvents(record.id, (event) => event.type === "connected");
  const session = await waitFor(() => claude.getClaudeSession(record.id), 5_000, "no child");
  await waitFor(() => launches().some((entry) => entry.pid === session.pid), 5_000, "no launch");
  const before = launches().length;
  const switched = await send(record.id, { type: "set_model", model: "claude-fable-5-1" });
  assert.equal(switched.status, 200);
  assert.deepEqual((await switched.json()).data, { model: "claude-fable-5-1" });
  assert.equal(launches().length, before, "the switch restarted the child");
  assert.equal(claude.getClaudeSession(record.id), session);
  assert.equal(index.getClaudeSessionRecord(record.id).model, "claude-fable-5-1");
  const live = await (await command.GET(new Request(`http://localhost/api/agent/${record.id}`), params(record.id))).json();
  assert.deepEqual(live.state.model, { id: "claude-fable-5-1", provider: "claude" });
  const effort = await send(record.id, { type: "set_effort", effort: "max" });
  assert.deepEqual((await effort.json()).data, { effort: "max" });
  assert.equal(index.getClaudeSessionRecord(record.id).effort, "max");
  await session.shutdown();

  process.env.PI_TEST_CLAUDE_STUB_MODE = "hang";
  const busy = await newRecord();
  await send(busy.id, { type: "prompt", message: "long" });
  const hanging = claude.getClaudeSession(busy.id);
  for (const body of [{ type: "set_model", model: "claude-haiku-5-5" }, { type: "set_effort", effort: "low" }]) {
    const refused = await send(busy.id, body);
    assert.notEqual(refused.status, 200, JSON.stringify(body));
    assert.match((await refused.json()).error, /turn is running/);
  }
  const stored = index.getClaudeSessionRecord(busy.id);
  assert.equal(stored.model, "claude-sonnet-5-5");
  assert.equal(stored.effort, "default");
  const stdin = readStubLog(stubLog).filter((entry) => entry.kind === "stdin" && entry.pid === hanging.pid);
  assert.ok(!stdin.some((entry) => entry.line.includes("control_request")), "a mid-turn switch reached stdin");
  await hanging.shutdown();
});

test("abort interrupts the turn and the session takes the next prompt; running ids include it meanwhile", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "hang";
  const record = await newRecord();
  await send(record.id, { type: "prompt", message: "long" });
  const session = claude.getClaudeSession(record.id);
  assert.ok(session);
  const runningIds = await (await running.GET()).json();
  assert.ok(runningIds.runningSessionIds.includes(record.id));
  const leaseReply = await (await lease.POST(new Request("http://localhost"), params(record.id))).json();
  assert.deepEqual(leaseReply, { success: true, running: true, awaitingInput: false, subagentsActive: false });
  assert.equal((await send(record.id, { type: "abort" })).status, 200);
  await waitFor(() => !session.isRunning(), 5_000, "abort did not end the turn");
  assert.equal(session.isAlive(), true);
  assert.equal((await send(record.id, { type: "prompt", message: "again" })).status, 200);
  assert.equal(session.isRunning(), true);
  await session.shutdown();
});

test("the lease and the running snapshot report a claude session's unsettled background subagent", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "subagent-early";
  process.env.PI_TEST_CLAUDE_STUB_HOLD_MS = "500";
  const record = await newRecord();
  const stream = readEvents(record.id, (event) => event.type === "prompt_done" && event.subagentsActive !== true);
  await waitFor(() => claude.getClaudeSession(record.id), 5_000, "opening the stream started no child");
  await send(record.id, { type: "prompt", message: "launch it" });
  const session = claude.getClaudeSession(record.id);
  await waitFor(() => !session.isRunning() && session.hasUnsettledSubagents(), 10_000, "the parent turn never ended with the subagent running");
  const held = await (await lease.POST(new Request("http://localhost"), params(record.id))).json();
  assert.deepEqual(held, { success: true, running: false, awaitingInput: false, subagentsActive: true });
  // no fork while the run's sidecar is still written and its result has not come back.
  const records = index.readClaudeIndex().length;
  const fork = await send(record.id, { type: "fork", entryId: "any" });
  assert.equal(fork.status, 500);
  assert.match((await fork.json()).error, /background subagent is still running/);
  assert.equal(index.readClaudeIndex().length, records);
  // Nor an edit: ending the child would end the subagent (the refusal comes before the transcript is read).
  const edit = await send(record.id, { type: "prompt", message: "edited", entryId: "any" });
  assert.equal(edit.status, 500);
  assert.match((await edit.json()).error, /background subagent is still running/);
  assert.equal(claude.getClaudeSession(record.id), session);
  assert.ok((await (await running.GET()).json()).subagentsActiveSessionIds.includes(record.id));
  await stream;
  delete process.env.PI_TEST_CLAUDE_STUB_HOLD_MS;
  const settled = await (await lease.POST(new Request("http://localhost"), params(record.id))).json();
  assert.equal(settled.subagentsActive, false, "settled after the last follow-up turn");
  assert.ok(!(await (await running.GET()).json()).subagentsActiveSessionIds.includes(record.id));
  await session.shutdown();
});

test("a lease on an idle claude id renews nothing and spawns nothing", async () => {
  const record = await newRecord();
  const before = launches().length;
  const reply = await (await lease.POST(new Request("http://localhost"), params(record.id))).json();
  assert.equal(reply.running, false);
  await delay(100);
  assert.equal(launches().length, before);
  assert.equal(claude.getClaudeSession(record.id), undefined);
});

test("after the child exits, opening the stream starts one with --resume <same id>", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  await send(record.id, { type: "prompt", message: "first" });
  const first = claude.getClaudeSession(record.id);
  await waitFor(() => !first.isRunning(), 10_000, "the first turn never ended");
  await first.shutdown();
  const before = launches().length;
  await readEvents(record.id, (event) => event.type === "connected");
  const launch = await waitFor(() => launches()[before], 5_000, "no relaunch");
  assert.deepEqual(launch.argv.slice(-2), ["--resume", record.id]);
  await claude.shutdownAllClaudeSessions();
});

test("a pi id still takes the pi path", async () => {
  const id = randomUUID();
  const response = await events.GET(new Request(`http://localhost/api/agent/${id}/events`), params(id));
  assert.equal(response.status, 404);
  assert.deepEqual(await (await command.GET(new Request("http://localhost"), params(id))).json(), { running: false });
  assert.equal((await state.GET(new Request("http://localhost"), params(id))).status, 404);
});

// a claude prompt's images are validated before any child starts or stdin write.
const PNG = { type: "image", data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAfbLI3wAAAABJRU5ErkJggg==", mimeType: "image/png" };
const stdinLines = () => readStubLog(stubLog).filter((entry) => entry.kind === "stdin");

test("invalid claude images are refused with 400 and a reason before a child starts or stdin is written", async () => {
  const record = await newRecord();
  const oversized = "AAAA".repeat(Math.ceil((10 * 1024 * 1024 + 1) / 3));
  const before = { launches: launches().length, stdin: stdinLines().length };
  for (const [images, reason] of [
    [[{ ...PNG, data: "not base64!" }], /valid base64/],
    [Array.from({ length: 11 }, () => PNG), /at most 10/],
    [[{ ...PNG, data: oversized }], /10MB/],
    [[{ ...PNG, mimeType: "image/svg+xml" }], /PNG, JPEG, GIF and WebP/],
  ]) {
    const response = await send(record.id, { type: "prompt", message: "look", images });
    assert.equal(response.status, 400);
    const body = await response.json();
    assert.match(body.error, reason);
    assert.equal(body.code, "prompt_rejected");
    assert.equal(body.accepted, false);
  }
  assert.equal(claude.getClaudeSession(record.id), undefined, "no child for a refused prompt");
  assert.deepEqual({ launches: launches().length, stdin: stdinLines().length }, before);
});

test("a valid claude image prompt is written to stdin once with the exact base64 and media type", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  const before = stdinLines().length;
  const response = await send(record.id, { type: "prompt", message: "look", images: [{ ...PNG, mimeType: "image/webp" }] });
  assert.equal(response.status, 200);
  const session = claude.getClaudeSession(record.id);
  await waitFor(() => !session.isRunning(), 10_000, "the turn never ended");
  const written = stdinLines().slice(before).map((entry) => JSON.parse(entry.line));
  assert.match(String(written[0]?.uuid), /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.deepEqual(written, [{ type: "user", uuid: written[0].uuid, message: { role: "user", content: [
    { type: "text", text: "look" },
    { type: "image", source: { type: "base64", media_type: "image/webp", data: PNG.data } },
  ] } }]);
  await claude.shutdownAllClaudeSessions();
});

test("compact on a claude id is accepted while idle, reports isCompacting meanwhile, and a mid-turn compact changes nothing", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "hang";
  const record = await newRecord();
  await readEvents(record.id, (event) => event.type === "connected");
  const session = await waitFor(() => claude.getClaudeSession(record.id), 5_000, "no child");
  const pending = send(record.id, { type: "compact" });
  await waitFor(async () => (await (await command.GET(new Request(`http://localhost/api/agent/${record.id}`), params(record.id))).json()).state?.isCompacting === true, 5_000, "get_state never reported the compaction");
  const again = await send(record.id, { type: "compact" });
  assert.notEqual(again.status, 200, "a second compact during the first one was accepted");
  assert.equal((await send(record.id, { type: "abort" })).status, 200);
  const stopped = await pending;
  assert.equal(stopped.status, 200);
  assert.deepEqual((await stopped.json()).data, { aborted: true });
  await session.shutdown();

  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const idle = await newRecord();
  const done = await send(idle.id, { type: "compact" });
  assert.equal(done.status, 200);
  assert.deepEqual((await done.json()).data, { tokensBefore: 16739 });
  await claude.getClaudeSession(idle.id)?.shutdown();
});

// a fork at a claude user message copies the transcript on disk.
function writeTranscript(record) {
  const dir = path.join(process.env.CLAUDE_CONFIG_DIR, "projects", record.cwd.replace(/[^A-Za-z0-9]/g, "-"));
  fs.mkdirSync(dir, { recursive: true });
  const entry = (type, uuid, parentUuid, content) => ({
    type,
    uuid,
    parentUuid,
    sessionId: record.id,
    isSidechain: false,
    timestamp: new Date().toISOString(),
    ...(type === "user" ? { promptSource: "sdk" } : {}),
    message: type === "user" ? { role: "user", content } : { id: `msg_${uuid}`, role: "assistant", model: "claude-sonnet-5-5", content: [{ type: "text", text: content }] },
  });
  const file = path.join(dir, `${record.id}.jsonl`);
  fs.writeFileSync(file, `${[
    entry("user", "f-u1", null, "one"),
    entry("assistant", "f-a1", "f-u1", "answer one"),
    entry("user", "f-u2", "f-a1", "two"),
    entry("assistant", "f-a2", "f-u2", "answer two"),
  ].map((line) => JSON.stringify(line)).join("\n")}\n`);
  return file;
}

test("fork on a claude id: malformed is 400, an idle source forks from disk and spawns nothing, the copy resumes on open", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  const transcript = writeTranscript(record);
  const sourceBytes = fs.readFileSync(transcript);
  const before = launches().length;
  for (const body of [{ type: "fork" }, { type: "fork", entryId: "" }, { type: "fork", entryId: 7 }]) {
    const refused = await send(record.id, body);
    assert.equal(refused.status, 400, JSON.stringify(body));
    assert.match((await refused.json()).error, /entryId/);
  }
  const missing = await send(record.id, { type: "fork", entryId: "nope" });
  assert.equal(missing.status, 500);
  assert.match((await missing.json()).error, /Invalid entry ID for forking/);

  const forked = await send(record.id, { type: "fork", entryId: "f-u2" });
  assert.equal(forked.status, 200);
  const { data } = await forked.json();
  assert.equal(data.cancelled, false);
  assert.equal(launches().length, before, "a fork spawned a child");
  assert.equal(claude.getClaudeSession(record.id), undefined);
  assert.equal(claude.getClaudeSession(data.newSessionId), undefined);
  assert.deepEqual(fs.readFileSync(transcript), sourceBytes, "the source's transcript changed");
  const copy = path.join(path.dirname(transcript), `${data.newSessionId}.jsonl`);
  assert.deepEqual(fs.readFileSync(copy, "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line).uuid), ["f-u1", "f-a1"]);
  const forkRecord = index.getClaudeSessionRecord(data.newSessionId);
  assert.equal(forkRecord.cwd, record.cwd);
  assert.equal(forkRecord.model, record.model);

  // The copy is a claude session like any: its first open resumes its own transcript.
  await readEvents(data.newSessionId, (event) => event.type === "connected");
  const launch = await waitFor(() => launches().slice(before).find((entry) => entry.argv.includes(data.newSessionId)), 5_000, "opening the fork started no child");
  assert.deepEqual(launch.argv.slice(-2), ["--resume", data.newSessionId]);

  // Before the first message there is nothing to copy: the fork's first launch is a new conversation.
  const fresh = (await (await send(record.id, { type: "fork", entryId: "f-u1" })).json()).data;
  assert.equal(fs.existsSync(path.join(path.dirname(transcript), `${fresh.newSessionId}.jsonl`)), false);
  await readEvents(fresh.newSessionId, (event) => event.type === "connected");
  const freshLaunch = await waitFor(() => launches().find((entry) => entry.argv.includes(fresh.newSessionId)), 5_000, "opening the empty fork started no child");
  assert.deepEqual(freshLaunch.argv.slice(-2), ["--session-id", fresh.newSessionId]);
  await claude.shutdownAllClaudeSessions();
});

test("a running claude turn refuses a fork and nothing is written", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "hang";
  const record = await newRecord();
  const transcript = writeTranscript(record);
  await send(record.id, { type: "prompt", message: "long" });
  const session = claude.getClaudeSession(record.id);
  assert.ok(session?.isRunning());
  const records = index.readClaudeIndex().length;
  const files = fs.readdirSync(path.dirname(transcript)).sort();
  const refused = await send(record.id, { type: "fork", entryId: "f-u2" });
  assert.equal(refused.status, 500);
  assert.match((await refused.json()).error, /turn is running/);
  assert.equal(index.readClaudeIndex().length, records);
  assert.deepEqual(fs.readdirSync(path.dirname(transcript)).sort(), files);
  assert.equal((await send(record.id, { type: "abort" })).status, 200);
  await waitFor(() => !session.isRunning(), 5_000, "abort did not end the turn");
  // Once the turn ended, the same fork is taken from the live, idle session (no new child).
  const before = launches().length;
  const forked = await send(record.id, { type: "fork", entryId: "f-u2" });
  assert.equal(forked.status, 200);
  assert.equal(launches().length, before);
  assert.equal(claude.getClaudeSession(record.id), session);
  await session.shutdown();
});

// 「从此处编辑」, sent — a prompt naming the user message it replaces.
const stdinOf = (pid) => readStubLog(stubLog).filter((entry) => entry.kind === "stdin" && entry.pid === pid).map((entry) => entry.line);
const STREAM_FLAGS = ["-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--forward-subagent-text"];

test("an edit on a claude id ends the idle child and resumes the same id truncated before the message, then sends the prompt", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  const transcript = writeTranscript(record);
  const sourceBytes = fs.readFileSync(transcript);
  const records = index.readClaudeIndex().length;
  for (const body of [{ type: "prompt", message: "x", entryId: "" }, { type: "prompt", message: "x", entryId: 7 }]) {
    const refused = await send(record.id, body);
    assert.equal(refused.status, 400, JSON.stringify(body));
    const reply = await refused.json();
    assert.match(reply.error, /entryId/);
    assert.equal(reply.code, "prompt_rejected");
  }
  // The page's stream holds an idle child.
  await readEvents(record.id, (event) => event.type === "connected");
  const old = await waitFor(() => claude.getClaudeSession(record.id), 5_000, "opening the stream started no child");
  assert.equal(old.resumeAt, null);
  const before = launches().length;
  // A refused edit ends nothing, starts nothing and writes nothing.
  for (const [entryId, error, message] of [
    ["nope", /Invalid entry ID for editing/, "edited"],
    ["f-a1", /one of its user messages/, "edited"],
    ["f-u1", /Nothing comes before this message/, "edited"],
    ["f-u2", /needs text/, "  "],
  ]) {
    const refused = await send(record.id, { type: "prompt", message, entryId });
    assert.equal(refused.status, 500, entryId);
    const reply = await refused.json();
    assert.match(reply.error, error, entryId);
    assert.equal(reply.code, "prompt_rejected");
  }
  assert.equal(launches().length, before);
  assert.equal(claude.getClaudeSession(record.id), old, "a refused edit ended the child");
  assert.deepEqual(fs.readFileSync(transcript), sourceBytes);

  const accepted = await send(record.id, { type: "prompt", message: "two, edited", entryId: "f-u2" });
  assert.equal(accepted.status, 200, JSON.stringify(await accepted.clone().json()));
  assert.equal(old.isAlive(), false, "the idle child still runs");
  const session = claude.getClaudeSession(record.id);
  assert.ok(session && session !== old);
  assert.equal(session.sessionId, record.id, "the edit changed the session id");
  assert.equal(session.resumeAt, "f-a1");
  await waitFor(() => launches().length > before, 5_000, "the edit started no child");
  const started = launches().slice(before);
  assert.equal(started.length, 1, "the edit started more than one child");
  assert.deepEqual(started[0].argv, [...STREAM_FLAGS, "--model", "claude-sonnet-5-5", "--permission-prompts", "host", "--permission-prompt-tool", "stdio", "--resume", record.id, "--resume-session-at", "f-a1"]);
  await waitFor(() => stdinOf(started[0].pid).length > 0, 5_000, "the edited prompt never reached the child");
  const edited = stdinOf(started[0].pid).map((line) => JSON.parse(line));
  assert.match(String(edited[0]?.uuid), /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.deepEqual(edited, [{ type: "user", uuid: edited[0].uuid, message: { role: "user", content: "two, edited" } }]);
  assert.equal(index.readClaudeIndex().length, records, "the edit made a new session record");
  await waitFor(() => !session.isRunning(), 5_000, "the edited turn never ended");
  // The next child is a plain resume again: the truncation was the edit's alone.
  await session.shutdown();
  const editLaunches = launches().length;
  await send(record.id, { type: "prompt", message: "next" });
  await waitFor(() => launches().length > editLaunches, 5_000, "the next prompt started no child");
  assert.deepEqual(launches().at(-1).argv.slice(-2), ["--resume", record.id]);
  await waitFor(() => !claude.getClaudeSession(record.id)?.isRunning(), 5_000, "the next turn never ended");
  await claude.getClaudeSession(record.id)?.shutdown();
});

test("a running claude turn, or an unsettled background subagent, refuses an edit and keeps the child", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "hang";
  const record = await newRecord();
  writeTranscript(record);
  await send(record.id, { type: "prompt", message: "long" });
  const session = claude.getClaudeSession(record.id);
  assert.ok(session?.isRunning());
  const before = launches().length;
  const refused = await send(record.id, { type: "prompt", message: "edited", entryId: "f-u2" });
  assert.equal(refused.status, 500);
  assert.match((await refused.json()).error, /turn is running/);
  assert.equal(claude.getClaudeSession(record.id), session);
  assert.equal(launches().length, before);
  assert.equal((await send(record.id, { type: "abort" })).status, 200);
  await waitFor(() => !session.isRunning(), 5_000, "abort did not end the turn");
  await session.shutdown();
});

test("set_permission_mode: an unknown mode is 400 and plan is 400 while host approvals are not ready, before any child is touched", async () => {
  // r3: host approvals not ready is the global mode on deny.
  const permissionConfig = path.join(agentDir, "claude-permissions.json");
  fs.mkdirSync(agentDir, { recursive: true });
  fs.writeFileSync(permissionConfig, JSON.stringify({ version: 1, mode: "deny", push: false }));
  try {
    const record = await newRecord();
    const before = launches().length;
    const refusals = [
      [{ type: "set_permission_mode", mode: "bogus" }, /Not a claude permission mode/],
      [{ type: "set_permission_mode", mode: "bypassPermissions" }, /Not a claude permission mode/],
      [{ type: "set_permission_mode" }, /Not a claude permission mode/],
      [{ type: "set_permission_mode", mode: "plan" }, /Ask in a dialog/],
    ];
    for (const [body, pattern] of refusals) {
      const response = await send(record.id, body);
      assert.equal(response.status, 400, JSON.stringify(body));
      assert.match((await response.json()).error, pattern, JSON.stringify(body));
    }
    assert.equal(launches().length, before, "a refused switch spawned a child");
    assert.equal(claude.getClaudeSession(record.id), undefined);

    // A live child: the refusals reach neither it nor the index.
    process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
    const session = claude.startClaudeSession(index.getClaudeSessionRecord(record.id));
    await waitFor(() => launches().some((entry) => entry.pid === session.pid), 5_000, "no launch");
    for (const [body] of refusals) assert.equal((await send(record.id, body)).status, 400, JSON.stringify(body));
    const stdin = readStubLog(stubLog).filter((entry) => entry.kind === "stdin" && entry.pid === session.pid);
    assert.deepEqual(stdin, [], "a refused switch reached claude's stdin");
    assert.equal(index.getClaudeSessionRecord(record.id).launchPermissionMode, "default");
    await session.shutdown();

    // A valid mode with no live child only writes the index.
    const idle = await send(record.id, { type: "set_permission_mode", mode: "acceptEdits" });
    assert.equal(idle.status, 200);
    assert.deepEqual((await idle.json()).data, { launchPermissionMode: "acceptEdits", pending: true });
    assert.equal(index.getClaudeSessionRecord(record.id).launchPermissionMode, "acceptEdits");
    assert.equal(claude.getClaudeSession(record.id), undefined, "the switch spawned a child");
  } finally {
    fs.rmSync(permissionConfig, { force: true });
  }
});

test("set_permission_mode plan is 200 for a session whose permission prompts come to Pi Web and 400, naming the reason, for one on deny ( r3)", async () => {
  // No claude-permissions.json: the default, ask.
  fs.rmSync(path.join(agentDir, "claude-permissions.json"), { force: true });
  const before = launches().length;
  const asked = await newRecord();
  const idle = await send(asked.id, { type: "set_permission_mode", mode: "plan" });
  assert.equal(idle.status, 200);
  assert.deepEqual((await idle.json()).data, { launchPermissionMode: "plan", pending: true });
  assert.equal(index.getClaudeSessionRecord(asked.id).launchPermissionMode, "plan");

  const denied = await newRecord();
  await index.upsertClaudeSessionRecord({ ...index.getClaudeSessionRecord(denied.id), permissionMode: "deny" });
  const refused = await send(denied.id, { type: "set_permission_mode", mode: "plan" });
  assert.equal(refused.status, 400);
  assert.match((await refused.json()).error, /Ask in a dialog/);
  assert.equal(index.getClaudeSessionRecord(denied.id).launchPermissionMode, "default");
  assert.equal(launches().length, before, "a switch with no live child spawned one");

  // A live child launched with ask takes plan through claude's set_permission_mode.
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const live = await newRecord();
  const session = claude.startClaudeSession(index.getClaudeSessionRecord(live.id));
  try {
    await waitFor(() => launches().some((entry) => entry.pid === session.pid), 5_000, "no launch");
    const accepted = await send(live.id, { type: "set_permission_mode", mode: "plan" });
    assert.equal(accepted.status, 200);
    assert.deepEqual((await accepted.json()).data, { launchPermissionMode: "plan" });
    const requests = readStubLog(stubLog)
      .filter((entry) => entry.kind === "stdin" && entry.pid === session.pid)
      .map((entry) => JSON.parse(entry.line))
      .filter((line) => line.type === "control_request");
    assert.deepEqual(requests.map((line) => line.request), [{ subtype: "set_permission_mode", mode: "plan" }]);
    assert.equal(index.getClaudeSessionRecord(live.id).launchPermissionMode, "plan");
  } finally {
    await session.shutdown();
  }
});

test("a pending claude permission: lease, running and the session list report it awaiting; extension_ui_response answers it; with no live child the response is refused and spawns nothing", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "permission";
  // Ask (the default) with the push switch off: no notifier is reached from a unit test.
  const permissionConfig = path.join(agentDir, "claude-permissions.json");
  fs.mkdirSync(agentDir, { recursive: true });
  fs.writeFileSync(permissionConfig, JSON.stringify({ version: 1, mode: "ask", push: false }));
  const sessions = await jiti.import("../sessions/route.ts");
  const awaitingIn = async (reply) => (await reply.json()).awaitingSessionIds.includes(record.id);
  const record = await newRecord();
  try {
    const stream = readEvents(record.id, (event) => event.type === "extension_ui_request" && event.method === "select");
    await waitFor(() => claude.getClaudeSession(record.id), 5_000, "opening the stream started no child");
    assert.equal((await send(record.id, { type: "prompt", message: "ls -la" })).status, 200);
    const dialog = (await stream).find((event) => event.type === "extension_ui_request" && event.method === "select");
    const session = claude.getClaudeSession(record.id);

    const held = await (await lease.POST(new Request("http://localhost"), params(record.id))).json();
    assert.deepEqual(held, { success: true, running: true, awaitingInput: true, subagentsActive: false });
    assert.equal(await awaitingIn(await running.GET()), true, "running: not awaiting");
    assert.equal(await awaitingIn(await sessions.GET(new Request("http://localhost/api/sessions"))), true, "session list: not awaiting");

    const answered = await send(record.id, { type: "extension_ui_response", id: dialog.id, value: "Allow once" });
    assert.equal(answered.status, 200);
    assert.deepEqual(await answered.json(), { success: true, data: null });
    await waitFor(() => !session.isRunning(), 5_000, "the allowed turn never ended");
    const answers = stdinOf(session.pid).map((line) => JSON.parse(line)).filter((line) => line.type === "control_response");
    assert.equal(answers.length, 1);
    assert.equal(answers[0].response.request_id, "perm-1");
    assert.equal(answers[0].response.response.behavior, "allow");
    const free = await (await lease.POST(new Request("http://localhost"), params(record.id))).json();
    assert.equal(free.awaitingInput, false);
    assert.equal(await awaitingIn(await running.GET()), false);
    assert.equal(await awaitingIn(await sessions.GET(new Request("http://localhost/api/sessions"))), false);

    // No live child: there is no dialog to answer, so the response is refused and starts none.
    await session.shutdown();
    const before = launches().length;
    const refused = await send(record.id, { type: "extension_ui_response", id: dialog.id, value: "Allow once" });
    assert.equal(refused.status, 400);
    assert.match((await refused.json()).error, /no live claude child/);
    await delay(150);
    assert.equal(launches().length, before, "the refused response spawned a child");
    assert.equal(claude.getClaudeSession(record.id), undefined);
  } finally {
    fs.rmSync(permissionConfig, { force: true });
    await claude.getClaudeSession(record.id)?.shutdown();
  }
});
