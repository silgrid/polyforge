// carries: components/ChatInput.tsx, lib/i18n/messages/*
// while a claude turn runs and the composer offers pi's Steer, the
// composer says what a message sent now does — claude takes it into the turn or keeps it
// for the next one — instead of pi's steer / follow-up wording or "wait for the turn to
// end"; without Steer (another pane) the old reason stays.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { ChatInput } = await jiti.import("./ChatInput.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const claude = { choosing: false, harness: "claude", model: "claude-opus-5-5", effort: "default", availability: { available: true } };
const composer = (extra) => renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(ChatInput, {
  onSend() {}, onAbort() {}, claude, isStreaming: true, ...extra,
})));
const placeholder = (html) => /placeholder="([^"]*)"/.exec(html)?.[1];

test("a running claude turn with Steer: the placeholder says the message joins this turn or waits for the next", () => {
  const html = composer({ onSteer() {} });
  assert.equal(placeholder(html), "claude is answering; a message sent now joins this turn or waits for the next.");
  assert.match(html, /aria-label="Deliver after the current response and its tool calls finish \(Stop interrupts them\)"/, "the composer offers no Steer");
});

test("a running claude turn without Steer keeps saying the next message waits for the turn's end", () => {
  assert.equal(placeholder(composer({})), "claude is answering; the next message can be sent when this turn ends.");
});
