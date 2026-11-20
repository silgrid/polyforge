// carries: lib/session-delete.ts, lib/bg-runtime.ts, lib/rpc-manager.ts
import "#test-env";
import assert from "node:assert/strict";
import { existsSync, mkdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const agentDir = join(tmpdir(), "bg-delete-" + Math.random().toString(36).slice(2));
mkdirSync(agentDir, { recursive: true });
process.env.PI_CODING_AGENT_DIR = agentDir;

// r1#1 put the executor's state on globalThis, so this test keeps the
// repo-standard moduleCache: false: deleteSessions (session-delete.ts) and
// this test hold two rpc-manager module instances over one shared executor.
const jiti = createJiti(import.meta.url, { interopDefault: true, moduleCache: false });
const { deleteSessions } = await jiti.import("./session-delete.ts");
const { getBgRuntime } = await jiti.import("./rpc-manager.ts");

const flush = async () => { for (let i = 0; i < 6; i += 1) await new Promise((resolve) => setImmediate(resolve)); };

const pollUntil = async (condition, deadlineMs, what) => {
  const deadline = Date.now() + deadlineMs;
  for (;;) {
    if (await condition()) return;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
};

function groupAlive(pgid) {
  if (!Number.isInteger(pgid) || pgid <= 0) return false;
  try {
    process.kill(-pgid, 0);
    return true;
  } catch (error) {
    return error.code === "EPERM";
  }
}

test.afterEach(() => {
  const runtime = getBgRuntime();
  for (const sessionId of ["bg-del-1", "bg-del-2"]) {
    for (const task of runtime.status(sessionId)) {
      try { runtime.stop(sessionId, task.id); } catch { /* terminal */ }
    }
  }
});

test.after(() => rmSync(agentDir, { recursive: true, force: true }));

test("deleting a session gates new tasks, stops its running task, clears the log directory and leaves nothing to revive", async () => {
  const runtime = getBgRuntime();
  const sessionId = "bg-del-1";
  // A session with no file on disk but a live background task.
  const info = runtime.run(sessionId, agentDir, "sleep 5");
  const logDir = join(agentDir, "pi-web-bg-tasks", sessionId);
  assert.ok(existsSync(logDir), "the task registered its log directory");

  const result = await deleteSessions([sessionId]);
  assert.deepEqual(result.missing, [sessionId], "the session itself reads as missing (it never had a file)");

  // The task's whole process group is gone (the stop's signals settle first).
  await pollUntil(() => !groupAlive(info.pgid ?? 0), 5_000, "the deleted session's task group to disappear");
  await pollUntil(() => runtime.status(sessionId).length === 0, 5_000, "the registry to clear");
  assert.equal(existsSync(logDir), false, "the session's log directory is cleaned");
  // The gate holds: no new task starts for a deleted session.
  assert.throws(() => runtime.run(sessionId, agentDir, "echo late"), /delet/i);
});
