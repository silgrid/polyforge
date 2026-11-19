// carries: components/UnifiedSetting.tsx
// polyforge#149 (#85 85a1b, AC3, owner ruling Q6 A): the 统一设置 shared
// component's four situations — 一致, 不一致, 点「统一成当前值」 (the click's
// own in-flight state), 不支持 — with the parent providing every value, read
// state and word. The status derivation is pure and pinned from the ruling's
// own words: a non-ready harness never counts as agreeing, and no harness
// ready at all is 不支持. The component is presentational: it renders with
// no i18n provider and holds no state of its own.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { UnifiedSetting, unifiedStatus } = await jiti.import("./UnifiedSetting.tsx");

const h = React.createElement;
const noop = () => {};
const TEXTS = {
  title: "Default model",
  currentValue: "Current value",
  unify: "Unify to the current value",
  consistent: "Every harness agrees.",
  unsupported: "No harness can be read.",
  saving: "Saving…",
};
const ready = (harness, value) => ({ harness, status: "ready", value });
const reason = (harness, status, why) => ({ harness, status, value: null, reason: why });

function render(props = {}) {
  return renderToStaticMarkup(h(UnifiedSetting, {
    texts: TEXTS,
    current: "claude-opus-5-5",
    values: [ready("pi", "claude-opus-5-5"), ready("claude", "claude-opus-5-5")],
    ...props,
  }));
}

test("the status derivation follows the ruling, never counting a non-ready harness as agreeing", () => {
  assert.equal(unifiedStatus([ready("pi", "a"), ready("claude", "a")]), "consistent");
  assert.equal(unifiedStatus([ready("pi", "a"), ready("claude", "b")]), "inconsistent");
  // A non-ready harness beside agreeing ones keeps it inconsistent (Q6 A).
  assert.equal(unifiedStatus([ready("pi", "a"), reason("claude", "unsupported", "no permission concept")]), "inconsistent");
  assert.equal(unifiedStatus([reason("pi", "not-installed", "absent"), reason("claude", "unknown", "unreadable")]), "unsupported");
  assert.equal(unifiedStatus([]), "unsupported");
});

test("the unified setting shows all four situations correctly", () => {
  // 一致: both values agree — the note, and no unify button (nothing to unify).
  const consistent = render();
  assert.match(consistent, /Every harness agrees\./);
  assert.ok(!consistent.includes("Unify to the current value"), "a consistent state offers the unify button");

  // 不一致: the differing values listed, and the unify button.
  const inconsistent = render({ values: [ready("pi", "claude-opus-5-5"), ready("claude", "claude-sonnet-5-5")], onUnify: noop });
  assert.ok(inconsistent.includes("claude-sonnet-5-5"), "the differing value is not listed");
  assert.match(inconsistent, /Unify to the current value/);
  assert.match(inconsistent, /data-harness="pi"/);

  // 点「统一成当前值」: the click's own in-flight state — the saving word, the button disabled.
  const clicked = render({ values: [ready("pi", "claude-opus-5-5"), ready("claude", "claude-sonnet-5-5")], busy: true, onUnify: noop });
  assert.match(clicked, /Saving…/);
  assert.match(clicked, /disabled/, "the unify button is not disabled while its one submission runs");

  // 不支持: every harness's visible reason, and no unify button.
  const unsupported = render({
    values: [reason("pi", "not-installed", "pi is not installed"), reason("claude", "unknown", "claude did not answer")],
  });
  assert.ok(unsupported.includes("pi is not installed"), "the first reason is not visible text");
  assert.ok(unsupported.includes("claude did not answer"), "the second reason is not visible text");
  assert.match(unsupported, /No harness can be read\./);
  assert.ok(!unsupported.includes("Unify to the current value"), "an unsupported state offers the unify button");
});

test("the component is presentational: every word is a prop, no i18n provider needed", () => {
  // Like SettingsUi.tsx: it renders outside the i18n provider, so every word
  // it shows came in as a prop (and no state of its own exists to fetch with).
  const html = render();
  assert.match(html, /Current value/);
  assert.match(html, /claude-opus-5-5/);
});
