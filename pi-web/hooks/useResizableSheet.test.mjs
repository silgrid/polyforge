import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./useResizableSheet.ts", import.meta.url), "utf8");
const cssSource = await readFile(new URL("../app/globals.css", import.meta.url), "utf8");

test("pi#120: the drag derives the percentage from the sheet's fixed bottom edge, clamped, never dismissing", () => {
  // The sheet is bottom-anchored, so its bottom edge stays fixed during the
  // drag: height under the pointer = sheetBottom - clientY, as a percentage of
  // the same container the sheet's percentage height resolves against.
  assert.match(source, /clampSheetHeightPct\(\(\(drag\.sheetBottom - event\.clientY\) \/ drag\.containerHeight\) \* 100\)/, "the move math derives from the fixed bottom edge");
  assert.match(source, /const container = sheet\.offsetParent \?\? sheet\.parentElement;/, "the percentage basis is the sheet's containing block");
  // Up grows, down shrinks; past either bound just clamps — the hook has no
  // dismissal concept at all (the backdrop tap stays the only dismiss
  // gesture, and it lives in AppShell, not here).
  assert.doesNotMatch(source, /setPfPanelOpen|setBgPanelOpen|onClose/, "the resizer has no dismissal call of its own");
  // A drag needs a measurable container before it starts.
  assert.match(source, /if \(containerHeight <= 0\) return;/, "no drag without a measurable container");
});

test("pi#120: pointer capture covers touch and mouse; the page cannot scroll while dragged", () => {
  assert.match(source, /target\.setPointerCapture\(event\.pointerId\)/, "pointer capture keeps the drag on the handle");
  assert.match(source, /event\.pointerType === "mouse" && event\.button !== 0/, "non-primary mouse buttons never start a drag");
  assert.match(source, /event\.pointerType === "mouse" && event\.buttons === 0/, "a mouse drag ends when the button is released off-target");
  // touch-action: none keeps the page from scrolling or pull-to-refresh
  // while the handle is dragged (pi#31: the drag must work under touch).
  const block = cssSource.slice(cssSource.indexOf(".pf-sheet-resize-handle {"));
  assert.match(block, /touch-action: none/, "the handle blocks page scroll/pull-to-refresh during the drag");
  assert.match(block, /cursor: row-resize/, "the handle shows the vertical resize cursor");
});

test("pi#120: the stored height restores through the range-checked parse; invalid reads as the 62% default", () => {
  assert.match(source, /parseStoredSheetHeightPct\(window\.localStorage\.getItem\(storageKey\)\)/, "the stored height restores through the range-checked parse");
  assert.match(source, /commitHeightPct\(stored \?\? SHEET_DEFAULT_HEIGHT_PCT, false\)/, "absent/invalid/out-of-range reads as the 62% default");
  // The restore never persists: an invalid stored value is read around, not
  // rewritten (the next finished drag replaces the value wholesale).
  const restoreStart = source.indexOf("restoredRef.current = true;");
  const restoreEnd = source.indexOf("}, [commitHeightPct, storageKey]);", restoreStart);
  const restore = source.slice(restoreStart, restoreEnd);
  assert.doesNotMatch(restore, /writeStoredSheetHeightPct/, "the restore must not rewrite storage");
  // Every finished drag persists its clamped height.
  assert.match(source, /commitHeightPct\(heightPctRef\.current, true\)/, "a finished drag persists the live height");
  assert.match(source, /writeStoredSheetHeightPct\(storageKey, nextPct\)/, "persistence goes through the storage helper");
});

test("pi#120: the handle keeps the shared separator contract (keyboard + reset, horizontal orientation)", () => {
  assert.match(source, /onDoubleClick: resetHeight/, "double-click resets to the 62% default");
  assert.match(source, /event\.key === "Enter"/, "Enter resets too");
  assert.match(source, /event\.key === "ArrowUp"/, "ArrowUp grows the sheet");
  assert.match(source, /event\.key === "ArrowDown"/, "ArrowDown shrinks it");
  assert.match(source, /event\.key === "Home"/, "Home clamps to the minimum");
  assert.match(source, /event\.key === "End"/, "End clamps to the maximum");
  assert.match(source, /"aria-orientation": "horizontal" as const/, "a horizontal separator bar resizing a vertical height");
  assert.match(source, /"aria-valuetext": `\$\{heightPct\}%`/, "the value reads as a percentage");
  // A drag abandoned by the window finishes cleanly (body styles restored).
  assert.match(source, /window\.addEventListener\("blur", cancelResize\)/, "window blur finishes the drag");
});