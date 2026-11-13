import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { Script, createContext } from "node:vm";
import ts from "typescript";

// The sidebar's watcher poll reports a write for the selected session every
// 2.5 s, including pi-web's own run appends. On a healthy stream the SSE path
// has already delivered them, so refreshFromDisk must not turn every rise into
// a full snapshot read; when the stream is half-dead it must, or the run's
// completed messages stay hidden. A background read is quiet on failure.
const source = ts.createSourceFile(
  "useAgentSession.ts",
  await readFile(new URL("./useAgentSession.ts", import.meta.url), "utf8"),
  ts.ScriptTarget.Latest,
  true,
);
const nodes = [];
function visit(node) { nodes.push(node); ts.forEachChild(node, visit); }
visit(source);
const declaration = nodes.find((node) => ts.isVariableDeclaration(node) && node.name.getText(source) === "refreshFromDisk");
assert.ok(declaration, "missing refreshFromDisk");
const callbackSource = ts.transpileModule(`(${declaration.initializer.arguments[0].getText(source)})`, {
  compilerOptions: { target: ts.ScriptTarget.ESNext },
}).outputText;

function setup({ running = false, compacting = false, content = 0, delivered = 0 } = {}) {
  const reads = [];
  const context = createContext({
    sessionHookMountedRef: { current: true },
    sessionIdRef: { current: "s1" },
    agentRunningRef: { current: running },
    bashRunningRef: { current: false },
    isCompacting: compacting,
    promptRunIdRef: { current: 7 },
    runContentEventsRef: { current: content },
    externalWriteContentEventsRef: { current: delivered },
    loadSession: (...args) => { reads.push(args); return Promise.resolve(null); },
  });
  context.refreshFromDisk = new Script(callbackSource).runInContext(context);
  return { context, reads };
}

test("an idle session reads its snapshot, quietly", async () => {
  const state = setup();
  await state.context.refreshFromDisk("s1");
  assert.equal(state.reads.length, 1);
  const [sid, showLoading, includeState, options] = state.reads[0];
  assert.deepEqual([sid, showLoading, includeState, options.force, options.quiet, options.expectRunId], ["s1", false, false, true, true, 7]);
});

test("a healthy run's own write is not read again: the stream delivered it", async () => {
  const state = setup({ running: true, content: 3, delivered: 2 });
  await state.context.refreshFromDisk("s1");
  assert.deepEqual(state.reads, []);
  assert.equal(state.context.externalWriteContentEventsRef.current, 3);
});

test("a run with no delivered message reads, keeping its run id and failing quietly", async () => {
  const state = setup({ running: true, content: 2, delivered: 2 });
  await state.context.refreshFromDisk("s1");
  assert.equal(state.reads.length, 1);
  const [sid, showLoading, includeState, options] = state.reads[0];
  assert.deepEqual([sid, showLoading, includeState, options.force, options.quiet, options.expectRunId], ["s1", false, false, true, true, 7]);
});

test("a run under compaction or a ! command follows the same rule", async () => {
  const compacting = setup({ compacting: true, content: 0, delivered: 0 });
  await compacting.context.refreshFromDisk("s1");
  assert.equal(compacting.reads.length, 1);
  assert.equal(compacting.reads[0][3].expectRunId, 7);

  const bash = setup({ content: 4, delivered: 4 });
  bash.context.bashRunningRef.current = true;
  await bash.context.refreshFromDisk("s1");
  assert.equal(bash.reads.length, 1);
});

test("a different or unmounted session is never read", async () => {
  const state = setup();
  await state.context.refreshFromDisk("other");
  state.context.sessionHookMountedRef.current = false;
  await state.context.refreshFromDisk("s1");
  assert.deepEqual(state.reads, []);
});
