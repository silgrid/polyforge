import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// wi pi#103 (sidebar A1: Archive), plan step 2 — the archive set wired into
// SessionSidebar state, no UI yet. Same pattern as the other SessionSidebar
// suites: the jiti import below is a parse smoke test; the rest pins the
// wiring seams at source level. The state machine itself is behaviorally
// covered by lib/session-archive.test.mjs.

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
await jiti.import("./SessionSidebar.tsx");

const source = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");

test("the sidebar owns archive state initialized from the shared lib, SSR-safely", () => {
  assert.match(source, /const \[archivedSessionIds, setArchivedSessionIds\] = useState<ReadonlySet<string>>\(\s*\(\) => new Set\(loadArchivedSessionIds\(sessionArchiveStorage\(\)\)\),\s*\)/);
  // The lib seam is imported from the module, not re-implemented inline.
  const importBlock = source.match(/import \{([\s\S]*?)\} from "@\/lib\/session-archive";/);
  assert.ok(importBlock, "SessionSidebar.tsx must import from @/lib/session-archive");
  for (const name of [
    "ARCHIVED_SESSIONS_STORAGE_KEY",
    "loadArchivedSessionIds",
    "pruneArchivedSessionIds",
    "saveArchivedSessionIds",
    "sessionArchiveStorage",
  ]) {
    assert.match(importBlock[1], new RegExp(`\\b${name}\\b`), `session-archive import block must carry ${name}`);
  }
});

test("the archive set persists on change, mirroring the unread-ids effect", () => {
  assert.match(source, /useEffect\(\(\) => \{\s*saveArchivedSessionIds\(archivedSessionIds, sessionArchiveStorage\(\)\);\s*\}, \[archivedSessionIds\]\)/);
});

test("external-deletion pruning rides loadSessions' success path next to the unread prune", () => {
  const loadSessionsBody = source.slice(
    source.indexOf("const loadSessions = useCallback"),
    source.indexOf("const initialLoadDone = useRef(false)"),
  );
  assert.ok(loadSessionsBody.length > 0, "loadSessions body must be found");
  assert.match(loadSessionsBody, /setArchivedSessionIds\(\(prev\) =>\s*pruneArchivedSessionIds\(/);
  assert.match(loadSessionsBody, /loadedSessionIds = new Set\(data\.sessions\.map\(\(session\) => session\.id\)\)/);
  assert.match(loadSessionsBody, /!loadedSessionIds\.has\(id\)/);
  // The prune only runs on the success path — after setAllSessions.
  assert.ok(
    loadSessionsBody.indexOf("setAllSessions(data.sessions)")
      < loadSessionsBody.indexOf("setArchivedSessionIds((prev) =>"),
  );
});

test("running activity force-unarchives; passive unread does not", () => {
  const effect = source.match(
    /useEffect\(\(\) => \{\s*if \(runningSessionIds\.size === 0\) return;\s*setArchivedSessionIds\(\(prev\) => pruneArchivedSessionIds\(prev, runningSessionIds\)\);\s*\}, \[runningSessionIds\]\);/,
  );
  assert.ok(effect, "an effect keyed on runningSessionIds must remove running ids from the archive set");
  // Only running ids (never unread ids) may force-unarchive: no other
  // setArchivedSessionIds site references unreadSessionIds.
  for (const site of source.matchAll(/setArchivedSessionIds\(/g)) {
    const around = source.slice(site.index, site.index + 400);
    assert.doesNotMatch(around, /unreadSessionIds/, "unread markers must never touch the archive set");
  }
});

test("a foreign tab's write reloads the set (storage-event precedent), diff-guarded", () => {
  const effect = source.match(/useEffect\(\(\) => \{\s*if \(typeof window === "undefined"\) return;\s*const onArchiveStorage = \(event: StorageEvent\) => \{/);
  assert.ok(effect, "a window storage listener effect must exist");
  assert.match(source, /if \(event\.key !== ARCHIVED_SESSIONS_STORAGE_KEY\) return;/);
  assert.match(source, /window\.addEventListener\("storage", onArchiveStorage\)/);
  // Diff guard: an identical reload must not change state (otherwise the
  // persist effect would echo the write back and ping-pong across tabs).
  assert.match(source, /if \(next\.size === prev\.size && \[\.\.\.next\]\.every\(\(id\) => prev\.has\(id\)\)\) return prev;/);
});

test("archive/unarchive are local set mutations — no fetch, no delete, no tab path", () => {
  const handlers = source.slice(
    source.indexOf("const archiveSession = useCallback"),
    source.indexOf("// Persist the archive set so it survives a browser refresh"),
  );
  assert.ok(handlers.length > 0, "archiveSession/unarchiveSession handlers must be found");
  assert.match(handlers, /const archiveSession = useCallback\(\(id: string\) => \{/);
  assert.match(handlers, /const unarchiveSession = useCallback\(\(id: string\) => \{/);
  assert.doesNotMatch(handlers, /fetch\(/);
  assert.doesNotMatch(handlers, /onSessionDeleted|onDeleted|window\.open/);
});

test("merely opening a session never changes the archive set (view-only open)", () => {
  // The cleared-on-select effect (unread bookkeeping) is keyed on
  // selectedSessionId; it must not touch the archive set.
  const selectEffect = source.match(
    /useEffect\(\(\) => \{\s*if \(!selectedSessionId\) return;\s*setUnreadSessionIds\(\(prev\) => \{[\s\S]*?\}, \[selectedSessionId\]\);/,
  );
  assert.ok(selectEffect, "the unread-clear-on-select effect must be found");
  assert.doesNotMatch(selectEffect[0], /archiv/i, "selecting a session must never mutate the archive set");
  // And handleSelectSessionFromList (the open path shared by the list, the
  // search and later the archive view) never references the archive set.
  const openHandler = source.slice(
    source.indexOf("const handleSelectSessionFromList = useCallback"),
    source.indexOf("const handleSelectSessionFromList = useCallback") + 1200,
  );
  assert.doesNotMatch(openHandler, /archiv/i, "the session-open path must never mutate the archive set");
});
