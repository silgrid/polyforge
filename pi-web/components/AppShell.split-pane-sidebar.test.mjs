import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

// This wi (split-view sidebar follow): focusing a split-view tab must move
// the sidebar highlight (and scroll it into view) without pane focus ever
// writing selectedSession (pi#33 isolation). The derivation is the focused
// pane's own now that the bg-tasks memo retired with the dock (#173); its
// inputs stay the pane-focus ones, pinned below.
const source = await readFile(new URL("./AppShell.tsx", import.meta.url), "utf8");

const memoStart = source.indexOf("const sidebarSessionId = useMemo(");
assert.ok(memoStart >= 0, "AppShell must derive the sidebar highlight session id");
const memoDepsAt = source.indexOf(
  "[splitPaneEnabled, isMobile, focusedPaneId, selectedSession?.id, paneTabs]",
  memoStart,
);
assert.ok(memoDepsAt > memoStart, "the sidebar memo must pin its deps");
const sidebarMemo = source.slice(memoStart, source.indexOf(");", memoDepsAt));

const focusStart = source.indexOf("onFocusPane={(sid) => {");
assert.ok(focusStart >= 0, "AppShell must define the split-pane focus handler");
const focusEnd = source.indexOf("onClosePane={(sid) => {", focusStart);
assert.ok(focusEnd > focusStart, "the focus handler must be followed by the close handler");
const focusHandler = source.slice(focusStart, focusEnd);

test("the sidebar highlight derives from the focused pane through resolveSidebarSessionId", () => {
  assert.match(sidebarMemo, /resolveSidebarSessionId\(\{/);
  // The same desktop-split guard the background-tasks memo uses — classic
  // layout (split off) and mobile are excluded by construction.
  assert.match(sidebarMemo, /splitPaneEnabled: splitPaneEnabled && !isMobile/);
  assert.match(sidebarMemo, /selectedSessionId: selectedSession\?\.id \?\? null/);
  assert.match(sidebarMemo, /lastSessionPaneId: lastSessionPaneIdRef\.current/);
  assert.match(sidebarMemo, /paneTabs,/);
  // The derivation itself never writes the classic selection.
  assert.doesNotMatch(sidebarMemo, /setSelectedSession/);
});

test("pane focus keeps the pi#33 isolation: it writes only focusedPaneId", () => {
  assert.match(focusHandler, /setFocusedPaneId\(sid\);/);
  assert.doesNotMatch(focusHandler, /setSelectedSession/,
    "focusing a pane must never touch selectedSession (pi#33)");
  // The sidebar highlight is a derived read, never a selection write: no
  // call site feeds selectedSession back into the sidebar highlight props.
  assert.doesNotMatch(source, /highlightSessionId=\{selectedSession/);
});

test("the SessionSidebar call site carries the derived highlight and the split-only follow gate", () => {
  assert.match(source, /highlightSessionId=\{sidebarSessionId\}/);
  assert.match(source, /followHighlightIntoView=\{splitPaneEnabled && !isMobile\}/);
  // The classic selection prop keeps feeding its classic consumers.
  assert.match(source, /selectedSessionId=\{selectedSession\?\.id \?\? null\}/);
});

test("the sidebar memo reads the one shared focus ref, declared once", () => {
  // The bg-tasks memo is gone with the dock; the sidebar derivation is the
  // only reader left of the last-focused-session ref.
  assert.equal(source.match(/const lastSessionPaneIdRef = useRef/g)?.length, 1);
  assert.match(sidebarMemo, /lastSessionPaneId: lastSessionPaneIdRef\.current/);
});
