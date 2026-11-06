import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { Script } from "node:vm";
import { createJiti } from "jiti";
import ts from "typescript";

// the Messages 「@」 action — a visible 「@」 beside Analyze on
// every row (own messages too, never in the analysis card), the fill target
// bound at click time, the per-message outcome (filled feedback, the prompt
// under the message, a typed refusal) and per-message single-flight.
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { PolyforgeMessages } = await jiti.import("./PolyforgeMessages.tsx");
const source = await readFile(new URL("./PolyforgeMessages.tsx", import.meta.url), "utf8");
const card = await readFile(new URL("./PolyforgeAnalysisCard.tsx", import.meta.url), "utf8");
const locales = Object.fromEntries(await Promise.all(["en", "zh-CN", "zh-TW"].map(async (locale) => [
  locale, await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8"),
])));

const analysis = {
  message_id: "msg_in", task_id: "task_0123456789abcdef", project: "alpha", type: "requirement",
  summary: "File the ticket.", suggested_actions: ["file_ticket"], model: "pi:m", duration: "1s",
};
const render = () => renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(PolyforgeMessages, {
  pfPresent: true, capable: true, reason: null,
  conversations: [{ id: "conv_1", kind: "direct", peer: "u_peer", peer_name: "Peer", last_seq: 2, unread: 0 }],
  contacts: [], groups: [], membershipAdmin: null, users: [], onChangeMembership: async () => {},
  error: null, selectedConversation: "conv_1", onSelectConversation: () => {}, selectedContact: null, onSelectContact: () => {},
  messages: [
    { id: "msg_in", seq: 1, text: "RECEIVED-281", created_at: "2026-11-03T00:00:00Z", mine: false },
    { id: "msg_own", seq: 2, text: "OWN-281", created_at: "2026-11-03T00:00:00Z", mine: true },
  ],
  initialPageLoaded: true, blocked: false, loadingOlder: false, onLoadOlder: () => {}, onAcknowledge: () => {},
  onSend: async () => {}, onAnalyze: async () => analysis, analyses: { msg_in: analysis }, onOpenAnchor: () => {},
  onOpenTask: () => {}, unreadTotal: 0, bindPromptTarget: () => null, onPrompt: async () => ({}), onPromptBusyChange: () => {},
})));

test("every row — received and own — carries one visible 「@」 button beside Analyze; the analysis card has none", () => {
  const html = render();
  const atButtons = html.match(/<button[^>]*>(?:(?!<\/button>).)*?@\s*<\/button>/gs) ?? [];
  assert.equal(atButtons.length, 2, "one 「@」 per row, own message included");
  for (const button of atButtons) {
    assert.match(button, /aria-label="[^"]+"/, "the 「@」 carries an accessible name");
    assert.doesNotMatch(button, /disabled/, "idle actions are usable");
  }
  const received = html.slice(html.indexOf("RECEIVED-281"), html.indexOf("OWN-281"));
  const own = html.slice(html.indexOf("OWN-281"));
  assert.match(received, /Analyze<\/button>/, "the received row keeps Analyze");
  assert.match(received, /@\s*<\/button>/, "the received row carries the 「@」");
  assert.doesNotMatch(own, /Analyze<\/button>/, "the own row still has no Analyze");
  assert.match(own, /@\s*<\/button>/, "the own row carries the 「@」");
  assert.doesNotMatch(card, /promptMessage|"@"|>@</, "the analysis card repeats no 「@」 action");
  assert.doesNotMatch(source, /!message\.mine && \(\s*<button type="button" onClick=\{\(event\) => \{ event\.stopPropagation\(\); void promptMessage/,
    "the 「@」 is not gated on received messages");
});

test("the action's keys exist in every locale, the two feedback lines are the owner's fixed lines", () => {
  for (const key of ["prompt", "prompting", "promptFilled", "promptNoTarget", "promptShown", "promptUpgrade"]) {
    for (const [locale, text] of Object.entries(locales)) {
      assert.match(text, new RegExp(`"polyforge\\.messages\\.${key}": "[^"]+"`), `${locale} carries polyforge.messages.${key}`);
    }
  }
  for (const text of Object.values(locales)) assert.match(text, /"polyforge\.messages\.promptFilled": "已填入 \{name\}"/);
  for (const text of Object.values(locales)) assert.match(text, /"polyforge\.messages\.promptNoTarget": "无目标会话，已显示在消息下"/);
  for (const text of Object.values(locales)) {
    assert.doesNotMatch(text.match(/"polyforge\.messages\.prompt": "([^"]+)"/)[1], /分析|analy|polyforge/i,
      "the 「@」 name never reads as Analyze or the dock toggle");
  }
});

// Execute the real promptMessage useCallback body with scripted collaborators.
function promptMessageWith({ target, answer }) {
  const ast = ts.createSourceFile("PolyforgeMessages.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  let node = null;
  (function find(n) {
    if (ts.isVariableDeclaration(n) && n.name.getText(ast) === "promptMessage") { node = n; return; }
    ts.forEachChild(n, find);
  })(ast);
  assert.ok(node, "PolyforgeMessages defines promptMessage");
  const state = { prompting: {}, outcomes: {}, binds: 0, requests: [] };
  const setter = (key) => (update) => { state[key] = typeof update === "function" ? update(state[key]) : update; };
  const sandbox = {
    promptingRef: { current: new Set() },
    bindPromptTarget: () => { state.binds += 1; return target; },
    setPrompting: setter("prompting"),
    setPromptOutcomes: setter("outcomes"),
    withoutKey: (record, key) => { const next = { ...record }; delete next[key]; return next; },
    onPrompt: (id) => { state.requests.push(id); return answer(id); },
    t: (key, params) => (params ? `${key}:${JSON.stringify(params)}` : key),
    Boolean, String, Error, Object, JSON,
  };
  const fn = new Script(ts.transpileModule(`(${node.initializer.arguments[0].getText(ast)})`, {
    compilerOptions: { target: ts.ScriptTarget.ES2020 },
  }).outputText).runInNewContext(sandbox);
  // The state lives in the sandbox realm: compare plain copies.
  return { run: fn, state, plain: (key) => JSON.parse(JSON.stringify(state[key])) };
}

test("a filled prompt reports 已填入 <name> under its message; the target was bound before the request", async () => {
  const filled = [];
  let boundBeforeRequest = false;
  const target = { name: "Session A", fill: (prompt) => { filled.push(prompt); return true; } };
  const harness = promptMessageWith({ target, answer: async () => { boundBeforeRequest = harness.state.binds === 1; return { prompt: "P" }; } });
  await harness.run("msg_1");
  assert.ok(boundBeforeRequest, "the target is bound at click time, before the request");
  assert.deepEqual(filled, ["P"]);
  assert.deepEqual(harness.plain("outcomes").msg_1, { feedback: 'polyforge.messages.promptFilled:{"name":"Session A"}' });
  assert.deepEqual(harness.plain("prompting"), {}, "the action is usable again");
});

test("no composer at click time: the prompt under the message with the no-target line; a closed target: under the message only", async () => {
  const none = promptMessageWith({ target: null, answer: async () => ({ prompt: "P" }) });
  await none.run("msg_1");
  assert.deepEqual(none.plain("outcomes").msg_1, { shown: "P", feedback: "polyforge.messages.promptNoTarget" });
  const closed = promptMessageWith({ target: { name: "A", fill: () => false }, answer: async () => ({ prompt: "P" }) });
  await closed.run("msg_1");
  assert.deepEqual(closed.plain("outcomes").msg_1, { shown: "P" }, "never retargeted, no no-target line");
});

test("a refusal is stated under the message, nothing is filled, the action is usable and a retry starts one new run", async () => {
  const filled = [];
  let fail = true;
  const harness = promptMessageWith({
    target: { name: "A", fill: (prompt) => { filled.push(prompt); return true; } },
    answer: async () => { if (fail) throw Object.assign(new Error("prompt task ended capped"), { upgrade: false }); return { prompt: "P" }; },
  });
  await harness.run("msg_1");
  assert.deepEqual(harness.plain("outcomes").msg_1, { error: { text: "prompt task ended capped", upgrade: false } });
  assert.deepEqual(filled, [], "a failure fills nothing");
  assert.deepEqual(harness.plain("prompting"), {});
  fail = false;
  await harness.run("msg_1");
  assert.deepEqual(filled, ["P"], "the retry fills");
  assert.equal(harness.state.requests.length, 2);
  const below = promptMessageWith({ target: null, answer: async () => { throw Object.assign(new Error("needs pf"), { upgrade: true }); } });
  await below.run("msg_1");
  assert.equal(below.state.outcomes.msg_1.error.upgrade, true, "the below-floor refusal keeps its upgrade marker");
  assert.match(source, /t\("polyforge\.messages\.promptUpgrade", \{ version: POLYFORGE_PROMPT_MINIMUM_PF_VERSION \}\)/);
});

test("one run per message; a different message runs at the same time and each fills its own bound target", async () => {
  const fills = [];
  const releases = {};
  const harness = promptMessageWith({
    target: { name: "A", fill: (prompt) => { fills.push(prompt); return true; } },
    answer: (id) => new Promise((resolve) => { releases[id] = () => resolve({ prompt: `P-${id}` }); }),
  });
  const first = harness.run("msg_1");
  const again = harness.run("msg_1");
  const other = harness.run("msg_2");
  assert.deepEqual(harness.plain("requests"), ["msg_1", "msg_2"], "a second click on a running message starts nothing");
  assert.deepEqual(Object.keys(harness.state.prompting).sort(), ["msg_1", "msg_2"], "each message shows its own in-flight state");
  releases.msg_2(); releases.msg_1();
  await Promise.all([first, again, other]);
  assert.deepEqual(fills.sort(), ["P-msg_1", "P-msg_2"]);
  assert.deepEqual(harness.plain("prompting"), {});
});

test("the 「@」 renders its in-flight state, keeps its glyph, and the outcome is component memory only", () => {
  assert.match(source, /disabled=\{Boolean\(prompting\[message\.id\]\)\}/);
  assert.match(source, /aria-busy=\{Boolean\(prompting\[message\.id\]\)\}/);
  assert.match(source, /className="animate-spin"/);
  assert.match(source, /<\/svg>\n\s*\)\}\n\s*@\n\s*<\/button>/, "the glyph stays visible while in flight");
  assert.match(source, /userSelect: "text"/, "the prompt under the message is selectable (copyable)");
  assert.doesNotMatch(source, /localStorage|sessionStorage/, "no stored copy of a prompt");
  assert.doesNotMatch(source, /fetch\(/, "the panel requests through the surface's onPrompt only");
  assert.match(source, /onPromptBusyChange\(promptBusy\)/, "the in-flight state is reported so the dock stays open");
});
