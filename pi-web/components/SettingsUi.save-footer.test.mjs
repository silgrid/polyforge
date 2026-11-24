// carries: components/SettingsUi.tsx
// polyforge#259 (T4 AC1): 详情页的 footer 在 dirty 时给出「保存（n 项改动）」与
// 「丢弃」，n=0 时一个都不给（没有要写的东西）；计数文案由调用方给。
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { ConfigSaveFooter } = await jiti.import("./SettingsUi.tsx");

const h = React.createElement;
const render = (props) => renderToStaticMarkup(h(ConfigSaveFooter, {
  discardLabel: "Discard",
  saveLabel: (n) => `Save (${n})`,
  onDiscard: () => {},
  onSave: () => {},
  ...props,
}));

test("a clean form offers no save and no discard", () => {
  const html = render({ dirty: 0 });
  assert.doesNotMatch(html, /Save \(/, "a clean form still offers Save");
  assert.doesNotMatch(html, /Discard/, "a clean form still offers Discard");
});

test("n unsaved changes read as the count and the button's own number", () => {
  const html = render({ dirty: 2, status: "2 unsaved changes" });
  assert.match(html, /2 unsaved changes/, "the footer does not state the count");
  assert.match(html, /<button[^>]*>Save \(2\)<\/button>/, "the save button does not carry the count");
  assert.match(html, /<button[^>]*>Discard<\/button>/, "the footer offers no discard");
});

test("a save in flight disables the button", () => {
  const html = render({ dirty: 1, saving: true });
  assert.match(html, /<button[^>]*disabled=""[^>]*>Save \(1\)<\/button>/, "the button is clickable while saving");
});
