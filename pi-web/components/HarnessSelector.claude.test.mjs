import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { HarnessSelector, HarnessOptions } = await jiti.import("./HarnessSelector.tsx");
const { ModelSelector } = await jiti.import("./ModelSelector.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const REASON = "claude was not found on PATH, so a claude session cannot be started.";
const options = (props) => renderToStaticMarkup(React.createElement(HarnessOptions, { value: "pi", claudeAvailable: true, unavailableReason: REASON, onSelect() {}, ...props }));
const optionTexts = (html) => [...html.matchAll(/role="option"[^>]*>([\s\S]*?)<\/button>/g)].map((match) => match[1].replace(/<[^>]+>/g, "").trim());

test("the menu offers exactly pi and claude as selector rows, the current one selected", () => {
  const html = options({ value: "claude" });
  assert.deepEqual(optionTexts(html), ["pi", "claude"]);
  assert.match(html, /role="option" aria-selected="false"[\s\S]*?>pi</);
  assert.match(html, /role="option" aria-selected="true"[\s\S]*?>claude</);
  assert.doesNotMatch(html, /disabled/);
});

test("claude not on PATH: its row is disabled and the reason is visible text, not a tooltip", () => {
  const html = options({ claudeAvailable: false });
  assert.deepEqual(optionTexts(html), ["pi", "claude"]);
  assert.match(html, /role="option" aria-selected="false" aria-disabled="true" disabled=""[\s\S]*?>claude</);
  assert.ok(html.includes(`>${REASON}<`));
  assert.doesNotMatch(html, /title="claude was not found/);
});

test("the trigger is the model selector's toolbar button family: a listbox popover showing the current harness", () => {
  const harness = renderToStaticMarkup(React.createElement(HarnessSelector, { value: "claude", onChange() {}, claudeAvailable: true, unavailableReason: REASON, ariaLabel: "Agent" }));
  const model = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(ModelSelector, { options: [{ provider: "claude", modelId: "claude-opus-5-5", name: "claude-opus-5-5" }], value: { provider: "claude", modelId: "claude-opus-5-5" }, onChange() {} })));
  assert.match(harness, /class="harness-selector"/);
  assert.match(harness, /<button type="button" aria-label="Agent" aria-haspopup="listbox" aria-expanded="false"/);
  assert.match(harness, /<span[^>]*>claude<\/span><\/button>/);
  assert.doesNotMatch(harness, /role="listbox"/, "closed until clicked");
  assert.doesNotMatch(harness, /class="model-selector/, "a control of its own, not the model selector");
  for (const shared of ["height:32px", "border-radius:9px", "font-size:12px", "color:var(--text-muted)"]) {
    assert.ok(harness.includes(shared) && model.includes(shared), shared);
  }
});
