import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

// pi#72 review fix F1: activeFilePath must be threaded from AppShell's REAL
// active file-tab identity down through SessionSidebar -> MultiRootFileExplorer
// -> FileExplorer, not reconstructed as a local click echo inside FileExplorer.
// This covers every path that changes the active tab: TabBar click (onSelectTab
// sets activeFileTabId directly), closing the active tab (falls back to another
// tab id, whose filePath is looked up the same way), and any other caller of
// handleOpenFile (session restore, @-mention preview, linked-file opens).

const appShellSource = await readFile(new URL("./AppShell.tsx", import.meta.url), "utf8");
const sidebarSource = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");
const multiRootSource = await readFile(new URL("./MultiRootFileExplorer.tsx", import.meta.url), "utf8");
const explorerSource = await readFile(new URL("./FileExplorer.tsx", import.meta.url), "utf8");

test("AppShell derives the active file path from the real active file tab, not a new tracker", () => {
  assert.match(appShellSource, /const activeFileTab = fileTabs\.find\(\(tab\) => tab\.id === activeFileTabId\) \?\? null;/);
  assert.match(appShellSource, /activeFilePath=\{activeFileTab\?\.filePath \?\? null\}/);
});

test("activeFileTabId is the single source of truth for every switching path (TabBar, close fallback, open)", () => {
  // TabBar click switches the active tab directly.
  assert.match(appShellSource, /onSelectTab=\{setActiveFileTabId\}/);
  // Opening a file (session restore, @-mention preview, linked file, explorer
  // row click forwarded through onOpenFile) always sets activeFileTabId too.
  assert.match(appShellSource, /const handleOpenFile = useCallback\(\([\s\S]*?setActiveFileTabId\(tabId\);/);
  // Closing the active tab falls back to another tab, still through the same
  // activeFileTabId state - so activeFileTab (and activeFilePath) stays correct.
  assert.match(appShellSource, /const handleCloseFileTab = useCallback\(\(tabId: string\) => \{[\s\S]*?setActiveFileTabId\(\(cur\) => \{[\s\S]*?return remaining\.at\(-1\)\?\.id/);
});

test("SessionSidebar declares and forwards activeFilePath unchanged to MultiRootFileExplorer", () => {
  assert.match(sidebarSource, /activeFilePath\?: string \| null;/);
  assert.match(
    sidebarSource,
    /export function SessionSidebar\(\{[\s\S]*?\bactiveFilePath\b[\s\S]*?\}: Props\) \{/,
  );
  assert.match(sidebarSource, /<MultiRootFileExplorer[\s\S]*?activeFilePath=\{activeFilePath \?\? null\}/);
});

test("MultiRootFileExplorer forwards the same value into each mounted FileExplorer (owning root highlights, others don't)", () => {
  assert.match(multiRootSource, /activeFilePath\?: string \| null;/);
  assert.match(multiRootSource, /<FileExplorer[\s\S]{0,600}activeFilePath=\{activeFilePath\}/);
});

test("FileExplorer no longer needs a click callback to learn the active file", () => {
  assert.doesNotMatch(explorerSource, /onActivateFile/);
  // Props may gain later additive members (e.g. the showIgnoredFiles opt-in,
  // wi pi#101); activeFilePath just has to be a declared prop.
  assert.match(explorerSource, /activeFilePath\?: string \| null;/);
});
