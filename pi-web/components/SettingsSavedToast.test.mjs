// carries: components/SettingsSavedToast.tsx
// polyforge#259 (T4 AC1): 开关写成功后出现 toast「已保存 · 撤销」，撤销只在它
// 的生命周期内可用（过期就什么都不渲染）；词由调用方给，组件自己不翻译。
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { SettingsSavedToast } = await jiti.import("./SettingsSavedToast.tsx");

const h = React.createElement;
const TEXTS = { saved: "已保存", undo: "撤销" };
const render = (props) => renderToStaticMarkup(h(SettingsSavedToast, { texts: TEXTS, onUndo: () => {}, ...props }));

test("a saved toast says 已保存 and offers 撤销", () => {
  const html = render({ expiresAt: Date.now() + 6000 });
  assert.match(html, /role="status"/, "the toast is not a status");
  assert.match(html, /已保存/);
  assert.match(html, /<button[^>]*>撤销<\/button>/, "the toast offers no undo");
});

test("an expired toast renders nothing at all", () => {
  assert.equal(render({ expiresAt: Date.now() - 1 }), "", "an expired toast still shows");
});
