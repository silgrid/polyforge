// carries: components/HarnessModelSettings*, lib/harness/model-view*
import "#test-env";
import test from "node:test";
import assert from "node:assert/strict";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { HarnessModelSettings, HarnessModelTable } = await jiti.import("./HarnessModelSettings.tsx");

test("模型页包含默认模型、三列清单和 pi 编辑区", () => {
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelSettings, { cwd: null })));
  assert.match(html, /data-model-page="unified"/);
  assert.match(html, /Default model/);
  assert.match(html, /Model list/);
  assert.match(html, /Chat availability/);
  assert.match(html, /pi/);
});

test("unified table renders both harnesses, exact provider identity and unsupported Claude chat", () => {
  const rows = [
    { provider: "anthropic", id: "opus", name: "Opus", harnesses: ["pi", "claude"], chatAvailable: true, reasoning: ["high"], isDefault: true },
    { provider: "gateway", id: "opus", name: "Gateway Opus", harnesses: ["pi"], chatAvailable: false, reasoning: ["minimal"], isDefault: false },
    { provider: "claude", id: "sonnet", name: "Sonnet", harnesses: ["claude"], chatAvailable: false, reasoning: ["adaptive"], isDefault: false },
  ];
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelTable, { rows })));
  assert.match(html, /anthropic/);
  assert.match(html, /Gateway Opus/);
  assert.match(html, /Sonnet/);
  assert.match(html, /Not supported/);
  assert.match(html, /high/);
  assert.match(html, /minimal/);
  assert.match(html, /adaptive/);
  assert.match(html, /Available for/);
  assert.match(html, /Chat availability/);
});


// polyforge#151 (85b2, u5): the model list's step-tier column — the fifth
// column over the Host's [step_tiers] catalog, per-row states from the pure
// projection (全部 / 指定 with per-harness lines / 未设置 / 多档), the
//「应用到」 targets being the row's own harnesses, and an incapable catalog
// read leaving the column read-only with the other columns untouched.
const TIER_ROWS = [
  { provider: "gateway", id: "opus", name: "Gateway Opus", harnesses: ["pi"], chatAvailable: false, reasoning: ["minimal"], isDefault: false },
  { provider: "claude", id: "sonnet", name: "Sonnet", harnesses: ["claude"], chatAvailable: false, reasoning: ["adaptive"], isDefault: false },
  { provider: "anthropic", id: "opus", name: "Opus", harnesses: ["pi", "claude"], chatAvailable: true, reasoning: ["high"], isDefault: false },
];

test("the table's fifth column: the header, the per-row tier states and the row's own harnesses as the apply-to targets", () => {
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelTable, {
    rows: TIER_ROWS,
    tier: {
      capable: true,
      reason: null,
      tiers: [
        { tier: "default", candidates: ["pi:gateway/opus", "claude:sonnet"] },
        { tier: "fast-chat", candidates: ["pi:gateway/opus"] },
      ],
    },
  })));
  assert.match(html, /Step tier/, "the fifth column's header");
  assert.match(html, /All: default/, "a row whose every harness sits in the same single tier shows 全部");
  assert.match(html, /Specific/, "a row with differing or multi tiers shows 指定");
  assert.match(html, /Multiple tiers: default、fast-chat/, "a harness in several tiers shows 多档 with its tiers");
  assert.match(html, /Not set/, "an unset row shows 未设置");
  assert.match(html, /data-step-tier-select/, "the tier select renders for a defined row");
  assert.match(html, /Apply to/, "the apply-to choice renders");
  // the merged pi+claude row carries the apply-to over its own two harnesses
  // (the per-harness checkboxes render in specific mode — the e2e clicks them)
  const merged = html.slice(html.indexOf("anthropic"));
  assert.match(merged, /Apply to/, "the merged row's apply-to");
  assert.match(merged, /data-mode="all"/, "the default choice is every harness the row offers");
});

test("an incapable catalog read leaves the tier column read-only, the other columns untouched", () => {
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelTable, {
    rows: TIER_ROWS,
    tier: { capable: false, reason: "the step-tier catalog needs pf 0.2.4 or newer (found an unparseable version)", tiers: [] },
  })));
  assert.match(html, /Step tier/, "the column stays");
  assert.ok(!html.includes("data-step-tier-select"), "no tier select without a capable catalog");
  assert.match(html, /Chat availability/, "the other columns are untouched");
  assert.match(html, /Not supported/, "the claude chat note stays");
});

// polyforge#151 (85b2, review r2 #1, u8's redo): the per-step re-read pinned
// at the unit level, on the hook shim — the e2e suite's cold compile on a
// fresh worktree outlasts the replay's bound, so the recorded run is this
// fast, deterministic one (S85-33 stays as the browser evidence). The page
// loads one catalog; an external writer changes it before the save; every
// plan step must re-read the Host catalog together with the CAS hash, so the
// external candidate survives the move — the page-load-catalog plan would
// unset the tier and destroy it while the fresh hash lets the CAS pass.
test("the tier cell re-reads the catalog with the CAS hash on every plan step", async () => {
  const shimJiti = createJiti(import.meta.url, {
    jsx: { runtime: "automatic" },
    moduleCache: false,
    alias: {
      react: new URL("../hooks/__fixtures__/react-hook-shim.mjs", import.meta.url).pathname,
      "react/jsx-runtime": new URL("../hooks/__fixtures__/react-jsx-runtime.mjs", import.meta.url).pathname,
      "@/hooks/useI18n": new URL("../hooks/__fixtures__/polyforge-tasks-stubs.mjs", import.meta.url).pathname,
      "./SettingsUi": new URL("../hooks/__fixtures__/polyforge-tasks-stubs.mjs", import.meta.url).pathname,
      "@/lib/harness/model-view": new URL("../lib/harness/model-view.ts", import.meta.url).pathname,
      "@/lib/step-tiers": new URL("../lib/step-tiers.ts", import.meta.url).pathname,
      "@/lib/enabled-models": new URL("../lib/enabled-models.ts", import.meta.url).pathname,
      "@/lib/model-catalog": new URL("../lib/model-catalog.ts", import.meta.url).pathname,
      "@/lib/model-discovery": new URL("../lib/model-discovery.ts", import.meta.url).pathname,
      "@/lib/model-context": new URL("../lib/model-context.ts", import.meta.url).pathname,
      "@/lib/model-provider-check": new URL("../lib/model-provider-check.ts", import.meta.url).pathname,
      "@/lib/model-image-limits": new URL("../lib/model-image-limits.ts", import.meta.url).pathname,
      "@/lib/settings-navigation": new URL("../lib/settings-navigation.ts", import.meta.url).pathname,
    },
  });
  const { renderHook } = await import(new URL("../hooks/__fixtures__/react-hook-shim.mjs", import.meta.url).href);
  const { StepTierCell } = await shimJiti.import("./HarnessModelSettings.tsx");

  const settle = async () => {
    for (let index = 0; index < 12; index++) await new Promise((resolve) => setImmediate(resolve));
  };
  const walk = (node, out = []) => {
    if (Array.isArray(node)) {
      for (const child of node) walk(child, out);
      return out;
    }
    if (!node || typeof node !== "object" || !("props" in node)) return out;
    if (typeof node.type === "function") return walk(node.type(node.props), out);
    out.push(node);
    walk(node.props.children, out);
    return out;
  };

  // what the page loaded (stale the moment the external writer lands)
  const pageLoadItems = [
    { key: "step_tiers.default", value: ["pi:anthropic/opus"], source: "file", class: "live", schema: { kind: "model-list" } },
    { key: "step_tiers.fast-chat", value: ["pi:tierdemo/other"], source: "file", class: "live", schema: { kind: "model-list" } },
  ];
  // the external write: default also holds a candidate the page never saw
  const freshItems = [
    { key: "step_tiers.default", value: ["pi:anthropic/opus", "pi:tierdemo/external"], source: "file", class: "live", schema: { kind: "model-list" } },
    { key: "step_tiers.fast-chat", value: ["pi:tierdemo/other"], source: "file", class: "live", schema: { kind: "model-list" } },
  ];
  const reads = [];
  const writes = [];
  const hook = renderHook(StepTierCell, {
    row: { provider: "anthropic", id: "opus", name: "Opus", harnesses: ["pi"], chatAvailable: false, reasoning: [], isDefault: false },
    tier: { capable: true, reason: null, tiers: [
      { tier: "default", candidates: ["pi:anthropic/opus"] },
      { tier: "fast-chat", candidates: ["pi:tierdemo/other"] },
    ] },
    tierRead: async () => {
      reads.push(Date.now());
      return { ok: true, items: freshItems, hash: "fresh-sha" };
    },
    tierWrite: async (write, hash) => {
      writes.push({ ...write, hash });
      return { ok: true };
    },
    onTierSaved() {},
  });
  for (let round = 0; round < 6; round++) { await settle(); hook.flush(); }
  const all = walk(hook.result);
  if (!all.some((node) => node.type === "select")) {
    console.log("U8DBG nodes:", all.length, "types:", JSON.stringify([...new Set(all.map((n) => typeof n.type === "string" ? n.type : "fn"))]));
    console.log("U8DBG text:", JSON.stringify(all.map((n) => typeof n.props.children === "string" ? n.props.children : "").filter(Boolean).slice(0, 10)));
  }
  const select = all.find((node) => node.type === "select" && JSON.stringify(node.props.children).includes("fast-chat"));
  assert.ok(select, "the tier select renders");
  select.props.onChange({ target: { value: "fast-chat" } });
  hook.flush();
  const save = walk(hook.result).find((node) => node.type === "button" && String(node.props.children ?? "").includes("stepTierApplyTo.submit"));
  assert.ok(save, "the apply-to submit renders");
  save.props.onClick();
  for (let round = 0; round < 8; round++) { await settle(); hook.flush(); }
  assert.ok(reads.length >= 1, "the submit read the Host catalog (the per-step re-read)");
  const defaultSet = writes.find((write) => write.action === "set" && write.tier === "default");
  assert.ok(defaultSet, "the stale default tier was never re-written (the external candidate was destroyed by a page-load-catalog plan)");
  assert.deepEqual(defaultSet.candidates, ["pi:tierdemo/external"],
    "the external concurrent candidate is preserved — the plan came from the fresh read");
  assert.equal(defaultSet.hash, "fresh-sha", "the write carries the hash from the same read that planned it");
  const moved = writes.find((write) => write.action === "set" && write.tier === "fast-chat");
  assert.deepEqual(moved?.candidates, ["pi:tierdemo/other", "pi:anthropic/opus"], "the row moved over");
  assert.ok(!writes.some((write) => write.action === "unset"), "no tier was left empty and unset by a stale plan");
});
