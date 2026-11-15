// carries: components/PolyforgePanel*, components/PolyforgeDetailProgress.test.mjs, components/PolyforgePanel.test.mjs, hooks/__fixtures__/react-hook-shim.mjs, hooks/__fixtures__/polyforge-panel-progress-stubs.mjs, e2e/polyforge.mjs
// polyforge#48 (u1 AC1, u2 AC2) — the job detail's execution progress on the
// react-hook shim (the PolyforgeTasks.refresh precedent): the component runs
// for real through its own hooks, the test — never the network — decides when
// each /api/polyforge/progress/tool read settles, and the request count is the
// unit under test. AC1: the poll's re-read of an expanded resultless card fires
// only while the job runs and only when the snapshot really changed (the
// panel's selected-step view is cached, so an unrelated re-render cannot
// retrigger it). AC2: an owner echo's delivery is judged against every line
// of the job's whole stream, never the selected step's lines alone.
import "#test-env";
import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

const fixture = (name) => fileURLToPath(new URL(`../hooks/__fixtures__/${name}`, import.meta.url));
const shimPath = fixture("react-hook-shim.mjs");
const jsxRuntimePath = fixture("react-jsx-runtime.mjs");
// The panel-progress stubs live under a test path (hooks/__tests__/, review
// r1#1): a unit's test commit carries the test side alone, so the fixtures it
// owns must not sit outside the recognized test paths.
const stubsPath = fileURLToPath(new URL("../hooks/__tests__/polyforge-panel-progress-stubs.mjs", import.meta.url));
const modulePath = (path) => fileURLToPath(new URL(`../${path}`, import.meta.url));
// jiti's own aliases, not tsconfig paths (the precedent's rationale): each
// "@/…" and stubbed relative specifier the panel module makes is aliased
// explicitly; the rest resolve to the real files.
const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  alias: {
    react: shimPath,
    "react/jsx-runtime": jsxRuntimePath,
    "react-dom": stubsPath,
    "@/hooks/useI18n": stubsPath,
    "./SafeMarkdownBody": stubsPath,
    "./PolyforgeMessages": stubsPath,
    "./PolyforgeMemory": stubsPath,
    "./PolyforgeIntegrate": stubsPath,
    "./PolyforgeTasks": stubsPath,
    "./PolyforgeWorkItems": stubsPath,
    "./PolyforgeProjects": stubsPath,
    "./PolyforgeJobComposer": stubsPath,
    "@/lib/polyforge-cards-pure": modulePath("lib/polyforge-cards-pure.ts"),
    "@/lib/panel-layout": modulePath("lib/panel-layout.ts"),
    "@/lib/polyforge-decisions-pure": modulePath("lib/polyforge-decisions-pure.ts"),
    "@/lib/polyforge-conversation-pure": modulePath("lib/polyforge-conversation-pure.ts"),
    "@/hooks/usePolyforgePanelPreferences": modulePath("hooks/usePolyforgePanelPreferences.ts"),
    "@/lib/polyforge-panel-preferences": modulePath("lib/polyforge-panel-preferences.ts"),
    "@/hooks/useIsMobile": modulePath("hooks/useIsMobile.ts"),
    "@/hooks/usePolyforgeProgress": modulePath("hooks/usePolyforgeProgress.ts"),
  },
});
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { PolyforgeDetailProgress } = await jiti.import("./PolyforgePanel.tsx");

const WI = "wi_E2Euuuu0001";
const toolLine = (id) => ({ id, kind: "tool", text: `bash: run ${id}`, tool: { name: "bash", argument: `run ${id}`, status: "running" } });
const narration = (id, text = `line ${id}`) => ({ id, kind: "narration", text });
const snapshotOf = (lines) => ({ available: true, lines, omitted: 0 });
// A resultless available detail: the state an expanded card holds while its
// command still runs — exactly what the poll re-reads.
const RESULTLESS = { available: true, hasResult: false, tool: { name: "bash", argument: "run t1", status: "running", input: "run t1", output: null, isError: false } };

const settle = async () => {
  for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve));
};

// The element tree without expanding function components (the piece under
// test owns its hooks; the tool card's own state stays unexpanded, its props
// — line, detail, onToggle — are the contract the test drives).
function walk(node, out = []) {
  if (Array.isArray(node)) {
    for (const child of node) walk(child, out);
    return out;
  }
  if (!node || typeof node !== "object" || !("props" in node)) return out;
  out.push(node);
  walk(node.props.children, out);
  return out;
}

function setup(overrides = {}) {
  const originalFetch = globalThis.fetch;
  const requests = [];
  globalThis.fetch = (url) => {
    const request = { url, settled: false };
    request.promise = new Promise((resolve) => {
      request.resolve = resolve;
    });
    requests.push(request);
    return request.promise;
  };
  const props = () => ({ wi: WI, running: false, snapshot: snapshotOf([toolLine("t1")]), echoes: [], allLines: [], scrollRef: { current: null }, ...overrides });
  let current = props();
  const hook = renderHook(PolyforgeDetailProgress, current);
  const answer = async (index, body) => {
    const request = requests[index];
    assert.ok(request && !request.settled, `request ${index} is open`);
    request.settled = true;
    request.resolve(new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } }));
    await settle();
    hook.flush();
  };
  const expand = () => {
    const card = walk(hook.result).find((node) => node.props?.line?.id === "t1");
    assert.ok(card, "the tool card renders");
    card.props.onToggle("t1", true);
    hook.flush();
  };
  // Re-render exactly as the panel would: with the props object it holds,
  // optionally over the snapshot the caller names.
  const rerender = (snapshot) => {
    current = { ...current, ...(snapshot ? { snapshot } : {}) };
    hook.rerender(current);
    hook.flush();
  };
  // Re-render with arbitrary prop overrides (the panel can flip resetFollow
  // while the step stays selected — review r1#9's case).
  const rerenderProps = (extra) => {
    current = { ...current, ...extra };
    hook.rerender(current);
    hook.flush();
  };
  // Answer every read the re-renders issued, so the next one is never
  // deduplicated by the in-flight mark (the panel's unrelated polls render
  // between answers in the browser; the red tree reads on each of them).
  const rerenderFreshAnswered = async (times, lines = [toolLine("t1")]) => {
    for (let index = 0; index < times; index++) {
      rerender(snapshotOf(lines));
      const last = requests.length - 1;
      if (last >= 0 && !requests[last].settled) await answer(last, RESULTLESS);
    }
  };
  return {
    hook,
    requests,
    answer,
    expand,
    rerender,
    rerenderProps,
    rerenderFreshAnswered,
    tree: () => walk(hook.result),
    restore() { globalThis.fetch = originalFetch; },
  };
}

test("polyforge#48 AC1: an ended ticket's expanded resultless card reads once; equal-content re-renders read nothing", async () => {
  const state = setup({ running: false });
  try {
    state.expand();
    assert.equal(state.requests.length, 1, "the expansion reads the detail once");
    assert.equal(state.requests[0].url, `/api/polyforge/progress/tool?wi=${WI}&id=t1`);
    await state.answer(0, RESULTLESS);
    // The panel re-renders on every unrelated poll (watch, status, cards) and
    // each render recomputes the selected-step view; on an ended ticket an
    // equal-content snapshot must not re-read the card (the running gate; the
    // panel's selection cache keeps the object stable in the browser).
    await state.rerenderFreshAnswered(3);
    assert.equal(state.requests.length, 1, `no further detail read on an ended ticket (${state.requests.length} reads)`);
  } finally {
    state.restore();
  }
});

test("polyforge#48 AC1: a running ticket re-reads once per real snapshot change, never per re-render", async () => {
  const state = setup({ running: true });
  try {
    state.expand();
    await state.answer(0, RESULTLESS);
    state.rerender();
    assert.equal(state.requests.length, 1, "an unchanged snapshot object reads nothing");
    await state.rerenderFreshAnswered(1, [toolLine("t1"), narration("n1")]);
    assert.equal(state.requests.length, 2, "one re-read per real snapshot change");
    state.rerender();
    assert.equal(state.requests.length, 2, "no further read without a change");
  } finally {
    state.restore();
  }
});

test("polyforge#48 AC2: a delivered echo is judged on every line of the job, not the selected step's", async () => {
  // The owner line naming the echo's msgAt sits in ANOTHER step's block: the
  // selected step's lines (the snapshot prop) carry no owner line, the whole
  // stream (allLines) does. A step switch must not make a delivered message
  // read as pending again.
  const ECHO = { at: "2026-11-15T10:00:00Z", text: "the owner's words", ack: { kind: "message", delivered: false, disposition: "resume" } };
  const selectedLines = [narration("b1", "step B's line")];
  const wholeLines = [narration("b1", "step B's line"),
    { id: "o1", kind: "owner", text: "the owner's words", msgAt: ECHO.at }];
  const delivered = setup({ running: true, snapshot: snapshotOf(selectedLines), allLines: wholeLines, echoes: [ECHO] });
  try {
    const notes = delivered.tree().filter((node) => "data-pf-progress-owner-note" in node.props);
    assert.equal(notes.length, 0, `a delivered echo (its owner line in another step's block) does not render (${notes.length} echo rows)`);
  } finally {
    delivered.restore();
  }
  // Before the event log's own line arrives, the echo is the only record:
  // it renders with its delivery note (the pending case must not over-hide).
  const pending = setup({ running: true, snapshot: snapshotOf(selectedLines), allLines: selectedLines, echoes: [ECHO] });
  try {
    const notes = pending.tree().filter((node) => "data-pf-progress-owner-note" in node.props);
    assert.equal(notes.length, 1, "an echo whose owner line has not landed yet renders");
    assert.match(String(notes[0].props.children), /resume/, "the note shows how far the delivery got");
  } finally {
    pending.restore();
  }
});

test("polyforge#48 r1#9: a manual click on the already-selected step resets nothing", async () => {
  // review r1#9: resetFollow riding in the reset effect's dependencies made
  // a manual click on the step that is ALREADY selected (the automatic
  // selection's own step) re-run the whole reset — folds opened, held tool
  // details cleared — though neither the ticket nor the step changed. The
  // reset must fire on a selection or step change alone.
  const state = setup({ running: false, resetKey: "build", resetFollow: "bottom" });
  try {
    state.expand();
    await state.answer(0, RESULTLESS);
    const held = () => state.tree().find((node) => node.props?.line?.id === "t1")?.props?.detail;
    assert.equal(held()?.input, "run t1", "the expanded card holds its read detail");
    // The manual flag flips while the step stays selected: resetFollow
    // changes, the ticket and step do not — nothing resets.
    state.rerenderProps({ resetFollow: "step" });
    assert.equal(held()?.input, "run t1", "the held detail survives resetFollow flipping on an unchanged step");
  } finally {
    state.restore();
  }
});
