import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const { SessionManager } = await jiti.import("@earendil-works/pi-coding-agent");
const {
  listSessionsIncremental,
  resetSessionScanIndexForTests,
} = await jiti.import("./session-list-scanner.ts");
const {
  disposeSessionScanWorkerPoolForTests,
  runSessionScanTask,
} = await jiti.import("./session-parse-worker-pool.ts");

const timestamp = "2026-01-01T00:00:00.000Z";
const line = (entry) => JSON.stringify(entry) + "\n";
const message = (id, role, content, time = timestamp) => ({
  type: "message", id, parentId: null, timestamp: time, message: { role, content },
});

// Unit tests for lib/session-parse-worker-pool.ts (polyforge#140): the session
// list's transcript parsing must run on the bounded worker pool, never on the
// server's main thread, and the scan's results must stay the SDK catalogue's.
// The pool records its completed reads into globalThis.__piWebScanTaskLog —
// the same facts the old fs.createReadStream interception observed.

function fixture(t) {
  const previousAgentDir = process.env.PI_CODING_AGENT_DIR;
  const root = fs.mkdtempSync(join(tmpdir(), "pi-web-scan-pool-"));
  const dir = join(root, "sessions", "project");
  fs.mkdirSync(dir, { recursive: true });
  process.env.PI_CODING_AGENT_DIR = root;
  resetSessionScanIndexForTests();
  const reads = [];
  globalThis.__piWebScanTaskLog = reads;
  t.after(() => {
    globalThis.__piWebScanTaskLog = undefined;
    resetSessionScanIndexForTests();
    if (previousAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
    else process.env.PI_CODING_AGENT_DIR = previousAgentDir;
    fs.rmSync(root, { recursive: true, force: true });
  });
  function write(id, entries = []) {
    const path = join(dir, `${id}.jsonl`);
    fs.writeFileSync(path, line({ type: "session", version: 3, id, cwd: root, timestamp }) + entries.map(line).join(""));
    return path;
  }
  return { root, dir, write, reads };
}

test("the list scan is parsed on the worker pool and matches the SDK catalogue", async (t) => {
  const { write, reads } = fixture(t);
  // A single 2 MiB JSON line: the parse the main thread used to stall on.
  const big = message("big", "assistant", "x".repeat(2 * 1024 * 1024));
  const a = write("pooled", [message("p1", "user", "first question"), big]);

  const listed = await listSessionsIncremental();
  assert.equal(listed.length, 1);
  assert.equal(listed[0].id, "pooled");
  assert.equal(listed[0].messageCount, 2);
  assert.equal(listed[0].firstMessage, "first question");
  assert.deepEqual(reads, [{ path: a, start: 0 }], "the scan ran as a pool task, reading from byte 0");

  // The row is the SDK catalogue's own metadata (allMessagesText aside).
  const sdk = (await SessionManager.listAll()).map((session) => {
    const info = { ...session };
    delete info.allMessagesText;
    return info;
  });
  assert.deepEqual(listed, sdk);
});

test("a file that only grew is resumed on the pool, from past its last scan", async (t) => {
  const { write, reads } = fixture(t);
  const a = write("resumed", [message("r1", "user", "first question")]);
  await listSessionsIncremental();
  fs.appendFileSync(a, line(message("r2", "assistant", "the reply")));
  reads.length = 0;

  const listed = await listSessionsIncremental();
  assert.equal(listed[0].messageCount, 2);
  assert.equal(reads.length, 1, "one pool task for the grown file");
  assert.equal(reads[0].path, a);
  assert.ok(reads[0].start > 0, `resumed past the previous scan, not from ${0}`);
});

test("a worker that cannot handle a task rejects it instead of hanging", async () => {
  await assert.rejects(runSessionScanTask({ kind: "not-a-task", path: "/nowhere" }), /unknown task kind/);
});

test.after(() => {
  disposeSessionScanWorkerPoolForTests();
});
