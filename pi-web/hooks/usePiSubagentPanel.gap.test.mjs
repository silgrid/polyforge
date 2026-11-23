// carries: hooks/usePiSubagentPanel.ts, lib/pi-subagent-panel.ts
// polyforge#255 round 3 #2 (r1#6a residue): a poll snapshot with no overlap
// replaces the transcript; the snapshot's own hasMore must turn 「加载更早」
// back on, or the middle entries are lost for the rest of the open panel.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { PiSubagentPanelController } = await jiti.import("./usePiSubagentPanel.ts");

const NOW = Date.UTC(2026, 10, 23, 12, 0, 0);
const iso = (offset) => new Date(NOW + offset).toISOString();
const runBody = () => ({ run: { sessionId: "child-1", parentSessionId: "parent-1", parentToolCallId: "toolu-run", profile: "explore", description: "Find", status: "running", createdAt: iso(-12_000) } });
const assistant = (text) => ({ role: "assistant", content: [{ type: "text", text }] });
const entries = (from, count) => Array.from({ length: count }, (_, i) => ({ id: `e-${from + i}`, message: assistant(`m${from + i}`) }));
const sessionBody = (list, hasMore) => ({ info: { cwd: "/repo" }, context: { messages: list.map((e) => e.message), entryIds: list.map((e) => e.id), hasMore, oldestEntryId: list[0]?.id ?? null } });
const response = (status, body) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

function controller(handle) {
  const c = new PiSubagentPanelController({
    childSessionId: "child-1",
    parentSessionId: "parent-1",
    fetchImpl: async (url) => handle(String(url)),
  });
  return c;
}

test("polyforge#255 r3: a non-overlapping replacement adopts the snapshot's hasMore, so the middle can be loaded", async () => {
  let call = 0;
  const c = controller((url) => {
    if (url === "/api/subagents/child-1") return response(200, runBody());
    if (url.includes("/context?")) {
      return response(200, sessionBody(entries(-50, 50), false));
    }
    call += 1;
    if (call === 1) return response(200, sessionBody(entries(1, 3), false));
    return response(200, sessionBody(entries(10, 50), true));
  });
  await c.load();
  assert.deepEqual(c.snapshot.entries.map((entry) => entry.id), ["e-1", "e-2", "e-3"], "the first read starts small");
  assert.equal(c.snapshot.hasEarlier, false, "the first read saw the whole (small) history");

  await c.refresh();
  assert.deepEqual(c.snapshot.entries.map((entry) => entry.id), entries(10, 50).map((entry) => entry.id), "the non-overlapping snapshot replaces the transcript");
  assert.equal(c.snapshot.hasEarlier, true, "the replacement snapshot's hasMore re-enables 加载更早");

  await c.loadEarlier();
  assert.deepEqual(c.snapshot.entries.map((entry) => entry.id), [...entries(-50, 50).map((entry) => entry.id), ...entries(10, 50).map((entry) => entry.id)], "the middle entries are reachable");
  assert.equal(c.snapshot.hasEarlier, false, "the earlier page reached the start");
});
