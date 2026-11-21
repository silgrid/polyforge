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
