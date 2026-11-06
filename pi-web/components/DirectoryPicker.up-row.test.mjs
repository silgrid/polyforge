import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

const componentSource = await import("node:fs/promises").then(({ readFile }) =>
  readFile(new URL("./DirectoryPicker.tsx", import.meta.url), "utf8"));
const cssSource = await import("node:fs/promises").then(({ readFile }) =>
  readFile(new URL("../app/globals.css", import.meta.url), "utf8"));

test("row manage buttons are always visible (no hover-gated display)", () => {
  const block = cssSource.match(/\.directory-picker-row-manage \{[^}]*\}/);
  assert.ok(block, ".directory-picker-row-manage rule must exist");
  assert.match(block[0], /display:\s*inline-flex/);
  assert.doesNotMatch(block[0], /display:\s*none/);
  // The hover/focus-within reveal override is gone: no rule flips the manage
  // buttons' display back on for the row state.
  assert.doesNotMatch(
    cssSource,
    /\.directory-picker-row:hover \.directory-picker-row-manage[^}]*display/,
    "hover-reveal override must be removed",
  );
});

test("the browse list renders a parent-navigation row when a parent exists", () => {
  // The up row sits before the drives/directories branches and navigates to
  // the tracked parentDirectory through the same navigateTo channel.
  const upRow = componentSource.match(/\{canNavigateUp && \(\s*<div className="directory-picker-row"[\s\S]*?navigateTo\(parentDirectory \?\? undefined\)[\s\S]*?directoryPicker\.goToParent[\s\S]*?<\/div>\s*\)\}/);
  assert.ok(upRow, "canNavigateUp-gated up-navigation row must precede the listings");
  const upRowIndex = componentSource.indexOf(upRow[0]);
  const drivesBranch = componentSource.indexOf("drives.length > 0");
  const dirsBranch = componentSource.indexOf("directories.length > 0");
  assert.ok(upRowIndex < drivesBranch && upRowIndex < dirsBranch, "up row renders above both listings");
});
