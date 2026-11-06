import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { createJiti } from "jiti";

// the Messages project-group collapse state (#212 paradigm).
const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-messages-collapse.ts");
const KEY = lib.POLYFORGE_MESSAGES_COLLAPSE_KEY;
const names = (set) => [...set].sort();

function withWindow(storage, fn) {
  const events = [];
  const previous = globalThis.window;
  globalThis.window = { localStorage: storage, dispatchEvent: (event) => { events.push(event.type); return true; } };
  try { return fn(events); } finally { globalThis.window = previous; }
}
const memoryStorage = (initial = {}) => {
  const data = new Map(Object.entries(initial));
  return { getItem: (k) => (data.has(k) ? data.get(k) : null), setItem: (k, v) => data.set(k, String(v)), data };
};
const throwing = { getItem() { throw new Error("denied"); }, setItem() { throw new Error("denied"); } };

test("the key and the event are the documented namespaced ones, distinct from #212 and #222", () => {
  assert.equal(KEY, "pi-web:polyforge-messages-collapsed");
  assert.notEqual(KEY, "pi-web:polyforge-config-disclosure");
  assert.notEqual(KEY, "pi-web:polyforge-messages-list-width");
  assert.equal(lib.POLYFORGE_MESSAGES_COLLAPSE_NONE.size, 0, "groups default to expanded");
});

test("missing, corrupt, non-array and wrong-typed values read as nothing collapsed", () => {
  for (const raw of [null, undefined, "", "{", "{not-json", "null", "{}", "42", "\"alpha\"", 7, {}, "[1, true, null, {}]", "[\"\", \"  \"]"]) {
    assert.deepEqual(names(lib.parsePolyforgeMessagesCollapse(raw)), [], String(raw));
  }
  assert.deepEqual(names(lib.parsePolyforgeMessagesCollapse(JSON.stringify(["beta", 3, " alpha ", "beta"]))), ["alpha", "beta"],
    "string entries are kept trimmed and once; others are dropped");
});

test("reads go through storage and survive a throwing storage", () => {
  withWindow(memoryStorage({ [KEY]: JSON.stringify(["alpha"]) }), () => {
    assert.deepEqual(names(lib.readCollapsedProjects()), ["alpha"]);
  });
  withWindow(throwing, () => assert.deepEqual(names(lib.readCollapsedProjects()), []));
});

test("a write toggles one project, preserves the others (stale names included) and announces it", () => {
  const storage = memoryStorage({ [KEY]: JSON.stringify(["ghost-project", "beta"]) });
  withWindow(storage, (events) => {
    assert.deepEqual(names(lib.writeProjectCollapsed("alpha", true)), ["alpha", "beta", "ghost-project"]);
    assert.deepEqual(JSON.parse(storage.data.get(KEY)), ["alpha", "beta", "ghost-project"]);
    lib.writeProjectCollapsed("beta", false);
    assert.deepEqual(JSON.parse(storage.data.get(KEY)), ["alpha", "ghost-project"], "a stale name is kept, never acted on");
    assert.deepEqual(events, [lib.POLYFORGE_MESSAGES_COLLAPSE_EVENT, lib.POLYFORGE_MESSAGES_COLLAPSE_EVENT]);
  });
});

test("with storage refusing a toggle still collapses for the page session, announces it and reads back stable", () => {
  withWindow(throwing, (events) => {
    assert.doesNotThrow(() => lib.writeProjectCollapsed("alpha", true));
    assert.deepEqual(events, [lib.POLYFORGE_MESSAGES_COLLAPSE_EVENT]);
    const first = lib.readCollapsedProjects();
    assert.deepEqual(names(first), ["alpha"], "the group collapsed despite the refused write");
    assert.equal(lib.readCollapsedProjects(), first, "the snapshot is the same object until the next toggle");
    lib.writeProjectCollapsed("beta", true);
    assert.deepEqual(names(lib.readCollapsedProjects()), ["alpha", "beta"], "toggles accumulate");
    lib.writeProjectCollapsed("alpha", false);
    assert.deepEqual(names(lib.readCollapsedProjects()), ["beta"], "and expand again");
  });
  // setItem alone refusing (quota) behaves the same.
  const quota = { getItem: () => null, setItem() { throw new Error("quota"); } };
  withWindow(quota, () => {
    lib.writeProjectCollapsed("gamma", true);
    assert.ok(lib.readCollapsedProjects().has("gamma"));
  });
  // Once storage keeps a write it is the truth again (another tab's write shows).
  const storage = memoryStorage();
  withWindow(storage, () => {
    lib.writeProjectCollapsed("delta", true);
    assert.ok(JSON.parse(storage.data.get(KEY)).includes("delta"), "the kept write lands in storage");
    storage.setItem(KEY, JSON.stringify(["epsilon"]));
    assert.deepEqual(names(lib.readCollapsedProjects()), ["epsilon"]);
  });
});

test("a stored read is the same object until the stored string changes", () => {
  const storage = memoryStorage({ [KEY]: JSON.stringify(["alpha"]) });
  withWindow(storage, () => {
    const first = lib.readCollapsedProjects();
    assert.equal(lib.readCollapsedProjects(), first);
    storage.setItem(KEY, JSON.stringify([]));
    assert.notEqual(lib.readCollapsedProjects(), first);
    assert.deepEqual(names(lib.readCollapsedProjects()), []);
  });
});

test("the module and the hook stay browser-only: no fetch, no server, both events heard, none-collapsed server snapshot", async () => {
  const source = await readFile(new URL("./polyforge-messages-collapse.ts", import.meta.url), "utf8");
  const hook = await readFile(new URL("../hooks/usePolyforgeMessagesCollapse.ts", import.meta.url), "utf8");
  for (const text of [source, hook]) {
    assert.doesNotMatch(text, /\bfetch\(|from\s+["']node:|\/api\//);
  }
  assert.match(hook, /addEventListener\("storage", onChange\)/);
  assert.match(hook, /addEventListener\(POLYFORGE_MESSAGES_COLLAPSE_EVENT, onChange\)/);
  assert.match(hook, /useSyncExternalStore\(subscribe, readSnapshot, serverSnapshot\)/);
  assert.match(hook, /const readSnapshot = readCollapsedProjects;/, "the snapshot includes the in-memory state");
  assert.match(hook, /const serverSnapshot = \(\) => POLYFORGE_MESSAGES_COLLAPSE_NONE;/);
});
