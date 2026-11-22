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
  const effectText = extractEffectContaining("onSystemToolsChange?.(loadedToolsRef.current)");
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
