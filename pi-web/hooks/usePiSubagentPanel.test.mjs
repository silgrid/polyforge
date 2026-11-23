// carries: hooks/usePiSubagentPanel.ts, lib/pi-subagent-panel.ts, components/PiSubagentPanel.tsx
// polyforge#255 u3: one pi subagent panel's controller — the 2 s snapshot
// read (Q5), the steer through the one subagents route (Q2/Q5), the bounded
// 404 retry, and the session switch that drops a stale answer (Q4).
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { PiSubagentPanelController } = await jiti.import("./usePiSubagentPanel.ts");

const NOW = Date.UTC(2026, 10, 23, 12, 0, 0);
const iso = (offset) => new Date(NOW + offset).toISOString();

function runBody(fields = {}) {
  return {
    run: {
      sessionId: "child-1",
      parentSessionId: "parent-1",
      parentToolCallId: "toolu-agent-1",
      profile: "explore",
      description: "Find the parser",
      status: "running",
      createdAt: iso(-12_000),
      ...fields,
    },
  };
}

function sessionBody(messages, entryIds, { hasMore = false, cwd = "/repo" } = {}) {
  return {
    info: { cwd },
    context: { messages, entryIds, hasMore, oldestEntryId: entryIds[0] ?? null },
  };
}

const assistant = (text) => ({ role: "assistant", content: [{ type: "text", text }] });

function response(status, body) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

/** A fake fetch: `handle(url, init)` returns a Response; every call is recorded. */
function fakeFetch(handle) {
  const calls = [];
  const impl = async (url, init = {}) => {
    calls.push({ url: String(url), init });
    return handle(String(url), init);
  };
  return { impl, calls };
}

function controller(handle, fields = {}) {
  const fetch = fakeFetch(handle);
  const snapshots = [];
  const c = new PiSubagentPanelController({
    childSessionId: "child-1",
    parentSessionId: "parent-1",
    fetchImpl: fetch.impl,
    onChange: (snapshot) => snapshots.push(snapshot),
    ...fields,
  });
  return { c, fetch, snapshots };
}

test("polyforge#255 u3: load reads the run and the child transcript, then walks the family to the launch call", async () => {
  const { c, fetch, snapshots } = controller((url) => {
    if (url === "/api/subagents/child-1") {
      return response(200, runBody({ parentSessionId: "mid-1", parentToolCallId: "toolu-mid" }));
    }
    if (url === "/api/subagents/mid-1") {
      return response(200, runBody({ sessionId: "mid-1", parentSessionId: "parent-1", parentToolCallId: "toolu-top" }));
    }
    if (url === "/api/sessions/child-1") {
      return response(200, sessionBody([assistant("first"), assistant("second")], ["e-1", "e-2"]));
    }
    return response(404, { error: "not found" });
  });
  await c.load();
  const snapshot = c.snapshot;
  assert.equal(snapshot.phase, "ready", "a read run opens the panel");
  assert.deepEqual(snapshot.entries.map((entry) => entry.id), ["e-1", "e-2"], "the child transcript is the panel's body");
  assert.equal(snapshot.cwd, "/repo", "the child's cwd interprets its file links");
  assert.equal(snapshot.launchToolCallId, "toolu-top", "a grandchild jumps to the Agent call in this chat, not its immediate parent's");
  assert.ok(snapshots.length >= 1, "every read reports its snapshot");
  assert.ok(fetch.calls.some((call) => call.url === "/api/sessions/child-1"), "the transcript comes from the sessions route");
});

test("polyforge#255 u3: a refresh appends only new entries and re-reads the transcript once the run settled", async () => {
  let phase = 0;
  const { c } = controller((url) => {
    if (url === "/api/subagents/child-1") {
      phase += 1;
      return response(200, runBody(phase === 1 ? {} : { status: "completed", completedAt: iso(-1_000) }));
    }
    return response(200, sessionBody(
      phase === 1 ? [assistant("first")] : [assistant("first"), assistant("second")],
      phase === 1 ? ["e-1"] : ["e-1", "e-2"],
    ));
  });
  await c.load();
  assert.deepEqual(c.snapshot.entries.map((entry) => entry.id), ["e-1"]);
  assert.equal(c.snapshot.run.status, "running");
  await c.refresh();
  assert.deepEqual(c.snapshot.entries.map((entry) => entry.id), ["e-1", "e-2"], "the new entry arrives without losing the loaded one");
  assert.equal(c.snapshot.run.status, "completed", "the settled status reaches the header");
  assert.equal(c.snapshot.steerError, null);
});

test("polyforge#255 u3: steer posts the one subagents steer request; a refusal keeps the text's failure visible and re-reads", async () => {
  let steerStatus = 409;
  const { c, fetch } = controller((url, init) => {
    if (init.method === "POST") {
      return steerStatus === 200
        ? response(200, { ok: true, ...runBody() })
        : response(steerStatus, { error: "Subagent is not running" });
    }
    if (url === "/api/subagents/child-1") return response(200, runBody());
    return response(200, sessionBody([assistant("first")], ["e-1"]));
  });
  await c.load();
  const before = fetch.calls.filter((call) => call.init.method === "POST").length;
  const refused = await c.steer("focus tests");
  assert.equal(refused, false, "a refused steer reports failure so the box keeps the draft");
  const post = fetch.calls.find((call) => call.init.method === "POST");
  assert.equal(post.url, "/api/subagents/child-1", "the message goes to the run's own subagents route");
  assert.deepEqual(JSON.parse(post.init.body), { action: "steer", message: "focus tests" }, "the body is the existing steer action, never a composer prompt");
  assert.equal(fetch.calls.filter((call) => call.init.method === "POST").length, before + 1, "one click sends once");
  assert.match(c.snapshot.steerError, /not running/, "the server's refusal is shown");

  steerStatus = 200;
  const sent = await c.steer("second try");
  assert.equal(sent, true, "a success clears the failure");
  assert.equal(c.snapshot.steerError, null);
});

test("polyforge#255 u3: a controller disposed before its answer lands drops it (a session switch never shows another child)", async () => {
  let release;
  const pending = new Promise((resolve) => { release = resolve; });
  const { c, snapshots } = controller(async (url) => {
    await pending;
    return url.startsWith("/api/subagents/") ? response(200, runBody()) : response(200, sessionBody([assistant("late")], ["e-late"]));
  });
  const loading = c.load();
  c.dispose();
  release();
  await loading;
  assert.deepEqual(c.snapshot.entries, [], "the disposed controller never applies the late transcript");
  assert.ok(snapshots.every((snapshot) => snapshot.entries.length === 0), "no late snapshot reaches the panel");
});

test("polyforge#255 u3: a missing run retries a bounded number of times, then reads as missing", async () => {
  const { c } = controller(() => response(404, { error: "Subagent not found" }), { missingRetries: 3 });
  await c.load();
  assert.equal(c.snapshot.phase, "loading", "the first 404 is retried, not shown");
  await c.refresh();
  await c.refresh();
  assert.equal(c.snapshot.phase, "loading", "the third try still retries");
  await c.refresh();
  assert.equal(c.snapshot.phase, "missing", "after the bounded retries the panel says the run does not exist");
});