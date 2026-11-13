import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

// Structural wiring tests (pi#80), in the source-regex style of
// FileViewer.test.mjs: the interactive behaviours need a browser, so the
// suite pins the contract seams instead — the keydown opener, the pane
// header affordance, the event bus, and the API integration points.

const chatWindow = await readFile(new URL("./ChatWindow.tsx", import.meta.url), "utf8");
const paneHeader = await readFile(new URL("./PaneHeader.tsx", import.meta.url), "utf8");
const splitPane = await readFile(new URL("./SplitPaneLayout.tsx", import.meta.url), "utf8");
const fileViewer = await readFile(new URL("./FileViewer.tsx", import.meta.url), "utf8");
const filesRoute = await readFile(new URL("../app/api/files/[...path]/route.ts", import.meta.url), "utf8");
const sessionSearchRoute = await readFile(new URL("../app/api/sessions/[id]/search/route.ts", import.meta.url), "utf8");

test("ChatWindow raises the search bar on Ctrl/Cmd+F for the active pane only", () => {
  assert.match(chatWindow, /useTabSearch\(\{/);
  assert.match(chatWindow, /event\.key\.toLowerCase\(\) === "f"/);
  assert.match(chatWindow, /if \(!isActivePane \|\| !sessionIdentity\) return;/);
  // Native find stays native inside modals and the bar itself.
  assert.match(chatWindow, /\[role='dialog'\], \.settings-general, \.tab-search-bar/);
  assert.match(chatWindow, /<TabSearchBar/);
});

test("PaneHeader exposes a keyboard-reachable search affordance", () => {
  assert.match(paneHeader, /onSearch\?: \(\) => void;/);
  assert.match(paneHeader, /role="button"[\s\S]*?onSearch\(\)/);
  assert.match(paneHeader, /tabIndex=\{0\}[\s\S]*?aria-label=\{searchLabel/);
});

test("SplitPaneLayout dispatches the open-tab-search event per pane", () => {
  assert.match(splitPane, /pi-web:open-tab-search/);
  assert.match(splitPane, /detail: tab\.sessionId/);
});

test("FileViewer integrates the shared bar and the rest-of-file search", () => {
  assert.match(fileViewer, /<TabSearchBar/);
  assert.match(fileViewer, /searchRestOfFile/);
  assert.match(fileViewer, /getFileApiUrl\(filePath, "search", sourceSessionId/);
});

test("files route carries the search type with the binary 415 refusal", () => {
  assert.match(filesRoute, /"search", "watch"/);
  assert.match(filesRoute, /type === "search"[\s\S]*?fileLooksBinary\(filePath\)/);
  assert.match(filesRoute, /binaryFile[\s\S]*?415/);
});

test("per-session search route is leaf-scoped with a size ceiling", () => {
  assert.match(sessionSearchRoute, /searchActiveBranch\(sm\.getEntries\(\) as never, query, leafId/);
  assert.match(sessionSearchRoute, /SM_CACHE_LIMITS\.maxFileBytes/);
  assert.match(sessionSearchRoute, /status: 413/);
});
