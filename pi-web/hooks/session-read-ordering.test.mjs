import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// a session read whose answer was generated before the newest
// reply must not remove that reply from the view once the stream has delivered
// it. On a weak network a mid-run forced read is held back; the stream resumes
// and renders the reply; then the late answer lands. Two shapes are pinned:
//  - a newer read has already applied, so the late answer is older than the
//    view and must be dropped;
//  - nothing newer has applied yet, so the snapshot must keep the messages the
//    stream delivered after the read was issued.
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { useAgentSession, MANUAL_FETCH_TAIL_MS } = await jiti.import("./useAgentSession.ts");
const { deleteSessionViewSnapshot } = await jiti.import("@/lib/session-view-cache");

globalThis.document ??= Object.assign(new EventTarget(), { visibilityState: "visible" });
globalThis.window ??= new EventTarget();
globalThis.requestAnimationFrame ??= () => 0;
globalThis.cancelAnimationFrame ??= () => {};

const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };
const until = async (predicate, message) => {
  for (let index = 0; index < 200; index++) {
    if (predicate()) return;
    await new Promise((resolve) => setImmediate(resolve));
  }
  assert.fail(message);
};
const earlier = { role: "user", content: "earlier", timestamp: 1 };
const prompt = { role: "user", content: "first submission", timestamp: 10 };
const reply = (text, timestamp) => ({ role: "assistant", content: [{ type: "text", text }], model: "m", provider: "p", timestamp });
const replyText = "REPLY-LATE-177";
const isReply = (message) => message.role === "assistant" && message.content?.[0]?.text === replyText;

function deferred() {
  const { promise, resolve } = Promise.withResolvers();
  return { promise, resolve };
}

// `mountGate` holds the mount read's answer; `send: false` leaves the view
// without a run of its own (the mount read and the stream only); `state` is
// the session /state answer.
async function setup({ mountGate = null, send = true, state = { running: true, state: { isStreaming: true, isPromptRunning: true } } } = {}) {
  mock.timers.enable({ apis: ["setTimeout", "setInterval", "Date"] });
  const sources = [];
  const server = {
    context: { messages: [earlier], entryIds: ["e1"] },
    gate: mountGate,
    reads: [],
    state,
    statusGate: null,
    statusReads: [],
    statusState: { isStreaming: true, isPromptRunning: true },
    // The session /state read: its answer is generated now, its delivery can wait.
    stateGate: null,
    stateReads: [],
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
      // The answer is generated from the file now; only its delivery waits
      // behind the gate (a slow link's late response).
      const context = { messages: [...server.context.messages], entryIds: [...server.context.entryIds], oldestEntryId: null, hasMore: false };
      const snapshotRevision = `r:${context.entryIds.join(",")}`;
      const gate = server.gate;
      server.gate = null;
      server.reads.push({ revision: snapshotRevision, held: Boolean(gate) });
      const body = json({
        sessionId: "A",
        filePath: "/A.jsonl",
        leafId: null,
        tree: [],
        treeFormat: "summary",
        snapshotRevision,
        context,
      });
      if (gate) await gate.promise;
      return body;
    }
    if (url === "/api/sessions/A/state") {
      const gate = server.stateGate;
      server.stateGate = null;
      server.stateReads.push({ held: Boolean(gate) });
      const body = json(server.state);
      if (gate) await gate.promise;
      return body;
    }
    if (url === "/api/agent/A" && init.body) {
      await server.response.promise;
      return json({ success: true, data: null });
    }
    if (url === "/api/agent/A") {
      // The status answer is generated now; only its delivery can wait (a slow
      // link), so a setStatus event can land after the read began.
      const gate = server.statusGate;
      server.statusGate = null;
      server.statusReads.push({ held: Boolean(gate) });
      const body = json({ running: true, state: server.statusState });
      if (gate) await gate.promise;
      return body;
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
  const sending = send ? hook.result.handleSend("first submission") : Promise.resolve();
  await settle();
  hook.flush();
  if (send) assert.equal(hook.result.agentRunning, true, "the run is active");
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

// The mid-run read is answered from the file before the reply is written; its
// answer is held until the caller releases it.
async function holdMidRunRead(env) {
  const gate = deferred();
  env.server.gate = gate;
  env.server.context = { messages: [earlier, prompt], entryIds: ["e1", "e2"] };
  const held = env.hook.result.refreshFromDisk("A");
  await until(() => env.server.reads.length === 2, "the mid-run forced read was not issued");
  await settle();
  env.hook.flush();
  return { held, gate };
}

async function streamTheReply(env) {
  await env.emit({ type: "message_end", message: prompt });
  await env.emit({ type: "message_end", message: reply(replyText, 20) });
  assert.equal(env.hook.result.messages.filter(isReply).length, 1, "the stream rendered the reply");
}

test("a late read whose answer predates a newer applied read cannot remove the reply", async () => {
  await withSetup(async (env) => {
    const { held, gate } = await holdMidRunRead(env);
    await streamTheReply(env);

    // The run's end read is answered with the reply written, and applies first.
    env.server.context = { messages: [earlier, prompt, reply(replyText, 20)], entryIds: ["e1", "e2", "e3"] };
    await env.emit({ type: "agent_end" });
    await until(() => env.server.reads.length >= 3, "the end-of-run read was not issued");
    await settle();
    env.hook.flush();
    assert.equal(env.hook.result.messages.filter(isReply).length, 1, "the newer read applied with the reply");

    // The slow answer lands last: it is older than the view and must be dropped.
    gate.resolve();
    await held;
    await settle();
    env.hook.flush();

    const messages = env.hook.result.messages;
    assert.equal(messages.filter(isReply).length, 1, "the late read did not remove the reply");
    assert.equal(messages.filter((message) => message.role === "user" && message.content === "first submission").length, 1);
    assert.equal(messages.at(-1).content?.[0]?.text, replyText, "the reply is still the transcript's last message");
  });
});

test("a read answered before the reply was written keeps the reply the stream delivered after it was issued", async () => {
  await withSetup(async (env) => {
    const { held, gate } = await holdMidRunRead(env);
    await streamTheReply(env);

    // No newer read has applied: this answer is the first to land, but the
    // stream content after its issue is newer than its snapshot.
    gate.resolve();
    await held;
    await settle();
    env.hook.flush();

    const messages = env.hook.result.messages;
    assert.equal(messages.filter(isReply).length, 1, "the reply the stream delivered after the read was issued stayed");
    assert.equal(messages.filter((message) => message.role === "user" && message.content === "first submission").length, 1);
    assert.equal(messages.at(-1).content?.[0]?.text, replyText, "the reply is still the transcript's last message");
  });
});


// code review: the ordering that drops a stale snapshot must not
// drop the includeState half of that read. Its /state request is separate and
// later, and the fields it applies (here queuedMessages) are ordered on their
// own.
test("an includeState read overtaken by a newer read still applies its state", async () => {
  await withSetup(async (env) => {
    env.server.response.resolve();
    await env.sending;

    // /reload's read is answered but held: a slow link.
    const gate = deferred();
    env.server.gate = gate;
    const reload = env.hook.result.handleBuiltinSlashCommand("/reload");
    await until(() => env.server.reads.some((read) => read.held), "the /reload read was not issued");

    // A newer forced read applies while the /reload answer is still held.
    env.server.context = { messages: [earlier, prompt], entryIds: ["e1", "e2"] };
    await env.hook.result.refreshFromDisk("A");
    await settle();
    env.hook.flush();

    // The stale answer still reads and applies its state.
    env.server.state = {
      running: true,
      state: { isStreaming: true, isPromptRunning: true, queuedMessages: { steering: ["kept from the stale read"], followUp: [] } },
    };
    gate.resolve();
    await reload;
    await settle();
    env.hook.flush();

    assert.deepEqual(env.hook.result.queuedMessages.steering, ["kept from the stale read"],
      "the overtaken read's state half still applied");
  });
});

// code review: a status event newer than the read survives it, as
// widgets already do.
test("a status event that arrives after an agent_end read began survives its reply", async () => {
  await withSetup(async (env) => {
    env.server.response.resolve();
    await env.sending;

    const gate = deferred();
    env.server.statusState = { isStreaming: true, isPromptRunning: true, extensionStatuses: [{ key: "saved", text: "from the snapshot" }] };
    env.server.statusGate = gate;
    await env.emit({ type: "agent_end" });
    await until(() => env.server.statusReads.some((read) => read.held), "the end-of-run state read was not issued");

    await env.emit({ type: "extension_ui_request", id: "s1", method: "setStatus", statusKey: "late", statusText: "live after the read" });
    assert.equal(env.hook.result.extensionStatuses.some((status) => status.key === "late"), true,
      "the stream's status rendered before the read answered");

    gate.resolve();
    await settle();
    env.hook.flush();

    assert.deepEqual(env.hook.result.extensionStatuses.map((status) => status.key).sort(), ["late", "saved"],
      "the newer status event survived the read, and the other key took the snapshot");
  });
});

// The mount read's /state half keeps a status event that
// arrived while it was in flight (the per-key touch counter), as agent_end does.
test("a status event that arrives while the mount /state read is in flight survives its reply", async () => {
  const stateGate = deferred();
  const mountGate = deferred();
  await withSetup(async (env) => {
    env.server.stateGate = stateGate;
    env.server.state = { running: true, state: { isStreaming: false, extensionStatuses: [{ key: "late", text: "from the snapshot" }, { key: "saved", text: "saved" }] } };
    mountGate.resolve();
    await until(() => env.server.stateReads.some((read) => read.held), "the mount /state read was not issued");

    await env.emit({ type: "extension_ui_request", id: "s1", method: "setStatus", statusKey: "late", statusText: "live after the read" });
    assert.equal(env.hook.result.extensionStatuses.find((status) => status.key === "late")?.text, "live after the read");

    stateGate.resolve();
    await settle();
    env.hook.flush();

    assert.deepEqual(env.hook.result.extensionStatuses.map((status) => [status.key, status.text]).sort(),
      [["late", "live after the read"], ["saved", "saved"]],
      "the newer status event survived the mount read; the untouched key took the snapshot");
  }, { mountGate, send: false });
});

// The reconcile poll's idle reply merges statuses the
// same way: a key touched while it was in flight keeps its live value.
test("a status event that arrives while a reconcile read is in flight survives its idle reply", async () => {
  await withSetup(async (env) => {
    env.server.response.resolve();
    await env.sending;

    const gate = deferred();
    env.server.statusState = { isStreaming: false, isPromptRunning: false, extensionStatuses: [{ key: "late", text: "from the snapshot" }, { key: "saved", text: "saved" }] };
    env.server.statusGate = gate;
    window.dispatchEvent(new Event("online"));
    await until(() => env.server.statusReads.some((read) => read.held), "the reconcile read was not issued");

    await env.emit({ type: "extension_ui_request", id: "s1", method: "setStatus", statusKey: "late", statusText: "live after the read" });

    gate.resolve();
    await settle();
    env.hook.flush();

    assert.deepEqual(env.hook.result.extensionStatuses.map((status) => [status.key, status.text]).sort(),
      [["late", "live after the read"], ["saved", "saved"]],
      "the reconcile idle reply kept the newer status; the untouched key took the snapshot");
  });
});

// A showLoading read overtaken by a newer applied read ends
// its own loading screen when it finds itself stale, not after its /state
// round trip.
test("a stale showLoading read clears its loading when it recognises staleness", async () => {
  const mountGate = deferred();
  await withSetup(async (env) => {
    assert.equal(env.hook.result.loading, true, "the uncached mount read raised the loading screen");
    // A newer read applies while the mount answer is held.
    env.server.context = { messages: [earlier, prompt], entryIds: ["e1", "e2"] };
    await env.hook.result.refreshFromDisk("A");
    await settle();
    env.hook.flush();
    assert.equal(env.hook.result.messages.length, 2, "the newer read applied its view");

    // The mount answer lands stale; its /state request is held.
    const stateGate = deferred();
    env.server.stateGate = stateGate;
    mountGate.resolve();
    await until(() => env.server.stateReads.some((read) => read.held), "the stale read's /state request was not issued");
    await settle();
    env.hook.flush();
    assert.equal(env.hook.result.loading, false, "the stale read ended its loading before its /state round trip");
    assert.equal(env.hook.result.messages.length, 2, "the stale snapshot did not replace the newer view");

    stateGate.resolve();
    await settle();
    env.hook.flush();
    assert.equal(env.hook.result.loading, false);
  }, { mountGate, send: false });
});

// A manual fetch's tail lives MANUAL_FETCH_TAIL_MS (the
// stale bound plus one watchdog check: in a foreground tab the kept stream
// delivers or the watchdog replaces it within it). Within it, even past the
// stale bound, the half-dead stream's buffered run is the
// read's and renders once; past it, a later run that repeats the same messages
// renders them.
async function manualFetchTail(env) {
  env.server.context = { messages: [earlier, ping, pong], entryIds: ["e1", "e2", "e3"] };
  await env.hook.result.manualFetch();
  await settle();
  env.hook.flush();
  assert.equal(env.hook.result.agentRunning, false, "the view saw no run");
  assert.equal(env.hook.result.messages.filter(isPing).length, 1, "the manual fetch showed the run's messages");
}
const ping = { role: "user", content: "PING-193", timestamp: 30 };
const pong = reply("PONG-193", 31);
const isPing = (message) => message.role === "user" && message.content === "PING-193";
const isPong = (message) => message.role === "assistant" && message.content?.[0]?.text === "PONG-193";
async function deliverRun(env) {
  await env.emit({ type: "agent_start" });
  await env.emit({ type: "message_end", message: ping });
  await env.emit({ type: "message_end", message: pong });
}

test("a manual fetch's tail swallows the half-dead stream's buffered run within the tail bound", async () => {
  await withSetup(async (env) => {
    await manualFetchTail(env);
    mock.timers.setTime(Date.now() + MANUAL_FETCH_TAIL_MS - 1000);
    await deliverRun(env);
    assert.equal(env.hook.result.messages.filter(isPing).length, 1, "the buffered prompt is the one the read already showed");
    assert.equal(env.hook.result.messages.filter(isPong).length, 1, "the buffered reply is the one the read already showed");
  }, { send: false, state: { running: false } });
});

test("a manual fetch's tail past the tail bound is dropped: a later same-text run renders", async () => {
  await withSetup(async (env) => {
    await manualFetchTail(env);
    mock.timers.setTime(Date.now() + MANUAL_FETCH_TAIL_MS + 1000);
    await deliverRun(env);
    assert.equal(env.hook.result.messages.filter(isPing).length, 2, "the later same-text message is not swallowed");
    assert.equal(env.hook.result.messages.filter(isPong).length, 2, "the later same-text reply is not swallowed");
  }, { send: false, state: { running: false } });
});

// the same status ordering at the other run-end read, the 15 s
// running poll's idle reply. routed it through the per-key merge
// as well; this pins that point, which no repo test covered before.
test("a status event that arrives after the reconcile read began survives its reply", async () => {
  await withSetup(async (env) => {
    env.server.response.resolve();
    await env.sending;

    const gate = deferred();
    env.server.statusState = { isStreaming: false, isPromptRunning: false, extensionStatuses: [{ key: "saved", text: "from the snapshot" }] };
    env.server.statusGate = gate;
    // The running poll fires on the foreground tick as well as on its interval.
    document.dispatchEvent(new Event("visibilitychange"));
    await until(() => env.server.statusReads.some((read) => read.held), "the reconcile state read was not issued");

    await env.emit({ type: "extension_ui_request", id: "s1", method: "setStatus", statusKey: "late", statusText: "live after the read" });
    assert.equal(env.hook.result.extensionStatuses.some((status) => status.key === "late"), true,
      "the stream's status rendered before the reconcile reply answered");

    gate.resolve();
    await settle();
    env.hook.flush();

    assert.deepEqual(env.hook.result.extensionStatuses.map((status) => status.key).sort(), ["late", "saved"],
      "the newer status event survived the reconcile reply, and the other key took the snapshot");
  });
});
