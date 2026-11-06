import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

const { prefetchSessionView } = await import(new URL("../lib/session-prefetch.ts", import.meta.url));
const { getSessionViewSnapshot, clearSessionViewCache } = await import(new URL("../lib/session-view-cache.ts", import.meta.url));

const DETAIL_PAYLOAD = {
  snapshotRevision: "rev-1",
  leafId: "leaf-1",
  tree: [{ id: "leaf-1" }],
  context: {
    messages: [{ role: "user", content: "hello" }],
    entryIds: ["leaf-1"],
    oldestEntryId: "leaf-1",
    hasMore: false,
    turnsBefore: 2475,
    thinkingLevel: "off",
    model: { provider: "p", modelId: "m" },
  },
  stats: { totalMessages: 1 },
  totalActiveMs: 5,
};

function withFetch(handler, fn) {
  const original = globalThis.fetch;
  globalThis.fetch = handler;
  return Promise.resolve(fn()).finally(() => {
    globalThis.fetch = original;
    clearSessionViewCache();
  });
}

test("prefetch feeds the parsed detail response into the view snapshot cache", () => {
  let calls = 0;
  return withFetch(async () => {
    calls += 1;
    return { ok: true, json: async () => DETAIL_PAYLOAD };
  }, async () => {
    const cached = await prefetchSessionView("sess-1");
    assert.equal(cached, true);
    assert.equal(calls, 1);
    const snapshot = getSessionViewSnapshot("sess-1");
    assert.ok(snapshot);
    assert.equal(snapshot.revision, "rev-1");
    assert.equal(snapshot.entryIds[0], "leaf-1");
    assert.equal(snapshot.messages.length, 1);
    // the offset is stored with the cursor it belongs to.
    assert.equal(snapshot.oldestEntryId, "leaf-1");
    assert.equal(snapshot.turnsBefore, 2475);
  });
});

test("concurrent prefetches for one session dedupe into a single request", () => {
  let calls = 0;
  return withFetch(async () => {
    calls += 1;
    await new Promise((resolve) => setTimeout(resolve, 20));
    return { ok: true, json: async () => DETAIL_PAYLOAD };
  }, async () => {
    const [a, b] = await Promise.all([prefetchSessionView("sess-2"), prefetchSessionView("sess-2")]);
    assert.equal(a, true);
    assert.equal(b, true);
    assert.equal(calls, 1);
  });
});

test("a failed or uncacheable response resolves false without touching the cache", () => {
  let calls = 0;
  return withFetch(async () => {
    calls += 1;
    if (calls === 1) return { ok: false, status: 500, json: async () => ({}) };
    return { ok: true, json: async () => ({ snapshotRevision: "rev-2" }) }; // no context
  }, async () => {
    assert.equal(await prefetchSessionView("sess-3"), false);
    assert.equal(await prefetchSessionView("sess-4"), false);
    assert.equal(getSessionViewSnapshot("sess-3"), null);
    assert.equal(getSessionViewSnapshot("sess-4"), null);
    // A later retry may issue a fresh request (the failed flight is not cached).
    assert.ok(calls >= 2);
  });
});
