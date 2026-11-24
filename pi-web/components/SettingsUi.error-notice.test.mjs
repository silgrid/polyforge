// carries: components/SettingsUi.tsx
// polyforge#259 (T4 AC2): 设置页的错误 notice 是 `role="alert"`，主文案是中文，
// 服务端原文只在可展开的「详情」折叠里。期望值来自要求本身：错误态必须有
// role=alert；没有原文时不渲染折叠（不留一个空的「详情」）。
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { ConfigErrorNotice } = await jiti.import("./SettingsUi.tsx");

const h = React.createElement;
const render = (props) => renderToStaticMarkup(h(ConfigErrorNotice, props));

test("the error notice is an alert that shows the Chinese summary first", () => {
  const html = render({ summary: "这个文件只读，改不了", detailsLabel: "详情", details: "forbidden: /root/.pi/agent/settings.json is read-only" });
  assert.match(html, /role="alert"/, "the notice is not an alert");
  assert.match(html, /这个文件只读，改不了/);
  assert.ok(html.indexOf("这个文件只读，改不了") < html.indexOf("forbidden:"), "the English original leads the summary");
});

test("the notice keeps the server's own words inside a details fold", () => {
  const html = render({ summary: "写入失败", detailsLabel: "详情", details: "pf: daemon not reachable (connection refused)" });
  assert.match(html, /<details[^>]*class="config-error-notice-details"/, "the original has no details fold");
  assert.match(html, /<summary[^>]*>详情<\/summary>/, "the fold is not named");
  const fold = /<details[\s\S]*<\/details>/.exec(html)?.[0] ?? "";
  assert.match(fold, /pf: daemon not reachable \(connection refused\)/, "the original is outside the fold");
  assert.doesNotMatch(html.replace(fold, ""), /pf: daemon not reachable/, "the original also renders outside the fold");
});

test("no original means no fold, not an empty one", () => {
  const html = render({ summary: "已保存", detailsLabel: "详情" });
  assert.doesNotMatch(html, /<details/, "an empty details fold renders");
  assert.match(html, /已保存/);
});