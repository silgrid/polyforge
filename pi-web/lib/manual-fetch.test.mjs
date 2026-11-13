// The manual fetch's pure helpers: what counts as new content,
// how a forced read merges into the paged-in history, when the bottom pull-up
// arms and triggers, and the notice's local hh:mm.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const {
  MANUAL_FETCH_TIMEOUT_MS,
  PULL_FETCH_THRESHOLD_PX,
  formatFetchTime,
  hasFreshContent,
  isPullFetchTriggered,
  mergeLoadedHistory,
  shouldArmPullFetch,
} = await jiti.import("./manual-fetch.ts");

const msg = (role, content) => ({ role, content, timestamp: 0 });
const windowOf = (ids, { oldestEntryId = ids[0] ?? null, hasMore = false, live = [] } = {}) => ({
  messages: [...ids.map((id) => msg(id.startsWith("u") ? "user" : "assistant", `text ${id}`)), ...live],
  entryIds: [...ids],
  oldestEntryId,
  hasMore,
});

test("the bounds are the plan's choices", () => {
  assert.equal(MANUAL_FETCH_TIMEOUT_MS, 15_000);
  assert.equal(PULL_FETCH_THRESHOLD_PX, 60);
});

test("hasFreshContent: a new entry id is new content", () => {
  assert.equal(hasFreshContent(windowOf(["u1", "a1"]), windowOf(["u1", "a1", "u2"])), true);
});

test("hasFreshContent: a changed message under a shared id is new content", () => {
  const next = windowOf(["u1", "a1"]);
  next.messages[1] = msg("assistant", "edited answer");
  assert.equal(hasFreshContent(windowOf(["u1", "a1"]), next), true);
});

test("hasFreshContent: the same view, or a narrower window of it, is current", () => {
  assert.equal(hasFreshContent(windowOf(["u1", "a1"]), windowOf(["u1", "a1"])), false);
  assert.equal(hasFreshContent(windowOf(["u0", "a0", "u1", "a1"]), windowOf(["u1", "a1"])), false);
});

test("hasFreshContent: same text under a new id is new, never matched by text", () => {
  const previous = { messages: [msg("user", "SAME")], entryIds: ["u1"] };
  const next = { messages: [msg("user", "SAME"), msg("user", "SAME")], entryIds: ["u1", "u2"] };
  assert.equal(hasFreshContent(previous, next), true);
});

test("mergeLoadedHistory: overlap keeps the paged-in prefix and its cursor", () => {
  const previous = windowOf(["e0", "e1", "e2", "e3"], { oldestEntryId: "e0", hasMore: true });
  const next = windowOf(["e2", "e3", "e4"], { oldestEntryId: "e2", hasMore: true });
  const merged = mergeLoadedHistory(previous, next);
  assert.deepEqual(merged.entryIds, ["e0", "e1", "e2", "e3", "e4"]);
  assert.equal(merged.messages.length, 5);
  assert.equal(merged.messages[0], previous.messages[0]);
  assert.equal(merged.messages[2], next.messages[0]);
  assert.equal(merged.oldestEntryId, "e0");
  assert.equal(merged.hasMore, true);
});

test("mergeLoadedHistory: the fresh window replaces changed shared content and the live tail", () => {
  const previous = windowOf(["e0", "e1", "e2"], { hasMore: false, live: [msg("user", "live prompt")] });
  const next = windowOf(["e1", "e2", "e3"], { hasMore: true, oldestEntryId: "e1" });
  next.messages[1] = msg("assistant", "rewritten");
  const merged = mergeLoadedHistory(previous, next);
  assert.deepEqual(merged.entryIds, ["e0", "e1", "e2", "e3"]);
  assert.equal(merged.messages.length, 4);
  assert.equal(merged.messages[2].content, "rewritten");
  assert.equal(merged.hasMore, false);
  assert.equal(merged.oldestEntryId, "e0");
});

test("mergeLoadedHistory: no overlap keeps the loaded history and appends the fresh window", () => {
  const previous = windowOf(["e0", "e1"], { oldestEntryId: "e0", hasMore: true, live: [msg("assistant", "live")] });
  const next = windowOf(["e60", "e61"], { oldestEntryId: "e60", hasMore: true });
  const merged = mergeLoadedHistory(previous, next);
  assert.deepEqual(merged.entryIds, ["e0", "e1", "e60", "e61"]);
  assert.equal(merged.messages.length, 4);
  assert.equal(merged.oldestEntryId, "e0");
  assert.equal(merged.hasMore, true);
});

test("mergeLoadedHistory: a fresh window covering the loaded one is taken whole", () => {
  const previous = windowOf(["e2", "e3"], { oldestEntryId: "e2", hasMore: true });
  const next = windowOf(["e1", "e2", "e3", "e4"], { oldestEntryId: "e1", hasMore: true });
  assert.equal(mergeLoadedHistory(previous, next), next);
  const whole = windowOf(["x1", "x2"], { hasMore: false, oldestEntryId: null });
  assert.equal(mergeLoadedHistory(previous, whole), whole);
  assert.equal(mergeLoadedHistory(previous, windowOf(["e2", "e3", "e4"])).entryIds.length, 3);
});

test("mergeLoadedHistory: never returns an entry id twice", () => {
  const previous = windowOf(["e0", "e1", "e2", "e3"], { hasMore: true });
  const next = windowOf(["e9", "e1", "e2", "e3", "e4"], { hasMore: true });
  const merged = mergeLoadedHistory(previous, next);
  assert.equal(new Set(merged.entryIds).size, merged.entryIds.length);
  assert.equal(merged.messages.length, merged.entryIds.length);
});

test("mergeLoadedHistory: same text sent twice stays two entries", () => {
  const previous = { messages: [msg("user", "SAME"), msg("assistant", "a")], entryIds: ["u1", "a1"], oldestEntryId: "u1", hasMore: false };
  const next = {
    messages: [msg("user", "SAME"), msg("assistant", "a"), msg("user", "SAME"), msg("assistant", "b")],
    entryIds: ["u1", "a1", "u2", "a2"],
    oldestEntryId: "u1",
    hasMore: false,
  };
  const merged = mergeLoadedHistory(previous, next);
  assert.equal(merged.messages.filter((m) => m.content === "SAME").length, 2);
});

test("shouldArmPullFetch: only a touch at the tail arms", () => {
  assert.equal(shouldArmPullFetch({ touch: true, scrollTop: 600, clientHeight: 400, scrollHeight: 1000 }), true);
  assert.equal(shouldArmPullFetch({ touch: true, scrollTop: 595, clientHeight: 400, scrollHeight: 1000 }), true);
  assert.equal(shouldArmPullFetch({ touch: true, scrollTop: 300, clientHeight: 400, scrollHeight: 1000 }), false);
  assert.equal(shouldArmPullFetch({ touch: true, scrollTop: 0, clientHeight: 400, scrollHeight: 1000 }), false);
  assert.equal(shouldArmPullFetch({ touch: false, scrollTop: 600, clientHeight: 400, scrollHeight: 1000 }), false);
});

test("isPullFetchTriggered: upward past the threshold only", () => {
  assert.equal(isPullFetchTriggered(500, 440), true);
  assert.equal(isPullFetchTriggered(500, 441), false);
  assert.equal(isPullFetchTriggered(500, 380), true);
  assert.equal(isPullFetchTriggered(100, 220), false, "a downward drag never triggers");
  assert.equal(isPullFetchTriggered(500, 480, 10), true);
});

test("formatFetchTime: zero-padded local hh:mm", () => {
  assert.equal(formatFetchTime(new Date(2026, 9, 30, 7, 5)), "07:05");
  assert.equal(formatFetchTime(new Date(2026, 9, 30, 23, 59, 59)), "23:59");
  const now = new Date();
  assert.equal(formatFetchTime(now), `${String(now.getHours()).padStart(2, "0")}:${String(now.getMinutes()).padStart(2, "0")}`);
});
