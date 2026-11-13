import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { Script } from "node:vm";
import ts from "typescript";

// the Messages 「@」 action's fill target is bound at click
// time through resolveChatInputHandle's derivation (focused split pane, else
// the classic/mobile composer). When the answer arrives it fills the bound
// composer if that slot still shows the bound draft key, else the session's
// stored draft; a bound split pane that closed meanwhile gets nothing (the
// prompt then shows under the message). Never retargeted, never sent.
const source = await readFile(new URL("./AppShell.tsx", import.meta.url), "utf8");

function extractCallback(name) {
  const ast = ts.createSourceFile("AppShell.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  let node = null;
  (function find(n) {
    if (ts.isVariableDeclaration(n) && n.name.getText(ast) === name) { node = n; return; }
    ts.forEachChild(n, find);
  })(ast);
  assert.ok(node, `AppShell defines ${name}`);
  return node.initializer.arguments[0].getText(ast);
}

/** A fake composer handle: its draft key and the text appended to it. */
const composer = (draftKey) => ({ key: draftKey, appended: [], draftKey() { return this.key; }, appendText(text) { this.appended.push(text); } });

function bindWith(env) {
  const drafts = [];
  const sandbox = {
    splitPaneEnabled: env.split ?? false,
    isMobile: env.mobile ?? false,
    focusedPaneId: env.focused ?? null,
    paneTabsRef: env.paneTabsRef ?? { current: [] },
    paneChatInputRefsRef: { current: env.panes ?? new Map() },
    chatInputRef: env.classic ?? { current: null },
    selectedSession: env.session ?? null,
    translate: (key) => `t(${key})`,
    appendDraftText: (key, text) => { drafts.push([key, text]); },
    resolveChatInputHandle: () => {
      if ((env.split ?? false) && !(env.mobile ?? false) && env.focused) return env.panes?.get(env.focused)?.current ?? null;
      return env.classic?.current ?? null;
    },
  };
  const bind = new Script(ts.transpileModule(`(${extractCallback("bindPromptTarget")})`, {
    compilerOptions: { target: ts.ScriptTarget.ES2020 },
  }).outputText).runInNewContext(sandbox);
  return { bind, drafts };
}

test("no composer at click time binds nothing", () => {
  assert.equal(bindWith({ classic: { current: null } }).bind(), null);
  assert.equal(bindWith({ split: true, focused: "s_a", panes: new Map([["s_a", { current: null }]]) }).bind(), null);
});

test("classic and mobile bind the current page's composer and name its session", () => {
  for (const mobile of [false, true]) {
    const a = composer("s_a");
    const { bind, drafts } = bindWith({ mobile, split: mobile, focused: "s_b", classic: { current: a }, session: { id: "s_a", name: "Session A" } });
    const target = bind();
    assert.equal(target.name, "Session A");
    assert.equal(target.fill("P"), true);
    assert.deepEqual(a.appended, ["P"], "the mounted composer gets the prompt");
    assert.deepEqual(drafts, []);
  }
  const fresh = bindWith({ classic: { current: composer("new:1:/p") }, session: null }).bind();
  assert.equal(fresh.name, "t(tabs.new)", "a new-session page counts as a target");
});

test("split view binds the focused pane only; the other pane is never written", () => {
  const a = composer("s_a");
  const b = composer("s_b");
  const panes = new Map([["s_a", { current: a }], ["s_b", { current: b }]]);
  const paneTabsRef = { current: [{ sessionId: "s_a", label: "A" }, { sessionId: "s_b", label: "B" }] };
  const target = bindWith({ split: true, focused: "s_b", panes, paneTabsRef }).bind();
  assert.equal(target.name, "B");
  assert.equal(target.fill("P"), true);
  assert.deepEqual([a.appended, b.appended], [[], ["P"]]);
});

test("the binding is fixed at click time: a later focus or page change does not move it", () => {
  const a = composer("s_a");
  const classic = { current: a };
  const { bind, drafts } = bindWith({ classic, session: { id: "s_a", name: "A" } });
  const target = bind();
  // The page switched to another session: its composer now shows s_b.
  const b = composer("s_b");
  classic.current = b;
  assert.equal(target.fill("P"), true);
  assert.deepEqual(b.appended, [], "never retargeted to the composer now on screen");
  assert.deepEqual(a.appended, [], "the unmounted composer is not written");
  assert.deepEqual(drafts, [["s_a", "P"]], "the bound session's stored draft gets the prompt");
  // Back on the bound session before the answer: its composer gets it.
  const back = composer("s_a");
  classic.current = back;
  assert.equal(target.fill("Q"), true);
  assert.deepEqual(back.appended, ["Q"]);
});

test("a bound split pane that closed gets nothing and reports it", () => {
  const a = composer("s_a");
  const b = composer("s_b");
  const panes = new Map([["s_a", { current: a }], ["s_b", { current: b }]]);
  const paneTabsRef = { current: [{ sessionId: "s_a", label: "A" }, { sessionId: "s_b", label: "B" }] };
  const { bind, drafts } = bindWith({ split: true, focused: "s_a", panes, paneTabsRef });
  const target = bind();
  paneTabsRef.current = [{ sessionId: "s_b", label: "B" }];
  panes.delete("s_a");
  assert.equal(target.fill("P"), false);
  assert.deepEqual([a.appended, b.appended, drafts], [[], [], []], "nothing is written anywhere");
});

test("the dock stays open while a 「@」 run is in flight, and the surface carries the binding", () => {
  assert.match(source, /if \(pfPromptBusyRef\.current\) return;/, "an outside press does not dismiss the dock mid-run");
  assert.match(source, /const pfMessagesSurface = \{[^}]*bindPromptTarget,\s*onPrompt: requestPfPrompt,\s*onPromptBusyChange: setPfPromptBusy,\s*\};/,
    "the one surface (desktop dock and mobile sheet) carries the binding, the request and the busy report");
  const bind = extractCallback("bindPromptTarget");
  assert.doesNotMatch(bind, /submit|send|addImages|Image/i, "the fill appends text only and never sends");
});

test("the request is one POST to the prompt route; a refusal keeps pf's text and the upgrade marker", async () => {
  const calls = [];
  const answers = [];
  const sandbox = {
    fetch: async (url, init) => { calls.push([url, init.method, init.headers["content-type"], init.body]); return answers.shift(); },
    parsePolyforgePrompt: (raw) => (raw && typeof raw.prompt === "string" && raw.prompt.trim() ? raw : null),
    JSON, Error, Object,
  };
  const request = new Script(ts.transpileModule(`(${extractCallback("requestPfPrompt")})`, {
    compilerOptions: { target: ts.ScriptTarget.ES2020 },
  }).outputText).runInNewContext(sandbox);
  const reply = (ok, body) => ({ ok, json: async () => body });
  answers.push(reply(true, { ok: true, prompt: { prompt: "P", task_id: "task_0123456789abcdef", model: "m", duration: "1s" } }));
  assert.equal((await request("msg_1")).prompt, "P");
  assert.deepEqual(calls, [["/api/polyforge/messages/prompt", "POST", "application/json", JSON.stringify({ message_id: "msg_1" })]]);
  answers.push(reply(false, { error: "the message @ action needs pf 9.9.9 or newer", upgrade: true }));
  await assert.rejects(request("msg_1"), (error) => error.upgrade === true && /needs pf/.test(error.message));
  answers.push(reply(true, { error: "prompt task ended capped" }));
  await assert.rejects(request("msg_1"), (error) => error.upgrade === false && error.message === "prompt task ended capped");
  answers.push(reply(true, { ok: true, prompt: { prompt: "  " } }));
  await assert.rejects(request("msg_1"), (error) => error.message === "the prompt is unavailable", "an unusable answer is never filled");
});
