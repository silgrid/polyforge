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


// polyforge#256 (T1, B7/B1): the model list's step-tier column is read-only.
// Expected labels come from the owner's ruling — a single shared tier shows
// its name, differing harnesses show `pi: default` / `claude: fast-chat`, an
// unset or incapable row shows 「—」 — and no interactive control or write
// callback may survive in the cell. The read-only reason is localized from
// the Host read's facts, never the server's English sentence.
const { enLocale } = await jiti.import("../lib/i18n/messages/en.ts");
const { zhCNLocale } = await jiti.import("../lib/i18n/messages/zh-CN.ts");
const { zhTWLocale } = await jiti.import("../lib/i18n/messages/zh-TW.ts");
const { translateMessage } = await jiti.import("../lib/i18n/format.ts");
const { stepTierReasonText } = await jiti.import("./HarnessModelSettings.tsx");

const TIER_ROWS = [
  { provider: "gateway", id: "opus", name: "Gateway Opus", harnesses: ["pi"], chatAvailable: false, reasoning: ["minimal"], isDefault: false },
  { provider: "claude", id: "sonnet", name: "Sonnet", harnesses: ["claude"], chatAvailable: false, reasoning: ["adaptive"], isDefault: false },
  { provider: "anthropic", id: "opus", name: "Opus", harnesses: ["pi", "claude"], chatAvailable: true, reasoning: ["high"], isDefault: false },
  { provider: "other", id: "nope", name: "Other", harnesses: ["pi"], chatAvailable: false, reasoning: [], isDefault: false },
];
const TIER = {
  capable: true,
  reason: null,
  tiers: [
    { tier: "default", candidates: ["pi:gateway/opus", "claude:sonnet", "pi:anthropic/opus"] },
    { tier: "fast-chat", candidates: ["claude:opus"] },
  ],
};
const table = (rows, tier) => renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelTable, { rows, tier })));

test("tier cells are read-only badges: a shared tier by name, differing harnesses by 归属, unset as —, and zero write controls", () => {
  const html = table(TIER_ROWS, TIER);
  assert.match(html, /Step tier/, "the fifth column's header");
  assert.match(html, /data-step-tier-badge="all">default</, "a row whose every harness sits in the same single tier shows the tier name");
  // the merged pi+claude row: pi in default, claude in fast-chat
  const merged = html.slice(html.indexOf("anthropic"));
  assert.match(merged, /data-step-tier-badge="pi">pi: default</, "pi's own tier is named with its harness");
  assert.match(merged, /data-step-tier-badge="claude">claude: fast-chat</, "claude's own tier is named with its harness");
  assert.match(html, /data-step-tier-state="unset"/, "an unset row shows 「—」");
  // no interactive control survives: no select, no apply-to, no button, no checkbox
  assert.ok(!html.includes("data-step-tier-select"), "the tier select is gone");
  assert.ok(!html.includes("Apply to"), "the apply-to control is gone");
  assert.ok(!html.includes("<select"), "no select renders in the tier column");
  assert.ok(!html.includes('type="checkbox"'), "no checkbox renders in the tier column");
  assert.ok(!html.includes('type="radio"'), "no radio renders in the tier column");
  assert.ok(!html.includes("<button"), "no button renders in the tier column");
  // the other columns are untouched
  assert.match(html, /Chat availability/);
  assert.match(html, /Not supported/);
});

test("a harness in several tiers keeps every real 归属, default first and the rest alphabetical", () => {
  const html = table(
    [{ provider: "anthropic", id: "opus", name: "Opus", harnesses: ["pi"], chatAvailable: false, reasoning: [], isDefault: false }],
    { capable: true, reason: null, tiers: [
      { tier: "fast-chat", candidates: ["pi:anthropic/opus"] },
      { tier: "default", candidates: ["pi:anthropic/opus"] },
    ] },
  );
  assert.match(html, /data-step-tier-badge="pi">pi: default、fast-chat</, "both tiers, default first");
});

test("an incapable catalog read leaves the tier column read-only, the other columns untouched", () => {
  const html = table(TIER_ROWS, { capable: false, reason: "the step-tier catalog is unavailable", tiers: [] });
  assert.match(html, /Step tier/, "the column stays");
  assert.match(html, /data-step-tier-state="readonly"/, "the cell is a read-only dash");
  assert.ok(!html.includes("data-step-tier-select"), "no tier select without a capable catalog");
  assert.match(html, /Chat availability/, "the other columns are untouched");
  assert.match(html, /Not supported/, "the claude chat note stays");
});

test("old pf reason follows locale: version, not-installed and unreadable stay distinct and localized", () => {
  const messages = { en: enLocale.messages, "zh-CN": zhCNLocale.messages, "zh-TW": zhTWLocale.messages };
  const translate = (locale) => (key, params) => translateMessage(locale, key, messages, params);
  const host = {
    pfPresent: true, pfVersion: "v0.2.3", capable: false,
    stepTiersReason: "the step-tier catalog needs pf 0.2.4-0 or newer (found v0.2.3)", items: [],
  };
  const rendered = Object.fromEntries(["en", "zh-CN", "zh-TW"].map((locale) => [locale, stepTierReasonText(host, translate(locale))]));
  assert.equal(new Set(Object.values(rendered)).size, 3, "each locale renders its own sentence");
  for (const [locale, text] of Object.entries(rendered)) {
    assert.ok(text.includes("v0.2.3"), `${locale} names the found version: ${text}`);
    assert.ok(text !== host.stepTiersReason, `${locale} renders its own sentence, never the server's: ${text}`);
    assert.ok(!text.startsWith("the step-tier catalog needs"), `${locale} never shows the server's hard-coded English: ${text}`);
  }
  const en = (patch) => stepTierReasonText({ ...host, ...patch }, translate("en"));
  assert.match(en({ pfPresent: false }), /not installed/i, "pf absent is its own reason");
  assert.match(en({ stepTiersReason: null }), /could not be read/i, "an unreadable catalog is its own reason");
  assert.match(en({ pfVersion: null }), /unparseable version/i, "an unknown version is named, never a fake number");
});
