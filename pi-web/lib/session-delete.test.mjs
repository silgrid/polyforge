import "#test-env";
import assert from "node:assert/strict";
import { existsSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// deleteSessions() behind DELETE /api/sessions/[id]. Carried over from
// upstream's app/api/sessions/delete-route.test.mjs when the bulk route went
// with upstream's unmounted sidebar: the cascade and the
// re-pointing are the live route's.
const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { deleteSessions } = await jiti.import("./session-delete.ts");
const { invalidateSessionListCache, invalidateSessionPathCache } = await jiti.import("./session-reader.ts");

const TIME = "2026-10-01T00:00:00.000Z";

/** A session file as pi writes it: header, then one exchange; a subagent's carries pi-web's meta entry. */
async function writeSession(dir, name, { id, cwd, parentSession, subagentOf }) {
  const path = join(dir, `${name}.jsonl`);
  const lines = [{ type: "session", version: 3, id, timestamp: TIME, cwd, ...(parentSession ? { parentSession } : {}) }];
  if (subagentOf) {
    lines.push({
      type: "custom",
      customType: "pi-web:subagent",
      id: `${id}-meta`,
      parentId: null,
      timestamp: TIME,
      data: { version: 1, parentSessionId: subagentOf.id, parentSessionPath: subagentOf.path, profile: "Explore", description: "Look around" },
    });
  }
  lines.push(
    { type: "message", id: `${id}-u`, parentId: lines.at(-1).id ?? null, timestamp: TIME, message: { role: "user", content: `hello ${name}`, timestamp: Date.parse(TIME) } },
    { type: "message", id: `${id}-a`, parentId: `${id}-u`, timestamp: TIME, message: { role: "assistant", content: [{ type: "text", text: "ok" }], timestamp: Date.parse(TIME) } },
  );
  await writeFile(path, `${lines.map((line) => JSON.stringify(line)).join("\n")}\n`);
  return path;
}

async function scratchAgentDir(t) {
  const dir = await mkdtemp(join(tmpdir(), "pi-web-session-delete-"));
  const previousAgentDir = process.env.PI_CODING_AGENT_DIR;
  const previousRegistry = globalThis.__piSessions;
  process.env.PI_CODING_AGENT_DIR = dir;
  globalThis.__piSessions = new Map();
  invalidateSessionListCache();
  const ids = [];
  t.after(async () => {
    globalThis.__piSessions = previousRegistry;
    if (previousAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
    else process.env.PI_CODING_AGENT_DIR = previousAgentDir;
    for (const id of ids) invalidateSessionPathCache(id);
    invalidateSessionListCache();
    await rm(dir, { recursive: true, force: true });
  });
  const sessionsDir = join(dir, "sessions", "--work-alpha--");
  await mkdir(sessionsDir, { recursive: true });
  return { sessionsDir, ids };
}

test("deletes sessions with their subagents, shutting open ones down and re-pointing what stays", async (t) => {
  const { sessionsDir, ids } = await scratchAgentDir(t);
  const cwd = "/work/alpha";
  const keepRoot = await writeSession(sessionsDir, "keep-root", { id: "keep-root", cwd });
  const a = await writeSession(sessionsDir, "a", { id: "del-a", cwd, parentSession: keepRoot });
  const b = await writeSession(sessionsDir, "b", { id: "del-b", cwd, parentSession: a });
  const aSub = await writeSession(sessionsDir, "a-sub", { id: "del-a-sub", cwd, parentSession: a, subagentOf: { id: "del-a", path: a } });
  // A fork of b that is not deleted: it stays, re-pointed past b and a to the nearest ancestor left.
  const keepFork = await writeSession(sessionsDir, "keep-fork", { id: "keep-fork", cwd, parentSession: b });
  ids.push("keep-root", "del-a", "del-b", "del-a-sub", "keep-fork");

  const shutdowns = [];
  globalThis.__piSessions.set("del-b", {
    sessionId: "del-b",
    isRunning: () => false,
    isAwaitingInput: () => false,
    shutdown: async () => { shutdowns.push("del-b"); globalThis.__piSessions.delete("del-b"); },
  });

  const result = await deleteSessions(["del-a", "del-b", "gone-elsewhere"]);

  assert.deepEqual(result.deleted.toSorted(), ["del-a", "del-a-sub", "del-b"]);
  assert.deepEqual(result.missing, ["gone-elsewhere"]);
  for (const path of [a, b, aSub]) assert.equal(existsSync(path), false, path);
  for (const path of [keepRoot, keepFork]) assert.equal(existsSync(path), true, path);
  // The open wrapper was shut down before its file went.
  assert.deepEqual(shutdowns, ["del-b"]);
  const forkHeader = JSON.parse(readFileSync(keepFork, "utf8").split("\n")[0]);
  assert.equal(forkHeader.parentSession, keepRoot);
});

test("what stays is re-pointed at its nearest ancestor left, or loses a parent none of whose ancestors stay", async (t) => {
  const { sessionsDir, ids } = await scratchAgentDir(t);
  const cwd = "/work/alpha";
  const root = await writeSession(sessionsDir, "root", { id: "del-root", cwd });
  const middle = await writeSession(sessionsDir, "middle", { id: "keep-middle", cwd, parentSession: root });
  const child = await writeSession(sessionsDir, "child", { id: "del-child", cwd, parentSession: middle });
  const grandchild = await writeSession(sessionsDir, "grandchild", { id: "keep-grandchild", cwd, parentSession: child });
  // A subagent of keep-middle whose header names the deleted child: its meta follows the header.
  const sub = await writeSession(sessionsDir, "sub", { id: "keep-sub", cwd, parentSession: child, subagentOf: { id: "keep-middle", path: middle } });
  ids.push("del-root", "keep-middle", "del-child", "keep-grandchild", "keep-sub");

  const result = await deleteSessions(["del-root", "del-child"]);

  assert.deepEqual(result.deleted.toSorted(), ["del-child", "del-root"]);
  assert.equal(existsSync(root) || existsSync(child), false);
  const lines = (path) => readFileSync(path, "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
  // Nothing above the deleted root stays: the header loses its parent.
  assert.equal("parentSession" in lines(middle)[0], false);
  assert.equal(lines(grandchild)[0].parentSession, middle);
  const [subHeader, subMeta, ...subRest] = lines(sub);
  assert.equal(subHeader.parentSession, middle);
  assert.deepEqual([subMeta.data.parentSessionId, subMeta.data.parentSessionPath], ["keep-middle", middle]);
  assert.equal(subRest.length, 2, "the rest of the file is kept");
});

test("ids already gone delete nothing and say so", async (t) => {
  await scratchAgentDir(t);
  assert.deepEqual(await deleteSessions(["nobody", "nobody"]), { deleted: [], missing: ["nobody"] });
});

// polyforge#252: the allowed-roots cache is a 5 s scan of the session list, so
// a delete pi-web itself completed must not wait for that TTL to stop granting
// the deleted session's cwd. A failed delete keeps the cache as it was.
test("a successful delete invalidates the allowed-roots cache; a failed one leaves it", async (t) => {
  const { sessionsDir, ids } = await scratchAgentDir(t);
  globalThis.__piAllowedRootsCache = undefined;
  globalThis.__piAdditionalAllowedRoots = new Set();
  const cwdKeep = "/work/cache-keep";
  const cwdGone = "/work/cache-gone";
  await writeSession(sessionsDir, "cache-keep", { id: "cache-keep", cwd: cwdKeep });
  await writeSession(sessionsDir, "cache-gone", { id: "cache-gone", cwd: cwdGone });
  ids.push("cache-keep", "cache-gone");
  const { getAllowedFileRoots } = await jiti.import("./file-access.ts");

  const before = await getAllowedFileRoots();
  assert.equal(before.has(cwdGone), true, "the cache holds the deleted session's cwd before the delete");

  await deleteSessions(["cache-gone"]);
  const after = await getAllowedFileRoots();
  assert.equal(after.has(cwdGone), false, "the deleted session's cwd leaves the cache at once, not after the TTL");
  assert.equal(after.has(cwdKeep), true, "a surviving session's cwd stays allowed");

  // A non-ENOENT failure (the header read on a directory answers EISDIR) is
  // returned as it always was, and the cache is left untouched.
  const broken = await writeSession(sessionsDir, "cache-broken", { id: "cache-broken", cwd: "/work/cache-broken" });
  ids.push("cache-broken");
  invalidateSessionListCache();
  globalThis.__piAllowedRootsCache = undefined; // let the cache rebuild and pick the new session up
  await getAllowedFileRoots();
  const cacheBeforeFailure = globalThis.__piAllowedRootsCache;
  assert.equal(cacheBeforeFailure.roots.has("/work/cache-broken"), true, "the cache holds the third session's cwd");
  rmSync(broken, { force: true });
  mkdirSync(broken);
  await assert.rejects(deleteSessions(["cache-broken"]), (error) => error && error.code !== "ENOENT");
  assert.equal(globalThis.__piAllowedRootsCache, cacheBeforeFailure, "a failed delete leaves the cache as it was");
  assert.equal(globalThis.__piAllowedRootsCache.roots.has("/work/cache-broken"), true);
});
