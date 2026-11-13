import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

// the step's `pf step ask` question in the job detail's
// execution progress (the amber 「等待 owner」 block) renders as Markdown — the
// four-column question table as a table, inline code and bold — through the
// narration's SafeMarkdownBody, in its frame with its label and time.
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const panel = await jiti.import("./PolyforgePanel.tsx");

const TABLE_QUESTION = [
  "Two choices for the badge:",
  "",
  "| 问题 | 推荐选项 | 理由 | 重点关注程度 |",
  "|---|---|---|---|",
  "| 1. colour of `badge-ask`? | **amber** | matches the frame | ★★ |",
].join("\n");

const AT = Date.UTC(2026, 10, 12, 1, 2, 3);

const renderAsk = (text) => {
  const lines = [{ id: "a1", kind: "ask", event: "awaiting_owner", tone: "amber", at: AT, text }];
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null,
    React.createElement(panel.PolyforgeDetailProgress, { wi: "wi_E2E502", running: false, snapshot: { available: true, lines }, echoes: [], scrollRef: { current: null } })));
  const start = html.indexOf('data-pf-progress-kind="ask"');
  assert.ok(start >= 0, "the ask block is rendered");
  return html.slice(html.lastIndexOf("<div", start));
};

test("the progress flow's ask block renders its question as Markdown — a GFM table, inline code and bold — in its amber frame with the label and time", () => {
  const ask = renderAsk(TABLE_QUESTION);
  assert.match(ask, /<table/);
  for (const header of ["问题", "推荐选项", "理由", "重点关注程度"]) assert.match(ask, new RegExp(`<th[^>]*>${header}</th>`));
  assert.match(ask, /<code[^>]*>badge-ask<\/code>/);
  assert.match(ask, /<strong>amber<\/strong>/);
  assert.ok(!ask.includes("|---|"), "the table's separator row is not shown as text");
  assert.match(ask, /title="awaiting_owner"/);
  assert.match(ask, /border-left:3px solid #d97706/);
  assert.match(ask, /background:rgba\(217, 119, 6, 0.10\)/);
  assert.match(ask, />Waiting for the owner<\/span>/);
  assert.match(ask, />\d{1,2}:\d\d:\d\d[^<]*<\/span>/, "the time stays");
});

test("an ask over MAX_MARKDOWN_CHARS renders inline in full as plain wrapped text, no Markdown and no reveal button", async () => {
  const { MAX_MARKDOWN_CHARS } = await jiti.import("./SafeMarkdownBody.tsx");
  const text = `${TABLE_QUESTION}\n${"x".repeat(MAX_MARKDOWN_CHARS)}\nE2E502-TAIL`;
  const ask = renderAsk(text);
  assert.ok(!ask.includes("<table"), "no Markdown table");
  assert.ok(!ask.includes("<button"), "no click-to-reveal box");
  assert.match(ask, /<div style="white-space:pre-wrap;overflow-wrap:anywhere">Two choices for the badge:/);
  assert.ok(ask.includes("|---|---|---|---|") && ask.includes("E2E502-TAIL"), "the raw text, in full");
  assert.match(ask, /border-left:3px solid #d97706/);
  assert.match(ask, />Waiting for the owner<\/span>/);
});
