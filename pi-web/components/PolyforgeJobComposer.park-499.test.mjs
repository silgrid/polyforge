import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

// a parked work item's cause sits above the job card's input
// box — the blocked kind, step, reason and required action (the watch
// snapshot's `blocked`) — with the move a park leaves: a wakeable park (blocked
// or paused) a copyable `pf resume <wi>`, the contract park `pf cancel <wi>`
// alone. A step that runs or waits shows neither.
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const composer = await jiti.import("./PolyforgeJobComposer.tsx");

const WI = "wi_T499";
const DRIVER = { kind: "driver", step: "verification", reason: "R499", requiredAction: "A499" };
const CONTRACT = { kind: "contract", step: "build", reason: "R499-contract", requiredAction: "A499-contract" };

const render = (props) => renderToStaticMarkup(React.createElement(I18nProvider, null,
  React.createElement(composer.PolyforgeJobComposer, { wi: WI, question: null, onSent() {}, ...props })));

// The whole `<div …>` element opening at `start` (its nested divs balanced).
const element = (html, start) => {
  const tags = /<div[\s>]|<\/div>/g;
  tags.lastIndex = start;
  let depth = 0;
  for (let tag = tags.exec(html); tag; tag = tags.exec(html)) {
    depth += tag[0] === "</div>" ? -1 : 1;
    if (depth === 0) return html.slice(start, tags.lastIndex);
  }
  return html.slice(start);
};
const elementWith = (html, attribute) => {
  const at = html.indexOf(attribute);
  return at < 0 ? null : element(html, html.lastIndexOf("<div", at));
};
const text = (fragment) => fragment.replace(/<[^>]*>/g, "");
const commands = (html) => [...html.matchAll(/data-pf-chat-command="([^"]+)"/g)].map((match) => match[1]);

test("a wakeable blocked park shows its cause above the input box — kind, step, reason, required action — and a copyable pf resume <wi>, no pf cancel", () => {
  const html = render({ state: { input: "open", awaiting: false, park: "blocked" }, blocked: DRIVER });
  const cause = elementWith(html, 'data-pf-chat-park-cause="driver"');
  assert.ok(cause, "the park cause is rendered");
  assert.ok(text(cause).includes("Blocked · parked by the driver"), `the kind, labelled (${text(cause)})`);
  assert.equal(cause.match(/data-pf-chat-park-step[^>]*>([^<]*)</)?.[1]?.trim(), "verification", "the step");
  assert.equal(text(elementWith(cause, "data-pf-chat-park-reason") ?? ""), "R499", "the reason");
  assert.equal(text(elementWith(cause, "data-pf-chat-park-action") ?? ""), "Required action: A499", "the required action");
  assert.deepEqual(commands(html), ["resume"], "one resume command");
  assert.match(html, /<code[^>]*>pf resume wi_T499<\/code>/, "the command as text");
  assert.ok(html.includes('data-pf-chat-command-copy="resume"'), "its copy button");
  assert.ok(!/pf cancel/.test(html), "no pf cancel");
  const inputAt = html.indexOf("data-pf-chat-input");
  assert.ok(inputAt > 0, "the input box stays");
  assert.ok(html.indexOf("data-pf-chat-park-cause") < inputAt, "the cause above the input box");
  assert.ok(html.indexOf('data-pf-chat-command="resume"') < inputAt, "the command above the input box");
  const lab = render({ state: { input: "open", awaiting: false, park: "blocked" }, blocked: DRIVER, profile: "lab" });
  assert.match(lab, /<code[^>]*>pf --profile lab resume wi_T499<\/code>/, "a named Cell's command carries it");
});

test("a paused park shows a copyable pf resume <wi> above the input box", () => {
  const html = render({ state: { input: "open", awaiting: false, park: "paused" } });
  assert.ok(html.includes('data-pf-chat-parked="paused"'), "the parked hint");
  assert.deepEqual(commands(html), ["resume"], "one resume command");
  assert.match(html, /<code[^>]*>pf resume wi_T499<\/code>/);
  const inputAt = html.indexOf("data-pf-chat-input");
  assert.ok(inputAt > 0 && html.indexOf('data-pf-chat-command="resume"') < inputAt, "the command above the input box");
  assert.ok(!html.includes("data-pf-chat-park-cause"), "no cause without a blocked field");
  assert.ok(!/pf cancel/.test(html), "no pf cancel");
});

test("a contract park shows its cause and only pf cancel <wi>, no pf resume and no input box", () => {
  const html = render({ state: { input: "closed", reason: "contract", commands: ["cancel"] }, blocked: CONTRACT });
  const cause = elementWith(html, 'data-pf-chat-park-cause="contract"');
  assert.ok(cause, "the park cause is rendered");
  assert.ok(text(cause).includes("Blocked · its lane snapshot targets another workflow contract (cancel and re-file)"), `the contract kind, labelled (${text(cause)})`);
  assert.equal(cause.match(/data-pf-chat-park-step[^>]*>([^<]*)</)?.[1]?.trim(), "build", "the step");
  assert.equal(text(elementWith(cause, "data-pf-chat-park-reason") ?? ""), "R499-contract", "the reason");
  assert.equal(text(elementWith(cause, "data-pf-chat-park-action") ?? ""), "Required action: A499-contract", "the required action");
  assert.deepEqual(commands(html), ["cancel"], "pf cancel alone");
  assert.match(html, /<code[^>]*>pf cancel wi_T499<\/code>/);
  assert.ok(!/pf resume/.test(html), "no pf resume");
  assert.ok(!html.includes("data-pf-chat-input"), "no input box");
});

test("a running or waiting step shows no park cause and no command", () => {
  for (const state of [{ input: "open", awaiting: false }, { input: "open", awaiting: true }]) {
    for (const blocked of [undefined, DRIVER]) {
      const html = render({ state, blocked });
      assert.ok(html.includes("data-pf-chat-input"), "the input box");
      assert.ok(!html.includes("data-pf-chat-park-cause"), `no park cause (${JSON.stringify(state)}, blocked ${Boolean(blocked)})`);
      assert.ok(!html.includes("data-pf-chat-command"), `no command (${JSON.stringify(state)}, blocked ${Boolean(blocked)})`);
    }
  }
});
