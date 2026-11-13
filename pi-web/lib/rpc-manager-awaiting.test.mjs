import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const {
  AgentSessionWrapper,
  getAwaitingRpcSessionIds,
  getRunningRpcSessionIds,
} = await jiti.import("./rpc-manager.ts");

function createWrapper(overrides = {}) {
  const inner = {
    sessionId: `awaiting-test-${Math.random().toString(36).slice(2)}`,
    isStreaming: false, isCompacting: false, isBashRunning: false, isIdle: true,
    sessionManager: { getCwd: () => "/tmp" },
    agent: { state: {}, abort() {} },
    abortRetry() {}, abortCompaction() {}, abortBranchSummary() {}, dispose() {},
    ...overrides,
  };
  return new AgentSessionWrapper(inner);
}

/** Marks the wrapper as running the way real traffic does (an in-flight prompt). */
function markRunning(wrapper) {
  wrapper.pendingPromptCount += 1;
}

test("awaiting input means running plus an unanswered blocking extension dialog", () => {
  const wrapper = createWrapper();
  try {
    assert.equal(wrapper.isAwaitingInput(), false, "idle with no dialog is not awaiting");

    markRunning(wrapper);
    assert.equal(wrapper.isRunning(), true);
    assert.equal(wrapper.isAwaitingInput(), false, "working with no dialog is not awaiting");

    wrapper.pendingUiRequests.set("select-1", { type: "extension_ui_request", id: "select-1", method: "select", title: "Pick" });
    assert.equal(wrapper.isAwaitingInput(), true, "select dialog parks the run");
    wrapper.pendingUiRequests.delete("select-1");

    wrapper.pendingUiRequests.set("confirm-1", { type: "extension_ui_request", id: "confirm-1", method: "confirm", title: "Sure?" });
    assert.equal(wrapper.isAwaitingInput(), true, "confirm dialog parks the run");
    wrapper.pendingUiRequests.delete("confirm-1");

    wrapper.pendingUiRequests.set("custom-1", { type: "extension_ui_request", id: "custom-1", method: "custom", lines: ["x"] });
    assert.equal(wrapper.isAwaitingInput(), true, "open custom overlay parks the run");
    wrapper.pendingUiRequests.set("custom-1", { type: "extension_ui_request", id: "custom-1", method: "custom", lines: [], closed: true });
    assert.equal(wrapper.isAwaitingInput(), false, "closed custom overlay does not park the run");
    wrapper.pendingUiRequests.delete("custom-1");

    wrapper.pendingUiRequests.set("notify-1", { type: "extension_ui_request", id: "notify-1", method: "notify", message: "hi" });
    assert.equal(wrapper.isAwaitingInput(), false, "non-blocking notify does not park the run");
    wrapper.pendingUiRequests.delete("notify-1");

    wrapper.pendingUiRequests.set("select-2", { type: "extension_ui_request", id: "select-2", method: "select", title: "Pick" });
    wrapper.pendingPromptCount = 0;
    assert.equal(wrapper.isRunning(), false);
    assert.equal(wrapper.isAwaitingInput(), false, "idle overlay that outlives its turn is not awaiting");
  } finally {
    wrapper.destroy();
  }
});

test("getAwaitingRpcSessionIds lists only running sessions parked on a blocking dialog", () => {
  const previousRegistry = globalThis.__piSessions;
  globalThis.__piSessions = new Map();
  const working = createWrapper({ sessionId: "awaiting-working" });
  const parked = createWrapper({ sessionId: "awaiting-parked" });
  const idle = createWrapper({ sessionId: "awaiting-idle" });
  try {
    markRunning(working);
    markRunning(parked);
    parked.pendingUiRequests.set("select-1", { type: "extension_ui_request", id: "select-1", method: "select", title: "Pick" });
    idle.pendingUiRequests.set("select-2", { type: "extension_ui_request", id: "select-2", method: "select", title: "Pick" });
    globalThis.__piSessions.set(working.sessionId, working);
    globalThis.__piSessions.set(parked.sessionId, parked);
    globalThis.__piSessions.set(idle.sessionId, idle);

    assert.deepEqual(getRunningRpcSessionIds().sort(), ["awaiting-parked", "awaiting-working"]);
    assert.deepEqual(getAwaitingRpcSessionIds(), ["awaiting-parked"]);

    parked.pendingUiRequests.delete("select-1");
    assert.deepEqual(getAwaitingRpcSessionIds(), [], "answering the dialog clears the badge");
  } finally {
    globalThis.__piSessions = previousRegistry;
    for (const wrapper of [working, parked, idle]) wrapper.destroy();
  }
});
