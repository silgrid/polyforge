import "#test-env";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// the view tracks how many turns of the branch precede the
// oldest loaded entry (the server's `turnsBefore`), the offset the minimap
// numbers turns from. It moves with the history cursor (fresh read, older
// page, cached snapshot, unmount persist) and a snapshot written without it
// reads as zero.
// one copy of that fact — the context page's `turnsBefore`;
// the returned `earlierTurnCount` is derived from it, never kept beside it.
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { useAgentSession } = await jiti.import("./useAgentSession.ts");
const { deleteSessionViewSnapshot, getSessionViewSnapshot, setSessionViewSnapshot } = await jiti.import("@/lib/session-view-cache");

globalThis.document ??= Object.assign(new EventTarget(), { visibilityState: "visible" });
globalThis.window ??= new EventTarget();
globalThis.requestAnimationFrame ??= () => 0;
globalThis.cancelAnimationFrame ??= () => {};

const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };
const user = (text) => ({ role: "user", content: text, timestamp: 1 });

function seedSnapshot(extra = {}) {
  setSessionViewSnapshot({
    sessionId: "A",
    revision: "r:cached",
    messages: [user("m5"), user("m6")],
    entryIds: ["e5", "e6"],
    leafId: null,
    oldestEntryId: "e5",
    hasMore: true,
    thinkingLevel: "off",
    model: null,
    loadedEntryIds: ["e5", "e6"],
    ...extra,
  });
}

// `read` answers the session read (held behind `gate` when set); `older`
// answers a /context page request.
async function mount({ read, gate = null, older = null }) {
  mock.timers.enable({ apis: ["setTimeout", "setInterval", "Date"] });
  const sources = [];
  globalThis.EventSource = class {
    readyState = 0;
    onmessage = null;
    onerror = null;
    constructor(url) { this.url = url; sources.push(this); }
    close() { this.readyState = 2; }
  };
  globalThis.fetch = async (requestUrl) => {
    const url = String(requestUrl);
    if (url.startsWith("/api/sessions/A/context?")) return json({ context: older });
    if (url.startsWith("/api/sessions/A?")) {
      const body = json({ sessionId: "A", filePath: "/A.jsonl", leafId: null, tree: [], treeFormat: "summary", ...read });
      if (gate) await gate.promise;
      return body;
    }
    if (url === "/api/sessions/A/state") return json({ running: false, state: null });
    if (url.startsWith("/api/models")) return json({ models: {}, modelList: [] });
    return json({});
  };
  const originalError = console.error;
  console.error = () => {};
  const hook = renderHook(useAgentSession, {
    session: { id: "A", cwd: "/project", path: "/A.jsonl", name: "A" },
    newSessionCwd: null,
    newSessionDraftKey: null,
    t: (key) => key,
  });
  const flush = async () => { await settle(); hook.flush(); };
  return {
    hook,
    flush,
    teardown: () => {
      hook.unmount();
      for (const source of sources) source.close();
      mock.timers.reset();
      console.error = originalError;
      deleteSessionViewSnapshot("A");
    },
  };
}

const freshRead = (turnsBefore) => ({
  snapshotRevision: "r:fresh",
  context: {
    messages: [user("m5"), user("m6")],
    entryIds: ["e5", "e6"],
    oldestEntryId: "e5",
    hasMore: true,
    turnsBefore,
    thinkingLevel: "off",
    model: null,
  },
});

test("a cached snapshot without turnsBefore mounts with no earlier-turn offset", async () => {
  seedSnapshot();
  const gate = Promise.withResolvers();
  const env = await mount({ read: freshRead(9), gate });
  try {
    await env.flush();
    assert.equal(env.hook.result.historyCursor, "e5", "the cached window was restored");
    assert.equal(env.hook.result.earlierTurnCount, 0);
    assert.equal(env.hook.result.data.context.turnsBefore, 0);
  } finally {
    gate.resolve();
    await env.flush();
    env.teardown();
  }
});

test("a cached snapshot restores its earlier-turn offset", async () => {
  seedSnapshot({ turnsBefore: 2475 });
  const gate = Promise.withResolvers();
  const env = await mount({ read: freshRead(9), gate });
  try {
    await env.flush();
    assert.equal(env.hook.result.earlierTurnCount, 2475);
    assert.equal(env.hook.result.data.context.turnsBefore, 2475);
  } finally {
    gate.resolve();
    await env.flush();
    env.teardown();
  }
});

test("the fresh read's turnsBefore becomes the offset and is stored with its cursor", async () => {
  const env = await mount({ read: freshRead(2475) });
  try {
    await env.flush();
    assert.equal(env.hook.result.historyCursor, "e5");
    assert.equal(env.hook.result.earlierTurnCount, 2475);
    assert.equal(env.hook.result.data.context.turnsBefore, 2475);
    const stored = getSessionViewSnapshot("A");
    assert.equal(stored?.oldestEntryId, "e5");
    assert.equal(stored?.turnsBefore, 2475);
  } finally {
    env.teardown();
  }
});

test("an older page sets its own offset, and the unmount snapshot stores it with the cursor", async () => {
  const older = {
    messages: [user("m3"), user("m4")],
    entryIds: ["e3", "e4"],
    oldestEntryId: "e3",
    hasMore: true,
    turnsBefore: 2450,
    thinkingLevel: "off",
    model: null,
  };
  const env = await mount({ read: freshRead(2475), older });
  try {
    await env.flush();
    assert.equal(env.hook.result.earlierTurnCount, 2475);
    await env.hook.result.loadContext("A", null, "e5", { tail: 2 });
    await env.flush();
    assert.equal(env.hook.result.historyCursor, "e3");
    assert.deepEqual(env.hook.result.entryIds, ["e3", "e4", "e5", "e6"]);
    assert.equal(env.hook.result.earlierTurnCount, 2450);
    assert.equal(env.hook.result.data.context.turnsBefore, 2450);
    // Nothing cached to cover: the unmount persists the paged-in view whole.
    deleteSessionViewSnapshot("A");
    env.hook.unmount();
    const stored = getSessionViewSnapshot("A");
    assert.equal(stored?.oldestEntryId, "e3");
    assert.equal(stored?.turnsBefore, 2450);
  } finally {
    env.teardown();
  }
});

test("a same-revision read fills the offset a snapshot was stored without", async () => {
  // The hover prefetch wrote snapshots without turnsBefore; the background
  // read at the same revision keeps the cached window and must still take the
  // offset from the page owning its cursor (review s11#1).
  seedSnapshot();
  const gate = Promise.withResolvers();
  const env = await mount({ read: { ...freshRead(2475), snapshotRevision: "r:cached" }, gate });
  try {
    await env.flush();
    assert.equal(env.hook.result.earlierTurnCount, 0, "the snapshot carried no offset");
    gate.resolve();
    await env.flush();
    assert.equal(env.hook.result.historyCursor, "e5");
    assert.equal(env.hook.result.earlierTurnCount, 2475);
    assert.equal(env.hook.result.data.context.turnsBefore, 2475);
    assert.equal(getSessionViewSnapshot("A")?.turnsBefore, 2475);
    env.hook.unmount();
    const stored = getSessionViewSnapshot("A");
    assert.equal(stored?.oldestEntryId, "e5");
    assert.equal(stored?.turnsBefore, 2475);
  } finally {
    env.teardown();
  }
});

test("the hook keeps one copy of the offset, derived from its context page", () => {
  const source = readFileSync(new URL("./useAgentSession.ts", import.meta.url), "utf8");
  assert.match(source, /const earlierTurnCount = data\?\.context\.turnsBefore \?\? 0;/, "the returned offset is derived from data.context.turnsBefore");
  assert.doesNotMatch(source, /setEarlierTurnCount|earlierTurnCountRef/, "no second state or ref mirrors it");
});
