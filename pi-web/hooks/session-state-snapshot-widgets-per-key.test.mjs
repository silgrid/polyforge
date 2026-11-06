import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// The same landing order as session-state-snapshot-widgets.test.mjs, with a
// reply that is stale for one key only: the mount's `/state` snapshot was read
// before the stream's `setWidget("polyforge")`, but its `usage` widget was not
// touched by the stream at all. Dropping the whole widget section would lose
// `usage`, which the reply still holds; only the touched key is stale
// (code review).
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { useAgentSession } = await jiti.import("./useAgentSession.ts");
const { deleteSessionViewSnapshot } = await jiti.import("@/lib/session-view-cache");

globalThis.document ??= Object.assign(new EventTarget(), { visibilityState: "visible" });
globalThis.window ??= new EventTarget();
globalThis.requestAnimationFrame ??= () => 0;
globalThis.cancelAnimationFrame ??= () => {};

const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };
const earlier = { role: "user", content: "earlier", timestamp: 1 };
const usage = { key: "usage", lines: ["usage 42%"], placement: "aboveEditor" };
const polyforge = { key: "polyforge", lines: ["E2E-PF-SENTINEL pi#111 fix running"], placement: "aboveEditor" };

async function setup() {
  mock.timers.enable({ apis: ["setTimeout", "setInterval", "Date"] });
  const sources = [];
  const stateRequestBody = { running: false, state: { extensionWidgets: [usage] } };
  const stateLanded = Promise.withResolvers();
  const stateRequested = Promise.withResolvers();
  globalThis.EventSource = class {
    readyState = 0;
    onmessage = null;
    onerror = null;
    constructor(url) { this.url = url; sources.push(this); }
    close() { this.readyState = 2; }
    emit(event) {
      this.readyState = 1;
      this.onmessage?.({ data: JSON.stringify(event) });
    }
  };
  globalThis.fetch = async (requestUrl) => {
    const url = requestUrl.replace("?systemPrompt=0", "");
    if (url.startsWith("/api/models")) return json({ models: {}, modelList: [] });
    if (url.startsWith("/api/sessions/A/context?")) return json({ context: { messages: [earlier], entryIds: ["e1"], oldestEntryId: null, hasMore: false } });
    if (url.startsWith("/api/sessions/A?")) {
      return json({
        sessionId: "A",
        filePath: "/A.jsonl",
        leafId: null,
        tree: [],
        treeFormat: "summary",
        snapshotRevision: "r:e1",
        context: { messages: [earlier], entryIds: ["e1"], oldestEntryId: null, hasMore: false },
      });
    }
    if (url === "/api/sessions/A/state") {
      // A snapshot read in flight: it holds the `usage` widget, but the stream
      // below sets `polyforge` before this reply lands.
      stateRequested.resolve();
      await stateLanded.promise;
      return json(stateRequestBody);
    }
    if (url === "/api/agent/A") return json({});
    return json({});
  };
  const hook = renderHook(useAgentSession, {
    session: { id: "A", cwd: "/project", path: "/A.jsonl", name: "A" },
    newSessionCwd: null,
    newSessionDraftKey: null,
    t: (key) => key,
  });
  await settle();
  hook.flush();
  const source = sources.filter((item) => item.readyState === 0).at(-1);
  source?.emit({ type: "connected", sessionId: "A", isStreaming: false });
  await settle();
  hook.flush();
  const emit = async (event) => {
    sources.filter((item) => item.readyState === 1).at(-1)?.emit(event);
    await settle();
    hook.flush();
  };
  const teardown = () => {
    hook.unmount();
    for (const item of sources) item.close();
    mock.timers.reset();
    deleteSessionViewSnapshot("A");
  };
  return { hook, emit, stateRequested, stateLanded, teardown };
}

async function withSetup(body) {
  const originalError = console.error;
  console.error = () => {};
  const env = await setup();
  try {
    await body(env);
  } finally {
    console.error = originalError;
    env.stateLanded.resolve();
    await settle();
    env.teardown();
  }
}

test("a stale /state snapshot keeps untouched widget keys and only the touched one from the stream", async () => {
  await withSetup(async (env) => {
    await env.stateRequested.promise;
    await env.emit({
      type: "extension_ui_request",
      id: "w1",
      method: "setWidget",
      widgetKey: polyforge.key,
      widgetLines: polyforge.lines,
    });
    assert.deepEqual(env.hook.result.extensionWidgets.map((widget) => widget.key), ["polyforge"], "the live widget is shown");

    // The delayed snapshot predates only the polyforge event; its usage entry
    // is not stale and must still apply.
    env.stateLanded.resolve();
    await settle();
    env.hook.flush();

    const widgets = new Map(env.hook.result.extensionWidgets.map((widget) => [widget.key, widget]));
    assert.deepEqual([...widgets.keys()].sort(), ["polyforge", "usage"], "both the stream's widget and the snapshot's untouched widget are shown");
    assert.deepEqual(widgets.get("polyforge")?.lines, polyforge.lines, "the stale snapshot did not replace the touched key");
    assert.deepEqual(widgets.get("usage")?.lines, usage.lines, "the snapshot applied the key the stream never touched");
  });
});