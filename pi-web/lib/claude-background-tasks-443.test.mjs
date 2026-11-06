// carries: lib/claude-adapter.ts, lib/claude-background-task-view.ts, lib/claude-session.ts, lib/__fixtures__/claude-2.1.319/*, e2e/claude-stub/claude-bg-tasks.mjs, lib/__fixtures__/claude-stub/claude.mjs
// claude's background tasks — a `Bash` run in the background, a
// `Monitor` (both `task_type: "local_bash"`) — are not subagent runs. u1 replays the
// real 2.1.319 `absorbed-notification/` recording (a background and a foreground
// Bash) and synthetic frames in the same shapes through ClaudeStreamTranslator.
// u2 on runs a ClaudeSession against the background-task stub
// (e2e/claude-stub/claude-bg-tasks.mjs), copied onto a scratch PATH as `claude`;
// u3's background subagent comes from the unit stub (lib/__fixtures__/claude-stub/claude.mjs,
// `subagent-early`), put first on PATH for that one start. u4 stops the stub's `hang` /
// `stuck` turns with a background task running. u5 stops one task of two and reads a
// task's output over claude's `stop_task` / `get_task_output` control requests.
// u5b: a stopped task's `task_notification { status: "stopped" }` leaves nothing unsettled.
// u7: a new subscriber gets the whole list of running tasks (`claude_background_tasks`),
// which the page's tasks are replaced by, so a task that ended unseen leaves the list.
// u8: a Stop's deadline with nothing listable to end signals as before; a `killed`
// notification is a stop too; fork's refusal names the subagent; the stub's plain interrupt
// stops its tasks, as claude's does.
import "#test-env";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { createJiti } from "jiti";
import { readStubLog } from "./__fixtures__/claude-stub-log.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
// A scratch agent dir, config dir and HOME, and a PATH whose `claude` is the background-task stub.
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-bg-tasks-443-"));
const agentDir = path.join(scratch, "agent");
const configDir = path.join(scratch, "claude-config");
const homeDir = path.join(scratch, "home");
const binDir = path.join(scratch, "bin");
const workDir = path.join(scratch, "work");
const stubLog = path.join(scratch, "stub.jsonl");
for (const dir of [agentDir, binDir, workDir, homeDir]) fs.mkdirSync(dir);
fs.copyFileSync(path.join(here, "..", "e2e", "claude-stub", "claude-bg-tasks.mjs"), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.HOME = homeDir;
process.env.CLAUDE_CONFIG_DIR = configDir;
process.env.PATH = `${binDir}${path.delimiter}${process.env.PATH}`;
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";

const jiti = createJiti(import.meta.url, { alias: { "@": path.dirname(here) }, interopDefault: true });
const adapter = await jiti.import("./claude-adapter.ts");
const view = await jiti.import("./claude-background-task-view.ts");
const claude = await jiti.import("./claude-session.ts");
const index = await jiti.import("./claude-index.ts");

const records = () => readStubLog(stubLog);
test.after(async () => {
  await claude.shutdownAllClaudeSessions();
  // Every task child the stub started, whatever ended its stub.
  for (const entry of records().filter((record) => record.kind === "task")) {
    try { process.kill(entry.taskPid, "SIGKILL"); } catch { /* gone */ }
  }
  fs.rmSync(scratch, { recursive: true, force: true });
});

const recording = fs.readFileSync(path.join(here, "__fixtures__", "claude-2.1.319", "absorbed-notification", "stream.jsonl"), "utf8")
  .split("\n").filter(Boolean).map((line) => JSON.parse(line));
const BG_TASK = "bt1rpujb9";
const FG_TASK = "b3xb3q0ar";
const BG_CALL = "toolu_018cxp63oQK3REvMqGWGBA9Q";
const FG_CALL = "toolu_01F8XsCVvX6BjdzaDbc2fSB8";

const taskEvents = (events) => events.filter((event) => event.type === view.CLAUDE_BACKGROUND_TASK_EVENT).map((event) => event.task);
const runEvents = (events) => events.filter((event) => event.type === adapter.CLAUDE_SUBAGENT_EVENT).map((event) => event.run);

// Synthetic frames in the recording's shapes (lib/__fixtures__/claude-2.1.319/absorbed-notification/stream.jsonl).
const init = () => ({ type: "system", subtype: "init", model: "claude-haiku-5-5", session_id: "s" });
const toolUse = (id, name, input) => ({ type: "assistant", message: { id: `msg-${id}`, role: "assistant", model: "claude-haiku-5-5", content: [{ type: "tool_use", id, name, input }] } });
const started = (taskId, toolUseId, description, backgrounded) => ({
  type: "system", subtype: "task_started", task_id: taskId, tool_use_id: toolUseId, description, task_type: "local_bash",
  ...(backgrounded === undefined ? {} : { is_backgrounded: backgrounded }),
});
const updated = (taskId, patch) => ({ type: "system", subtype: "task_updated", task_id: taskId, patch });
const notification = (taskId, toolUseId, status) => ({ type: "system", subtype: "task_notification", task_id: taskId, tool_use_id: toolUseId, status, output_file: `/tmp/${taskId}.output`, summary: "done" });
const result = () => ({ type: "result", subtype: "success", is_error: false, result: "ok", session_id: "s" });

test("443 u1 the recorded background Bash is a task, never a subagent run, and holds nothing unsettled while it runs", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 1_000);
  const events = [];
  let unsettledAfterStart;
  let runningAfterStart;
  for (const frame of recording) {
    events.push(...translator.push(frame).events);
    if (frame.type === "system" && frame.subtype === "task_started" && frame.task_id === BG_TASK) {
      unsettledAfterStart = translator.hasUnsettledSubagents();
      runningAfterStart = translator.hasRunningBackgroundTasks();
    }
  }
  assert.equal(unsettledAfterStart, false, "a running background Bash counted as an unsettled subagent");
  assert.equal(runningAfterStart, true, "the running background Bash is not reported as a running background task");
  assert.deepEqual(runEvents(events).filter((run) => run.toolCallId === BG_CALL || run.toolCallId === FG_CALL).map((run) => run.toolCallId), [],
    "a Bash call came out as a claude_subagent run");
  const tasks = taskEvents(events);
  assert.ok(tasks.length > 0, "no claude_background_task event");
  assert.ok(!tasks.some((task) => task.taskId === FG_TASK), "the foreground Bash produced a task event");
  assert.ok(tasks.every((task) => task.taskId === BG_TASK));
  assert.deepEqual(
    { taskId: tasks[0].taskId, toolCallId: tasks[0].toolCallId, kind: tasks[0].kind, command: tasks[0].command, description: tasks[0].description, status: tasks[0].status },
    { taskId: BG_TASK, toolCallId: BG_CALL, kind: "shell", command: "sleep 4; echo BG_DONE_395", description: "Run delayed echo in background", status: "running" },
  );
  assert.equal(tasks.at(-1).status, "completed", "the task's notification did not settle it");
  assert.equal(typeof tasks.at(-1).endedAt, "number", "a settled task has no end");
  assert.equal(translator.hasRunningBackgroundTasks(), false);
  assert.equal(translator.hasUnsettledSubagents(), false);
});

test("443 u1 a Monitor is a monitor task; a foreground Bash later moved to the background is listed then", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 2_000);
  const events = [init(), toolUse("call-mon", "Monitor", { command: "tail -f app.log", description: "watch logs" }), started("bmon00001", "call-mon", "watch logs"),
    toolUse("call-fg", "Bash", { command: "npm test", description: "Run tests" }), started("bfg000001", "call-fg", "Run tests", false)]
    .flatMap((frame) => translator.push(frame).events);
  assert.deepEqual(runEvents(events), [], "a local_bash task came out as a subagent run");
  const monitor = taskEvents(events);
  assert.deepEqual(monitor.map((task) => [task.taskId, task.kind, task.command, task.status]), [["bmon00001", "monitor", "tail -f app.log", "running"]],
    "the Monitor is not one running monitor task, or the foreground Bash was listed");
  assert.equal(view.claudeBackgroundTaskLabel(monitor[0]), "watch logs", "a monitor is labelled by its description");
  const moved = taskEvents(translator.push(updated("bfg000001", { is_backgrounded: true })).events);
  assert.deepEqual(moved.map((task) => [task.taskId, task.toolCallId, task.kind, task.command, task.status]), [["bfg000001", "call-fg", "shell", "npm test", "running"]],
    "a foreground Bash moved to the background is not listed");
  assert.equal(view.claudeBackgroundTaskLabel(moved[0]), "npm test", "a shell task is labelled by its command");
  assert.equal(translator.hasUnsettledSubagents(), false);
});

test("443 u1 a background Bash's notification still opens claude's follow-up turn, once", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 3_000);
  const events = [init(), toolUse("call-bg", "Bash", { command: "sleep 30", run_in_background: true }), started("bbg000001", "call-bg", "sleep", true)]
    .flatMap((frame) => translator.push(frame).events);
  assert.equal(translator.hasUnsettledSubagents(), false, "the running task held the session unsettled");
  events.push(...translator.push(result()).events);
  events.push(...translator.push(notification("bbg000001", "call-bg", "completed")).events);
  assert.equal(translator.hasUnsettledSubagents(), true, "between the notification and claude's follow-up turn the session is not settled");
  const followUp = translator.push(init());
  assert.equal(followUp.claudeTurnStart, true, "claude's follow-up turn for the task did not open as a turn");
  assert.equal(translator.hasUnsettledSubagents(), false);
  translator.push(result());
  assert.equal(translator.push(init()).claudeTurnStart, undefined, "the follow-up turn was announced twice");
  assert.deepEqual(runEvents(events), []);
  assert.deepEqual(taskEvents(events).map((task) => task.status), ["running", "completed"]);
});

test("443 u1 the child's exit settles a running background task as stopped", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 4_000);
  [init(), toolUse("call-dev", "Bash", { command: "npm run dev", run_in_background: true }), started("bdev00001", "call-dev", "dev server", true), result()]
    .forEach((frame) => translator.push(frame));
  assert.equal(translator.hasRunningBackgroundTasks(), true);
  const exit = translator.settleOnExit();
  assert.deepEqual(runEvents(exit), []);
  assert.deepEqual(taskEvents(exit).map((task) => [task.taskId, task.command, task.status, task.endedAt]), [["bdev00001", "npm run dev", "stopped", 4_000]]);
  assert.equal(translator.hasRunningBackgroundTasks(), false);
});

test("443 u1 the view lists running tasks oldest first and times them", () => {
  const task = (taskId, startedAt, status, endedAt) => ({ taskId, toolCallId: `call-${taskId}`, kind: "shell", command: taskId, status, startedAt, ...(endedAt === undefined ? {} : { endedAt }) });
  const tasks = { b: task("b", 200, "running"), a: task("a", 100, "running"), c: task("c", 50, "completed", 80) };
  assert.deepEqual(view.runningClaudeBackgroundTasks(tasks).map((entry) => entry.taskId), ["a", "b"]);
  assert.equal(view.claudeBackgroundTaskElapsedMs(tasks.a, 1_100), 1_000);
  assert.equal(view.claudeBackgroundTaskElapsedMs(tasks.c, 1_100), 30, "a settled task's time stops at its end");
});

// u2: a session with a long-running background task (the stub's `bg:` turn).

async function waitFor(predicate, timeoutMs, message) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const value = predicate();
    if (value) return value;
    await delay(25);
  }
  assert.fail(message);
}
const alive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
};
const stdinOf = (pid) => records().filter((entry) => entry.kind === "stdin" && entry.pid === pid).map((entry) => JSON.parse(entry.line));
const taskOf = (pid, command) => records().find((entry) => entry.kind === "task" && entry.pid === pid && entry.command === command);
const settledCount = (events) => events.filter((event) => event.type === "agent_settled").length;

async function newRecord(extra = {}) {
  const record = {
    id: randomUUID(),
    cwd: workDir,
    model: "claude-haiku-5-5",
    created: new Date().toISOString(),
    modified: new Date().toISOString(),
    firstMessage: "",
    usage: { ...index.EMPTY_CLAUDE_USAGE },
    ...extra,
  };
  await index.upsertClaudeSessionRecord(record);
  return record;
}

/** A live session and its events, after one turn per message, each to its end. */
async function sessionAfter(messages, extra = {}) {
  const session = claude.startClaudeSession(await newRecord(extra));
  const events = [];
  session.onEvent((event) => events.push(event));
  for (const [n, message] of messages.entries()) {
    await session.send({ type: "prompt", message });
    await waitFor(() => settledCount(events) === n + 1, 5_000, `the turn of "${message}" did not end`);
  }
  return { session, events };
}

test("443 u2 a turn that leaves a background task running ends settled: no subagentsActive, nothing unsettled", async () => {
  const { session, events } = await sessionAfter(["bg: npm run dev"]);
  const task = await waitFor(() => taskOf(session.pid, "npm run dev"), 5_000, "the stub started no task child");
  assert.deepEqual(events.filter((event) => event.type === view.CLAUDE_BACKGROUND_TASK_EVENT).map((event) => [event.task.taskId, event.task.command, event.task.status]),
    [[task.taskId, "npm run dev", "running"]], "the session did not report the running background task");
  for (const type of ["agent_settled", "prompt_done"]) {
    const ends = events.filter((event) => event.type === type);
    assert.equal(ends.length, 1);
    assert.equal(ends[0].subagentsActive, undefined, `${type} carried subagentsActive for a background task: the chime, notification and push were held`);
  }
  assert.equal(session.hasUnsettledSubagents(), false);
  assert.ok(!claude.getClaudeSessionIdsWithUnsettledSubagents().includes(session.sessionId), "the lease / running list would report subagentsActive");
  assert.equal(alive(task.taskPid), true, "the task child is gone");
  await session.shutdown();
  await waitFor(() => !alive(task.taskPid), 5_000, "the task child outlived its claude");
});

test("443 u2 a session whose background task still runs forks", async () => {
  const { session } = await sessionAfter(["bg: npm run dev", "and then?"]);
  const task = await waitFor(() => taskOf(session.pid, "npm run dev"), 5_000, "the stub started no task child");
  const second = stdinOf(session.pid).filter((line) => line.type === "user")[1];
  assert.ok(second?.uuid, "the second message went to claude without its uuid");
  const fork = await claude.forkClaudeSession(session.sessionId, second.uuid);
  assert.equal(fork.cancelled, false);
  assert.match(fork.newSessionId, /^[0-9a-f-]{36}$/);
  // The copy holds the conversation before the message: the background Bash's exchange.
  const copy = claude.readClaudeHistory(fork.newSessionId);
  assert.ok(copy.file, "the fork wrote no transcript");
  assert.ok(copy.messages.some((message) => message.role === "assistant" && JSON.stringify(message.content).includes("npm run dev")), "the fork lost the background Bash's exchange");
  assert.equal(alive(task.taskPid), true, "forking ended the background task");
  assert.equal(session.isAlive(), true);
  await session.shutdown();
});

test("443 u2 a running background task keeps the idle child from being reaped", async () => {
  process.env.PI_WEB_IDLE_TIMEOUT_MS = "200";
  try {
    const { session } = await sessionAfter(["bg: npm run dev"]);
    const task = await waitFor(() => taskOf(session.pid, "npm run dev"), 5_000, "the stub started no task child");
    // The same idle timeout reaps a child with nothing running: the countdown is live here.
    const { session: idle } = await sessionAfter(["hello"]);
    await waitFor(() => !idle.isAlive(), 5_000, "an idle child with no background task was never reaped");
    // Then many more idle periods for the one with a running task.
    const until = Date.now() + 10 * 200;
    while (Date.now() < until && session.isAlive()) await delay(25);
    assert.equal(session.isAlive(), true, "the idle reap ended a child whose background task was still running");
    assert.equal(alive(session.pid), true, "the claude process exited");
    assert.equal(alive(task.taskPid), true, "the idle reap killed the background task");
    assert.ok(!records().some((entry) => entry.kind === "signal" && entry.pid === session.pid), "claude's process group was signalled");
    await session.shutdown();
  } finally {
    process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";
  }
});

// u3: an operation that restarts the claude child — the effort back to default, an
// in-session edit — is refused while background work is unfinished, naming it.

// The unit stub for a background subagent: it reads its recordings beside its own file.
const unitStubDir = path.join(scratch, "unit-stub");
fs.mkdirSync(path.join(unitStubDir, "bin"), { recursive: true });
fs.copyFileSync(path.join(here, "__fixtures__", "claude-stub", "claude.mjs"), path.join(unitStubDir, "bin", "claude"));
fs.chmodSync(path.join(unitStubDir, "bin", "claude"), 0o755);
fs.symlinkSync(path.join(here, "__fixtures__", "claude-2.1.312"), path.join(unitStubDir, "claude-2.1.312"));
const AGENT_DESCRIPTION = "Read note.txt first line";

/** A session started by the unit stub in `mode` (the PATH and stub settings the child inherits, then restored). */
function startUnitStubSession(record, mode, extraEnv) {
  const saved = { PATH: process.env.PATH, PI_TEST_CLAUDE_STUB_MODE: process.env.PI_TEST_CLAUDE_STUB_MODE };
  for (const key of Object.keys(extraEnv)) saved[key] = process.env[key];
  process.env.PATH = `${path.join(unitStubDir, "bin")}${path.delimiter}${saved.PATH}`;
  process.env.PI_TEST_CLAUDE_STUB_MODE = mode;
  Object.assign(process.env, extraEnv);
  try {
    return claude.startClaudeSession(record);
  } finally {
    for (const [key, value] of Object.entries(saved)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }
}

test("443 u3 the effort back to default is refused while a background subagent is unsettled, naming it; the child lives on", async () => {
  const record = await newRecord({ effort: "high" });
  // The parent turn answers first; the subagent then holds well past this test.
  const session = startUnitStubSession(record, "subagent-early", { PI_TEST_CLAUDE_STUB_HOLD_MS: "60000" });
  const events = [];
  session.onEvent((event) => events.push(event));
  await session.send({ type: "prompt", message: "launch it" });
  await waitFor(() => settledCount(events) === 1 && session.hasUnsettledSubagents()
    && runEvents(events).some((run) => run.description === AGENT_DESCRIPTION && run.background && run.status === "running"),
  10_000, "the parent turn never ended with its background subagent running");
  const lines = stdinOf(session.pid).length;
  const stoppedBefore = runEvents(events).filter((run) => run.status === "stopped").length;

  const answer = await session.send({ type: "set_effort", effort: "default" });
  assert.equal(answer.rejected, true, `the effort back to default was not refused: ${JSON.stringify(answer)}`);
  assert.equal(answer.pending, undefined);
  assert.equal(answer.effort, "high", "the refusal does not report the effort still in force");
  assert.equal(typeof answer.error, "string");
  assert.ok(answer.error.includes(AGENT_DESCRIPTION), `the refusal does not name the subagent: ${answer.error}`);
  assert.equal(session.isAlive(), true, "the child was ended");
  assert.equal(alive(session.pid), true, "the claude process exited");
  assert.equal(runEvents(events).filter((run) => run.status === "stopped").length, stoppedBefore, "the subagent run was stopped");
  assert.equal(index.getClaudeSessionRecord(record.id).effort, "high", "the index's effort changed");
  assert.equal(stdinOf(session.pid).length, lines, "the refusal wrote to claude's stdin");
  assert.ok(!records().some((entry) => entry.kind === "signal" && entry.pid === session.pid));
  await session.shutdown();
});

test("443 u3 a running background task refuses the effort back to default and an edit, naming its command; claude and the task live on", async () => {
  const { session } = await sessionAfter(["bg: npm run dev", "and then?"], { effort: "high" });
  const task = await waitFor(() => taskOf(session.pid, "npm run dev"), 5_000, "the stub started no task child");
  const lines = stdinOf(session.pid).length;
  const launches = records().filter((entry) => entry.kind === "launch").length;

  const answer = await session.send({ type: "set_effort", effort: "default" });
  assert.equal(answer.rejected, true, `the effort back to default was not refused: ${JSON.stringify(answer)}`);
  assert.equal(answer.effort, "high");
  assert.ok(answer.error?.includes("npm run dev"), `the refusal does not name the task's command: ${answer.error}`);
  assert.equal(session.isAlive(), true, "the child was ended");
  assert.equal(alive(task.taskPid), true, "the background task was killed");
  assert.equal(index.getClaudeSessionRecord(session.sessionId).effort, "high", "the index's effort changed");
  assert.equal(stdinOf(session.pid).length, lines, "the refusal wrote to claude's stdin");

  const second = stdinOf(session.pid).filter((line) => line.type === "user")[1];
  assert.ok(second?.uuid, "the second message went to claude without its uuid");
  const record = index.getClaudeSessionRecord(session.sessionId);
  await assert.rejects(claude.editClaudeSession(record, second.uuid, { message: "edited" }), (error) => {
    assert.ok(error.message.includes("npm run dev"), `the edit's refusal does not name the task's command: ${error.message}`);
    return true;
  });
  assert.equal(session.isAlive(), true, "the edit ended the child");
  assert.equal(claude.getClaudeSession(session.sessionId), session, "the edit replaced the child");
  assert.equal(alive(session.pid), true, "the claude process exited");
  assert.equal(alive(task.taskPid), true, "the edit killed the background task");
  assert.equal(stdinOf(session.pid).length, lines, "the edit wrote to claude's stdin");
  assert.equal(records().filter((entry) => entry.kind === "launch").length, launches, "a child was started");
  assert.ok(!records().some((entry) => entry.kind === "signal" && entry.pid === session.pid), "claude's process group was signalled");
  await session.shutdown();
});

test("443 u3 with no background work the effort back to default still ends the idle child", async () => {
  const { session } = await sessionAfter(["hello"], { effort: "high" });
  assert.deepEqual(await session.send({ type: "set_effort", effort: "default" }), { effort: "default", pending: true });
  assert.equal(session.isAlive(), false, "the idle child was not ended");
  assert.equal(index.getClaudeSessionRecord(session.sessionId).effort, "default");
});

// u4: Stop mid-turn ends only the turn; ending the background work with it is a
// confirmed force stop.

const FORCE_STOP_EVENT = "claude_force_stop_needed";
const signalsOf = (pid) => records().filter((entry) => entry.kind === "signal" && entry.pid === pid);
const interruptsOf = (pid) => stdinOf(pid).filter((line) => line.type === "control_request" && line.request?.subtype === "interrupt");

/** Sends `message` (a stub directive whose turn stays open) once the background task runs, and waits for the stub to take it. */
async function openTurn(session, message) {
  const sent = stdinOf(session.pid).length;
  await session.send({ type: "prompt", message });
  await waitFor(() => stdinOf(session.pid).length > sent, 5_000, `the stub never read "${message}"`);
  assert.equal(session.isRunning(), true);
}

test("443 u4 Stop mid-turn asks claude to spare the background tasks; the turn ends aborted, the task lives on", async () => {
  const { session, events } = await sessionAfter(["bg: npm run dev"]);
  const task = await waitFor(() => taskOf(session.pid, "npm run dev"), 5_000, "the stub started no task child");
  await openTurn(session, "hang");

  await session.send({ type: "abort" });
  await waitFor(() => settledCount(events) === 2, 5_000, "the interrupted turn did not end");
  assert.deepEqual(interruptsOf(session.pid).map((line) => line.request), [{ subtype: "interrupt", spare_background_tasks: true }],
    "Stop's interrupt does not ask claude to spare its background work (a plain interrupt stops background agents)");
  assert.equal(events.filter((event) => event.type === "agent_settled").at(-1).aborted, true, "the turn did not end as stopped");
  assert.equal(session.isRunning(), false);
  assert.equal(session.isAlive(), true, "the child was ended");
  assert.equal(alive(task.taskPid), true, "Stop killed the background task");
  assert.deepEqual(signalsOf(session.pid), [], "claude's process group was signalled");
  assert.ok(!events.some((event) => event.type === FORCE_STOP_EVENT));
  await session.shutdown();
});

test("443 u4 a turn still running at the deadline with background work is not signalled: a force stop is asked for, replayed, and only abort force signals", async () => {
  const { session, events } = await sessionAfter(["bg: npm run dev"]);
  const task = await waitFor(() => taskOf(session.pid, "npm run dev"), 5_000, "the stub started no task child");
  await openTurn(session, "stuck");

  const stoppedAt = Date.now();
  await session.send({ type: "abort" });
  await waitFor(() => interruptsOf(session.pid).length === 1, 2_000, "Stop sent no interrupt to a running turn");
  const asked = await waitFor(() => events.find((event) => event.type === FORCE_STOP_EVENT), claude.CLAUDE_INTERRUPT_DEADLINE_MS + 3_000,
    `no ${FORCE_STOP_EVENT} at the deadline`);
  // Past the deadline by a second: nothing more happens on its own.
  const quietUntil = stoppedAt + claude.CLAUDE_INTERRUPT_DEADLINE_MS + 1_000;
  while (Date.now() < quietUntil) await delay(50);
  assert.deepEqual(signalsOf(session.pid), [], "the deadline signalled claude's process group with background work running");
  assert.equal(alive(session.pid), true, "the claude process exited");
  assert.equal(alive(task.taskPid), true, "the background task was killed");
  assert.equal(session.isRunning(), true, "the still-running turn was ended");
  assert.equal(events.filter((event) => event.type === FORCE_STOP_EVENT).length, 1, "the force stop was asked for more than once");
  assert.deepEqual(asked.work, [{ kind: "task", id: task.taskId, label: "npm run dev" }], "the force stop does not list the work it would end");

  // A page that subscribes now (a reconnect) is told at once.
  const replayed = [];
  const unsubscribe = session.onEvent((event) => replayed.push(event));
  assert.deepEqual(replayed.filter((event) => event.type === FORCE_STOP_EVENT), [asked], "a new subscriber was not told of the pending force stop");
  unsubscribe();

  await session.send({ type: "abort", force: true });
  await waitFor(() => signalsOf(session.pid).some((entry) => entry.signal === "SIGINT"), 2_000, "abort force sent no SIGINT");
  await waitFor(() => !session.isAlive(), 5_000, "the force-stopped child did not exit");
  await waitFor(() => !alive(task.taskPid), 5_000, "the background task outlived the force stop");
  assert.equal(events.filter((event) => event.type === view.CLAUDE_BACKGROUND_TASK_EVENT && event.task.taskId === task.taskId).at(-1)?.task.status, "stopped",
    "the force-stopped task did not settle as stopped");
});

// u5: one background task stopped on its own, a task's output read, both over claude's
// control channel (2.1.322: `stop_task`, `get_task_output`).

const taskRequestsOf = (pid, subtype) => stdinOf(pid).filter((line) => line.type === "control_request" && line.request?.subtype === subtype);
const stubTaskOutput = (command) => `$ ${command}\nstub output line 1\nstub output line 2\n`;
/** The command's answer; a rejection fails the test with its text (the command may not exist yet). */
const answerOf = (session, command) => session.send(command).catch((error) => assert.fail(`${command.type} was rejected: ${error.message}`));

test("443 u5 stop_task stops one background task over claude's control request; the other runs on", async () => {
  const { session, events } = await sessionAfter(["bg: npm run dev", "monitor: watch logs"]);
  const dev = await waitFor(() => taskOf(session.pid, "npm run dev"), 5_000, "the stub started no dev task");
  const monitor = await waitFor(() => taskOf(session.pid, "watch logs"), 5_000, "the stub started no monitor task");

  await answerOf(session, { type: "stop_task", taskId: dev.taskId });
  assert.deepEqual(taskRequestsOf(session.pid, "stop_task").map((line) => line.request), [{ subtype: "stop_task", task_id: dev.taskId }],
    "stop_task is not claude's stop_task control request for that task");
  assert.equal(typeof taskRequestsOf(session.pid, "stop_task")[0].request_id, "string");
  await waitFor(() => !alive(dev.taskPid), 5_000, "the stopped task's child lives on");
  const devTask = await waitFor(() => events.filter((event) => event.type === view.CLAUDE_BACKGROUND_TASK_EVENT && event.task.taskId === dev.taskId).at(-1)?.task.status === "stopped",
    5_000, "the stopped task did not settle as stopped");
  assert.ok(devTask);
  assert.deepEqual(events.filter((event) => event.type === view.CLAUDE_BACKGROUND_TASK_EVENT && event.task.taskId === monitor.taskId).map((event) => event.task.status), ["running"],
    "the other task's state changed");
  assert.equal(alive(monitor.taskPid), true, "stopping one task killed the other");
  assert.deepEqual(session.backgroundWork().map((work) => [work.kind, work.id]), [["task", monitor.taskId]]);
  assert.equal(session.isAlive(), true, "stopping a task ended claude");
  assert.deepEqual(signalsOf(session.pid), [], "claude's process group was signalled");
  await session.shutdown();
});

test("443 u5 task_output reads a task's output over claude's get_task_output", async () => {
  const { session } = await sessionAfter(["bg: npm run dev", "monitor: watch logs"]);
  const monitor = await waitFor(() => taskOf(session.pid, "watch logs"), 5_000, "the stub started no monitor task");
  const output = await answerOf(session, { type: "task_output", taskId: monitor.taskId });
  assert.deepEqual(taskRequestsOf(session.pid, "get_task_output").map((line) => line.request), [{ subtype: "get_task_output", task_id: monitor.taskId }],
    "task_output is not claude's get_task_output control request for that task");
  const text = stubTaskOutput("watch logs");
  assert.deepEqual(output, { output: text, totalBytes: Buffer.byteLength(text), truncated: false });
  await session.shutdown();
});

test("443 u5 stop_task and task_output without a taskId are refused before claude's stdin; claude's error comes back as the rejection", async () => {
  const { session } = await sessionAfter(["bg: npm run dev"]);
  const dev = await waitFor(() => taskOf(session.pid, "npm run dev"), 5_000, "the stub started no dev task");
  const lines = stdinOf(session.pid).length;
  for (const type of ["stop_task", "task_output"]) {
    for (const taskId of [undefined, ""]) {
      await assert.rejects(session.send({ type, ...(taskId === undefined ? {} : { taskId }) }), (error) => {
        assert.match(error.message, new RegExp(`^${type} needs the task id`), `${type} without a taskId: ${error.message}`);
        return true;
      });
    }
  }
  assert.equal(stdinOf(session.pid).length, lines, "a command without a taskId wrote to claude's stdin");

  await assert.rejects(session.send({ type: "stop_task", taskId: "bnosuch00" }), (error) => {
    assert.equal(error.message, "No running task with ID: bnosuch00", "claude's refusal of stop_task is not the rejection");
    return true;
  });
  await assert.rejects(session.send({ type: "task_output", taskId: "bnosuch00" }), (error) => {
    assert.equal(error.message, "No task found with ID: bnosuch00", "claude's refusal of get_task_output is not the rejection");
    return true;
  });
  assert.equal(alive(dev.taskPid), true);

  await session.shutdown();
  for (const type of ["stop_task", "task_output"]) {
    await assert.rejects(session.send({ type, taskId: dev.taskId }), (error) => {
      assert.match(error.message, new RegExp(`^${type} needs a running session`), `${type} on an exited child: ${error.message}`);
      return true;
    });
  }
});

test("443 u5 a new subscriber is first told of the running background tasks", async () => {
  const { session } = await sessionAfter(["bg: npm run dev", "monitor: watch logs"]);
  const dev = await waitFor(() => taskOf(session.pid, "npm run dev"), 5_000, "the stub started no dev task");
  const monitor = await waitFor(() => taskOf(session.pid, "watch logs"), 5_000, "the stub started no monitor task");
  await answerOf(session, { type: "stop_task", taskId: dev.taskId });
  await waitFor(() => !session.backgroundWork().some((work) => work.id === dev.taskId), 5_000, "the stopped task still runs");

  // A page that subscribes now (a reconnect) gets the running tasks at once — since u7 as
  // the one whole-list snapshot.
  const replayed = [];
  const unsubscribe = session.onEvent((event) => replayed.push(event));
  unsubscribe();
  assert.deepEqual(replayed.map((event) => [event.type, event.tasks?.map((task) => [task.taskId, task.kind, task.command, task.status])]),
    [[view.CLAUDE_BACKGROUND_TASKS_EVENT, [[monitor.taskId, "monitor", "watch logs", "running"]]]],
    "a new subscriber was not told of the running background task (and only of it)");
  await session.shutdown();
});

// u5b: a task the user stopped leaves nothing unsettled. claude (2.1.322) marks a killed
// shell task notified before it sends the SDK `task_notification { status: "stopped" }`,
// so no follow-up turn comes for it; a completed or failed one still gets that turn.

test("443 u5b a stopped background task's notification leaves nothing unsettled; a completed one still waits for claude's follow-up turn", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 5_000);
  const events = [
    init(),
    toolUse("call-stop", "Bash", { command: "npm run dev", run_in_background: true }), started("bstop0001", "call-stop", "dev server", true),
    toolUse("call-done", "Bash", { command: "npm test", run_in_background: true }), started("bdone0001", "call-done", "tests", true),
    result(),
    updated("bstop0001", { status: "killed", end_time: 4_500 }),
    notification("bstop0001", "call-stop", "stopped"),
  ].flatMap((frame) => translator.push(frame).events);
  assert.equal(translator.hasUnsettledSubagents(), false, "a stopped task's notification held the session unsettled: no follow-up turn comes for it");
  assert.deepEqual(taskEvents(events).filter((task) => task.taskId === "bstop0001").map((task) => [task.status, task.endedAt]), [["running", undefined], ["stopped", 4_500]]);
  assert.deepEqual(translator.runningBackgroundTasks().map((task) => task.taskId), ["bdone0001"]);

  translator.push(notification("bdone0001", "call-done", "completed"));
  assert.equal(translator.hasUnsettledSubagents(), true, "a completed task's notification no longer waits for claude's follow-up turn");
  assert.equal(translator.push(init()).claudeTurnStart, true, "claude's follow-up turn for the completed task did not open as a turn");
  assert.equal(translator.hasUnsettledSubagents(), false);
});

test("443 u5b after stop_task the session is settled: it forks, and the next turn ends without subagentsActive", async () => {
  const { session, events } = await sessionAfter(["bg: npm run dev", "and then?"]);
  const dev = await waitFor(() => taskOf(session.pid, "npm run dev"), 5_000, "the stub started no dev task");
  await answerOf(session, { type: "stop_task", taskId: dev.taskId });
  await waitFor(() => events.some((event) => event.type === view.CLAUDE_BACKGROUND_TASK_EVENT && event.task.taskId === dev.taskId && event.task.status === "stopped"),
    5_000, "the stopped task did not settle as stopped");
  // A barrier: claude answers control requests in order on stdout, so this answer comes after
  // every frame of the stop (task_updated, task_notification, background_tasks_changed).
  await answerOf(session, { type: "task_output", taskId: dev.taskId });
  assert.equal(session.hasUnsettledSubagents(), false, "the stopped task left the session unsettled");
  assert.ok(!claude.getClaudeSessionIdsWithUnsettledSubagents().includes(session.sessionId), "the lease / running list would report subagentsActive");

  const second = stdinOf(session.pid).filter((line) => line.type === "user")[1];
  assert.ok(second?.uuid, "the second message went to claude without its uuid");
  const fork = await claude.forkClaudeSession(session.sessionId, second.uuid).catch((error) => assert.fail(`the fork was refused: ${error.message}`));
  assert.equal(fork.cancelled, false);

  const before = events.length;
  await session.send({ type: "prompt", message: "after the stop" });
  await waitFor(() => settledCount(events.slice(before)) === 1, 5_000, "the turn after the stop did not end");
  for (const type of ["agent_settled", "prompt_done"]) {
    const ends = events.slice(before).filter((event) => event.type === type);
    assert.equal(ends.length, 1);
    assert.equal(ends[0].subagentsActive, undefined, `${type} after the stop carried subagentsActive: the chime, notification and push were held`);
  }
  await session.shutdown();
});

// u7: the page's tasks follow the server's whole list. A new subscriber (a reconnect, a
// session switched back to, a page after a pi-web restart) gets one `claude_background_tasks`
// snapshot of the running tasks, empty included, and the page replaces its tasks by it.

/** What a page subscribing now is told at once. */
function subscribeNow(session) {
  const told = [];
  session.onEvent((event) => told.push(event))();
  return told;
}

test("443 u7 a new subscriber gets the whole list of running tasks, and an empty one once the last task stopped", async () => {
  const { session } = await sessionAfter(["bg: npm run dev"]);
  const dev = await waitFor(() => taskOf(session.pid, "npm run dev"), 5_000, "the stub started no dev task");

  const running = subscribeNow(session);
  assert.deepEqual(running.map((event) => [event.type, event.tasks?.map((task) => [task.taskId, task.command, task.status])]),
    [[view.CLAUDE_BACKGROUND_TASKS_EVENT, [[dev.taskId, "npm run dev", "running"]]]],
    "a new subscriber was not given the whole list of running tasks (and nothing else)");

  await answerOf(session, { type: "stop_task", taskId: dev.taskId });
  await waitFor(() => session.backgroundWork().length === 0, 5_000, "the stopped task still runs");
  const none = subscribeNow(session);
  assert.deepEqual(none.map((event) => [event.type, event.tasks]), [[view.CLAUDE_BACKGROUND_TASKS_EVENT, []]],
    "with no task running a new subscriber was not told so: a task that ended while it was away would stay listed as running");
  await session.shutdown();
});

test("443 u7 the page's tasks are replaced by the whole list: a running task missing from it leaves the running list", () => {
  const task = (taskId, startedAt, status, endedAt) => ({ taskId, toolCallId: `call-${taskId}`, kind: "shell", command: `cmd ${taskId}`, status, startedAt, ...(endedAt === undefined ? {} : { endedAt }) });
  const before = { gone: task("gone", 100, "running"), kept: task("kept", 200, "running"), done: task("done", 50, "completed", 80) };
  const after = view.replaceClaudeBackgroundTasks(before, [task("kept", 200, "running"), task("fresh", 300, "running")]);
  assert.deepEqual(view.runningClaudeBackgroundTasks(after).map((entry) => entry.taskId), ["kept", "fresh"],
    "a task the server no longer runs is still listed as running, or a running one it reported is missing");
  assert.equal(after.done?.status, "completed", "a task that settled in view (its open panel's final state) was dropped");
  assert.deepEqual(view.runningClaudeBackgroundTasks(view.replaceClaudeBackgroundTasks(before, [])), [], "an empty list leaves a task running");
  // A task the page saw settle never goes back to running.
  assert.equal(view.replaceClaudeBackgroundTasks(before, [task("done", 50, "running")]).done.status, "completed");
});

// u8: self-review's non-blocking findings (#2 #4 #5 #6).

test("443 u8 a Stop's deadline with only a finished task awaiting claude's follow-up signals the group, asking no force stop", async () => {
  const { session, events } = await sessionAfter(["bg: npm run build"]);
  const build = await waitFor(() => taskOf(session.pid, "npm run build"), 5_000, "the stub started no build task");
  await openTurn(session, "stuck-finish: npm run build");
  await waitFor(() => events.some((event) => event.type === view.CLAUDE_BACKGROUND_TASK_EVENT && event.task.taskId === build.taskId && event.task.status === "completed"),
    5_000, "the task did not complete during the turn");
  // Precondition: the finished task awaits claude's follow-up turn, and nothing runs that a signal would end.
  assert.equal(session.hasUnsettledSubagents(), true);
  assert.deepEqual(session.backgroundWork(), []);

  await session.send({ type: "abort" });
  await waitFor(() => signalsOf(session.pid).some((entry) => entry.signal === "SIGINT"), claude.CLAUDE_INTERRUPT_DEADLINE_MS + 3_000,
    "the deadline sent no SIGINT though no background work was running");
  assert.ok(!events.some((event) => event.type === FORCE_STOP_EVENT), "a force stop was asked for with nothing to end");
  await waitFor(() => !session.isAlive(), 5_000, "the signalled child did not exit");
});

test("443 u8 a background task's notification as killed leaves nothing unsettled, as stopped does", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 6_000);
  [init(), toolUse("call-kill", "Bash", { command: "npm run dev", run_in_background: true }), started("bkill0001", "call-kill", "dev server", true), result(),
    notification("bkill0001", "call-kill", "killed")].forEach((frame) => translator.push(frame));
  assert.equal(translator.hasUnsettledSubagents(), false, "a killed task's notification held the session unsettled: no follow-up turn comes for it");
  assert.equal(translator.hasRunningBackgroundTasks(), false);
});

test("443 u8 fork is refused while a background subagent is unsettled, naming it", async () => {
  const record = await newRecord();
  const session = startUnitStubSession(record, "subagent-early", { PI_TEST_CLAUDE_STUB_HOLD_MS: "60000" });
  const events = [];
  session.onEvent((event) => events.push(event));
  await session.send({ type: "prompt", message: "launch it" });
  await waitFor(() => settledCount(events) === 1 && session.hasUnsettledSubagents()
    && runEvents(events).some((run) => run.description === AGENT_DESCRIPTION && run.background && run.status === "running"),
  10_000, "the parent turn never ended with its background subagent running");
  await assert.rejects(claude.forkClaudeSession(session.sessionId, "any"), (error) => {
    assert.match(error.message, /background subagent is still running/);
    assert.ok(error.message.includes(AGENT_DESCRIPTION), `the fork's refusal does not name the subagent: ${error.message}`);
    return true;
  });
  await session.shutdown();
});

test("443 u8 the stub's plain interrupt stops its background tasks; one sparing them leaves them running", async () => {
  const stub = spawn(path.join(binDir, "claude"), ["--session-id", randomUUID()], { cwd: workDir, stdio: ["pipe", "pipe", "ignore"] });
  stub.stdout.resume();
  const exited = new Promise((resolve) => stub.once("exit", resolve));
  try {
    const write = (event) => stub.stdin.write(`${JSON.stringify(event)}\n`);
    write({ type: "user", message: { role: "user", content: "bg: sleep forever" } });
    const task = await waitFor(() => taskOf(stub.pid, "sleep forever"), 5_000, "the stub started no task child");
    write({ type: "control_request", request_id: "spare", request: { subtype: "interrupt", spare_background_tasks: true } });
    await waitFor(() => stdinOf(stub.pid).some((line) => line.request_id === "spare"), 5_000, "the stub never read the sparing interrupt");
    await delay(200);
    assert.equal(alive(task.taskPid), true, "an interrupt with spare_background_tasks stopped the task");
    write({ type: "control_request", request_id: "plain", request: { subtype: "interrupt" } });
    await waitFor(() => !alive(task.taskPid), 5_000, "a plain interrupt left the task running");
  } finally {
    stub.stdin.end();
    await Promise.race([exited, delay(5_000)]);
    if (stub.exitCode === null) stub.kill("SIGKILL");
  }
});
