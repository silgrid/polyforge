// carries: components/AgentSessionPanel.tsx, components/AppShell.tsx
// polyforge#255 u5 (Q3 A): the top bar's subagent row body opens the child's
// in-chat progress panel; the old side-panel toggle button is gone, and the
// row of the chat already showing the child is disabled rather than nesting it.
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { AgentSessionPanel } = await jiti.import("./AgentSessionPanel.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
await jiti.import("@/lib/i18n/messages/en");

const source = await readFile(new URL("./AgentSessionPanel.tsx", import.meta.url), "utf8");

const root = {
  id: "root-1", path: "/s/root.jsonl", cwd: "/repo", created: "2026-01-01T00:00:00.000Z", modified: "2026-01-01T00:00:00.000Z",
  messageCount: 1, firstMessage: "Root chat", name: "Root chat",
};
const child = {
  ...root,
  id: "child-1",
  path: "/s/child.jsonl",
  firstMessage: "Find the parser",
  name: "Find the parser",
  relation: { kind: "subagent", parentSessionId: "root-1", profile: "Explore", description: "Find the parser", status: "completed" },
};

function render(selectedSessionId) {
  return renderToStaticMarkup(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(AgentSessionPanel, {
        rootSession: root,
        subagents: [child],
        selectedSessionId,
        runningSessionIds: new Set(),
        onSelectSession: () => {},
        onOpenSubagent: () => {},
      }),
    ),
  );
}

test("polyforge#255 u5: the subagent row body opens the in-chat panel; no side-panel toggle button remains", () => {
  assert.match(source, /onClick=\{main \? onSelect : onOpen\}/, "the row body opens the panel (the main row still selects)");
  assert.match(source, /onOpenSubagent: \(sessionId: string, label: string\) => void;/, "the panel opener is the panel's one entry prop");
  assert.doesNotMatch(source, /onToggleSubagentTab|subagent\.openTab|subagent\.closeTab/, "the old side-panel toggle is deleted");
  const html = render("root-1");
  assert.match(html, /Find the parser/, "the subagent row renders its description");
  // Two row buttons only: the main row and the subagent row. A toggle would be a third.
  assert.equal((html.match(/<button/g) ?? []).length, 2, "no toggle button renders beside the row");
  assert.doesNotMatch(html, /aria-disabled="true"/, "a child that is not the current chat opens its panel");
});

test("polyforge#255 u5: the row of the chat already showing the child is disabled (no self-embedding)", () => {
  const html = render("child-1");
  assert.match(html, /<button[^>]*aria-disabled="true"[^>]*>[\s\S]*?Find the parser/, "the current child's row is inert");
});