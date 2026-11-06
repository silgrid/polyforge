import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

// lib/session-archive.ts (wi pi#103, sidebar A1: Archive) — behavioral
// tests over injected storage, mirroring lib/session-filter.test.mjs: the
// archive-set state machine (load/save/prune/group) is DOM-free and fully
// drivable from node --test.

const jiti = createJiti(import.meta.url);
const {
  ARCHIVED_SESSIONS_STORAGE_KEY,
  loadArchivedSessionIds,
  saveArchivedSessionIds,
  pruneArchivedSessionIds,
  groupArchivedSessionsByProject,
} = await jiti.import("./session-archive.ts");

function memoryStorage(initial = new Map()) {
  return {
    getItem: (key) => (initial.has(key) ? initial.get(key) : null),
    setItem: (key, value) => { initial.set(key, String(value)); },
    removeItem: (key) => { initial.delete(key); },
  };
}

function throwingStorage() {
  return {
    getItem: () => { throw new Error("denied"); },
    setItem: () => { throw new Error("denied"); },
    removeItem: () => { throw new Error("denied"); },
  };
}

function archivedSession(overrides = {}) {
  return {
    path: "/sessions/a.jsonl",
    id: "a1",
    cwd: "/work/alpha-project",
    projectRoot: "/work/alpha-project",
    created: "2026-09-01T00:00:00.000Z",
    modified: "2026-09-01T00:00:00.000Z",
    messageCount: 2,
    firstMessage: "hello",
    ...overrides,
  };
}

test("the storage key follows the unread-ids precedent", () => {
  assert.equal(ARCHIVED_SESSIONS_STORAGE_KEY, "pi-web:archived-session-ids");
});

test("load reads a JSON string array; save writes one and removes the key when empty", () => {
  const storage = memoryStorage();
  saveArchivedSessionIds(new Set(["a", "b"]), storage);
  assert.equal(storage.getItem(ARCHIVED_SESSIONS_STORAGE_KEY), JSON.stringify(["a", "b"]));
  assert.deepEqual([...loadArchivedSessionIds(storage)].sort(), ["a", "b"]);
  saveArchivedSessionIds(new Set(), storage);
  assert.equal(storage.getItem(ARCHIVED_SESSIONS_STORAGE_KEY), null,
    "an empty set must REMOVE the key (the unread-ids precedent)");
});

test("load degrades to the empty set for absent, corrupt, non-array, and non-string payloads", () => {
  const emptyCases = new Map([
    [null, "absent"],
    ["", "empty string"],
    ["not json", "corrupt"],
    ["42", "non-array number"],
    ["{}", "non-array object"],
    [JSON.stringify([]), "empty array"],
  ]);
  for (const [raw] of emptyCases) {
    const storage = memoryStorage();
    if (raw !== null) storage.setItem(ARCHIVED_SESSIONS_STORAGE_KEY, raw);
    assert.equal(loadArchivedSessionIds(storage).size, 0, `raw=${raw}`);
  }
  // Non-string entries are dropped, valid ones survive (unread-ids filter).
  const mixed = memoryStorage();
  mixed.setItem(ARCHIVED_SESSIONS_STORAGE_KEY, JSON.stringify(["ok", 7, null]));
  assert.deepEqual([...loadArchivedSessionIds(mixed)], ["ok"]);
  // A JSON-encoded scalar string is tolerated as a single id (the e2e's
  // readArchiveSet tolerates raw scalars; the loader accepts the quoted form).
  const scalar = memoryStorage();
  scalar.setItem(ARCHIVED_SESSIONS_STORAGE_KEY, JSON.stringify("solo"));
  assert.deepEqual([...loadArchivedSessionIds(scalar)], ["solo"]);
  // A raw (non-JSON) scalar reads as empty — corrupt payload, best-effort.
  const rawScalar = memoryStorage();
  rawScalar.setItem(ARCHIVED_SESSIONS_STORAGE_KEY, "solo");
  assert.equal(loadArchivedSessionIds(rawScalar).size, 0);
});

test("null storage and throwing storage never throw and read as empty", () => {
  assert.equal(loadArchivedSessionIds(null).size, 0);
  assert.equal(loadArchivedSessionIds(throwingStorage()).size, 0);
  // Saves are swallowed, not propagated.
  saveArchivedSessionIds(new Set(["a"]), null);
  saveArchivedSessionIds(new Set(["a"]), throwingStorage());
  saveArchivedSessionIds(new Set(), throwingStorage());
});

test("prune is a pure set difference that keeps the reference when nothing matches", () => {
  const ids = new Set(["a", "b", "c"]);
  assert.equal(pruneArchivedSessionIds(ids, ["x", "y"]), ids,
    "no-op prune must return the same reference (React state identity)");
  const pruned = pruneArchivedSessionIds(ids, ["b", "not-there"]);
  assert.notEqual(pruned, ids);
  assert.deepEqual([...pruned].sort(), ["a", "c"]);
  // The input set is untouched (pure).
  assert.deepEqual([...ids].sort(), ["a", "b", "c"]);
  // Both reuse sites: external-deletion pruning and running force-unarchive.
  const runningPruned = pruneArchivedSessionIds(pruned, ["a"]);
  assert.deepEqual([...runningPruned], ["c"]);
});

test("groupArchivedSessionsByProject groups by projectRoot ?? cwd, freshest first", () => {
  const fresh = "2026-09-05T00:00:00.000Z";
  const mid = "2026-09-03T00:00:00.000Z";
  const old = "2026-09-01T00:00:00.000Z";
  const groups = groupArchivedSessionsByProject([
    archivedSession({ id: "a-old", modified: old, cwd: "/work/alpha-project" }),
    archivedSession({ id: "a-fresh", modified: fresh, cwd: "/work/alpha-project" }),
    // projectRoot wins over cwd when both exist (worktree sessions group
    // under the shared main repo root).
    archivedSession({ id: "w", modified: mid, cwd: "/work/other-wt", projectRoot: "/work/alpha-project" }),
    archivedSession({ id: "b-mid", modified: mid, cwd: "/work/beta-project", projectRoot: undefined }),
  ]);
  assert.deepEqual(groups.map((group) => group.root), [
    "/work/alpha-project", // freshest session (a-fresh) leads
    "/work/beta-project",
  ]);
  assert.deepEqual(groups[0].sessions.map((session) => session.id), ["a-fresh", "w", "a-old"]);
  assert.deepEqual(groups[1].sessions.map((session) => session.id), ["b-mid"]);
});

test("groupArchivedSessionsByProject is pure over its input and tolerates the empty list", () => {
  assert.deepEqual(groupArchivedSessionsByProject([]), []);
});
