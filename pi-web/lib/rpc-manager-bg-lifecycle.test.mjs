// carries: lib/rpc-manager.ts, lib/bg-protocol.ts, lib/bg-tools.ts, lib/bg-runtime.ts, app/api/agent/[id]/route.ts
import "#test-env";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const agentDir = await mkdtemp(join(tmpdir(), "bg-rpc-manager-"));
process.env.PI_CODING_AGENT_DIR = agentDir;
await mkdir(join(agentDir, "sessions"), { recursive: true });

const jiti = createJiti(import.meta.url, { interopDefault: true, moduleCache: false });
const { startRpcSession, setRpcSessionTools, getBgRuntime, getRpcSession } = await jiti.import("./rpc-manager.ts");
const { PI_RESTART_REFUSED_CODE } = await jiti.import("./bg-protocol.ts");

const pollUntil = async (condition, deadlineMs, what) => {
  const deadline = Date.now() + deadlineMs;
  for (;;) {
    if (await condition()) return;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
};

const flush = async () => { for (let i = 0; i < 6; i += 1) await new Promise((resolve) => setImmediate(resolve)); };

const startedWrappers = [];
const startedTasks = [];
const usedSessionIds = new Set();

async function openSession(cwd, options = {}) {
  const id = `bg-lifecycle-${Math.random().toString(36).slice(2)}`;
  const started = await startRpcSession(id, "", cwd, options);
  startedWrappers.push(started.session);
  usedSessionIds.add(started.session.sessionId);
  return started;
}

/**
 * The wake path reopens a wrapper whose session a completed task belongs to
 * (Q11 A); the reopened wrapper's idle timer would otherwise hold the test
 * process for its full idle timeout, so every used session is reaped here.
 */
async function reapReopenedSessions() {
  for (const sessionId of usedSessionIds) {
    try {
      const reopened = await startRpcSession(sessionId, "", agentDir);
      reopened.session.destroy();
    } catch {
      // A session that cannot start again has nothing to reap.
    }
  }
  usedSessionIds.clear();
}

test.afterEach(async () => {
  const runtime = getBgRuntime();
  for (const { sessionId, id } of startedTasks.splice(0)) {
    try { runtime.stop(sessionId, id); } catch { /* already terminal */ }
  }
  for (const wrapper of startedWrappers.splice(0)) {
    try { await wrapper.destroy(); } catch { /* already gone */ }
  }
  await reapReopenedSessions();
});

test.after(() => rm(agentDir, { recursive: true, force: true }));

test("a normal session mounts the four background tools; a Chat-only session does not", async () => {
  const normal = await openSession(agentDir);
  await normal.session.waitUntilReady();
  const tools = await normal.session.send({ type: "get_tools" });
  const names = new Set(tools.map((tool) => tool.name));
  for (const name of ["bg_run", "bg_status", "bg_output", "bg_stop"]) {
    assert.ok(names.has(name), `the normal session must expose ${name}`);
    assert.equal(tools.find((tool) => tool.name === name).active, true, `${name} must be active`);
  }

  const chatOnly = await openSession(agentDir, { toolNames: [] });
  await chatOnly.session.waitUntilReady();
  const chatTools = await chatOnly.session.send({ type: "get_tools" });
  for (const name of ["bg_run", "bg_status", "bg_output", "bg_stop"]) {
    assert.ok(!chatTools.some((tool) => tool.name === name), `a Chat-only session must not expose ${name}`);
  }
});

test("a task's terminal state reaches the session's event stream under the bridge's event names", async () => {
  const runtime = getBgRuntime();
  const started = await openSession(agentDir);
  await started.session.waitUntilReady();
  const events = [];
  started.session.onEvent((event) => events.push(event));

  const info = runtime.run(started.session.sessionId, agentDir, "echo hello-events");
  startedTasks.push(info);
  await pollUntil(
    () => events.some((event) => event.type === "background_task_terminal" && event.task.id === info.id),
    15_000,
    "the terminal event on the session stream",
  );
  const terminal = events.find((event) => event.type === "background_task_terminal" && event.task.id === info.id);
  assert.equal(terminal.task.status, "completed", "the payload keeps the bridge's status vocabulary");
  assert.equal(terminal.task.outputPath, info.logPath, "the payload carries the log path");
  assert.ok(terminal.task.command.includes("hello-events"));
  await flush();
});

test("reload, clone, navigate and the Chat-only rebuild are refused with the structured code while a task runs", async () => {
  const runtime = getBgRuntime();
  const started = await openSession(agentDir);
  const sessionId = started.session.sessionId;
  usedSessionIds.add(sessionId);
  await started.session.waitUntilReady();

  const info = runtime.run(sessionId, agentDir, "sleep 2");
  startedTasks.push(info);
  await pollUntil(() => runtime.status(sessionId, info.id)[0].status === "running", 5_000, "the task to run");

  const refusesCoded = (promiseFactory) => assert.rejects(promiseFactory, (error) => {
    assert.equal(error.code, PI_RESTART_REFUSED_CODE, "the refusal carries the structured code");
    assert.ok(/background/.test(error.message), "the refusal names the background work");
    return true;
  });
  await refusesCoded(() => started.session.send({ type: "reload" }));
  await refusesCoded(() => started.session.send({ type: "clone" }));
  await refusesCoded(() => started.session.send({ type: "navigate_tree", targetId: "whatever" }));
  await refusesCoded(() => setRpcSessionTools(sessionId, started.session.sessionFile, []));

  // The refusal lands before anything is written: the session's entries hold
  // no new tool selection from the refused switch.
  const entriesBefore = started.session.inner.sessionManager.getEntries().filter(
    (entry) => entry.type === "custom" && entry.customType === "pi-web:tool-selection",
  ).length;
  await refusesCoded(() => setRpcSessionTools(sessionId, started.session.sessionFile, []));
  const entriesAfter = started.session.inner.sessionManager.getEntries().filter(
    (entry) => entry.type === "custom" && entry.customType === "pi-web:tool-selection",
  ).length;
  assert.equal(entriesAfter, entriesBefore, "a refused Chat-only switch writes no selection entry");

  // Once the work ends, the same commands go through again. The stop also
  // delivers the task's notification (AC1), whose turn must settle first —
  // a reload mid-turn is refused by the ordinary running check.
  await runtime.stop(sessionId, info.id);
  await pollUntil(() => runtime.status(sessionId, info.id)[0].status === "stopped", 5_000, "the task to stop");
  await pollUntil(() => !started.session.isRunning() && !runtime.hasActiveWork(sessionId), 15_000, "the notification turn and the wake to settle");
  const reloaded = await started.session.send({ type: "reload" });
  assert.equal(reloaded.success, true);
});

test("ending the wrapper (Stop's forced idle reap) leaves the background task running", async () => {
  const runtime = getBgRuntime();
  const started = await openSession(agentDir);
  const sessionId = started.session.sessionId;
  usedSessionIds.add(sessionId);
  await started.session.waitUntilReady();

  const info = runtime.run(sessionId, agentDir, "sleep 1; echo survived");
  startedTasks.push(info);
  await started.session.destroy();
  // The wrapper is gone; the task it started is not: it runs to its natural
  // end and succeeds, which no collateral kill could let it do.
  const deadline = Date.now() + 15_000;
  let task = runtime.status(sessionId, info.id)[0];
  while (task.status !== "succeeded" && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 20));
    task = runtime.status(sessionId, info.id)[0];
  }
  assert.equal(task.status, "succeeded", "the orphaned task runs to its natural end");
  assert.equal(task.exitCode, 0);
});

test("a wrapper that is no longer alive is refused a Chat-only rebuild under running work", async (t) => {
  const runtime = getBgRuntime();
  const started = await openSession(agentDir);
  const sessionId = started.session.sessionId;
  usedSessionIds.add(sessionId);
  await started.session.waitUntilReady();
  const info = runtime.run(sessionId, agentDir, "sleep 2");
  startedTasks.push(info);
  await pollUntil(() => runtime.status(sessionId, info.id)[0].status === "running", 5_000, "the task to run");
  // A real session file, so the refused branch would truly rebuild on the old code (N1).
  const deadFile = join(agentDir, "sessions", `${sessionId}.jsonl`);
  await writeFile(deadFile, `${JSON.stringify({ type: "session", version: 3, id: sessionId, timestamp: new Date().toISOString(), cwd: agentDir })}\n`);
  await started.session.destroy();
  // The closing wrapper leaves the registry on its own clock; wait it out so
  // the assertion sees a rebuild, not a disposal still in flight.
  await pollUntil(() => {
    const wrapper = getRpcSession(sessionId);
    return wrapper === undefined || !wrapper.isAlive();
  }, 10_000, "the destroyed wrapper to leave the registry");

  await assert.rejects(
    () => setRpcSessionTools(sessionId, deadFile, []),
    (error) => {
      assert.equal(error.code, PI_RESTART_REFUSED_CODE, "the dead-wrapper branch refuses with the structured code");
      return true;
    },
  );
  assert.ok(getRpcSession(sessionId) === undefined || !getRpcSession(sessionId).isAlive(), "no wrapper is rebuilt for the refused switch");

  // The task still runs (Stop's cleanup took the wrapper, not the task) and
  // is stopped here; its wake finds no session file and settles at once.
  runtime.stop(sessionId, info.id);
  await pollUntil(() => runtime.status(sessionId, info.id)[0].status === "stopped", 5_000, "the task to stop");
  void t;
});
