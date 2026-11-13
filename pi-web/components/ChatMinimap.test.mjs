import "#test-env";
import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createJiti } from "jiti";

registerHooks({
  load(url, context, nextLoad) {
    if (!url.endsWith(".module.css")) return nextLoad(url, context);
    return {
      format: "module",
      shortCircuit: true,
      source: "export default new Proxy({}, { get: (_, key) => String(key) });",
    };
  },
});

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const { AssistantOutline, countToolCalls } = await jiti.import("./ChatMinimap.tsx");

test("renders math in headings without disabling heading navigation", () => {
  const html = renderToStaticMarkup(
    React.createElement(AssistantOutline, {
      markdown: String.raw`# Inline $f_{k,t+1}$

## Parentheses \(x^2 + y^2\)`,
      onHeadingClick() {},
    }),
  );

  assert.match(html, /class="katex"/);
  assert.match(html, /data-preview-heading-index="0"/);
  assert.match(html, /data-preview-heading-index="1"/);
  assert.doesNotMatch(html, /disabled=""/);
});

test("preview popup is click-to-toggle: no hover triggers, rail click toggles, jumps close it", async () => {
  const { readFile } = await import("node:fs/promises");
  const source = await readFile(new URL("./ChatMinimap.tsx", import.meta.url), "utf8");
  // No hover path opens or closes the popup anymore.
  assert.doesNotMatch(source, /onMouseEnter=\{showPreview\}/);
  assert.doesNotMatch(source, /onMouseLeave=\{schedulePreviewHide\}/);
  assert.doesNotMatch(source, /PREVIEW_HIDE_DELAY/);
  // Rail click with the preview open closes it (单击弹回) instead of jumping.
  assert.match(
    source,
    /if \(minimapHovered\) \{\s*hidePreview\(\);\s*return;\s*\}/,
  );
  // A pointerdown outside preview and rail closes it too.
  assert.match(source, /window\.addEventListener\("pointerdown", onPointerDown\)/);
  // Every jump from inside the preview closes it afterwards.
  const jumpHandlers = source.match(/scrollToNode\(node, "smooth"\);[\s\S]{0,80}?setMinimapHovered\(false\);/g) ?? [];
  assert.ok(jumpHandlers.length >= 1, "user-row jump must close the preview");
  assert.match(source, /scrollToAssistant\(node, assistantIndex\);[\s\S]{0,60}?setMinimapHovered\(false\);/);
  assert.match(source, /if \(liveNode\) scrollToHeading[\s\S]{0,80}?setMinimapHovered\(false\);/);
  assert.match(source, /if \(liveNode\) scrollToAssistant\(liveNode, assistantIndex\);[\s\S]{0,60}?setMinimapHovered\(false\);/);
});

test("minimap rail width is 24px and shared across ChatWindow, ChatInput and the running list", async () => {
  const { readFile } = await import("node:fs/promises");
  const minimap = await readFile(new URL("./ChatMinimap.tsx", import.meta.url), "utf8");
  const chatWindow = await readFile(new URL("./ChatWindow.tsx", import.meta.url), "utf8");
  const chatInput = await readFile(new URL("./ChatInput.tsx", import.meta.url), "utf8");
  const runningList = await readFile(new URL("./ClaudeSubagentRunningList.tsx", import.meta.url), "utf8");

  // The rail width is pinned at 24px and exported so all consumers share one constant.
  assert.match(minimap, /export const MINIMAP_WIDTH = 24;/);
  // ChatWindow must import the shared constant instead of defining its own.
  assert.match(chatWindow, /import \{ ChatMinimap, MINIMAP_WIDTH, useMessageRefs \} from "\.\/ChatMinimap";/);
  assert.doesNotMatch(chatWindow, /CHAT_MINIMAP_WIDTH/);
  assert.match(chatWindow, /right: isMobile \? 0 : MINIMAP_WIDTH/);
  // The running list and the composer share one outer gutter: one container
  // in ChatWindow holds both, its sides the chat column's padding, the right one plus the
  // rail on desktop. Neither the composer nor the list carries a side gutter of its own.
  const shared = chatWindow.match(/paddingLeft: CHAT_COLUMN_PADDING,\s*paddingRight: CHAT_COLUMN_PADDING \+ \(isMobile \? 0 : MINIMAP_WIDTH\)/g) ?? [];
  assert.equal(shared.length, 1, "ChatWindow has exactly one container with the shared composer gutter");
  const afterGutter = chatWindow.slice(chatWindow.search(/paddingRight: CHAT_COLUMN_PADDING \+ \(isMobile \? 0 : MINIMAP_WIDTH\)/));
  const listAt = afterGutter.indexOf("<ClaudeSubagentRunningList");
  const inputAt = afterGutter.indexOf("{chatInputElement}");
  assert.ok(listAt > 0 && inputAt > listAt, "the shared-gutter container holds the running list, then the composer");
  assert.doesNotMatch(chatInput, /isMobile \? 16 : 40/);
  assert.doesNotMatch(chatInput, /"0 16px 8px"/);
  assert.doesNotMatch(runningList, /padding: "4px 12px 2px"/);
});

test("counts tool calls per assistant reply, including replies that also answer", () => {
  // A reply can both answer and call tools, so counting text-less messages
  // would undercount this turn.
  assert.equal(countToolCalls({
    role: "assistant",
    content: [
      { type: "text", text: "Let me check that file." },
      { type: "toolCall", toolCallId: "1", toolName: "read", input: {} },
      { type: "toolCall", toolCallId: "2", toolName: "grep", input: {} },
    ],
  }), 2);

  assert.equal(countToolCalls({
    role: "assistant",
    content: [{ type: "toolCall", toolCallId: "3", toolName: "bash", input: {} }],
  }), 1);

  assert.equal(countToolCalls({
    role: "assistant",
    content: [{ type: "text", text: "Done." }],
  }), 0);
});

test("counts no tool calls for non-assistant or string-content messages", () => {
  assert.equal(countToolCalls({ role: "user", content: "run the tests" }), 0);
  assert.equal(countToolCalls({ role: "assistant", content: "plain string" }), 0);
  assert.equal(countToolCalls({ role: "assistant" }), 0);
});

// (upstream #791 client half). The rail renders only after the
// scroll container is measured, so these are source pins like the
// click-to-toggle test above; the end-to-end proof is e2e/run.mjs.
test("loaded turns are numbered after the turns not loaded", async () => {
  const { readFile } = await import("node:fs/promises");
  const source = await readFile(new URL("./ChatMinimap.tsx", import.meta.url), "utf8");
  assert.match(source, /\n  turnsBefore: number;\n\}/, "turnsBefore is a required prop");
  assert.match(source, /String\(turnsBefore \+ node\.index \+ 1\)\.padStart\(2, "0"\)/);
  assert.doesNotMatch(source, /String\(node\.index \+ 1\)/, "no numbering from the loaded window alone");
});

test("one dotted stub above the first node while earlier turns are unloaded", async () => {
  const { readFile } = await import("node:fs/promises");
  const source = await readFile(new URL("./ChatMinimap.tsx", import.meta.url), "utf8");
  assert.equal(source.match(/data-minimap-earlier=""/g)?.length, 1);
  assert.match(
    source,
    /\{turnsBefore > 0 && \([\s\S]{0,120}?<div\s+data-minimap-earlier=""[\s\S]{0,120}?top: 0,\s*height: MINIMAP_PADDING,[\s\S]{0,80}?repeating-linear-gradient\(to bottom, var\(--text-dim\) 0 2px, transparent 2px 4px\)/,
  );
  // The stub comes before the rail nodes, and no node is added per unloaded turn.
  assert.ok(source.indexOf("data-minimap-earlier") < source.indexOf("data-minimap-node-index"));
  assert.doesNotMatch(source, /Array\.from\(\{ length: turnsBefore/);
});

test("the preview's first row reports the unloaded turns; its click scrolls to the top and closes the preview", async () => {
  const { readFile } = await import("node:fs/promises");
  const source = await readFile(new URL("./ChatMinimap.tsx", import.meta.url), "utf8");
  const chatWindow = await readFile(new URL("./ChatWindow.tsx", import.meta.url), "utf8");
  // First child of the preview box, before the turn rows.
  assert.match(
    source,
    /data-minimap-preview-box=""[\s\S]{0,400}?\{turnsBefore > 0 && \(\s*<button\s+type="button"\s+className=\{styles\.earlier\}\s+data-minimap-preview-earlier=""\s+onClick=\{handleEarlierClick\}\s*>\s*\{t\("chatMinimap\.earlierTurns", \{ count: turnsBefore \}\)\}\s*<\/button>\s*\)\}\s*\{allNodes\.map\(/,
  );
  // One stable handler (#127): scroll to the top (the list's sentinel pages
  // history in) and close the preview, like every other row.
  assert.match(
    source,
    /const handleEarlierClick = useCallback\(\(\) => \{\s*scrollContainer\.current\?\.scrollTo\(\{ top: 0, behavior: "smooth" \}\);\s*setMinimapHovered\(false\);\s*\}, \[scrollContainer\]\);/,
  );
  // ChatWindow passes the hook's offset.
  assert.match(chatWindow, /historyCursor, hasEarlierMessages, earlierTurnCount, streamingStore,/);
  assert.match(chatWindow, /<ChatMinimap[\s\S]{0,400}?turnsBefore=\{earlierTurnCount\}/);
});
