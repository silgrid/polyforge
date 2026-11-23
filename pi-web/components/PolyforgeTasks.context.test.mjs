// carries: components/PolyforgeTasks.tsx, lib/polyforge-tasks-pure.ts, lib/polyforge-cards-pure.ts
import "#test-env";
import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// polyforge#230 AC2/AC3 and owner ruling Q2=B, on the react-hook shim the
// existing Tasks suites use: the component runs for real (hooks, effects,
// state), the test — never the network — answers each `/api/polyforge/tasks`
// and `/api/polyforge/work-items/<id>` read, and every assertion reads the
// rendered element tree, not the source. It proves (a) a running card's
// context line — the job meter text and its tone colour with a window, the
// token count alone without one, nothing without a context; (b) the detail's
// `tokens / window` / tokens-only row; (c) the analyzed ticket's row on the
// card and the detail, opening the wi id through onOpenAnchor without a
// detail read, the wi-id fallback on a failed read, no row on an ordinary
// task, and one read per ticket per session; (d) the owner-ruled 15 s
// re-read registered only while a running task is listed.
const fixture = (name) => fileURLToPath(new URL(`../hooks/__fixtures__/${name}`, import.meta.url));
const shimPath = fixture("react-hook-shim.mjs");
const jsxRuntimePath = fixture("react-jsx-runtime.mjs");
// this suite's own i18n/MarkdownBody stand-ins (hooks/__tests__, so the unit's
// test commit owns them): t renders 分析 <slug> as the user sees it.
const stubsPath = fileURLToPath(new URL("../hooks/__tests__/polyforge-tasks-context-stubs.mjs", import.meta.url));
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
    "@/lib/polyforge-work-items-pure": modulePath("lib/polyforge-work-items-pure.ts"),
  },
});
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { PolyforgeTasks } = await jiti.import("./PolyforgeTasks.tsx");

const settle = async () => {
  for (let index = 0; index < 12; index++) await new Promise((resolve) => setImmediate(resolve));
};

// ---- timers: capture what the component registers, drive it by hand ----
let realSetInterval;
let realClearInterval;
let timers = [];
function stubTimers() {
  realSetInterval = globalThis.setInterval;
  realClearInterval = globalThis.clearInterval;
  timers = [];
  globalThis.setInterval = (fn, ms) => {
    timers.push({ fn, ms, cleared: false });
    return timers.length;
  };
  globalThis.clearInterval = (id) => {
    const timer = timers[id - 1];
    if (timer) timer.cleared = true;
  };
}
function restoreTimers() {
  globalThis.setInterval = realSetInterval;
  globalThis.clearInterval = realClearInterval;
}
const refreshTimer = () => timers.find((timer) => timer.ms === 15000 && !timer.cleared);

// ---- the element tree, as the automatic JSX runtime emits it ----
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
const cards = (hook) => nodes(hook).filter(({ node }) => node.type === "button" && "data-pf-task" in node.props).map(({ node }) => node);
const card = (hook, id) => cards(hook).find((node) => node.props["data-pf-task"] === id);
const textOf = (node) => {
  const visit = (child) => {
    if (Array.isArray(child)) return child.map(visit).join("");
    if (typeof child === "string" || typeof child === "number") return String(child);
    if (!child || typeof child !== "object" || !("props" in child)) return "";
    if (typeof child.type === "function") return visit(child.type(child.props));
    return visit(child.props.children);
  };
  return visit(node.props.children);
};
const within = (node, prop) => {
  const found = [];
  const visit = (child) => {
    if (Array.isArray(child)) {
      child.forEach(visit);
      return;
    }
    if (!child || typeof child !== "object" || !("props" in child)) return;
    if (typeof child.type === "function") {
      visit(child.type(child.props));
      return;
    }
    if (prop in child.props) found.push(child);
    visit(child.props.children);
  };
  visit(node.props.children);
  return found;
};
const errorTexts = (hook) => nodes(hook)
  .filter(({ node }) => node.type === "div" && node.props.style?.color === "#dc2626")
  .map(({ node }) => textOf(node))
  .filter(Boolean);

// ---- one rendered tab against a scripted fetch ----
const ROW = (id, state, extra = {}) => ({
  id, project: "workbench", state, prompt_summary: `prompt of ${id}`,
  started_at: "2026-11-22T10:00:00Z",
  ...(state === "running" ? {} : { ended_at: "2026-11-22T10:05:00Z", wall_consumed_ns: 300e9 }),
  report_tail: "", reporter: "alice", ...extra,
});
const detailFor = (row) => ({
  id: row.id, project: row.project, state: row.state, candidates: ["pi:m/one"],
  max_duration_ns: 1, max_tokens: 1, wall_consumed_ns: 1, tokens_consumed: 1,
  created_at: "2026-11-22T10:00:00Z", prompt: "", report: "", report_len: 0, truncated: false,
  ...(row.context ? { context: row.context } : {}),
  ...(row.analysis_of ? { analysis_of: row.analysis_of } : {}),
  ...(row.candidate ? { candidate: row.candidate } : {}),
});

async function render(rows, options = {}) {
  stubTimers();
  const originalFetch = globalThis.fetch;
  const requests = [];
  const opened = [];
  globalThis.fetch = (url) => {
    const text = String(url);
    requests.push(text);
    let status = 200;
    let body;
    if (text === "/api/polyforge/tasks") {
      body = { pfPresent: true, capable: true, tasks: rows };
    } else if (text.startsWith("/api/polyforge/work-items/")) {
      status = options.ticketStatus ?? 200;
      body = { item: status === 200 ? (options.ticket ?? { slug: "polyforge#72", goal: "任务上下文" }) : null };
    } else if (text.startsWith("/api/polyforge/tasks/")) {
      const id = decodeURIComponent(text.split("?")[0].slice("/api/polyforge/tasks/".length));
      const row = rows.find((entry) => entry.id === id);
      body = { task: row ? detailFor(row) : null };
    } else {
      status = 404;
      body = { error: "unexpected request" };
    }
    return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
  };
  const hook = renderHook(PolyforgeTasks, {
    taskCountKey: "1:0",
    renderDetail: (node) => node,
    onOpenAnchor: (ref) => opened.push(ref),
  });
  const pump = async () => {
    for (let round = 0; round < 6; round++) {
      await settle();
      hook.flush();
    }
  };
  await pump();
  return {
    hook, requests, opened, pump,
    restore() {
      globalThis.fetch = originalFetch;
      restoreTimers();
    },
  };
}

test("AC2 (a): a running card shows the job meter's text with its tone colour, the tokens alone without a window, and no line without a context", async () => {
  const rows = [
    ROW("task_0000000000000001", "running", { context: { tokens: 4300, window: 400000, percent: 1.075, model: "openai/m1", asOf: "2026-11-22T10:00:00Z" } }),
    ROW("task_0000000000000002", "running", { context: { tokens: 750, window: 1000, percent: 75, model: "openai/m1" } }),
    ROW("task_0000000000000003", "running", { context: { tokens: 950, window: 1000, percent: 95, model: "openai/m1" } }),
    ROW("task_0000000000000004", "running", { context: { tokens: 4300, model: "openai/m1", asOf: "2026-11-22T10:00:00Z" } }),
    ROW("task_0000000000000005", "completed"),
  ];
  const state = await render(rows);
  try {
    const line = (id) => within(card(state.hook, id), "data-pf-task-context")[0];
    // 4300 / 400000 * 100 = 1.075 -> the job card's `1% / 400k`, below 70%: dim
    assert.equal(textOf(line("task_0000000000000001")), "1% / 400k");
    assert.equal(line("task_0000000000000001").props.style.color, "var(--text-dim)");
    // the job card's tone band: above 70 amber, above 90 red
    assert.equal(line("task_0000000000000002").props.style.color, "rgba(234,179,8,0.95)", "75% is amber");
    assert.equal(line("task_0000000000000003").props.style.color, "#ef4444", "95% is red");
    // 4300 with no window: formatCompactCount(4300) = 4k, never a guessed percent
    assert.equal(textOf(line("task_0000000000000004")), "4k");
    assert.doesNotMatch(textOf(line("task_0000000000000004")), /%/, "no window: no percent");
    // the hover names the model and when the count was taken
    assert.match(String(line("task_0000000000000001").props.title), /openai\/m1/);
    assert.match(String(line("task_0000000000000001").props.title), /2026-11-22T10:00:00Z/);
    // a finished task carries no context (the daemon clears it): no line
    assert.equal(line("task_0000000000000005"), undefined);
  } finally {
    state.restore();
  }
});

test("AC2 (b): the detail shows tokens over the window, or the tokens alone", async () => {
  const rows = [
    ROW("task_0000000000000011", "running", { context: { tokens: 4300, window: 400000, percent: 1.075, model: "openai/m1", asOf: "2026-11-22T10:00:00Z" } }),
    ROW("task_0000000000000012", "running", { context: { tokens: 4300, model: "openai/m1" } }),
  ];
  const state = await render(rows);
  try {
    card(state.hook, "task_0000000000000011").props.onClick();
    await state.pump();
    const windowed = within(state.hook.result, "data-pf-detail-task-context")[0];
    assert.ok(windowed, "the detail's context row is rendered");
    assert.match(textOf(windowed), /4,300 \/ 400,000/, "the job detail's grouped tokens over the window");
    assert.match(textOf(windowed), /上下文/, "the job detail's own label");
    assert.match(textOf(windowed), /openai\/m1/, "the model rides the row");

    card(state.hook, "task_0000000000000012").props.onClick();
    await state.pump();
    const tokensOnly = within(state.hook.result, "data-pf-detail-task-context")[0];
    assert.match(textOf(tokensOnly), /4,300/);
    assert.doesNotMatch(textOf(tokensOnly), /\?\s*\/|%/, "no window: no percent and no '?' side");
  } finally {
    state.restore();
  }
});

test("AC3 (c): the card and the detail name the analyzed ticket, open it by wi id, fall back to the wi id and read each ticket once", async () => {
  const rows = [
    ROW("task_0000000000000021", "running", { analysis_of: "wi_abc123" }),
    ROW("task_0000000000000022", "completed", { analysis_of: "wi_abc123" }),
    ROW("task_0000000000000023", "completed"),
  ];
  const state = await render(rows);
  try {
    const ticketReads = () => state.requests.filter((url) => url.startsWith("/api/polyforge/work-items/"));
    assert.equal(ticketReads().length, 1, "one read for the one analyzed work item, however many rows name it");
    assert.equal(ticketReads()[0], "/api/polyforge/work-items/wi_abc123");

    const analysis = within(card(state.hook, "task_0000000000000021"), "data-pf-task-analysis")[0];
    assert.ok(analysis, "the card's analysis row is rendered");
    assert.match(textOf(analysis), /分析 polyforge#72/, "分析 <slug>");
    assert.match(textOf(analysis), /任务上下文/, "the ticket's goal beneath it");
    assert.equal(within(card(state.hook, "task_0000000000000023"), "data-pf-task-analysis").length, 0, "an ordinary task has no analysis row");

    // the click opens the analyzed wi id and never also selects the task
    const detailReads = () => state.requests.filter((url) => url.startsWith("/api/polyforge/tasks/")).length;
    const before = detailReads();
    let stopped = 0;
    analysis.props.onClick({ stopPropagation: () => { stopped += 1; } });
    await state.pump();
    assert.deepEqual(state.opened, ["wi_abc123"]);
    assert.equal(stopped, 1, "the row's click stops before the card's own select");
    assert.equal(detailReads(), before, "opening the ticket is not a task detail read");

    // the detail's top row: same ticket, same opener
    card(state.hook, "task_0000000000000021").props.onClick();
    await state.pump();
    const detailAnalysis = within(state.hook.result, "data-pf-detail-task-analysis")[0];
    assert.ok(detailAnalysis, "the detail's analysis row is rendered");
    assert.match(textOf(detailAnalysis), /分析 polyforge#72/);
    assert.match(textOf(detailAnalysis), /任务上下文/);
    detailAnalysis.props.onClick({ stopPropagation: () => {} });
    await state.pump();
    assert.deepEqual(state.opened, ["wi_abc123", "wi_abc123"]);
  } finally {
    state.restore();
  }
});

test("AC3 (c): a failed ticket read shows the wi id, never an error, and the row stays openable", async () => {
  const rows = [ROW("task_0000000000000031", "running", { analysis_of: "wi_feed999" })];
  const state = await render(rows, { ticketStatus: 500 });
  try {
    const analysis = within(card(state.hook, "task_0000000000000031"), "data-pf-task-analysis")[0];
    assert.ok(analysis, "the row is still rendered");
    assert.match(textOf(analysis), /分析 wi_feed999/, "the wi id stands in for the unread slug");
    assert.deepEqual(errorTexts(state.hook), [], "a failed ticket read is not an error state");
    analysis.props.onClick({ stopPropagation: () => {} });
    await state.pump();
    assert.deepEqual(state.opened, ["wi_feed999"], "the row opens the wi id it knows");
  } finally {
    state.restore();
  }
});

test("owner Q2=B (d): a running task registers the 15 s list re-read and firing it reads the list again; no running task registers none", async () => {
  const running = await render([ROW("task_0000000000000041", "running")]);
  try {
    const timer = refreshTimer();
    assert.ok(timer, "the 15 s re-read is registered while a running task is listed");
    const listReads = () => running.requests.filter((url) => url === "/api/polyforge/tasks").length;
    const before = listReads();
    timer.fn();
    await running.pump();
    assert.equal(listReads(), before + 1, "the timer's tick is one more list read");
  } finally {
    running.restore();
  }
  const settled = await render([ROW("task_0000000000000042", "completed")]);
  try {
    assert.equal(refreshTimer(), undefined, "no running task: no re-read timer");
  } finally {
    settled.restore();
  }
});
