// carries: hooks/usePiSubagentPanel.ts, lib/pi-subagent-panel.ts
// polyforge#255 round 2 #5/#6: the 2 s poll is single-flight (a slow read
// still lands; no second read starts beside it), a snapshot with no overlap
// replaces the transcript instead of dropping the gap, `hasEarlier` is only
// set by the first load and an earlier page, and a card's own tool call id
// wins over the run's parent call.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { PiSubagentPanelController } = await jiti.import("./usePiSubagentPanel.ts");
const { mergePiSubagentEntries } = await jiti.import("@/lib/pi-subagent-panel");

const NOW = Date.UTC(2026, 10, 23, 12, 0, 0);
const iso = (offset) => new Date(NOW + offset).toISOString();
const runBody = (fields = {}) => ({ run: { sessionId: "child-1", parentSessionId: "parent-1", parentToolCallId: "toolu-run", profile: "explore", description: "Find", status: "running", createdAt: iso(-12_000), ...fields } });
const assistant = (text) => ({ role: "assistant", content: [{ type: "text", text }] });
const sessionBody = (entries, { hasMore = false } = {}) => ({ info: { cwd: "/repo" }, context: { messages: entries.map(([, message]) => message), entryIds: entries.map(([id]) => id), hasMore, oldestEntryId: entries[0]?.[0] ?? null } });
const response = (status, body) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

function controller(handle, fields = {}) {
  const calls = [];
  const c = new PiSubagentPanelController({
    childSessionId: "child-1",
    parentSessionId: "parent-1",
    fetchImpl: async (url, init = {}) => { calls.push({ url: String(url), init }); return handle(String(url), init); },
    ...fields,
  });
  return { c, calls };
}

function deferred() {
  let resolve;
  const promise = new Promise((r) => { resolve = r; });
  return { promise, resolve };
}

test("polyforge#255 r2: a read slower than the 2 s interval still lands; no second read starts beside it", async () => {
  const gate = deferred();
  let reads = 0;
  const { c, calls } = controller(async (url) => {
    if (url === "/api/subagents/child-1") {
      reads += 1;
      if (reads === 1) await gate.promise;
      return response(200, runBody());
    }
    return response(200, sessionBody([["e-1", assistant("first")]]));
  });
  const load = c.load();
  const refresh = c.refresh();
  await Promise.resolve();
  // The first read is still in flight: the tick must not start a second run
  // read (and must not invalidate the first one).
  assert.equal(calls.filter((call) => call.url === "/api/subagents/child-1").length, 1, "single-flight: one run read at a time");
  gate.resolve();
  await Promise.all([load, refresh]);
  assert.equal(c.snapshot.phase, "ready", "the slow read landed");
  assert.deepEqual(c.snapshot.entries.map((entry) => entry.id), ["e-1"], "its transcript landed");
});

test("polyforge#255 r2: a poll snapshot that does not overlap the loaded transcript replaces it instead of dropping the gap", () => {
  const merged = mergePiSubagentEntries(
    [{ id: "old-1", message: assistant("old 1") }, { id: "old-2", message: assistant("old 2") }],
    [{ id: "new-1", message: assistant("new 1") }, { id: "new-2", message: assistant("new 2") }],
    "append",
  );
  assert.deepEqual(merged.map((entry) => entry.id), ["new-1", "new-2"], "a non-overlapping snapshot is a hole: show it whole");
  const overlapping = mergePiSubagentEntries(
    [{ id: "old-1", message: assistant("old 1") }, { id: "old-2", message: assistant("old 2") }],
    [{ id: "old-2", message: assistant("old 2") }, { id: "new-1", message: assistant("new 1") }],
    "append",
  );
  assert.deepEqual(overlapping.map((entry) => entry.id), ["old-1", "old-2", "new-1"], "an overlapping snapshot still appends only the new tail");
});

test("polyforge#255 r2: hasEarlier is set by the first load and an earlier page, never flipped back by a poll", async () => {
  let phase = 0;
  const { c } = controller((url) => {
    if (url === "/api/subagents/child-1") {
      phase += 1;
      return response(200, runBody());
    }
    if (url.includes("/context?")) {
      return response(200, sessionBody([["e-0", assistant("earlier")]], { hasMore: false }));
    }
    return response(200, sessionBody([["e-1", assistant("first")]], { hasMore: true }));
  });
  await c.load();
  assert.equal(c.snapshot.hasEarlier, true, "the first page says there is more");
  await c.loadEarlier();
  assert.equal(c.snapshot.hasEarlier, false, "the earlier page reached the start");
  const before = c.snapshot.entries.map((entry) => entry.id);
  await c.refresh();
  assert.deepEqual(c.snapshot.entries.map((entry) => entry.id), before, "a poll neither duplicates nor drops the loaded history");
  assert.equal(c.snapshot.hasEarlier, false, "a poll does not turn the button back on");
});

test("polyforge#255 r2: a card's own tool call id wins; an unreadable ancestor chain yields null, never another chat's id", async () => {
  const preferred = controller((url) => {
    if (url === "/api/subagents/child-1") return response(200, runBody({ parentSessionId: "other-chat", parentToolCallId: "toolu-other" }));
    return response(200, sessionBody([["e-1", assistant("first")]]));
  }, { preferredToolCallId: "toolu-card" });
  await preferred.c.load();
  assert.equal(preferred.c.snapshot.launchToolCallId, "toolu-card", "the card's own call id is the launch position");

  const unreadable = controller((url) => {
    if (url === "/api/subagents/child-1") return response(200, runBody({ parentSessionId: "other-chat", parentToolCallId: "toolu-other" }));
    if (url === "/api/subagents/other-chat") return response(404, { error: "Subagent not found" });
    return response(200, sessionBody([["e-1", assistant("first")]]));
  });
  await unreadable.c.load();
  assert.equal(unreadable.c.snapshot.launchToolCallId, null, "a chain that cannot be read offers no jump, not a foreign id");
});
