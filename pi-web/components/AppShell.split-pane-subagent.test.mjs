// carries: components/AppShell*, components/ChatWindow*, components/MessageView*
// polyforge#229: split view is the default desktop layout, but renderPane never
// passed onOpenSubagent to the pane's ChatWindow. MessageView only renders an
// Agent card's 「open tab」 button when `subagent && onOpenSubagent`, and the
// running list above the composer only opens a run when its click reaches an
// opener, so in the layout users actually see both entry points did nothing.
// The classic full-width window and the sub-agent panel were wired; this fixes
// only the pane, with the same handler, and pins the counter-example that a
// card without an opener renders no button at all.
//
// The pane handler is not session-bound: pane focus never updates
// selectedSession, so a handler that read it (like the classic file-link
// handler does, hence getPaneOpenFileHandler) would open the wrong session.
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { MessageView } = await jiti.import("./MessageView.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const [appShell, chatWindow, messageView] = await Promise.all([
  readFile(new URL("./AppShell.tsx", import.meta.url), "utf8"),
  readFile(new URL("./ChatWindow.tsx", import.meta.url), "utf8"),
  readFile(new URL("./MessageView.tsx", import.meta.url), "utf8"),
]);

/** `source` from `startMarker` up to (excluding) the next `endMarker`. */
function between(source, startMarker, endMarker, what) {
  const start = source.indexOf(startMarker);
  assert.ok(start >= 0, `${what}: missing anchor ${JSON.stringify(startMarker)}`);
  const end = source.indexOf(endMarker, start + startMarker.length);
  assert.ok(end > start, `${what}: missing end anchor ${JSON.stringify(endMarker)}`);
  return source.slice(start, end);
}

const OPEN_SUBAGENT_PROP = /onOpenSubagent=\{handleOpenSubagentTab\}/;

const renderPane = between(appShell, "renderPane={(sid, focused) => {", ") : showChat ? (", "the split renderPane");
const classicWindow = between(appShell, ") : showChat ? (", ') : initialCwdStatus === "validating" ? (', "the classic ChatWindow branch");
const sentinelPane = between(renderPane, "if (isNewSessionTab(sid)) {", "const paneSession", "the sentinel branch");
const sessionPane = renderPane.slice(renderPane.indexOf("const paneSession"));
const subagentHandler = between(
  appShell,
  "const handleOpenSubagentTab = useCallback(",
  "}, [isMobile]);",
  "the subagent tab handler",
);

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

test("the split session pane opens subagents in a side-panel tab", () => {
  // The requirement: an Agent card in a split pane offers the same 「open tab」
  // action as the classic layout, so the pane must hand ChatWindow the opener
  // MessageView and the running list gate their buttons on.
  assert.match(
    sessionPane,
    OPEN_SUBAGENT_PROP,
    "the split session pane must pass onOpenSubagent={handleOpenSubagentTab} to its ChatWindow",
  );
});

test("the new-session sentinel pane carries no subagent opener", () => {
  // session={null}: no transcript Agent card exists, and ChatWindow skips the
  // running list for a new session, so there is nothing to open there.
  assert.doesNotMatch(
    sentinelPane,
    /onOpenSubagent=/,
    "the sentinel pane renders a new-session composer and can open no subagent",
  );
});

test("the pane and the classic window share the same, session-independent subagent handler", () => {
  assert.match(classicWindow, OPEN_SUBAGENT_PROP, "the classic ChatWindow stays wired (no regression)");
  // Pane focus only updates focusedPaneId, never selectedSession; a handler
  // that read selectedSession would open a tab labelled with the wrong session.
  assert.doesNotMatch(
    subagentHandler,
    /selectedSession/,
    "handleOpenSubagentTab must not read selectedSession, so every pane may share it",
  );
  assert.match(
    subagentHandler,
    /setActiveFileTabId\(`agent:\$\{sessionId\}`\)/,
    "the shared handler activates the subagent's own agent:<id> tab",
  );
  assert.match(subagentHandler, /setRightPanelOpen\(true\)/, "the shared handler opens the right panel");
});

test("the running list forwards each row to the session's subagent opener", () => {
  // ChatWindow:2054 — a running run's click must reach the same opener as the
  // Agent card, or the list stays inert in the pane.
  const listBlock = between(chatWindow, "<PiRunningList", "onOpenTask={openPiTaskPanel}", "the running list");
  assert.match(
    listBlock,
    /onOpenSubagent=\{\(subagentSessionId, label\) => onOpenSubagent\?\.\(subagentSessionId, label\)\}/,
    "each running subagent row opens its own session id under its own label",
  );
});

test("a subagent card opens the tab with its session id and its description", () => {
  // The button's click is the only place the (sessionId, label) pair is built:
  // the run's own id, and its description falling back to the agent profile.
  assert.match(
    messageView,
    /onClick=\{\(\) => onOpenSubagent\(subagent\.sessionId, subagent\.description \|\| subagent\.profile\)\}/,
    "the card's button must open the run's session under its description (else its profile)",
  );
  assert.match(messageView, /aside=\{subagent && onOpenSubagent && \(/, "the button exists only with an opener");
});

test("a subagent result with no opener renders no side-panel button", () => {
  // The defect's visible half: without the opener the card silently drops the
  // action. Both readings are observed on the rendered HTML, not the source.
  const withOpener = renderCard({ onOpenSubagent() {} });
  assert.match(withOpener, /aria-label="View sub-agent work in side panel"/);

  const withoutOpener = renderCard({});
  assert.doesNotMatch(withoutOpener, /View sub-agent work in side panel/);
  assert.match(withoutOpener, />Agent</, "the card itself still renders");
  assert.match(withoutOpener, />Explore</, "the card keeps its profile");
});
