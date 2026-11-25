// carries: components/HarnessModelSettings*, lib/harness/model-view*
import "#test-env";
import test from "node:test";
import assert from "node:assert/strict";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { HarnessModelSettings, HarnessModelTable, ModelPageNav } = await jiti.import("./HarnessModelSettings.tsx");
const { ModelStepTierDrawer } = await jiti.import("./ModelStepTierDrawer.tsx");

test("four model subpages: the sidebar nav carries only the pages, and only the chosen pane renders", () => {
  const page = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelSettings, { cwd: null })));
  assert.match(page, /data-model-page="unified"/);
  assert.ok(!/<h2[\s>]/.test(page), "the page-level 「模型」 h2 is gone (the tab already names the page)");
  assert.equal((page.match(/data-model-subpage=/g) ?? []).length, 4, "the sidebar holds the four subpages and nothing else");
  for (const key of ["default", "list", "providers", "claude"]) assert.match(page, new RegExp(`data-model-subpage="${key}"`), `${key} is one of the four subpages`);
  assert.match(page, /data-model-page-pane="default"/, "the default subpage is the one mounted on open");
  assert.ok(!page.includes('data-model-page-pane="list"'), "the other panes are not mounted beside it");

  const nav = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(ModelPageNav, { page: "list", onSelect: () => {} })));
  assert.match(nav, /data-model-subpage="list"[^>]*aria-current="page"|aria-current="page"[^>]*data-model-subpage="list"/, "the chosen subpage is marked active");
  assert.ok(!nav.includes("Opus"), "the sidebar never copies a model row");

  const list = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelSettings, { cwd: null, initialSubpage: "list" })));
  assert.match(list, /data-model-page-pane="list"/);
  assert.match(list, /data-model-skeleton/, "the list subpage's first read is a skeleton, not a table");
});

test("the Provider subpage is flat: no second split view, only its own form footer", () => {
  const page = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelSettings, { cwd: null, initialSubpage: "providers" })));
  assert.match(page, /data-model-page-pane="providers"/);
  assert.match(page, /data-provider-page="flat"/);
  assert.equal((page.match(/config-split-view/g) ?? []).length, 1, "the Provider editor renders no nested split view");
  assert.equal((page.match(/class="config-footer"/g) ?? []).length, 1, "the Provider subpage has one form footer, not a nested one");
  assert.match(page, /data-provider-footer=""/);
});

test("unified table renders both harnesses, exact provider identity and always-available Claude chat", () => {
  const rows = [
    { provider: "anthropic", id: "opus", name: "Opus", harnesses: ["pi", "claude"], chatAvailable: true, reasoning: ["high"], isDefault: true },
    { provider: "gateway", id: "opus", name: "Gateway Opus", harnesses: ["pi"], chatAvailable: false, reasoning: ["minimal"], isDefault: false },
    { provider: "claude", id: "sonnet", name: "Sonnet", harnesses: ["claude"], chatAvailable: false, reasoning: ["adaptive"], isDefault: false },
  ];
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelTable, { rows })));
  assert.match(html, /anthropic/);
  assert.match(html, /Gateway Opus/);
  assert.match(html, /Sonnet/);
  assert.match(html, /Always available/);
  assert.ok(!html.includes("Not supported"), "the old 「不支持」 wording is gone");
  assert.match(html, /high/);
  assert.match(html, /minimal/);
  assert.match(html, /adaptive/);
  assert.match(html, /Available for/);
  assert.match(html, /Chat availability/);
});


// polyforge#258 (T3) AC2: the default model and reasoning are not written on
// change; the subpage offers one explicit save action, disabled while the
// draft is empty (owner plan review item 1 A: dirty + 「保存（N）」).
test("the default model is saved explicitly, never on change", () => {
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelSettings, { cwd: null, initialSubpage: "default" })));
  const tag = html.match(/<button[^>]*data-model-save=""[^>]*>/)?.[0] ?? "";
  assert.ok(tag, "the default subpage carries the explicit save action");
  assert.match(tag, /disabled/, "nothing changed yet, so the save action is disabled");
  assert.match(html, /Save/, "the action is named as a save");
});

// polyforge#258 (T3) AC1/AC2: claude's chat cell says 「始终可用」 with the
// reason in its tooltip (owner plan review item 4: never 「不支持」), and pi's
// reasoning select names the global level it follows instead of printing a
// second value beside the select. Expected text comes from the owner's ruling
// and the Global level passed in (`high`), never from the component's output.
test("claude chat is always available with its reason, and pi reasoning follows the global level", () => {
  const rows = [
    { provider: "gateway", id: "one", name: "Gateway One", harnesses: ["pi"], chatAvailable: true, reasoning: ["high"], isDefault: false },
    { provider: "anthropic", id: "opus", name: "Opus", harnesses: ["claude"], chatAvailable: false, reasoning: ["xhigh"], isDefault: false },
  ];
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelTable, {
    rows,
    levels: { "gateway:one": ["low", "medium", "high"] },
    piSettings: { reasoningEffort: { raw: "high" }, perModelReasoningEffort: {} },
    onEffort: () => {},
  })));
  assert.match(html, /data-claude-chat="always"/, "the claude cell marks its always-on state");
  assert.match(html, /Always available/);
  assert.match(html, /Claude sessions do not disable a model/, "the reason is readable in the tooltip");
  assert.ok(!html.includes("Not supported"), "the unexplained 「不支持」 is gone");
  assert.match(html, /Follow global \(high\)/, "the reasoning select's default names the global level it follows");
  assert.match(html, /data-reasoning="readonly">xhigh/, "claude's reasoning is a read-only value, not a select");
  const claudeRow = html.slice(html.indexOf("anthropic"));
  assert.ok(!/data-reasoning="[^"]*"[^>]*><select/.test(claudeRow), "no reasoning select on a claude row");
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

test("tier cells are small labels: a shared tier by name, differing harnesses by 归属, unset as —, and no inline editor", () => {
  const html = table(TIER_ROWS, TIER);
  assert.match(html, /Step tier/, "the fifth column's header");
  assert.match(html, /data-step-tier-badge="all">default</, "a row whose every harness sits in the same single tier shows the tier name");
  // the merged pi+claude row: pi in default, claude in fast-chat
  const merged = html.slice(html.indexOf("anthropic"));
  assert.match(merged, /data-step-tier-badge="pi">pi: default</, "pi's own tier is named with its harness");
  assert.match(merged, /data-step-tier-badge="claude">claude: fast-chat</, "claude's own tier is named with its harness");
  assert.match(html, /data-step-tier-state="unset"/, "an unset row shows 「—」");
  // the cell is one small label that opens the drawer: no inline editor
  assert.ok(!html.includes("data-step-tier-select"), "the inline tier select is gone");
  assert.ok(!html.includes("Apply to"), "the inline apply-to control is gone");
  assert.ok(!html.includes("<select"), "no select renders in the tier column");
  assert.ok(!html.includes('type="checkbox"'), "no checkbox renders in the tier column");
  assert.ok(!html.includes('type="radio"'), "no radio renders in the tier column");
  assert.equal((html.match(/<button/g) ?? []).length, TIER_ROWS.length, "each capable row's cell is exactly one button");
  assert.match(html, /<button[^>]*data-step-tier-cell="editable"[^>]*aria-haspopup="dialog"/, "the label opens a dialog");
  // the other columns are untouched
  assert.match(html, /Chat availability/);
  assert.match(html, /Always available/);
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
  assert.match(html, /Always available/, "the claude chat note stays");
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

// polyforge#257 (T2 止血) pinned the old provider sidebar's shared group
// label; polyforge#258 (T3) replaces that sidebar with the four subpages, so
// the old shape is gone (no compatibility path). The subpage assertions above
// are the replacement.

// polyforge#258 (T3) AC2: the step-tier cell is a label; the tier choice and
// 「应用到」 live inside the right-side drawer (owner plan review item 3 A).
test("the step-tier drawer edits the tier and keeps 应用到 inside it", () => {
  const row = { provider: "gateway", id: "one", name: "Gateway One", harnesses: ["pi", "claude"], chatAvailable: false, reasoning: [], isDefault: false };
  const tier = { capable: true, reason: null, tiers: [
    { tier: "default", candidates: ["pi:gateway/one"] },
    { tier: "fast-chat", candidates: ["claude:one"] },
  ] };
  const drawer = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(ModelStepTierDrawer, {
    row, tier,
    tierRead: async () => ({ ok: true, items: [], hash: null }),
    tierWrite: async () => ({ ok: true }),
    onClose: () => {},
    onSaved: () => {},
  })));
  assert.match(drawer, /role="dialog"/);
  assert.match(drawer, /data-step-tier-drawer=""/);
  assert.match(drawer, /data-step-tier-select=""/, "the drawer carries the tier select");
  assert.match(drawer, /Apply to/, "「应用到」 is inside the drawer");
  assert.match(drawer, /data-step-tier-current="pi">pi: default/, "each harness's current tier shows");
  assert.match(drawer, /data-step-tier-current="claude">claude: fast-chat/);

  const table = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelTable, {
    rows: [row], tier, onOpenTier: () => {},
  })));
  assert.match(table, /<button[^>]*data-step-tier-cell="editable"[^>]*data-step-tier-open=""/, "the cell opens the drawer");
  assert.ok(!table.includes("<select"), "no tier select in the cell");
  assert.ok(!table.includes("Apply to"), "no apply-to in the cell");
});

// polyforge#271 AC1 (A3/A4, Q5 A): the model page's first read is a skeleton;
// a successful empty read is 「没有模型」 with its actions; a failure is an
// alert with a retry and is never read as empty; a partial success keeps the
// rows that answered and shows the failure beside them.
test("model subpages expose loading empty and partial failure", async () => {
  const { readView } = await jiti.import("@/lib/settings-read-states.ts");
  assert.deepEqual(readView({ loading: true, error: false, count: 0 }), { kind: "loading", staleError: false });
  assert.deepEqual(readView({ loading: false, error: true, count: 0 }), { kind: "error", staleError: false }, "a failed read is never the empty state");
  assert.deepEqual(readView({ loading: false, error: false, count: 0 }), { kind: "empty", staleError: false });
  assert.deepEqual(readView({ loading: false, error: true, count: 1 }), { kind: "ready", staleError: true }, "a partial read keeps its rows and shows the failure");
  assert.deepEqual(readView({ loading: false, error: false, count: 1 }), { kind: "ready", staleError: false });

  // SSR runs no effects, so the page's initial state is its first read.
  const pending = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelSettings, { cwd: null })));
  assert.match(pending, /data-model-skeleton/, "the default subpage's first read is a skeleton");
  assert.ok(!pending.includes("No model is available"), "a pending read is not the empty state");

  const pendingList = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelSettings, { cwd: null, initialSubpage: "list" })));
  assert.match(pendingList, /data-model-skeleton/, "the list subpage's first read is a skeleton");

  const { ModelEmptyState } = await jiti.import("./HarnessModelSettings.tsx");
  const empty = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(ModelEmptyState, {
    onOpenAccount: () => {}, onOpenProviders: () => {},
  })));
  assert.ok(empty.includes("No model is available"), "an empty model set names what is missing");
  assert.match(empty, /data-model-action="open-account"/, "the empty model set offers the account action");
  assert.match(empty, /data-model-action="open-providers"/, "the empty model set offers the Providers action");
});

// polyforge#271 round 2 (review r2#3/#5): a refresh with rows keeps them (no
// skeleton), and the page's failure block is an alert with a working retry.
test("model page failure renders an alert with a working retry", async () => {
  const { ModelPageError } = await jiti.import("./HarnessModelSettings.tsx");
  const { readView } = await jiti.import("@/lib/settings-read-states.ts");
  assert.equal(readView({ loading: true, error: false, count: 1 }).kind, "ready", "a refresh with rows keeps them on screen");
  let retries = 0;
  const tree = ModelPageError({ summary: "The read failed.", onRetry: () => { retries += 1; } });
  const walk = (node, out = []) => {
    if (Array.isArray(node)) { for (const child of node) walk(child, out); return out; }
    if (!node || typeof node !== "object" || !("props" in node)) return out;
    if (typeof node.type === "function") return walk(node.type(node.props), out);
    out.push(node);
    walk(node.props.children, out);
    return out;
  };
  const nodes = walk(tree);
  assert.ok(nodes.some((node) => node.props?.role === "alert"), "the failure is an alert");
  const retry = nodes.find((node) => node.props?.["data-model-retry"] !== undefined);
  assert.ok(retry, "the retry control is rendered");
  retry.props.onClick();
  assert.equal(retries, 1, "the retry control did not call the read again");
});
