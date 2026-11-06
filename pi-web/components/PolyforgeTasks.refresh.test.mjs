import "#test-env";
import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// Behavioural coverage of the Tasks tab's manual refresh button
// the component renders on the react-hook shim
// (hooks/__fixtures__/react-hook-shim.mjs), the JSX runtime fixture keeps the
// element tree readable as plain objects, and the test, never the network,
// decides when each `/api/polyforge/tasks` read settles. It proves (a) one
// mount read, (b) exactly one more per click, (c) the disabled / aria-busy /
// animate-spin state of the newest read in flight and its return, (d) a stale
// read that lands late neither ending the spin nor writing list or error, and
// (e) a failed refresh keeping the list on screen with the red listError.
const fixture = (name) => fileURLToPath(new URL(`../hooks/__fixtures__/${name}`, import.meta.url));
const shimPath = fixture("react-hook-shim.mjs");
const jsxRuntimePath = fixture("react-jsx-runtime.mjs");
// The i18n context and the markdown renderer are stubbed: the shim has no
// context hooks, and the list view never renders a report.
const stubsPath = fixture("polyforge-tasks-stubs.mjs");
// jiti's own aliases, not tsconfig paths: with tsconfigPaths on, the "@/*"
// mapping resolves these specifiers to the real files before the alias map
// is consulted. Each "@/…" import the component makes is aliased explicitly.
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

const ROW = (id, project = "workbench") => ({
  id,
  project,
  state: "completed",
  prompt_summary: `prompt of ${id}`,
  ended_at: "2026-10-31T00:00:00Z",
  wall_consumed_ns: 60e9,
  report_tail: "",
  reporter: "alice",
});
const LIST = (...tasks) => ({ pfPresent: true, capable: true, tasks });
const TASK_A = ROW("task_000000000000000a");
const TASK_OLD = ROW("task_000000000000000b");
const TASK_NEW = ROW("task_000000000000000c");

const settle = async () => {
  for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve));
};

// The element tree as the JSX an automatic runtime emits: objects with a
// `type` (a tag name or a function component) and `props`; children may be a
// single node or an array.
function walk(node, parent = null, out = []) {
  if (Array.isArray(node)) {
    for (const child of node) walk(child, parent, out);
    return out;
  }
  if (!node || typeof node !== "object" || !("props" in node)) return out;
  // the search bar and its action are function components.
  // Expand them in place (they use no React hooks the shim must own) so the
  // same element-tree assertions reach the rendered input and button.
  if (typeof node.type === "function") return walk(node.type(node.props), parent, out);
  out.push({ node, parent });
  walk(node.props.children, node, out);
  return out;
}
const nodes = (hook) => walk(hook.result);
const refreshButton = (hook) => nodes(hook).find(({ node }) => node.type === "button" && "data-pf-tasks-refresh" in node.props)?.node;
const spinClass = (button) => button.props.children.props.className;
const listedIds = (hook) => nodes(hook)
  .filter(({ node }) => node.type === "button" && "data-pf-task" in node.props)
  .map(({ node }) => node.props["data-pf-task"]);
const listErrorText = (hook) => nodes(hook)
  .filter(({ node }) => node.type === "div" && node.props.style?.color === "#dc2626")
  .map(({ node }) => node.props.children)
  .find((child) => typeof child === "string" && child.length > 0);

function setup() {
  const originalFetch = globalThis.fetch;
  const requests = [];
  globalThis.fetch = (url) => {
    const request = { url, settled: false };
    request.promise = new Promise((resolve, reject) => {
      request.resolve = resolve;
      request.reject = reject;
    });
    requests.push(request);
    return request.promise;
  };
  const hook = renderHook(PolyforgeTasks, { taskCountKey: "0:0" });
  const answer = async (index, body, status = 200) => {
    const request = requests[index];
    assert.ok(request && !request.settled, `request ${index} is open`);
    request.settled = true;
    request.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
    await settle();
    hook.flush();
  };
  const clickRefresh = () => {
    const button = refreshButton(hook);
    assert.ok(button, "the refresh button is on screen");
    button.props.onClick();
    hook.flush();
  };
  return {
    hook,
    requests,
    answer,
    clickRefresh,
    restore() { globalThis.fetch = originalFetch; },
  };
}

test("(a) the tab reads the list exactly once on mount, and the refresh button renders idle beside the search box", async () => {
  const state = setup();
  try {
    assert.equal(state.requests.length, 1, "one list read on mount");
    assert.equal(state.requests[0].url, "/api/polyforge/tasks");
    assert.equal(refreshButton(state.hook), undefined, "no button while the first read is the loading notice");
    // An empty list (暂无任务) also carries the button: the point of the
    // control is letting the user re-read when the count key holds.
    await state.answer(0, LIST());
    assert.equal(state.requests.length, 1, "resolving the mount read starts no further read");
    assert.ok(nodes(state.hook).some(({ node }) => node.type === "span" && node.props.children === "polyforge.tasks.empty"),
      "the empty list shows 暂无任务");

    const tree = nodes(state.hook);
    const buttonEntry = tree.find(({ node }) => node.type === "button" && "data-pf-tasks-refresh" in node.props);
    const inputEntry = tree.find(({ node }) => node.type === "input" && node.props.type === "search");
    assert.ok(buttonEntry && inputEntry, "the list view carries the refresh button beside the searchbox");
    const button = buttonEntry.node;
    const input = inputEntry.node;
    assert.equal(inputEntry.parent, buttonEntry.parent, "the button sits on the search box's own row");
    // owner-intended change: the action is the shared 26px/40px
    // control (the geometry is browser-pinned by e2e); its busy contract is
    // unchanged.
    assert.equal(button.props.className, "pf-searchbar-action", "the shared square action");
    assert.equal(button.props["data-pf-searchbar-action"], "", "the shared action marker");
    assert.equal(input.props.className, "pf-searchbar-input", "the shared search input");
    assert.equal(button.props.disabled, false);
    assert.equal(button.props["aria-busy"], false);
    assert.equal(spinClass(button), undefined, "no spin while idle");
    assert.equal(button.props.title, "polyforge.refresh");
    assert.equal(button.props["aria-label"], "polyforge.refresh");

    state.hook.rerender({ taskCountKey: "0:0" });
    state.hook.flush();
    assert.equal(state.requests.length, 1, "re-rendering with the same count key reads nothing");
  } finally {
    state.restore();
  }
});

test("(b) a click reads the list exactly once more, and the fresh list replaces it", async () => {
  const state = setup();
  try {
    await state.answer(0, LIST(TASK_A));
    state.clickRefresh();
    assert.equal(state.requests.length, 2, "the click adds exactly one list read");
    assert.equal(state.requests[1].url, "/api/polyforge/tasks");
    await state.answer(1, LIST(TASK_A, TASK_NEW));
    assert.deepEqual(listedIds(state.hook).sort(), [TASK_A.id, TASK_NEW.id].sort(), "the fresh list is on screen");
    assert.equal(state.requests.length, 2, "no further read follows the answer");
  } finally {
    state.restore();
  }
});

test("(c) while the newest read is in flight the button is disabled, aria-busy and spinning; the answer restores all three", async () => {
  const state = setup();
  try {
    await state.answer(0, LIST(TASK_A));
    state.clickRefresh();
    await settle();
    state.hook.flush();
    const busy = refreshButton(state.hook);
    assert.equal(busy.props.disabled, true, "disabled: a click cannot stack a second read");
    assert.equal(busy.props["aria-busy"], true);
    assert.equal(spinClass(busy), "animate-spin", "the spinning icon of the sidebar's refresh");
    assert.equal(busy.props.title, "polyforge.refreshing");
    assert.equal(busy.props["aria-label"], "polyforge.refreshing");
    assert.deepEqual(listedIds(state.hook), [TASK_A.id], "the old list stays on screen during the refresh");

    await state.answer(1, LIST(TASK_OLD));
    const idle = refreshButton(state.hook);
    assert.equal(idle.props.disabled, false);
    assert.equal(idle.props["aria-busy"], false);
    assert.equal(spinClass(idle), undefined, "the spin ends with the answer");
    assert.equal(idle.props.title, "polyforge.refresh");
    assert.equal(idle.props["aria-label"], "polyforge.refresh");
  } finally {
    state.restore();
  }
});

test("(d) a stale read that lands late neither ends the spin nor writes the list or the error", async () => {
  const state = setup();
  try {
    await state.answer(0, LIST(TASK_A));
    // The click's read (request 1) is followed by a count-change re-read
    // (request 2): the newest seq is 2, so 1 is stale.
    state.clickRefresh();
    state.hook.rerender({ taskCountKey: "1:0" });
    state.hook.flush();
    assert.equal(state.requests.length, 3, "the count change read once more over the read in flight");
    assert.equal(refreshButton(state.hook).props.disabled, true);

    // The stale read answers first, with a list of its own: neither the list
    // nor the spin may follow it.
    await state.answer(1, LIST(TASK_OLD));
    assert.deepEqual(listedIds(state.hook), [TASK_A.id], "the stale answer does not write the list");
    assert.equal(refreshButton(state.hook).props.disabled, true, "the stale answer does not end the newer spin");
    assert.equal(refreshButton(state.hook).props["aria-busy"], true);

    // The newest read answers: its list lands and the spin ends.
    await state.answer(2, LIST(TASK_NEW));
    assert.deepEqual(listedIds(state.hook), [TASK_NEW.id], "the newest answer writes the list");
    assert.equal(refreshButton(state.hook).props.disabled, false, "and only it ends the spin");
    assert.equal(refreshButton(state.hook).props["aria-busy"], false);

    // A stale failure is equally silent: no listError, no end of the spin.
    state.clickRefresh();
    state.hook.rerender({ taskCountKey: "2:0" });
    state.hook.flush();
    assert.equal(state.requests.length, 5);
    await state.answer(3, { error: "stale boom" }, 500);
    assert.equal(listErrorText(state.hook), undefined, "a stale failure writes no error");
    assert.deepEqual(listedIds(state.hook), [TASK_NEW.id]);
    assert.equal(refreshButton(state.hook).props.disabled, true, "a stale failure does not end the spin");
    await state.answer(4, LIST(TASK_A));
    assert.equal(listErrorText(state.hook), undefined);
    assert.deepEqual(listedIds(state.hook), [TASK_A.id]);
    assert.equal(refreshButton(state.hook).props.disabled, false);
  } finally {
    state.restore();
  }
});

test("(e) a failed refresh keeps the old list on screen under the red listError and frees the button", async () => {
  const state = setup();
  try {
    await state.answer(0, LIST(TASK_A));
    state.clickRefresh();
    await state.answer(1, { error: "task list failed" }, 500);
    assert.deepEqual(listedIds(state.hook), [TASK_A.id], "the old list stays during and after a failed refresh");
    assert.equal(listErrorText(state.hook), "task list failed", "pf's own bounded error sits above the list");
    assert.equal(refreshButton(state.hook).props.disabled, false, "the failed read still ends the spin");
    assert.equal(spinClass(refreshButton(state.hook)), undefined);

    // A later success clears the error and replaces the list.
    state.clickRefresh();
    await state.answer(2, LIST(TASK_A, TASK_NEW));
    assert.equal(listErrorText(state.hook), undefined, "a successful refresh clears the error");
    assert.deepEqual(listedIds(state.hook).sort(), [TASK_A.id, TASK_NEW.id].sort());
  } finally {
    state.restore();
  }
});