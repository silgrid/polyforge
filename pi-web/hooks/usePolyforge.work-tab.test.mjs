// carries: hooks/usePolyforge.ts, hooks/__fixtures__/react-hook-shim.mjs
import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// Work's sub-tab is page memory across one opening only: a dock close, and a
// switch to Work from another top-level tab, both start on Jobs; an explicit
// target (the attention walk, an anchor, an analysis card's task link) writes
// after that reset and is not overwritten by the open. These drive the real
// `usePolyforge` on the react-hook shim with a stubbed fetch (the pattern
// hooks/usePolyforge.reprobe.test.mjs established).
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);

const win = new EventTarget();
globalThis.window = win;
globalThis.document = Object.assign(new EventTarget(), { visibilityState: "visible" });
const { usePolyforge } = await jiti.import("./usePolyforge.ts");

const TASK_ID = "task_0123456789abcdef";
// An open feed entry of a background task: the walk's task target.
const TASK_DECISION = {
  id: 7, at: "2026-11-22T00:00:00.000Z", kind: "task", task_id: TASK_ID, class: "failed",
  attention: true, resolved: false, dismissed: false, replied: false,
};
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };
const json = (body) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

async function setup({ panelOpen = true, decisions = [] } = {}) {
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  globalThis.fetch = async (url) => {
    const path = String(url);
    if (path === "/api/polyforge/status") {
      return json({ pfPresent: true, status: { queue: { running: 0, max: 4 }, tasks: { running: 0, completed: 0 }, jobs: [] } });
    }
    if (path === "/api/polyforge/decisions") {
      return json({ pfPresent: true, feed: { decisions, attention: decisions.filter((entry) => entry.attention && !entry.resolved && !entry.dismissed).length } });
    }
    if (path.startsWith("/api/polyforge/messages")) return json({ capable: false, reason: "pf is not installed", conversations: [], contacts: [], groups: [] });
    if (path.startsWith("/api/polyforge/cards")) return json({ cards: [] });
    return json({});
  };
  const hook = renderHook((props) => usePolyforge(props.panelOpen), { panelOpen });
  await settle();
  hook.flush();
  return hook;
}

async function teardown(hook) {
  hook.unmount();
  mock.timers.reset();
  await settle();
}

async function render(hook) {
  await settle();
  hook.flush();
  await settle();
}

test("a dock close resets Work's sub-tab to Jobs for every other sub-tab", async () => {
  const hook = await setup({ panelOpen: true });
  try {
    for (const tab of ["tasks", "items", "projects", "integrate"]) {
      hook.result.setWorkTab(tab);
      await render(hook);
      assert.equal(hook.result.workTab, tab, `${tab} is the sub-tab while the dock is open`);
      hook.rerender({ panelOpen: false });
      await render(hook);
      hook.rerender({ panelOpen: true });
      await render(hook);
      assert.equal(hook.result.workTab, "jobs", `a dock closed on ${tab} reopens on 作业`);
    }
  } finally {
    await teardown(hook);
  }
});

test("switching to Work from Memory or Messages lands on Jobs", async () => {
  const hook = await setup({ panelOpen: true });
  try {
    for (const other of ["memory", "messages"]) {
      hook.result.setWorkTab("tasks");
      await render(hook);
      hook.result.setActiveTab(other);
      await render(hook);
      assert.equal(hook.result.activeTab, other, `${other} is the top-level tab`);
      hook.result.setActiveTab("work");
      await render(hook);
      assert.equal(hook.result.activeTab, "work", `switching back shows 工单`);
      assert.equal(hook.result.workTab, "jobs", `switching to 工单 from ${other} starts on 作业`);
    }
  } finally {
    await teardown(hook);
  }
});

test("re-activating Work while it is already shown keeps the manual sub-tab", async () => {
  const hook = await setup({ panelOpen: true });
  try {
    hook.result.setWorkTab("projects");
    await render(hook);
    hook.result.setActiveTab("work");
    await render(hook);
    assert.equal(hook.result.activeTab, "work");
    assert.equal(hook.result.workTab, "projects", "a click on the already shown 工单 tab resets nothing");
  } finally {
    await teardown(hook);
  }
});

test("a manual sub-tab switch holds through re-renders while the dock stays open", async () => {
  const hook = await setup({ panelOpen: true });
  try {
    hook.result.setWorkTab("projects");
    await render(hook);
    for (let tick = 0; tick < 3; tick++) {
      hook.rerender({ panelOpen: true });
      await render(hook);
      assert.equal(hook.result.workTab, "projects", `a poll re-render (${tick + 1}) keeps the manual sub-tab`);
    }
  } finally {
    await teardown(hook);
  }
});

test("the attention walk's task target survives the same-batch open", async () => {
  const hook = await setup({ panelOpen: false, decisions: [TASK_DECISION] });
  try {
    assert.equal(hook.result.attentionCount, 1, "the feed's one open task entry needs a human");
    hook.result.openNextAttention();
    await render(hook);
    assert.equal(hook.result.workTab, "tasks", "the walk sends a task target to 任务");
    assert.equal(hook.result.taskRequest?.id, TASK_ID, "and hands that task to the Tasks surface");
    // AppShell's openPfNeedsInput calls the walk and setPfPanelOpen(true) in
    // one batch: opening the dock must not overwrite the walk's target.
    hook.rerender({ panelOpen: true });
    await render(hook);
    assert.equal(hook.result.workTab, "tasks", "the open leaves the walk's 任务 target");
    assert.equal(hook.result.taskRequest?.id, TASK_ID, "the handed task is still there to open");
  } finally {
    await teardown(hook);
  }
});

test("an analysis card's task link from Messages lands on Tasks", async () => {
  const hook = await setup({ panelOpen: true });
  try {
    hook.result.setActiveTab("messages");
    await render(hook);
    hook.result.setWorkTab("integrate");
    await render(hook);
    // openTask's order: the top-level switch first, then the explicit target.
    hook.result.setActiveTab("work");
    hook.result.setWorkTab("tasks");
    await render(hook);
    assert.equal(hook.result.activeTab, "work", "the card's link shows 工单");
    assert.equal(hook.result.workTab, "tasks", "the explicit task target writes after the sub-tab reset");
  } finally {
    await teardown(hook);
  }
});
