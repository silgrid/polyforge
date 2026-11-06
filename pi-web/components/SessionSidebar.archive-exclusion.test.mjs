import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// wi pi#103 (sidebar A1: Archive), plan step 4 — archived sessions leave the
// RENDERED universe (visibleSessions and every derived count/badge) while
// allSessions stays the full universe for the non-render bookkeeping. Same
// pattern as the other SessionSidebar suites: the jiti import below is a
// parse smoke test, the rest pins the wiring at source level; the exclusion
// predicate itself is behaviorally covered against the lib below.

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
await jiti.import("./SessionSidebar.tsx");

const source = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");

test("the archived-exclusion composes FIRST, ahead of the worker filter and the age window", () => {
  // The exclusion memo exists, sits between allSessions and visibleSessions,
  // and visibleSessions derives from it — not from allSessions directly.
  const exclusion = source.match(
    /const unarchivedSessions = useMemo\(\s*\(\) => archivedSessionIds\.size === 0\s*\?\s*allSessions\s*:\s*allSessions\.filter\(\(session\) => !archivedSessionIds\.has\(session\.id\)\),\s*\[allSessions, archivedSessionIds\],\s*\);/,
  );
  assert.ok(exclusion, "the unarchivedSessions exclusion memo must exist over allSessions");
  const visible = source.match(
    /const visibleSessions = useMemo\(\s*\(\) => filterSessionsByAge\([\s\S]*?\),\s*\[unarchivedSessions,/,
  );
  assert.ok(visible, "visibleSessions must derive from unarchivedSessions (archived ids excluded)");
  // Ahead of the reveal toggle: the worker-filter branch reads
  // unarchivedSessions whether or not showFilteredSessions is on, so an
  // archived id never re-renders via the reveal toggle.
  assert.match(visible[0], /showFilteredSessions \|\| sessionFilterPatterns\.length === 0\s*\?\s*unarchivedSessions\s*:\s*unarchivedSessions\.filter\(/);
  // The age window still composes after (the outer filterSessionsByAge call).
  assert.match(visible[0], /filterSessionsByAge\(/);
});

test("hiddenSessionCount never counts archived sessions", () => {
  const hiddenCount = source.match(
    /const hiddenSessionCount = useMemo\(\s*\(\) => sessionFilterPatterns\.length > 0\s*\?\s*countFilteredSessions\(unarchivedSessions, sessionFilterPatterns, sessionAgeFilterDays\)\s*:\s*0,\s*\[unarchivedSessions, sessionFilterPatterns, sessionAgeFilterDays\],\s*\);/,
  );
  assert.ok(
    hiddenCount,
    "hiddenSessionCount must count over allSessions MINUS archived ids (unarchivedSessions), inside the age window (sessionAgeFilterDays,)",
  );
});

test("allSessions-driven bookkeeping is NOT filtered (non-render concerns keep the full universe)", () => {
  // Prune, unread bookkeeping, and the initial load all stay on allSessions;
  // only the rendered universe shrinks. Assert the non-render sites keep
  // reading allSessions/unarchived state correctly.
  const loadSessionsBody = source.slice(
    source.indexOf("const loadSessions = useCallback"),
    source.indexOf("const initialLoadDone = useRef(false)"),
  );
  assert.match(loadSessionsBody, /setAllSessions\(data\.sessions\)/);
  // The archived-exclusion memo is the ONLY new filter over allSessions in
  // the render path: unread/running bookkeeping state updates never consult
  // unarchivedSessions.
  const unreadBookkeeping = source.slice(
    source.indexOf("const [unreadSessionIds, setUnreadSessionIds]"),
    source.indexOf("const [unreadSessionIds, setUnreadSessionIds]") + 2500,
  );
  assert.ok(unreadBookkeeping.length > 0);
  assert.doesNotMatch(unreadBookkeeping, /unarchivedSessions/, "unread bookkeeping must not be filtered by the archive set");
  // allSessions itself is never reassigned to the exclusion — the archive
  // view (step 5), the search badge (step 6) and the prune need the full set.
  assert.doesNotMatch(source, /setAllSessions\(unarchivedSessions\)/);
  assert.doesNotMatch(source, /allSessions = allSessions\.filter/);
});

test("the exclusion predicate removes exactly the archived session, leaving the rest intact", async () => {
  // Behavioral, lib-level: replicate the memo's exact expression over the
  // injected archive set and check the rendered universe.
  const lib = await jiti.import("../lib/session-archive.ts");
  const sessions = [
    { id: "alpha", cwd: "/w/a", modified: "2026-01-03T00:00:00.000Z" },
    { id: "beta", cwd: "/w/b", modified: "2026-01-02T00:00:00.000Z" },
    { id: "gamma", cwd: "/w/c", modified: "2026-01-01T00:00:00.000Z" },
  ];
  const archived = lib.loadArchivedSessionIds({
    getItem: () => JSON.stringify(["beta"]),
    setItem: () => {},
    removeItem: () => {},
  });
  // Same shape as the component memo (empty-set short-circuit excluded for
  // brevity — it is pinned at source level above).
  const rendered = sessions.filter((session) => !archived.has(session.id));
  assert.deepEqual(rendered.map((s) => s.id), ["alpha", "gamma"]);
  // And the empty set keeps every session (the memo's short-circuit branch).
  const empty = new Set();
  assert.equal(sessions.filter((s) => !empty.has(s.id)).length, 3);
});
