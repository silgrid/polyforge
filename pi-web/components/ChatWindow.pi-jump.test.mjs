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
