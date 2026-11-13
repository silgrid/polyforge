import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// after a compaction the header's context meter shows the new size
// without a reply. The meter renders the hook's `contextUsage` (ChatWindow hands it to
// the header) and `sessionStats.contextUsage`; both come from the session's state
// read. A compaction changes the context (pi: unknown until the next reply; claude: the
// size it reported it left) and a manual one is followed by no reply, so its end reads
// the usage once. Both harnesses end a compaction with pi's `compaction_end`.
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { useAgentSession } = await jiti.import("./useAgentSession.ts");

globalThis.document ??= Object.assign(new EventTarget(), { visibilityState: "visible" });
globalThis.window ??= new EventTarget();

const json = (body) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };

const HARNESSES = {
  pi: {
    info: (id) => ({ id, path: "", cwd: "/project", created: "", modified: "", messageCount: 2, firstMessage: "" }),
    before: { tokens: 40_000, percent: 40, contextWindow: 100_000 },
    // pi's getContextUsage() after a compaction: unknown until a reply reports usage.
    after: { tokens: null, percent: null, contextWindow: 100_000 },
    result: { summary: "summary", firstKeptEntryId: "e3", tokensBefore: 40_000 },
  },
  claude: {
    info: (id) => ({ id, path: "", cwd: "/project", created: "", modified: "", messageCount: 2, firstMessage: "", harness: "claude", claude: { model: "claude-haiku-5-5", effort: "default" } }),
    before: { tokens: 16_739, percent: 1.6739, contextWindow: 1_000_000 },
    // The compact/ recording's post_tokens.
    after: { tokens: 2_978, percent: 0.2978, contextWindow: 1_000_000 },
    result: { tokensBefore: 16_739 },
  },
};

// A hook left mounted keeps its timers (the lease renewal) and the process alive: a failed
// assertion must not hang the run, so every hook a test opened is unmounted after it.
const mounted = new Set();
test.afterEach(() => {
  for (const hook of mounted) hook.unmount();
  mounted.clear();
});

/** One open session: the server's usage is `server.usage`; `usageReads` counts the reads of it. */
function openSession(harness, id) {
  const { info } = HARNESSES[harness];
  const server = { usage: HARNESSES[harness].before, usageReads: 0, sources: [] };
  globalThis.EventSource = class {
    readyState = 0;
    constructor(url) {
      this.url = url;
      server.sources.push(this);
      queueMicrotask(() => { this.readyState = 1; this.onmessage?.({ data: JSON.stringify({ type: "connected", sessionId: id, isStreaming: false }) }); });
    }
    close() { this.readyState = 2; }
  };
  globalThis.fetch = async (url, init = {}) => {
    const path = String(url);
    const state = () => ({ sessionId: id, isStreaming: false, isPromptRunning: false, isCompacting: false, contextUsage: server.usage });
    if (path === `/api/sessions/${id}/state`) {
      server.usageReads += 1;
      return json({ running: true, state: state() });
    }
    if (path === `/api/agent/${id}?systemPrompt=0` || (path === `/api/agent/${id}` && !init.body)) {
      server.usageReads += 1;
      return json({ success: true, running: true, state: state() });
    }
    if (path.startsWith(`/api/sessions/${id}?`)) {
      return json({
        sessionId: id, filePath: "", totalActiveMs: 0, tree: [], leafId: null, snapshotRevision: null, toolNames: [], info: info(id),
        context: { messages: [], entryIds: [], oldestEntryId: null, hasMore: false, turnsBefore: 0, thinkingLevel: "off", model: null },
      });
    }
    if (path.startsWith("/api/models")) return json({ models: {}, modelList: [], defaultModel: null });
    return json({});
  };
  const rendered = renderHook(useAgentSession, { session: info(id), newSessionCwd: null, newSessionDraftKey: null, t: (key) => key });
  const hook = {
    get result() { return rendered.result; },
    flush: () => rendered.flush(),
    unmount: () => { if (mounted.delete(hook)) rendered.unmount(); },
  };
  mounted.add(hook);
  const emit = async (event) => {
    server.sources.at(-1).onmessage?.({ data: JSON.stringify(event) });
    await settle();
    hook.flush();
  };
  return { hook, server, emit };
}

for (const harness of Object.keys(HARNESSES)) {
  const { before, after, result } = HARNESSES[harness];

  test(`${harness}: a manual compaction's end shows the new context size without a reply`, async () => {
    const { hook, server, emit } = openSession(harness, `${harness}-1`);
    await settle();
    hook.flush();
    assert.deepEqual(hook.result.contextUsage, before, "the open session shows the pre-compaction size");

    await emit({ type: "compaction_start", reason: "manual" });
    assert.equal(hook.result.isCompacting, true);
    // The compaction runs on the server; its usage changes before compaction_end is sent.
    server.usage = after;
    const readsBefore = server.usageReads;
    await emit({ type: "compaction_end", reason: "manual", result, aborted: false, willRetry: false });
    assert.equal(hook.result.isCompacting, false);
    assert.deepEqual(hook.result.contextUsage, after, "the meter's value after the compaction, with no reply, reload or reconnect");
    assert.equal(server.usageReads - readsBefore, 1, "one read at the compaction's end, no polling");

    // No later read: nothing polls once the compaction ended.
    const settled = server.usageReads;
    await new Promise((resolve) => setTimeout(resolve, 50));
    await settle();
    assert.equal(server.usageReads, settled);
    hook.unmount();
  });

  test(`${harness}: a stopped or failed compaction changes nothing and reads nothing`, async () => {
    const { hook, server, emit } = openSession(harness, `${harness}-2`);
    await settle();
    hook.flush();
    assert.deepEqual(hook.result.contextUsage, before);
    for (const end of [
      { type: "compaction_end", reason: "manual", aborted: true, willRetry: false },
      { type: "compaction_end", reason: "manual", aborted: false, willRetry: false, errorMessage: "Compaction failed: nothing to compact" },
    ]) {
      await emit({ type: "compaction_start", reason: "manual" });
      const reads = server.usageReads;
      await emit(end);
      assert.equal(server.usageReads, reads, `${end.aborted ? "a stopped" : "a failed"} compaction read the usage`);
      assert.deepEqual(hook.result.contextUsage, before);
    }
    hook.unmount();
  });
}

// pi's own automatic compaction ends with the same event (reason threshold or overflow; the
// older auto_compaction_end pair too): its end reads the size as well. A claude session has
// no such event: claude compacts inside a turn, whose end reads the usage anyway.
test("pi: an automatic compaction's end reads the size too, in both event spellings", async () => {
  const { before, after, result } = HARNESSES.pi;
  for (const [start, end, reason] of [["compaction_start", "compaction_end", "threshold"], ["auto_compaction_start", "auto_compaction_end", "overflow"]]) {
    const { hook, server, emit } = openSession("pi", `pi-auto-${reason}`);
    await settle();
    hook.flush();
    assert.deepEqual(hook.result.contextUsage, before);
    await emit({ type: start, reason });
    server.usage = after;
    const reads = server.usageReads;
    await emit({ type: end, reason, result, aborted: false, willRetry: reason === "overflow" });
    assert.equal(server.usageReads - reads, 1, `${end} (${reason})`);
    assert.deepEqual(hook.result.contextUsage, after, `${end} (${reason})`);
    hook.unmount();
  }
});
