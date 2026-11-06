import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// Importing the module is a parse smoke test (same pattern as the other
// SessionSidebar suites).
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
await jiti.import("./SessionSidebar.tsx");

const source = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");
const filterLib = await readFile(new URL("../lib/session-filter.ts", import.meta.url), "utf8");
const {
  DEFAULT_SESSION_FILTER_PATTERNS,
  loadSessionFilterPatterns,
  isSessionFiltered,
} = await jiti.import("../lib/session-filter.ts");

function memoryStorage(initial = new Map()) {
  return {
    getItem: (key) => (initial.has(key) ? initial.get(key) : null),
    setItem: (key, value) => { initial.set(key, String(value)); },
  };
}

function session(overrides = {}) {
  return {
    path: "/sessions/s.jsonl",
    id: "s1",
    cwd: "/work/project",
    created: "2026-01-01T00:00:00.000Z",
    modified: "2026-01-01T00:00:00.000Z",
    messageCount: 2,
    firstMessage: "hello",
    ...overrides,
  };
}

test("the sidebar reads the filter through the shared lib and derives visibleSessions from allSessions", () => {
  // The wiring goes through the production store seam (code review): a LIVE
  // useSyncExternalStore subscription so a Settings edit updates the rendered
  // sidebar in the same window — never a mount-time one-shot read. The
  // import block also carries the pi#65 hidden-count/marking helpers
  // (computeFilteredSessionIds, countFilteredSessions, setShowFilteredSessions,
  // isFamilyRowFiltered); assert the seam names are present rather than
  // pinning the exact member list, so a later addition to that block does
  // not have to touch this unrelated wiring assertion.
  const sessionFilterImportBlock = source.match(
    /import \{([\s\S]*?)\} from "@\/lib\/session-filter";/,
  );
  assert.ok(sessionFilterImportBlock, "SessionSidebar.tsx must import from @/lib/session-filter");
  for (const name of [
    "getServerSessionFilterState",
    "getSessionFilterState",
    "isSessionFiltered",
    "subscribeSessionFilter",
  ]) {
    assert.match(
      sessionFilterImportBlock[1],
      new RegExp(`\\b${name}\\b`),
      `session-filter import block must carry ${name}`,
    );
  }
  // SSR: useSyncExternalStore carries a stable getServerSnapshot (React 19
  // server-rendered client components require it).
  assert.match(source, /useSyncExternalStore\(subscribeSessionFilter, getSessionFilterState, getServerSessionFilterState\)/);
  assert.doesNotMatch(source, /loadSessionFilterPatterns\(sessionFilterStorage\(\)\)/);
  assert.doesNotMatch(source, /loadShowFilteredSessions\(sessionFilterStorage\(\)\)/);
  // visibleSessions is derived ONCE, before grouping. Local matching covers
  // names and the first-message preview; the server-side full-text match
  // (review r1) adds ids whose only match sits beyond the preview cut,
  // without restoring the oversized list payload. pi#103: the archived-
  // exclusion composes FIRST — visibleSessions reads unarchivedSessions
  // (allSessions minus the archive set), so an archived id never renders
  // regardless of the reveal toggle or the age window.
  assert.match(
    source,
    /const visibleSessions = useMemo\(\s*\(\) => filterSessionsByAge\(\s*showFilteredSessions \|\| sessionFilterPatterns\.length === 0\s*\? unarchivedSessions\s*: unarchivedSessions\.filter\(\(session\) => \(\s*!isSessionFiltered\(session, sessionFilterPatterns\)\s*&& !serverFilterMatchedIds\.has\(session\.id\)\s*\)\),\s*sessionAgeFilterDays,\s*\)/,
  );
  // ...and the exclusion itself sits directly over allSessions.
  assert.match(
    source,
    /const unarchivedSessions = useMemo\(\s*\(\) => archivedSessionIds\.size === 0\s*\?\s*allSessions\s*:\s*allSessions\.filter\(\(session\) => !archivedSessionIds\.has\(session\.id\)\)/,
  );
  // pi#82: the age window composes after the worker filter inside the
  // same memo, so one derivation stays the single source of visible rows.
  assert.match(source, /filterSessionsByAge,/);
  assert.match(source, /sessionFilter\.ageFilterDays/);
  // The server match is fetched against the pattern list it is applied to,
  // and a response from older patterns never leaks into newer ones.
  assert.match(source, /fetch\(`\/api\/sessions\/filter-match\?patterns=\$\{encodeURIComponent\(JSON\.stringify\(sessionFilterPatterns\)\)\}`/);
  assert.match(source, /serverFilterMatch\.key === sessionFilterPatternsKey\s*\?\s*serverFilterMatch\.ids\s*:\s*EMPTY_SERVER_FILTER_MATCH/);
});

test("every rendered list is fed visibleSessions, never allSessions", () => {
  // Main list: the project-scope filter runs over the already-filtered set.
  assert.match(source, /sessionsForProject\(visibleSessions, selectedProject\.key\)/);
  assert.match(source, /: visibleSessions;/);
  // Pinned groups: the directory grouping runs over the filtered set too.
  assert.match(source, /sessionsForDirectory\(visibleSessions, project\.root\)/);
  // Non-render consumers deliberately keep allSessions: notifications and
  // unread/running bookkeeping must not lose hidden sessions.
  assert.match(source, /knownSubagentIds = new Set\(\s*\n?\s*allSessions\s*\n?\s*\.filter/);
  assert.match(source, /const projects = getRecentProjects\(allSessions\);/);
});

test("a worker session matching the default pattern is hidden while a non-matching sibling is not", () => {
  const patterns = loadSessionFilterPatterns(memoryStorage());
  assert.deepEqual(patterns, [...DEFAULT_SESSION_FILTER_PATTERNS]);
  const worker = session({ id: "w", firstMessage: "Execute the pinned skill entry below" });
  const chat = session({ id: "c", firstMessage: "Chat about the release" });
  assert.equal(isSessionFiltered(worker, patterns), true, "the worker session matches");
  assert.equal(isSessionFiltered(chat, patterns), false, "the sibling does not");
});

test("matching is case-insensitive on name and firstMessage", () => {
  const patterns = ["worker"];
  assert.equal(isSessionFiltered(session({ name: "My WORKER run" }), patterns), true);
  assert.equal(isSessionFiltered(session({ name: undefined, firstMessage: "starting WORKER step" }), patterns), true);
  assert.equal(isSessionFiltered(session({ name: "Deploy", firstMessage: "ship it" }), patterns), false);
});

test("an empty stored pattern array disables hiding; the toggle re-reveals with no visual difference", () => {
  const empty = loadSessionFilterPatterns(memoryStorage(new Map([
    ["pi-web:session-filter-patterns", "[]"],
  ])));
  assert.deepEqual(empty, []);
  const worker = session({ firstMessage: "Execute the pinned skill entry" });
  assert.equal(isSessionFiltered(worker, empty), false, "an empty list hides nothing");
  // The component-level contract: showFilteredSessions OR an empty pattern
  // list short-circuits to the unfiltered set — the re-revealed rows are
  // byte-identical to the pre-feature rendering. (pi#103: that set is now
  // unarchivedSessions, so an ARCHIVED session still never re-renders via
  // the toggle — the exclusion composes ahead of it.)
  assert.match(
    source,
    /showFilteredSessions \|\| sessionFilterPatterns\.length === 0\s*\n\s*\? unarchivedSessions/,
  );
});

test("the sidebar never reads the filter storage keys directly (only through the lib seam)", () => {
  assert.doesNotMatch(source, /pi-web:session-filter-patterns/);
  assert.doesNotMatch(source, /pi-web:show-filtered-sessions/);
  assert.match(filterLib, /export function sessionFilterStorage\(\)/);
});
