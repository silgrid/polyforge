import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const { clampChatContentWidth, clampChatContentFontSize, terminalFontSize } = await jiti.import("../hooks/useChatAppearance.ts");

test("the width slider floors at 600 but keeps the 820 default (pi#78)", () => {
  assert.equal(clampChatContentWidth(600), 600);
  assert.equal(clampChatContentWidth(590), 600, "values below the floor clamp up to the minimum");
  assert.equal(clampChatContentWidth(820), 820);
});

test("chat width validation preserves the default and supported range", () => {
  assert.equal(clampChatContentWidth(undefined), 820);
  assert.equal(clampChatContentWidth(null), 820, "a missing stored preference falls to the default (fresh profile, pi#78)");
  assert.equal(clampChatContentWidth("invalid"), 820);
  assert.equal(clampChatContentWidth(500), 600, "below the 600 floor clamps up");
  assert.equal(clampChatContentWidth(700), 700, "700 is inside the 600-floor range now (pi#78)");
  assert.equal(clampChatContentWidth(1104), 1104);
  assert.equal(clampChatContentWidth(2400), 2000);
});

test("chat font size preserves the default and bounds stored or supplied values", () => {
  for (const value of [undefined, null, "invalid", Infinity, NaN]) {
    assert.equal(clampChatContentFontSize(value), 14);
  }
  assert.equal(clampChatContentFontSize(8), 12);
  assert.equal(clampChatContentFontSize("18"), 18);
  assert.equal(clampChatContentFontSize(18.7), 19);
  assert.equal(clampChatContentFontSize(30), 24);
});

test("the terminal keeps 13px at the default chat size and moves with the slider", () => {
  for (const value of [undefined, null, "invalid", 14]) assert.equal(terminalFontSize(value), 13);
  assert.equal(terminalFontSize(12), 11);
  assert.equal(terminalFontSize(18), 17);
  assert.equal(terminalFontSize(24), 23);
  assert.equal(terminalFontSize(30), 23);
});
