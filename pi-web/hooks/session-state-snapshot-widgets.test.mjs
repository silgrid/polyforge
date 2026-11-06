import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// A mount's snapshot read (`GET /api/sessions/<id>/state`, taken before the
// extension's `setWidget` reached the wrapper) can land after the event stream
// delivered that widget. The read's widget section is then stale: applying it
// erases a widget the user already sees, and on the mobile layout the whole
// extension shelf disappears (e2e/extension-widgets-169.mjs S6). The
// delayed `/state` reply here reproduces that landing order deterministically.
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

async function setup() {
  mock.timers.enable({ apis: ["setTimeout", "setInterval", "Date"] });
  const sources = [];
  const stateRequestBody = { running: false, state: { extensionWidgets: [] } };
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
      // A snapshot read in flight: what it returns was read now, but it lands
      // only after the widget event below.
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

test("a /state snapshot that lands after setWidget keeps the widget it did not see", async () => {
  await withSetup(async (env) => {
    // The widget event reaches the page while the mount's state read is in flight.
    await env.stateRequested.promise;
    await env.emit({
      type: "extension_ui_request",
      id: "w1",
      method: "setWidget",
      widgetKey: "polyforge",
      widgetLines: ["E2E-PF-SENTINEL pi#111 fix running"],
    });
    assert.deepEqual(env.hook.result.extensionWidgets.map((widget) => widget.key), ["polyforge"], "the live widget is shown");

    // The delayed snapshot holds what the wrapper had when the read started:
    // no widget at all.
    env.stateLanded.resolve();
    await settle();
    env.hook.flush();

    assert.deepEqual(
      env.hook.result.extensionWidgets.map((widget) => widget.key),
      ["polyforge"],
      "the stale snapshot's widget section must not erase a widget set after it was read",
    );
    assert.deepEqual(env.hook.result.extensionWidgets[0]?.lines, ["E2E-PF-SENTINEL pi#111 fix running"]);
  });
});