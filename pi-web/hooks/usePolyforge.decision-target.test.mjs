// carries: hooks/usePolyforge.ts, hooks/__fixtures__/react-hook-shim.mjs
import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// polyforge#240 Q7 A: a browser notification opens the decision entry it was
// built from through the existing attention target rules — never the walk's
// next entry — so a click on an old notification can never open another
// work item. The real `usePolyforge` runs on the react-hook shim with a
// stubbed fetch.
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);

const win = new EventTarget();
globalThis.window = win;
globalThis.document = Object.assign(new EventTarget(), { visibilityState: "visible" });
const { usePolyforge } = await jiti.import("./usePolyforge.ts");

const TASK_ID = "task_0123456789abcdef";
const entry = (id, wi, profile) => ({
  id,
  at: "2026-11-23T00:00:00Z",
  kind: "work_item",
  ...(wi ? { wi } : {}),
  ...(wi ? { slug: `polyforge#${id}` } : {}),
  ...(profile ? { profile } : {}),
  class: "needs_input",
  attention: true,
  resolved: false,
  dismissed: false,
  replied: false,
});
const TASK_ENTRY = {
  id: 4,
  at: "2026-11-23T00:00:00Z",
  kind: "task",
  task_id: TASK_ID,
  class: "failed",
  attention: true,
  resolved: false,
  dismissed: false,
  replied: false,
};
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };
const json = (body) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

async function setup({ decisions = [], panelOpen = true } = {}) {
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  globalThis.fetch = async (url) => {
    const path = String(url);
    if (path === "/api/polyforge/status") {
      return json({ pfPresent: true, status: { queue: { running: 0, max: 4 }, tasks: { running: 0, completed: 0 }, jobs: [] } });
    }
    if (path === "/api/polyforge/decisions") {
      return json({
        pfPresent: true,
        feed: {
          decisions,
          attention: decisions.filter((item) => item.attention && !item.resolved && !item.dismissed).length,
        },
      });
    }
    if (path.startsWith("/api/polyforge/messages")) return json({ capable: false, reason: "pf is not installed", conversations: [], contacts: [], groups: [] });
    if (path.startsWith("/api/polyforge/cards")) return json({ cards: [] });
    return json({});
  };
  const hook = renderHook((props) => usePolyforge(props.panelOpen), { panelOpen });
  await render(hook);
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

test("a notification opens the entry it was built from, never the walk's next entry", async () => {
  const A = entry(1, "wi_A", "public");
  const B = entry(2, "wi_B", "other");
  const C = entry(3, "wi_C", "public");
  const hook = await setup({ decisions: [A, B, C] });
  try {
    assert.equal(typeof hook.result.openDecisionTarget, "function", "the directed opener is exported");
    hook.result.openNextAttention();
    await render(hook);
    assert.equal(hook.result.selectedWi, "wi_A", "the walk starts at the first entry");
    hook.result.openNextAttention();
    await render(hook);
    assert.equal(hook.result.selectedWi, "wi_B", "the walk's next entry is B");

    // The walk would open C here; the notification's own entry is A.
    assert.equal(hook.result.openDecisionTarget(A), true, "the entry names an openable target");
    await render(hook);
    assert.equal(hook.result.selectedWi, "wi_A", "clicking A's notification opens A");
    assert.equal(hook.result.selectedProfile, "public", "A's own Cell");
    assert.equal(hook.result.activeTab, "work", "the panel shows Work");
    assert.equal(hook.result.workTab, "jobs", "the job detail, as the attention walk opens it");

    hook.result.openNextAttention();
    await render(hook);
    assert.equal(hook.result.selectedWi, "wi_B", "the walk continues after the entry the click opened");
  } finally {
    await teardown(hook);
  }
});

test("a task entry's notification opens Work › Tasks on that task, in its Cell", async () => {
  const hook = await setup({ decisions: [TASK_ENTRY] });
  try {
    assert.equal(hook.result.openDecisionTarget(TASK_ENTRY), true);
    await render(hook);
    assert.equal(hook.result.activeTab, "work");
    assert.equal(hook.result.workTab, "tasks", "a task entry lands on Tasks");
    assert.equal(hook.result.taskRequest?.id, TASK_ID, "the task request names the entry's task");
    assert.equal(hook.result.taskRequest?.profile, undefined, "the default Cell carries no profile");
  } finally {
    await teardown(hook);
  }
});

test("an entry that names nothing openable opens nothing and never jumps to another entry", async () => {
  const nameless = entry(9);
  const hook = await setup({ decisions: [nameless, entry(10, "wi_other")] });
  try {
    assert.equal(hook.result.openDecisionTarget(nameless), false, "no target");
    await render(hook);
    assert.equal(hook.result.selectedWi, null, "the selection is untouched");
    assert.equal(hook.result.taskRequest, null, "no task is opened");
  } finally {
    await teardown(hook);
  }
});