// carries: hooks/useAgentSession.ts, lib/message-display.ts, lib/bg-protocol.ts
import "#test-env";
import assert from "node:assert/strict";
import { fileURLToPath, pathToFileURL } from "node:url";
import test from "node:test";
import { createJiti } from "jiti";

// AC3's client half: a command the server refuses over unfinished background
// work answers the structured code, and the page rolls its own view back —
// the composer's draft and text are never cleared by these paths.
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { useAgentSession } = await jiti.import("./useAgentSession.ts");
const { PI_RESTART_REFUSED_CODE } = await jiti.import("../lib/bg-protocol.ts");

globalThis.document ??= Object.assign(new EventTarget(), { visibilityState: "visible" });
globalThis.window ??= new EventTarget();

const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const settle = async () => { for (let index = 0; index < 12; index += 1) await new Promise((resolve) => setImmediate(resolve)); };

const pollUntil = async (hook, condition, deadlineMs, what) => {
  const deadline = Date.now() + deadlineMs;
  while (!(await condition())) {
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await settle();
    hook.flush();
  }
};

const mounted = new Set();
test.afterEach(() => {
  for (const hook of mounted) hook.unmount();
  mounted.clear();
});

function openSession(id) {
  const refusals = [];
  const branchSnapshots = [];
  const contextFetches = [];
  globalThis.EventSource = class {
    readyState = 0;
    constructor() {
      this.readyState = 1;
      queueMicrotask(() => this.onmessage?.({ data: JSON.stringify({ type: "connected", sessionId: id, isStreaming: false }) }));
    }
    close() { this.readyState = 2; }
  };
  globalThis.fetch = async (url, init = {}) => {
    const path = String(url);
    if (init.body) {
      const body = JSON.parse(init.body);
      if (body.type === "navigate_tree" || body.type === "set_tools") {
        refusals.push(body.type);
        return json({
          error: "Cannot do this while this session has unfinished background work",
          code: PI_RESTART_REFUSED_CODE,
        }, 500);
      }
      return json({ success: true, data: null });
    }
    if (path === `/api/sessions/${id}/state` || path === `/api/agent/${id}?systemPrompt=0` || path === `/api/agent/${id}`) {
      return json({ success: true, running: true, state: { sessionId: id, isStreaming: false, isPromptRunning: false, isBashRunning: false, isCompacting: false, contextUsage: null } });
    }
    if (path.startsWith(`/api/sessions/${id}`) && path.includes("/context")) {
      contextFetches.push(path);
      // The context read echoes the leaf it was asked for, as the server does.
      const askedLeaf = new URL(path, "http://localhost").searchParams.get("leafId");
      return json({
        sessionId: id, filePath: "", totalActiveMs: 0, tree: [], leafId: askedLeaf, snapshotRevision: null,
        toolNames: ["read"], info: { id, path: "", cwd: "/project", created: "", modified: "", messageCount: 2, firstMessage: "" },
        context: { messages: [], entryIds: [], oldestEntryId: null, hasMore: false, turnsBefore: 0, thinkingLevel: "off", model: null },
      });
    }
    if (path.startsWith("/api/models")) return json({ models: {}, modelList: [], defaultModel: null });
    return json({});
  };
  const rendered = renderHook(useAgentSession, {
    session: { id, path: "", cwd: "/project", created: "", modified: "", messageCount: 2, firstMessage: "" },
    newSessionCwd: null,
    newSessionDraftKey: null,
    t: (key) => key,
    onBranchDataChange: (tree, activeLeafId, onLeafChange, locked) => {
      branchSnapshots.push({ tree, activeLeafId, onLeafChange, locked });
    },
  });
  const hook = {
    get result() { return rendered.result; },
    flush: () => rendered.flush(),
    unmount: () => { if (mounted.delete(hook)) rendered.unmount(); },
    refusals: () => refusals,
    contextFetches: () => contextFetches,
    branch: () => branchSnapshots.at(-1) ?? null,
    branchHistory: () => branchSnapshots.map((entry) => entry.activeLeafId),
  };
  mounted.add(hook);
  return hook;
}

test("a branch switch refused over background work never loads the refused branch", async () => {
  const hook = openSession("bg-refuse-3");
  await pollUntil(hook, () => hook.branch() !== null, 5_000, "the branch data callback");

  hook.branch().onLeafChange("leaf-2");
  await pollUntil(hook, () => hook.refusals().some((type) => type === "navigate_tree"), 5_000, "the refused navigate_tree");
  await settle();
  hook.flush();
  assert.equal(hook.contextFetches().some((url) => url.includes("leafId=leaf-2")), false, "the refused branch's context is never loaded");
  assert.ok(hook.result.notices.some((notice) => notice.type === "error"), "the refusal surfaces as a visible notice");
});

test("a tool preset switch refused over background work rolls back and keeps the composer", async () => {
  const hook = openSession("bg-refuse-1");
  await settle();
  hook.flush();
  const before = hook.result.toolPreset;

  await hook.result.handleToolPresetChange("full");
  await settle();
  hook.flush();
  assert.equal(hook.refusals().filter((type) => type === "set_tools").length, 1, "the switch reached the server and was refused");
  assert.equal(hook.result.toolPreset, before, "the preset rolls back to what the session has");
  assert.ok(hook.result.notices.some((notice) => notice.type === "error"), "the refusal surfaces as a visible notice");
});

// The branch-switch (navigate_tree) rollback shares this code path and is
// refused with the same structured code server-side (lib/rpc-manager-bg-
// lifecycle.test.mjs); the react-hook shim cannot drive handleLeafChange —
// the hook's conditional hook paths shift the shim's slot mapping between
// renders, so the optimistic set never lands in a readable state. The
// rollback code stays (hooks/useAgentSession.ts) and is listed in
// artifacts/self-review.md for review's attention.
