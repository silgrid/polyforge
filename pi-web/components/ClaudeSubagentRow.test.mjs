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
const { ClaudeSubagentRow, ClaudeSubagentPanelContext } = await jiti.import("./ClaudeSubagentRow.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const OPEN_PANEL = "Open panel";

function run(fields) {
  return {
    toolCallId: "toolu_agent",
    agentId: "a1b2c3d4e5f6",
    agentType: "general-purpose",
    description: "Read note.txt first line",
    prompt: "Read note.txt and report its first line.",
    background: false,
    status: "running",
    startedAt: Date.UTC(2026, 10, 10, 12, 0, 0),
    entries: [],
    ...fields,
  };
}

function render(runFields, openPanel) {
  const row = React.createElement(ClaudeSubagentRow, { run: run(runFields), toolName: "Agent", renderEntry: () => null });
  const tree = openPanel === undefined ? row : React.createElement(ClaudeSubagentPanelContext.Provider, { value: openPanel }, row);
  return renderToStaticMarkup(React.createElement(I18nProvider, null, tree));
}

// Every <button> with its whole content, nesting kept visible: a button opened inside
// another shows up as a `<button` within the outer match.
function buttons(html) {
  const found = [];
  for (const match of html.matchAll(/<button([^>]*)>/g)) {
    const start = match.index + match[0].length;
    const inner = html.slice(start, html.indexOf("</button>", start));
    found.push({
      attrs: match[1],
      inner,
      label: `${/aria-label="([^"]*)"/.exec(match[1])?.[1] ?? ""} ${/title="([^"]*)"/.exec(match[1])?.[1] ?? ""} ${inner.replace(/<[^>]+>/g, " ")}`,
    });
  }
  return found;
}

test("an Agent row offers an open-panel button beside its header for a running or finished run", () => {
  for (const status of ["running", "completed"]) {
    const html = render({ status, ...(status === "completed" ? { result: "first line", usage: { toolUses: 1, durationMs: 1200, totalTokens: 900 } } : {}) }, () => {});
    const all = buttons(html);
    const open = all.filter((button) => button.label.includes(OPEN_PANEL));
    assert.equal(open.length, 1, `a ${status} row offers ${open.length} open-panel buttons:\n${html}`);
    const toggle = all.find((button) => /aria-expanded=/.test(button.attrs));
    assert.ok(toggle, `a ${status} row has no header toggle`);
    assert.ok(!toggle.label.includes(OPEN_PANEL), `the open-panel button sits inside the ${status} row's header toggle`);
    assert.ok(all.every((button) => !button.inner.includes("<button")), `a ${status} row nests a button inside another:\n${html}`);
  }
  const without = render({ status: "running" }, undefined);
  assert.ok(!without.includes(OPEN_PANEL), "a row without a panel to open still offers the open-panel button");
});
