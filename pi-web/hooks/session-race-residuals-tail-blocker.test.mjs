import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// (ledger N1), beyond the authored repro's divider-before-prompt
// shape: an entry the stream never reports with its own `message_end` must not
// hold back the messages behind it in a mid-run read's late tail.
//  - a compaction divider between the run's messages (overflow-retry shape);
//  - an extension boundary draft (`custom_message`) before the prompt;
//  - and a regular custom message whose own `message_end` does arrive first,
//    which must still be consumed at the head rather than dropped.
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
const reply = { role: "assistant", content: [{ type: "text", text: "TAIL-BLOCKER-REPLY-224" }], model: "m", provider: "p", timestamp: 20 };
const isPrompt = (message) => message.role === "user" && message.content === "first submission";
const isReply = (message) => message.role === "assistant" && message.content?.[0]?.text === "TAIL-BLOCKER-REPLY-224";
// What entryToUiMessage() renders for a compaction entry. The stream reports
// it with compaction_end, never with message_end.
const divider = { role: "custom", customType: "compaction", content: "compacted summary", display: true, timestamp: 15 };
// An extension boundary draft (`custom_message`): a custom entry written beside
// the run, unverified but the same no-message_end class as the divider.
const draft = { role: "custom", customType: "boundary-draft", content: "drafted context", display: true, timestamp: 15 };
// A custom message the stream itself appends: its own message_end does arrive.
const custom = { role: "custom", customType: "extension-note", content: "extension note", display: true, timestamp: 15 };

async function setup() {
  mock.timers.enable({ apis: ["setTimeout", "setInterval", "Date"] });
  const sources = [];
  const server = {
    context: { messages: [earlier], entryIds: ["e1"] },
    response: Promise.withResolvers(),
  };
  globalThis.EventSource = class {
    readyState = 0;
    onmessage = null;
    onerror = null;
    constructor(url) { this.url = url; sources.push(this); }
    close() { this.readyState = 2; }
    emit(event) { this.readyState = 1; this.onmessage?.({ data: JSON.stringify(event) }); }
  };
  globalThis.fetch = async (requestUrl, init = {}) => {
    const url = requestUrl.replace("?systemPrompt=0", "");
    if (url.startsWith("/api/models")) return json({ models: {}, modelList: [] });
    if (url.startsWith("/api/sessions/A/context?")) return json({ context: { ...server.context, oldestEntryId: null, hasMore: false } });
    if (url.startsWith("/api/sessions/A?")) {
      return json({
        sessionId: "A",
        filePath: "/A.jsonl",
        leafId: null,
        tree: [],
        treeFormat: "summary",
        snapshotRevision: `r:${server.context.entryIds.join(",")}`,
        context: { ...server.context, messages: [...server.context.messages], entryIds: [...server.context.entryIds], oldestEntryId: null, hasMore: false },
      });
    }
    if (url === "/api/sessions/A/state") return json({ running: false });
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
  const sending = hook.result.handleSend("first submission");
  await settle();
  hook.flush();
  assert.equal(hook.result.agentRunning, true, "the run is active");
  return {
    hook,
    emit,
    server,
    sending,
    teardown: () => {
      hook.unmount();
      for (const source of sources) source.close();
      mock.timers.reset();
      deleteSessionViewSnapshot("A");
    },
  };
}

async function withSetup(body) {
  const originalError = console.error;
  console.error = () => {};
  const env = await setup();
  try {
    await body(env);
  } finally {
    console.error = originalError;
    env.server.response.resolve();
    await env.sending;
    env.teardown();
  }
}

async function readThenDeliver(env, messages, events) {
  env.server.context = {
    messages,
    entryIds: messages.map((_, index) => `e${index + 1}`),
  };
  await env.hook.result.refreshFromDisk("A");
  await settle();
  env.hook.flush();
  assert.deepEqual(
    env.hook.result.messages.map((message) => message.role),
    messages.map((message) => message.role),
    "the read showed the file's tail once",
  );
  for (const event of events) await env.emit(event);
}

test("a compaction divider between the run's messages does not duplicate the reply after it", async () => {
  await withSetup(async (env) => {
    // Overflow-retry compaction: the prompt is already in the file, the divider
    // was written between it and the reply.
    await readThenDeliver(env, [earlier, prompt, divider, reply], [
      { type: "message_end", message: prompt },
      { type: "message_end", message: reply },
    ]);
    assert.equal(env.hook.result.messages.filter(isPrompt).length, 1, "the prompt is not appended after the read showed it");
    assert.equal(env.hook.result.messages.filter(isReply).length, 1, "the reply is not appended after the read showed it");
    assert.deepEqual(
      env.hook.result.messages.map((message) => message.role),
      ["user", "user", "custom", "assistant"],
      "the transcript is unchanged",
    );
  });
});

test("an extension boundary draft with no message_end does not block the prompt behind it", async () => {
  await withSetup(async (env) => {
    await readThenDeliver(env, [earlier, draft, prompt, reply], [
      { type: "message_end", message: prompt },
      { type: "message_end", message: reply },
    ]);
    assert.equal(env.hook.result.messages.filter(isPrompt).length, 1, "the prompt is not appended after the read showed it");
    assert.equal(env.hook.result.messages.filter(isReply).length, 1, "the reply is not appended after the read showed it");
    assert.deepEqual(
      env.hook.result.messages.map((message) => message.role),
      ["user", "custom", "user", "assistant"],
      "the transcript is unchanged",
    );
  });
});

test("a custom message whose own message_end arrives first is consumed, not skipped", async () => {
  await withSetup(async (env) => {
    await readThenDeliver(env, [earlier, custom, prompt, reply], [
      { type: "message_end", message: custom },
      { type: "message_end", message: prompt },
      { type: "message_end", message: reply },
    ]);
    assert.equal(env.hook.result.messages.filter((message) => message.customType === "extension-note").length, 1, "the custom message is shown once");
    assert.equal(env.hook.result.messages.filter(isPrompt).length, 1, "the prompt is not appended after the read showed it");
    assert.equal(env.hook.result.messages.filter(isReply).length, 1, "the reply is not appended after the read showed it");
    assert.deepEqual(
      env.hook.result.messages.map((message) => message.role),
      ["user", "custom", "user", "assistant"],
      "the transcript is unchanged",
    );
  });
});