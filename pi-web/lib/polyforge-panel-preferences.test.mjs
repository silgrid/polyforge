import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const prefs = await jiti.import("./polyforge-panel-preferences.ts");
const { POLYFORGE_TITLE_LENGTH: TITLE, POLYFORGE_POLL_INTERVAL: POLL } = prefs;

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

test("keys, defaults and bounds are the documented ones", () => {
  assert.equal(TITLE.key, "pi-web:polyforge-title-length");
  assert.equal(POLL.key, "pi-web:polyforge-poll-interval");
  // owner-intended change: the title-length default is 60 (was 30);
  // the 10/200 bounds are unchanged and a stored 30 is still kept.
  assert.deepEqual([TITLE.defaultValue, TITLE.min, TITLE.max], [60, 10, 200], "the default is 60 code points, bounds 10–200");
  assert.deepEqual([POLL.defaultValue, POLL.min, POLL.max], [3, 1, 60]);
  assert.equal(prefs.POLYFORGE_PREFERENCES_EVENT, "pi-web:polyforge-preferences");
});

test("absent, corrupt, non-numeric and non-positive values fall back; positive out-of-range values clamp", () => {
  for (const raw of [null, undefined, "", "bogus", "12abc", "NaN", "Infinity", {}, "1e3", "-5", "0", "-0.2", "0.4"]) {
    assert.equal(prefs.clampPolyforgePreference(TITLE, raw), 60, String(raw));
    assert.equal(prefs.clampPolyforgePreference(POLL, raw), 3, String(raw));
  }
  // owner-intended change: an explicitly stored 30 is kept (no migration).
  assert.equal(prefs.clampPolyforgePreference(TITLE, "30"), 30, "a stored 30 is kept, not rewritten to the new default");
  assert.equal(prefs.clampPolyforgePreference(TITLE, 30), 30);
  assert.equal(prefs.clampPolyforgePreference(TITLE, "999"), 200);
  assert.equal(prefs.clampPolyforgePreference(TITLE, "5"), 10);
  assert.equal(prefs.clampPolyforgePreference(POLL, "0.6"), 1);
  assert.equal(prefs.clampPolyforgePreference(POLL, "999"), 60);
  assert.equal(prefs.clampPolyforgePreference(POLL, " 7 "), 7);
  assert.equal(prefs.clampPolyforgePreference(TITLE, 90), 90);
  assert.equal(prefs.clampPolyforgePreference(TITLE, "45.6"), 46);
});

test("reads go through storage and survive a throwing storage", () => {
  withWindow(memoryStorage({ [TITLE.key]: "90", [POLL.key]: "bogus" }), () => {
    assert.equal(prefs.readPolyforgePreference(TITLE), 90);
    assert.equal(prefs.readPolyforgePreference(POLL), 3);
  });
  withWindow(throwing, () => assert.equal(prefs.readPolyforgePreference(TITLE), 60));
});

test("a write stores the clamped value and announces it; a refused write reports null", () => {
  const storage = memoryStorage();
  withWindow(storage, (events) => {
    assert.equal(prefs.writePolyforgePreference(POLL, "120"), 60);
    assert.equal(storage.data.get(POLL.key), "60");
    assert.deepEqual(events, ["pi-web:polyforge-preferences"]);
  });
  withWindow(throwing, (events) => {
    assert.equal(prefs.writePolyforgePreference(TITLE, "50"), null);
    assert.deepEqual(events, []);
  });
});

test("the module and the hook stay browser-only: no fetch, no server write, both events heard", async () => {
  const lib = await readFile(new URL("./polyforge-panel-preferences.ts", import.meta.url), "utf8");
  const hook = await readFile(new URL("../hooks/usePolyforgePanelPreferences.ts", import.meta.url), "utf8");
  for (const source of [lib, hook]) {
    assert.doesNotMatch(source, /\bfetch\(|from\s+["']node:|\/api\//);
  }
  assert.match(hook, /addEventListener\("storage", onChange\)/);
  assert.match(hook, /addEventListener\(POLYFORGE_PREFERENCES_EVENT, onChange\)/);
  assert.match(hook, /useSyncExternalStore\(subscribe, readTitleLength, defaultTitleLength\)/, "the server snapshot is the default");
  assert.match(hook, /useSyncExternalStore\(subscribe, readPollInterval, defaultPollInterval\)/);
});
