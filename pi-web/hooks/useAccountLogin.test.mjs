import "#test-env";
import test from "node:test";
import assert from "node:assert/strict";
import { createJiti } from "jiti";
const shim = new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url).pathname;
const jiti = createJiti(import.meta.url, { alias: { react: shim } });
const { renderHook } = await import(shim);
const { useAccountLogin } = await jiti.import("./useAccountLogin.ts");
const settle = async () => { for (let n = 0; n < 8; n++) await new Promise((resolve) => setImmediate(resolve)); };
function setup() {
  const original = globalThis.fetch; const requests = []; let reads = 0;
  globalThis.fetch = (url, init) => new Promise((resolve) => requests.push({ url, init, resolve }));
  const hook = renderHook(() => useAccountLogin(() => reads++));
  return { hook, requests, reads: () => reads, answer: async (index, body, status = 200) => { requests[index].resolve(new Response(JSON.stringify(body), { status })); await settle(); hook.flush(); }, restore: () => { hook.unmount(); globalThis.fetch = original; } };
}
test("login opening deduplicates, failed POST is retryable, and exit requests a status read", async () => {
  const state = setup();
  try {
    void state.hook.result.open(); void state.hook.result.open(); state.hook.flush();
    assert.equal(state.requests.length, 1);
    assert.equal(state.hook.result.opening, true);
    assert.deepEqual(JSON.parse(state.requests[0].init.body), { login: { harness: "claude", id: "auth-login" } });
    await state.answer(0, { error: "fixture failed" }, 503);
    assert.equal(state.hook.result.error, "fixture failed");
    assert.equal(state.hook.result.opening, false);
    void state.hook.result.open();
    await state.answer(1, { id: "login-1" });
    state.hook.result.completed("wrong-id");
    assert.equal(state.reads(), 0);
    state.hook.result.completed("login-1"); state.hook.result.completed("login-1"); state.hook.flush();
    assert.equal(state.reads(), 1, "only the current terminal's first exit asks for native status");
    assert.equal(state.hook.result.checkedAfterExit, true);
    assert.equal("loggedIn" in state.hook.result, false, "terminal exit never manufactures auth success");
    state.hook.result.closed("login-1"); state.hook.flush();
    assert.equal(state.reads(), 2);
    assert.equal(state.hook.result.terminal, null);
  } finally { state.restore(); }
});
test("unmount while login opens disposes the late terminal instead of publishing stale state", async () => {
  const state = setup();
  void state.hook.result.open();
  state.hook.unmount();
  state.requests[0].resolve(new Response(JSON.stringify({ id: "late-login" })));
  await settle();
  assert.equal(state.requests.length, 2);
  assert.equal(state.requests[1].url, "/api/terminal/late-login");
  assert.equal(state.requests[1].init.method, "DELETE");
  assert.equal(state.reads(), 0);
  state.restore();
});
test("cancelled pending login cleans its late result and a newer request remains current", async () => {
  const state = setup();
  try {
    void state.hook.result.open(); state.hook.result.close(); state.hook.flush();
    void state.hook.result.open("setup-token");
    await state.answer(1, { id: "new-login" });
    await state.answer(0, { id: "old-login" });
    assert.equal(state.hook.result.terminal.id, "new-login");
    assert.equal(state.requests[2].url, "/api/terminal/old-login");
    assert.equal(state.requests[2].init.method, "DELETE");
    assert.equal(state.reads(), 0);
  } finally { state.restore(); }
});
