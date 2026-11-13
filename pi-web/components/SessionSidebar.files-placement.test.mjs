import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// Parse smoke test, same convention as SessionSidebar.test.mjs.
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
await jiti.import("./SessionSidebar.tsx");

const source = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");

// The preference store is pure logic; import it directly so persistence is
// tested as behavior, not just source shape.
const {
  getFilesPlacement,
  setFilesPlacement,
  FILES_PLACEMENT_STORAGE_KEY,
} = await jiti.import("../lib/files-placement.ts");

test("the sidebar derives the layout from the preference AND the mobile breakpoint (mobile always tabs)", () => {
  assert.match(source, /import \{ useFilesPlacement \} from "@\/lib\/files-placement";/);
  assert.match(source, /import \{ useIsMobile \} from "@\/hooks\/useIsMobile";/);
  // Upstream's rule, verbatim semantics: stacked = desktop AND "stacked"
  // preference — so mobile forces tabs regardless of the stored value.
  assert.match(source, /const stackedSidebarLayout = !isMobile && filesPlacement === "stacked";/);
  // Verification rewind (mobile e2e regression, plan step-4): the tab
  // choice is user-explicit state with a viewport-derived default — when
  // the mobile breakpoint forces tabs, the default tab is "files" so the
  // multi-root explorer stays reachable without a tab click (the repo's
  // mobile e2e contract, [data-explorer-section] right after opening the
  // sidebar); desktop tabs mode (the user's placement choice) defaults to
  // "sessions". An explicit tab click always wins.
  assert.match(source, /const \[sidebarTabChoice, setSidebarTab\] = useState<"sessions" \| "files" \| null>\(null\);/);
  assert.match(source, /const sidebarTab = sidebarTabChoice \?\? \(isMobile \? "files" : "sessions"\);/);
  assert.match(source, /const sessionsPaneHidden = !stackedSidebarLayout && sidebarTab !== "sessions";/);
  assert.match(source, /const filesPaneHidden = !stackedSidebarLayout && sidebarTab !== "files";/);
});

test("tabs mode renders a two-tab tablist between the header and the sessions pane", () => {
  const tabsBlock = source.slice(
    source.indexOf('{!stackedSidebarLayout && ('),
    source.indexOf('      {/* Session list —'),
  );
  assert.match(tabsBlock, /role="tablist"/);
  assert.match(tabsBlock, /aria-label=\{t\("sidebar\.tabsLabel"\)\}/);
  assert.equal((tabsBlock.match(/role="tab"/g) ?? []).length, 2);
  assert.match(tabsBlock, /id="session-sidebar-tab-sessions"/);
  assert.match(tabsBlock, /id="session-sidebar-tab-files"/);
  assert.match(tabsBlock, /aria-selected=\{sidebarTab === "sessions"\}/);
  assert.match(tabsBlock, /aria-selected=\{sidebarTab === "files"\}/);
  assert.match(tabsBlock, /onClick=\{\(\) => setSidebarTab\("sessions"\)\}/);
  assert.match(tabsBlock, /onClick=\{\(\) => setSidebarTab\("files"\)\}/);
  assert.match(tabsBlock, /aria-controls="session-sidebar-panel-sessions"/);
  assert.match(tabsBlock, /aria-controls="session-sidebar-panel-files"/);
});

test("the panes toggle display only — the explorer never unmounts on a layout or tab switch", () => {
  // Sessions pane: ALWAYS rendered, hidden via display:none in tabs mode.
  const sessionsPaneBlock = source.slice(
    source.indexOf('<div\n        ref={sessionPaneRef}'),
    source.indexOf("<SessionSearch"),
  );
  assert.match(sessionsPaneBlock, /display: sessionsPaneHidden \? "none" : "flex"/);
  assert.match(sessionsPaneBlock, /role=\{stackedSidebarLayout \? undefined : "tabpanel"\}/);
  assert.match(sessionsPaneBlock, /aria-labelledby=\{stackedSidebarLayout \? undefined : "session-sidebar-tab-sessions"\}/);
  // Stacked flex behavior is unchanged: the var-driven flex stays on the
  // stacked branch of the conditional.
  assert.match(
    sessionsPaneBlock,
    /flex: stackedSidebarLayout\s*\?\s*explorerOpen\s*\?\s*"0 1 var\(--sidebar-session-pane-height, 320px\)"\s*: "1 1 auto"\s*: "1 1 auto",/,
  );
  // Explorer section: ALWAYS rendered, hidden via display:none in tabs mode.
  const explorerBlock = source.slice(
    source.indexOf('<div\n        ref={explorerSectionRef}'),
    source.indexOf('<div style={{ display: "flex", alignItems: "center", flexShrink: 0 }}'),
  );
  assert.match(explorerBlock, /display: filesPaneHidden \? "none" : "flex"/);
  assert.match(explorerBlock, /role=\{stackedSidebarLayout \? undefined : "tabpanel"\}/);
  // The explorer instance itself is not conditionally keyed or wrapped —
  // it stays inside the section's explorerOpen slot as before (the body
  // gating and the instance are unchanged from the stacked-only layout).
  const explorerBodyIndex = source.indexOf("{explorerOpen && (\n            <div ref={explorerScrollRef}");
  assert.ok(explorerBodyIndex > 0, "the explorer body keeps its existing explorerOpen gating");
  const explorerInstance = source.slice(
    source.indexOf("<MultiRootFileExplorer\n"),
    source.indexOf("<MultiRootFileExplorer\n") + 1000,
  );
  assert.doesNotMatch(explorerInstance, /key=\{/);
  // Neither pane sits behind a layout-mode conditional unmount: the ONLY
  // layout-mode conditionals in the sidebar body are the tab bar and the
  // resize handle — the pane divs themselves always render.
  assert.equal((source.match(/\{!stackedSidebarLayout && \(/g) ?? []).length, 1,
    "exactly one !stacked conditional: the tab bar");
  assert.equal((source.match(/\{stackedSidebarLayout && explorerOpen && \(/g) ?? []).length, 1,
    "exactly one stacked conditional: the resize handle");
  assert.doesNotMatch(source, /\{stackedSidebarLayout && \(\s*<div\s+ref=\{(?:sessionPaneRef|explorerSectionRef)\}/,
    "no pane is wrapped in a stacked-only conditional");
  assert.doesNotMatch(source, /\{!stackedSidebarLayout && \(\s*<div\s+ref=\{(?:sessionPaneRef|explorerSectionRef)\}/,
    "no pane is wrapped in a tabs-only conditional");
});

test("the section resize handle renders in stacked mode only", () => {
  const handleIndex = source.indexOf("sidebar-section-resize-handle");
  const handleBlock = source.slice(Math.max(0, handleIndex - 500), handleIndex);
  assert.match(handleBlock, /\{stackedSidebarLayout && explorerOpen && \(/);
  // ...and the resizer wiring itself is untouched (same useResizablePanel
  // storage key and CSS variable — stacked keeps today's behavior).
  assert.match(source, /storageKey: "pi-web:sidebar-session-pane-height"/);
  assert.match(source, /cssVariable: "--sidebar-session-pane-height"/);
});

test("the layout toggle persists through the store and re-reads without a reload (mocked storage)", () => {
  const store = new Map();
  globalThis.window = {
    localStorage: {
      getItem: (key) => (store.has(key) ? store.get(key) : null),
      setItem: (key, value) => store.set(key, String(value)),
      removeItem: (key) => store.delete(key),
    },
  };
  try {
    // Settings panel writes; the sidebar reads — same key, no prop drilling.
    assert.equal(getFilesPlacement(), "stacked");
    setFilesPlacement("tabs");
    assert.equal(store.get(FILES_PLACEMENT_STORAGE_KEY), "tabs");
    assert.equal(getFilesPlacement(), "tabs");
    setFilesPlacement("stacked");
    assert.equal(store.get(FILES_PLACEMENT_STORAGE_KEY), "stacked");
    assert.equal(getFilesPlacement(), "stacked");
  } finally {
    delete globalThis.window;
  }
});

test("the archive view stays reachable in both layouts (A1 regression guard)", () => {
  // The archive view renders INSIDE the sessions pane (SessionSearch
  // region), which is always mounted in both layouts — only its display
  // toggles. The entry button and view must not be layout-gated.
  const archiveViewIndex = source.indexOf("archiveViewOpen ? (");
  const sessionsPaneIndex = source.indexOf("ref={sessionPaneRef}");
  const sessionPaneEndIndex = source.indexOf("{explorerOpen && changesCount > 0 && (");
  assert.ok(archiveViewIndex > sessionsPaneIndex && archiveViewIndex < sessionPaneEndIndex,
    "the archive view lives inside the sessions pane");
  const archiveEntryIndex = source.indexOf("已归档 N」 entry");
  const archiveOpenCallIndex = source.indexOf("setArchiveViewOpen(true)", archiveEntryIndex);
  const archiveBlock = source.slice(archiveEntryIndex, archiveOpenCallIndex + "setArchiveViewOpen(true)".length);
  assert.match(archiveBlock, /setArchiveViewOpen\(true\)/);
  assert.doesNotMatch(archiveBlock, /stackedSidebarLayout/);
});
