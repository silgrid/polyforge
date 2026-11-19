// carries: lib/bg-runtime.ts, lib/rpc-manager.ts
import "#test-env";
import assert from "node:assert/strict";
import { mkdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// r1#1: BG_RUNTIME's state (tasks, deletion gate, liveness) must survive a
// Next.js hot reload, which instantiates rpc-manager a second time: two module
// instances, one executor. Each fresh import here stands in for the reloaded
// module — same process, same agent dir, same root.
const agentDir = join(tmpdir(), "bg-global-" + Math.random().toString(36).slice(2));
mkdirSync(join(agentDir, "sessions"), { recursive: true });
process.env.PI_CODING_AGENT_DIR = agentDir;

const pollUntil = async (condition, deadlineMs, what) => {
  const deadline = Date.now() + deadlineMs;
  for (;;) {
    if (await condition()) return;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
};

const flush = async () => { for (let i = 0; i < 6; i += 1) await new Promise((resolve) => setImmediate(resolve)); };

test.after(() => rmSync(agentDir, { recursive: true, force: true }));

test("two rpc-manager instances share one executor: one gate, one task registry, recovery runs once", async (t) => {
  const jitiA = createJiti(import.meta.url, { interopDefault: true, moduleCache: false });
  const { getBgRuntime: runtimeA } = await jitiA.import("./rpc-manager.ts");
  const first = runtimeA();

  // A live task started through the first instance.
  const info = first.run("sess-shared", agentDir, "sleep 2");
  t.after(() => { try { first.stop("sess-shared", info.id); } catch { /* terminal */ } });

  // The second module instance (the hot-reloaded rpc-manager) constructs its
  // own BG_RUNTIME: the recovery pass must not rewrite the live task as
  // interrupted, and both instances must see the same registry.
  const jitiB = createJiti(import.meta.url, { interopDefault: true, moduleCache: false });
  const { getBgRuntime: runtimeB } = await jitiB.import("./rpc-manager.ts");
  const second = runtimeB();
  assert.notEqual(first, second, "the two module instances hold two controller objects");
  assert.equal(second.status("sess-shared", info.id)[0].status, "running", "a live task survives the second instance's recovery pass");

  // The deletion gate is one: what one instance blocks, the other cannot start.
  first.beginSessionDeletion(["sess-gated"]);
  assert.throws(() => second.run("sess-gated", agentDir, "echo hi"), /delet/i);

  // The registry is one: the second instance stops the first one's task.
  second.stop("sess-shared", info.id);
  await pollUntil(() => second.status("sess-shared", info.id)[0].status === "stopped", 5_000, "the shared task to stop");
  assert.equal(first.status("sess-shared", info.id)[0].status, "stopped");
  await flush();
});
