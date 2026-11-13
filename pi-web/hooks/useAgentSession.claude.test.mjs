import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { useAgentSession, newSessionRequestBody, foldClaudeSubagents } = await jiti.import("./useAgentSession.ts");

globalThis.document ??= Object.assign(new EventTarget(), { visibilityState: "visible" });
globalThis.window ??= new EventTarget();

const json = (body) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };

test("a pi create body is the one sent before claude existed", () => {
  const options = { cwd: "/p", harness: "pi", claudeModel: "claude-sonnet-5-5" };
  assert.deepEqual(newSessionRequestBody(options), { cwd: "/p", type: "ensure_session" });
  assert.deepEqual(
    newSessionRequestBody({ ...options, toolNames: ["read"], model: { provider: "a", modelId: "b" }, thinkingLevel: "high" }),
    { cwd: "/p", type: "ensure_session", toolNames: ["read"], provider: "a", modelId: "b", thinkingLevel: "high" },
  );
});

test("a claude create body carries the harness and the full model name, and no preset, pi model, reasoning or tools", () => {
  assert.deepEqual(
    newSessionRequestBody({ cwd: "/p", harness: "claude", claudeModel: "claude-haiku-5-5", toolNames: ["read"], model: { provider: "a", modelId: "b" }, thinkingLevel: "high" }),
    { cwd: "/p", type: "ensure_session", harness: "claude", model: "claude-haiku-5-5" },
  );
});

test("a fresh composer set to claude creates a claude session, sends no set_model and reports its choices", async () => {
  const requests = [];
  const choices = [];
  globalThis.EventSource = class {
    readyState = 0;
    constructor(url) { this.url = url; queueMicrotask(() => { this.readyState = 1; this.onmessage?.({ data: JSON.stringify({ type: "connected", isStreaming: false }) }); }); }
    close() { this.readyState = 2; }
  };
  globalThis.fetch = async (url, init = {}) => {
    const body = init.body ? JSON.parse(init.body) : null;
    requests.push({ url, body });
    if (url === "/api/claude") return json({ available: true });
    if (url.startsWith("/api/models")) return json({ models: {}, modelList: [], defaultModel: { provider: "p", modelId: "m" } });
    if (url === "/api/agent/new") return json({ success: true, sessionId: "c1", model: { provider: "claude", modelId: "claude-haiku-5-5" } });
    if (url === "/api/agent/c1") return json({ success: true, data: null });
    return json({});
  };
  const hook = renderHook(useAgentSession, {
    session: null,
    newSessionCwd: "/project",
    newSessionDraftKey: "new:draft",
    onNewSessionChoicesChange: (value) => choices.push(value),
    t: (key) => key,
  });
  await settle();
  hook.flush();
  assert.deepEqual(hook.result.claudeAvailability, { available: true, models: [] }, "no list answered: the composer offers only the session's model");
  assert.equal(hook.result.newSessionHarness, "pi");
  assert.equal(hook.result.isClaudeSession, false);
  hook.result.chooseNewSessionHarness("claude");
  hook.flush();
  assert.equal(hook.result.newSessionClaudeModel, "default", "claude starts at claude's own default alias");
  hook.result.setNewSessionClaudeModel("claude-haiku-5-5");
  hook.flush();
  await settle();
  assert.equal(hook.result.isClaudeSession, true);
  assert.deepEqual(choices.at(-1), { model: null, thinkingLevel: null, harness: "claude", claudeModel: "claude-haiku-5-5", claudeEffort: "default" });
  const originalError = console.error;
  console.error = () => {};
  try {
    await hook.result.handleSend("hello claude");
    await settle();
  } finally {
    console.error = originalError;
  }
  const create = requests.find((request) => request.url === "/api/agent/new");
  assert.deepEqual(create.body, { cwd: "/project", type: "ensure_session", harness: "claude", model: "claude-haiku-5-5" });
  const commands = requests.filter((request) => request.url === "/api/agent/c1" && request.body).map((request) => request.body.type);
  assert.deepEqual(commands, ["prompt"]);
  hook.unmount();
});

test("an image-only claude send reports [Image] as the new session's first message", async () => {
  const requests = [];
  const created = [];
  globalThis.EventSource = class {
    readyState = 0;
    constructor(url) { this.url = url; queueMicrotask(() => { this.readyState = 1; this.onmessage?.({ data: JSON.stringify({ type: "connected", isStreaming: false }) }); }); }
    close() { this.readyState = 2; }
  };
  globalThis.fetch = async (url, init = {}) => {
    const body = init.body ? JSON.parse(init.body) : null;
    requests.push({ url, body });
    if (url === "/api/claude") return json({ available: true });
    if (url.startsWith("/api/models")) return json({ models: {}, modelList: [], defaultModel: { provider: "p", modelId: "m" } });
    if (url === "/api/agent/new") return json({ success: true, sessionId: "c9", model: { provider: "claude", modelId: "claude-opus-5-5" } });
    if (url === "/api/agent/c9") return json({ success: true, data: null });
    return json({});
  };
  const hook = renderHook(useAgentSession, {
    session: null,
    newSessionCwd: "/project",
    newSessionDraftKey: "new:image-only",
    onSessionCreated: (info) => created.push(info),
    t: (key) => key,
  });
  await settle();
  hook.flush();
  hook.result.chooseNewSessionHarness("claude");
  hook.flush();
  await settle();
  const originalError = console.error;
  console.error = () => {};
  try {
    await hook.result.handleSend("", [{ data: "aW1hZ2U=", mimeType: "image/png", previewUrl: "blob:x" }]);
    await settle();
  } finally {
    console.error = originalError;
  }
  const prompt = requests.find((request) => request.url === "/api/agent/c9" && request.body?.type === "prompt");
  assert.deepEqual(prompt.body, { type: "prompt", message: "", images: [{ type: "image", data: "aW1hZ2U=", mimeType: "image/png" }] });
  assert.equal(created.at(-1)?.firstMessage, "[Image]");
  hook.unmount();
});

test("switching the harness resets the destination's model: claude to default, pi to its default (no override); the claude list is GET /api/claude's model names", async () => {
  globalThis.EventSource = class {
    readyState = 0;
    constructor() { queueMicrotask(() => { this.readyState = 1; this.onmessage?.({ data: JSON.stringify({ type: "connected", isStreaming: false }) }); }); }
    close() { this.readyState = 2; }
  };
  const modelList = [{ provider: "p", id: "m0", name: "Model 0" }, { provider: "p", id: "m1", name: "Model 1" }];
  globalThis.fetch = async (url) => {
    if (url === "/api/claude") {
      return json({
        available: true,
        models: [{ value: "default", source: "alias" }, { value: "haiku", resolvedModel: "claude-haiku-5-5", source: "alias" }, { value: "claude-custom-1", source: "custom" }, { value: "-bad name", source: "custom" }],
        defaultModel: "default",
      });
    }
    if (url.startsWith("/api/models")) return json({ models: { "p/m0": "Model 0", "p/m1": "Model 1" }, modelList, defaultModel: { provider: "p", modelId: "m0" } });
    return json({});
  };
  const hook = renderHook(useAgentSession, { session: null, newSessionCwd: "/project", newSessionDraftKey: "new:switch", t: (key) => key });
  await settle();
  hook.flush();
  await hook.result.handleModelChange("p", "m1");
  hook.flush();
  assert.deepEqual(hook.result.displayModel, { provider: "p", modelId: "m1" });
  hook.result.chooseNewSessionHarness("claude");
  hook.flush();
  hook.result.setNewSessionClaudeModel("claude-haiku-5-5");
  hook.flush();
  assert.equal(hook.result.newSessionClaudeModel, "claude-haiku-5-5");
  hook.result.chooseNewSessionHarness("pi");
  hook.flush();
  assert.equal(hook.result.newSessionHarness, "pi");
  assert.equal(hook.result.newSessionModel, null, "no pi override is kept");
  assert.equal(hook.result.isAutoModelSelection, true);
  assert.deepEqual(hook.result.displayModel, { provider: "p", modelId: "m0" }, "pi's default model again");
  hook.result.chooseNewSessionHarness("claude");
  hook.flush();
  assert.equal(hook.result.newSessionClaudeModel, "default", "claude resets to claude's default alias");
  assert.deepEqual(hook.result.claudeAvailability, { available: true, models: ["default", "haiku", "claude-custom-1"] }, "the listed names, an invalid one dropped");
  hook.unmount();
});

// the claude subagent run map.
const sdkCall = (id, text) => ({ id, message: { role: "assistant", content: [{ type: "toolCall", id: `toolu_${id}`, name: "Read", arguments: { file_path: text } }] } });
const subRun = (entries, over = {}) => ({ toolCallId: "toolu_agent", background: true, status: "running", entries, ...over });

test("history seeds the run map, live deltas merge by entry id, and a mid-run reload shows each entry once", () => {
  let state = { sessionId: null, runs: {} };
  state = foldClaudeSubagents(state, "c2", [subRun([sdkCall("e1", "a")])], "history");
  assert.deepEqual(state.runs.toolu_agent.entries[0].message.content[0], { type: "toolCall", toolCallId: "toolu_e1", toolName: "Read", input: { file_path: "a" } }, "entries take the chat's tool call shape");
  state = foldClaudeSubagents(state, "c2", [subRun([sdkCall("e2", "b")], { progress: "Reading" })], "live");
  state = foldClaudeSubagents(state, "c2", [subRun([sdkCall("e2", "b")])], "live");
  assert.deepEqual(state.runs.toolu_agent.entries.map((entry) => entry.id), ["e1", "e2"], "a repeated live entry is not added twice");
  // A reload mid-run: the read holds e1 and e2 on disk, then the stream brings e3.
  state = foldClaudeSubagents(state, "c2", [subRun([sdkCall("e1", "a"), sdkCall("e2", "b")])], "history");
  state = foldClaudeSubagents(state, "c2", [subRun([sdkCall("e3", "c")], { status: "completed" })], "live");
  assert.deepEqual(state.runs.toolu_agent.entries.map((entry) => entry.id), ["e1", "e2", "e3"]);
  assert.equal(state.runs.toolu_agent.status, "completed");
  assert.equal(state.runs.toolu_agent.progress, "Reading");
  const same = foldClaudeSubagents(state, "c2", undefined, "history");
  assert.equal(same, state, "a detail without claudeSubagents changes nothing");
  assert.deepEqual(foldClaudeSubagents(state, "other", undefined, "history").runs, {}, "another session starts empty");
});

test("the hook exposes the session's runs from history and merges claude_subagent events; a session without any stays empty", async () => {
  const sources = [];
  globalThis.EventSource = class {
    readyState = 0;
    constructor(url) { this.url = url; sources.push(this); queueMicrotask(() => { this.readyState = 1; this.onmessage?.({ data: JSON.stringify({ type: "connected", isStreaming: false }) }); }); }
    close() { this.readyState = 2; }
  };
  const detail = (id, claudeSubagents) => ({
    sessionId: id, filePath: "", totalActiveMs: 0, tree: [], leafId: null, snapshotRevision: null, toolNames: [],
    info: { id, path: "", cwd: "/project", created: "", modified: "", messageCount: 0, firstMessage: "", harness: "claude", claude: { model: "claude-haiku-5-5" } },
    context: { messages: [], entryIds: [], oldestEntryId: null, hasMore: false, turnsBefore: 0, thinkingLevel: "off", model: null },
    ...(claudeSubagents ? { claudeSubagents } : {}),
  });
  globalThis.fetch = async (url) => {
    if (String(url).startsWith("/api/sessions/c3")) return json(detail("c3", [subRun([sdkCall("e1", "a")])]));
    if (String(url).startsWith("/api/sessions/c4")) return json(detail("c4"));
    if (String(url).startsWith("/api/models")) return json({ models: {}, modelList: [], defaultModel: null });
    return json({});
  };
  const session = { id: "c3", path: "", cwd: "/project", created: "", modified: "", messageCount: 0, firstMessage: "", harness: "claude", claude: { model: "claude-haiku-5-5" } };
  const hook = renderHook(useAgentSession, { session, newSessionCwd: null, newSessionDraftKey: null, t: (key) => key });
  await settle();
  hook.flush();
  assert.deepEqual(Object.keys(hook.result.claudeSubagents), ["toolu_agent"]);
  sources.at(-1).onmessage?.({ data: JSON.stringify({ type: "claude_subagent", run: subRun([sdkCall("e2", "b")], { status: "completed" }) }) });
  await settle();
  hook.flush();
  assert.deepEqual(hook.result.claudeSubagents.toolu_agent.entries.map((entry) => entry.id), ["e1", "e2"]);
  assert.equal(hook.result.claudeSubagents.toolu_agent.status, "completed");
  assert.equal(hook.result.messages.length, 0, "a subagent event adds no main message");
  hook.unmount();

  const plain = renderHook(useAgentSession, { session: { ...session, id: "c4" }, newSessionCwd: null, newSessionDraftKey: null, t: (key) => key });
  await settle();
  plain.flush();
  assert.deepEqual(plain.result.claudeSubagents, {});
  plain.unmount();
});

// claude's effort in the create body, and an open session's model / effort switches.
test("a claude create body carries a picked effort and never the default entry", () => {
  const options = { cwd: "/p", harness: "claude", claudeModel: "claude-opus-5-5" };
  assert.deepEqual(newSessionRequestBody({ ...options, claudeEffort: "medium" }), { cwd: "/p", type: "ensure_session", harness: "claude", model: "claude-opus-5-5", effort: "medium" });
  assert.deepEqual(newSessionRequestBody({ ...options, claudeEffort: "default" }), { cwd: "/p", type: "ensure_session", harness: "claude", model: "claude-opus-5-5" });
});

test("a fresh claude composer's effort seeds the created session; it sends no set_model or set_effort", async () => {
  const requests = [];
  globalThis.EventSource = class {
    readyState = 0;
    constructor() { queueMicrotask(() => { this.readyState = 1; this.onmessage?.({ data: JSON.stringify({ type: "connected", isStreaming: false }) }); }); }
    close() { this.readyState = 2; }
  };
  globalThis.fetch = async (url, init = {}) => {
    requests.push({ url, body: init.body ? JSON.parse(init.body) : null });
    if (url === "/api/claude") return json({ available: true });
    if (url.startsWith("/api/models")) return json({ models: {}, modelList: [], defaultModel: null });
    if (url === "/api/agent/new") return json({ success: true, sessionId: "c7" });
    if (url === "/api/agent/c7") return json({ success: true, data: null });
    return json({});
  };
  const hook = renderHook(useAgentSession, { session: null, newSessionCwd: "/project", newSessionDraftKey: "new:effort", t: (key) => key });
  await settle();
  hook.flush();
  hook.result.chooseNewSessionHarness("claude");
  hook.flush();
  hook.result.handleClaudeEffortChange("high");
  hook.flush();
  assert.equal(hook.result.newSessionClaudeEffort, "high");
  const originalError = console.error;
  console.error = () => {};
  try {
    await hook.result.handleSend("seeded");
    await settle();
  } finally {
    console.error = originalError;
  }
  assert.equal(requests.find((request) => request.url === "/api/agent/new").body.effort, "high");
  assert.deepEqual(requests.filter((request) => request.url === "/api/agent/c7" && request.body).map((request) => request.body.type), ["prompt"]);
  hook.unmount();
});

test("an open claude session shows a switch only once claude accepted it; a refusal is one localized line the next success clears", async () => {
  const answers = [
    { success: true, data: { model: "claude-opus-5-5", rejected: true, error: "API error · model not changed", errorCode: "check_failed" } },
    { success: true, data: { model: "claude-fable-5-1" } },
    { success: true, data: { effort: "max" } },
  ];
  const commands = [];
  globalThis.EventSource = class {
    readyState = 0;
    constructor() { queueMicrotask(() => { this.readyState = 1; this.onmessage?.({ data: JSON.stringify({ type: "connected", isStreaming: false }) }); }); }
    close() { this.readyState = 2; }
  };
  const info = { id: "c5", path: "", cwd: "/project", created: "", modified: "", messageCount: 0, firstMessage: "", harness: "claude", claude: { model: "claude-opus-5-5", effort: "default" } };
  globalThis.fetch = async (url, init = {}) => {
    if (url === "/api/agent/c5" && init.body) {
      commands.push(JSON.parse(init.body));
      return json(answers.shift());
    }
    if (String(url).startsWith("/api/sessions/c5")) {
      return json({ sessionId: "c5", filePath: "", totalActiveMs: 0, tree: [], leafId: null, snapshotRevision: null, toolNames: [], info, context: { messages: [], entryIds: [], oldestEntryId: null, hasMore: false, turnsBefore: 0, thinkingLevel: "off", model: null } });
    }
    if (String(url).startsWith("/api/models")) return json({ models: {}, modelList: [], defaultModel: null });
    return json({});
  };
  const t = (key, params = {}) => (key === "claude.switchModelError" ? `Could not switch model: ${params.error}` : key);
  const hook = renderHook(useAgentSession, { session: info, newSessionCwd: null, newSessionDraftKey: null, t });
  await settle();
  hook.flush();
  hook.result.handleClaudeModelChange("claude-haiku-5-5");
  await settle();
  hook.flush();
  assert.equal(hook.result.claudeSessionChoices.model, "claude-opus-5-5", "a refused switch keeps the old model");
  assert.equal(hook.result.claudeSwitchError, "Could not switch model: API error · model not changed");
  hook.result.handleClaudeModelChange("claude-fable-5-1");
  await settle();
  hook.flush();
  assert.equal(hook.result.claudeSessionChoices.model, "claude-fable-5-1");
  assert.equal(hook.result.claudeSwitchError, null, "the next success replaces the line");
  hook.result.handleClaudeEffortChange("max");
  await settle();
  hook.flush();
  assert.equal(hook.result.claudeSessionChoices.effort, "max");
  assert.deepEqual(commands, [{ type: "set_model", model: "claude-haiku-5-5" }, { type: "set_model", model: "claude-fable-5-1" }, { type: "set_effort", effort: "max" }]);
  hook.unmount();
});

// a claude session's "Edit from here" is pi's in-place edit — the composer
// takes the message (text and images), Cancel leaves everything as it was, and Send is one
// prompt naming the message it replaces (the server resumes the same session truncated
// before it). "New session" still forks.
function claudeEditHarness({ rejectPrompt = false, goneAfterReject = false } = {}) {
  let rejected = false;
  const commands = [];
  const replaced = [];
  const restored = [];
  const forked = [];
  const reads = [];
  globalThis.EventSource = class {
    readyState = 0;
    constructor() { queueMicrotask(() => { this.readyState = 1; this.onmessage?.({ data: JSON.stringify({ type: "connected", isStreaming: false }) }); }); }
    close() { this.readyState = 2; }
  };
  const info = { id: "c7", path: "", cwd: "/project", created: "", modified: "", messageCount: 4, firstMessage: "one", harness: "claude", claude: { model: "claude-opus-5-5", effort: "default" } };
  const messages = [
    { role: "user", content: "one", timestamp: 1 },
    { role: "assistant", content: [{ type: "text", text: "answer one" }], timestamp: 2 },
    { role: "user", content: "two", timestamp: 3 },
    { role: "assistant", content: [{ type: "text", text: "answer two" }], timestamp: 4 },
  ];
  globalThis.fetch = async (url, init = {}) => {
    if (url === "/api/agent/c7" && init.body) {
      const body = JSON.parse(init.body);
      commands.push(body);
      if (body.type === "fork") return json({ success: true, data: { cancelled: false, newSessionId: "fork-7" } });
      if (body.type === "prompt" && rejectPrompt) {
        rejected = true;
        return new Response(JSON.stringify({ error: "claude cannot resume there", code: "prompt_rejected", accepted: false }), { status: 500, headers: { "Content-Type": "application/json" } });
      }
      return json({ success: true, data: null });
    }
    if (String(url).startsWith("/api/sessions/c7")) {
      reads.push(url);
      // Another page edited before u2 meanwhile: the history no longer holds it.
      const gone = goneAfterReject && rejected;
      return json({ sessionId: "c7", filePath: "", totalActiveMs: 0, tree: [], leafId: gone ? "a1" : "a2", snapshotRevision: null, toolNames: [], info, claudeEditEntryIds: gone ? [] : ["u2"], context: { messages: gone ? messages.slice(0, 2) : messages, entryIds: gone ? ["u1", "a1"] : ["u1", "a1", "u2", "a2"], oldestEntryId: "u1", hasMore: false, turnsBefore: 0, thinkingLevel: "off", model: null } });
    }
    if (String(url).startsWith("/api/models")) return json({ models: {}, modelList: [], defaultModel: null });
    return json({});
  };
  const chatInputRef = { current: { replaceMessage: (message) => replaced.push(message), restoreSubmission: (...args) => restored.push(args) } };
  const hook = renderHook(useAgentSession, {
    session: info,
    newSessionCwd: null,
    newSessionDraftKey: null,
    chatInputRef,
    t: (key) => key,
    onSessionForked: (id) => forked.push(id),
  });
  return { hook, commands, replaced, restored, forked, reads };
}
const EDITED = { role: "user", content: [{ type: "text", text: "two" }, { type: "image", source: { type: "base64", media_type: "image/png", data: "aW1hZ2U=" } }] };

test("a claude edit stages in place like pi's; Cancel changes nothing; Send is one prompt naming the replaced message; New session forks", async () => {
  const { hook, commands, replaced, forked } = claudeEditHarness();
  try {
    await settle();
    hook.flush();
    assert.deepEqual(hook.result.claudeEditEntryIds, ["u2"]);
    assert.deepEqual(hook.result.entryIds, ["u1", "a1", "u2", "a2"]);
    // Staging: the composer takes the message, the bubble is the one being edited, nothing is sent.
    hook.result.handleEditContent(EDITED, "u2");
    hook.flush();
    assert.deepEqual(replaced, [EDITED]);
    assert.equal(hook.result.editEntryId, "u2");
    assert.equal(typeof hook.result.cancelEdit, "function");
    assert.deepEqual(commands, [], "staging an edit sent a command");
    // Cancel: no edit pending, the history as it was, still nothing sent.
    hook.result.cancelEdit();
    hook.flush();
    assert.equal(hook.result.editEntryId, null);
    assert.equal(hook.result.cancelEdit, undefined);
    assert.deepEqual(hook.result.entryIds, ["u1", "a1", "u2", "a2"]);
    assert.deepEqual(commands, []);
    // Send: one prompt that names the replaced message; no navigate, no fork, no new session.
    hook.result.handleEditContent(EDITED, "u2");
    hook.flush();
    await hook.result.handleSend("two, edited", [{ data: "aW1hZ2U=", mimeType: "image/png", previewUrl: "" }]);
    await settle();
    hook.flush();
    assert.deepEqual(commands, [{ type: "prompt", message: "two, edited", images: [{ type: "image", data: "aW1hZ2U=", mimeType: "image/png" }], entryId: "u2" }]);
    assert.equal(hook.result.editEntryId, null);
    assert.deepEqual(forked, [], "the edit opened another session");
    // The view ends before the edited message, then shows the edited prompt.
    assert.deepEqual(hook.result.entryIds, ["u1", "a1"]);
    assert.deepEqual(hook.result.messages.map((message) => message.role), ["user", "assistant", "user"]);
    assert.equal(hook.result.messages[2].content[0].text, "two, edited");
    // "New session" is still a fork at the message, with nothing put in a draft.
    await hook.result.handleFork("u1");
    hook.flush();
    assert.deepEqual(commands.at(-1), { type: "fork", entryId: "u1" });
    assert.deepEqual(forked, ["fork-7"]);
  } finally {
    hook.unmount();
  }
});

test("a refused claude edit leaves the edit pending over the whole history and gives the composer its text back", async () => {
  const { hook, commands, restored, reads } = claudeEditHarness({ rejectPrompt: true });
  const originalError = console.error;
  console.error = () => {};
  try {
    await settle();
    hook.flush();
    hook.result.handleEditContent(EDITED, "u2");
    hook.flush();
    const readsBefore = reads.length;
    await hook.result.handleSend("two, edited");
    await settle();
    hook.flush();
    assert.deepEqual(commands, [{ type: "prompt", message: "two, edited", entryId: "u2" }]);
    assert.equal(hook.result.editEntryId, "u2", "the refused edit is no longer pending");
    assert.ok(reads.length > readsBefore, "the history was not read again");
    assert.deepEqual(hook.result.entryIds, ["u1", "a1", "u2", "a2"]);
    assert.equal(restored.length, 1);
    assert.equal(restored[0][0], "two, edited");
  } finally {
    console.error = originalError;
    hook.unmount();
  }
});

test("a refused claude edit whose message left the history is no longer pending", async () => {
  const { hook, commands } = claudeEditHarness({ rejectPrompt: true, goneAfterReject: true });
  const originalError = console.error;
  console.error = () => {};
  try {
    await settle();
    hook.flush();
    hook.result.handleEditContent(EDITED, "u2");
    hook.flush();
    await hook.result.handleSend("two, edited");
    await settle();
    hook.flush();
    assert.deepEqual(commands, [{ type: "prompt", message: "two, edited", entryId: "u2" }]);
    assert.deepEqual(hook.result.entryIds, ["u1", "a1"]);
    assert.equal(hook.result.editEntryId, null, "an edit of a message the page no longer shows stays pending");
    assert.equal(hook.result.cancelEdit, undefined);
  } finally {
    console.error = originalError;
    hook.unmount();
  }
});

// rewrote "a ! command leaves a staged claude edit staged and the history whole":
// a claude session has no shell, so a `!` message is a prompt like any other and carries the edit.
test("a ! message in a claude session is a prompt, never a bash command; a staged edit goes with it", async () => {
  const { hook, commands } = claudeEditHarness();
  const originalError = console.error;
  console.error = () => {};
  try {
    await settle();
    hook.flush();
    hook.result.handleEditContent(EDITED, "u2");
    hook.flush();
    await hook.result.handleSend("!ls");
    await settle();
    hook.flush();
    assert.deepEqual(commands, [{ type: "prompt", message: "!ls", entryId: "u2" }]);
    assert.ok(!commands.some((command) => command.type === "bash"), "a ! message went out as a bash command");
    assert.equal(hook.result.editEntryId, null, "the edit went with the prompt");
    assert.deepEqual(hook.result.entryIds, ["u1", "a1"], "the view is cut before the edited message");
    assert.deepEqual(hook.result.messages.map((message) => message.role), ["user", "assistant", "user"]);
  } finally {
    console.error = originalError;
    hook.unmount();
  }
});

// the panel's relay. One visible user message in the main session in the
// fixed wording, steered into a running main turn or sent as a plain prompt when idle; a
// running foreground subagent is first moved to the background (claude's background_tasks).
const RELAY_TEXT = "请用 SendMessage 把下面这段话原样转给 subagent aabe137615e4325e1（chain），不要做别的：\nalso say PONG";
function claudeRelayHarness({ streaming, answer = () => ({ success: true, data: null }) }) {
  const commands = [];
  const restored = [];
  globalThis.EventSource = class {
    readyState = 0;
    constructor() { queueMicrotask(() => { this.readyState = 1; this.onmessage?.({ data: JSON.stringify({ type: "connected", isStreaming: streaming }) }); }); }
    close() { this.readyState = 2; }
  };
  const info = { id: "c8", path: "", cwd: "/project", created: "", modified: "", messageCount: 0, firstMessage: "", harness: "claude", claude: { model: "claude-haiku-5-5", effort: "default" } };
  globalThis.fetch = async (url, init = {}) => {
    if (url === "/api/agent/c8" && init.body) {
      const body = JSON.parse(init.body);
      commands.push(body);
      const reply = answer(body);
      return new Response(JSON.stringify(reply), { status: reply.status ?? 200, headers: { "Content-Type": "application/json" } });
    }
    if (String(url).startsWith("/api/sessions/c8")) {
      return json({ sessionId: "c8", filePath: "", totalActiveMs: 0, tree: [], leafId: null, snapshotRevision: null, toolNames: [], info, context: { messages: [], entryIds: [], oldestEntryId: null, hasMore: false, turnsBefore: 0, thinkingLevel: "off", model: null } });
    }
    if (String(url).startsWith("/api/models")) return json({ models: {}, modelList: [], defaultModel: null });
    if (String(url).startsWith("/api/agent/c8")) return json({ running: streaming, ...(streaming ? { state: { isStreaming: true, isPromptRunning: true } } : {}) });
    return json({});
  };
  const chatInputRef = { current: { replaceMessage: () => {}, restoreSubmission: (...args) => restored.push(args) } };
  const hook = renderHook(useAgentSession, { session: info, newSessionCwd: null, newSessionDraftKey: null, chatInputRef, t: (key) => key });
  return { hook, commands, restored };
}
const relayRun = (over = {}) => ({ toolCallId: "toolu_chain", agentId: "aabe137615e4325e1", agentType: "general-purpose", description: "chain", background: false, status: "running", entries: [], ...over });

test("relaying to a running foreground subagent backgrounds its Agent call, then steers the relay message into the running turn", async () => {
  const { hook, commands, restored } = claudeRelayHarness({
    streaming: true,
    answer: (body) => (body.type === "background_task" ? { success: true, data: { backgrounded: true } } : { success: true, data: null }),
  });
  try {
    await settle();
    hook.flush();
    assert.equal(hook.result.agentRunning, true, "the main turn runs");
    await hook.result.relayToClaudeSubagent(relayRun(), "also say PONG");
    await settle();
    hook.flush();
    assert.deepEqual(commands, [
      { type: "background_task", toolCallId: "toolu_chain" },
      { type: "prompt", message: RELAY_TEXT, streamingBehavior: "steer" },
    ]);
    assert.deepEqual(restored, [], "nothing went to the main composer");
  } finally {
    hook.unmount();
  }
});

test("relaying to a finished subagent sends the relay message as a plain prompt and backgrounds nothing", async () => {
  const { hook, commands, restored } = claudeRelayHarness({ streaming: false });
  try {
    await settle();
    hook.flush();
    assert.equal(hook.result.agentRunning, false, "the main session is idle");
    await hook.result.relayToClaudeSubagent(relayRun({ status: "completed", result: "DONE" }), "also say PONG");
    await settle();
    hook.flush();
    assert.deepEqual(commands, [{ type: "prompt", message: RELAY_TEXT }]);
    assert.ok(hook.result.messages.some((message) => message.role === "user" && message.content === RELAY_TEXT), "the relay is a visible user message");
    assert.deepEqual(restored, []);
  } finally {
    hook.unmount();
  }
});

test("a relay that claude or the server refuses rejects, sends nothing after the refusal and leaves the main composer alone", async () => {
  const originalError = console.error;
  console.error = () => {};
  const notBackgrounded = claudeRelayHarness({
    streaming: true,
    answer: (body) => (body.type === "background_task" ? { success: true, data: { backgrounded: false } } : { success: true, data: null }),
  });
  try {
    await settle();
    notBackgrounded.hook.flush();
    await assert.rejects(notBackgrounded.hook.result.relayToClaudeSubagent(relayRun(), "also say PONG"));
    assert.deepEqual(notBackgrounded.commands, [{ type: "background_task", toolCallId: "toolu_chain" }], "no relay without backgrounded: true");
    await assert.rejects(notBackgrounded.hook.result.relayToClaudeSubagent(relayRun({ agentId: undefined }), "also say PONG"), "no agent id: nothing to address");
    await assert.rejects(notBackgrounded.hook.result.relayToClaudeSubagent(relayRun({ background: true }), "   "), "empty text");
    assert.equal(notBackgrounded.commands.length, 1);
    assert.deepEqual(notBackgrounded.restored, []);
  } finally {
    notBackgrounded.hook.unmount();
  }
  const refused = claudeRelayHarness({
    streaming: false,
    answer: () => ({ status: 500, error: "The claude process has exited", code: "prompt_rejected", accepted: false }),
  });
  try {
    await settle();
    refused.hook.flush();
    await assert.rejects(refused.hook.result.relayToClaudeSubagent(relayRun({ status: "completed" }), "also say PONG"), /claude process has exited/);
    await settle();
    refused.hook.flush();
    assert.deepEqual(refused.commands, [{ type: "prompt", message: RELAY_TEXT }]);
    assert.deepEqual(refused.restored, [], "the panel keeps its text; the main composer gets none");
  } finally {
    refused.hook.unmount();
    console.error = originalError;
  }
});
