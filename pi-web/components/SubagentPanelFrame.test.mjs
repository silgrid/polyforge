// carries: components/SubagentPanelFrame.tsx, components/ClaudeSubagentPanel.tsx
// polyforge#255 u1: the shared shell the Claude and Pi subagent panels both
// use — the floating section, its top-edge drag handle, the header/body/footer
// slots and the one Esc rule. The Claude panel's existing selectors, storage
// key and IME/pane gating are the contract this extraction must keep.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { SubagentPanelFrame, isClaudeSubagentPanelEscape, CLAUDE_SUBAGENT_PANEL_HEIGHT_KEY, CLAUDE_SUBAGENT_PANEL_MIN_HEIGHT } = await jiti.import("./SubagentPanelFrame.tsx");

function render(props = {}) {
  return renderToStaticMarkup(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(
        SubagentPanelFrame,
        {
          panelAttrs: { "data-test-panel": "child-1" },
          ariaLabel: "Child one",
          accent: "#8b5cf6",
          borderColor: "rgba(139,92,246,0.35)",
          status: "running",
          resizeLabel: "Resize panel",
          header: React.createElement("div", { "data-test-header": "" }, "HEADER"),
          footer: React.createElement("div", { "data-test-footer": "" }, "FOOTER"),
          ...props,
        },
        React.createElement("div", { "data-test-body": "" }, "BODY"),
      ),
    ),
  );
}

function key(overrides = {}) {
  return { key: "Escape", isComposing: false, keyCode: 27, defaultPrevented: false, target: { nodeName: "DIV" }, ...overrides };
}

test("polyforge#255 u1: the frame renders the drag handle and all three slots under the caller's data attributes", () => {
  const html = render();
  assert.match(html, /<section[^>]*data-test-panel="child-1"/, "the caller's data attribute names the panel");
  assert.match(html, /aria-label="Child one"/, "the caller's label names the section");
  assert.match(html, /data-claude-subagent-panel-resize=""/, "the shared drag handle keeps the existing attribute");
  assert.match(html, /role="separator"/);
  assert.match(html, /aria-orientation="horizontal"/);
  assert.match(html, /data-test-header=""/, "the header slot renders");
  assert.match(html, /data-test-body=""/, "the body slot renders");
  assert.match(html, /data-test-footer=""/, "the footer slot renders");
  assert.ok(html.indexOf("HEADER") < html.indexOf("BODY") && html.indexOf("BODY") < html.indexOf("FOOTER"), "header, body and footer keep their order");
});

test("polyforge#255 u1: a failed run keeps the red left edge; the storage key and minimum height stay Claude's", () => {
  assert.equal(CLAUDE_SUBAGENT_PANEL_HEIGHT_KEY, "pi-web:claude-subagent-panel-height", "the saved Claude height keeps loading");
  assert.equal(CLAUDE_SUBAGENT_PANEL_MIN_HEIGHT, 160, "the panel floor stays 160px");
  const html = render({ status: "failed" });
  assert.match(html, /border-left-color:#f87171/i, "a failed run's left edge is red");
});

test("polyforge#255 u1: Escape closes only from the panel's own pane, never while an input method composes", () => {
  const pane = { contains: (node) => node !== null && node.nodeName === "DIV" };
  assert.equal(isClaudeSubagentPanelEscape(key(), pane, true), true, "a key inside the pane closes it");
  assert.equal(isClaudeSubagentPanelEscape(key(), { contains: () => false }, false), false, "a key in another pane does not");
  assert.equal(isClaudeSubagentPanelEscape(key({ target: { nodeName: "BODY" } }), pane, true), true, "a page-level key closes the active pane");
  assert.equal(isClaudeSubagentPanelEscape(key({ target: { nodeName: "BODY" } }), pane, false), false, "a page-level key leaves an inactive pane alone");
  assert.equal(isClaudeSubagentPanelEscape(key({ isComposing: true }), pane, true), false, "IME composition swallows Escape");
  assert.equal(isClaudeSubagentPanelEscape(key({ keyCode: 229 }), pane, true), false, "Safari's keyCode 229 is the same composition");
  assert.equal(isClaudeSubagentPanelEscape(key({ defaultPrevented: true }), pane, true), false, "an already-handled Escape is not the panel's");
  assert.equal(isClaudeSubagentPanelEscape(key({ key: "Enter" }), pane, true), false, "only Escape closes");
});