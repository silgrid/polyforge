import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// (ledger N1): a mid-run read can show a tail whose first entry
// never gets a `message_end` — a compaction divider is one such entry (it is a
// `custom` message written to the file, reported by `compaction_end`). When the
// stalled stream resumes, the read's already-shown messages must not render a
// second time; the buffer blocks only the tail's head today, so the later
// `message_end` events are appended again until the run settles.
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
const replyText = "RESIDUAL-REPLY-224";
const reply = { role: "assistant", content: [{ type: "text", text: replyText }], model: "m", provider: "p", timestamp: 20 };
const isPrompt = (message) => message.role === "user" && message.content === "first submission";
const isReply = (message) => message.role === "assistant" && message.content?.[0]?.text === replyText;
// The compaction divider: entryToUiMessage() renders a `compaction` entry as
// this custom message; the stream reports it with compaction_end, never with
// message_end.
const compaction = { role: "custom", customType: "compaction", content: "compacted summary", display: true, timestamp: 15 };

async function setup() {
  mock.timers.enable({ apis: ["setTimeout", "setInterval", "Date"] });
  const sources = [];
  const server = {
    context: { messages: [earlier], entryIds: ["e1"] },
    response: Promise.withResolvers(),
    reads: 0,
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
      server.reads += 1;
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

test("a compaction divider in a mid-run read's tail does not duplicate the messages the stream then delivers", async () => {
  await withSetup(async (env) => {
    // The stream stalled; the sidebar's external-write signal forces a mid-run
    // read. The file now holds: the earlier message, the compaction divider the
    // run wrote, the prompt and the reply. The stalled stream still buffers the
    // prompt's and the reply's message_end events.
    env.server.context = { messages: [earlier, compaction, prompt, reply], entryIds: ["e1", "e2", "e3", "e4"] };
    await env.hook.result.refreshFromDisk("A");
    await settle();
    env.hook.flush();
    assert.deepEqual(
      env.hook.result.messages.map((message) => message.role),
      ["user", "custom", "user", "assistant"],
      "the read showed the compaction divider, the prompt and the reply once",
    );

    // The stream resumes and delivers the two completed messages it buffered.
    await env.emit({ type: "message_end", message: prompt });
    await env.emit({ type: "message_end", message: reply });

    assert.equal(env.hook.result.messages.filter(isPrompt).length, 1, "the prompt is not appended after the read showed it");
    assert.equal(env.hook.result.messages.filter(isReply).length, 1, "the reply is not appended after the read showed it");
    assert.deepEqual(
      env.hook.result.messages.map((message) => message.role),
      ["user", "custom", "user", "assistant"],
      "the transcript is unchanged",
    );
  });
});