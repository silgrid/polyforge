import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

// the question the step waits on, above the job card's input
// box, renders as Markdown — the step-ask question table as a table, inline
// code and bold — in its amber frame with its step, fold toggle and the
// verbatim text on hover; folded to three lines until opened.
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const composer = await jiti.import("./PolyforgeJobComposer.tsx");

const TABLE_QUESTION = [
  "Two choices for the badge:",
  "",
  "| 问题 | 推荐选项 | 理由 | 重点关注程度 |",
  "|---|---|---|---|",
  "| 1. colour of `badge-ask`? | **amber** | matches the frame | ★★ |",
].join("\n");

// The whole `<div …>` element opening at `start` (its nested divs balanced).
const element = (html, start) => {
  const tags = /<div[\s>]|<\/div>/g;
  tags.lastIndex = start;
  let depth = 0;
  for (let tag = tags.exec(html); tag; tag = tags.exec(html)) {
    depth += tag[0] === "</div>" ? -1 : 1;
    if (depth === 0) return html.slice(start, tags.lastIndex);
  }
  return html.slice(start);
};
const renderStrip = (question) => {
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null,
    React.createElement(composer.PolyforgeJobComposer, { wi: "wi_E2E502", state: { input: "open", awaiting: true }, question: { id: "2026-11-12T01:00:00Z", step: "build", question }, onSent() {} })));
  const start = html.indexOf("data-pf-chat-question=");
  assert.ok(start >= 0, "the question strip is rendered");
  const strip = element(html, html.lastIndexOf("<div", start));
  const textAt = strip.indexOf("data-pf-chat-question-text=");
  assert.ok(textAt >= 0, "the question text is rendered");
  return { strip, text: element(strip, strip.lastIndexOf("<div", textAt)) };
};
const escaped = (value) => value.replaceAll("&", "&amp;").replaceAll("\"", "&quot;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");

test("the question above the input box renders as Markdown — a GFM table, inline code and bold — with its frame, step, fold toggle and verbatim title", () => {
  const { strip, text } = renderStrip(TABLE_QUESTION);
  assert.match(text, /<table/);
  for (const header of ["问题", "推荐选项", "理由", "重点关注程度"]) assert.match(text, new RegExp(`<th[^>]*>${header}</th>`));
  assert.match(text, /<code[^>]*>badge-ask<\/code>/);
  assert.match(text, /<strong>amber<\/strong>/);
  assert.ok(!text.replace(/ title="[^"]*"/, "").includes("|---|"), "the table's separator row is not shown as text (the title alone keeps it)");
  assert.ok(text.includes(`title="${escaped(TABLE_QUESTION)}"`), "the verbatim question on hover");
  assert.match(text, /^<div[^>]*style="[^"]*max-height:5.1em[^"]*overflow:hidden/, "folded: three lines' height, the rest hidden");
  assert.match(strip, /data-pf-chat-question-id="2026-11-12T01:00:00Z"/);
  assert.match(strip, /border-left:3px solid #d97706/);
  assert.match(strip, />The agent asks · build<\/span>/);
  assert.match(strip, /data-pf-chat-question-toggle="" aria-expanded="false"/);
});

test("a question over MAX_MARKDOWN_CHARS above the input box renders as plain text, no Markdown and no reveal button", async () => {
  const { MAX_MARKDOWN_CHARS } = await jiti.import("./SafeMarkdownBody.tsx");
  const question = `${TABLE_QUESTION}\n${"x".repeat(MAX_MARKDOWN_CHARS)}\nE2E502-TAIL`;
  const { strip, text } = renderStrip(question);
  assert.ok(!text.includes("<table"), "no Markdown table");
  assert.ok(!text.includes("<button"), "no click-to-reveal box");
  assert.match(text, /<div style="white-space:pre-wrap;overflow-wrap:anywhere">Two choices for the badge:/);
  assert.ok(text.replace(/ title="[^"]*"/, "").includes("|---|---|---|---|") && text.includes("E2E502-TAIL</div>"), "the raw text, in full");
  assert.match(text, /^<div[^>]*style="[^"]*max-height:5.1em[^"]*overflow:hidden/, "still folded to three lines' height");
  assert.match(strip, /data-pf-chat-question-toggle="" aria-expanded="false"/);
});
