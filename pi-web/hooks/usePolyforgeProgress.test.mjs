import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// Behavioural coverage of the job detail's progress hook, on
// the react-hook shim with a fake `document`, a fake clock (node:test
// mock.timers) and a stubbed fetch — the pattern. Every
// assertion reads the exported constants and the observable requests/state.
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { PROGRESS_POLL_MS, PROGRESS_REQUEST_TIMEOUT_MS, usePolyforgeProgress } = await jiti.import("./usePolyforgeProgress.ts");

const page = Object.assign(new EventTarget(), { visibilityState: "visible" });
globalThis.document = page;

const WI = "wi_progress01";
const OTHER = "wi_progress02";
const entry = (id, text = id, kind = "narration") => ({ id, kind, text });
const GOOD = { available: true, lines: [entry("a", "STEP-CODE"), entry("b", "bash: ls", "tool"), { id: "e", kind: "event", text: "00:00:01 ▶ code #1", tone: "accent", event: "step_started" }], omitted: 0 };
const NEXT = { available: true, lines: [entry("z", "STEP-REVIEW")], omitted: 2 };
const UNAVAILABLE = { available: false, lines: [], omitted: 0 };
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };
const setVisibility = (state) => {
  page.visibilityState = state;
  page.dispatchEvent(new Event("visibilitychange"));
};

async function setup({ wi = WI, running = true, visible = true } = {}) {
  page.visibilityState = visible ? "visible" : "hidden";
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  // `answer`: "ok" (the current `body`), "error" (HTTP 500), "offline" or "never".
  const server = { answer: "ok", body: GOOD };
  const requests = [];
  globalThis.fetch = async (url, init = {}) => {
    const request = { url, init, settled: false };
    requests.push(request);
    switch (server.answer) {
      case "never":
        return new Promise((_resolve, reject) => {
          init.signal?.addEventListener("abort", () => { request.settled = true; reject(new DOMException("aborted", "AbortError")); }, { once: true });
        });
      case "offline": request.settled = true; throw new TypeError("Failed to fetch");
      case "error": request.settled = true; return new Response(JSON.stringify({ error: "boom" }), { status: 500 });
      default: request.settled = true; return new Response(JSON.stringify(server.body), { status: 200, headers: { "Content-Type": "application/json" } });
    }
  };
  const hook = renderHook((props) => usePolyforgeProgress(props.wi, props.running), { wi, running });
  await settle();
  hook.flush();
  const tick = async (ms) => { mock.timers.tick(ms); await settle(); hook.flush(); };
  const rerender = async (props) => { hook.rerender(props); await settle(); hook.flush(); };
  const open = () => requests.filter((request) => !request.settled && !request.init.signal?.aborted).length;
  return { hook, server, requests, tick, rerender, open };
}

async function teardown(state) {
  state.hook.unmount();
  mock.timers.reset();
  page.visibilityState = "visible";
  await settle();
}

test("a fixed 2 s cadence and a bounded request", () => {
  assert.equal(PROGRESS_POLL_MS, 2000);
  assert.equal(PROGRESS_REQUEST_TIMEOUT_MS, 5000);
});

test("a running selection reads at once, then exactly once per 2 s tick", async () => {
  const state = await setup();
  try {
    assert.equal(state.requests.length, 1, "one immediate read");
    assert.equal(state.requests[0].url, `/api/polyforge/progress?wi=${WI}`);
    assert.equal(state.requests[0].init.cache, "no-store");
    assert.deepEqual(state.hook.result, GOOD);
    await state.tick(PROGRESS_POLL_MS - 1);
    assert.equal(state.requests.length, 1, "nothing before the interval");
    await state.tick(1);
    assert.equal(state.requests.length, 2);
    state.server.body = NEXT;
    await state.tick(PROGRESS_POLL_MS);
    assert.deepEqual(state.hook.result, NEXT, "a newer snapshot replaces the old one wholesale");
    for (let index = 0; index < 4; index++) await state.tick(PROGRESS_POLL_MS);
    assert.equal(state.requests.length, 7, "one request per tick, no second timer");
  } finally {
    await teardown(state);
  }
});

test("a non-running selection reads exactly once and is never polled", async () => {
  const state = await setup({ running: false });
  try {
    assert.equal(state.requests.length, 1, "one read");
    assert.deepEqual(state.hook.result, GOOD);
    await state.tick(PROGRESS_POLL_MS * 5);
    assert.equal(state.requests.length, 1, "no interval");
    setVisibility("hidden");
    setVisibility("visible");
    await settle();
    assert.equal(state.requests.length, 1, "visibility does not re-read a non-running selection");
    await state.rerender({ wi: WI, running: true });
    assert.equal(state.requests.length, 2, "becoming running reads at once");
    await state.tick(PROGRESS_POLL_MS);
    assert.equal(state.requests.length, 3, "and polls");
  } finally {
    await teardown(state);
  }
});

test("a hidden page issues no request; hiding aborts the one in flight; showing reads at once", async () => {
  const state = await setup({ visible: false });
  try {
    await state.tick(PROGRESS_POLL_MS * 3);
    assert.equal(state.requests.length, 0, "zero requests while hidden from mount");
    assert.equal(state.hook.result, null);
    state.server.answer = "never";
    setVisibility("visible");
    await settle();
    assert.equal(state.requests.length, 1, "becoming visible reads at once");
    setVisibility("hidden");
    assert.equal(state.requests[0].init.signal.aborted, true, "hiding aborts the request in flight");
    await state.tick(PROGRESS_POLL_MS * 3);
    assert.equal(state.requests.length, 1, "zero requests while hidden");
    state.server.answer = "ok";
    setVisibility("visible");
    await settle();
    state.hook.flush();
    assert.equal(state.requests.length, 2);
    assert.deepEqual(state.hook.result, GOOD);
    await state.tick(PROGRESS_POLL_MS);
    assert.equal(state.requests.length, 3, "and resumes the cadence");
  } finally {
    await teardown(state);
  }
});

test("a failed or degraded read keeps the last good snapshot; with none, degraded is the unavailable state", async () => {
  const state = await setup();
  try {
    for (const answer of ["error", "offline"]) {
      state.server.answer = answer;
      await state.tick(PROGRESS_POLL_MS);
      assert.deepEqual(state.hook.result, GOOD, `${answer}: the last good tail stays`);
    }
    state.server.answer = "ok";
    state.server.body = UNAVAILABLE;
    await state.tick(PROGRESS_POLL_MS);
    assert.deepEqual(state.hook.result, GOOD, "a degraded body keeps the last good snapshot");
    state.server.body = { available: true, entries: [entry("old")] };
    await state.tick(PROGRESS_POLL_MS);
    assert.deepEqual(state.hook.result, GOOD, "a malformed (-shaped) body keeps the last good snapshot");
  } finally {
    await teardown(state);
  }
  const fresh = await setup({ visible: false });
  try {
    fresh.server.body = UNAVAILABLE;
    setVisibility("visible");
    await settle();
    fresh.hook.flush();
    assert.deepEqual(fresh.hook.result, UNAVAILABLE);
  } finally {
    await teardown(fresh);
  }
});

test("a never-settling read is aborted at the bound and at most one request is ever open", async () => {
  const state = await setup();
  try {
    state.server.answer = "never";
    let peak = 0;
    for (let index = 0; index < 12; index++) {
      await state.tick(PROGRESS_POLL_MS / 2);
      peak = Math.max(peak, state.open());
    }
    assert.equal(peak, 1, "at most one request in flight");
    assert.ok(state.requests.slice(1, -1).every((request) => request.init.signal.aborted), "each stuck read was aborted");
    assert.deepEqual(state.hook.result, GOOD, "an aborted read writes nothing");
  } finally {
    await teardown(state);
  }
});

test("another job never shows the previous snapshot; no wi or a non-id reads nothing; unmount stops everything", async () => {
  const state = await setup();
  try {
    assert.deepEqual(state.hook.result, GOOD);
    state.server.answer = "never";
    await state.rerender({ wi: OTHER, running: true });
    assert.equal(state.hook.result, null, "the previous job's tail is not shown for the next job");
    assert.equal(state.requests.at(-1).url, `/api/polyforge/progress?wi=${OTHER}`);
    const count = state.requests.length;
    await state.rerender({ wi: null, running: true });
    assert.equal(state.requests[count - 1].init.signal.aborted, true, "deselect aborts the read in flight");
    await state.tick(PROGRESS_POLL_MS * 3);
    assert.equal(state.requests.length, count, "no wi, no read");
    await state.rerender({ wi: "task#210", running: true });
    await state.tick(PROGRESS_POLL_MS * 3);
    assert.equal(state.requests.length, count, "a slug is never sent as a wi");
    state.server.answer = "ok";
    await state.rerender({ wi: WI, running: true });
    assert.equal(state.requests.length, count + 1);
    state.hook.unmount();
    await state.tick(PROGRESS_POLL_MS * 3);
    assert.equal(state.requests.length, count + 1, "unmount stops the interval");
  } finally {
    mock.timers.reset();
    page.visibilityState = "visible";
    await settle();
  }
});

test(" (N5): a first read that fails outright is the unavailable state, never Loading; a later good read replaces it", async () => {
  for (const answer of ["error", "offline", "malformed", "never"]) {
    const state = await setup({ visible: false });
    try {
      state.server.answer = answer === "malformed" ? "ok" : answer;
      if (answer === "malformed") state.server.body = { available: true, entries: [] };
      setVisibility("visible");
      await settle();
      if (answer === "never") await state.tick(PROGRESS_REQUEST_TIMEOUT_MS);
      state.hook.flush();
      assert.deepEqual(state.hook.result, UNAVAILABLE, `${answer}: the first failed read is unavailable, not null (Loading)`);
      state.server.answer = "ok";
      state.server.body = GOOD;
      await state.tick(PROGRESS_POLL_MS);
      assert.deepEqual(state.hook.result, GOOD, `${answer}: a later good read replaces the unavailable state`);
    } finally {
      await teardown(state);
    }
  }
});

test(" (N5): hiding or deselecting during the first read writes nothing — only a failed read is unavailable", async () => {
  const state = await setup({ visible: false });
  try {
    state.server.answer = "never";
    setVisibility("visible");
    await settle();
    setVisibility("hidden");
    await settle();
    state.hook.flush();
    assert.equal(state.hook.result, null, "an abort by hiding is not a failed read");
  } finally {
    await teardown(state);
  }
});
