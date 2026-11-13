import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// wi pi#103 (sidebar A1: Archive), plan step 5 — the global 「已归档 N」 entry
// and the replacement archive view. Same pattern as the other SessionSidebar
// suites: the jiti import below is a parse smoke test; the rest pins the
// wiring at source level. The grouping model is behaviorally covered by
// lib/session-archive.test.mjs.

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
await jiti.import("./SessionSidebar.tsx");

const source = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");

test("the entry renders the GLOBAL archived count and is gated on count > 0", () => {
  // The count derives from the archived slice of allSessions (global — never
  // sessionsForProject), so it is stable across project switches.
  const slice = source.match(
    /const archivedSessions = useMemo\(\s*\(\) => archivedSessionIds\.size === 0\s*\?\s*EMPTY_ARCHIVED_SESSIONS\s*:\s*allSessions\.filter\(\(session\) => archivedSessionIds\.has\(session\.id\)\),\s*\[allSessions, archivedSessionIds\],\s*\);/,
  );
  assert.ok(slice, "the archived slice must be derived from the FULL allSessions universe");
  assert.doesNotMatch(slice[0], /sessionsForProject|selectedProject/, "the count must never be project-scoped");
  // The entry (and everything about it) only renders when the count is > 0 —
  // at N=0 no 已归档/Archived text may exist in the tree.
  const entry = source.match(
    /\{archivedSessions\.length > 0 && \(\s*\/\* 「已归档 N」 entry[\s\S]*?setArchiveViewOpen\(true\)[\s\S]*?\}\)\}/,
  );
  assert.ok(entry, "the entry button must be gated on archivedSessions.length > 0");
  assert.match(entry[0], /t\("sidebar\.archivedSessions", \{ count: archivedSessions\.length \}\)/);
  assert.match(entry[0], /setArchiveViewOpen\(true\)/);
});

test("the archive view REPLACES the list region (not an inline block)", () => {
  // The SessionSearch children area is a ternary: the view INSTEAD of the
  // windowed list — a non-archived row cannot render while the view is open.
  const region = source.match(
    /\{archiveViewOpen \? \(\s*\/\* Archive view \(wi pi#103\): temporarily REPLACES[\s\S]*?\) : \(\s*<div\s+ref=\{listScrollRef\}/,
  );
  assert.ok(region, "the pane's list region must be an archiveViewOpen ternary over the windowed list");
});

test("the view has a prominent back button wired to close, and auto-returns at N=0", () => {
  const view = source.match(
    /\/\* Archive view \(wi pi#103\): temporarily REPLACES[\s\S]*?archivedGroups\.map/,
  );
  assert.ok(view, "the archive view body must be found");
  assert.match(view[0], /title=\{t\("sidebar\.archiveBack"\)\}/);
  assert.match(view[0], /aria-label=\{t\("sidebar\.archiveBack"\)\}/);
  assert.match(view[0], /onClick=\{\(\) => setArchiveViewOpen\(false\)\}/);
  // Auto-return: when the set empties while the view is open, it closes so
  // the normal list restores exactly.
  const autoReturn = source.match(
    /useEffect\(\(\) => \{\s*if \(archiveViewOpen && archivedSessions\.length === 0\) setArchiveViewOpen\(false\);\s*\}, \[archiveViewOpen, archivedSessions\.length\]\);/,
  );
  assert.ok(autoReturn, "opening the view with an emptying set must auto-close it");
});

test("the view groups by project with path-derived labels (pinned-group convention)", () => {
  const view = source.match(/archivedGroups\.map\(\(group\) => \([\s\S]*?\)\)\}/);
  assert.ok(view, "the view must render archivedGroups");
  assert.match(view[0], /title=\{group\.root\}/, "the full path rides the title attr");
  assert.match(view[0], /\{displayCwd\(group\.root, homeDir\)\}/, "the label uses the displayCwd convention");
  assert.match(source, /const archivedGroups = useMemo\(\s*\(\) => groupArchivedSessionsByProject\(archivedSessions\),\s*\[archivedSessions\],\s*\);/);
});

test("archive rows offer ONLY open (view-only) and unarchive", () => {
  const row = source.match(
    /function ArchiveSessionRow\(\{[\s\S]*?\n\}\n\n\/\*\*\s*\n \* Compact per-project activity badges/,
  );
  assert.ok(row, "the ArchiveSessionRow component must be found");
  const body = row[0];
  // Open: both the wrapper click and the anchor's plain activation go through
  // onOpen — and NEVER through anything that mutates the archive set.
  assert.match(body, /onClick=\{\(\) => onOpen\(session\)\}/);
  assert.match(body, /if \(!isPlainActivation\(event\)\) return;/);
  assert.match(body, /onOpen\(session\);/);
  // The open handlers (wrapper + anchor) reference nothing archive-related:
  // the only archive-adjacent surface in the row is the unarchive button.
  const openHandlers = [
    body.match(/<div\n      onClick=\{\(\) => onOpen\(session\)\}[\s\S]*?<div style=\{\{ flex: 1, minWidth: 0 \}\}>/),
    body.match(/onClick=\{\(event\) => \{[\s\S]*?\}\}\n          >/),
  ];
  for (const handler of openHandlers) {
    assert.ok(handler, "the open handler slices must be found");
    assert.doesNotMatch(handler[0], /unarchive|archivedSessionIds|setArchived/i);
  }
  // The row mutates nothing directly — it only calls its injected props.
  assert.doesNotMatch(body, /setArchivedSessionIds|archiveSession|unarchiveSession/);
  // Exactly one row action: unarchive. No delete/rename/new-tab/context menu.
  assert.match(body, /title=\{t\("sidebar\.unarchive"\)\}/);
  assert.match(body, /onUnarchived\(session\.id\)/);
  assert.doesNotMatch(body, /sidebar\.delete|sidebar\.rename|openInNewTab|window\.open|dispatchSessionRowContextMenu|handleDelete|startRename/);
});

test("archive rows show unread indication via the shared indicator", () => {
  const row = source.match(
    /function ArchiveSessionRow\(\{[\s\S]*?\n\}\n\n\/\*\*\s*\n \* Compact per-project activity badges/,
  );
  assert.ok(row, "the ArchiveSessionRow component must be found");
  assert.match(row[0], /isUnread \? \(\s*<UnreadSessionIndicator \/>/);
  // And the call site feeds it from the unread-ids set (passive unread stays
  // archived — the dot merely shows in the view).
  assert.match(source, /isUnread=\{unreadSessionIds\.has\(session\.id\)\}/);
});

test("the entry, back and unarchive strings exist in all three locales", async () => {
  for (const locale of ["zh-CN", "zh-TW", "en"]) {
    const messages = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    assert.match(messages, /"sidebar\.archivedSessions": "[^"]*\{count\}[^"]*"/, `${locale} archivedSessions carries the count`);
    assert.match(messages, /"sidebar\.archiveBack": "/, `${locale} archiveBack exists`);
    assert.match(messages, /"sidebar\.unarchive": "/, `${locale} unarchive exists`);
  }
  // Locator contracts, exactly: zh-CN entry 已归档 {count}, en Archived {count};
  // back 返回/Back; unarchive 取消归档/取消封存/Unarchive.
  const zhCN = await readFile(new URL("../lib/i18n/messages/zh-CN.ts", import.meta.url), "utf8");
  const en = await readFile(new URL("../lib/i18n/messages/en.ts", import.meta.url), "utf8");
  assert.match(zhCN, /"sidebar\.archivedSessions": "已归档 \{count\}"/);
  assert.match(en, /"sidebar\.archivedSessions": "Archived \{count\}"/);
  assert.match(zhCN, /"sidebar\.archiveBack": "返回"/);
  assert.match(en, /"sidebar\.archiveBack": "Back"/);
  assert.match(zhCN, /"sidebar\.unarchive": "取消归档"/);
  assert.match(en, /"sidebar\.unarchive": "Unarchive"/);
});
