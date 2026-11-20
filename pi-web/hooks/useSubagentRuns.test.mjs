// carries: hooks/useSubagentRuns.ts, lib/background-tasks-client.ts, app/api/agent/*/subagent-runs/*
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test, { mock } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { nextSubagentRunsRows, parseSubagentRunsBody } = await jiti.import("@/lib/background-tasks-client");
const source = await readFile(new URL("./useSubagentRuns.ts", import.meta.url), "utf8");

/** One live run row, hand-made (the wire shape the route promises). */
function row(fields = {}) {
  return {
    sessionId: "sub-1",
    parentSessionId: "parent-1",
    profile: "general-purpose",
    description: "Audit",
    status: "running",
    createdAt: "2026-11-18T12:00:00.000Z",
    ...fields,
  };
}

test("the rows machine drops a session's late answer after a switch (切换会话不串项)", () => {
  const ofA = [row({ sessionId: "sub-a" })];
  const ofB = [row({ sessionId: "sub-b", status: "queued", createdAt: "2026-11-18T12:00:10.000Z" })];

  // Session A's poll answer lands while A is current: its rows show.
  assert.deepEqual(nextSubagentRunsRows([], 1, { seq: 1, status: 200, body: { runs: ofA } }).map((r) => r.sessionId), ["sub-a"]);
  // The switch to B bumps the sequence; A's late answer (seq 1) changes
  // nothing — B's rows stay B's.
  assert.equal(nextSubagentRunsRows(ofB, 2, { seq: 1, status: 200, body: { runs: ofA } }), ofB);
  // An unmount's cleanup bump drops the same way.
  assert.equal(nextSubagentRunsRows(ofA, 5, { seq: 4, status: 200, body: { runs: [] } }), ofA);
  // B's own answer replaces the rows.
  assert.deepEqual(nextSubagentRunsRows(ofA, 2, { seq: 2, status: 200, body: { runs: ofB } }).map((r) => r.sessionId), ["sub-b"]);
  // A failed or malformed poll keeps the last rows: a session's rows are
  // never an error surface.
  assert.equal(nextSubagentRunsRows(ofB, 3, { seq: 3, status: 500, body: null }), ofB);
  assert.equal(nextSubagentRunsRows(ofB, 4, { seq: 4, status: 200, body: { runs: "no" } }), ofB);
});

test("the rows parser accepts only a whole valid rows array", () => {
  assert.deepEqual(parseSubagentRunsBody({ runs: [row({ status: "queued" })] })?.map((r) => r.status), ["queued"]);
  // A row without the running/queued shape refuses the whole answer.
  assert.equal(parseSubagentRunsBody({ runs: [row(), { sessionId: "x", status: "completed", createdAt: "z" }] }), null);
  assert.equal(parseSubagentRunsBody({ runs: [row(), null] }), null);
  assert.equal(parseSubagentRunsBody(null), null);
  assert.equal(parseSubagentRunsBody({}), null);
});

test("the hook wires the machine and clears the rows on a session switch (a wiring check)", () => {
  // The behaviour lives in lib/background-tasks-client.ts and its tests; this
  // pins only the wiring: the hook runs that machine, clears the rows when
  // the session changes, and its cleanup invalidates the in-flight answer.
  assert.match(source, /setRuns\(\(prev\) => nextSubagentRunsRows\(prev, seqRef\.current, \{ seq, status: response\.status, body \}\)\);/);
  assert.match(source, /setRuns\(\[\]\);/);
  assert.match(source, /seqRef\.current \+= 1;/);
});

test("the poll falls silent on an idle session with nothing live (G11: the graph dock's click window admits no session read)", async () => {
  // G11 (e2e/polyforge-graph.mjs): a 2 s poll that reads while the session
  // sits idle with nothing live lands inside that suite's click window. The
  // tick may read only while it can learn something — the session is running
  // a reply (a run is dispatched there) or the last answer held a live row;
  // the mount discovery read (and the page's return to visibility) still
  // runs, so a reload that lands on a live run finds it.
  const shim = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
  const hookJiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shim } });
  const { renderHook } = await import(pathToFileURL(shim).href);
  const { useSubagentRuns } = await hookJiti.import("./useSubagentRuns.ts");
  const realFetch = globalThis.fetch;
  const reads = [];
  let answer = { runs: [] };
  globalThis.fetch = (url) => {
    reads.push(String(url));
    return Promise.resolve(new Response(JSON.stringify(answer), { status: 200, headers: { "Content-Type": "application/json" } }));
  };
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  try {
    const hook = renderHook(([id, running]) => useSubagentRuns(id, running), ["s1", false]);
    const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };
    const tick = async (ms) => { mock.timers.tick(ms); await settle(); hook.flush(); await settle(); hook.flush(); };
    await settle();
    hook.flush();
    assert.equal(reads.length, 1, "the mount discovery read happened once");
    // An idle session with nothing live polls no further.
    for (let index = 0; index < 3; index++) await tick(2_000);
    assert.equal(reads.length, 1, `an idle session with nothing live read ${reads.length - 1} more time(s) — the graph dock's click window admits no session read (G11)`);
    // A running reply re-arms the tick.
    hook.rerender(["s1", true]);
    await tick(2_000);
    assert.equal(reads.length, 2, "a running session polls");
    // A live row keeps the tick reading after the reply ends…
    answer = { runs: [row()] };
    await tick(2_000);
    assert.equal(reads.length, 3, "the running poll re-read");
    hook.rerender(["s1", false]);
    await tick(2_000);
    assert.equal(reads.length, 4, "a live run keeps the poll on while the session is idle");
    // …and the read that sees it settled is the last one.
    answer = { runs: [] };
    await tick(2_000);
    assert.equal(reads.length, 5, "the settling read");
    for (let index = 0; index < 3; index++) await tick(2_000);
    assert.equal(reads.length, 5, "the poll stopped once nothing is live and the session is idle");
    hook.unmount();
  } finally {
    mock.timers.reset();
    globalThis.fetch = realFetch;
  }
});
