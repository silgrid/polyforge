// carries: components/AppShell*, components/ChatWindow*, components/MessageView*, components/PiRunningList*, components/AgentSessionPanel*, components/TabBar*
// polyforge#255 u5: every pi subagent entry — the running list's row, the
// Agent tool card, the top bar's subagent row — opens the in-chat progress
// panel of the chat it belongs to (Q3/Q4 A), and the right panel's agent tab
// path is gone. Split panes keep their own chat: the top bar's request names
// the active pane, cards and the running list open their own ChatWindow's
// panel, and no entry creates a right-panel tab.
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
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const [appShell, chatWindow, messageView, tabBar, agentSessionPanel] = await Promise.all([
  readFile(new URL("./AppShell.tsx", import.meta.url), "utf8"),
  readFile(new URL("./ChatWindow.tsx", import.meta.url), "utf8"),
  readFile(new URL("./MessageView.tsx", import.meta.url), "utf8"),
  readFile(new URL("./TabBar.tsx", import.meta.url), "utf8"),
  readFile(new URL("./AgentSessionPanel.tsx", import.meta.url), "utf8"),
]);

/** `source` from `startMarker` up to (excluding) the next `endMarker`. */
function between(source, startMarker, endMarker, what) {
  const start = source.indexOf(startMarker);
  assert.ok(start >= 0, `${what}: missing anchor ${JSON.stringify(startMarker)}`);
  const end = source.indexOf(endMarker, start + startMarker.length);
  assert.ok(end > start, `${what}: missing end anchor ${JSON.stringify(endMarker)}`);
  return source.slice(start, end);
}

const renderPane = between(appShell, "renderPane={(sid, focused) => {", ") : showChat ? (", "the split renderPane");
const sentinelPane = between(renderPane, "if (isNewSessionTab(sid)) {", "const paneSession", "the sentinel branch");
const sessionPane = renderPane.slice(renderPane.indexOf("const paneSession"));
const classicWindow = between(appShell, ") : showChat ? (", ') : initialCwdStatus === "validating" ? (', "the classic ChatWindow branch");
const openPanelHandler = between(appShell, "const handleOpenPiSubagentPanel = useCallback(", "}, [isMobile, sidebarSessionId]);", "the pi panel opener");

const ast = ts.createSourceFile("AppShell.tsx", appShell, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);

/** The text of the arrow function AppShell passes to useCallback, by binding name. */
function extractAppShellCallback(name, fileAst = ast) {
  let node = null;
  function find(n) {
    if (ts.isVariableDeclaration(n) && n.name.getText(fileAst) === name) {
      node = n;
      return;
    }
    ts.forEachChild(n, find);
  }
  find(fileAst);
  assert.ok(node, `AppShell must define ${name}`);
  return node.initializer.arguments[0].getText(fileAst);
}

const plain = (value) => JSON.parse(JSON.stringify(value));
function runExtracted(callbackText, sandbox) {
  return new Script(ts.transpileModule(`(${callbackText})`, {
    compilerOptions: { target: ts.ScriptTarget.ES2020 },
  }).outputText).runInNewContext(sandbox);
}

function renderCard(props) {
  const block = {
    type: "toolCall",
    toolCallId: "call-agent-split",
    toolName: "Agent",
    input: { subagent_type: "Explore", prompt: "Find the parser", description: "Find parser" },
  };
  const result = {
    role: "toolResult",
    toolCallId: block.toolCallId,
    content: [{ type: "text", text: "Parser is in lib/parser.ts" }],
    details: {
      kind: "pi-web-subagent",
      sessionId: "child-session",
      profile: "Explore",
      description: "Find parser",
      status: "completed",
      runInBackground: false,
      createdAt: "2026-01-01T00:00:00.000Z",
    },
  };
  return renderToStaticMarkup(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(MessageView, {
        message: { role: "assistant", provider: "anthropic", model: "claude-test", content: [block] },
        toolResults: new Map([[block.toolCallId, result]]),
        ...props,
      }),
    ),
  );
}

test("polyforge#255 u5: the split session pane and the classic window both receive the top-bar panel request", () => {
  for (const [name, window] of [["session pane", sessionPane], ["classic window", classicWindow]]) {
    assert.match(window, /piSubagentPanelRequest={piSubagentPanelRequest}/, `${name} receives the request`);
    assert.match(window, /onPiSubagentPanelRequestHandled={handlePiSubagentPanelRequestHandled}/, `${name} acknowledges it`);
    assert.doesNotMatch(window, /onOpenSubagent=\{handleOpenSubagentTab\}/, `${name} keeps no right-panel tab opener`);
  }
  // session={null}: no transcript Agent card and no running list, so no request either.
  assert.doesNotMatch(sentinelPane, /piSubagentPanelRequest=/, "the new-session sentinel has no subagent panel");
});

test("polyforge#255 u5: the top bar's opener targets the active pane, never the selected session", () => {
  assert.doesNotMatch(openPanelHandler, /selectedSession/, "pane focus never updates selectedSession; reading it would open the wrong chat");
  const requests = [];
  const open = runExtracted(openPanelHandler, {
    sidebarSessionId: "pane-A",
    isMobile: false,
    piSubagentRequestSeqRef: { current: 0 },
    setPiSubagentPanelRequest: (request) => requests.push(request),
    setActiveTopPanel: () => {},
  });
  open("child-1", "Explore");
  assert.deepEqual(plain(requests), [{ paneSessionId: "pane-A", childSessionId: "child-1", requestId: 1 }], "the request names the active pane's chat");
  open("child-2", "Explore");
  assert.equal(requests[1].requestId, 2, "each click is its own one-shot request");
  assert.match(appShell, /selectedSessionId=\{sidebarSessionId\}/, "the switcher's selected row is the active pane's session");
  assert.match(appShell, /onOpenSubagent=\{handleOpenPiSubagentPanel\}/, "the switcher's subagent row opens the in-chat panel");
  assert.match(appShell, /getSessionFamily\(sessionsWithSelection, sidebarSessionId \?\? undefined\)/, "the switcher's family is the active pane's family");
});

test("polyforge#255 u5: the right panel's agent tab path and its state are gone", () => {
  for (const [name, source] of [["AppShell", appShell], ["ChatWindow", chatWindow], ["TabBar", tabBar], ["AgentSessionPanel", agentSessionPanel]]) {
    assert.doesNotMatch(source, /SessionChatPanel/, `${name} keeps no right-panel subagent chat`);
    assert.doesNotMatch(source, /handleOpenSubagentTab|openSubagentTabIds|handleToggleSubagentTab|lastAgentTabId/, `${name} keeps no agent-tab state`);
  }
  assert.doesNotMatch(appShell, /agentTabs/, "the agent tab list state is deleted");
  assert.doesNotMatch(appShell, /`agent:\$\{/, "no agent:<sessionId> tab id is built");
  assert.doesNotMatch(tabBar, /kind === "agent"|subagent\.tabLabel/, "the tab bar renders no agent tab");
  assert.doesNotMatch(agentSessionPanel, /onToggleSubagentTab|subagent\.openTab|subagent\.closeTab/, "the switcher has no side-panel toggle button");
});

test("polyforge#255 u5: ChatWindow owns one pi subagent panel and opens it from its own list and cards", () => {
  assert.match(chatWindow, /kind: "subagent" \| "task" \| "piTask" \| "piHistory" \| "piSubagent"/, "the one floating slot holds the pi subagent's panel");
  assert.match(chatWindow, /<PiSubagentPanel/, "the panel renders");
  assert.match(chatWindow, /onOpenSubagent=\{openPiSubagentPanel\}/, "the running list and Agent cards open this chat's panel");
  const effect = between(chatWindow, "const request = piSubagentPanelRequest;", "}, [piSubagentPanelRequest, session?.id, onPiSubagentPanelRequestHandled]);", "the request effect");
  assert.match(effect, /request\.paneSessionId !== session\.id/, "a request for another pane is ignored");
  assert.match(effect, /kind: "piSubagent", id: request\.childSessionId/, "the accepted request opens the child's panel here");
  assert.match(effect, /onPiSubagentPanelRequestHandled\?\.\(request\.requestId\)/, "the request is consumed once");
  assert.match(chatWindow, /const jumpToPiSubagent = useCallback/, "Jump closes the panel and scrolls to the launch card");
  assert.match(chatWindow, /data-pi-agent-tool-call=/, "the launch card is locatable by its tool call id");
});

test("polyforge#255 u5: a pi subagent's Agent card opens the in-chat panel with its session and description", () => {
  const withOpener = renderCard({ onOpenSubagent() {} });
  assert.match(withOpener, /aria-label="Open sub-agent panel"/, "the card offers the panel action");
  assert.match(withOpener, /data-pi-agent-tool-call="call-agent-split"/, "the card carries the tool call id Jump looks for");
  assert.match(withOpener, />Agent</, "the card itself still renders");
  assert.match(withOpener, />Explore</, "the card keeps its profile");

  const withoutOpener = renderCard({});
  assert.doesNotMatch(withoutOpener, /aria-label="Open sub-agent panel"/, "without an opener the card renders no button");
  assert.match(messageView, /onClick=\{\(\) => onOpenSubagent\(subagent\.sessionId, subagent\.description \|\| subagent\.profile\)\}/, "the button opens the run's own session under its description");
});
