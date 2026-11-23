// carries: components/PolyforgeStepControls*, lib/polyforge-overrides*
// PolyforgeStepControls.tier-source.test.mjs (polyforge#81 u7, Q13 A):
// the source line shows the real source even when the effective chain is
// empty — a declared-empty yaml level still governs, and hiding its
// source made the empty chain read as "no chain declared". With no source
// at all the line stays the harness-default text. The component runs for
// real through the react-hook shim; t() returns the key, so the test
// asserts keys and the daemon's source string, never English copy.
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
const sourceLine = (hook) => nodes(hook).find((node) => node.type === "span" && "data-pf-step-controls-source" in node.props);
const textOf = (node) => JSON.stringify(node.props.children);

function render(row) {
  return renderHook(PolyforgeStepControls, {
    wi: "wi_A1", step: "review", skippable: false, unstarted: true, row, onApplied() {},
  });
}

test("the source shows with an empty effective chain", () => {
  const hook = render({ id: "review", status: "pending", effective: [], source: "steps.proj.review.models" });
  const line = sourceLine(hook);
  assert.ok(line, "the source line must render for an unstarted step");
  const text = textOf(line);
  assert.ok(text.includes("steps.proj.review.models"), `the real source must show, got ${text}`);
  assert.ok(text.includes("—"), `an empty chain renders as (none), got ${text}`);
  assert.ok(!text.includes("polyforge.stepControls.sourceNone"), "a real source must not read as no chain");
});

test("no source at all keeps the harness-default text", () => {
  const hook = render({ id: "review", status: "pending", effective: [], source: "" });
  const line = sourceLine(hook);
  assert.ok(line, "the source line must render for an unstarted step");
  assert.ok(textOf(line).includes("polyforge.stepControls.sourceNone"), `no source must read as no chain, got ${textOf(line)}`);
});

test("a non-empty chain still shows its candidates and source", () => {
  const hook = render({ id: "review", status: "pending", effective: ["pi:prov/a", "claude:b"], source: "step override tier main" });
  const text = textOf(sourceLine(hook));
  assert.ok(text.includes("pi:prov/a, claude:b"), `the candidates must show, got ${text}`);
  assert.ok(text.includes("step override tier main"), `the tier source must show, got ${text}`);
});
