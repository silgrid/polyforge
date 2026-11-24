// carries: components/ConfigDangerMenu.tsx
// polyforge#259 (T4 AC1): 危险操作（删除 / 移除）放进详情 header 的「⋯」菜单，
// 并要二次确认——第一次点击只提问，第二次才执行。期望值来自这句话：三个阶段
// 各自渲染什么，以及「只有确认态允许写入」。
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { ConfigDangerMenu, dangerConfirmAllowed } = await jiti.import("./ConfigDangerMenu.tsx");

const h = React.createElement;
const TEXTS = { menu: "More actions", action: "Remove from Claude", confirmText: "Remove it?", confirm: "Remove", cancel: "Cancel" };
const render = (stage) => renderToStaticMarkup(h(ConfigDangerMenu, {
  texts: TEXTS, stage, onStageChange: () => {}, onConfirm: () => {},
}));

test("the write is reachable only from the confirmation stage", () => {
  assert.equal(dangerConfirmAllowed("closed"), false, "the closed menu can write");
  assert.equal(dangerConfirmAllowed("menu"), false, "picking the action writes without asking");
  assert.equal(dangerConfirmAllowed("confirm"), true);
});

test("a closed menu offers only the more button, never the action itself", () => {
  const html = render("closed");
  assert.match(html, /aria-label="More actions"/, "the more button has no accessible name");
  assert.doesNotMatch(html, /Remove from Claude/, "the danger action shows while the menu is closed");
  assert.doesNotMatch(html, /Remove it\?/, "the confirmation shows while the menu is closed");
});

test("the menu lists the action, and picking it asks instead of writing", () => {
  const html = render("menu");
  assert.match(html, /Remove from Claude/, "the menu does not list the action");
  assert.doesNotMatch(html, /Remove it\?/, "picking the action jumps straight to the write");
});

test("the confirmation stage names the action and offers confirm and cancel", () => {
  const html = render("confirm");
  assert.match(html, /Remove it\?/, "the confirmation asks nothing");
  assert.match(html, /<button[^>]*>Remove<\/button>/, "no confirm button");
  assert.match(html, /<button[^>]*>Cancel<\/button>/, "no cancel button");
  // The confirming button is the danger variant: the shared button, red.
  assert.match(html, /config-button-danger/, "the confirm button is not the shared danger button");
});
