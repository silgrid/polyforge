import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

// Structural wiring tests (pi#81): pin the edit-mode seams in the
// source-regex style of FileViewer.test.mjs — the interactive overlay needs
// a browser, so the suite pins the contract points instead.

const fileViewer = await readFile(new URL("./FileViewer.tsx", import.meta.url), "utf8");
const overlay = await readFile(new URL("./FileEditorOverlay.tsx", import.meta.url), "utf8");
const filesRoute = await readFile(new URL("../app/api/files/[...path]/route.ts", import.meta.url), "utf8");

test("the viewer gates editing on the approved caps and lost-update anchor", () => {
  assert.match(fileViewer, /EDIT_MAX_BYTES/);
  assert.match(fileViewer, /binaryInfo/);
  assert.match(fileViewer, /expectedMtimeMs: data\.mtimeMs \?\? 0/);
  // Truncated-but-small files load the remaining chunks before editing.
  assert.match(fileViewer, /while \(current\.truncated && current\.size <= EDIT_MAX_BYTES\)/);
  // Dirty drafts park per-path on file switch (no data loss).
  assert.match(fileViewer, /draftCacheRef\.current\.get\(filePath\)/);
  assert.match(fileViewer, /draftCacheRef\.current\.set\(lastFilePathRef\.current, draft\)/);
});

test("a dirty draft asks Save / Discard / Stay before leaving edit mode", () => {
  assert.match(fileViewer, /setConfirmExit\(\{ redirectTo: mode \}\)/);
  assert.match(fileViewer, /t\("fileEdit\.save"\)/);
  assert.match(fileViewer, /t\("fileEdit\.discard"\)/);
  assert.match(fileViewer, /t\("fileEdit\.stay"\)/);
});

test("409 conflicts never blind-overwrite: reload-or-stay only", () => {
  assert.match(fileViewer, /response\.status === 409/);
  assert.match(fileViewer, /currentMtimeMs/);
  assert.match(fileViewer, /t\("fileEdit\.reload"\)/);
  // The conflict path refetches instead of saving again with a fresh stamp.
  assert.match(fileViewer, /fileEdit\.conflict[\s\S]*?fetchContent\(filePath\)/);
  assert.doesNotMatch(fileViewer, /saveAnyway/i);
});

test("the editor overlay is a transparent textarea over a highlighted layer", () => {
  assert.match(overlay, /color: "transparent"/);
  assert.match(overlay, /caretColor/);
  assert.match(overlay, /HIGHLIGHT_DEBOUNCE_MS/);
  // Soft tabs instead of focus loss.
  assert.match(overlay, /event\.key === "Tab"/);
});

test("the files route enforces the save guards server-side", () => {
  assert.match(filesRoute, /type === "save"/);
  assert.match(filesRoute, /saveTextFileSafely/);
  assert.match(filesRoute, /isExistingFilePathAllowed\(filePath, allowedRoots\)/);
  assert.match(filesRoute, /stat\.size > EDIT_MAX_BYTES/);
  assert.match(filesRoute, /status: 409/);
  assert.match(filesRoute, /status: 413/);
  assert.match(filesRoute, /mtimeMs: stat\.mtimeMs/);
});
