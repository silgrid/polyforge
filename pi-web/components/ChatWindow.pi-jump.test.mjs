// carries: components/ChatWindow.tsx, lib/pi-subagent-panel.ts
// polyforge#255 round 3 #1: the paginated Jump search itself is executed from
// ChatWindow's source (TS-AST), not matched as text: an unmounted launch call
// pages the existing history until it is found (or the history ends), and the
// panel is only closed when `found`.
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { Script } from "node:vm";
import ts from "typescript";

const source = await readFile(new URL("./ChatWindow.tsx", import.meta.url), "utf8");
const ast = ts.createSourceFile("ChatWindow.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
const plain = (value) => JSON.parse(JSON.stringify(value));
function run(text, sandbox) {
  return new Script(ts.transpileModule(`(${text})`, { compilerOptions: { target: ts.ScriptTarget.ES2020 } }).outputText).runInNewContext(sandbox);
}
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
const assistant = (text, toolCallId) => toolCallId
  ? { role: "assistant", content: [{ type: "toolCall", toolCallId, toolName: "Agent", input: {} }] }
  : { role: "assistant", content: [{ type: "text", text }] };

test("polyforge#255 r3: the Jump search pages until the launch call is found, then reports found", async () => {
  const effect = arrow("if (!piSubagentPanelOpen)");
  const seen = [];
  const pages = [
    { messages: [assistant("x", "other")], entryIds: ["e-40"], hasMore: true, oldestEntryId: "e-40" },
    { messages: [assistant("x", "call-launch")], entryIds: ["e-1"], hasMore: true, oldestEntryId: "e-1" },
  ];
  const sandbox = {
    piSubagentPanelOpen: true,
    piSubagentPanel: { launchToolCallId: "call-launch" },
    session: { id: "parent-1" },
    piJumpHistoryRef: { current: { messages: [], entryIds: [], historyCursor: "e-50", hasEarlierMessages: true } },
    setPiJump: (value) => seen.push(value),
    loadOlderPage: async () => pages.shift() ?? null,
    loadingOlderRef: { current: false },
    scrollContainerRef: { current: null },
    prevScrollAnchorRef: { current: null },
    captureScrollAnchor: () => null,
    findPiSubagentLaunchEntryId: (toolCallId, messages, entryIds) => {
      for (let i = 0; i < messages.length; i += 1) {
        if ((messages[i].content ?? []).some((block) => block.toolCallId === toolCallId)) return entryIds[i];
      }
      return null;
    },
  };
  const cleanup = run(effect, sandbox)();
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  const states = seen.map((item) => item.status);
  assert.ok(states.includes("searching"), "the search reports searching while it pages");
  assert.deepEqual(plain(seen.at(-1)), { status: "found", entryId: "e-1" }, "the launch call found on an older page is the target");
  assert.equal(typeof cleanup, "function", "the effect returns its cancel handle");
});

test("polyforge#255 r3: a history searched to its end reports missing; the panel is not closed by a missing Jump", async () => {
  const effect = arrow("if (!piSubagentPanelOpen)");
  const seen = [];
  run(effect, {
    piSubagentPanelOpen: true,
    piSubagentPanel: { launchToolCallId: "call-launch" },
    session: { id: "parent-1" },
    piJumpHistoryRef: { current: { messages: [], entryIds: [], historyCursor: "e-50", hasEarlierMessages: true } },
    setPiJump: (value) => seen.push(value),
    loadOlderPage: async () => ({ messages: [assistant("x")], entryIds: ["e-1"], hasMore: false, oldestEntryId: "e-1" }),
    findPiSubagentLaunchEntryId: () => null,
    loadingOlderRef: { current: false },
    scrollContainerRef: { current: null },
    prevScrollAnchorRef: { current: null },
    captureScrollAnchor: () => null,
  })();
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(plain(seen.at(-1)), { status: "missing", entryId: null }, "an exhausted history reports missing");

  const jump = arrow("if (piJump.status !== \"found\" || !piJump.entryId) return;");
  const calls = [];
  run(jump, {
    piJump: { status: "missing", entryId: null },
    piSubagentPanel: { launchToolCallId: "call-launch" },
    setOpenPanelChoice: (value) => calls.push(["close", value]),
    scrollContainerRef: { current: null },
    highlightJumpTarget: () => {},
    setVisibleCount: () => {},
    setPendingPiSubagentJump: () => {},
  })();
  assert.deepEqual(plain(calls), [], "a missing Jump leaves the panel open");
  run(jump, {
    piJump: { status: "found", entryId: "e-1" },
    piSubagentPanel: { launchToolCallId: "call-launch" },
    setOpenPanelChoice: (value) => calls.push(["close", value]),
    scrollContainerRef: { current: null },
    highlightJumpTarget: () => {},
    setVisibleCount: () => {},
    setPendingPiSubagentJump: () => {},
  })();
  assert.deepEqual(plain(calls), [["close", null]], "a found Jump closes the panel before scrolling");
});

// polyforge#275: the turn's process loop itself is executed from ChatWindow's
// source (TS-AST), its helpers the real lib implementations. A turn that settles
// before the reload brings its entry ids has live steps with no entry id; with no
// Jump pending, nothing may force its answered fold open (claude-session-219 S3).
const { createJiti } = await import("jiti");
const jiti = createJiti(import.meta.url);
const messageDisplay = await jiti.import("../lib/message-display.ts");
const subagentView = await jiti.import("../lib/claude-subagent-view.ts");
const transcriptNotices = await jiti.import("../lib/transcript-notices.ts");
const turnViews = await jiti.import("../lib/turn-views.ts");
const processLoop = (() => {
  const start = source.indexOf("const processViews: ReactNode[] = [];");
  assert.ok(start >= 0, "ChatWindow has no process-views loop");
  const forStart = source.indexOf("for (let processIdx = userIdx + 1;", start);
  let loop = null;
  const walk = (node) => {
    if (ts.isForStatement(node) && node.getStart(ast) === forStart) loop = node;
    ts.forEachChild(node, walk);
  };
  walk(ast);
  assert.ok(loop, "ChatWindow has no process loop over the turn's steps");
  const body = `${source.slice(start, loop.getEnd())}\nreturn { revealProcess, processMessageCount, processToolCount };`;
  return new Script(ts.transpileModule(`(() => {${body}})()`, { compilerOptions: { target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.React } }).outputText);
})();
function runProcessLoop({ messages, entryIds, pendingPiSubagentJump = null, pendingSubagentJump = null }) {
  const finalAssistantIdx = messages.length - 1;
  const finalViews = turnViews.getFinalAnswerViews(new WeakMap(), messages[finalAssistantIdx]);
  const result = processLoop.runInNewContext({
    messages, entryIds, userIdx: 0, finalAssistantIdx, finalViews, finalAnswerMessage: finalViews.answer,
    pendingSearchScroll: null, searchBlock: null, pendingSubagentJump, pendingPiSubagentJump,
    claudeSubagents: {}, visibleRefIndexByMessage: new Map(), messageKeys: messages.map((_, index) => `k${index}`),
    renderMessage: () => ({}), React: { createElement: () => ({}) }, TranscriptNoticeLines: () => null,
    isHiddenCustomMessage: messageDisplay.isHiddenCustomMessage,
    getDisplayableAssistantBlocks: messageDisplay.getDisplayableAssistantBlocks,
    countToolCallBlocks: messageDisplay.countToolCallBlocks,
    getTranscriptNotices: transcriptNotices.getTranscriptNotices,
    isRunningBackgroundAgentMessage: subagentView.isRunningBackgroundAgentMessage,
    isSubagentJumpTarget: subagentView.isSubagentJumpTarget,
    Boolean,
  });
  return { ...plain(result), answered: messageDisplay.collapsesProcessDetails(finalViews.answer) };
}
// The S3 turn: the user's message, a step with streamed text and a Read call, its
// result, then the final reply. One process step, one tool call (counted by hand).
const settledTurn = [
  { role: "user", content: "E2E second message" },
  { role: "assistant", content: [{ type: "text", text: "E2E streamed reply" }, { type: "toolCall", id: "t1", name: "Read", arguments: { file_path: "e2e-probe.txt" } }] },
  { role: "toolResult", toolCallId: "t1", toolName: "Read", content: [{ type: "text", text: "E2E tool output" }] },
  { role: "assistant", content: [{ type: "text", text: "E2E final reply" }] },
];
const reloadedEntryIds = ["e1", "e2", "e3", "e4"];

test("polyforge#275: a settled turn folds while its steps still lack entry ids, and after the reload brings them", () => {
  const live = runProcessLoop({ messages: settledTurn, entryIds: [] });
  assert.equal(live.revealProcess, false, "with no Jump pending, a settled live turn (no entry ids yet) must not force its process details open");
  assert.equal(live.processMessageCount, 1);
  assert.equal(live.processToolCount, 1);
  assert.equal(live.answered, true, "the answered turn folds by default");
  const reloaded = runProcessLoop({ messages: settledTurn, entryIds: reloadedEntryIds });
  assert.equal(reloaded.revealProcess, false, "after the reload, with no Jump pending, the fold stays closed");
  assert.equal(reloaded.processMessageCount, 1);
  assert.equal(reloaded.processToolCount, 1);
});

test("polyforge#275: a pi Jump still reveals the step it targets, and only that step", () => {
  const jump = { toolCallId: "t1", entryId: "e2" };
  assert.equal(runProcessLoop({ messages: settledTurn, entryIds: reloadedEntryIds, pendingPiSubagentJump: jump }).revealProcess, true, "a pi Jump to the launch step reveals it");
  assert.equal(runProcessLoop({ messages: settledTurn, entryIds: [], pendingPiSubagentJump: jump }).revealProcess, false, "a step with no entry id is no Jump target");
  assert.equal(runProcessLoop({ messages: settledTurn, entryIds: reloadedEntryIds, pendingPiSubagentJump: { toolCallId: "t9", entryId: "e9" } }).revealProcess, false, "a Jump elsewhere does not reveal this turn");
  assert.equal(runProcessLoop({ messages: settledTurn, entryIds: reloadedEntryIds, pendingSubagentJump: { entryId: "e2" } }).revealProcess, true, "a claude Jump to the launch step reveals it");
});

test("polyforge#275: a turn with only its final answer has no process steps to fold", () => {
  const result = runProcessLoop({ messages: [settledTurn[0], settledTurn[3]], entryIds: [] });
  assert.equal(result.processMessageCount, 0);
  assert.equal(result.revealProcess, false);
});
