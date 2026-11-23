// carries: components/AppShell*, components/ChatWindow*, components/MessageView*, components/PiRunningList*, components/AgentSessionPanel*, components/TabBar*
// polyforge#255 u5: every pi subagent entry — the running list's row, the
// Agent tool card, the top bar's subagent row — opens the in-chat progress
// panel of the chat it belongs to (Q3/Q4 A), carries its own launch call id
// (Q6), and the right panel's agent tab path is gone. The handlers are
// executed from their source, not matched as text: the pane filter, the
// queued row, the card's tool call id and the current-child guard all run.
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { Script } from "node:vm";
import { createJiti } from "jiti";
import ts from "typescript";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { MessageView } = await jiti.import("./MessageView.tsx");
const { PiRunningList } = await jiti.import("./PiRunningList.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
await jiti.import("@/lib/i18n/messages/en");

const [appShell, chatWindow, messageView, runningList] = await Promise.all([
  readFile(new URL("./AppShell.tsx", import.meta.url), "utf8"),
  readFile(new URL("./ChatWindow.tsx", import.meta.url), "utf8"),
  readFile(new URL("./MessageView.tsx", import.meta.url), "utf8"),
  readFile(new URL("./PiRunningList.tsx", import.meta.url), "utf8"),
]);
const appAst = ts.createSourceFile("AppShell.tsx", appShell, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
const chatAst = ts.createSourceFile("ChatWindow.tsx", chatWindow, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
const messageAst = ts.createSourceFile("MessageView.tsx", messageView, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
const listAst = ts.createSourceFile("PiRunningList.tsx", runningList, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);

const plain = (value) => JSON.parse(JSON.stringify(value));
function run(text, sandbox) {
  return new Script(ts.transpileModule(`(${text})`, { compilerOptions: { target: ts.ScriptTarget.ES2020 } }).outputText).runInNewContext(sandbox);
}

/** The arrow function whose source contains `marker`. */
function arrow(source, fileAst, marker) {
  let found = null;
  function walk(node) {
    if (found) return;
    if (ts.isArrowFunction(node) && node.getText(fileAst).includes(marker)) {
      found = node.getText(fileAst);
      return;
    }
    ts.forEachChild(node, walk);
  }
  walk(fileAst);
  assert.ok(found, `no arrow function contains ${JSON.stringify(marker)}`);
  return found;
}

function callbackBinding(source, fileAst, name) {
  let node = null;
  function walk(n) {
    if (ts.isVariableDeclaration(n) && n.name.getText(fileAst) === name) {
      node = n;
      return;
    }
    ts.forEachChild(n, walk);
  }
  walk(fileAst);
  assert.ok(node, `missing ${name}`);
  return node.initializer.arguments[0].getText(fileAst);
}

const renderCard = (props) => {
  const block = {
    type: "toolCall",
    toolCallId: "call-agent-1",
    toolName: "Agent",
    input: { subagent_type: "Explore", prompt: "Find the parser", description: "Find parser" },
  };
  const result = {
    role: "toolResult",
    toolCallId: block.toolCallId,
    content: [{ type: "text", text: "Parser is in lib/parser.ts" }],
    details: { kind: "pi-web-subagent", sessionId: "child-1", profile: "Explore", description: "Find parser", status: "completed", runInBackground: false, createdAt: "2026-01-01T00:00:00.000Z" },
  };
  return renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(MessageView, {
    message: { role: "assistant", provider: "anthropic", model: "claude-test", content: [block] },
    toolResults: new Map([[block.toolCallId, result]]),
    ...props,
  })));
};

test("polyforge#255 u5: the top bar's opener targets the active pane, never the selected session", () => {
  const handler = callbackBinding(appShell, appAst, "handleOpenPiSubagentPanel");
  assert.doesNotMatch(handler, /selectedSession/, "pane focus never updates selectedSession; reading it would open the wrong chat");
  const requests = [];
  const open = run(handler, {
    sidebarSessionId: "pane-A",
    isMobile: false,
    piSubagentRequestSeqRef: { current: 0 },
    setPiSubagentPanelRequest: (request) => requests.push(request),
    setActiveTopPanel: () => {},
  });
  open("child-1", "Explore");
  assert.deepEqual(plain(requests), [{ paneSessionId: "pane-A", childSessionId: "child-1", requestId: 1 }], "the request names the active pane's chat");
});

test("polyforge#255 u5: the ChatWindow request effect ignores another pane and consumes its own", () => {
  const effect = arrow(chatWindow, chatAst, "request.paneSessionId !== session.id");
  const other = [];
  run(effect, {
    piSubagentPanelRequest: { paneSessionId: "pane-B", childSessionId: "child-1", requestId: 4 },
    session: { id: "pane-A" },
    setOpenPanelChoice: (choice) => other.push(choice),
    onPiSubagentPanelRequestHandled: (id) => other.push(["handled", id]),
  })();
  assert.deepEqual(plain(other), [], "a request for another pane changes nothing here");

  const own = [];
  run(effect, {
    piSubagentPanelRequest: { paneSessionId: "pane-A", childSessionId: "child-1", requestId: 5, toolCallId: "call-card" },
    session: { id: "pane-A" },
    setOpenPanelChoice: (choice) => own.push(choice),
    onPiSubagentPanelRequestHandled: (id) => own.push(["handled", id]),
  })();
  assert.deepEqual(plain(own), [
    { sessionId: "pane-A", kind: "piSubagent", id: "child-1", toolCallId: "call-card" },
    ["handled", 5],
  ], "the owning pane opens the child and consumes the request once");
});

test("polyforge#255 u5: a card keeps its own tool call id for Jump", () => {
  const opener = callbackBinding(chatWindow, chatAst, "openPiSubagentPanel");
  const choices = [];
  const open = run(opener, { session: { id: "pane-A" }, setOpenPanelChoice: (choice) => choices.push(choice) });
  open("child-1", "Find parser", "call-agent-1");
  assert.deepEqual(plain(choices), [{ sessionId: "pane-A", kind: "piSubagent", id: "child-1", label: "Find parser", toolCallId: "call-agent-1" }], "the card's own call id travels with the open choice");

  const click = arrow(messageView, messageAst, "onOpenSubagent(subagent.sessionId");
  const calls = [];
  run(click, { onOpenSubagent: (...args) => calls.push(args), subagent: { sessionId: "child-1", description: "Find parser", profile: "Explore" }, block: { toolCallId: "call-agent-1" } })();
  assert.deepEqual(plain(calls), [["child-1", "Find parser", "call-agent-1"]], "the button passes the card's tool call id, not only the child id");
});

test("polyforge#255 u5: a queued running-list row opens the same in-chat panel", () => {
  const click = arrow(runningList, listAst, "onOpenSubagent(run.sessionId");
  const opened = [];
  run(click, { running: false, onOpenSubagent: (...args) => opened.push(args), run: { sessionId: "sub-1" }, label: "Audit the parsers" })();
  assert.deepEqual(plain(opened), [["sub-1", "Audit the parsers"]], "a queued row forwards its opener too");
  assert.doesNotMatch(click, /if \(running\)/, "the click is not gated on the running state");
});

test("polyforge#255 u5: the top bar's subagent row opens the panel; the current child's row is inert", () => {
  const onOpenArrow = arrow(appShell, appAst, "if (session.id === selectedSessionId) return;");
  const calls = [];
  const open = run(onOpenArrow, {
    session: { id: "child-1", relation: { kind: "subagent", description: "Find parser", profile: "Explore" } },
    selectedSessionId: "child-1",
    onOpenSubagent: (...args) => calls.push(args),
    sessionTitle: () => "Find parser",
  });
  open();
  assert.deepEqual(plain(calls), [], "a row for the chat already showing the child does not open a nested panel");

  run(onOpenArrow, {
    session: { id: "child-2", relation: { kind: "subagent", description: "Other task", profile: "Explore" } },
    selectedSessionId: "root-1",
    onOpenSubagent: (...args) => calls.push(args),
    sessionTitle: () => "Other task",
  })();
  assert.deepEqual(plain(calls), [["child-2", "Other task"]], "another subagent's row opens its panel");
});

test("polyforge#255 u5: the right panel's agent tab path is gone", () => {
  for (const source of [appShell, chatWindow, messageView, runningList]) {
    assert.doesNotMatch(source, /SessionChatPanel|handleOpenSubagentTab|openSubagentTabIds|handleToggleSubagentTab|lastAgentTabId|agentTabs/, "no agent-tab path remains");
  }
  assert.doesNotMatch(appShell, /`agent:\$\{/, "no agent:<sessionId> tab id is built");
  const withOpener = renderCard({ onOpenSubagent() {} });
  assert.match(withOpener, /aria-label="Open sub-agent panel"/, "the card offers the panel action");
  assert.match(withOpener, /data-pi-agent-tool-call="call-agent-1"/, "the card carries its tool call id");
  assert.doesNotMatch(renderCard({}), /aria-label="Open sub-agent panel"/, "without an opener the card renders no button");
});
