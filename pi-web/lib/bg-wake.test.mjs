// carries: lib/bg-runtime.ts, lib/bg-protocol.ts
import "#test-env";
import assert from "node:assert/strict";
import { readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { interopDefault: true, moduleCache: false });
const { createBgTaskController } = await jiti.import("./bg-runtime.ts");
const { BG_TASK_NOTIFICATION_TYPE } = await jiti.import("./bg-protocol.ts");

function makeFakeSupervisor() {
  const spawned = [];
  const spawnTask = (options) => {
    let resolveDone;
    const done = new Promise((resolve) => { resolveDone = resolve; });
    const entry = {
      options,
      task: {
        pgid: 400000 + spawned.length + 1,
        logPath: options.logPath,
        ready: Promise.resolve(),
        done,
        stop: async () => {},
      },
      resolveDone,
    };
    spawned.push(entry);
    return entry.task;
  };
  return { spawned, spawnTask };
}

// A session wrapper the wake path can talk to; its turn and its deliveries
// are the test's to control.
function makeWrapper({ alive = true, running = false } = {}) {
  const wrapper = {
    sent: [],
    alive,
    running,
    hold: null,
    isAlive: () => wrapper.alive,
    isRunning: () => wrapper.running,
    waitUntilReady: async () => {},
    sessionFile: "/tmp/wrapped.jsonl",
    inner: {
      sendCustomMessage: async (message, options) => {
        if (wrapper.hold) await wrapper.hold;
        wrapper.sent.push({ message, options });
      },
    },
  };
  return wrapper;
}

function makeDeps(overrides = {}) {
  const supervisor = makeFakeSupervisor();
  const root = join(tmpdir(), "bg-wake-test-" + Math.random().toString(36).slice(2));
  const calls = { resolveSessionPath: [], reopenSession: [] };
  const deps = {
    getLogRoot: () => root,
    spawnTask: supervisor.spawnTask,
    baseEnv: {},
    now: () => 1_700_000_000_000,
    getSession: overrides.getSession ?? (() => overrides.wrapper ?? undefined),
    resolveSessionPath: overrides.resolveSessionPath ?? (async (sessionId) => {
      calls.resolveSessionPath.push(sessionId);
      return overrides.sessionFile ?? "/tmp/sess-file.jsonl";
    }),
    reopenSession: overrides.reopenSession ?? (async (sessionId, sessionFile) => {
      calls.reopenSession.push({ sessionId, sessionFile });
      return overrides.reopened ?? makeWrapper();
    }),
    ...(overrides.wakeIdlePollMs !== undefined ? { wakeIdlePollMs: overrides.wakeIdlePollMs } : {}),
    ...(overrides.wakeIdleTimeoutMs !== undefined ? { wakeIdleTimeoutMs: overrides.wakeIdleTimeoutMs } : {}),
  };
  return { deps, supervisor, root, calls };
}

const flush = async () => { for (let i = 0; i < 6; i += 1) await new Promise((resolve) => setImmediate(resolve)); };

const pollUntil = async (condition, deadlineMs, what) => {
  const deadline = Date.now() + deadlineMs;
  for (;;) {
    if (await condition()) return;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
};

async function endTask(supervisor, index, result) {
  supervisor.spawned[index].resolveDone(result);
  await supervisor.spawned[index].task.done;
  await flush();
}

test("an idle session receives one notification carrying the exit status, duration and log path", async (t) => {
  const wrapper = makeWrapper();
  const { deps, supervisor, root } = makeDeps({ wrapper });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  const info = controller.run("sess-1", "/tmp/w", "make build");
  await endTask(supervisor, 0, { exitCode: 0, signal: null, bytesWritten: 12, reason: null });

  assert.equal(wrapper.sent.length, 1, "exactly one notification");
  const { message, options } = wrapper.sent[0];
  assert.equal(message.customType, BG_TASK_NOTIFICATION_TYPE);
  assert.ok(message.content.includes("exit code 0"), "the notification carries the exit code");
  assert.ok(message.content.includes("duration"), "the notification carries the duration");
  assert.ok(message.content.includes(info.logPath), "the notification carries the log path");
  assert.ok(message.content.includes("make build"), "the notification carries the command");
  assert.equal(options.deliverAs, "followUp");
  assert.equal(options.triggerTurn, true);
});

test("a session mid-turn receives the notification after its turn ends, not before", async (t) => {
  const wrapper = makeWrapper({ running: true });
  const { deps, supervisor, root } = makeDeps({ wrapper, wakeIdlePollMs: 10, wakeIdleTimeoutMs: 5_000 });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  controller.run("sess-1", "/tmp/w", "make test");
  await endTask(supervisor, 0, { exitCode: 0, signal: null, bytesWritten: 1, reason: null });

  // Mid-turn (a reply streaming, a compaction running): nothing is delivered.
  await new Promise((resolve) => setTimeout(resolve, 60));
  assert.equal(wrapper.sent.length, 0, "nothing is delivered while the session is busy");

  wrapper.running = false;
  await pollUntil(() => wrapper.sent.length === 1, 2_000, "the notification after the turn ends");
});

test("a turn that never ends still gets the notification, queued as a follow-up", async (t) => {
  const wrapper = makeWrapper({ running: true });
  const { deps, supervisor, root } = makeDeps({ wrapper, wakeIdlePollMs: 10, wakeIdleTimeoutMs: 50 });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  controller.run("sess-1", "/tmp/w", "make stuck");
  await endTask(supervisor, 0, { exitCode: 0, signal: null, bytesWritten: 0, reason: null });
  await pollUntil(() => wrapper.sent.length === 1, 2_000, "the notification past the wait deadline");
  assert.equal(wrapper.sent[0].options.deliverAs, "followUp");
});

test("a reaped wrapper (Stop's forced cleanup) is reopened and woken there", async (t) => {
  const dead = makeWrapper({ alive: false });
  const reopened = makeWrapper();
  const { deps, supervisor, root, calls } = makeDeps({
    getSession: () => dead,
    reopened,
    sessionFile: "/tmp/sess-file.jsonl",
  });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  controller.run("sess-1", "/tmp/w", "make build");
  await endTask(supervisor, 0, { exitCode: 0, signal: null, bytesWritten: 0, reason: null });

  assert.deepEqual(calls.reopenSession, [{ sessionId: "sess-1", sessionFile: "/tmp/sess-file.jsonl" }]);
  assert.equal(reopened.sent.length, 1, "the reopened wrapper receives the notification");
  assert.equal(dead.sent.length, 0);
});

test("a session whose file is gone is not woken and nothing throws", async (t) => {
  const { deps, supervisor, root } = makeDeps({ resolveSessionPath: async () => null });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  controller.run("sess-gone", "/tmp/w", "true");
  await endTask(supervisor, 0, { exitCode: 0, signal: null, bytesWritten: 0, reason: null });
  assert.equal(controller.hasActiveWork("sess-gone"), false);
});

test("several tasks ending at once each deliver exactly once", async (t) => {
  const wrapper = makeWrapper();
  const { deps, supervisor, root } = makeDeps({ wrapper });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  const first = controller.run("sess-1", "/tmp/w", "make a");
  const second = controller.run("sess-1", "/tmp/w", "make b");
  supervisor.spawned[0].resolveDone({ exitCode: 0, signal: null, bytesWritten: 0, reason: null });
  supervisor.spawned[1].resolveDone({ exitCode: 1, signal: null, bytesWritten: 0, reason: null });
  await Promise.all([supervisor.spawned[0].task.done, supervisor.spawned[1].task.done]);
  await pollUntil(() => wrapper.sent.length === 2, 2_000, "both notifications");

  const contents = wrapper.sent.map((entry) => entry.message.content);
  assert.equal(contents.filter((text) => text.includes("exit code 0")).length, 1, "the first task's notification");
  assert.equal(contents.filter((text) => text.includes("exit code 1")).length, 1, "the second task's notification");
  assert.ok(contents[0].includes(first.id.slice(0, 8)) || contents[1].includes(first.id.slice(0, 8)));
  assert.ok(contents[0].includes(second.id.slice(0, 8)) || contents[1].includes(second.id.slice(0, 8)));
  // Only one turn is triggered per notification; two tasks, two messages.
  assert.equal(wrapper.sent.filter((entry) => entry.options.triggerTurn).length, 2);
});

test("the once flag is on disk before the first byte of the delivery", async (t) => {
  const wrapper = makeWrapper();
  let release = () => {};
  wrapper.hold = new Promise((resolve) => { release = resolve; });
  const { deps, supervisor, root } = makeDeps({ wrapper });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  const info = controller.run("sess-1", "/tmp/w", "make build");
  await endTask(supervisor, 0, { exitCode: 0, signal: null, bytesWritten: 0, reason: null });

  assert.equal(wrapper.sent.length, 0, "the delivery is held");
  const persisted = JSON.parse(readFileSync(join(root, "sess-1", info.id, "task.json"), "utf8"));
  assert.equal(persisted.notified, true, "the once flag is persisted before the delivery");
  assert.equal(controller.hasActiveWork("sess-1"), true, "the wake is still owed while it is held");

  release();
  await pollUntil(() => wrapper.sent.length === 1, 2_000, "the held delivery to land");
  assert.equal(controller.hasActiveWork("sess-1"), false);
});

test("a deletion that begins while the wake waits for a busy session lands silently", async (t) => {
  const wrapper = makeWrapper({ running: true });
  const { deps, supervisor, root, calls } = makeDeps({ wrapper, wakeIdlePollMs: 10, wakeIdleTimeoutMs: 30_000 });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  controller.run("sess-gated", "/tmp/w", "make test");
  supervisor.spawned[0].resolveDone({ exitCode: 0, signal: null, bytesWritten: 0, reason: null });
  await flush();
  // Mid-wait (the session is busy), the session is deleted.
  controller.beginSessionDeletion(["sess-gated"]);
  wrapper.running = false;
  await new Promise((resolve) => setTimeout(resolve, 200));
  assert.equal(wrapper.sent.length, 0, "nothing is delivered to a session deleted mid-wait");
  assert.deepEqual(calls.reopenSession, [], "nothing is reopened for a session deleted mid-wait");
  assert.equal(controller.hasActiveWork("sess-gated"), false, "the wake settles silently");
});

test("a wrapper that dies while the wake waits is not sent the message", async (t) => {
  const wrapper = makeWrapper({ running: true });
  const { deps, supervisor, root } = makeDeps({ wrapper, wakeIdlePollMs: 10, wakeIdleTimeoutMs: 30_000, resolveSessionPath: async () => null });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  controller.run("sess-dying", "/tmp/w", "make test");
  supervisor.spawned[0].resolveDone({ exitCode: 0, signal: null, bytesWritten: 0, reason: null });
  await flush();
  // The wrapper dies mid-wait; its session has no file left.
  wrapper.alive = false;
  wrapper.running = false;
  await new Promise((resolve) => setTimeout(resolve, 200));
  assert.equal(wrapper.sent.length, 0, "no message goes to the wrapper that died mid-wait");
  assert.equal(controller.hasActiveWork("sess-dying"), false, "the wake settles");
});
