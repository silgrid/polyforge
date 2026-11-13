import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

import {
  FILES_PLACEMENT_DEFAULT,
  FILES_PLACEMENT_STORAGE_KEY,
  getFilesPlacement,
  parseFilesPlacement,
  setFilesPlacement,
  subscribeFilesPlacement,
} from "./files-placement.ts";

const source = await (await import("node:fs/promises")).readFile(
  new URL("./files-placement.ts", import.meta.url),
  "utf8",
);

function installWindow({ failing = false } = {}) {
  const store = new Map();
  const listeners = new Set();
  const fakeWindow = {
    localStorage: {
      getItem: (key) => {
        if (failing) throw new Error("quota");
        return store.has(key) ? store.get(key) : null;
      },
      setItem: (key, value) => {
        if (failing) throw new Error("quota");
        store.set(key, String(value));
      },
      removeItem: (key) => store.delete(key),
    },
    addEventListener: (_type, handler) => listeners.add(handler),
    removeEventListener: (_type, handler) => listeners.delete(handler),
    dispatchEvent: (event) => {
      for (const handler of listeners) handler(event);
      return true;
    },
  };
  globalThis.window = fakeWindow;
  return { store, listeners };
}

test("defaults to stacked when nothing is stored (fresh install keeps today's layout)", () => {
  installWindow();
  assert.equal(getFilesPlacement(), "stacked");
  assert.equal(FILES_PLACEMENT_DEFAULT, "stacked");
});

test("parses only the two known values; anything else is the default", () => {
  assert.equal(parseFilesPlacement("tabs"), "tabs");
  assert.equal(parseFilesPlacement("stacked"), "stacked");
  assert.equal(parseFilesPlacement(null), "stacked");
  assert.equal(parseFilesPlacement("below"), "stacked");
  assert.equal(parseFilesPlacement(""), "stacked");
  assert.equal(parseFilesPlacement("garbage"), "stacked");
});

test("persists under the fork key and notifies subscribers on change", () => {
  const { store } = installWindow();
  const seen = [];
  const unsubscribe = subscribeFilesPlacement((change) => seen.push(change.value));

  setFilesPlacement("tabs");
  assert.equal(store.get(FILES_PLACEMENT_STORAGE_KEY), "tabs");
  assert.equal(getFilesPlacement(), "tabs");
  assert.deepEqual(seen, ["tabs"]);

  setFilesPlacement("stacked");
  assert.equal(store.get(FILES_PLACEMENT_STORAGE_KEY), "stacked");
  assert.equal(getFilesPlacement(), "stacked");
  assert.deepEqual(seen, ["tabs", "stacked"]);

  unsubscribe();
  setFilesPlacement("tabs");
  assert.deepEqual(seen, ["tabs", "stacked"], "unsubscribed listeners hear nothing");
});

test("reads the default when storage is unavailable (SSR) or throwing (privacy mode)", () => {
  const original = globalThis.window;
  delete globalThis.window;
  try {
    assert.equal(getFilesPlacement(), "stacked");
    setFilesPlacement("tabs"); // must not throw
    const unsubscribe = subscribeFilesPlacement(() => {});
    assert.equal(typeof unsubscribe, "function");
    unsubscribe();
  } finally {
    if (original !== undefined) globalThis.window = original;
  }
  installWindow({ failing: true });
  assert.equal(getFilesPlacement(), "stacked");
  // The write is best-effort, but the broadcast carries the EFFECTIVE
  // value read back from the same storage (the build-outputs review-B1
  // rule), so listeners never see a half-applied "tabs".
  let broadcast = null;
  const unsubscribe = subscribeFilesPlacement((change) => { broadcast = change.value; });
  setFilesPlacement("tabs");
  assert.equal(broadcast, "stacked");
  unsubscribe();
});

test("corrupt payloads read as the default", () => {
  const store = new Map([[FILES_PLACEMENT_STORAGE_KEY, "{not json"]]);
  globalThis.window = {
    localStorage: {
      getItem: (key) => store.get(key) ?? null,
      setItem: (key, value) => store.set(key, String(value)),
      removeItem: (key) => store.delete(key),
    },
  };
  try {
    assert.equal(getFilesPlacement(), "stacked");
  } finally {
    delete globalThis.window;
  }
});

test("the hook binds through useSyncExternalStore with a default server snapshot", () => {
  // Source-shape assertions: the React binding must not read storage during
  // the server/hydration render (getServerSnapshot returns the default),
  // and subscribe must re-read storage on every notification so all
  // surfaces agree on the live value without prop drilling.
  assert.match(source, /useSyncExternalStore\(subscribeFilesPlacement, getSnapshot, getServerSnapshot\)/);
  assert.match(source, /function getServerSnapshot\(\): FilesPlacement \{\s*return FILES_PLACEMENT_DEFAULT;\s*\}/);
  assert.match(source, /export const FILES_PLACEMENT_STORAGE_KEY = "pi-web:files-placement";/);
  assert.match(source, /export const FILES_PLACEMENT_CHANGE_EVENT = "pi-web:files-placement-change";/);
  // Only the fork key is used — upstream's "pi-web:sidebar-files-placement"
  // is neither read nor migrated.
  assert.doesNotMatch(source, /sidebar-files-placement/);
});
