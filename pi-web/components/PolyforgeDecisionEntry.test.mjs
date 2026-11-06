// carries: components/PolyforgeDecisionEntry.tsx, lib/polyforge-decisions-pure.ts
import "#test-env";
import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// a decision-feed entry whose question the owner already
// answered (the daemon marks it `replied`, `attention:false`, still
// unresolved until the step hands off) renders muted with the replied state
// — 「已回复，等 step 处理」 — and offers no 「忽略」. The component renders on
// the react-hook shim (hooks/__fixtures__/react-hook-shim.mjs) with the JSX
// runtime fixture, so the element tree is plain objects; `t` is the stub's
// key echo, so the test asserts message keys (the PolyforgeTasks.refresh
// convention).
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
    "@/lib/polyforge-decisions-pure": modulePath("lib/polyforge-decisions-pure.ts"),
    "@/lib/polyforge-tasks-pure": modulePath("lib/polyforge-tasks-pure.ts"),
  },
});
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { PolyforgeDecisionEntry } = await jiti.import("./PolyforgeDecisionEntry.tsx");
const { sanitizePolyforgeDecision } = await jiti.import("../lib/polyforge-decisions-pure.ts");

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
const find = (nodes, attribute) => nodes.find((node) => attribute in node.props);
const text = (node) => [node?.props.children].flat().filter((child) => typeof child === "string").join("");

test("a replied entry renders muted with the replied state and no dismiss", () => {
  // The daemon's wire entry (protocol.Decision): an answered question.
  const decision = sanitizePolyforgeDecision({
    id: 31, at: "2026-11-12T08:10:00.123456789Z", kind: "work_item", profile: "default", wi: "wi_W31", slug: "demo#31",
    class: "needs_input", source: "needs_input", phrase: "the step asks the owner", question_id: "2026-11-12T08:10:00.123456789Z",
    attention: false, resolved: false, dismissed: false, replied: true, replied_at: "2026-11-12T08:15:30.5Z",
  });
  assert.ok(decision, "the replied entry is a valid entry");
  assert.equal(decision.replied, true, "the sanitizer keeps replied");
  const hook = renderHook(PolyforgeDecisionEntry, { decision, busy: false, onDismiss: () => assert.fail("no dismiss") });
  const nodes = walk(hook.result);
  const root = find(nodes, "data-pf-decision");
  assert.equal(root.props["data-pf-decision"], 31);
  assert.equal(root.props["data-pf-decision-state"], "replied", "the entry's state is replied");
  assert.equal(root.props.style.opacity, 0.55, "muted like every non-open entry");
  const stateLabel = find(nodes, "data-pf-decision-state-label");
  assert.equal(text(stateLabel), "polyforge.decisions.state.replied", "the state label is the replied message");
  assert.equal(stateLabel.props.style.color, "var(--text-dim)", "the state label is dim, not amber");
  assert.notEqual(find(nodes, "data-pf-decision-label").props.style.color, "#d97706", "the class label is not amber");
  assert.equal(find(nodes, "data-pf-decision-dismiss"), undefined, "no 忽略 on a replied entry");
});
