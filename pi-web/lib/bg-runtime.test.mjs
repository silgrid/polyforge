// carries: lib/bg-runtime.ts, lib/bg-protocol.ts
import "#test-env";
import assert from "node:assert/strict";
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { interopDefault: true, moduleCache: false });
const { createBgTaskController } = await jiti.import("./bg-runtime.ts");
const { BG_LOG_MAX_BYTES, BG_MAX_CONCURRENT_PER_SESSION, BG_TASK_NOTIFICATION_TYPE } = await jiti.import("./bg-protocol.ts");
const { hasActiveSessionLivenessProvider } = await jiti.import("./session-liveness.ts");
const { sanitizeProjectCommandEnvironment } = await jiti.import("./project-command-env.ts");

// A fake process layer: every spawn is recorded, every task's end and stop are
// the test's to decide.
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
        stopCalls: 0,
        stop: async () => {
          entry.task.stopCalls += 1;
          resolveDone({ exitCode: null, signal: "SIGTERM", bytesWritten: 3, reason: "stopped" });
        },
      },
      resolveDone,
    };
    spawned.push(entry);
    return entry.task;
  };
  return { spawned, spawnTask };
}

function makeDeps(overrides = {}) {
  const supervisor = overrides.supervisor ?? makeFakeSupervisor();
  const root = join(tmpdir(), "bg-runtime-test-" + Math.random().toString(36).slice(2));
  const calls = { resolveSessionPath: [], reopenSession: [] };
  const deps = {
    getLogRoot: () => root,
    spawnTask: supervisor.spawnTask,
    baseEnv: { MYVAR: "1", PI_WEB_PASSWORD: "secret", PATH: "/usr/bin" },
    now: () => 1_700_000_000_000,
    getSession: overrides.getSession ?? (() => undefined),
    resolveSessionPath: overrides.resolveSessionPath ?? (async (sessionId) => {
      calls.resolveSessionPath.push(sessionId);
      return null;
    }),
    reopenSession: overrides.reopenSession ?? (async (sessionId, sessionFile) => {
      calls.reopenSession.push({ sessionId, sessionFile });
      throw new Error("unused");
    }),
  };
  return { deps, supervisor, root, calls };
}

function readPersisted(root, sessionId, taskId) {
  return JSON.parse(readFileSync(join(root, sessionId, taskId, "task.json"), "utf8"));
}

const flush = async () => { for (let i = 0; i < 4; i += 1) await new Promise((resolve) => setImmediate(resolve)); };

test("bgRun returns the id at once, spawns sanitized in the session cwd and registers the task on disk", (t) => {
  const { deps, supervisor, root } = makeDeps();
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  const info = controller.run("sess-1", "/tmp/w", "echo hi");

  assert.ok(info.id.length > 0, "the task id must be non-empty");
  assert.equal(info.status, "running");
  assert.equal(info.sessionId, "sess-1");
  assert.equal(info.command, "echo hi");
  assert.equal(info.cwd, "/tmp/w");
  assert.equal(info.startedAt, 1_700_000_000_000);
  assert.equal(info.logPath, join(root, "sess-1", info.id, "log"));
  assert.equal(info.notified, false);

  // One spawn, with the sanitized environment and the session cwd.
  assert.equal(supervisor.spawned.length, 1);
  const spawnOptions = supervisor.spawned[0].options;
  assert.equal(spawnOptions.command, "echo hi");
  assert.equal(spawnOptions.cwd, "/tmp/w");
  assert.equal(spawnOptions.logPath, info.logPath);
  assert.equal(spawnOptions.logMaxBytes, BG_LOG_MAX_BYTES);
  const expectedEnv = sanitizeProjectCommandEnvironment(deps.baseEnv);
  assert.deepEqual(spawnOptions.env, expectedEnv);
  assert.equal(spawnOptions.env.PI_WEB_PASSWORD, undefined, "the web password must not reach the task");
  assert.equal(spawnOptions.env.MYVAR, "1");

  // Registered on disk, layered per session and task (Q3 A).
  const persisted = readPersisted(root, "sess-1", info.id);
  assert.equal(persisted.id, info.id);
  assert.equal(persisted.status, "running");
  assert.equal(persisted.sessionId, "sess-1");
});

test("the fifth concurrent task of one session is refused with the reason", (t) => {
  const { deps, root } = makeDeps();
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  for (let index = 0; index < BG_MAX_CONCURRENT_PER_SESSION; index += 1) {
    controller.run("sess-1", "/tmp/w", `task-${index}`);
  }
  assert.throws(
    () => controller.run("sess-1", "/tmp/w", "task-4"),
    /4/,
    "the refusal must name the concurrent-task limit",
  );
  // The limit is per session: another session still starts.
  const other = controller.run("sess-2", "/tmp/w", "other");
  assert.equal(other.status, "running");
});

test("a task's terminal transition is persisted once and reaches the sink", async (t) => {
  const { deps, supervisor, root } = makeDeps();
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  const terminals = [];
  const updates = [];
  controller.setSink({
    terminal: (sessionId, task) => terminals.push({ sessionId, task }),
    update: (sessionId, tasks) => updates.push({ sessionId, tasks }),
  });

  const ok = controller.run("sess-1", "/tmp/w", "true");
  supervisor.spawned[0].resolveDone({ exitCode: 0, signal: null, bytesWritten: 2, reason: null });
  await supervisor.spawned[0].task.done;
  await flush();
  const succeeded = controller.status("sess-1", ok.id)[0];
  assert.equal(succeeded.status, "succeeded");
  assert.equal(succeeded.exitCode, 0);
  assert.equal(succeeded.endedAt, 1_700_000_000_000);
  assert.equal(readPersisted(root, "sess-1", ok.id).status, "succeeded");
  assert.equal(terminals.length, 1);
  assert.equal(terminals[0].sessionId, "sess-1");
  assert.equal(terminals[0].task.id, ok.id);
  assert.ok(updates.length >= 1);

  const bad = controller.run("sess-1", "/tmp/w", "false");
  supervisor.spawned[1].resolveDone({ exitCode: 2, signal: null, bytesWritten: 0, reason: null });
  await supervisor.spawned[1].task.done;
  await flush();
  assert.equal(controller.status("sess-1", bad.id)[0].status, "failed");
  assert.equal(controller.status("sess-1", bad.id)[0].exitCode, 2);

  const capped = controller.run("sess-1", "/tmp/w", "yes");
  supervisor.spawned[2].resolveDone({ exitCode: null, signal: null, bytesWritten: BG_LOG_MAX_BYTES, reason: "log-limit" });
  await supervisor.spawned[2].task.done;
  await flush();
  const limited = controller.status("sess-1", capped.id)[0];
  assert.equal(limited.status, "failed", "a log past its cap ends the task failed");
  assert.equal(limited.reason, "log-limit");
  assert.equal(readPersisted(root, "sess-1", capped.id).reason, "log-limit");
});

test("a stop marks the task stopped at once, once, whatever the process layer reports later", async (t) => {
  const { deps, supervisor, root } = makeDeps();
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  const info = controller.run("sess-1", "/tmp/w", "sleep 300");
  const stopped = controller.stop("sess-1", info.id);
  assert.equal(stopped.status, "stopped");
  assert.equal(supervisor.spawned[0].task.stopCalls, 1);

  // Idempotent: a second stop changes nothing and stops nothing again.
  const again = controller.stop("sess-1", info.id);
  assert.equal(again.status, "stopped");
  assert.equal(supervisor.spawned[0].task.stopCalls, 1);

  // A late report from the process layer cannot resurrect or re-settle the task.
  supervisor.spawned[0].resolveDone({ exitCode: 0, signal: null, bytesWritten: 0, reason: null });
  await supervisor.spawned[0].task.done;
  await flush();
  assert.equal(controller.status("sess-1", info.id)[0].status, "stopped");
});

test("tasks are private to their session", (t) => {
  const { deps, root } = makeDeps();
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  const info = controller.run("sess-1", "/tmp/w", "echo hi");
  assert.deepEqual(controller.status("sess-2"), [], "another session sees no tasks");
  assert.throws(() => controller.status("sess-2", info.id), /not found/i);
  assert.throws(() => controller.stop("sess-2", info.id), /not found/i);
  assert.throws(() => controller.readOutput("sess-2", info.id), /not found/i);
});

test("a restart recovers registered running tasks as interrupted, without signalling or waking", (t) => {
  const { deps, supervisor, root, calls } = makeDeps();
  t.after(() => rmSync(root, { recursive: true, force: true }));
  // A registry a previous pi-web process left behind: one running task.
  mkdirSync(join(root, "sess-old", "task-1"), { recursive: true });
  writeFileSync(join(root, "sess-old", "task-1", "log"), "partial output\n");
  const prior = {
    id: "task-1", sessionId: "sess-old", command: "build", cwd: "/tmp/w",
    status: "running", startedAt: 1, endedAt: null, exitCode: null, signal: null,
    reason: null, bytesWritten: 15, logPath: join(root, "sess-old", "task-1", "log"), notified: false,
  };
  writeFileSync(join(root, "sess-old", "task-1", "task.json"), JSON.stringify(prior));

  const controller = createBgTaskController(deps);
  const recovered = controller.status("sess-old");
  assert.equal(recovered.length, 1);
  assert.equal(recovered[0].status, "interrupted", "a registered task no process owns reads as interrupted");
  assert.equal(readPersisted(root, "sess-old", "task-1").status, "interrupted");
  // No signal to any old pid (there is none to send to) and no wake.
  assert.equal(supervisor.spawned.length, 0);
  assert.deepEqual(calls.resolveSessionPath, []);
  assert.deepEqual(calls.reopenSession, []);
  assert.equal(controller.hasActiveWork("sess-old"), false, "an interrupted task holds nothing");
});

test("the deletion gate refuses new tasks, keeps the completion silent, and the cleanup clears everything", async (t) => {
  const { deps, supervisor, root, calls } = makeDeps();
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  const info = controller.run("sess-1", "/tmp/w", "sleep 300");
  const runner = controller.run("sess-1", "/tmp/w", "sleep 600");
  controller.beginSessionDeletion(["sess-1"]);

  assert.throws(() => controller.run("sess-1", "/tmp/w", "echo late"), /delet/i);
  // Another session is unaffected.
  controller.run("sess-2", "/tmp/w", "echo ok");

  // A task ending under the gate is never delivered: no path resolution, no reopen.
  supervisor.spawned[0].resolveDone({ exitCode: 0, signal: null, bytesWritten: 0, reason: null });
  await supervisor.spawned[0].task.done;
  await flush();
  assert.deepEqual(calls.resolveSessionPath, []);
  assert.equal(readPersisted(root, "sess-1", info.id).notified, true, "the gate counts as notified: nothing will ever be delivered");

  await controller.deleteSessionTasks(["sess-1"]);
  // The task still running when the cleanup arrives is stopped.
  assert.equal(supervisor.spawned[1].task.stopCalls, 1, "the cleanup stops the running task");
  void runner;
  assert.equal(existsSync(join(root, "sess-1")), false, "the session's log directory is gone");
  assert.deepEqual(controller.status("sess-1"), []);
});

test("the session liveness follows the work: active while a task runs or a completion is pending, gone when it ends", async (t) => {
  let releaseResolution = () => {};
  const holding = makeDeps({
    resolveSessionPath: async () => new Promise((resolve) => { releaseResolution = resolve; }),
  });
  t.after(() => rmSync(holding.root, { recursive: true, force: true }));
  const controller = createBgTaskController(holding.deps);
  const sessionId = "sess-live";
  assert.equal(hasActiveSessionLivenessProvider({ sessionId }), false);

  controller.run(sessionId, "/tmp/w", "sleep 1");
  assert.equal(hasActiveSessionLivenessProvider({ sessionId }), true, "a running task holds the session");

  holding.supervisor.spawned[0].resolveDone({ exitCode: 0, signal: null, bytesWritten: 0, reason: null });
  await holding.supervisor.spawned[0].task.done;
  await flush();
  assert.equal(hasActiveSessionLivenessProvider({ sessionId }), true, "an undelivered completion still holds the session");
  assert.equal(controller.hasActiveWork(sessionId), true);

  releaseResolution(null); // the session file resolves to nothing: delivery ends
  await flush();
  assert.equal(hasActiveSessionLivenessProvider({ sessionId }), false, "the hold ends with the delivery");
  assert.equal(controller.hasActiveWork(sessionId), false);
});

test("the completion notification goes to the session as a custom message with triggerTurn", async (t) => {
  const sent = [];
  const wrapper = {
    isAlive: () => true,
    isRunning: () => false,
    waitUntilReady: async () => {},
    sessionFile: "/tmp/sess.jsonl",
    inner: {
      sendCustomMessage: async (message, options) => {
        sent.push({ message, options });
      },
    },
  };
  const { deps, supervisor, root } = makeDeps({ getSession: () => wrapper });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const controller = createBgTaskController(deps);
  controller.run("sess-w", "/tmp/w", "echo done");
  supervisor.spawned[0].resolveDone({ exitCode: 0, signal: null, bytesWritten: 9, reason: null });
  await supervisor.spawned[0].task.done;
  await flush();
  assert.equal(sent.length, 1, "exactly one notification");
  assert.equal(sent[0].message.customType, BG_TASK_NOTIFICATION_TYPE);
  assert.equal(sent[0].options.deliverAs, "followUp");
  assert.equal(sent[0].options.triggerTurn, true);
  assert.ok(sent[0].message.content.length > 0, "the notification carries text");
});
