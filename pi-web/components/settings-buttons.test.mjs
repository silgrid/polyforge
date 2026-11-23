// carries: components/HarnessApplyTo*, components/UnifiedSetting*, components/McpConfig*, components/AccountSettings*, components/SettingsUi*
//
// polyforge#257 (T2 止血) AC2: the settings surfaces' controls go through the
// shared ConfigButton. The base `.config-button` class alone carries no
// padding, height or colour (settings.css styles `.config-button-default` /
// `-secondary`), so a direct `className="config-button"` renders as a
// one-pixel-bordered word — the review's P1. The expected markup is
// SettingsUi.tsx's ConfigButton contract (default variant `secondary`, size
// `default`); the disabled semantics the ticket keeps are asserted beside it.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { HarnessApplyTo } = await jiti.import("./HarnessApplyTo.tsx");
const { UnifiedSetting } = await jiti.import("./UnifiedSetting.tsx");

const h = React.createElement;
const render = (element) => renderToStaticMarkup(element);

/** The rendered tag of the button whose visible text is exactly `label`. */
function buttonTag(html, label) {
  const matches = [...html.matchAll(/<button[^>]*>[^<]*<\/button>/g)].map((match) => match[0]);
  return matches.find((tag) => tag.replace(/<[^>]+>/g, "") === label) ?? null;
}

const SHARED = 'class="config-button config-button-secondary config-button-default"';

test("the apply-to submit is the shared button, disabled while no target is selected (AC2)", () => {
  const texts = { title: "Apply to", all: "All harnesses", specific: "Specific harnesses", submit: "Apply" };
  const targets = [{ harness: "pi", label: "pi" }, { harness: "claude", label: "claude" }];
  const empty = render(h(HarnessApplyTo, { texts, targets, value: { mode: "specific", selected: [] }, onChange: () => {}, onSubmit: () => {} }));
  const submit = buttonTag(empty, "Apply");
  assert.ok(submit, "the submit button is not rendered");
  assert.match(submit, new RegExp(SHARED), "the submit button carries the shared variant and size classes");
  assert.match(submit, /disabled=""/, "an empty specific selection still cannot submit");

  const named = render(h(HarnessApplyTo, { texts, targets, value: { mode: "specific", selected: ["pi"] }, onChange: () => {}, onSubmit: () => {} }));
  const enabled = buttonTag(named, "Apply");
  assert.match(enabled, new RegExp(SHARED), "a named selection's submit is the shared button too");
  assert.doesNotMatch(enabled, /disabled/, "a named target can submit");
});

test("the unify button is the shared button and keeps its in-flight disabled state (AC2)", () => {
  const texts = { title: "Approval mode", currentValue: "Current value", unify: "Unify to the current value", consistent: "Consistent", unsupported: "Unsupported", saving: "Saving" };
  const values = [
    { harness: "pi", status: "ready", value: "ask" },
    { harness: "claude", status: "ready", value: "acceptEdits" },
  ];
  const idle = render(h(UnifiedSetting, { texts, current: "ask", values, onUnify: () => {} }));
  const unify = buttonTag(idle, "Unify to the current value");
  assert.ok(unify, "the unify button is not rendered");
  assert.match(unify, new RegExp(SHARED), "the unify button carries the shared variant and size classes");

  const busy = render(h(UnifiedSetting, { texts, current: "ask", values, busy: true, onUnify: () => {} }));
  assert.match(buttonTag(busy, "Saving"), /disabled=""/, "the one submission's in-flight state still disables the button");
});
