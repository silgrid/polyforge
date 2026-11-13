import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// On the weak network of #153 the event stream can stall and then resume. While
// it is stalled the sidebar's watcher signal forces a snapshot read in the
// middle of the run; when the stream resumes it delivers the buffered
// message_end events for messages that read already showed. They must not
// render those messages a second time (the prompt would land after the reply),
// and a partial streaming bubble must end when the snapshot holds its message.
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
const prompt = { role: "user", content: "first submission", timestamp: 10 };
const reply = (text, timestamp) => ({ role: "assistant", content: [{ type: "text", text }], model: "m", provider: "p", timestamp });
const isPrompt = (message) => message.role === "user" && message.content === "first submission";

async function setup({ initial = { messages: [earlier], entryIds: ["e1"] } } = {}) {
  mock.timers.enable({ apis: ["setTimeout", "setInterval", "Date"] });
  const sources = [];
  const server = {
    context: initial,
    response: Promise.withResolvers(),
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
  globalThis.fetch = async (requestUrl, init = {}) => {
    const url = requestUrl.replace("?systemPrompt=0", "");
    if (url.startsWith("/api/models")) return json({ models: {}, modelList: [] });
    if (url.startsWith("/api/sessions/A/context?")) return json({ context: { ...server.context, oldestEntryId: null, hasMore: false } });
    if (url.startsWith("/api/sessions/A?")) {
      const snapshotRevision = `r:${server.context.entryIds.join(",")}`;
      return json({
        sessionId: "A",
        filePath: "/A.jsonl",
        leafId: null,
        tree: [],
        treeFormat: "summary",
        snapshotRevision,
        context: { ...server.context, oldestEntryId: null, hasMore: false },
      });
    }
    if (url === "/api/sessions/A/state") return json({ running: true, state: { isStreaming: true, isPromptRunning: true } });
    if (url === "/api/agent/A" && init.body) {
      await server.response.promise;
      return json({ success: true, data: null });
    }
    if (url === "/api/agent/A") return json({ running: true, state: { isStreaming: true, isPromptRunning: true } });
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
  // The run is in flight: its prompt request has not answered yet, and the
  // sidebar's next watcher signal is what forces a read mid-run.
  const sending = hook.result.handleSend("first submission");
  await settle();
  hook.flush();
  assert.equal(hook.result.agentRunning, true, "the run is active");
  const readMidRun = async (context) => {
    server.context = context;
    await hook.result.refreshFromDisk("A");
    await settle();
    hook.flush();
  };
  const teardown = () => {
    hook.unmount();
    for (const source of sources) source.close();
    mock.timers.reset();
    deleteSessionViewSnapshot("A");
  };
  return { hook, emit, readMidRun, teardown, sending, server };
}

async function withSetup(body, options) {
  const originalError = console.error;
  console.error = () => {};
  const env = await setup(options);
  try {
    await body(env);
  } finally {
    console.error = originalError;
    env.server.response.resolve();
    await env.sending;
    env.teardown();
  }
}

test("a mid-run read, then the buffered message_end events: each message appears once", async () => {
  await withSetup(async (env) => {
    await env.readMidRun({ messages: [earlier, prompt, reply("reply", 20)], entryIds: ["e1", "e2", "e3"] });
    assert.deepEqual(env.hook.result.messages.map((message) => message.role), ["user", "user", "assistant"], "the read showed the prompt and reply");

    // The stalled stream resumes and delivers what the read already loaded.
    await env.emit({ type: "message_end", message: prompt });
    await env.emit({ type: "message_end", message: reply("reply", 20) });

    const messages = env.hook.result.messages;
    assert.deepEqual(messages.map((message) => message.role), ["user", "user", "assistant"]);
    assert.equal(messages.filter(isPrompt).length, 1, "the prompt is not appended after the reply");
    assert.equal(messages.filter((message) => message.role === "assistant").length, 1, "the reply is not appended twice");
  });
});

test("a sliding-window read does not swallow a later same-text follow-up", async () => {
  // The view holds e1..e3; the sidebar's read after new writes returns e2..e5,
  // the last 50-entry window without e1. The tail must start after the view's
  // anchor (e3), not after the shared entry prefix (none), so the new follow-up
  // "继续" is not mistaken for the earlier one the window happens to hold.
  const oldReply = reply("old reply", 2);
  const continuePrompt = { role: "user", content: "继续", timestamp: 3 };
  await withSetup(async (env) => {
    await env.readMidRun({ messages: [continuePrompt, oldReply, prompt, reply("reply", 20)], entryIds: ["e2", "e3", "e4", "e5"] });
    assert.equal(env.hook.result.messages.filter((message) => message.role === "user" && message.content === "继续").length, 1);

    await env.emit({ type: "message_end", message: { role: "user", content: "继续", timestamp: 30 } });

    const messages = env.hook.result.messages;
    assert.equal(messages.filter((message) => message.role === "user" && message.content === "继续").length, 2, "the follow-up renders beside the earlier prompt");
    assert.equal(messages.at(-1).content, "继续", "it is appended after the read's messages");
  }, { initial: { messages: [earlier, continuePrompt, oldReply], entryIds: ["e1", "e2", "e3"] } });
});

test("a live-delivered message does not swallow a later same-text message after the read", async () => {
  await withSetup(async (env) => {
    // The stream delivered the prompt before the read: its message_end already
    // arrived, so it must not be part of the read's still-pending tail.
    await env.emit({ type: "message_end", message: prompt });
    // Consume the signal that delivery already covered; the next one reads.
    await env.hook.result.refreshFromDisk("A");
    await settle();
    env.hook.flush();
    await env.readMidRun({ messages: [earlier, prompt, reply("reply", 20)], entryIds: ["e1", "e2", "e3"] });
    assert.equal(env.hook.result.messages.filter(isPrompt).length, 1);

    await env.emit({ type: "message_end", message: { role: "user", content: "first submission", timestamp: 30 } });

    assert.equal(env.hook.result.messages.filter(isPrompt).length, 2, "the later same-text steer renders");
    assert.equal(env.hook.result.messages.at(-1).content, "first submission");
  });
});

test("a mid-run read ends the partial bubble whose message the snapshot holds", async () => {
  await withSetup(async (env) => {
    await env.emit({ type: "agent_start" });
    await env.emit({
      type: "message_start",
      message: { role: "assistant", content: [{ type: "text", text: "partial" }], model: "m", provider: "p", timestamp: 20 },
    });
    assert.equal(env.hook.result.streamingStore.getState().isStreaming, true, "the reply is streaming");

    await env.readMidRun({ messages: [earlier, prompt, reply("reply", 20)], entryIds: ["e1", "e2", "e3"] });
    assert.equal(env.hook.result.streamingStore.getState().isStreaming, false, "the completed copy ends the bubble");
    assert.equal(env.hook.result.streamingStore.getState().streamingMessage, null);
    assert.equal(env.hook.result.messages.filter((message) => message.role === "assistant").length, 1);
  });
});