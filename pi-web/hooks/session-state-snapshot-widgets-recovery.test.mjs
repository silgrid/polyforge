import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// The same stale-snapshot order as session-state-snapshot-widgets.test.mjs, at
// the two recovery reads that also replace a wrapper's widget list:
// reconciliation's idle reply (useAgentSession's 15 s running poll) and the
// `agent_end` read of GET /api/agent/<id>. Both were unconditional whole-list
// replacements; a setWidget the stream delivered while either read was in
// flight must survive them.
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { useAgentSession } = await jiti.import("./useAgentSession.ts");
const { deleteSessionViewSnapshot } = await jiti.import("@/lib/session-view-cache");

globalThis.document ??= Object.assign(new EventTarget(), { visibilityState: "visible" });
globalThis.window ??= new EventTarget();
globalThis.requestAnimationFrame ??= () => 0;
globalThis.cancelAnimationFrame ??= () => {};

const RECONCILE_MS = 15_000;
const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };
const earlier = { role: "user", content: "earlier", timestamp: 1 };
const widget = { key: "polyforge", lines: ["E2E-PF-SENTINEL pi#111 fix running"] };

async function setup() {
  mock.timers.enable({ apis: ["setTimeout", "setInterval", "Date"] });
  const sources = [];
  const held = {
    status: { body: { running: false, state: { extensionWidgets: [] } }, requested: Promise.withResolvers(), release: Promise.withResolvers(), active: false },
    agentEnd: { body: { state: { extensionWidgets: [] } }, requested: Promise.withResolvers(), release: Promise.withResolvers(), active: false },
  };
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
    const url = requestUrl;
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
      return json({ running: true, state: { isStreaming: true, isPromptRunning: true, extensionWidgets: [] } });
    }
    if (url === "/api/agent/A?systemPrompt=0") {
      if (held.status.active) {
        held.status.requested.resolve();
        await held.status.release.promise;
        return json(held.status.body);
      }
      return json({ running: true, state: { isStreaming: true, isPromptRunning: true } });
    }
    if (url === "/api/agent/A") {
      if (held.agentEnd.active) {
        held.agentEnd.requested.resolve();
        await held.agentEnd.release.promise;
        return json(held.agentEnd.body);
      }
      return json({});
    }
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
  for (const source of sources) if (source.readyState === 0) source.emit({ type: "connected", sessionId: "A", isStreaming: false });
  await settle();
  hook.flush();
  const emit = async (event) => {
    sources.filter((source) => source.readyState === 1).at(-1)?.emit(event);
    await settle();
    hook.flush();
  };
  const startRun = async () => {
    await emit({ type: "agent_start" });
    assert.equal(hook.result.agentRunning, true, "the run is active");
  };
  const tick = async (ms) => {
    mock.timers.tick(ms);
    await settle();
    hook.flush();
  };
  const teardown = () => {
    hook.unmount();
    for (const source of sources) source.close();
    mock.timers.reset();
    deleteSessionViewSnapshot("A");
  };
  return { hook, emit, held, startRun, tick, teardown };
}

async function withSetup(body) {
  const originalError = console.error;
  console.error = () => {};
  const env = await setup();
  try {
    await body(env);
  } finally {
    console.error = originalError;
    env.held.status.release.resolve();
    env.held.agentEnd.release.resolve();
    await settle();
    env.teardown();
  }
}

const widgetKeys = (env) => env.hook.result.extensionWidgets.map((item) => item.key);

test("the reconcile poll's stale widget section does not erase a setWidget that arrived while it read", async () => {
  await withSetup(async (env) => {
    await env.startRun();
    env.held.status.active = true;
    await env.tick(RECONCILE_MS);
    await env.held.status.requested.promise;

    await env.emit({ type: "extension_ui_request", id: "w1", method: "setWidget", widgetKey: widget.key, widgetLines: widget.lines });
    assert.deepEqual(widgetKeys(env), ["polyforge"], "the live widget is shown while the poll's reply is in flight");

    env.held.status.release.resolve();
    await settle();
    env.hook.flush();

    assert.deepEqual(widgetKeys(env), ["polyforge"], "a stale reconciliation reply must not erase a newer widget event");
    assert.deepEqual(env.hook.result.extensionWidgets[0]?.lines, widget.lines);
  });
});

test("the agent_end read's stale widget section does not erase a setWidget that arrived while it read", async () => {
  await withSetup(async (env) => {
    await env.startRun();
    env.held.agentEnd.active = true;
    await env.emit({ type: "agent_end" });
    await env.held.agentEnd.requested.promise;

    await env.emit({ type: "extension_ui_request", id: "w1", method: "setWidget", widgetKey: widget.key, widgetLines: widget.lines });
    assert.deepEqual(widgetKeys(env), ["polyforge"], "the live widget is shown while the agent_end reply is in flight");

    env.held.agentEnd.release.resolve();
    await settle();
    env.hook.flush();

    assert.deepEqual(widgetKeys(env), ["polyforge"], "a stale agent_end reply must not erase a newer widget event");
    assert.deepEqual(env.hook.result.extensionWidgets[0]?.lines, widget.lines);
  });
});
