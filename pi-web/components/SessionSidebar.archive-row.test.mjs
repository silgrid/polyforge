import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// wi pi#103 (sidebar A1: Archive), plan step 3 — the row-menu Archive
// action. Parse smoke (jiti) + source-level wiring assertions in the
// repo's established SessionSidebar.*.test.mjs pattern. The e2e locator
// contract pins the label: the accessible name must be exactly
// 归档 / 封存 / Archive.

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
await jiti.import("./SessionSidebar.tsx");

const source = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");
const sessionItemSource = source.slice(source.indexOf("function SessionItem({"));
const messageFiles = {
  en: await readFile(new URL("../lib/i18n/messages/en.ts", import.meta.url), "utf8"),
  zhCN: await readFile(new URL("../lib/i18n/messages/zh-CN.ts", import.meta.url), "utf8"),
  zhTW: await readFile(new URL("../lib/i18n/messages/zh-TW.ts", import.meta.url), "utf8"),
};

test("SessionItem accepts an onArchived prop and renderSessionRow wires it to archiveSession", () => {
  assert.match(source, /onArchived\?: \(id: string\) => void;/);
  assert.match(source, /onArchived,\n/);
  // The shared row renderer (main list and pinned groups) passes it through.
  const renderSessionRowSource = source.slice(
    source.indexOf("const renderSessionRow = (family: SessionFamily) => {"),
    source.indexOf("return (\n    <div\n      ref={sessionPaneResizer.panelRef}"),
  );
  assert.ok(renderSessionRowSource.length > 0, "renderSessionRow must be found");
  assert.match(renderSessionRowSource, /onArchived=\{archiveSession\}/);
});

test("the archive action is a hover-action button carrying the exact e2e locator label", () => {
  // The button renders inside the hover action row (hovered && !transient)
  // and carries the localized title as its accessible name.
  const hoverRow = sessionItemSource.slice(
    sessionItemSource.indexOf("{/* Action buttons — shown on hover */}"),
    sessionItemSource.indexOf("{/* Collapse toggle — always visible when hasChildren */}"),
  );
  assert.ok(hoverRow.length > 0, "the hover action row must be found");
  assert.match(hoverRow, /title=\{t\("sidebar\.archive"\)\}/);
  assert.match(hoverRow, /aria-label=\{t\("sidebar\.archive"\)\}/);
  // Same 32×32 icon-button styling family as rename / open-in-new-tab /
  // delete (the e2e's right-click flow relies on the hover row rendering).
  assert.match(hoverRow, /width: 32, height: 32, padding: 0,/);
});

test("the archive action is hidden for running sessions; the running row's menu is otherwise unchanged", () => {
  const hoverRow = sessionItemSource.slice(
    sessionItemSource.indexOf("{/* Action buttons — shown on hover */}"),
    sessionItemSource.indexOf("{/* Collapse toggle — always visible when hasChildren */}"),
  );
  // Gated on the row's family-wide isRunning prop (a subagent running also
  // hides the action — the family row is one unit).
  assert.match(hoverRow, /\{!isRunning && \(/);
  // The three pre-existing actions stay unconditionally in the row.
  for (const label of ["sidebar.rename", "sidebar.openInNewTab", "sidebar.deleteWithShiftClick"]) {
    assert.match(hoverRow, new RegExp(`title=\\{t\\("${label}"\\)\\}`), `${label} must remain in the hover row`);
  }
});

test("the archive click handler stays local: stopPropagation + set mutation, no fetch/tabs/delete", () => {
  const archiveButton = sessionItemSource.slice(
    sessionItemSource.indexOf('{!isRunning && ('),
    sessionItemSource.indexOf("{/* An archive box"),
  );
  assert.match(archiveButton, /e\.stopPropagation\(\);/);
  assert.match(archiveButton, /onArchived\?\.\(session\.id\);/);
  assert.doesNotMatch(archiveButton, /fetch\(|window\.open|performDelete|onDeleted/);
});

test("i18n carries the archive label in all three dictionaries with the exact e2e strings", () => {
  const { en, zhCN, zhTW } = messageFiles;
  assert.match(en, /"sidebar\.archive": "Archive",/);
  assert.match(zhCN, /"sidebar\.archive": "归档",/);
  assert.match(zhTW, /"sidebar\.archive": "封存",/);
});
