import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { ClaudeSubagentPanel, isClaudeSubagentPanelEscape } = await jiti.import("./ClaudeSubagentPanel.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const NOW = Date.UTC(2026, 10, 10, 12, 0, 0);

const STEP = {
  id: "entry-step-1",
  message: { role: "assistant", content: [{ type: "toolCall", id: "toolu_read", name: "Read", arguments: { file_path: "note.txt" } }] },
};
const STEP_RESULT = {
  id: "entry-result-1",
  message: { role: "toolResult", toolCallId: "toolu_read", toolName: "Read", content: [{ type: "text", text: "first line" }] },
};

function run(fields) {
  return {
    toolCallId: "toolu_agent",
    agentId: "a1b2c3d4e5f6",
    agentType: "general-purpose",
    description: "Read note.txt first line",
    prompt: "Read note.txt and report its first line.",
    background: false,
    status: "running",
    startedAt: NOW - 12_000,
    entries: [STEP, STEP_RESULT],
    ...fields,
  };
}

function render(props) {
  const rendered = [];
  const renderEntry = (message, toolResults, key) => {
    rendered.push({ message, toolResults, key });
    return React.createElement("div", { key, "data-rendered-entry": key }, `step ${message.content[0].name} -> ${toolResults.get(message.content[0].id)?.content[0].text}`);
  };
  const html = renderToStaticMarkup(
    React.createElement(I18nProvider, null, React.createElement(ClaudeSubagentPanel, { renderEntry, onJump: () => {}, onClose: () => {}, onRelay: () => {}, now: NOW, ...props })),
  );
  return { html, rendered };
}

function buttons(html) {
  return [...html.matchAll(/<button([^>]*)>([\s\S]*?)<\/button>/g)].map((match) => ({
    attrs: match[1],
    label: `${/aria-label="([^"]*)"/.exec(match[1])?.[1] ?? ""} ${match[2].replace(/<[^>]+>/g, " ")}`,
    disabled: /\sdisabled=""/.test(match[1]),
  }));
}

function textarea(html) {
  const match = /<textarea([^>]*)>/.exec(html);
  return match ? { attrs: match[1], disabled: /\sdisabled=""/.test(match[1]) } : null;
}

test("the panel shows the run's prompt, steps and result with the Agent row's own body", () => {
  const live = render({ run: run() });
  assert.match(live.html, /data-claude-subagent-panel="toolu_agent"/);
  assert.match(live.html, /Read note\.txt first line/);
  assert.match(live.html, /Read note\.txt and report its first line\./);
  // The step goes through the chat's own renderer, its tool result paired with it.
  assert.deepEqual(live.rendered.map((entry) => entry.key), ["entry-step-1"]);
  assert.match(live.html, /data-rendered-entry="entry-step-1"/);
  assert.match(live.html, /step Read -&gt; first line/);
  assert.match(live.html, /Running/);
  assert.doesNotMatch(live.html, />Result</);

  const settled = render({ run: run({ status: "completed", result: "The first line is: hello panel", usage: { totalTokens: 1200, toolUses: 1, durationMs: 9_000 } }) });
  assert.match(settled.html, />Result</);
  assert.match(settled.html, /The first line is: hello panel/);
  assert.match(settled.html, /data-rendered-entry="entry-step-1"/);
  assert.doesNotMatch(settled.html, /Running/);
});

test("the panel offers jump to launch, close and an input box that waits for claude's agent id", () => {
  const ready = render({ run: run() }).html;
  const readyButtons = buttons(ready);
  assert.ok(readyButtons.some((button) => /Jump to launch/.test(button.label)), ready);
  assert.ok(readyButtons.some((button) => /Close/.test(button.label)), ready);
  assert.ok(readyButtons.some((button) => /Send/.test(button.label)), ready);
  const box = textarea(ready);
  assert.ok(box, "the panel has an input box");
  assert.equal(box.disabled, false);
  assert.doesNotMatch(ready, /agent id/i);

  // A finished run's panel keeps its input: a message resumes it.
  assert.equal(textarea(render({ run: run({ status: "completed", result: "done" }) }).html).disabled, false);

  const waiting = render({ run: run({ agentId: undefined }) }).html;
  assert.equal(textarea(waiting).disabled, true);
  const send = buttons(waiting).find((button) => /Send/.test(button.label));
  assert.ok(send, waiting);
  assert.equal(send.disabled, true);
  // The reason is visible text, not only a tooltip.
  assert.match(waiting.replace(/<[^>]+>/g, " ").replace(/title="[^"]*"/g, ""), /agent id/i);
});

test("Esc closes the panel when pressed in its chat pane or on the page of the active pane, never someone else's or a composition's", () => {
  const composer = { nodeName: "TEXTAREA" };
  const otherPane = { nodeName: "TEXTAREA" };
  const body = { nodeName: "BODY" };
  const pane = { contains: (node) => node === composer };
  const key = (fields) => ({ key: "Escape", isComposing: false, keyCode: 27, defaultPrevented: false, target: composer, ...fields });

  // The composer keeps the focus while the panel is open: its Esc is the panel's.
  assert.equal(isClaudeSubagentPanelEscape(key({}), pane, true), true);
  assert.equal(isClaudeSubagentPanelEscape(key({}), pane, false), true);
  // Nothing focused: the active pane's panel only.
  for (const target of [body, { nodeName: "HTML" }, { nodeName: "#document" }, null]) {
    assert.equal(isClaudeSubagentPanelEscape(key({ target }), pane, true), true, String(target?.nodeName));
    assert.equal(isClaudeSubagentPanelEscape(key({ target }), pane, false), false, String(target?.nodeName));
  }
  // Another pane's composer, or a dialog above the page.
  assert.equal(isClaudeSubagentPanelEscape(key({ target: otherPane }), pane, true), false);
  assert.equal(isClaudeSubagentPanelEscape(key({ target: { nodeName: "DIV" } }), null, true), false);
  // An Escape that cancels an IME composition, or that something handled first, or another key.
  assert.equal(isClaudeSubagentPanelEscape(key({ isComposing: true }), pane, true), false);
  assert.equal(isClaudeSubagentPanelEscape(key({ keyCode: 229 }), pane, true), false);
  assert.equal(isClaudeSubagentPanelEscape(key({ defaultPrevented: true }), pane, true), false);
  assert.equal(isClaudeSubagentPanelEscape(key({ key: "Enter" }), pane, true), false);
});

test("the panel's left border is as wide as its other borders, only its colour marks the run", () => {
  for (const [status, accent] of [["running", "#8b5cf6"], ["failed", "#f87171"]]) {
    const { html } = render({ run: run({ status }) });
    const section = /<section[^>]*data-claude-subagent-panel="[^"]*"[^>]*style="([^"]*)"/.exec(html) ?? /<section[^>]*style="([^"]*)"[^>]*data-claude-subagent-panel=/.exec(html);
    assert.ok(section, html.slice(0, 600));
    const style = section[1];
    assert.match(style, /(?:^|;)border:1px solid /, `${status}: ${style}`);
    // No side gets a width of its own: the left edge keeps the 1px of \`border\`.
    assert.doesNotMatch(style, /(?:^|;)border-left:/, `${status}: ${style}`);
    assert.doesNotMatch(style, /(?:^|;)border-(?:left|top|right|bottom)-width:/, `${status}: ${style}`);
    assert.match(style, new RegExp(`(?:^|;)border-left-color:${accent}(?:;|$)`), `${status}: ${style}`);
  }
});
