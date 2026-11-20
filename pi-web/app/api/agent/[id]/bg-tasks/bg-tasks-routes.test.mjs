// carries: app/api/agent/[id]/bg-tasks/*, lib/bg-runtime.ts, lib/bg-protocol.ts, lib/rpc-manager.ts
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test, { after } from "node:test";
import { createJiti } from "jiti";

// The three bg-tasks routes answer from the built-in executor's registry
// (polyforge#173, AC2): every read is scoped to the session the URL names, the
// logs answer is a tail capped at 8 KiB naming truncated/tail, and kill is the
// executor's idempotent stop. The routes are exercised as handlers over a
// private agent dir whose registry is seeded with persisted records — the
// shape a restart leaves behind — and one real task started through the
// controller, so the running → stopped transition runs through the route too.
const agentDir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-bg-routes-"));
const projectDir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-bg-routes-project-"));
process.env.PI_CODING_AGENT_DIR = agentDir;
after(() => {
  fs.rmSync(agentDir, { recursive: true, force: true });
  fs.rmSync(projectDir, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { tsconfigPaths: true, interopDefault: true, moduleCache: false });
const { GET: listTasks } = await jiti.import("./route.ts");
const { GET: readLogs } = await jiti.import("./[taskId]/logs/route.ts");
const { POST: killTask } = await jiti.import("./[taskId]/kill/route.ts");
const { getBgRuntime } = await jiti.import("@/lib/rpc-manager");

const registryRoot = path.join(agentDir, "pi-web-bg-tasks");
const SESSION_A = "11111111-111-4111-8111-111111111111";
const SESSION_B = "22222222-222-4222-8222-222222222222";

/** A persisted record the way a previous process left it (the registry's own shape). */
function seedRecord(sessionId, taskId, fields) {
  const dir = path.join(registryRoot, sessionId, taskId);
  fs.mkdirSync(dir, { recursive: true });
  const logPath = path.join(dir, "log");
  const info = {
    id: taskId,
    sessionId,
    command: fields.command ?? `echo ${taskId}`,
    cwd: fields.cwd ?? projectDir,
    status: fields.status ?? "succeeded",
    startedAt: fields.startedAt ?? 1_700_000_000_000,
    endedAt: fields.endedAt ?? null,
    exitCode: fields.exitCode ?? null,
    signal: fields.signal ?? null,
    reason: fields.reason ?? null,
    bytesWritten: fields.bytesWritten ?? null,
    logPath,
    notified: fields.notified ?? true,
    ...(fields.pgid === undefined ? {} : { pgid: fields.pgid }),
  };
  fs.writeFileSync(path.join(dir, "task.json"), JSON.stringify(info));
  if (fields.log !== undefined) fs.writeFileSync(logPath, fields.log);
  return info;
}

function taskDir(sessionId, taskId) {
  return path.join(registryRoot, sessionId, taskId);
}

const list = async (sessionId) => {
  const response = await listTasks(new Request(`http://localhost/api/agent/${sessionId}/bg-tasks`), { params: Promise.resolve({ id: sessionId }) });
  const body = await response.json();
  return { status: response.status, body };
};
const logs = async (sessionId, taskId, query = "") => {
  const response = await readLogs(new Request(`http://localhost/api/agent/${sessionId}/bg-tasks/${taskId}/logs${query}`), { params: Promise.resolve({ id: sessionId, taskId }) });
  const body = await response.json();
  return { status: response.status, body };
};
const kill = async (sessionId, taskId) => {
  const response = await killTask(new Request(`http://localhost/api/agent/${sessionId}/bg-tasks/${taskId}/kill`, { method: "POST" }), { params: Promise.resolve({ id: sessionId, taskId }) });
  const body = await response.json();
  return { status: response.status, body };
};

test("the list answers only this session's records, live map and disk alike", async () => {
  // A settled record from a previous process (in the registry, not the live map)
  // and a record still marked running on disk (a restart nobody recovered yet):
  // both belong to A and read through A's own id only.
  seedRecord(SESSION_A, "task-settled", { status: "succeeded", endedAt: 1_700_000_030_000, exitCode: 0, startedAt: 1_700_000_000_000, log: "done\n" });
  seedRecord(SESSION_A, "task-orphan", { status: "running", startedAt: 1_700_000_010_000 });

  const a = await list(SESSION_A);
  assert.equal(a.status, 200, `list A -> ${a.status} ${JSON.stringify(a.body)}`);
  const ids = a.body.tasks.map((task) => task.id);
  assert.deepEqual(ids, ["task-settled", "task-orphan"], `oldest start first, both records present: ${JSON.stringify(ids)}`);
  const settled = a.body.tasks.find((task) => task.id === "task-settled");
  assert.equal(settled.status, "succeeded");
  assert.equal(settled.exitCode, 0);
  const orphan = a.body.tasks.find((task) => task.id === "task-orphan");
  // A persisted running record belongs to no live process group: it reads as
  // interrupted, exactly what a restart's recovery writes (#172's semantics).
  assert.equal(orphan.status, "interrupted", JSON.stringify(orphan));

  const b = await list(SESSION_B);
  assert.equal(b.status, 200);
  assert.deepEqual(b.body.tasks, [], "another session's list stays empty: no cross-session leak");
});

test("logs route returns the registered tail within the 8 KiB cap with truncated and tail fields", async () => {
  // 20 000 bytes of two-byte lines: the expected tail is hand-computed from
  // the seeded bytes, never from the implementation's own answer.
  const line = "0123456789";
  const big = line.repeat(2_000);
  seedRecord(SESSION_A, "task-logs", { status: "succeeded", log: big, startedAt: 1_700_000_005_000 });

  const capped = await logs(SESSION_A, "task-logs", "?maxBytes=100");
  assert.equal(capped.status, 200, JSON.stringify(capped.body));
  assert.equal(capped.body.text, big.slice(-100), "the last 100 bytes, kept to character boundaries");
  assert.equal(capped.body.startByte, 19_900);
  assert.equal(capped.body.endByte, 20_000);
  assert.equal(capped.body.totalBytes, 20_000);
  assert.equal(capped.body.truncated, true, "a tail short of the whole log names it");
  assert.equal(capped.body.tail, true, "the answer is a tail and says so");

  // A log inside the cap reads whole: a 400-byte tail, uncut and unmarked.
  const line400 = "x".repeat(400);
  seedRecord(SESSION_A, "task-logs-small", { status: "succeeded", log: line400, startedAt: 1_700_000_006_000 });
  const whole = await logs(SESSION_A, "task-logs-small");
  assert.equal(whole.status, 200);
  assert.equal(whole.body.text, line400, "a log inside the cap reads whole by default");
  assert.equal(whole.body.truncated, false);
  assert.equal(whole.body.tail, true);
  assert.equal(whole.body.startByte, 0);

  // A client asking past the cap gets the cap, not more (AC2: the result stays ≤ 8 KiB).
  const greedy = await logs(SESSION_A, "task-logs", "?maxBytes=999999");
  assert.equal(greedy.status, 200);
  assert.equal(Buffer.byteLength(greedy.body.text), 8_192, "clamped to the 8 KiB cap");
  assert.equal(greedy.body.startByte, 20_000 - 8_192);
  assert.equal(greedy.body.truncated, true);
});

test("a running task stops through the kill route, once and idempotently", async () => {
  const started = getBgRuntime().run(SESSION_A, projectDir, "sleep 30");
  assert.equal(started.status, "running");

  const listed = await list(SESSION_A);
  const live = listed.body.tasks.find((task) => task.id === started.id);
  assert.ok(live, "the just-started task is listed for its own session");
  assert.equal(live.status, "running");

  const stopped = await kill(SESSION_A, started.id);
  assert.equal(stopped.status, 200, JSON.stringify(stopped.body));
  assert.equal(stopped.body.task.status, "stopped", "the executor's stop settles the task at once");
  assert.equal(typeof stopped.body.message, "string");

  // The route waits for nobody: a repeat answers the same settled record.
  const again = await kill(SESSION_A, started.id);
  assert.equal(again.status, 200);
  assert.equal(again.body.task.status, "stopped");
  assert.equal(again.body.task.id, started.id);

  // The stop is real: the record on disk says stopped, the task is out of the live map's running set.
  const persisted = JSON.parse(fs.readFileSync(path.join(taskDir(SESSION_A, started.id), "task.json"), "utf8"));
  assert.equal(persisted.status, "stopped");
});

test("another session's task id reads as not found through every route, as JSON", async () => {
  for (const attempt of [
    () => logs(SESSION_B, "task-settled"),
    () => kill(SESSION_B, "task-settled"),
    () => logs(SESSION_B, "no-such-task"),
    () => kill(SESSION_A, "no-such-task"),
  ]) {
    const { status, body } = await attempt();
    assert.equal(status, 404, `expected 404, got ${status} ${JSON.stringify(body)}`);
    assert.equal(typeof body.error, "string", "every refusal is a decodable JSON body");
  }
});
