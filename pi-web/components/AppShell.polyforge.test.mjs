import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./AppShell.tsx", import.meta.url), "utf8");
const panelSource = await readFile(new URL("./PolyforgePanel.tsx", import.meta.url), "utf8");
const hookSource = await readFile(new URL("../hooks/usePolyforge.ts", import.meta.url), "utf8");

test("the Polyforge button is gated on pf presence alone, independent of chat visibility", () => {
  // the button lives in its own renderer now, shared by the
  // resident narrow-phone slot and the wide/desktop inline row.
  assert.match(source, /const renderPolyforgeButton = \(mobile: boolean\) => \{\s*if \(!pfPresent\) return null;/,
    "the button renders iff pf is found");
  // owner-intended change the button's name is polyforge.open,
  // followed by the attention count's title while anything needs a human.
  assert.match(source, /aria-label=\{label\}/);
  assert.match(source, /: translate\("polyforge\.open"\);/);
  assert.match(source, /\{includePolyforge && renderPolyforgeButton\(mobile\)\}/);
  // The chat actions (history, …) keep their original gate.
  assert.match(source, /const showChatActions = mobile \|\| showChat;/);
  assert.match(source, /if \(!showChatActions && !\(includePolyforge && pfPresent\)\) return null;/);
  assert.match(source, /\{showChatActions && \(<>/);
});

test("the badge shows the queue running count and nothing while the daemon is down", () => {
  assert.match(source, /pfRunningCount !== null && pfRunningCount > 0/);
  assert.match(source, /\{pfRunningCount > 9 \? "9\+" : pfRunningCount\}/);
  assert.match(hookSource, /const runningCount = opsStatus \? opsStatus\.queue\.running : null;/);
});

test("the dock slot is mutually exclusive in both directions", () => {
  // Opening Polyforge closes bg-tasks…
  assert.match(source, /setPfPanelOpen\(\(open\) => \{\s*const next = !open;\s*if \(next\) setBgPanelOpen\(false\);\s*return next;\s*\}\);/);
  // …and opening bg-tasks closes Polyforge. owner-intended change:
  // the pi top bar's bg-tasks toggle is gone, so the terminal notification's
  // jump (pinned by the next test) is the only bg-tasks-opening path; no
  // toolbar toggle-shaped opener may remain.
  assert.doesNotMatch(source, /setBgPanelOpen\(\(open\) => \{/,
    "a toolbar toggle-shaped bg-tasks opener remains although the top-bar toggle is gone");
  assert.doesNotMatch(source, /setBgPanelOpen\(\(open\) => !open\)/);
  assert.doesNotMatch(source, /translate\("bgTasks\.open"\)/,
    "the top bar still renders a bg-tasks toggle");
});

test("every path that opens bg-tasks closes the Polyforge panel (notification jump, B-2)", () => {
  // The bg-task terminal notification's onClick is the only bg-tasks-opening
  // path now that removed the top-bar toggle; it must not leave
  // the pf dock covering the panel the jump was meant to show (AC9 / AC12).
  const jumps = [];
  let index = source.indexOf("setBgPanelOpen(true)");
  while (index !== -1) {
    jumps.push(index);
    index = source.indexOf("setBgPanelOpen(true)", index + 1);
  }
  assert.ok(jumps.length > 0, "expected at least one direct setBgPanelOpen(true) (the notification jump)");
  for (const jump of jumps) {
    const context = source.slice(jump, jump + 90);
    assert.match(
      context,
      /setBgPanelOpen\(true\);\s*setPfPanelOpen\(false\);/,
      "a setBgPanelOpen(true) path does not close the Polyforge panel",
    );
  }
});

test("the panel mounts in the shared dock slot: resizable desktop right dock, drag-resizable mobile sheet; bg-tasks keeps 340", () => {
  const pfDockStart = source.indexOf("{pfPanelOpen && (");
  const bgDockStart = source.indexOf("{bgPanelOpen && (");
  assert.ok(pfDockStart > bgDockStart, "the pf panel mounts in the same container as bg-tasks");
  const pfBlockEnd = source.indexOf("\n        </div>", pfDockStart);
  assert.ok(pfBlockEnd > pfDockStart, "pf dock block end");
  const pfBlock = source.slice(pfDockStart, pfBlockEnd);
  assert.match(pfBlock, /role="dialog"/);
  assert.match(pfBlock, /height: `\$\{pfSheetResizer\.heightPct\}%`/, "the mobile sheet height is the sheet resizer state (pi#120: default 62%, drag-resizable, persisted)");
  assert.match(pfBlock, /aria-label=\{translate\("polyforge\.title"\)\}/);
  assert.match(pfBlock, /role="complementary"/);
  assert.match(pfBlock, /<PolyforgePanel/);
  assert.match(pfBlock, /onClose=\{\(\) => setPfPanelOpen\(false\)\}/);
  assert.doesNotMatch(pfBlock, /width: 340/, "the pf dock is no longer fixed at 340 (pi#117: 460 default, resizable)");
  // The bg-tasks dock in the same slot stays fixed at 340, not coupled to the
  // Polyforge width.
  const bgBlock = source.slice(bgDockStart, pfDockStart);
  assert.match(bgBlock, /width: 340/, "the bg-tasks dock keeps its fixed 340px width");
  assert.doesNotMatch(bgBlock, /pf-dock|pfDockResizer/, "the bg dock is not coupled to the Polyforge width");
  // The resize handle renders in the desktop branch only (after the mobile
  // sheet in the source), so the mobile sheet never grows a resize handle.
  const handleIndex = source.indexOf('data-resize-handle="polyforge-dock"');
  const mobileSheetIndex = source.indexOf("height: `${pfSheetResizer.heightPct}%`", pfDockStart);
  assert.ok(handleIndex > mobileSheetIndex, "the dock handle renders in the desktop branch only");
});

test("pi#117: the desktop dock opens at 460 and resizes via a left-edge handle clamped to [340, 90vw]", () => {
  // Reuse, no new mechanism: the dock resizes through the shared
  // useResizablePanel hook (sidebar / right panel pattern).
  const resizerStart = source.indexOf("const pfDockResizer = useResizablePanel({");
  assert.ok(resizerStart >= 0, "the dock has a resizer");
  const resizer = source.slice(resizerStart, source.indexOf("});", resizerStart));
  for (const wiring of [
    /ariaLabel: translate\("polyforge\.resizeDock"\)/,
    /cssVariable: "--pf-dock-width"/,
    /defaultWidth: POLYFORGE_DOCK_DEFAULT_WIDTH/,
    /getMaxWidth: getResponsivePfDockMaxWidth/,
    /growthDirection: "left"/,
    /maxWidth: POLYFORGE_DOCK_MAX_WIDTH/,
    /minWidth: POLYFORGE_DOCK_MIN_WIDTH/,
    /storageKey: "pi-web:polyforge-dock-width"/,
    /widthRef: pfDockWidthRef/,
  ]) {
    assert.match(resizer, wiring, `missing resizer wiring: ${wiring}`);
  }
  // The live max is 90vw (absent/invalid storage reads as the 460 default via
  // the hook's restore).
  assert.match(
    source,
    /getResponsivePfDockMaxWidth = useCallback\(\s*\(\) => typeof window === "undefined"\s*\? POLYFORGE_DOCK_MAX_WIDTH\s*: getPolyforgeDockMaxWidth\(window\.innerWidth\),\s*\[\],\s*\);/,
    "the max derives from the viewport (90vw)",
  );
  // The dock div renders the resizer state as its css variable (the hook
  // writes the variable directly during the drag, state on commit).
  assert.match(source, /width: "var\(--pf-dock-width\)"/, "the dock width reads the css variable");
  assert.match(source, /"--pf-dock-width": `\$\{pfDockResizer\.width\}px`/, "the variable is the resizer state");
  assert.match(source, /ref=\{pfDockResizer\.panelRef\}/, "the resizer's panelRef is the dock");
});

test("pi#117: dragging the resize handle never dismisses the dock (the handle counts as part of the dock)", () => {
  const dockAttr = source.indexOf("data-pf-dock");
  const handleAttr = source.indexOf('data-resize-handle="polyforge-dock"');
  const panelStart = source.indexOf("<PolyforgePanel", dockAttr);
  const dockEnd = source.indexOf("\n              </div>", panelStart);
  assert.ok(dockAttr >= 0 && handleAttr > dockAttr, "the handle exists after the dock attribute");
  assert.ok(handleAttr < dockEnd, "the handle is a DOM child of the dock element");
  // The pi#113 dismissal exempts whatever is inside [data-pf-dock] via the
  // same closest() check the existing test pins — a handle inside the dock
  // can never trigger the outside-pointerdown dismissal.
  assert.match(source, /target\.closest\("\[data-pf-dock\], \[data-pf-toggle\]"\)/, "the dismissal exempts the dock subtree");
  assert.match(source, /\{\.\.\.pfDockResizer\.separatorProps\}/, "the handle carries the separator props");
  assert.match(source, /className=\{`pf-dock-resize-handle\$\{pfDockResizer\.isResizing \? " is-resizing" : ""\}`\}/);
});

test("the shell wires the hook's full state into the panel", () => {
  assert.match(source, /usePolyforge\(pfPanelOpen\)/);
  assert.match(source, /cards: pfCards,\s*cardsError: pfCardsError,/, "the card stream comes from the hook");
  assert.equal(source.split("cards={pfCards}").length - 1, 2, "both the dock and the sheet render the card stream");
  for (const prop of ["opsStatus={pfOpsStatus}", "opsError={pfOpsError}", "cards={pfCards}", "cardsError={pfCardsError}", "watchStatus={pfWatchStatus}",
    "selectedWi={pfSelectedWi}", "onSelect={selectPolyforgeJob}", "onRefresh={refreshPolyforgeStatus}"]) {
    assert.ok(source.includes(prop), `missing panel wiring: ${prop}`);
  }
});

test("no RegExp lookbehind anywhere in the new client sources", () => {
  for (const [name, src] of [["AppShell", source], ["PolyforgePanel", panelSource], ["usePolyforge", hookSource]]) {
    assert.doesNotMatch(src, /\(\?<[=!]/, `${name} uses a RegExp lookbehind (forbidden in client code)`);
  }
});

test("pi#113: a pointer press outside the desktop dock dismisses it, without eating the click", () => {
  // The listener lives only while the desktop dock is open (mobile keeps its
  // backdrop; a closed dock leaves nothing attached).
  // a pinned dock installs no listener at all.
  assert.match(source, /if \(!pfPanelOpen \|\| isMobile \|\| pfDockPinned\) return;/, "gated on desktop-dock-open and unpinned");
  assert.match(source, /document\.addEventListener\("pointerdown", dismissOnOutsidePress\);/);
  assert.match(source, /return \(\) => document\.removeEventListener\("pointerdown", dismissOnOutsidePress\);/, "cleanup detaches the listener");
  // The dismissal path must not swallow the press: the same click still fires
  // on its target (mem_Y6pX8exg — eating the events loses the click).
  const start = source.indexOf("const dismissOnOutsidePress");
  assert.ok(start >= 0, "dismissOnOutsidePress handler exists");
  const body = source.slice(start, source.indexOf("};", start));
  assert.match(body, /setPfPanelOpen\(false\);/, "an outside press closes the dock");
  assert.doesNotMatch(body, /preventDefault|stopPropagation/, "the dismissing press must not be eaten");
  // Presses inside the dock (rows, refresh, ×, event-mode controls) and on
  // the topbar toggle (whose own click already ends closed — a pointerdown
  // dismissal there would close-then-reopen) never dismiss.
  assert.match(body, /target\.closest\("\[data-pf-dock\], \[data-pf-toggle\]"\)/, "dock and toggle are exempt");
  const attrUses = (name) => (source.match(new RegExp(`${name}`, "g")) ?? []).length
    - (source.match(new RegExp(`\\[${name}\\]`, "g")) ?? []).length;
  assert.equal(attrUses("data-pf-dock"), 1, "exactly the desktop dock is marked (mobile sheet and bg dock are not)");
  assert.ok(attrUses("data-pf-toggle") >= 1, "the topbar Polyforge toggle is marked");
  // The toggle keeps its functional-update semantics: press-then-click on it
  // ends closed, never a close-then-reopen.
  assert.match(source, /setPfPanelOpen\(\(open\) => \{\s*const next = !open;\s*if \(next\) setBgPanelOpen\(false\);\s*return next;\s*\}\);/);
});

test("pi#120: both mobile sheets drag-resize through one shared mechanism; each height persists independently", () => {
  // One shared hook, two consumers, two independent storage keys — the
  // pi#117 uncoupled dock widths mirrored for the sheet heights.
  const resizers = [...source.matchAll(
    /const (pfSheetResizer|bgSheetResizer) = useResizableSheet\(\{\s*ariaLabel: translate\("(polyforge|bgTasks)\.resizeSheet"\),\s*storageKey: "(pi-web:[a-z-]+-sheet-height)",\s*\}\);/g,
  )].map((m) => [m[1], m[2], m[3]]);
  assert.deepEqual(resizers, [
    ["pfSheetResizer", "polyforge", "pi-web:polyforge-sheet-height"],
    ["bgSheetResizer", "bgTasks", "pi-web:bg-tasks-sheet-height"],
  ], "two sheet resizers with distinct keys");
  // Both mobile sheets render the top-edge handle as a DOM child (inside the
  // sheet, above the backdrop), so dragging resizes and never dismisses; the
  // handle carries no dismissal of its own — the backdrop tap stays the
  // only dismiss gesture.
  for (const [resizer, handle, panel] of [["bgSheetResizer", "bg-tasks-sheet", "<BackgroundTasksPanel"], ["pfSheetResizer", "polyforge-sheet", "<PolyforgePanel"]]) {
    const sheetIndex = source.indexOf(`ref={${resizer}.sheetRef}`);
    assert.ok(sheetIndex >= 0, `${resizer} renders a mobile sheet`);
    const handleIndex = source.indexOf(`data-resize-handle="${handle}"`);
    assert.ok(handleIndex > sheetIndex, `the ${handle} handle renders inside its sheet`);
    const panelIndex = source.indexOf(panel, sheetIndex);
    assert.ok(handleIndex < panelIndex, "the handle precedes the panel content (a DOM child of the sheet)");
    const sheetBlock = source.slice(sheetIndex, panelIndex);
    assert.ok(sheetBlock.includes("height: `" + "${" + resizer + ".heightPct}%`"), "the sheet height is the resizer state");
    assert.ok(sheetBlock.includes(`{...${resizer}.separatorProps}`), "the handle carries the separator props");
    assert.doesNotMatch(sheetBlock, /onClick/, "the handle never dismisses the sheet");
  }
  // Exactly two sheet handles exist (the mobile branches); the desktop
  // branches keep no height resize (the pi#117 dock resizer is width-only).
  assert.equal((source.match(/pf-sheet-resize-handle/g) ?? []).length, 2, "only the two mobile sheets carry a sheet handle");
});

test("the unread dot is its own element beside the running-count badge", () => {
  assert.match(source, /pfUnreadTotal > 0 && \(/);
  assert.match(source, /aria-label=\{translate\("polyforge\.messages\.unread"\)\}/);
  const dotIndex = source.indexOf('aria-label={translate("polyforge.messages.unread")}');
  const badgeIndex = source.indexOf("aria-label={String(pfRunningCount)}");
  assert.ok(dotIndex >= 0 && badgeIndex > dotIndex, "the running badge still renders separately after the unread dot");
  assert.match(source, /\{pfRunningCount > 9 \? "9\+" : pfRunningCount\}/, "the running count is not replaced");
});

test("the shell passes the messages surface and tab state into both dock branches", () => {
  assert.match(source, /const pfMessagesSurface = \{/);
  assert.match(source, /onOpenAnchor: openPfAnchor/);
  assert.equal((source.match(/messagesSurface=\{pfMessagesSurface\}/g) ?? []).length, 2, "desktop dock and mobile sheet share the surface");
  assert.match(source, /activeTab=\{pfActiveTab\}/);
  assert.match(source, /onActiveTab=\{setPfActiveTab\}/);
});

test("both dock branches forward Work's sub-tab state", () => {
  assert.match(source, /workTab: pfWorkTab,\s*setWorkTab: setPfWorkTab,/, "the shell reads the sub-tab from the one hook");
  assert.equal((source.match(/workTab=\{pfWorkTab\}\s*onWorkTab=\{setPfWorkTab\}/g) ?? []).length, 2, "mobile sheet and desktop dock share the sub-tab");
});

test("both dock branches forward the 工单列表 badge count and its reporter", () => {
  assert.match(source, /workItemsRunning: pfWorkItemsRunning,\s*setWorkItemsRunning: setPfWorkItemsRunning,/, "the shell reads the badge state from the one hook");
  assert.equal((source.match(/workItemsRunning=\{pfWorkItemsRunning\}\s*onWorkItemsRunningChange=\{setPfWorkItemsRunning\}/g) ?? []).length, 2,
    "mobile sheet and desktop dock share the badge count");
});

test("both dock branches pass the memory cwd to the panel", () => {
  assert.equal((source.match(/memoryCwd=\{projectTrustCwd\}/g) ?? []).length, 2, "mobile sheet and desktop dock share the session cwd");
  assert.match(source, /const projectTrustCwd = selectedSession\?\.cwd \?\? effectiveNewSessionCwd;/, "the cwd is the config panel's own source");
});

test("the desktop dock pin persists globally, suppresses only the outside-press dismissal and reopens on load", () => {
  assert.match(source, /const PF_DOCK_PINNED_KEY = "pi-web:polyforge-dock-pinned";/, "one global storage key");
  // Guarded reads and writes, like the other pi-web:polyforge-* keys.
  const mount = source.slice(source.indexOf("let pinned = false;"), source.indexOf("const togglePfDockPin"));
  assert.match(mount, /try \{\s*pinned = window\.localStorage\.getItem\(PF_DOCK_PINNED_KEY\) === "1";\s*\} catch/, "a guarded read; only \"1\" pins");
  assert.match(mount, /if \(window\.matchMedia\(MOBILE_MEDIA_QUERY\)\.matches\) return;\s*setBgPanelOpen\(false\);\s*setPfPanelOpen\(true\);/,
    "a pinned load reopens the dock on desktop only, closing bg-tasks first (shared slot)");
  assert.match(source, /\}, \[\]\);\s*\n\s*const togglePfDockPin/, "the reopen is a one-shot mount effect");
  const toggle = source.slice(source.indexOf("const togglePfDockPin"), source.indexOf("}, [pfDockPinned]);"));
  assert.match(toggle, /try \{\s*if \(next\) window\.localStorage\.setItem\(PF_DOCK_PINNED_KEY, "1"\);\s*else window\.localStorage\.removeItem\(PF_DOCK_PINNED_KEY\);\s*\} catch/, "a guarded write");
  // Closing never touches the pin: both close paths only close the dock.
  assert.equal((source.match(/onClose=\{\(\) => setPfPanelOpen\(false\)\}/g) ?? []).length, 2, "the panel close only closes (sheet and dock)");
  assert.equal((source.match(/setPfDockPinned\(/g) ?? []).length, 2, "only the mount read and the pin control write the pin");
  // The pin control renders on the desktop dock only.
  assert.match(source, /showPin=\{false\}\s*pinned=\{pfDockPinned\}/, "the mobile sheet shows no pin");
  assert.match(source, /showPin\s*pinned=\{pfDockPinned\}\s*onTogglePin=\{togglePfDockPin\}/, "the desktop dock shows the pin");
  // The dock stays an absolute overlay (no chat resize).
  const dock = source.slice(source.indexOf('id="polyforge-dock"'), source.indexOf('id="polyforge-dock"') + 400);
  assert.match(dock, /position: "absolute", top: 0, right: 0, bottom: 0/, "the dock stays an overlay");
});
