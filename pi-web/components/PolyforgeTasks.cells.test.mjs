// carries: components/PolyforgeTasks.tsx, lib/polyforge-tasks-pure.ts
import "#test-env";
import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// polyforge#73's Tasks-tab Cell behaviours, rendered on the react-hook shim
// (the refresh test's fixtures): an external open (the decision walk's task,
// the analysis card's link) hands the task's Cell along, and the detail read
// is pinned to it (`?profile=`); each listed row names its Cell
// (`data-pf-task-cell`) and a failed Cell shows its error while the rest
// list. The test, never the network, decides when each read settles.
const fixture = (name) => fileURLToPath(new URL(`../hooks/__fixtures__/${name}`, import.meta.url));
const shimPath = fixture("react-hook-shim.mjs");
const jsxRuntimePath = fixture("react-jsx-runtime.mjs");
const stubsPath = fixture("polyforge-tasks-stubs.mjs");
const modulePath = (path) => fileURLToPath(new URL(`../${path}`, import.meta.url));
const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  alias: {
    react: shimPath,
    "react/jsx-runtime": jsxRuntimePath,
    "@/hooks/useI18n": stubsPath,
    "./MarkdownBody": stubsPath,
    "@/lib/polyforge-memory-pure": modulePath("lib/polyforge-memory-pure.ts"),
    "@/lib/polyforge-tasks-pure": modulePath("lib/polyforge-tasks-pure.ts"),
    "@/lib/polyforge-cards-pure": modulePath("lib/polyforge-cards-pure.ts"),
    "@/lib/panel-layout": modulePath("lib/panel-layout.ts"),
    "@/lib/polyforge-decisions-pure": modulePath("lib/polyforge-decisions-pure.ts"),
  },
});
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { PolyforgeTasks } = await jiti.import("./PolyforgeTasks.tsx");

const DETAIL = {
  id: "task_1111111111111111", project: "workbench", state: "completed", candidates: ["pi:m/one"],
  max_duration_ns: 1, max_tokens: 1, wall_consumed_ns: 1, tokens_consumed: 1, created_at: "2026-11-16T00:00:00Z",
  started_at: "2026-11-16T00:00:01Z", ended_at: "2026-11-16T00:10:00Z", prompt: "the prompt",
  report: "**done**", report_len: 8, truncated: false,
};
const ROW = (id, cell) => ({
  id,
  project: "workbench",
  state: "completed",
  prompt_summary: `prompt of ${id}`,
  ended_at: "2026-11-16T00:00:00Z",
  wall_consumed_ns: 60e9,
  report_tail: "",
  reporter: "alice",
  ...(cell ? { cell } : {}),
});
const TASK_PUBLIC = "task_1111111111111111";

const settle = async () => {
  for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve));
};

function walk(node, parent = null, out = []) {
  if (Array.isArray(node)) {
    for (const child of node) walk(child, parent, out);
    return out;
  }
  if (!node || typeof node !== "object" || !("props" in node)) return out;
  if (typeof node.type === "function") return walk(node.type(node.props), parent, out);
  out.push({ node, parent });
  walk(node.props.children, node, out);
  return out;
}
const nodes = (hook) => walk(hook.result);
const props = (hook, key) => nodes(hook).map(({ node }) => node.props).filter((p) => key in p);

function setup(initialProps) {
  const originalFetch = globalThis.fetch;
  const requests = [];
  globalThis.fetch = (url) => {
    const request = { url, settled: false };
    request.promise = new Promise((resolve) => { request.resolve = resolve; });
    requests.push(request);
    return request.promise;
  };
  const hook = renderHook(PolyforgeTasks, { taskCountKey: "0:0", renderDetail: (node) => node, ...initialProps });
  const answer = async (index, body, status = 200) => {
    const request = requests[index];
    assert.ok(request && !request.settled, `request ${index} (${request?.url}) is open`);
    request.settled = true;
    request.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
    await settle();
    hook.flush();
  };
  return { hook, requests, answer, restore() { globalThis.fetch = originalFetch; } };
}

test("polyforge#73 u5: an external open with a Cell reads that task's detail pinned to the Cell, once", async () => {
  const handled = [];
  const { hook, requests, answer, restore } = setup({
    openTask: { id: TASK_PUBLIC, cell: "public" },
    onOpenTaskHandled: () => handled.push(Date.now()),
  });
  try {
    assert.equal(requests[0]?.url, "/api/polyforge/tasks", "the mount list read");
    assert.equal(requests[1]?.url, `/api/polyforge/tasks/${TASK_PUBLIC}?profile=public`,
      `the detail read is pinned to the handed Cell (${requests.map((r) => r.url).join(", ")})`);
    assert.equal(requests.length, 2, "no further read");
    assert.equal(handled.length, 1, "handed back once");
    await answer(0, { pfPresent: true, capable: true, tasks: [] });
    await answer(1, { pfPresent: true, capable: true, task: DETAIL });
    const named = props(hook, "data-pf-task").map((p) => p["data-pf-task"]);
    assert.ok(named.includes(TASK_PUBLIC), "the open detail names its task");
  } finally {
    restore();
  }
});

test("polyforge#73 u5: an external open without a Cell keeps the plain detail read", async () => {
  const { requests, restore } = setup({ openTask: { id: TASK_PUBLIC } });
  try {
    assert.equal(requests[1]?.url, `/api/polyforge/tasks/${TASK_PUBLIC}`, "no ?profile without a Cell");
  } finally {
    restore();
  }
});

test("polyforge#73 u6: every row names its Cell; a row click reads that task's detail pinned to it", async () => {
  const { hook, requests, answer, restore } = setup({});
  try {
    await answer(0, { pfPresent: true, capable: true, tasks: [ROW("task_00000000000000d1", "default"), ROW(TASK_PUBLIC, "public")] });
    // each row names its own Cell; the top single-Cell annotation is gone
    const cells = props(hook, "data-pf-task-cell").map((p) => p["data-pf-task-cell"]);
    assert.deepEqual(cells, ["default", "public"], "a chip per row, in the list's order");
    assert.equal(props(hook, "data-pf-tab-cell").length, 0, "the retired top single-Cell annotation renders nothing");
    // a row click reads that task's detail in its own Cell
    const row = nodes(hook).find(({ node }) => node.type === "button" && node.props["data-pf-task"] === TASK_PUBLIC)?.node;
    assert.ok(row, "the public row renders");
    row.props.onClick();
    hook.flush();
    assert.equal(requests.at(-1)?.url, `/api/polyforge/tasks/${TASK_PUBLIC}?profile=public`, "the detail read is pinned to the clicked row's Cell");
  } finally {
    restore();
  }
});

test("polyforge#73 u6: a Cell that failed to list shows its error while the rest list", async () => {
  const { hook, answer, restore } = setup({});
  try {
    await answer(0, {
      pfPresent: true, capable: true,
      tasks: [ROW("task_00000000000000d1", "default")],
      cellErrors: [{ cell: "public", error: "pf: cell public is unreachable" }],
    });
    const errors = props(hook, "data-pf-cell-error").map((p) => p["data-pf-cell-error"]);
    assert.deepEqual(errors, ["public"], "the failed Cell is named on the list");
    assert.equal(props(hook, "data-pf-task-cell").length, 1, "the default Cell's row still lists");
  } finally {
    restore();
  }
});
