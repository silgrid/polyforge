// carries: components/AgentSessionPanel.tsx, components/AppShell.tsx
// polyforge#255 u5 (Q3 A): the top bar's subagent row body opens the child's
// in-chat progress panel; the old side-panel toggle button is gone, and the
// row of the chat already showing the child is inert rather than nesting it.
// The row's click and the opener closure are executed from their source.
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";
import ts from "typescript";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { AgentSessionPanel } = await jiti.import("./AgentSessionPanel.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
await jiti.import("@/lib/i18n/messages/en");

const source = await readFile(new URL("./AgentSessionPanel.tsx", import.meta.url), "utf8");
const ast = ts.createSourceFile("AgentSessionPanel.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);

function arrow(marker) {
  let found = null;
  const walk = (node) => {
    if (ts.isArrowFunction(node)) {
      const text = node.getText(ast);
      if (text.includes(marker) && (found === null || text.length < found.length)) found = text;
    }
    ts.forEachChild(node, walk);
  };
  walk(ast);
  assert.ok(found, `no arrow contains ${JSON.stringify(marker)}`);
  return found;
}

/** The expression of the JSX attribute `name` whose text contains `marker`. */
function attributeExpression(name, marker) {
  let found = null;
  const walk = (node) => {
    if (found) return;
    if (ts.isJsxAttribute(node) && node.name.getText(ast) === name && node.initializer && ts.isJsxExpression(node.initializer)) {
      const expression = node.initializer.expression;
      if (expression && expression.getText(ast).includes(marker)) {
        found = expression.getText(ast);
        return;
      }
    }
    ts.forEachChild(node, walk);
  };
  walk(ast);
  assert.ok(found, `no ${name} attribute contains ${JSON.stringify(marker)}`);
  return found;
}

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

test("polyforge#255 u5: the row body opens the in-chat panel; no side-panel toggle button remains", () => {
  assert.doesNotMatch(source, /onToggleSubagentTab|subagent\.openTab|subagent\.closeTab/, "the old side-panel toggle is deleted");
  // The row button's own click: main selects, a subagent opens the panel.
  const calls = [];
  const onClick = attributeExpression("onClick", "main ? onSelect : onOpen");
  const click = new Function("main", "onSelect", "onOpen", `return (${onClick})`)(false, () => calls.push("select"), () => calls.push("open"));
  click();
  assert.deepEqual(JSON.parse(JSON.stringify(calls)), ["open"], "a subagent row opens the panel");
  calls.length = 0;
  new Function("main", "onSelect", "onOpen", `return (${onClick})`)(true, () => calls.push("select"), () => calls.push("open"))();
  assert.deepEqual(JSON.parse(JSON.stringify(calls)), ["select"], "the main row still selects its session");

  // The opener closure: the current child is inert, another child opens.
  const opener = arrow("if (session.id === selectedSessionId) return;");
  const opened = [];
  const open = new Function("session", "selectedSessionId", "onOpenSubagent", "sessionTitle", `return (${opener})`);
  open(child, "child-1", (...args) => opened.push(args), () => "Find the parser")();
  assert.deepEqual(JSON.parse(JSON.stringify(opened)), [], "the chat already showing the child opens nothing");
  open(child, "root-1", (...args) => opened.push(args), () => "Find the parser")();
  assert.deepEqual(JSON.parse(JSON.stringify(opened)), [["child-1", "Find the parser"]], "another child's row opens its panel");

  const html = render("root-1");
  assert.match(html, /Find the parser/, "the subagent row renders its description");
  assert.equal((html.match(/<button/g) ?? []).length, 2, "no toggle button renders beside the row");
  assert.doesNotMatch(html, /aria-disabled="true"/, "a child that is not the current chat opens its panel");
});

test("polyforge#255 u5: the row of the chat already showing the child is disabled (no self-embedding)", () => {
  const html = render("child-1");
  assert.match(html, /<button[^>]*aria-disabled="true"[^>]*>[\s\S]*?Find the parser/, "the current child's row is inert");
});
