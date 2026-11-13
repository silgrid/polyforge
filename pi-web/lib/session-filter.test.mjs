import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true, interopDefault: true });
const {
  SESSION_FILTER_PATTERNS_STORAGE_KEY,
  SHOW_FILTERED_SESSIONS_STORAGE_KEY,
  DEFAULT_SESSION_FILTER_PATTERNS,
  sessionFilterStorage,
  loadSessionFilterPatterns,
  saveSessionFilterPatterns,
  loadShowFilteredSessions,
  saveShowFilteredSessions,
  isSessionFiltered,
  countFilteredSessions,
  computeFilteredSessionIds,
  isFamilyRowFiltered,
  filterSessionsByAge,
  isSessionOutsideAgeWindow,
  loadSessionAgeFilterDays,
  saveSessionAgeFilterDays,
  clampSessionAgeFilterDays,
  DEFAULT_SESSION_AGE_FILTER_DAYS,
  SESSION_AGE_FILTER_DAYS_STORAGE_KEY,
} = await jiti.import("./session-filter.ts");

function memoryStorage(initial = new Map()) {
  return {
    getItem: (key) => (initial.has(key) ? initial.get(key) : null),
    setItem: (key, value) => { initial.set(key, String(value)); },
  };
}

/** A storage whose getItem throws — the browser storage-policy denial. */
function throwingStorage() {
  return {
    getItem: () => { throw new Error("denied"); },
    setItem: () => { throw new Error("denied"); },
  };
}

test("storage keys and the default pattern list match the spec", () => {
  assert.equal(SESSION_FILTER_PATTERNS_STORAGE_KEY, "pi-web:session-filter-patterns");
  assert.equal(SHOW_FILTERED_SESSIONS_STORAGE_KEY, "pi-web:show-filtered-sessions");
  assert.deepEqual(DEFAULT_SESSION_FILTER_PATTERNS, ["Execute the pinned skill entry"]);
});

test("a missing key reads the default worker-session pattern", () => {
  assert.deepEqual(loadSessionFilterPatterns(memoryStorage()), ["Execute the pinned skill entry"]);
  assert.deepEqual(loadSessionFilterPatterns(null), ["Execute the pinned skill entry"]);
});

test("patterns round-trip through the storage key", () => {
  const storage = memoryStorage();
  saveSessionFilterPatterns(storage, ["worker", "Execute the pinned skill entry", "部署"]);
  assert.equal(
    storage.getItem(SESSION_FILTER_PATTERNS_STORAGE_KEY),
    JSON.stringify(["worker", "Execute the pinned skill entry", "部署"]),
  );
  assert.deepEqual(loadSessionFilterPatterns(storage), ["worker", "Execute the pinned skill entry", "部署"]);
});

test("a null storage never writes and never throws", () => {
  saveSessionFilterPatterns(null, ["a"]);
  saveShowFilteredSessions(null, true);
  assert.deepEqual(loadSessionFilterPatterns(null), ["Execute the pinned skill entry"]);
  assert.equal(loadShowFilteredSessions(null), false);
});

test("corrupt payloads fall back to the default list", () => {
  for (const raw of ["not json", "42", '"a string"', "{}", "[1, 2]", '["ok", 7]']) {
    const storage = memoryStorage(new Map([[SESSION_FILTER_PATTERNS_STORAGE_KEY, raw]]));
    assert.deepEqual(
      loadSessionFilterPatterns(storage),
      ["Execute the pinned skill entry"],
      `corrupt payload ${raw} must fall back to the defaults`,
    );
  }
});

test("a throwing storage read degrades to the defaults; writes are swallowed", () => {
  const storage = throwingStorage();
  assert.deepEqual(loadSessionFilterPatterns(storage), ["Execute the pinned skill entry"]);
  assert.equal(loadShowFilteredSessions(storage), false);
  // Persistence is best-effort: the throwing setItem must not propagate.
  saveSessionFilterPatterns(storage, ["x"]);
  saveShowFilteredSessions(storage, true);
});

test("whitespace-only lines are ignored and an empty stored list disables hiding", () => {
  const storage = memoryStorage(new Map([
    [SESSION_FILTER_PATTERNS_STORAGE_KEY, JSON.stringify(["  ", "\t", "worker"])],
  ]));
  assert.deepEqual(loadSessionFilterPatterns(storage), ["worker"]);
  const emptyStorage = memoryStorage(new Map([
    [SESSION_FILTER_PATTERNS_STORAGE_KEY, "[]"],
  ]));
  assert.deepEqual(loadSessionFilterPatterns(emptyStorage), []);
});

test("isSessionFiltered matches case-insensitively on name OR firstMessage", () => {
  const patterns = ["Execute THE pinned skill entry", "worker"];
  const byName = { name: "My WORKER session", firstMessage: "hello" };
  const byFirstMessage = { name: undefined, firstMessage: "execute the pinned skill entry below" };
  const unrelated = { name: "Chat about deployments", firstMessage: "hi" };
  assert.equal(isSessionFiltered(byName, patterns), true, "name matches case-insensitively");
  assert.equal(isSessionFiltered(byFirstMessage, patterns), true, "firstMessage matches case-insensitively");
  assert.equal(isSessionFiltered(unrelated, patterns), false);
});

test("an empty pattern list or whitespace-only patterns disable hiding entirely", () => {
  const session = { name: "Execute the pinned skill entry", firstMessage: "worker" };
  assert.equal(isSessionFiltered(session, []), false);
  assert.equal(isSessionFiltered(session, ["   ", "\t"]), false);
});

test("the show-filtered toggle persists under its own key, default false", () => {
  const storage = memoryStorage();
  assert.equal(loadShowFilteredSessions(storage), false);
  saveShowFilteredSessions(storage, true);
  assert.equal(storage.getItem(SHOW_FILTERED_SESSIONS_STORAGE_KEY), "true");
  assert.equal(loadShowFilteredSessions(storage), true);
  saveShowFilteredSessions(storage, false);
  assert.equal(loadShowFilteredSessions(storage), false);
});

test("sessionFilterStorage never throws and is null without a window", () => {
  assert.equal(sessionFilterStorage(), null, "no window in the node test env");
});

test("countFilteredSessions counts exactly the matching sessions, no more", () => {
  const sessions = [
    { id: "s1", name: "My WORKER session", firstMessage: "hello" },
    { id: "s2", name: undefined, firstMessage: "Execute the pinned skill entry below" },
    { id: "s3", name: "Chat about deployments", firstMessage: "hi" },
    { id: "s4", name: "worker 2", firstMessage: undefined },
    { id: "s5", name: undefined, firstMessage: undefined },
  ];
  assert.equal(countFilteredSessions(sessions, ["worker", "execute the pinned"]), 3);
  assert.equal(countFilteredSessions(sessions, ["deployments"]), 1);
  // Empty pattern list (hiding disabled) and no matches both read as 0 —
  // the badge and marking stay off, preserving current behavior.
  assert.equal(countFilteredSessions(sessions, []), 0);
  assert.equal(countFilteredSessions(sessions, ["zzz-no-match"]), 0);
  // Whitespace-only patterns never match (same rule as isSessionFiltered).
  assert.equal(countFilteredSessions(sessions, ["  "]), 0);
});

test("computeFilteredSessionIds returns exactly the matching ids", () => {
  const sessions = [
    { id: "s1", name: "My WORKER session", firstMessage: "hello" },
    { id: "s2", name: undefined, firstMessage: "Execute the pinned skill entry below" },
    { id: "s3", name: "Chat about deployments", firstMessage: "hi" },
  ];
  assert.deepEqual(
    [...computeFilteredSessionIds(sessions, ["worker", "execute the pinned"])].sort(),
    ["s1", "s2"],
  );
  assert.deepEqual(
    [...computeFilteredSessionIds(sessions, [])],
    [],
    "an empty pattern list hides nothing",
  );
  assert.deepEqual(
    [...computeFilteredSessionIds([], ["worker"])],
    [],
    "an empty session list yields an empty id set",
  );
  // A session can match both rules; it must be counted and listed once.
  assert.equal(countFilteredSessions([{ id: "s9", name: "worker", firstMessage: "worker" }], ["worker"]), 1);
  assert.equal(computeFilteredSessionIds([{ id: "s9", name: "worker", firstMessage: "worker" }], ["worker"]).size, 1);
});

test("isFamilyRowFiltered marks a row on a ROOT match only, never a subagent-only match (review r1 P2)", () => {
  const rootId = "root-1";
  const childId = "child-1";

  // Root matches, child does not: the row IS marked.
  const rootOnlyIds = new Set([rootId]);
  assert.equal(isFamilyRowFiltered(rootId, rootOnlyIds), true, "root-only match marks the row");

  // Child matches, root does not: the row must NOT be marked — the
  // rendered title/click target is the root, which the patterns do not hit.
  const childOnlyIds = new Set([childId]);
  assert.equal(isFamilyRowFiltered(rootId, childOnlyIds), false, "child-only match must not mark the row");

  // Both match: the row is marked (the root match alone is sufficient).
  const bothIds = new Set([rootId, childId]);
  assert.equal(isFamilyRowFiltered(rootId, bothIds), true, "root+child match marks the row");

  // Neither matches: not marked.
  const neitherIds = new Set(["unrelated"]);
  assert.equal(isFamilyRowFiltered(rootId, neitherIds), false, "no match does not mark the row");

  // Reveal OFF (or no active patterns) is represented as a null set —
  // marking must be off unconditionally, regardless of what would have
  // matched, preserving current (pre-wi) behavior exactly.
  assert.equal(isFamilyRowFiltered(rootId, null), false, "reveal OFF (null set) never marks any row");
  assert.equal(isFamilyRowFiltered(childId, null), false, "reveal OFF (null set) never marks any row, child id too");
});

// ---- session age filter (pi#82) ----

const DAY_MS = 24 * 60 * 60 * 1000;
const NOW = 1_800_000_000_000;
const session = (ageDays) => ({ id: "s", modified: new Date(NOW - ageDays * DAY_MS).toISOString() });

test("age filter: 0 and negative days are a no-op returning the same array", () => {
  const list = [session(1), session(400)];
  assert.equal(filterSessionsByAge(list, 0, NOW), list);
  assert.equal(filterSessionsByAge(list, -3, NOW), list);
});

test("age filter keeps sessions inside the window and drops older ones", () => {
  const list = [session(0), session(6), session(7), session(30)];
  const kept = filterSessionsByAge(list, 7, NOW);
  // now - modified >= 7 days hides; exactly 7 days old is OUTSIDE (>=)
  assert.deepEqual(kept.map((x) => x.modified), [list[0].modified, list[1].modified]);
});

test("age filter keeps sessions with an unusable modified value", () => {
  const list = [{ id: "a", modified: undefined }, { id: "b", modified: "not-a-date" }];
  assert.equal(filterSessionsByAge(list, 7, NOW).length, 2);
});

test("age filter days: absent key reads the default, garbage falls back, value clamps", () => {
  const store = () => {
    const map = new Map();
    return { getItem: (k) => map.get(k) ?? null, setItem: (k, v) => map.set(k, v) };
  };
  assert.equal(loadSessionAgeFilterDays(store()), DEFAULT_SESSION_AGE_FILTER_DAYS);
  const corrupt = store();
  corrupt.setItem(SESSION_AGE_FILTER_DAYS_STORAGE_KEY, "not-a-number");
  assert.equal(loadSessionAgeFilterDays(corrupt), DEFAULT_SESSION_AGE_FILTER_DAYS);
  const big = store();
  big.setItem(SESSION_AGE_FILTER_DAYS_STORAGE_KEY, "9999");
  assert.equal(loadSessionAgeFilterDays(big), 365);
  const zero = store();
  saveSessionAgeFilterDays(zero, 0);
  assert.equal(loadSessionAgeFilterDays(zero), 0);
  const frac = store();
  saveSessionAgeFilterDays(frac, 2.6);
  assert.equal(loadSessionAgeFilterDays(frac), 3);
  assert.equal(clampSessionAgeFilterDays("abc"), DEFAULT_SESSION_AGE_FILTER_DAYS);
});

test("isSessionOutsideAgeWindow honours the disabled window", () => {
  assert.equal(isSessionOutsideAgeWindow(session(400), 0, NOW), false);
  assert.equal(isSessionOutsideAgeWindow(session(400), 7, NOW), true);
  assert.equal(isSessionOutsideAgeWindow(session(6), 7, NOW), false);
});

// ---- default window 15 days ----

test("age filter days: an absent key reads 15; a stored 7 or 0 is kept", () => {
  assert.equal(loadSessionAgeFilterDays(memoryStorage()), 15, "a browser that never saved the setting reads 15");
  const seven = memoryStorage(new Map([[SESSION_AGE_FILTER_DAYS_STORAGE_KEY, "7"]]));
  assert.equal(loadSessionAgeFilterDays(seven), 7, "a stored 7 is kept, never migrated");
  const zero = memoryStorage(new Map([[SESSION_AGE_FILTER_DAYS_STORAGE_KEY, "0"]]));
  assert.equal(loadSessionAgeFilterDays(zero), 0, "a stored 0 (show all) is kept");
});

test("age filter: the default window shows a 10-day-old session and hides a 20-day-old one; 30 days shows it", () => {
  const tenDays = { id: "ten", modified: new Date(NOW - 10 * DAY_MS).toISOString() };
  const twentyDays = { id: "twenty", modified: new Date(NOW - 20 * DAY_MS).toISOString() };
  const list = [tenDays, twentyDays];
  const storage = memoryStorage();
  assert.deepEqual(
    filterSessionsByAge(list, loadSessionAgeFilterDays(storage), NOW).map((s) => s.id),
    ["ten"],
    "default settings: 10 days ago shown, 20 days ago hidden",
  );
  saveSessionAgeFilterDays(storage, 30);
  assert.deepEqual(
    filterSessionsByAge(list, loadSessionAgeFilterDays(storage), NOW).map((s) => s.id),
    ["ten", "twenty"],
    "after setting 30 days the 20-day-old session is shown",
  );
});

// ---- hidden count honours the age window ----

test("hidden count honours the age window: the default 15 days counts a 10-day-old hidden session, not a 20-day-old one; 0 counts all", () => {
  const marker = DEFAULT_SESSION_FILTER_PATTERNS[0];
  const hiddenTenDays = { id: "hidden-ten", firstMessage: marker, modified: new Date(NOW - 10 * DAY_MS).toISOString() };
  const hiddenTwentyDays = { id: "hidden-twenty", firstMessage: marker, modified: new Date(NOW - 20 * DAY_MS).toISOString() };
  const shownTenDays = { id: "shown-ten", firstMessage: "an ordinary chat", modified: new Date(NOW - 10 * DAY_MS).toISOString() };
  const list = [hiddenTenDays, hiddenTwentyDays, shownTenDays];
  const patterns = loadSessionFilterPatterns(memoryStorage());
  const storage = memoryStorage();
  assert.equal(
    countFilteredSessions(list, patterns, loadSessionAgeFilterDays(storage), NOW),
    1,
    "default 15 days: only the 10-day-old rule-matching session counts as hidden",
  );
  saveSessionAgeFilterDays(storage, 0);
  assert.equal(
    countFilteredSessions(list, patterns, loadSessionAgeFilterDays(storage), NOW),
    2,
    "days 0 (show all): both rule-matching sessions count as hidden",
  );
});
