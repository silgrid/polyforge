// carries: components/AppShell*, components/ChatWindow*, components/MessageView*, hooks/useAgentSession*, docs/agents/sessions.md, docs/agents/client-platform.md
// polyforge#231: split view's renderPane forwards fewer callbacks to a pane's
// ChatWindow than the classic layout does. The owner confirmed the analysis
// (2026-11-22) and every ruling in "计划审补充": fork/quote opens the forked
// session in a new, focused pane; notifications target the pane's own session;
// explorer refresh is shared; sound controls reach every pane while only the
// focused pane chimes; top-bar projections are focused-gated; search target
// and scroll position are keyed by the pane's sid; the rulings are written in
// docs/agents/sessions.md and beside renderPane.
//
// Proof convention (polyforge#229 owner ruling 7.1-A): this repository has no
// jsdom/testing-library, so the pane wiring is pinned by a source slice of
// renderPane, and the handlers the pane passes are executed for real out of
// AppShell.tsx (the same TS-AST extraction AppShell.split-pane-composer.test.mjs
// uses). No real DOM event is dispatched.
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { Script } from "node:vm";
import ts from "typescript";

const source = await readFile(new URL("./AppShell.tsx", import.meta.url), "utf8");
const docs = await readFile(new URL("../docs/agents/sessions.md", import.meta.url), "utf8");
const clientPlatform = await readFile(new URL("../docs/agents/client-platform.md", import.meta.url), "utf8");

/** `source` from `startMarker` up to (excluding) the next `endMarker`. */
function between(source, startMarker, endMarker, what) {
  const start = source.indexOf(startMarker);
  assert.ok(start >= 0, `${what}: missing anchor ${JSON.stringify(startMarker)}`);
  const end = source.indexOf(endMarker, start + startMarker.length);
  assert.ok(end > start, `${what}: missing end anchor ${JSON.stringify(endMarker)}`);
  return source.slice(start, end);
}

const renderPane = between(source, "renderPane={(sid, focused) => {", ") : showChat ? (", "the split renderPane");
const sentinel = between(renderPane, "if (isNewSessionTab(sid)) {", "const paneSession", "the sentinel branch");
const sessionPane = renderPane.slice(renderPane.indexOf("const paneSession"));
const classicWindow = between(source, ") : showChat ? (", ') : initialCwdStatus === "validating" ? (', "the classic ChatWindow branch");

const ast = ts.createSourceFile("AppShell.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);

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

// Values built inside the extracted vm context have another realm's prototypes,
// so compare their plain JSON shape, never object identity/prototype.
const plain = (value) => JSON.parse(JSON.stringify(value));

function runExtracted(callbackText, sandbox) {
  return new Script(ts.transpileModule(`(${callbackText})`, {
    compilerOptions: { target: ts.ScriptTarget.ES2020 },
  }).outputText).runInNewContext(sandbox);
}

// ---- u1: quote / fork / initial prompt are bound to the pane's session ----

test("pane fork, quote and initial prompt bind to the pane session", async () => {
  // The pane must not reuse the classic handler: pane focus never updates
  // selectedSession, so a fork routed through it would leave the new session
  // out of every pane (the reported bug: "fork 后不跳转").
  assert.match(
    sessionPane,
    /onSessionForked=\{\(newSessionId\) => handlePaneSessionForked\(newSessionId, sid\)\}/,
    "the session pane must route onSessionForked through a sid-aware handler",
  );
  assert.doesNotMatch(sessionPane, /onSessionForked=\{handleSessionForked\}/, "the pane must not reuse the selectedSession-based fork handler");
  // Quote-in-new-chat needs both the handler and the gate that renders the
  // quote toolbar (ChatWindow only offers it when quoteSelectionEnabled).
  assert.match(sessionPane, /onAskInNewChat=\{handlePaneAskInNewChat\}/, "the pane must pass a pane-aware ask-in-new-chat handler");
  assert.match(sessionPane, /quoteSelectionEnabled=\{quoteSelectionEnabled\}/, "the quote toolbar must render in a pane");
  // Owner ruling 9=A folds the ticket-external pass-through props in here.
  assert.match(sessionPane, /modelsRefreshKey=\{modelsRefreshKey\}/, "the pane follows the same model refresh key");
  assert.match(sessionPane, /externalSessionChange=\{externalSessionChange\}/, "the pane follows external writes for its own session");
  // The pending quote prompt belongs to one session id: pane B must never
  // consume or display pane A's pending prompt.
  assert.match(
    sessionPane,
    /initialPrompt=\{pendingQuotePrompt\?\.sessionId === sid \? pendingQuotePrompt\?\.text : undefined\}/,
    "the pane's initial prompt must be filtered by its own sid",
  );
  assert.match(
    sessionPane,
    /onInitialPromptConsumed=\{\(\) => handlePaneInitialPromptConsumed\(sid\)\}/,
    "the pane's consume callback must carry its own sid",
  );
  assert.doesNotMatch(sessionPane, /pendingQuotePrompt\?\.sessionId === selectedSession\?\.id/, "a pane must never read the selected session's pending prompt");

  // Behaviour: a pane fork opens a new tab and focuses it, keeping the source
  // pane; the classic selection/URL update still runs first.
  let tabs = [{ sessionId: "source" }];
  const calls = [];
  const paneFork = runExtracted(extractAppShellCallback("handlePaneSessionForked"), {
    handleSessionForked: (id) => calls.push(["fork", id]),
    splitPaneEnabled: true,
    isMobile: false,
    selectedSession: { id: "source", cwd: "/repo", projectRoot: "/repo" },
    sessionCatalog: [{ id: "source", cwd: "/repo", projectRoot: "/repo" }],
    projectDisplayNameForPath: () => "proj",
    openPaneOp: (prev, id, label, projectName) => [...prev, { sessionId: id, label, projectName }],
    setPaneTabs: (updater) => { tabs = updater(tabs); },
    setFocusedPaneId: (id) => calls.push(["focus", id]),
  });
  paneFork("child", "source");
  assert.deepEqual(plain(calls), [["fork", "child"], ["focus", "child"]], "the fork must update the selection and then focus the new pane");
  assert.deepEqual(plain(tabs.map((t) => t.sessionId)), ["source", "child"], "the source pane stays and the forked session opens beside it");
  assert.equal(tabs[1].projectName, "proj", "the new pane keeps the source project's attribution");

  // Classic layout / mobile: no pane is opened, the classic navigation stands.
  const classicCalls = [];
  const classicFork = runExtracted(extractAppShellCallback("handlePaneSessionForked"), {
    handleSessionForked: (id) => classicCalls.push(["fork", id]),
    splitPaneEnabled: false,
    isMobile: false,
    selectedSession: { id: "source", cwd: "/repo" },
    sessionCatalog: [],
    projectDisplayNameForPath: () => "proj",
    openPaneOp: () => { throw new Error("classic layout must not open a pane"); },
    setPaneTabs: () => { throw new Error("classic layout must not touch pane tabs"); },
    setFocusedPaneId: () => { throw new Error("classic layout must not focus a pane"); },
  });
  classicFork("child", "source");
  assert.deepEqual(plain(classicCalls), [["fork", "child"]]);

  // Behaviour: quote-in-new-chat forks the source pane's session, records the
  // prompt under the NEW session id, then opens it in a pane.
  const pending = [];
  const asked = [];
  const ask = runExtracted(extractAppShellCallback("handlePaneAskInNewChat"), {
    sendAgentCommand: async (sid, cmd) => {
      asked.push([sid, cmd]);
      return { newSessionId: "forked" };
    },
    translate: (key) => `translated:${key}`,
    setPendingQuotePrompt: (value) => pending.push(value),
    handlePaneSessionForked: (newSessionId, sourceSessionId) => asked.push(["pane-fork", newSessionId, sourceSessionId]),
  });
  await ask("quoted text", "source", "entry-7");
  assert.deepEqual(plain(asked[0]), ["source", { type: "fork_branch", entryId: "entry-7" }], "the fork must run on the pane's own session");
  assert.deepEqual(plain(pending), [{ sessionId: "forked", text: "quoted text" }], "the pending prompt belongs to the forked session");
  assert.deepEqual(plain(asked[1]), ["pane-fork", "forked", "source"], "the forked session opens in a pane");

  // A failed fork keeps the classic error and opens no pane.
  const failedPending = [];
  const failedFork = [];
  const failingAsk = runExtracted(extractAppShellCallback("handlePaneAskInNewChat"), {
    sendAgentCommand: async () => ({}),
    translate: (key) => `translated:${key}`,
    setPendingQuotePrompt: (value) => failedPending.push(value),
    handlePaneSessionForked: (...args) => failedFork.push(args),
  });
  await assert.rejects(failingAsk("quoted text", "source", "entry-7"), /translated:chat\.quoteForkFailed/);
  assert.deepEqual(plain(failedPending), [], "a failed fork records no pending prompt");
  assert.deepEqual(plain(failedFork), [], "a failed fork opens no pane");

  // Consuming a prompt clears only the pane that consumed it.
  const cleared = [];
  const consume = runExtracted(extractAppShellCallback("handlePaneInitialPromptConsumed"), {
    setPendingQuotePrompt: (updater) => cleared.push(updater({ sessionId: "a", text: "keep" })),
  });
  consume("b");
  assert.deepEqual(plain(cleared[0]), { sessionId: "a", text: "keep" }, "another pane's pending prompt is kept");
  const clearedOwn = [];
  const consumeOwn = runExtracted(extractAppShellCallback("handlePaneInitialPromptConsumed"), {
    setPendingQuotePrompt: (updater) => clearedOwn.push(updater({ sessionId: "a", text: "send" })),
  });
  consumeOwn("a");
  assert.equal(clearedOwn[0], null, "the consuming pane's pending prompt is cleared");

  // AC3: the classic window keeps its unconditional wiring.
  assert.match(classicWindow, /onSessionForked=\{handleSessionForked\}/, "the classic ChatWindow keeps the global fork handler");
  assert.match(classicWindow, /onAskInNewChat=\{handleAskInNewChat\}/, "the classic ChatWindow keeps the global quote handler");
  assert.match(classicWindow, /quoteSelectionEnabled=\{quoteSelectionEnabled\}/, "the classic quote toolbar stays enabled");

  // Guard against a test-only helper: renderPane's named handler must exist in
  // AppShell and must not be the classic one under another name.
  assert.ok(source.includes("const handlePaneSessionForked = useCallback("), "AppShell must define handlePaneSessionForked");
  assert.ok(source.includes("const handlePaneAskInNewChat = useCallback("), "AppShell must define handlePaneAskInNewChat");
  assert.ok(source.includes("const handlePaneInitialPromptConsumed = useCallback("), "AppShell must define handlePaneInitialPromptConsumed");
});

// ---- u2: the attention notification belongs to the pane that raised it ----

test("pane attention notification targets the pane session", () => {
  // Pane focus never updates selectedSession: the classic handler would notify
  // (and open) the selected chat, not the background pane waiting for input.
  assert.match(
    sessionPane,
    /onAttentionNeeded=\{\(request\) => handleAttentionNeeded\(request, paneSession\)\}/,
    "the session pane must pass its own session as the notification target",
  );
  assert.doesNotMatch(sessionPane, /onAttentionNeeded=\{handleAttentionNeeded\}/, "the pane must not reuse the selectedSession-based handler");

  const delivered = [];
  const attention = runExtracted(extractAppShellCallback("handleAttentionNeeded"), {
    selectedSession: { id: "selected", relation: "subagent" },
    isHiddenRelation: (relation) => relation === "subagent",
    shouldShowBrowserNotification: () => true,
    claimExtensionAttentionNotification: () => true,
    notifiedAttentionRequestIdsRef: { current: new Set() },
    deliverSessionNotification: (payload) => delivered.push(payload),
    translate: (key) => key,
  });
  // The selected chat is hidden, the pane is not: the pane's request still
  // notifies, and the notification opens the pane's session.
  attention({ id: "req-1", method: "custom", title: "Approve?" }, { id: "pane", relation: undefined });
  assert.equal(delivered.length, 1, "the pane's waiting input must notify");
  assert.equal(delivered[0].targetSession.id, "pane", "the notification opens the pane's own session");
  assert.equal(delivered[0].tag, "pi-extension-ui:req-1", "the global dedupe tag is unchanged");

  // The pane's own hidden relation still suppresses its notification, even
  // when the selected chat is visible: the gate follows the target.
  const hiddenDelivered = [];
  const hiddenAttention = runExtracted(extractAppShellCallback("handleAttentionNeeded"), {
    selectedSession: { id: "selected", relation: undefined },
    isHiddenRelation: (relation) => relation === "subagent",
    shouldShowBrowserNotification: () => true,
    claimExtensionAttentionNotification: () => true,
    notifiedAttentionRequestIdsRef: { current: new Set() },
    deliverSessionNotification: (payload) => hiddenDelivered.push(payload),
    translate: (key) => key,
  });
  hiddenAttention({ id: "req-2", method: "custom", title: "Approve?" }, { id: "pane", relation: "subagent" });
  assert.equal(hiddenDelivered.length, 0, "a hidden pane's session never notifies");

  // Classic layout passes no target: it keeps notifying the selected session.
  const classicDelivered = [];
  const classicAttention = runExtracted(extractAppShellCallback("handleAttentionNeeded"), {
    selectedSession: { id: "selected", relation: undefined },
    isHiddenRelation: () => false,
    shouldShowBrowserNotification: () => true,
    claimExtensionAttentionNotification: () => true,
    notifiedAttentionRequestIdsRef: { current: new Set() },
    deliverSessionNotification: (payload) => classicDelivered.push(payload),
    translate: (key) => key,
  });
  classicAttention({ id: "req-3", method: "custom", title: "Approve?" });
  assert.equal(classicDelivered[0]?.targetSession.id, "selected", "classic keeps the selected session");
});

// ---- u3: the file explorer refreshes from every pane ----

test("pane tool end and uploads refresh the explorer", () => {
  // A pane's tool calls and dropped files change the workspace the explorer
  // shows. Both handlers only bump one global refresh key and read no session,
  // so every pane shares them (owner ruling 3=A).
  assert.match(sessionPane, /onToolEnd=\{handleToolEnd\}/, "the session pane must report finished tools");
  assert.match(sessionPane, /onFilesUploaded=\{handleExplorerRefresh\}/, "the session pane must refresh after uploads");
  // The sentinel composer accepts drops too, but has no session, so it gets no
  // onToolEnd. It is not a reporting callback, so the stats suite's sentinel
  // negatives still hold.
  assert.match(sentinel, /onFilesUploaded=\{handleExplorerRefresh\}/, "the new-session composer must refresh after uploads");
  assert.doesNotMatch(sentinel, /onToolEnd=/, "the sentinel has no session to run tools");

  for (const name of ["handleToolEnd", "handleExplorerRefresh"]) {
    const body = extractAppShellCallback(name);
    assert.ok(!body.includes("selectedSession"), `${name} must stay session-independent so every pane can share it`);
  }

  assert.match(classicWindow, /onToolEnd=\{handleToolEnd\}/, "the classic window keeps reporting tools");
  assert.match(classicWindow, /onFilesUploaded=\{handleExplorerRefresh\}/, "the classic window keeps refreshing after uploads");
});

// ---- u4: the completion sound crosses panes exactly once ----

test("pane sound controls: every pane unlocks and holds, only the focused pane chimes", () => {
  // Sound state and its controls are page-global: the toggle and the audio
  // unlock/hold must reach every pane so a background pane's run can hold the
  // context open. Only the chime itself is focused-gated: the focused pane's
  // ChatWindow plays it, an unfocused pane still rings through its own
  // onAgentEnd coalescing (owner ruling 4=A: keep that debounce).
  assert.match(sessionPane, /soundEnabled=\{soundEnabled\}/, "the pane follows the page's sound switch");
  assert.match(sessionPane, /onSoundToggle=\{onSoundToggle\}/, "the pane's composer keeps the sound toggle");
  assert.match(sessionPane, /unlockAudio=\{unlockAudio\}/, "every pane can unlock audio from its own gesture");
  assert.match(sessionPane, /holdAudio=\{holdAudio\}/, "every pane holds the audio context while its run is active");
  assert.match(sessionPane, /playDoneSound=\{focused \? playDoneSound : undefined\}/, "only the focused pane plays the completion chime");
  assert.doesNotMatch(sessionPane, /playDoneSound=\{playDoneSound\}/, "an unfocused pane must not chime twice");
  // The sentinel has no run and no completion sound.
  assert.doesNotMatch(sentinel, /playDoneSound=|onSoundToggle=|soundEnabled=|unlockAudio=|holdAudio=/, "the new-session composer owns no run sound");

  // The classic window keeps all five unconditionally.
  assert.match(classicWindow, /soundEnabled=\{soundEnabled\}/);
  assert.match(classicWindow, /onSoundToggle=\{onSoundToggle\}/);
  assert.match(classicWindow, /playDoneSound=\{playDoneSound\}/);
  assert.match(classicWindow, /unlockAudio=\{unlockAudio\}/);
  assert.match(classicWindow, /holdAudio=\{holdAudio\}/);

  // The client-platform document states the split behaviour beside the
  // existing completion-sound rules.
  assert.match(clientPlatform, /only the focused pane plays the completion chime/, "client-platform.md must name the split rule");
  assert.match(clientPlatform, /every pane holds and unlocks the audio context/, "client-platform.md must name the per-pane hold");
});
