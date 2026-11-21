// carries: components/PolyforgeStepControls*, lib/polyforge-params*, lib/polyforge-overrides*, app/api/polyforge/override/*
// PolyforgeStepControls.params.test.mjs (u7, polyforge#79): the selected
// step's params on the react-hook shim (the Tasks-refresh precedent): the
// controls run for real through their own hooks, the test — never the
// network — decides when each read settles. The params section renders
// for every selected step (a started step read-only — AC1's「已经跑过的
// step 只读」), the write controls only for an unstarted one; a param set
// and clear ride the same one-action POST face (`pf override --param` /
// `--clear-param`), and the daemon's typed refusal shows inline (AC2).
import "#test-env";
import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

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
    "@/lib/polyforge-status": modulePath("lib/polyforge-status.ts"),
    "@/lib/step-tiers": modulePath("lib/step-tiers.ts"),
    type: shimPath,
  },
});
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { PolyforgeStepControls } = await jiti.import("./PolyforgeStepControls.tsx");

const PARAMS_ROW = {
  id: "review",
  status: "in_progress",
  params: [
    { name: "flag", value: "on", source: "step override" },
    { name: "image", value: "reg/x:1", source: "steps.proj.review.params.image" },
  ],
};

const settle = async () => {
  for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve));
};

function walk(node, out = []) {
  if (Array.isArray(node)) {
    for (const child of node) walk(child, out);
    return out;
  }
  if (!node || typeof node !== "object" || !("props" in node)) return out;
  if (typeof node.type === "function") return walk(node.type(node.props), out);
  out.push(node);
  walk(node.props.children, out);
  return out;
}
const nodes = (hook) => walk(hook.result.current ?? hook.result);
const sectionOf = (hook) => nodes(hook).find((node) => node.type === "div" && "data-pf-step-controls-params" in node.props);
const paramRows = (hook) => nodes(hook).filter((node) => node.type === "span" && "data-pf-param" in node.props);
const textOf = (node) => JSON.stringify(node.props.children);

function setup(props) {
  const originalFetch = globalThis.fetch;
  const posts = [];
  globalThis.fetch = (url, init) => {
    const request = { url, init, settled: false };
    request.promise = new Promise((resolve) => {
      request.resolve = (status, body) => {
        request.settled = true;
        resolve(new Response(JSON.stringify(body ?? { ok: true, result: { wi: "wi_A1", changed: true, posture: "queued", published: false, step: { id: "review", status: "pending", skippable: false } } }), { status, headers: { "Content-Type": "application/json" } }));
      };
    });
    posts.push(request);
    return request.promise;
  };
  const hook = renderHook(PolyforgeStepControls, {
    wi: "wi_A1", profile: undefined, step: "review", skippable: false, unstarted: true, onApplied() {}, ...props,
  });
  return { hook, posts, done: () => { globalThis.fetch = originalFetch; } };
}

test("an unstarted step renders the params section read-write: every declared param's final value and source, a set and a clear per param", () => {
  const { hook, done } = setup({ params: PARAMS_ROW });
  const section = sectionOf(hook);
  assert.ok(section, "the params section renders");
  const rows = paramRows(hook);
  assert.equal(rows.length, 2, "one row per declared param");
  assert.match(textOf(rows[0]), /flag/);
  assert.match(textOf(rows[0]), /on/);
  assert.match(textOf(rows[0]), /step override/, "the per-ticket layer's label");
  assert.match(textOf(rows[1]), /steps\.proj\.review\.params\.image/, "the project layer's label");
  for (const name of ["flag", "image"]) {
    assert.ok(nodes(hook).some((n) => n.type === "input" && n.props["data-pf-param-input"] === name), `the ${name} input`);
    assert.ok(nodes(hook).some((n) => n.type === "button" && n.props["data-pf-param-set"] === name), `the ${name} set`);
    assert.ok(nodes(hook).some((n) => n.type === "button" && n.props["data-pf-param-clear"] === name), `the ${name} clear`);
  }
  done();
});

test("a started step with no params view renders nothing at all — no shell shrinks the progress region (the e2e P31A finding)", () => {
  const hook = renderHook(PolyforgeStepControls, {
    wi: "wi_A1", profile: undefined, step: "review", skippable: false, unstarted: false, onApplied() {},
  });
  assert.equal(hook.result ?? null, null, "nothing renders");
  hook.flush();
});

test("a started step is read-only: the params rows stay, the write controls and the model/skip ones never render (AC1)", () => {
  const { hook, done } = setup({ params: PARAMS_ROW, unstarted: false, overlay: undefined, row: undefined });
  assert.equal(paramRows(hook).length, 2, "the read-only rows stay");
  assert.equal(nodes(hook).filter((n) => "data-pf-param-input" in (n.props ?? {})).length, 0, "no param input");
  assert.equal(nodes(hook).filter((n) => "data-pf-param-set" in (n.props ?? {})).length, 0, "no param set");
  assert.equal(nodes(hook).filter((n) => "data-pf-step-controls-apply" in (n.props ?? {})).length, 0, "no model setter");
  assert.equal(nodes(hook).filter((n) => "data-pf-step-controls-skip" in (n.props ?? {})).length, 0, "no skip switch");
  done();
});

test("a param set posts the one-action body and the refusal shows inline (AC2)", async () => {
  const { hook, posts, done } = setup({ params: PARAMS_ROW });
  const input = nodes(hook).find((n) => n.type === "input" && n.props["data-pf-param-input"] === "image");
  input.props.onChange({ target: { value: "reg/new:2" } });
  hook.flush();
  nodes(hook).find((n) => n.type === "button" && n.props["data-pf-param-set"] === "image").props.onClick();
  await settle();
  assert.equal(posts.length, 1, "one POST");
  assert.equal(posts[0].url, "/api/polyforge/override");
  assert.deepEqual(JSON.parse(posts[0].init.body), {
    wi: "wi_A1", action: { kind: "param", step: "review", name: "image", value: "reg/new:2" },
  });
  // the lane did not declare the name: the daemon's refusal shows inline
  await answerRefusal(hook, posts, 0);
  const error = nodes(hook).find((n) => "data-pf-step-controls-error" in (n.props ?? {}));
  assert.ok(error, "the refusal line renders");
  assert.match(textOf(error), /declares no step param/);
  // a clear posts the clear action
  nodes(hook).find((n) => n.type === "button" && n.props["data-pf-param-clear"] === "image").props.onClick();
  await settle();
  assert.deepEqual(JSON.parse(posts[1].init.body), { wi: "wi_A1", action: { kind: "clearParam", step: "review", name: "image" } });
  done();
});

async function answerRefusal(hook, posts, index) {
  posts[index].settle = true;
  // resolve with a refusal body through the recorded promise
  posts[index].resolve(409, { ok: false, error: 'DAEMON-400: the lane declares no step param "nosuch" on step review: a param is set only where the lane declares it' });
  await settle();
  hook.flush();
}

// polyforge#151 (85b2, u6): the override row is the shared picker — the
// tier catalog rides the watch snapshot (G11: no read of its own), the
// manual fill keeps its data-pf-step-controls-model hook, the write shape
// stays { kind: "models", step, models: [candidate] }, and a failed or
// degraded catalog leaves the manual fill with its reason.
test("the override row is the shared picker: a tier choice posts the full candidate, argv unchanged", async () => {
  const { hook, posts, done } = setup({ params: PARAMS_ROW, stepTiers: {
    tiers: [{ tier: "default", candidates: ["pi:ovr/p25-model", "claude:ovr-claude"] }],
    defined: { pi: ["ovr/p25-model"], claude: ["ovr-claude"] },
  } });
  const select = nodes(hook).find((n) => n.type === "select");
  assert.ok(select, "the picker's select renders beside the manual fill");
  assert.ok(nodes(hook).some((n) => n.type === "input" && n.props["data-pf-step-controls-model"] === ""), "the manual fill keeps its hook");
  select.props.onChange({ target: { value: "pi:ovr/p25-model" } });
  hook.flush();
  assert.equal(nodes(hook).find((n) => "data-pf-step-controls-model" in (n.props ?? {})).props.value, "pi:ovr/p25-model", "the choice fills the manual draft");
  nodes(hook).find((n) => n.type === "button" && "data-pf-step-controls-apply" in n.props).props.onClick();
  await settle();
  assert.equal(posts.length, 1, "one POST");
  assert.deepEqual(JSON.parse(posts[0].init.body), {
    wi: "wi_A1", action: { kind: "models", step: "review", models: ["pi:ovr/p25-model"] },
  }, "the single-model override's shape is unchanged");
  done();
});

test("a failed catalog read leaves the override's manual fill alone, with its reason", () => {
  const { hook, done } = setup({ params: PARAMS_ROW, stepTiers: { tiers: [], defined: { pi: [], claude: [] }, reason: "the step-tier catalog needs pf 0.2.4 or newer" } });
  assert.equal(nodes(hook).filter((n) => n.type === "select").length, 0, "no select without a catalog");
  assert.ok(nodes(hook).some((n) => n.type === "input" && "data-pf-step-controls-model" in n.props), "the manual fill stays");
  assert.match(nodes(hook).map(textOf).join(""), /needs pf 0\.2\.4/, "the read's reason shows beside the fill");
  done();
});
