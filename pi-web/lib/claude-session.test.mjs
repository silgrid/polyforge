import "#test-env";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { createJiti } from "jiti";
import { readStubLog } from "./__fixtures__/claude-stub-log.mjs";

// A scratch agent dir (index), an inherited claude config dir, a scratch HOME and a
// PATH whose `claude` is the stub: nothing here touches the operator's ~/.claude.
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-session-"));
const agentDir = path.join(scratch, "agent");
const configDir = path.join(scratch, "claude-config");
const homeDir = path.join(scratch, "home");
const binDir = path.join(scratch, "bin");
const workDir = path.join(scratch, "work");
const stubLog = path.join(scratch, "stub.jsonl");
fs.mkdirSync(agentDir);
fs.mkdirSync(binDir);
fs.mkdirSync(workDir);
fs.mkdirSync(homeDir);
fs.copyFileSync(new URL("./__fixtures__/claude-stub/claude.mjs", import.meta.url), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
// The stub reads the recordings next to its own file.
fs.symlinkSync(fileURLToPathSafe(new URL("./__fixtures__/claude-2.1.312", import.meta.url)), path.join(scratch, "claude-2.1.312"));
fs.symlinkSync(fileURLToPathSafe(new URL("./__fixtures__/claude-2.1.322", import.meta.url)), path.join(scratch, "claude-2.1.322"));
function fileURLToPathSafe(url) {
  return decodeURIComponent(url.pathname);
}
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.HOME = homeDir;
process.env.CLAUDE_CONFIG_DIR = configDir;
process.env.PATH = `${binDir}${path.delimiter}${process.env.PATH}`;
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_WEB_PASSWORD = "not-for-the-child";
process.env.CLAUDECODE = "1";
process.env.ANTHROPIC_API_KEY = "inherited-by-the-child";
process.env.CLAUDE_INHERITED_SENTINEL = "inherited-by-the-child";
process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";

// the push notifier the session reaches, recording instead of sending.
const pushes = [];
globalThis.__piWebPushNotifier = Promise.resolve({
  getVapidPublicKey: () => "",
  addSubscription: () => {},
  notifySessionComplete: async () => {},
  notifyClaudePermissionPending: async (...args) => {
    pushes.push(args);
  },
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
const launches = () => records().filter((entry) => entry.kind === "launch");
const alive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
};

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

function collect(session) {
  const events = [];
  session.onEvent((event) => events.push(event));
  return events;
}

test("findClaudeBinary resolves claude from PATH only", () => {
  assert.equal(claude.findClaudeBinary(), path.join(binDir, "claude"));
  assert.equal(claude.findClaudeBinary(path.join(scratch, "nowhere")), null);
  assert.equal(claude.findClaudeBinary(""), null);
});

test("a session launches with the reduced argv and its full model name, the inherited env minus PI_WEB_PASSWORD/CLAUDECODE, and creates no config dir", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  const before = launches().length;
  const session = claude.startClaudeSession(record);
  const launch = await waitFor(() => launches()[before], 5_000, "the stub recorded no launch");
  assert.deepEqual(launch.argv, [
    "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--forward-subagent-text",
    "--model", "claude-sonnet-5-5", "--permission-prompts", "host", "--permission-prompt-tool", "stdio", "--session-id", record.id,
  ]);
  assert.ok(!launch.argv.includes("--cwd"), "the folder is the spawn's cwd, not a flag");
  assert.equal(fs.realpathSync(launch.cwd), fs.realpathSync(workDir));
  // CLAUDE_CONFIG_DIR is the child's only because pi-web inherited it (this test set it).
  assert.equal(launch.configDir, configDir);
  for (const name of ["ANTHROPIC_API_KEY", "CLAUDE_INHERITED_SENTINEL", "CLAUDE_CONFIG_DIR"]) assert.ok(launch.envKeys.includes(name), name);
  for (const name of ["PI_WEB_PASSWORD", "CLAUDECODE"]) assert.ok(!launch.envKeys.includes(name), name);
  assert.equal(fs.existsSync(path.join(agentDir, "claude")), false, "no dedicated config dir is created");
  assert.equal(fs.existsSync(path.join(homeDir, ".pi", "agent", "claude")), false);
  assert.equal(claude.startClaudeSession(record), session, "a live child is reused");
  await session.shutdown();
});

test("a legacy #219 index record launches with its alias, which claude takes verbatim, and no preset flag", async () => {
  const record = await newRecord();
  // As #219 wrote it: alias + preset, no model.
  const file = index.claudeIndexPath();
  const parsed = JSON.parse(fs.readFileSync(file, "utf8"));
  parsed.sessions = parsed.sessions.map((entry) => (entry.id === record.id ? { ...entry, model: undefined, modelAlias: "haiku", preset: "writable" } : entry));
  fs.writeFileSync(file, JSON.stringify(parsed));
  const legacy = index.getClaudeSessionRecord(record.id);
  const before = launches().length;
  const session = claude.startClaudeSession(legacy);
  const launch = await waitFor(() => launches()[before], 5_000, "the stub recorded no launch");
  assert.equal(launch.argv[launch.argv.indexOf("--model") + 1], "haiku");
  for (const flag of ["--permission-mode", "--tools", "--allowedTools", "--bare"]) assert.ok(!launch.argv.includes(flag), flag);
  assert.deepEqual(await session.send({ type: "get_tools" }), [], "pi-web lists no claude tools");
  await session.shutdown();
});

test("a prompt reaches stdin and the reply streams through the adapter; usage comes from the result", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  assert.equal(await session.send({ type: "prompt", message: "read the notes" }), null);
  assert.equal(session.isRunning(), true);
  // A message sent meanwhile is a steer now (AC4: lib/claude-steer-395.test.mjs).
  await waitFor(() => events.some((event) => event.type === "prompt_done"), 10_000, "the turn never ended");
  const stdin = records().filter((entry) => entry.kind === "stdin" && entry.pid === session.pid).map((entry) => JSON.parse(entry.line));
  // Each user line carries a fresh uuid, so claude reports its lifecycle.
  assert.match(String(stdin[0]?.uuid), /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.deepEqual(stdin, [{ type: "user", uuid: stdin[0].uuid, message: { role: "user", content: "read the notes" } }]);
  const types = events.map((event) => event.type);
  assert.equal(types[0], "agent_start");
  assert.ok(types.includes("message_update"));
  assert.deepEqual(types.slice(-3), ["agent_end", "agent_settled", "prompt_done"]);
  const ended = events.filter((event) => event.type === "message_end").map((event) => event.message.role);
  assert.deepEqual(ended, ["user", "assistant", "toolResult", "assistant"]);
  assert.equal(session.isRunning(), false);
  await waitFor(() => index.getClaudeSessionRecord(record.id).usage.output === 283, 5_000, "usage was not recorded");
  const saved = index.getClaudeSessionRecord(record.id);
  assert.equal(saved.firstMessage, "read the notes");
  const stats = await session.send({ type: "get_session_stats" });
  assert.equal(stats.tokens.output, 283);
  assert.equal(stats.toolCalls, 1);
  const state = await session.send({ type: "get_state" });
  assert.deepEqual(state.model, { id: "claude-sonnet-5-5", provider: "claude" });
  // fork is answered (an id no user message of the transcript carries is refused); navigate_tree is not.
  await assert.rejects(session.send({ type: "fork", entryId: "x" }), /Invalid entry ID for forking/);
  await assert.rejects(session.send({ type: "navigate_tree", targetId: "x" }), /not available for claude sessions/);
  await session.shutdown();
});

// images travel to the child in the one stdin line, unchanged.
const PNG = { type: "image", data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAfbLI3wAAAABJRU5ErkJggg==", mimeType: "image/png" };

test("a prompt's images reach stdin as base64 image blocks in one line, and the live user message carries them", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  assert.equal(await session.send({ type: "prompt", message: "what is this?", images: [PNG] }), null);
  await waitFor(() => events.some((event) => event.type === "prompt_done"), 10_000, "the turn never ended");
  const stdin = records().filter((entry) => entry.kind === "stdin" && entry.pid === session.pid).map((entry) => JSON.parse(entry.line));
  assert.match(String(stdin[0]?.uuid), /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.deepEqual(stdin, [{ type: "user", uuid: stdin[0].uuid, message: { role: "user", content: [
    { type: "text", text: "what is this?" },
    { type: "image", source: { type: "base64", media_type: "image/png", data: PNG.data } },
  ] } }]);
  const user = events.find((event) => event.type === "message_end" && event.message.role === "user").message;
  assert.deepEqual(user.content, [{ type: "text", text: "what is this?" }, { type: "image", data: PNG.data, mimeType: "image/png" }]);
  await waitFor(() => index.getClaudeSessionRecord(record.id).firstMessage === "what is this?", 5_000, "firstMessage was not recorded");
  await session.shutdown();
});

test("an image-only prompt is a turn: images alone on stdin, firstMessage [Image]; no text and no image is still refused", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  await assert.rejects(session.send({ type: "prompt", message: "  " }), /A claude prompt needs text/);
  await assert.rejects(session.send({ type: "prompt", message: "", images: [] }), /A claude prompt needs text/);
  assert.equal(await session.send({ type: "prompt", message: "", images: [PNG] }), null);
  await waitFor(() => events.some((event) => event.type === "prompt_done"), 10_000, "the turn never ended");
  const stdin = records().filter((entry) => entry.kind === "stdin" && entry.pid === session.pid).map((entry) => JSON.parse(entry.line));
  assert.match(String(stdin[0]?.uuid), /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.deepEqual(stdin, [{ type: "user", uuid: stdin[0].uuid, message: { role: "user", content: [
    { type: "image", source: { type: "base64", media_type: "image/png", data: PNG.data } },
  ] } }]);
  await waitFor(() => index.getClaudeSessionRecord(record.id).firstMessage === "[Image]", 5_000, "firstMessage was not [Image]");
  await session.shutdown();
});

test("Stop sends the interrupt, keeps the partial reply and leaves the session usable", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "hang";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  await session.send({ type: "prompt", message: "long" });
  await waitFor(() => events.some((event) => event.type === "message_update"), 5_000, "nothing streamed");
  await session.send({ type: "abort" });
  await waitFor(() => events.some((event) => event.type === "prompt_done"), 5_000, "the stop did not end the turn");
  const interrupt = records().filter((entry) => entry.kind === "stdin" && entry.pid === session.pid).map((entry) => JSON.parse(entry.line)).at(-1);
  assert.equal(interrupt.type, "control_request");
  assert.equal(interrupt.request.subtype, "interrupt");
  assert.ok(events.some((event) => event.type === "message_end" && event.message.content?.[0]?.text === "partial answ"));
  assert.ok(events.some((event) => event.type === "prompt_done" && event.aborted === true));
  assert.ok(!events.some((event) => event.type === "prompt_error"), "a stop is not an error");
  assert.equal(session.isAlive(), true);
  await session.send({ type: "prompt", message: "next" });
  assert.equal(session.isRunning(), true, "the next send starts a new turn");
  await session.shutdown();
});

test("an exit mid-turn ends the turn with claude's error and the config dir", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "exit";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  await session.send({ type: "prompt", message: "x" });
  await waitFor(() => events.some((event) => event.type === "session_shutdown"), 5_000, "the exit was not noticed");
  const error = events.find((event) => event.type === "prompt_error");
  assert.match(error.errorMessage, /code 3/);
  assert.ok(error.errorMessage.includes(configDir));
  assert.equal(claude.getClaudeSession(record.id), undefined);
});

test("idle expiry kills the whole process group, and the next start resumes the same id", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  process.env.PI_TEST_CLAUDE_STUB_GRANDCHILD = "1";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  await session.send({ type: "prompt", message: "first" });
  await waitFor(() => events.some((event) => event.type === "prompt_done"), 10_000, "the turn never ended");
  const grandchild = await waitFor(() => records().find((entry) => entry.kind === "grandchild" && entry.pid === session.pid), 5_000, "no grandchild");
  delete process.env.PI_TEST_CLAUDE_STUB_GRANDCHILD;
  process.env.PI_WEB_IDLE_TIMEOUT_MS = "200";
  session.renewIdleTimer();
  await waitFor(() => !session.isAlive(), 5_000, "the idle timer did not end the child");
  process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";
  await waitFor(() => !alive(session.pid) && !alive(grandchild.grandchildPid), 5_000, "a process of the group survived");
  assert.ok(events.some((event) => event.type === "session_shutdown"));

  const before = launches().length;
  const resumed = claude.startClaudeSession(record);
  assert.notEqual(resumed, session);
  const launch = await waitFor(() => launches()[before], 5_000, "no relaunch");
  assert.deepEqual(launch.argv.slice(-2), ["--resume", record.id]);
  assert.ok(!launch.argv.includes("--session-id"));
  await resumed.shutdown();
});

test("PI_WEB_IDLE_TIMEOUT_MS=0 keeps the child", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  session.renewIdleTimer();
  await delay(300);
  assert.equal(session.isAlive(), true);
  assert.deepEqual(claude.getRunningClaudeSessionIds().includes(record.id), false);
  await claude.shutdownAllClaudeSessions();
  assert.equal(session.isAlive(), false);
});

const subagentText = (event) => event.type === "claude_subagent" && event.run.entries.some((entry) => entry.message.role === "assistant" && entry.message.content.some((block) => block.type === "text"));
const count = (events, type) => events.filter((event) => event.type === type).length;

test("a background subagent keeps the session unsettled until claude's follow-up turn ends, which ends the turn once", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "subagent";
  process.env.PI_TEST_CLAUDE_STUB_HOLD_MS = "400";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  await session.send({ type: "prompt", message: "launch it" });
  await waitFor(() => events.some(subagentText), 10_000, "the subagent's text never arrived");
  assert.equal(session.hasUnsettledSubagents(), true, "the running background subagent is unsettled");
  assert.ok(claude.getClaudeSessionIdsWithUnsettledSubagents().includes(record.id));
  await waitFor(() => events.some((event) => event.type === "prompt_done"), 10_000, "the turn never ended");
  delete process.env.PI_TEST_CLAUDE_STUB_HOLD_MS;
  assert.equal(session.hasUnsettledSubagents(), false);
  assert.equal(count(events, "agent_start"), 1, "the follow-up came while the turn ran: no second start");
  // in 2.1.312's order (both results at the end) the turn now ends at the
  // first result, claude's own signal; the follow-up's content came before it, so this is
  // still the one completion. The message below predates that and says "last".
  assert.equal(count(events, "prompt_done"), 1, "one completion, after the last follow-up result");
  const done = events.find((event) => event.type === "agent_settled");
  assert.equal(done.subagentsActive, undefined, "settled: nothing holds the announcement");
  const anchor = events.find((event) => event.type === "message_end" && event.message.role === "custom");
  assert.equal(anchor.message.customType, "claude:task-notification");
  const reply = events.findIndex((event) => event.type === "message_end" && event.message.role === "assistant" && /^The subagent finished/.test(event.message.content[0]?.text ?? ""));
  assert.ok(events.indexOf(anchor) < reply, "the follow-up reply comes after its anchor");
  assert.ok(!events.some((event) => /^(message_|tool_execution_)/.test(event.type) && JSON.stringify(event).includes("toolu_012N7brUKegpGiA36sdi1ApH")), "the subagent's Read is no main event");
  await session.shutdown();
});

test("a parent turn answered while its subagent runs reports subagentsActive; claude's follow-up is its own turn and the last one announces", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "subagent-early";
  process.env.PI_TEST_CLAUDE_STUB_HOLD_MS = "600";
  process.env.PI_WEB_IDLE_TIMEOUT_MS = "100";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  await session.send({ type: "prompt", message: "launch it" });
  await waitFor(() => events.some((event) => event.type === "prompt_done"), 10_000, "the parent turn never ended");
  const first = events.find((event) => event.type === "prompt_done");
  assert.equal(first.subagentsActive, true, "the parent's end holds the announcement");
  assert.equal(session.isRunning(), false);
  assert.equal(session.hasUnsettledSubagents(), true);
  await delay(300);
  assert.equal(session.isAlive(), true, "no idle reap while the subagent is unsettled");
  await waitFor(() => count(events, "prompt_done") === 2, 10_000, "claude's follow-up turn never ended");
  delete process.env.PI_TEST_CLAUDE_STUB_HOLD_MS;
  const types = events.map((event) => event.type);
  assert.equal(count(events, "agent_start"), 2, "the follow-up turn starts on its own, without a prompt");
  const secondStart = types.lastIndexOf("agent_start");
  assert.equal(events[secondStart + 1].type, "message_start");
  assert.equal(events[secondStart + 1].message.customType, "claude:task-notification", "it opens with the hidden anchor");
  assert.ok(!events.slice(secondStart).some((event) => event.type === "message_end" && event.message.role === "user"), "no user bubble starts it");
  const last = events.filter((event) => event.type === "prompt_done").at(-1);
  assert.equal(last.subagentsActive, undefined, "the last follow-up end announces");
  await waitFor(() => !session.isAlive(), 5_000, "once settled the idle timer ends the child again");
  process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";
});

test("a session without Agent calls reports no unsettled subagent", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  await session.send({ type: "prompt", message: "plain" });
  await waitFor(() => events.some((event) => event.type === "prompt_done"), 10_000, "the turn never ended");
  assert.equal(session.hasUnsettledSubagents(), false);
  assert.equal(events.find((event) => event.type === "prompt_done").subagentsActive, undefined);
  assert.ok(!events.some((event) => event.type === "claude_subagent"));
  await session.shutdown();
});

test("history and stats read claude's transcript; usage is the index's result totals", () => {
  const record = { id: "5b203fcd-56c9-4d0c-b700-749b82c41afb", usage: { input: 6, output: 283, cacheRead: 840, cacheWrite: 1102, cost: 0.00028825 } };
  const dir = path.join(configDir, "projects", "-x");
  fs.mkdirSync(dir, { recursive: true });
  fs.copyFileSync(new URL("./__fixtures__/claude-2.1.312/transcript.jsonl", import.meta.url), path.join(dir, `${record.id}.jsonl`));
  const history = claude.readClaudeHistory(record.id);
  assert.equal(history.messages.length, 4);
  const stats = claude.claudeSessionStats(record, history.messages);
  assert.deepEqual(stats.tokens, { input: 6, output: 283, cacheRead: 840, cacheWrite: 1102, total: 2231 });
  assert.equal(stats.cost, 0.00028825);
  assert.equal(stats.toolCalls, 1);
  assert.equal(stats.userMessages, 1);
});

test("history carries the subagent runs rebuilt from the sidecars; an unreadable sidecar degrades silently", () => {
  const id = "44444444-4444-4444-8444-444444444444";
  const dir = path.join(configDir, "projects", "-sub");
  const sidecars = path.join(dir, id, "subagents");
  fs.mkdirSync(sidecars, { recursive: true });
  const recording = new URL("./__fixtures__/claude-2.1.312/subagent/", import.meta.url);
  fs.copyFileSync(new URL("transcript.jsonl", recording), path.join(dir, `${id}.jsonl`));
  for (const name of ["agent-a9fb2ff3aa8e8ff40.jsonl", "agent-a9fb2ff3aa8e8ff40.meta.json"]) {
    fs.copyFileSync(new URL(`subagents/${name}`, recording), path.join(sidecars, name));
  }
  const history = claude.readClaudeHistory(id);
  assert.equal(history.subagents.length, 1);
  assert.equal(history.subagents[0].entries.length, 3);
  assert.equal(history.subagents[0].agentType, "general-purpose");
  fs.rmSync(path.join(sidecars, "agent-a9fb2ff3aa8e8ff40.jsonl"));
  fs.mkdirSync(path.join(sidecars, "agent-a9fb2ff3aa8e8ff40.jsonl"));
  const degraded = claude.readClaudeHistory(id);
  assert.equal(degraded.subagents.length, 1, "the run is still there");
  assert.deepEqual(degraded.subagents[0].entries, [], "without its entries");
  assert.equal(degraded.messages.length, history.messages.length);
});

test("without an inherited CLAUDE_CONFIG_DIR the child gets none and its transcript is read from ~/.claude", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  delete process.env.CLAUDE_CONFIG_DIR;
  try {
    const record = await newRecord();
    const before = launches().length;
    const session = claude.startClaudeSession(record);
    const events = collect(session);
    const launch = await waitFor(() => launches()[before], 5_000, "the stub recorded no launch");
    assert.ok(!launch.envKeys.includes("CLAUDE_CONFIG_DIR"), "no CLAUDE_CONFIG_DIR is injected");
    await session.send({ type: "prompt", message: "home dir" });
    await waitFor(() => events.some((event) => event.type === "prompt_done"), 10_000, "the turn never ended");
    const history = claude.readClaudeHistory(record.id);
    assert.ok(history.file?.startsWith(path.join(homeDir, ".claude", "projects")), String(history.file));
    assert.ok(history.messages.length > 0);
    await session.shutdown();
  } finally {
    process.env.CLAUDE_CONFIG_DIR = configDir;
  }
});

test("every spawn carries the record's effort; a default record has none; state reports the index's model and effort", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const plain = await newRecord();
  let before = launches().length;
  const first = claude.startClaudeSession(plain);
  assert.ok(!(await waitFor(() => launches()[before], 5_000, "no launch")).argv.includes("--effort"));
  assert.equal((await first.send({ type: "get_state" })).claudeEffort, "default");
  await first.shutdown();

  const record = await newRecord({ effort: "xhigh" });
  before = launches().length;
  const session = claude.startClaudeSession(record);
  const launch = await waitFor(() => launches()[before], 5_000, "no launch");
  assert.equal(launch.argv[launch.argv.indexOf("--effort") + 1], "xhigh");
  await index.updateClaudeSessionRecord(record.id, (entry) => ({ ...entry, model: "claude-haiku-5-5", effort: "low" }));
  const state = await session.send({ type: "get_state" });
  assert.deepEqual(state.model, { id: "claude-haiku-5-5", provider: "claude" });
  assert.equal(state.claudeEffort, "low");
  assert.deepEqual(claude.claudeSessionInfo(index.getClaudeSessionRecord(record.id)).claude, { model: "claude-haiku-5-5", effort: "low" });
  await session.shutdown();
  before = launches().length;
  const resumed = claude.startClaudeSession(index.getClaudeSessionRecord(record.id));
  const again = await waitFor(() => launches()[before], 5_000, "no resume launch");
  assert.equal(again.argv[again.argv.indexOf("--effort") + 1], "low");
  assert.equal(again.argv[again.argv.indexOf("--model") + 1], "claude-haiku-5-5");
  await resumed.shutdown();
});

const stdinOf = (pid) => records().filter((entry) => entry.kind === "stdin" && entry.pid === pid).map((entry) => JSON.parse(entry.line));

test("a live model switch is claude's set_model on the same child; the index records it and the next spawn carries it", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  await waitFor(() => launches().some((entry) => entry.pid === session.pid), 5_000, "no launch");
  const before = launches().length;
  assert.deepEqual(await session.send({ type: "set_model", model: "claude-haiku-5-5" }), { model: "claude-haiku-5-5" });
  assert.equal(launches().length, before, "no new launch");
  assert.ok(session.isAlive());
  const request = stdinOf(session.pid).find((line) => line.type === "control_request");
  assert.deepEqual(request.request, { subtype: "set_model", model: "claude-haiku-5-5" });
  assert.equal(index.getClaudeSessionRecord(record.id).model, "claude-haiku-5-5");
  await session.shutdown();
  const next = claude.startClaudeSession(index.getClaudeSessionRecord(record.id));
  const launch = await waitFor(() => launches()[before], 5_000, "no relaunch");
  assert.equal(launch.argv[launch.argv.indexOf("--model") + 1], "claude-haiku-5-5");
  await next.shutdown();
});

test("a switch claude refuses (the recorded check_failed) keeps the old model and the child", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "switch";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  // The model-switch recording answers its first request with claude's refusal.
  const refused = await session.send({ type: "set_model", model: "claude-opus-5-5" });
  assert.equal(refused.rejected, true);
  assert.equal(refused.model, "claude-sonnet-5-5");
  assert.equal(refused.errorCode, "check_failed");
  assert.match(refused.error, /model not changed/);
  assert.equal(index.getClaudeSessionRecord(record.id).model, "claude-sonnet-5-5");
  assert.ok(session.isAlive());
  assert.deepEqual(await session.send({ type: "set_model", model: "claude-opus-5-5" }), { model: "claude-sonnet-5-5" }, "the recorded success names the model claude reports");
  await session.shutdown();
});

test("a child that exits while a switch waits records it; the next spawn carries the new --model and --effort", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "hang";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  await waitFor(() => launches().some((entry) => entry.pid === session.pid), 5_000, "no launch");
  const switching = session.send({ type: "set_model", model: "claude-haiku-5-5" });
  await waitFor(() => stdinOf(session.pid).some((line) => line.type === "control_request"), 5_000, "set_model never reached stdin");
  process.kill(session.pid, "SIGKILL");
  assert.deepEqual(await switching, { model: "claude-haiku-5-5", pending: true });
  assert.equal(index.getClaudeSessionRecord(record.id).model, "claude-haiku-5-5");

  const second = claude.startClaudeSession(index.getClaudeSessionRecord(record.id));
  await waitFor(() => launches().some((entry) => entry.pid === second.pid), 5_000, "no relaunch");
  const effort = second.send({ type: "set_effort", effort: "high" });
  await waitFor(() => stdinOf(second.pid).some((line) => line.type === "control_request"), 5_000, "set_effort never reached stdin");
  process.kill(second.pid, "SIGKILL");
  assert.deepEqual(await effort, { effort: "high", pending: true });

  const before = launches().length;
  const next = claude.startClaudeSession(index.getClaudeSessionRecord(record.id));
  const launch = await waitFor(() => launches()[before], 5_000, "no relaunch");
  assert.equal(launch.argv[launch.argv.indexOf("--model") + 1], "claude-haiku-5-5");
  assert.equal(launch.argv[launch.argv.indexOf("--effort") + 1], "high");
  await next.shutdown();
});

test("an effort change: a level goes to the live child as apply_flag_settings; the default ends the idle child; pre-#272 reads default", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  assert.equal(index.getClaudeSessionRecord(record.id).effort, "default");
  const session = claude.startClaudeSession(record);
  await waitFor(() => launches().some((entry) => entry.pid === session.pid), 5_000, "no launch");
  const before = launches().length;
  assert.deepEqual(await session.send({ type: "set_effort", effort: "max" }), { effort: "max" });
  assert.deepEqual(stdinOf(session.pid).find((line) => line.type === "control_request").request, { subtype: "apply_flag_settings", settings: { effortLevel: "max" } });
  assert.ok(session.isAlive());
  assert.equal(launches().length, before);
  assert.equal(index.getClaudeSessionRecord(record.id).effort, "max");
  assert.deepEqual(await session.send({ type: "set_effort", effort: "default" }), { effort: "default", pending: true });
  assert.equal(session.isAlive(), false, "the default entry ends the idle child");
  const next = claude.startClaudeSession(index.getClaudeSessionRecord(record.id));
  const launch = await waitFor(() => launches()[before], 5_000, "no relaunch");
  assert.ok(!launch.argv.includes("--effort"));
  await next.shutdown();
});

test("a running turn refuses a switch and changes nothing", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "hang";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  await session.send({ type: "prompt", message: "long" });
  await assert.rejects(session.send({ type: "set_model", model: "claude-haiku-5-5" }), /turn is running/);
  await assert.rejects(session.send({ type: "set_effort", effort: "low" }), /turn is running/);
  assert.ok(!stdinOf(session.pid).some((line) => line.type === "control_request"));
  assert.equal(index.getClaudeSessionRecord(record.id).model, "claude-sonnet-5-5");
  await session.shutdown();
});

test("compact sends /compact once as a guarded turn and resolves with claude's pre-compaction tokens; the next prompt is taken", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  const outcome = session.send({ type: "compact" });
  assert.equal((await session.send({ type: "get_state" })).isCompacting, true);
  await assert.rejects(session.send({ type: "prompt", message: "meanwhile" }), /compaction is running/);
  await assert.rejects(session.send({ type: "set_model", model: "claude-haiku-5-5" }), /turn is running/);
  assert.deepEqual(await outcome, { tokensBefore: 16739 });
  const lines = stdinOf(session.pid).filter((line) => line.type === "user");
  assert.deepEqual(lines, [{ type: "user", message: { role: "user", content: "/compact" } }]);
  const types = events.map((event) => event.type);
  assert.deepEqual(types.filter((type) => type.startsWith("compaction")), ["compaction_start", "compaction_end"]);
  assert.deepEqual(events.find((event) => event.type === "compaction_end").result, { tokensBefore: 16739 });
  assert.ok(!types.includes("agent_start") && !types.includes("message_end"), "a compaction is no reply");
  assert.equal((await session.send({ type: "get_state" })).isCompacting, false);
  assert.ok(claude.readClaudeHistory(record.id).messages.some((message) => message.customType === "compaction"));
  await session.send({ type: "prompt", message: "after" });
  await waitFor(() => !session.isRunning(), 5_000, "the prompt after the compaction did not finish");
  await session.shutdown();
});

test("Stop during a compaction ends it as stopped without a marker; a failed one rejects with claude's error", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "hang";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  const outcome = session.send({ type: "compact" });
  await waitFor(() => stdinOf(session.pid).length > 0, 5_000, "/compact never reached stdin");
  await session.send({ type: "abort" });
  assert.deepEqual(await outcome, { aborted: true });
  assert.equal(events.find((event) => event.type === "compaction_end").aborted, true);
  assert.equal(claude.readClaudeHistory(record.id).messages.filter((message) => message.customType === "compaction").length, 0);
  assert.ok(session.isAlive());
  await session.shutdown();

  process.env.PI_TEST_CLAUDE_STUB_MODE = "exit";
  const failing = claude.startClaudeSession(await newRecord());
  await assert.rejects(failing.send({ type: "compact" }), /claude config dir/);
});

const INSTRUCTION = "Use Simplified Chinese (简体中文) for replies, narration and reports. Keep code, code comments, commit messages, files you write, technical terms and identifiers as they are, or as the project's conventions require. Thinking in Chinese is best effort only.";

test("a 中文 record's every spawn carries --append-system-prompt with the fixed text; an English or pre-feature record's carries none", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const zh = await newRecord({ agentLanguage: "zh-CN" });
  let before = launches().length;
  const first = claude.startClaudeSession(index.getClaudeSessionRecord(zh.id));
  const launch = await waitFor(() => launches()[before], 5_000, "no launch");
  assert.equal(launch.argv[launch.argv.indexOf("--append-system-prompt") + 1], INSTRUCTION);
  await first.shutdown();
  before = launches().length;
  const resumed = claude.startClaudeSession(index.getClaudeSessionRecord(zh.id));
  const again = await waitFor(() => launches()[before], 5_000, "no resume launch");
  assert.equal(again.argv[again.argv.indexOf("--append-system-prompt") + 1], INSTRUCTION);
  await resumed.shutdown();

  for (const extra of [{ agentLanguage: "en" }, {}]) {
    const record = await newRecord(extra);
    before = launches().length;
    const session = claude.startClaudeSession(index.getClaudeSessionRecord(record.id));
    const english = await waitFor(() => launches()[before], 5_000, "no launch");
    assert.ok(!english.argv.includes("--append-system-prompt"));
    assert.ok(!english.argv.includes(INSTRUCTION));
    await session.shutdown();
  }
});

// the header's context meter.
const probe = await jiti.import("./claude-probe.ts");
const recordingsDir = new URL("./__fixtures__/claude-2.1.312/", import.meta.url);

test("a claude model's window is pi's catalog entry, the one claude itself reports in every recording; an alias reads the full name claude's initialize answer resolves it to", async () => {
  for (const model of ["claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-1"]) assert.equal(claude.claudeContextWindow(model), 1_000_000, model);
  // The stub on PATH answers initialize as claude 2.1.322 did: default/opus/sonnet/haiku resolve
  // to full names in pi's catalog; its fable is claude-fable-5-2, which the catalog lacks: no window.
  const answer = await probe.probeClaude(claude.findClaudeBinary());
  assert.equal(answer.version, "2.1.322");
  for (const alias of ["default", "opus", "sonnet", "haiku"]) assert.equal(claude.claudeContextWindow(alias), 1_000_000, alias);
  assert.equal(claude.claudeContextWindow("fable"), null, "an alias whose full name pi's catalog lacks has no window");
  const reported = [];
  for (const file of fs.readdirSync(recordingsDir, { recursive: true }).map(String).filter((name) => name.endsWith(".jsonl"))) {
    for (const line of fs.readFileSync(new URL(file, recordingsDir), "utf8").split("\n")) {
      let event;
      try {
        event = JSON.parse(line);
      } catch {
        continue;
      }
      if (event?.type !== "result" || !event.modelUsage) continue;
      for (const [model, usage] of Object.entries(event.modelUsage)) reported.push([model, usage.contextWindow]);
    }
  }
  assert.deepEqual([...new Set(reported.map(([model]) => model))].sort(), ["claude-haiku-5-5", "claude-opus-5-5", "claude-sonnet-5-5"]);
  for (const [model, contextWindow] of reported) assert.equal(claude.claudeContextWindow(model), contextWindow, model);
  assert.equal(claude.claudeContextWindow("claude-default"), null, "no catalog entry: no window");
  assert.equal(claude.claudeContextWindow(""), null);
});

test("the meter's usage is claude's count over the window; an unknown count is no number, and no window is no meter", () => {
  assert.deepEqual(claude.claudeContextUsage(250_000, 1_000_000), { tokens: 250_000, percent: 25, contextWindow: 1_000_000 });
  assert.deepEqual(claude.claudeContextUsage(null, 1_000_000), { tokens: null, percent: null, contextWindow: 1_000_000 });
  assert.equal(claude.claudeContextUsage(250_000, null), null);
  assert.equal(claude.claudeContextUsage(null, null), null);
});

test("get_state's contextUsage: unknown before claude reports, the reply's count after, the transcript's for a resumed child, claude's size after /compact", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const contextWindow = 1_000_000;
  const usage = (tokens) => ({ tokens, percent: (tokens / contextWindow) * 100, contextWindow });
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  assert.deepEqual((await session.send({ type: "get_state" })).contextUsage, { tokens: null, percent: null, contextWindow }, "a new conversation has reported nothing");
  const events = collect(session);
  await session.send({ type: "prompt", message: "read the notes" });
  await waitFor(() => events.some((event) => event.type === "prompt_done"), 10_000, "the turn never ended");
  assert.deepEqual((await session.send({ type: "get_state" })).contextUsage, usage(1151), "the recorded turn's last reply, its final output included");
  assert.deepEqual(await session.send({ type: "set_model", model: "claude-haiku-5-5" }), { model: "claude-haiku-5-5" });
  assert.deepEqual((await session.send({ type: "get_state" })).contextUsage, usage(1151), "a model switch keeps the conversation's count");
  await session.shutdown();

  const resumed = claude.startClaudeSession(record);
  assert.deepEqual((await resumed.send({ type: "get_state" })).contextUsage, usage(1151), "a resumed child reads the transcript before its first reply");
  assert.deepEqual(await resumed.send({ type: "compact" }), { tokensBefore: 16739 });
  assert.deepEqual((await resumed.send({ type: "get_state" })).contextUsage, usage(2978), "a compaction leaves the size claude reported");
  await resumed.shutdown();

  const afterCompaction = claude.startClaudeSession(record);
  assert.deepEqual((await afterCompaction.send({ type: "get_state" })).contextUsage, usage(2978), "the transcript's boundary carries that size");
  await afterCompaction.shutdown();
});

// a running foreground subagent is moved to the background (claude's own
// `background_tasks` control request, the TUI's Ctrl+B) before a relay message is sent.
test("background_task sends claude's background_tasks request for that Agent call and returns claude's answer", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "subagent";
  process.env.PI_TEST_CLAUDE_STUB_HOLD_MS = "1500";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  try {
    await session.send({ type: "prompt", message: "launch it" });
    await waitFor(() => events.some(subagentText), 10_000, "the subagent never ran");
    assert.equal(session.isRunning(), true, "sent while the turn runs: no idle guard");
    const answer = await session.send({ type: "background_task", toolCallId: "toolu_01F1YZd2Gseivs4hckhQVw5s" }).catch((error) => ({ error: error.message }));
    assert.deepEqual(answer, { backgrounded: true });
    const requests = stdinOf(session.pid).filter((line) => line.type === "control_request");
    assert.equal(requests.length, 1);
    assert.deepEqual(requests[0].request, { subtype: "background_tasks", tool_use_id: "toolu_01F1YZd2Gseivs4hckhQVw5s" });
    assert.equal(typeof requests[0].request_id, "string");
  } finally {
    delete process.env.PI_TEST_CLAUDE_STUB_HOLD_MS;
    await session.shutdown();
  }
});

test("background_task without a tool call id is refused before anything reaches claude", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  await waitFor(() => launches().some((entry) => entry.pid === session.pid), 5_000, "no launch");
  for (const command of [{ type: "background_task" }, { type: "background_task", toolCallId: "" }, { type: "background_task", toolCallId: 7 }]) {
    await assert.rejects(session.send(command), /needs the tool call id/, JSON.stringify(command));
  }
  assert.ok(!stdinOf(session.pid).some((line) => line.type === "control_request"), "nothing reached claude");
  await session.shutdown();
});

// claude's permission prompts, wired through the session (the stub's
// `permission` mode speaks claude 2.1.322's can_use_tool / control_response shapes).
const permissionConfig = path.join(agentDir, "claude-permissions.json");
const setPermissionConfig = (config) => fs.writeFileSync(permissionConfig, JSON.stringify({ version: 1, ...config }));
const clearPermissionConfig = () => fs.rmSync(permissionConfig, { force: true });
const ASK_FLAGS = ["--permission-prompts", "host", "--permission-prompt-tool", "stdio"];
const flagsAt = (argv) => argv.slice(argv.indexOf("--permission-prompts"), argv.indexOf("--permission-prompts") + 4);
const isAsk = (argv) => JSON.stringify(flagsAt(argv)) === JSON.stringify(ASK_FLAGS);
const isDeny = (argv) => argv[argv.indexOf("--permission-prompts") + 1] === "none" && !argv.includes("--permission-prompt-tool");
const dialogs = (events, method) => events.filter((event) => event.type === "extension_ui_request" && (!method || event.method === method));
const closed = (events, id) => events.some((event) => event.type === "extension_ui_closed" && event.id === id);
const answersOf = (pid) => stdinOf(pid).filter((line) => line.type === "control_response");
const replyText = (events) => events.filter((event) => event.type === "message_end" && event.message.role === "assistant").map((event) => event.message.content.map((block) => block.text ?? "").join("")).at(-1);
const toolResult = (events) => events.find((event) => event.type === "message_end" && event.message.role === "toolResult")?.message;

test("ask mode launches with --permission-prompts host --permission-prompt-tool stdio; claude's can_use_tool becomes a dialog and the answer reaches stdin as control_response, the turn going on", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "permission";
  clearPermissionConfig();
  const record = await newRecord({ name: "perm one" });
  const before = launches().length;
  const pushed = pushes.length;
  const session = claude.startClaudeSession(record);
  try {
    const launch = await waitFor(() => launches()[before], 5_000, "no launch");
    assert.deepEqual(flagsAt(launch.argv), ASK_FLAGS, `the default mode is ask: ${launch.argv.join(" ")}`);
    assert.equal(session.permissionMode, "ask");
    const events = collect(session);
    await session.send({ type: "prompt", message: "ls -la" });
    const dialog = await waitFor(() => dialogs(events, "select")[0], 5_000, "claude's can_use_tool opened no dialog");
    assert.match(dialog.title, /Bash/);
    assert.match(dialog.title, /ls -la/);
    assert.deepEqual(dialog.options, ["Allow once", "Always allow in this session", "Deny"]);
    assert.ok(dialog.timeout > 0 && dialog.expiresAt > Date.now(), "the dialog carries the timeout");
    assert.equal(session.isRunning(), true, "the turn waits for the answer");
    await waitFor(() => pushes.length > pushed, 5_000, "push on: no pending-permission push");
    assert.deepEqual(pushes.slice(pushed), [[record.id, "perm one", "Bash"]]);
    assert.equal(await session.send({ type: "extension_ui_response", id: dialog.id, value: "Allow once" }), null);
    const [answer] = await waitFor(() => answersOf(session.pid).length > 0 && answersOf(session.pid), 5_000, "no control_response reached stdin");
    assert.deepEqual(answer, { type: "control_response", response: { subtype: "success", request_id: "perm-1", response: {
      behavior: "allow", updatedInput: { command: "ls -la" }, toolUseID: "toolu_perm_1", decisionClassification: "user_temporary",
    } } });
    assert.ok(closed(events, dialog.id), "the answered dialog was not closed");
    await waitFor(() => events.some((event) => event.type === "prompt_done"), 5_000, "the turn never ended");
    assert.equal(toolResult(events)?.isError, false, "the allowed call ran");
    assert.equal(replyText(events), "allowed");
    assert.equal(session.isAwaitingInput(), false);
    assert.equal(await session.send({ type: "extension_ui_response", id: dialog.id, value: "Allow once" }), null, "an answered id is ignored");
    assert.equal(answersOf(session.pid).length, 1, "a second answer wrote nothing");
  } finally {
    await session.shutdown();
  }
});

test("a pending request is replayed to a new listener, makes isAwaitingInput true and holds the idle timer", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "permission";
  // The push switch is read when a request opens: off now, so nothing is pushed.
  setPermissionConfig({ mode: "ask", push: false });
  const record = await newRecord();
  const pushed = pushes.length;
  const session = claude.startClaudeSession(record);
  try {
    const events = collect(session);
    assert.equal(session.isAwaitingInput(), false);
    await session.send({ type: "prompt", message: "sleep 1" });
    const dialog = await waitFor(() => dialogs(events, "select")[0], 5_000, "claude's can_use_tool opened no dialog");
    assert.equal(session.isAwaitingInput(), true);
    assert.ok(claude.getAwaitingClaudeSessionIds().includes(record.id));
    const replayed = [];
    session.onEvent((event) => replayed.push(event));
    assert.deepEqual(replayed, [dialog], "a new listener gets the pending dialog first");
    process.env.PI_WEB_IDLE_TIMEOUT_MS = "100";
    session.renewIdleTimer();
    // Sampled over several idle periods: the child is never reaped while the request waits.
    const until = Date.now() + 600;
    while (Date.now() < until) {
      assert.equal(session.isAlive(), true, "the idle timer reaped a child with a pending permission request");
      await delay(20);
    }
    assert.equal(pushes.length, pushed, "push off: nothing pushed");
    // Stop withdraws it: claude's control_cancel_request closes the dialog, nothing is written.
    await session.send({ type: "abort" });
    await waitFor(() => closed(events, dialog.id), 5_000, "the withdrawn request's dialog stayed open");
    await waitFor(() => events.some((event) => event.type === "prompt_done"), 5_000, "the stopped turn never ended");
    assert.equal(answersOf(session.pid).length, 0, "a withdrawn request is never answered");
    assert.equal(session.isAwaitingInput(), false);
    assert.ok(!claude.getAwaitingClaudeSessionIds().includes(record.id));
  } finally {
    process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";
    clearPermissionConfig();
    await session.shutdown();
  }
});

test("AskUserQuestion's answers reach claude as updatedInput.answers", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "permission";
  clearPermissionConfig();
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  try {
    const events = collect(session);
    await session.send({ type: "prompt", message: "ask: sizes" });
    const first = await waitFor(() => dialogs(events, "select")[0], 5_000, "the first question opened no dialog");
    assert.equal(first.title, "Colour: Which colour?");
    assert.deepEqual(first.options, ["Red — warm", "Blue — cool", "Other…"]);
    await session.send({ type: "extension_ui_response", id: first.id, value: "Blue — cool" });
    const second = await waitFor(() => dialogs(events, "input")[0], 5_000, "the multi-select question opened no input");
    assert.match(second.title, /^Sizes: Which sizes\?/);
    assert.equal(answersOf(session.pid).length, 0, "nothing is answered before the last question");
    await session.send({ type: "extension_ui_response", id: second.id, value: "S, L" });
    const [answer] = await waitFor(() => answersOf(session.pid).length > 0 && answersOf(session.pid), 5_000, "no control_response reached stdin");
    assert.equal(answer.response.request_id, "perm-1");
    assert.equal(answer.response.response.behavior, "allow");
    assert.deepEqual(answer.response.response.updatedInput.answers, { "Which colour?": "Blue", "Which sizes?": "S, L" });
    assert.equal(answer.response.response.updatedInput.questions.length, 2, "the original input goes back with the answers");
    await waitFor(() => events.some((event) => event.type === "prompt_done"), 5_000, "the turn never ended");
    assert.match(JSON.stringify(toolResult(events)?.content), /Which colour\?/);
    assert.equal(replyText(events), "allowed");
  } finally {
    await session.shutdown();
  }
});

test("deny (the global default or the record's override) launches --permission-prompts none and claude's permission_denied becomes a warning notice", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "permission";
  try {
    setPermissionConfig({ mode: "deny", push: true });
    const record = await newRecord();
    let before = launches().length;
    const session = claude.startClaudeSession(record);
    const launch = await waitFor(() => launches()[before], 5_000, "no launch");
    assert.ok(isDeny(launch.argv), `the global deny launches --permission-prompts none: ${launch.argv.join(" ")}`);
    assert.equal(session.permissionMode, "deny");
    const events = collect(session);
    await session.send({ type: "prompt", message: "rm x" });
    await waitFor(() => events.some((event) => event.type === "prompt_done"), 5_000, "the turn never ended");
    assert.deepEqual(dialogs(events).map(({ method, notifyType, message }) => ({ method, notifyType, message })), [
      { method: "notify", notifyType: "warning", message: "claude denied Bash: mode" },
    ], "one warning notice, no dialog");
    assert.equal(toolResult(events)?.isError, true, "the tool card still shows the error");
    assert.equal(answersOf(session.pid).length, 0);
    await session.shutdown();

    // The record's override wins over the global mode, either way; its notice speaks the session's language.
    const zh = await newRecord({ permissionMode: "deny", agentLanguage: "zh-CN" });
    setPermissionConfig({ mode: "ask", push: true });
    before = launches().length;
    const overridden = claude.startClaudeSession(index.getClaudeSessionRecord(zh.id));
    const zhLaunch = await waitFor(() => launches()[before], 5_000, "no launch");
    assert.ok(isDeny(zhLaunch.argv), `the record's deny wins over the global ask: ${zhLaunch.argv.join(" ")}`);
    const zhEvents = collect(overridden);
    await overridden.send({ type: "prompt", message: "rm y" });
    await waitFor(() => zhEvents.some((event) => event.type === "prompt_done"), 5_000, "the turn never ended");
    assert.deepEqual(dialogs(zhEvents).map(({ method, message }) => ({ method, message })), [{ method: "notify", message: "claude 拒绝了 Bash：mode" }]);
    await overridden.shutdown();

    setPermissionConfig({ mode: "deny", push: true });
    const asking = await newRecord({ permissionMode: "ask" });
    before = launches().length;
    const askSession = claude.startClaudeSession(index.getClaudeSessionRecord(asking.id));
    assert.ok(isAsk((await waitFor(() => launches()[before], 5_000, "no launch")).argv), "the record's ask wins over the global deny");
    await askSession.shutdown();
  } finally {
    clearPermissionConfig();
  }
});

test("the global mode is read at spawn: a changed default leaves the live child's argv; an override ends the idle child and the next start carries it; a running turn refuses it", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "permission";
  clearPermissionConfig();
  const record = await newRecord();
  let before = launches().length;
  const live = claude.startClaudeSession(record);
  const sessions = [live];
  try {
    assert.ok(isAsk((await waitFor(() => launches()[before], 5_000, "no launch")).argv), "launched with the default ask");
    setPermissionConfig({ mode: "deny", push: true });
    const other = await newRecord();
    before = launches().length;
    sessions.push(claude.startClaudeSession(other));
    assert.ok(isDeny((await waitFor(() => launches()[before], 5_000, "no launch")).argv), "a child started after the change reads deny");
    assert.equal(live.isAlive(), true, "the default change ended nothing");
    assert.equal(live.permissionMode, "ask");
    assert.ok(isAsk(live.argv), "the live child keeps its argv");
    assert.equal(launches().filter((entry) => entry.argv.includes(record.id)).length, 1, "the live session was not relaunched");

    clearPermissionConfig();
    assert.deepEqual(await claude.setClaudePermissionOverride(record.id, "deny"), { override: "deny", restarted: true });
    assert.equal(index.getClaudeSessionRecord(record.id).permissionMode, "deny", "the override is in the index");
    await waitFor(() => !live.isAlive(), 5_000, "the idle child was not ended");
    before = launches().length;
    const denied = claude.startClaudeSession(index.getClaudeSessionRecord(record.id));
    sessions.push(denied);
    const deniedLaunch = await waitFor(() => launches()[before], 5_000, "no relaunch");
    assert.ok(isDeny(deniedLaunch.argv), `the next start carries the override over the global ask: ${deniedLaunch.argv.join(" ")}`);

    assert.deepEqual(await claude.setClaudePermissionOverride(record.id, null), { override: null, restarted: true });
    assert.equal(index.getClaudeSessionRecord(record.id).permissionMode, undefined, "cleared: the session follows the default");
    await waitFor(() => !denied.isAlive(), 5_000, "the idle child was not ended");
    before = launches().length;
    const following = claude.startClaudeSession(index.getClaudeSessionRecord(record.id));
    sessions.push(following);
    assert.ok(isAsk((await waitFor(() => launches()[before], 5_000, "no relaunch")).argv), "following the default ask again");

    const events = collect(following);
    await following.send({ type: "prompt", message: "ls" });
    await waitFor(() => dialogs(events, "select")[0], 5_000, "no dialog");
    await assert.rejects(claude.setClaudePermissionOverride(record.id, "deny"), (error) => error instanceof claude.ClaudeSessionBusyError && /turn is running/.test(error.message));
    assert.equal(index.getClaudeSessionRecord(record.id).permissionMode, undefined, "a refused override changed nothing");
    assert.equal(following.isAlive(), true, "a refused override ended nothing");

    assert.deepEqual(await claude.setClaudePermissionOverride(other.id, "ask"), { override: "ask", restarted: true });
    await assert.rejects(claude.setClaudePermissionOverride(randomUUID(), "ask"), /not found/);
  } finally {
    clearPermissionConfig();
    for (const session of sessions) await session.shutdown();
  }
});

test("the child's exit closes its pending dialogs", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "permission";
  clearPermissionConfig();
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  const events = collect(session);
  await session.send({ type: "prompt", message: "ls" });
  const dialog = await waitFor(() => dialogs(events, "select")[0], 5_000, "no dialog");
  assert.equal(session.isAwaitingInput(), true);
  process.kill(session.pid, "SIGKILL");
  await waitFor(() => events.some((event) => event.type === "session_shutdown"), 5_000, "the exit was not noticed");
  const closedAt = events.findIndex((event) => event.type === "extension_ui_closed" && event.id === dialog.id);
  assert.ok(closedAt >= 0, "the dead child's dialog stayed open");
  assert.ok(closedAt < events.findIndex((event) => event.type === "session_shutdown"), "closed before the shutdown event");
  assert.equal(session.isAwaitingInput(), false);
  assert.ok(!claude.getAwaitingClaudeSessionIds().includes(record.id));
  assert.equal(answersOf(session.pid).length, 0, "nothing was answered for it");
});

test("a turn's end closes the main agent's pending prompt even when claude withdraws nothing on Stop", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "permission-silent-stop";
  clearPermissionConfig();
  const record = await newRecord();
  const session = claude.startClaudeSession(record);
  try {
    const events = collect(session);
    await session.send({ type: "prompt", message: "ls" });
    const dialog = await waitFor(() => dialogs(events, "select")[0], 5_000, "no dialog");
    assert.equal(session.isAwaitingInput(), true);
    await session.send({ type: "abort" });
    await waitFor(() => events.some((event) => event.type === "prompt_done"), 5_000, "the stopped turn never ended");
    await waitFor(() => closed(events, dialog.id), 5_000, "the stopped turn's dialog stayed open");
    assert.equal(session.isAwaitingInput(), false, "no longer awaiting once the turn ended");
    assert.ok(!claude.getAwaitingClaudeSessionIds().includes(record.id));
    assert.equal(answersOf(session.pid).length, 0, "nothing was answered for it");
  } finally {
    await session.shutdown();
  }
});
