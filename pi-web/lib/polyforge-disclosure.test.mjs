import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-disclosure.ts");
const DEFAULT = lib.POLYFORGE_DISCLOSURE_DEFAULT;
const KEY = lib.POLYFORGE_DISCLOSURE_KEY;

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

test("the key, the seven regions and the all-closed default are the documented ones", () => {
  assert.equal(KEY, "pi-web:polyforge-config-disclosure");
  // the workflow card's region joins; the summarize and
  // candidates cards (and their regions) are gone.
  assert.deepEqual([...lib.POLYFORGE_DISCLOSURE_REGIONS], ["detect", "cliToml", "daemon", "workspace", "history", "install", "workflow"]);
  assert.deepEqual(Object.keys(DEFAULT).sort(), [...lib.POLYFORGE_DISCLOSURE_REGIONS].sort());
  assert.ok(Object.values(DEFAULT).every((open) => open === false), "every region defaults to closed");
});

test("missing, corrupt, non-object and wrong-typed values read as the defaults", () => {
  for (const raw of [null, undefined, "", "{", "null", "[]", "[true]", "42", "\"open\"", 7, {}]) {
    assert.deepEqual(lib.parsePolyforgeDisclosure(raw), DEFAULT, String(raw));
  }
  assert.deepEqual(lib.parsePolyforgeDisclosure(JSON.stringify({ detect: "true", history: 1, install: null })), DEFAULT);
});

test("a partial object merges the defaults and unknown regions are ignored", () => {
  const parsed = lib.parsePolyforgeDisclosure(JSON.stringify({ detect: true, workspace: true, bogus: true, history: "x" }));
  assert.deepEqual(parsed, { ...DEFAULT, detect: true, workspace: true });
  assert.equal("bogus" in parsed, false);
});

test("reads go through storage and survive a throwing storage", () => {
  withWindow(memoryStorage({ [KEY]: JSON.stringify({ workflow: true }) }), () => {
    assert.deepEqual(lib.readPolyforgeDisclosure(), { ...DEFAULT, workflow: true });
  });
  withWindow(throwing, () => assert.deepEqual(lib.readPolyforgeDisclosure(), DEFAULT));
});

test("a write changes one region, preserves the others and announces it", () => {
  const storage = memoryStorage({ [KEY]: JSON.stringify({ detect: true, history: true }) });
  withWindow(storage, (events) => {
    const next = lib.writePolyforgeDisclosureRegion("history", false);
    assert.deepEqual(next, { ...DEFAULT, detect: true });
    assert.deepEqual(JSON.parse(storage.data.get(KEY)), { ...DEFAULT, detect: true });
    assert.deepEqual(events, [lib.POLYFORGE_DISCLOSURE_EVENT]);
  });
});

test("with storage blocked a toggle still flips the region, announces it and reads back stable", () => {
  withWindow(throwing, (events) => {
    assert.deepEqual(lib.writePolyforgeDisclosureRegion("detect", true), { ...DEFAULT, detect: true });
    assert.deepEqual(events, [lib.POLYFORGE_DISCLOSURE_EVENT], "the open page hears the toggle");
    const first = lib.readPolyforgeDisclosure();
    assert.deepEqual(first, { ...DEFAULT, detect: true }, "the region opened despite the refused write");
    assert.equal(lib.readPolyforgeDisclosure(), first, "the snapshot is the same object until the next toggle");
    lib.writePolyforgeDisclosureRegion("workspace", true);
    assert.deepEqual(lib.readPolyforgeDisclosure(), { ...DEFAULT, detect: true, workspace: true }, "toggles accumulate");
    lib.writePolyforgeDisclosureRegion("detect", false);
    assert.deepEqual(lib.readPolyforgeDisclosure(), { ...DEFAULT, workspace: true }, "and close again");
  });
  // setItem alone refusing (quota) behaves the same.
  const quota = { getItem: () => null, setItem() { throw new Error("quota"); } };
  withWindow(quota, () => {
    lib.writePolyforgeDisclosureRegion("history", true);
    assert.equal(lib.readPolyforgeDisclosure().history, true);
  });
  // Once storage keeps a write it is the truth again (another tab's write shows).
  const storage = memoryStorage();
  withWindow(storage, () => {
    lib.writePolyforgeDisclosureRegion("install", true);
    assert.equal(JSON.parse(storage.data.get(KEY)).install, true);
    storage.setItem(KEY, JSON.stringify({ workflow: true }));
    assert.deepEqual(lib.readPolyforgeDisclosure(), { ...DEFAULT, workflow: true });
  });
});

test("a stored read is the same object until the stored string changes", () => {
  const storage = memoryStorage({ [KEY]: JSON.stringify({ daemon: true }) });
  withWindow(storage, () => {
    const first = lib.readPolyforgeDisclosure();
    assert.equal(lib.readPolyforgeDisclosure(), first);
    storage.setItem(KEY, JSON.stringify({ daemon: false }));
    assert.notEqual(lib.readPolyforgeDisclosure(), first);
    assert.deepEqual(lib.readPolyforgeDisclosure(), DEFAULT);
  });
});

test("the module and the hook stay browser-only: no fetch, no server, both events heard, default server snapshot", async () => {
  const source = await readFile(new URL("./polyforge-disclosure.ts", import.meta.url), "utf8");
  const hook = await readFile(new URL("../hooks/usePolyforgeDisclosure.ts", import.meta.url), "utf8");
  for (const text of [source, hook]) {
    assert.doesNotMatch(text, /\bfetch\(|from\s+["']node:|\/api\//);
  }
  assert.match(hook, /addEventListener\("storage", onChange\)/);
  assert.match(hook, /addEventListener\(POLYFORGE_DISCLOSURE_EVENT, onChange\)/);
  assert.match(hook, /useSyncExternalStore\(subscribe, readSnapshot, serverSnapshot\)/);
  assert.match(hook, /const readSnapshot = readPolyforgeDisclosure;/, "the snapshot includes the in-memory state");
  assert.match(hook, /const serverSnapshot = \(\) => POLYFORGE_DISCLOSURE_DEFAULT;/);
});

test("a stored object from before the merge reads through the defaults", () => {
  const parsed = lib.parsePolyforgeDisclosure(JSON.stringify({ summarize: true, candidates: true, workspace: true }));
  assert.deepEqual(parsed, { ...DEFAULT, workspace: true });
  assert.equal("summarize" in parsed, false);
  assert.equal("candidates" in parsed, false);
});
