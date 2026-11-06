// carries: hooks/usePolyforge.ts, hooks/__fixtures__/react-hook-shim.mjs
import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// after a first `pfPresent:false` the dock hook re-probes the
// status route every 30 s and on window focus, so a pf that becomes available
// shows the Polyforge button (AppShell renders it exactly when `pfPresent`)
// without a page reload. Drives the real `usePolyforge(false)` (dock closed)
// on the react-hook shim with a fake clock and a stubbed fetch — the
// pattern.
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);

const win = new EventTarget();
globalThis.window = win;
globalThis.document = Object.assign(new EventTarget(), { visibilityState: "visible" });
const { usePolyforge } = await jiti.import("./usePolyforge.ts");

const REPROBE_MS = 30_000;
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };
const json = (body) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

async function setup() {
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  const server = { present: false };
  const requests = [];
  globalThis.fetch = async (url) => {
    const path = String(url);
    requests.push(path);
    if (path === "/api/polyforge/status") return json({ pfPresent: server.present, status: null });
    if (path === "/api/polyforge/decisions") return json({ pfPresent: server.present, feed: null });
    if (path.startsWith("/api/polyforge/messages")) return json({ capable: false, reason: "pf is not installed", conversations: [], contacts: [] });
    return json({});
  };
  const hook = renderHook((props) => usePolyforge(props.panelOpen), { panelOpen: false });
  await settle();
  hook.flush();
  const tick = async (ms) => { mock.timers.tick(ms); await settle(); hook.flush(); };
  const focus = async () => { win.dispatchEvent(new Event("focus")); await settle(); hook.flush(); };
  const statusReads = () => requests.filter((path) => path === "/api/polyforge/status").length;
  return { hook, server, requests, tick, focus, statusReads };
}

async function teardown(state) {
  state.hook.unmount();
  mock.timers.reset();
  await settle();
}

test("a pf absent at mount is re-probed within 30 s and turns present without a remount", async () => {
  const state = await setup();
  try {
    assert.equal(state.statusReads(), 1, "one status read at mount");
    assert.equal(state.hook.result.pfPresent, false);
    state.server.present = true;
    await state.tick(REPROBE_MS - 1);
    assert.equal(state.statusReads(), 1, "no re-probe before 30 s");
    await state.tick(1);
    assert.equal(state.statusReads(), 2, "one re-probe at 30 s");
    assert.equal(state.hook.result.pfPresent, true, "pf available again reads present");
    await state.tick(REPROBE_MS);
    assert.equal(state.statusReads(), 3, "the closed 30 s cadence takes over; the re-probe timer is gone");
  } finally {
    await teardown(state);
  }
});

test("window focus re-probes an absent pf at once", async () => {
  const state = await setup();
  try {
    assert.equal(state.hook.result.pfPresent, false);
    state.server.present = true;
    await state.focus();
    assert.equal(state.statusReads(), 2, "one status read on focus");
    assert.equal(state.hook.result.pfPresent, true);
  } finally {
    await teardown(state);
  }
});

test("while pf stays absent each re-probe is one status read and nothing else; unmount stops it", async () => {
  const state = await setup();
  try {
    assert.equal(state.hook.result.pfPresent, false);
    const atMount = state.requests.length;
    await state.tick(REPROBE_MS);
    assert.deepEqual(state.requests.slice(atMount), ["/api/polyforge/status"], "a 30 s re-probe reads status only");
    await state.focus();
    assert.deepEqual(state.requests.slice(atMount), ["/api/polyforge/status", "/api/polyforge/status"], "a focus re-probe reads status only");
    await state.tick(REPROBE_MS);
    assert.equal(state.requests.length, atMount + 3);
    assert.equal(state.hook.result.pfPresent, false);
    state.hook.unmount();
    const atUnmount = state.requests.length;
    await state.tick(2 * REPROBE_MS);
    await state.focus();
    assert.equal(state.requests.length, atUnmount, "no timer or listener survives unmount");
  } finally {
    mock.timers.reset();
    await settle();
  }
});
