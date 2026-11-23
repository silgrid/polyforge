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
    // polyforge#151: the panel threads the watch snapshot's step-tier catalog
    // into the step controls' shared picker.
    "@/lib/step-tiers": modulePath("lib/step-tiers.ts"),
    "@/lib/panel-layout": modulePath("lib/panel-layout.ts"),
    "@/lib/polyforge-decisions-pure": modulePath("lib/polyforge-decisions-pure.ts"),
    "@/lib/polyforge-conversation-pure": modulePath("lib/polyforge-conversation-pure.ts"),
    "@/hooks/usePolyforgePanelPreferences": modulePath("hooks/usePolyforgePanelPreferences.ts"),
    "@/lib/polyforge-panel-preferences": modulePath("lib/polyforge-panel-preferences.ts"),
    "@/hooks/useIsMobile": modulePath("hooks/useIsMobile.ts"),
    "@/hooks/usePolyforgeProgress": modulePath("hooks/usePolyforgeProgress.ts"),
    // polyforge#53: the shared ThinkingFold's own dependencies resolve to
    // the real pure modules (the preview and the preference), with
    // react-markdown stubbed in the stubs file below.
    "@/lib/message-display": modulePath("lib/message-display.ts"),
    "@/lib/thinking-expansion-preference": modulePath("lib/thinking-expansion-preference.ts"),
    "react-markdown": stubsPath,
  },
});
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { PolyforgeDetailProgress } = await jiti.import("./PolyforgePanel.tsx");
// The shared session treatment and its icon: the same module instances the
// panel's own import chain loads, so an element's type compares by identity.
const { ThinkingFold } = await jiti.import("./ThinkingFold.tsx");
const { ThinkingIcon } = await jiti.import("./ThinkingIcon.tsx");
const { THINKING_EXPANDED_EVENT } = await jiti.import("@/lib/thinking-expansion-preference");
const stubs = await import(pathToFileURL(stubsPath).href);
const { SafeMarkdownBody } = stubs;
const ReactMarkdown = stubs.default;

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

// ── polyforge#53 ─────────────────────────────────────────────────────────────
// The progress's thinking narration renders through the session's own
// collapsible thinking treatment (the shared ThinkingFold): collapsed to one
// line (the same ThinkingIcon + first-line preview) unless the
// 「默认展开思考块」 preference expands it, click toggles, and the preference's
// broadcast updates an already-rendered row. The window is stubbed: the
// preference read and the event listener are the row's own hooks' to drive.
const THINK_TEXT = "E2E-P53-THINK-P1\n\nE2E-P53-THINK-P2";
const thinkingLine = (id, text = THINK_TEXT) => ({ id, kind: "narration", text, variant: "thinking" });

// A stubbed window whose localStorage answers one mutable value and whose
// addEventListener captures the listeners (the broadcast the row registers).
const stubWindow = () => {
  const previousWindow = globalThis.window;
  let stored = null;
  const listeners = [];
  globalThis.window = {
    localStorage: { getItem: () => stored },
    addEventListener: (name, listener) => listeners.push([name, listener]),
    removeEventListener: () => {},
  };
  return {
    listeners,
    set stored(value) { stored = value; },
    restore() {
      if (previousWindow === undefined) delete globalThis.window;
      else globalThis.window = previousWindow;
    },
  };
};

// The thinking line's row element: a function component carrying the line.
// The tree walk never expands it — its hooks are driven through a nested
// renderHook below, exactly as the browser mounts it.
const thinkingRow = (state) => state.tree().find((node) => typeof node.type === "function" && node.props?.line?.id === "th1");

test("polyforge#53 AC1: a thinking narration renders collapsed through the shared fold; click expands, click collapses", () => {
  const window = stubWindow();
  try {
    const state = setup({ snapshot: snapshotOf([thinkingLine("th1")]) });
    try {
      const rowElement = thinkingRow(state);
      assert.ok(rowElement, "the thinking line renders as its own fold row (the shared session treatment)");
      const row = renderHook(rowElement.type, rowElement.props);
      try {
        const fold = () => walk(row.result).find((node) => node.type === ThinkingFold);
        let view = fold();
        assert.ok(view, "the row renders through the shared ThinkingFold");
        assert.equal(view.props.text, THINK_TEXT, "the fold carries the whole thinking text");
        assert.equal(view.props.expanded, false, "collapsed by default (the preference off)");
        // Collapsed: one toggle button — the session's own icon and the
        // first-line preview in its label; the Markdown body is absent.
        let out = walk(renderHook(ThinkingFold, view.props).result);
        const button = out.find((node) => node.props?.["aria-expanded"] === false);
        assert.ok(button, "the collapsed row is one toggle button");
        assert.ok(out.some((node) => node.type === ThinkingIcon), "the same thinking icon as the session's blocks");
        assert.equal(button.props["aria-label"], `i18n.thinking: E2E-P53-THINK-P1`, "the label carries the first-line preview");
        assert.equal(out.some((node) => node.type === ReactMarkdown && node.props.children === "E2E-P53-THINK-P1"), true, "the preview is the first line only");
        assert.equal(out.some((node) => node.type === SafeMarkdownBody), false, "the body is not rendered while collapsed");
        // Click → expanded: the full text renders as the row's Markdown body.
        button.props.onClick();
        row.flush();
        view = fold();
        assert.equal(view.props.expanded, true, "a click expands the row");
        out = walk(renderHook(ThinkingFold, view.props).result);
        const body = out.find((node) => node.type === SafeMarkdownBody);
        assert.ok(body, "the expanded row renders the thinking text as Markdown");
        assert.equal(body.props.children, THINK_TEXT, "the body carries the whole thinking text");
        // Click again → collapsed.
        out.find((node) => node.props?.["aria-expanded"] === true).props.onClick();
        row.flush();
        assert.equal(fold().props.expanded, false, "a second click collapses the row");
      } finally {
        row.unmount();
      }
    } finally {
      state.restore();
    }
  } finally {
    window.restore();
  }
});

test("polyforge#53 AC2: the 「默认展开思考块」 preference expands a thinking row by default", () => {
  const window = stubWindow();
  window.stored = "true";
  try {
    const state = setup({ snapshot: snapshotOf([thinkingLine("th1")]) });
    try {
      const rowElement = thinkingRow(state);
      assert.ok(rowElement, "the thinking line renders as its own fold row");
      const row = renderHook(rowElement.type, rowElement.props);
      try {
        const view = walk(row.result).find((node) => node.type === ThinkingFold);
        assert.ok(view, "the row renders through the shared ThinkingFold");
        assert.equal(view.props.expanded, true, "the preference expands the row by default");
        const out = walk(renderHook(ThinkingFold, view.props).result);
        assert.equal(out.some((node) => node.type === SafeMarkdownBody && node.props.children === THINK_TEXT), true, "the body renders without a click");
      } finally {
        row.unmount();
      }
    } finally {
      state.restore();
    }
  } finally {
    window.restore();
  }
});

test("polyforge#53 AC2: the preference's broadcast updates an already-rendered thinking row", () => {
  const window = stubWindow();
  try {
    const state = setup({ snapshot: snapshotOf([thinkingLine("th1")]) });
    try {
      const rowElement = thinkingRow(state);
      assert.ok(rowElement, "the thinking line renders as its own fold row");
      const row = renderHook(rowElement.type, rowElement.props);
      try {
        const fold = () => walk(row.result).find((node) => node.type === ThinkingFold);
        assert.equal(fold()?.props.expanded, false, "collapsed before the change");
        // The settings panel toggles the preference and broadcasts the event
        // the row registered a listener for.
        window.stored = "true";
        assert.equal(window.listeners.some(([name]) => name === THINKING_EXPANDED_EVENT), true, "the row listens to the preference's broadcast");
        for (const [name, listener] of window.listeners) if (name === THINKING_EXPANDED_EVENT) listener();
        row.flush();
        assert.equal(fold()?.props.expanded, true, "the rendered row follows the broadcast");
      } finally {
        row.unmount();
      }
    } finally {
      state.restore();
    }
  } finally {
    window.restore();
  }
});
