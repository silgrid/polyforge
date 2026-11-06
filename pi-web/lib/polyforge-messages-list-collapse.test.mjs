import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { createJiti } from "jiti";

// the Messages contact column's whole-column strip (the
// ≤640px sheet shares the key).
const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-messages-list-collapse.ts");
const KEY = lib.POLYFORGE_MESSAGES_LIST_COLLAPSE_KEY;

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

test("the key is its own, distinct from #222's width and #223's project groups", () => {
  assert.equal(KEY, "pi-web:polyforge-messages-list-collapsed");
  assert.notEqual(KEY, "pi-web:polyforge-messages-list-width");
  assert.notEqual(KEY, "pi-web:polyforge-messages-collapsed");
  assert.equal(lib.POLYFORGE_MESSAGES_LIST_COLLAPSE_EVENT, KEY);
});

test("tolerant parse: only 1/true is collapsed; absent, corrupt or anything else is expanded", () => {
  assert.equal(lib.parsePolyforgeMessagesListCollapse("1"), true);
  assert.equal(lib.parsePolyforgeMessagesListCollapse("true"), true);
  for (const raw of [null, undefined, "", "0", "false", "yes", "[1]", 1, true, {}]) {
    assert.equal(lib.parsePolyforgeMessagesListCollapse(raw), false, JSON.stringify(raw));
  }
});

test("reads and writes go through storage under the one key, with the change event", () => {
  const storage = memoryStorage();
  withWindow(storage, (events) => {
    assert.equal(lib.readMessagesListCollapsed(), false, "expanded by default");
    assert.equal(lib.writeMessagesListCollapsed(true), true);
    assert.equal(storage.data.get(KEY), "1");
    assert.equal(lib.readMessagesListCollapsed(), true);
    lib.writeMessagesListCollapsed(false);
    assert.equal(storage.data.get(KEY), "0");
    assert.equal(lib.readMessagesListCollapsed(), false);
    assert.deepEqual(events, [lib.POLYFORGE_MESSAGES_LIST_COLLAPSE_EVENT, lib.POLYFORGE_MESSAGES_LIST_COLLAPSE_EVENT]);
    assert.deepEqual([...storage.data.keys()], [KEY], "no other key is written (#222 width, #223 groups untouched)");
  });
});

test("a refusing storage keeps the toggle in memory for this page", () => {
  withWindow(throwing, (events) => {
    assert.equal(lib.readMessagesListCollapsed(), false);
    lib.writeMessagesListCollapsed(true);
    assert.equal(lib.readMessagesListCollapsed(), true, "the in-memory fallback");
    lib.writeMessagesListCollapsed(false);
    assert.equal(lib.readMessagesListCollapsed(), false);
    assert.equal(events.length, 2);
  });
  withWindow(memoryStorage({ [KEY]: "1" }), () => {
    lib.writeMessagesListCollapsed(true);
    assert.equal(lib.readMessagesListCollapsed(), true, "storage holds the truth again after a kept write");
  });
});

test("the module and the hook stay browser-only: no fetch, no server write; the hook's server snapshot is expanded", async () => {
  const source = await readFile(new URL("./polyforge-messages-list-collapse.ts", import.meta.url), "utf8");
  const hook = await readFile(new URL("../hooks/usePolyforgeMessagesListCollapse.ts", import.meta.url), "utf8");
  for (const text of [source, hook]) assert.doesNotMatch(text, /fetch\(|from "node:|require\(/);
  assert.match(hook, /useSyncExternalStore\(subscribe, readMessagesListCollapsed, serverSnapshot\)/);
  assert.match(hook, /const serverSnapshot = \(\) => false;/);
  assert.match(hook, /window\.addEventListener\("storage", onChange\);/);
});
