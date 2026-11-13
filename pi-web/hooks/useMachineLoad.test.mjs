import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// Behavioural coverage of the machine-load hook, replacing the
// earlier source-regex pins: the hook runs on the react-hook shim
// (hooks/__fixtures__/react-hook-shim.mjs) against a fake `document`, a fake
// clock (node:test mock.timers) and a stubbed fetch. Every assertion reads the
// exported constants and observable requests/state, never the source text.
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { MACHINE_LOAD_POLL_MS, MACHINE_LOAD_REQUEST_TIMEOUT_MS, useMachineLoad } = await jiti.import("./useMachineLoad.ts");

const page = Object.assign(new EventTarget(), { visibilityState: "visible" });
globalThis.document = page;

const GOOD = { cpuPercent: 12.5, coreCount: 4, loadavg: [1, 2, 3], memoryUsedBytes: 1024, memoryTotalBytes: 4096 };
const NEXT = { cpuPercent: 50, coreCount: 8, loadavg: [0.5, 0.25, 0.125], memoryUsedBytes: 2048, memoryTotalBytes: 4096 };
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };
const setVisibility = (state) => {
  page.visibilityState = state;
  page.dispatchEvent(new Event("visibilitychange"));
};

async function setup({ visible = true } = {}) {
  page.visibilityState = visible ? "visible" : "hidden";
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  // `answer` decides the next replies: "ok" (the current `metrics`), "error"
  // (HTTP 500), "nonjson" (200 with a non-JSON body), "offline" (a network
  // failure) or "never" (never settles; it only ends when aborted).
  const server = { answer: "ok", metrics: GOOD };
  const requests = [];
  globalThis.fetch = async (url, init = {}) => {
    const request = { url, init, settled: false };
    requests.push(request);
    const done = (value) => { request.settled = true; return value; };
    switch (server.answer) {
      case "never":
        return new Promise((_resolve, reject) => {
          init.signal?.addEventListener("abort", () => { request.settled = true; reject(new DOMException("aborted", "AbortError")); }, { once: true });
        });
      case "offline": request.settled = true; throw new TypeError("Failed to fetch");
      case "error": return done(new Response("boom", { status: 500 }));
      case "nonjson": return done(new Response("<html>", { status: 200 }));
      default: return done(new Response(JSON.stringify({ metrics: server.metrics }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }
  };
  const hook = renderHook(useMachineLoad);
  await settle();
  hook.flush();
  const tick = async (ms) => { mock.timers.tick(ms); await settle(); hook.flush(); };
  // At most one request may be open (neither settled nor aborted) at a time.
  const open = () => requests.filter((request) => !request.settled && !request.init.signal?.aborted).length;
  return { hook, server, requests, tick, open };
}

async function teardown(state) {
  state.hook.unmount();
  mock.timers.reset();
  page.visibilityState = "visible";
  await settle();
}

test("the request bound equals one poll interval", () => {
  assert.equal(MACHINE_LOAD_POLL_MS, 2000);
  assert.equal(MACHINE_LOAD_REQUEST_TIMEOUT_MS, MACHINE_LOAD_POLL_MS);
});

test("mount while visible fetches once at once, then exactly one request per poll tick", async () => {
  const state = await setup();
  try {
    assert.equal(state.requests.length, 1, "one immediate request on mount");
    assert.equal(state.requests[0].url, "/api/machine-load");
    assert.equal(state.requests[0].init.cache, "no-store");
    assert.deepEqual(state.hook.result, GOOD, "a whitelisted success becomes the state");
    await state.tick(MACHINE_LOAD_POLL_MS - 1);
    assert.equal(state.requests.length, 1, "nothing before the interval");
    await state.tick(1);
    assert.equal(state.requests.length, 2, "one request at the tick");
    state.server.metrics = NEXT;
    await state.tick(MACHINE_LOAD_POLL_MS);
    assert.equal(state.requests.length, 3);
    assert.deepEqual(state.hook.result, NEXT, "each fresh success replaces the state");
    for (let index = 0; index < 5; index++) await state.tick(MACHINE_LOAD_POLL_MS);
    assert.equal(state.requests.length, 8, "one request per tick, no second timer");
  } finally {
    await teardown(state);
  }
});

test("a hidden page issues no request; hiding aborts the request in flight; showing reads at once", async () => {
  const state = await setup({ visible: false });
  try {
    await state.tick(MACHINE_LOAD_POLL_MS * 3);
    assert.equal(state.requests.length, 0, "zero requests while hidden from mount");
    assert.equal(state.hook.result, null);
    state.server.answer = "never";
    setVisibility("visible");
    await settle();
    assert.equal(state.requests.length, 1, "becoming visible fetches once immediately");
    setVisibility("hidden");
    assert.equal(state.requests[0].init.signal.aborted, true, "hiding aborts the request in flight");
    await state.tick(MACHINE_LOAD_POLL_MS * 3);
    assert.equal(state.requests.length, 1, "zero requests while hidden");
    state.server.answer = "ok";
    setVisibility("visible");
    await settle();
    state.hook.flush();
    assert.equal(state.requests.length, 2, "the return reads at once");
    assert.deepEqual(state.hook.result, GOOD);
    await state.tick(MACHINE_LOAD_POLL_MS);
    assert.equal(state.requests.length, 3, "and resumes the cadence");
  } finally {
    await teardown(state);
  }
});

test("a failed tick (HTTP error, non-JSON, network failure, metrics null) keeps the last good values", async () => {
  const state = await setup();
  try {
    assert.deepEqual(state.hook.result, GOOD);
    for (const answer of ["error", "nonjson", "offline"]) {
      state.server.answer = answer;
      const before = state.requests.length;
      await state.tick(MACHINE_LOAD_POLL_MS);
      assert.equal(state.requests.length, before + 1, `${answer}: the tick requested`);
      assert.deepEqual(state.hook.result, GOOD, `${answer}: no placeholder flash, the last good value stays`);
    }
    state.server.answer = "ok";
    state.server.metrics = null;
    await state.tick(MACHINE_LOAD_POLL_MS);
    assert.deepEqual(state.hook.result, GOOD, "metrics: null keeps the last good value");
    state.server.metrics = { ...NEXT, coreCount: 0 };
    await state.tick(MACHINE_LOAD_POLL_MS);
    assert.deepEqual(state.hook.result, GOOD, "a malformed body keeps the last good value");
  } finally {
    await teardown(state);
  }
});

test("with no good value yet a failure leaves the state null (the view's unavailable text)", async () => {
  const state = await setup({ visible: false });
  try {
    state.server.answer = "error";
    setVisibility("visible");
    await settle();
    state.hook.flush();
    assert.equal(state.requests.length, 1);
    assert.equal(state.hook.result, null);
  } finally {
    await teardown(state);
  }
});

test("a never-settling request is aborted at the bound, writes nothing, and frees the slot", async () => {
  const state = await setup();
  try {
    assert.deepEqual(state.hook.result, GOOD);
    state.server.answer = "never";
    await state.tick(MACHINE_LOAD_POLL_MS);
    assert.equal(state.requests.length, 2, "the tick requested");
    const stuck = state.requests[1];
    await state.tick(MACHINE_LOAD_REQUEST_TIMEOUT_MS - 1);
    assert.equal(stuck.init.signal.aborted, false, "not aborted before the bound");
    // The bound and the next poll tick fall due together; the fake clock runs
    // both before any promise continuation, so that tick still finds the
    // request in flight and skips it — at most one request is ever open.
    await state.tick(1);
    assert.equal(stuck.init.signal.aborted, true, "aborted at MACHINE_LOAD_REQUEST_TIMEOUT_MS");
    assert.equal(state.requests.length, 2, "a tick that finds a request in flight skips");
    assert.deepEqual(state.hook.result, GOOD, "the timed-out request writes nothing: the last good value stays");
    state.server.answer = "ok";
    state.server.metrics = NEXT;
    await state.tick(MACHINE_LOAD_POLL_MS);
    assert.equal(state.requests.length, 3, "the freed slot lets the next tick request again");
    assert.deepEqual(state.hook.result, NEXT);
  } finally {
    await teardown(state);
  }
});

test("never two requests open at once", async () => {
  const state = await setup();
  try {
    state.server.answer = "never";
    let peak = 0;
    for (let index = 0; index < 12; index++) {
      await state.tick(MACHINE_LOAD_POLL_MS / 2);
      peak = Math.max(peak, state.open());
    }
    assert.equal(peak, 1, "at most one request in flight");
    assert.ok(state.requests.length >= 3, `requests keep coming after each bound (${state.requests.length})`);
  } finally {
    await teardown(state);
  }
});

test("unmount removes the listener, clears the timer and aborts the request in flight", async () => {
  const state = await setup();
  try {
    state.server.answer = "never";
    await state.tick(MACHINE_LOAD_POLL_MS);
    const pending = state.requests.at(-1);
    state.hook.unmount();
    assert.equal(pending.init.signal.aborted, true, "unmount aborts");
    const count = state.requests.length;
    await state.tick(MACHINE_LOAD_POLL_MS * 3);
    setVisibility("hidden");
    setVisibility("visible");
    await settle();
    assert.equal(state.requests.length, count, "no timer and no listener survive the unmount");
  } finally {
    mock.timers.reset();
    page.visibilityState = "visible";
  }
});
