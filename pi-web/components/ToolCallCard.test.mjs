import "#test-env";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { createJiti } from "jiti";

// the transcript's tool card shell, shared with the Polyforge
// progress cards. Rendered for real (react-dom/server) plus source pins for
// the reuse rule (one shell, one markdown guard; no copy left in MessageView).
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { ToolCallCard, ToolCallCardStat, TOOL_DURATION_MIN_MS, formatToolDuration, toolCallCardTone } = await jiti.import("./ToolCallCard.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { SafeMarkdownBody, MAX_MARKDOWN_CHARS } = await jiti.import("./SafeMarkdownBody.tsx");

const render = (props, children) => renderToStaticMarkup(React.createElement(ToolCallCard, { onToggle: () => {}, ...props }, children));

test("the shell draws one rounded bordered card in the call's tone with one aria-expanded header button", () => {
  const ok = render({ isError: false, expanded: false, name: "bash", preview: "ls -la", headerTitle: "bash: ls -la", "data-probe": "x" });
  assert.match(ok, /^<div data-probe="x" style="border-radius:7px;overflow:hidden;font-size:12px;border:1px solid rgba\(34,197,94,0.25\);background:rgba\(34,197,94,0.04\)">/);
  assert.equal([...ok.matchAll(/<button/g)].length, 1, "one header control");
  assert.match(ok, /<button type="button" aria-expanded="false" title="bash: ls -la"/);
  assert.match(ok, /white-space:nowrap[^"]*">ls -la<\/span>/, "the preview is one ellipsized line");
  assert.match(ok, /text-overflow:ellipsis/);
  const failed = render({ isError: true, expanded: true, name: "bash", preview: "x" }, React.createElement("pre", null, "BODY"));
  assert.match(failed, /border:1px solid rgba\(248,113,113,0.45\);background:rgba\(248,113,113,0.05\)/);
  assert.match(failed, /aria-expanded="true"/);
  assert.match(failed, /<\/div><pre>BODY<\/pre><\/div>$/, "the body follows the header row inside the card");
  assert.deepEqual(toolCallCardTone(false).border, "1px solid rgba(34,197,94,0.25)");
});

test("stats sit between the preview and the chevron; the aside sits beside the header button", () => {
  const html = render({ isError: false, expanded: false, name: "n", preview: "p",
    stats: React.createElement(ToolCallCardStat, null, "1.5s"), aside: React.createElement("button", { type: "button" }, "ASIDE") });
  assert.ok(html.indexOf(">p</span>") < html.indexOf(">1.5s</span>") && html.indexOf(">1.5s</span>") < html.indexOf("<svg"), "preview, stat, chevron");
  assert.ok(html.indexOf("</button><button") > 0 && html.includes(">ASIDE</button>"), "the aside follows the header button");
});

test("the duration format and its floor are the transcript's", () => {
  assert.equal(TOOL_DURATION_MIN_MS, 100);
  assert.equal(formatToolDuration(1500), "1.5s");
  assert.equal(formatToolDuration(200), "0.2s");
  assert.equal(formatToolDuration(185_000), "3m 5s");
});

test("the markdown guard renders markdown below its limit and a reveal button above it", () => {
  const wrap = (children) => renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(SafeMarkdownBody, null, children)));
  const small = wrap("P1\n\n- a\n- b");
  assert.match(small, /<p>P1<\/p>/);
  assert.match(small, /<li>a<\/li>/);
  const huge = wrap("x".repeat(MAX_MARKDOWN_CHARS + 1));
  assert.ok(!huge.includes("<p>") && huge.includes("<button"), "an oversized body is not parsed");
});

test("reuse, not a copy: MessageView renders its tool card through the shell and imports the markdown guard", () => {
  const view = readFileSync(new URL("./MessageView.tsx", import.meta.url), "utf8");
  assert.match(view, /import \{ SafeMarkdownBody \} from "\.\/SafeMarkdownBody";/);
  assert.match(view, /from "\.\/ToolCallCard";/);
  assert.match(view, /<ToolCallCard\b/);
  assert.doesNotMatch(view, /function SafeMarkdownBody|const MAX_MARKDOWN_CHARS|rgba\(34,197,94,0\.04\)|function formatToolDuration/, "no second shell, guard or duration format");
});
