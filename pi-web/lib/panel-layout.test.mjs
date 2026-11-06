import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const {
  clampPanelWidth,
  clampSheetHeightPct,
  getDefaultRightPanelWidth,
  getPolyforgeDockMaxWidth,
  getPolyforgeMessagesListDefaultWidth,
  getPolyforgeMessagesListMaxWidth,
  getRightPanelMaxWidth,
  getSidebarMaxWidth,
  parseStoredSheetHeightPct,
  POLYFORGE_BLOCK_STYLE,
  POLYFORGE_DOCK_DEFAULT_WIDTH,
  POLYFORGE_DOCK_MAX_WIDTH,
  POLYFORGE_DOCK_MIN_WIDTH,
  POLYFORGE_MESSAGES_LIST_CHAT_MIN_WIDTH,
  POLYFORGE_MESSAGES_LIST_DEFAULT_MAX_WIDTH,
  POLYFORGE_MESSAGES_LIST_DEFAULT_MIN_WIDTH,
  POLYFORGE_MESSAGES_LIST_MAX_WIDTH,
  POLYFORGE_MESSAGES_LIST_MIN_WIDTH,
  SHEET_DEFAULT_HEIGHT_PCT,
  SHEET_MAX_HEIGHT_PCT,
  SHEET_MIN_HEIGHT_PCT,
} = await jiti.import("./panel-layout.ts");

test("clamps panel widths to finite bounds", () => {
  assert.equal(clampPanelWidth(420.4, 180, 480), 420);
  assert.equal(clampPanelWidth(120, 180, 480), 180);
  assert.equal(clampPanelWidth(600, 180, 480), 480);
  assert.equal(clampPanelWidth(Number.NaN, 180, 480), 180);
  assert.equal(clampPanelWidth(200, 300, 250), 300);
});

test("keeps the responsive right panel default within useful limits", () => {
  assert.equal(getDefaultRightPanelWidth(700), 360);
  assert.equal(getDefaultRightPanelWidth(1366), 574);
  assert.equal(getDefaultRightPanelWidth(1920), 640);
});

test("polyforge dock (pi#117): 460 default, 340 min, 90vw live max", () => {
  assert.equal(POLYFORGE_DOCK_DEFAULT_WIDTH, 460, "the dock opens at 460 by default");
  assert.equal(POLYFORGE_DOCK_MIN_WIDTH, 340, "the drag clamp never narrows below 340");
  assert.equal(getPolyforgeDockMaxWidth(1280), 1152, "90vw at 1280");
  assert.equal(getPolyforgeDockMaxWidth(1920), 1728, "90vw at 1920");
  // The static cap only bounds aria-valuemax before mount; it must never
  // bind tighter than the live 90vw max on any real viewport.
  assert.ok(POLYFORGE_DOCK_MAX_WIDTH >= getPolyforgeDockMaxWidth(7680), "the cap stays at or above 90vw at 8K");
  assert.equal(clampPanelWidth(700, POLYFORGE_DOCK_MIN_WIDTH, getPolyforgeDockMaxWidth(1280)), 700);
  assert.equal(clampPanelWidth(2000, POLYFORGE_DOCK_MIN_WIDTH, getPolyforgeDockMaxWidth(1280)), 1152, "an oversized drag clamps to 90vw");
  assert.equal(clampPanelWidth(100, POLYFORGE_DOCK_MIN_WIDTH, getPolyforgeDockMaxWidth(1280)), 340, "an undersized drag clamps to 340");
});

test("the Messages list keeps the old default and the chat's 200px floor", () => {
  assert.equal(POLYFORGE_MESSAGES_LIST_MIN_WIDTH, 120);
  assert.equal(POLYFORGE_MESSAGES_LIST_MAX_WIDTH, 480);
  assert.equal(POLYFORGE_MESSAGES_LIST_CHAT_MIN_WIDTH, 200);
  assert.equal(POLYFORGE_MESSAGES_LIST_DEFAULT_MIN_WIDTH, 120);
  assert.equal(POLYFORGE_MESSAGES_LIST_DEFAULT_MAX_WIDTH, 220);
  // Default: 35% of the panel, clamped exactly like the pre-#222 CSS clamp.
  assert.equal(getPolyforgeMessagesListDefaultWidth(459), 161);
  assert.equal(getPolyforgeMessagesListDefaultWidth(340), 120);
  assert.equal(getPolyforgeMessagesListDefaultWidth(1000), 220);
  // Cap: min(480, panel − 200); the hook raises a cap below 120 back to 120.
  assert.equal(getPolyforgeMessagesListMaxWidth(340), 140);
  assert.equal(getPolyforgeMessagesListMaxWidth(459), 259);
  assert.equal(getPolyforgeMessagesListMaxWidth(480), 280);
  assert.equal(getPolyforgeMessagesListMaxWidth(1000), 480);
  assert.equal(getPolyforgeMessagesListMaxWidth(300), 100, "the raw cap can fall below the minimum; the hook's max(min, cap) keeps 120");
  assert.equal(clampPanelWidth(100, POLYFORGE_MESSAGES_LIST_MIN_WIDTH, getPolyforgeMessagesListMaxWidth(300)), 120, "120 wins when the cap falls below it");
});

test("reserves chat space while split panels are visible", () => {
  assert.equal(getSidebarMaxWidth({
    viewportWidth: 700,
    rightPanelOpen: true,
    rightPanelWidth: 560,
  }), 380);
  assert.equal(getSidebarMaxWidth({
    viewportWidth: 1366,
    rightPanelOpen: true,
    rightPanelWidth: 686,
  }), 260);
  assert.equal(getRightPanelMaxWidth({
    viewportWidth: 1024,
    sidebarOpen: true,
    sidebarWidth: 260,
  }), 344);
  assert.equal(getRightPanelMaxWidth({
    viewportWidth: 1366,
    sidebarOpen: true,
    sidebarWidth: 260,
  }), 686);
});

test("does not rewrite desktop widths while the file panel is in overlay mode", () => {
  assert.equal(getRightPanelMaxWidth({
    viewportWidth: 900,
    sidebarOpen: true,
    sidebarWidth: 480,
  }), 1200);
});

test("pi#120: the sheet height contract is default 62, bounds [30, 90]", () => {
  assert.equal(SHEET_DEFAULT_HEIGHT_PCT, 62);
  assert.equal(SHEET_MIN_HEIGHT_PCT, 30);
  assert.equal(SHEET_MAX_HEIGHT_PCT, 90);
});

test("pi#120: sheet height percentages clamp to [30, 90]; invalid input reads as the 62 default", () => {
  assert.equal(clampSheetHeightPct(62), 62);
  assert.equal(clampSheetHeightPct(29.6), 30, "dragging past the minimum clamps (never dismisses)");
  assert.equal(clampSheetHeightPct(90.4), 90, "dragging past the maximum clamps");
  assert.equal(clampSheetHeightPct(20), 30);
  assert.equal(clampSheetHeightPct(95), 90);
  assert.equal(clampSheetHeightPct(62.4), 62, "percentages round to integers");
  assert.equal(clampSheetHeightPct(Number.NaN), 62, "NaN reads as the 62 default, never the min");
  assert.equal(clampSheetHeightPct(Number.POSITIVE_INFINITY), 62, "non-finite never silently becomes a bound");
});

test("pi#120: stored sheet heights parse only within [30, 90]; anything else reads as absent", () => {
  assert.equal(parseStoredSheetHeightPct(null), null, "absent reads as absent (the caller takes 62)");
  assert.equal(parseStoredSheetHeightPct("62"), 62);
  assert.equal(parseStoredSheetHeightPct("30"), 30);
  assert.equal(parseStoredSheetHeightPct("90"), 90);
  assert.equal(parseStoredSheetHeightPct("29"), null, "out-of-range low reads as absent");
  assert.equal(parseStoredSheetHeightPct("91"), null, "out-of-range high reads as absent");
  assert.equal(parseStoredSheetHeightPct("abc"), null, "unparsable reads as absent");
  assert.equal(parseStoredSheetHeightPct(""), null);
  assert.equal(parseStoredSheetHeightPct("62.5"), 63, "a fractional in-range value rounds");
});

test("the one Polyforge block treatment keeps its frame and a denser 6px 8px padding", () => {
  // owner-intended change: 8px 10px -> 6px 8px, shared by the Jobs cards
  // and the Tasks blocks, the one block treatment.
  assert.deepEqual(POLYFORGE_BLOCK_STYLE, {
    background: "none",
    border: "1px solid var(--border)",
    borderRadius: 6,
    padding: "6px 8px",
    boxSizing: "border-box",
  });
});
