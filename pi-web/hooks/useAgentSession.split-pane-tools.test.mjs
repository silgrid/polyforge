// carries: hooks/useAgentSession*, components/ChatWindow*, components/AppShell*
// polyforge#231 u5 (owner ruling 5=A): split view makes onSystemToolsChange
// flip between the focused pane's handler and undefined. useAgentSession used
// to report tools only from loadTools, which never replays them: after a focus
// switch the top bar kept the previous pane's tools (or stayed empty). The
// callback now re-reports this hook's last loaded tools when its identity
// arrives and nulls when it is removed, the pattern the loader and the debug
// reader already use.
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { Script } from "node:vm";
import ts from "typescript";

const source = await readFile(new URL("./useAgentSession.ts", import.meta.url), "utf8");
const appShell = await readFile(new URL("../components/AppShell.tsx", import.meta.url), "utf8");
const sessionsDoc = await readFile(new URL("../docs/agents/sessions.md", import.meta.url), "utf8");
const ast = ts.createSourceFile("useAgentSession.ts", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);

const plain = (value) => JSON.parse(JSON.stringify(value));

/** The first `useEffect(() => { ... })` whose callback text contains `marker`. */
function extractEffectContaining(marker) {
  let found = null;
  function find(node) {
    if (found) return;
    if (
      ts.isCallExpression(node)
      && node.expression.getText(ast) === "useEffect"
      && node.arguments.length > 0
      && node.arguments[0].getText(ast).includes(marker)
    ) {
      found = node.arguments[0].getText(ast);
      return;
    }
    ts.forEachChild(node, find);
  }
  find(ast);
  assert.ok(found, `useAgentSession must register the effect containing ${JSON.stringify(marker)}`);
  return found;
}

function runExtracted(callbackText, sandbox) {
  return new Script(ts.transpileModule(`(${callbackText})`, {
    compilerOptions: { target: ts.ScriptTarget.ES2020 },
  }).outputText).runInNewContext(sandbox);
}

test("the tools callback re-reports on change and clears on removal", () => {
  const effectText = extractEffectContaining("onSystemToolsChange(loadedToolsRef.current)");
  const tools = [{ name: "read" }, { name: "bash" }];
  const reported = [];
  const effect = runExtracted(effectText, {
    onSystemToolsChange: (value) => reported.push(value),
    loadedToolsRef: { current: tools },
  });
  const cleanup = effect();
  // Arriving (a pane gaining focus): this hook's own tools are reported at
  // once, before any reload, so the top bar shows B's tools, never A's.
  assert.deepEqual(plain(reported), [plain(tools)], "a new callback gets this chat's current tools immediately");
  // Losing the callback (focus moved away): the global value is cleared.
  cleanup();
  assert.equal(reported.at(-1), null, "removing the callback clears the reported tools");

  // An absent callback is the classic/unfocused state: the effect is inert.
  const inert = runExtracted(effectText, {
    onSystemToolsChange: undefined,
    loadedToolsRef: { current: tools },
  });
  assert.doesNotThrow(() => inert(), "no callback must not throw");
  assert.equal(inert(), undefined, "no callback registers no cleanup");

  // loadTools still reports the freshly loaded tools and stores them for the
  // re-report above.
  assert.match(source, /loadedToolsRef\.current = tools;/, "loadTools must remember the tools it loaded");
  assert.match(source, /onSystemToolsChange\?\.\(tools\);/, "loadTools must still report the freshly loaded tools");
});

// ---- u7 (review r1#1): losing the focused pane clears its top-bar projections ----

test("the top-bar projections clear when the focused pane loses them", () => {
  // Focus can move to the new-session sentinel, which passes none of the
  // page-global callbacks. Unless the effects that report the branch tree and
  // the system prompt clean up on removal, the top bar keeps the previous
  // pane's tree and its onLeafChange closure, and picking a branch there sends
  // navigate_tree to that (now unfocused) session (review r1#1).
  const promptEffectText = extractEffectContaining("onSystemPromptChange?.(null)");
  const promptReports = [];
  const promptEffect = runExtracted(promptEffectText, {
    onSystemPromptChange: (value) => promptReports.push(value),
  });
  const promptCleanup = promptEffect();
  assert.equal(typeof promptCleanup, "function", "the prompt cleanup must be registered while a callback is held");
  promptCleanup();
  assert.equal(promptReports.at(-1), null, "losing the prompt callback clears the top bar's prompt");
  const inertPrompt = runExtracted(promptEffectText, { onSystemPromptChange: undefined });
  assert.equal(inertPrompt(), undefined, "no callback registers no prompt cleanup");

  const branchEffectText = extractEffectContaining("onBranchDataChange([], null");
  const branchReports = [];
  const branchEffect = runExtracted(branchEffectText, {
    onBranchDataChange: (...args) => branchReports.push(args),
  });
  const branchCleanup = branchEffect();
  assert.equal(typeof branchCleanup, "function", "the branch cleanup must be registered while a callback is held");
  branchCleanup();
  const [tree, leaf, onLeaf, locked] = branchReports.at(-1);
  assert.deepEqual(plain(tree), [], "losing the branch callback empties the top bar's tree");
  assert.equal(leaf, null, "and its active leaf");
  assert.equal(typeof onLeaf, "function", "and it leaves a callable no-op instead of the old pane's handleLeafChange");
  assert.doesNotThrow(() => onLeaf("leaf-1"), "a stale branch click must be a no-op");
  assert.equal(locked, false, "and the switch is not reported as locked");
  const inertBranch = runExtracted(branchEffectText, { onBranchDataChange: undefined });
  assert.equal(inertBranch(), undefined, "no callback registers no branch cleanup");

  // The normal reporting effects stay exactly as they were.
  assert.match(source, /onSystemPromptChange\?\.\(systemPrompt\);/, "a prompt change still reports the current prompt");
  assert.match(
    source,
    /onBranchDataChange\(data\?\.tree \?\? \[\], activeLeafId, handleLeafChange, branchSwitchLocked\);/,
    "branch data changes still report the live tree",
  );

  // AC2: the written ruling must state the cleanup that now exists.
  const commentStart = appShell.indexOf("// polyforge#231: branch, system prompt/tools");
  assert.ok(commentStart >= 0, "renderPane keeps the top-bar ruling comment");
  const comment = appShell.slice(commentStart, commentStart + 800);
  assert.match(comment, /empty tree/, "the comment must name the branch cleanup");
  const tableStart = sessionsDoc.indexOf("## Split view: pane callback ownership");
  assert.ok(tableStart >= 0, "sessions.md keeps the ownership table");
  const table = sessionsDoc.slice(tableStart, sessionsDoc.indexOf("\n## ", tableStart + 3));
  assert.match(table, /`onBranchDataChange`[^\n]*清空/, "the table's branch row must state the clear-on-blur");
  assert.match(table, /`onSystemPromptChange`[^\n]*清空/, "the table's prompt row must state the clear-on-blur");
});
