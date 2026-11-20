// carries: components/ChatInput.tsx, components/ChatWindow.tsx, lib/default-model-client.ts
import "#test-env";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { Script } from "node:vm";
import test from "node:test";
import { createJiti } from "jiti";
import ts from "typescript";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { ChatInput, ClaudeComposerControls, CLAUDE_EFFORT_ENTRIES, builtinSlashCommands, canRunBuiltinSlashCommandWhileStreaming, claudeModelOptions, filterComposerImageFiles, filterSlashCommands, offersBuiltinSlashCommandWhileStreaming } = await jiti.import("./ChatInput.tsx");
const { clearDraft, setDraft } = await jiti.import("@/lib/draft-store");
const { isBareMcpCommand } = await jiti.import("@/lib/mcp-command");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { enLocale } = await jiti.import("@/lib/i18n/messages/en");
const { zhCNLocale } = await jiti.import("@/lib/i18n/messages/zh-CN");
const { zhTWLocale } = await jiti.import("@/lib/i18n/messages/zh-TW");

const translator = (locale) => (key, params = {}) => String(locale.messages[key] ?? key).replace(/\{(\w+)\}/g, (_, name) => String(params[name] ?? ""));
const t = translator(enLocale);
const render = (state, tr = t) => renderToStaticMarkup(React.createElement(ClaudeComposerControls, { state, t: tr }));
const base = { choosing: true, harness: "pi", model: "default", effort: "default", availability: { available: true, models: ["default", "opus", "fable", "sonnet", "haiku"] } };
const HINTS = {
  en: "Uses the system claude; if something goes wrong, adjust claude from the pi-web terminal.",
  "zh-CN": "使用系统 claude；如有问题，请在 pi-web 终端手动调整 claude",
  "zh-TW": "使用系統 claude；如有問題，請在 pi-web 終端手動調整 claude",
};
const RETIRED = ["claude-default", "claude default", "read-only", "writable", "CLAUDE.md", "claude config dir", "settings.json env", "<select"];

function composer(claude, extra = {}) {
  return renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(ChatInput, {
    onSend() {}, onAbort() {}, claude, ...extra,
  })));
}
/** The bottom bar's markup and what follows it (the lines under the selectors). */
function split(html) {
  const start = html.indexOf('class="chat-input-controls"');
  assert.ok(start >= 0, "no bottom bar");
  return { before: html.slice(0, start), after: html.slice(start) };
}

/**
 * The same extractor ChatInput.test.mjs's `chatInputCallback` is: the named
 * callback's source, transpiled and run in a bare scope — so a claude
 * compaction's send blocking is asserted as behaviour, not as source text
 * (polyforge#71 r1#2).
 */
function claudeInputCallback(name, context) {
  const sourceText = readFileSync(new URL("./ChatInput.tsx", import.meta.url), "utf8");
  const source = ts.createSourceFile("ChatInput.tsx", sourceText, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  let found;
  const visit = (node) => {
    if (found) return;
    if (ts.isVariableDeclaration(node) && node.name.getText(source) === name) {
      found = node.initializer.arguments[0];
      return;
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
  assert.ok(found, `callback ${name} not found`);
  return new Script(ts.transpileModule(found.getText(source), {
    compilerOptions: { target: ts.ScriptTarget.ES2020 },
  }).outputText).runInNewContext(context);
}

test("a fresh composer: the harness selector and the model selector side by side in the bottom bar, no <select>", () => {
  for (const harness of ["pi", "claude"]) {
    const html = composer({ ...base, harness }, harness === "pi" ? { onModelChange() {}, modelList: [{ provider: "p", id: "m0", name: "Model 0" }], model: { provider: "p", modelId: "m0" } } : {});
    const { before, after } = split(html);
    assert.doesNotMatch(html, /<select/, "no select control");
    assert.doesNotMatch(before, /aria-haspopup="listbox"/, "no selector above the bar");
    const harnessAt = after.indexOf('class="harness-selector"');
    const modelAt = after.indexOf('class="model-selector');
    assert.ok(harnessAt > 0 && modelAt > harnessAt, `${harness}: harness selector, then the model selector, both in the bar`);
    assert.match(after, new RegExp(`aria-label="Agent" aria-haspopup="listbox"[^>]*>[\\s\\S]*?<span[^>]*>${harness}</span>`));
  }
});

test("claude chosen: the model selector shows the name passed to --model (claude's default alias by default,) and the system-claude hint is under the selectors", () => {
  const html = composer({ ...base, harness: "claude" });
  const { after } = split(html);
  assert.match(after, /class="model-selector[\s\S]*?>default<\/span>/);
  assert.ok(after.indexOf(HINTS.en) > after.indexOf('class="model-selector'), "the hint sits under the selectors");
  for (const retired of RETIRED) assert.ok(!html.includes(retired), retired);
  assert.match(composer({ ...base, harness: "claude", model: "claude-fable-5-1" }), />claude-fable-5-1<\/span>/);
});

test("the claude model selector lists GET /api/claude's names, each as passed to --model, plus the session's own model when the list lacks it", () => {
  const listed = { available: true, models: ["default", "opus", "fable", "sonnet", "haiku", "claude-custom-1"] };
  assert.deepEqual(claudeModelOptions(listed, "opus"), ["default", "opus", "fable", "sonnet", "haiku", "claude-custom-1"]);
  assert.deepEqual(claudeModelOptions(listed, "claude-opus-5-5"), ["default", "opus", "fable", "sonnet", "haiku", "claude-custom-1", "claude-opus-5-5"], "an older session's full name stays choosable");
  assert.deepEqual(claudeModelOptions({ available: true, models: [] }, "default"), ["default"]);
  assert.deepEqual(claudeModelOptions(null, "haiku"), ["haiku"], "before the answer: only the current model");
});

test("the hint appears in each language's exact wording only while claude is chosen", () => {
  for (const [locale, messages] of [["en", enLocale], ["zh-CN", zhCNLocale], ["zh-TW", zhTWLocale]]) {
    assert.equal(messages.messages["claude.systemHint"], HINTS[locale]);
    assert.ok(render({ ...base, harness: "claude" }, translator(messages)).includes(HINTS[locale]), locale);
    assert.equal(render(base, translator(messages)), "", `${locale}: nothing for pi`);
  }
});

test("claude missing from PATH: the reason is visible text under the selectors, never a tooltip only", () => {
  const html = render({ ...base, availability: { available: false } });
  assert.match(html, />claude was not found on PATH, so a claude session cannot be started\.</);
  assert.doesNotMatch(html, /title="claude was not found/);
  assert.equal(render({ ...base, availability: null }), "", "unknown availability shows nothing");
});

// rewrote #250's "fixed for this session" pin: an open claude session switches its
// model and effort in pi's slots, compacts with claude's own /compact, and still has no harness switch.
test("an open claude session shows the model selector, the effort control and the compact control in the bar; no fixed line, no harness selector", () => {
  const open = { ...base, choosing: false, harness: "claude", model: "claude-haiku-5-5", onModelChange() {}, onEffortChange() {} };
  assert.equal(render({ ...base, choosing: false }), "", "a pi session shows nothing under the selectors");
  assert.equal(render(open), "", "no fixed line under the selectors");
  const html = composer(open, { onCompact() {} });
  const { after } = split(html);
  assert.match(after, /class="model-selector[\s\S]*?>claude-haiku-5-5<\/span>/);
  assert.match(after, /aria-label="Change reasoning level"/);
  assert.match(after, /aria-label="Compact context"/);
  assert.doesNotMatch(html, /class="harness-selector"/);
  assert.doesNotMatch(html, /fixed for this session|claude-fixed-line/);
});

// polyforge#71 (AC5) rewrites the earlier "rendered, not hidden" rule: a claude
// session's compact control follows pi's — hidden while a turn runs (it returns as
// the turn ends), Stop compaction while claude's own compaction runs, disabled
// only while the stream has not said whether the session is idle — and it takes
// pi's run-end stray-click guard. Model and effort stay rendered, disabled.
// polyforge#71 (AC6): a claude session's model switch shows pi's own spinner
// (ModelSelector busy), and nothing else — the effort control never spins (Q3=A).
// polyforge#71 (AC7), rewritten as behaviour per review r1#2: a claude compaction
// takes no message — Enter (handleSend), a menu submit and the Steer button's
// queued send (sendQueued) all stop at the send functions' entry, the draft kept.
// pi's compaction still queues (the guard is claude's alone).
test("claude's compaction blocks every send path at its entry and keeps the draft", async () => {
  const blocked = (value) => {
    const calls = { sent: [], steered: [], queuedPrompts: [], builtins: [], cleared: 0, unlocked: 0 };
    const scope = {
      value, attachedImages: [],
      claudeComposer: true, isCompacting: true, isStreaming: false,
      onAudioUnlock() { calls.unlocked += 1; },
      offersBuiltinSlashCommandWhileStreaming, canRunBuiltinSlashCommandWhileStreaming, isBareMcpCommand,
      runBuiltinCommand: async (message) => { calls.builtins.push(message); return false; },
      clearInput() { calls.cleared += 1; },
      onSend: (message) => calls.sent.push(message),
      onSteer: (message) => calls.steered.push(message),
      onFollowUp() { throw new Error("claude has no follow-up queue"); },
      onPromptWithStreamingBehavior: (message, behavior) => calls.queuedPrompts.push([message, behavior]),
      onBuiltinCommand: async () => ({ handled: false }),
    };
    return { calls, handleSend: claudeInputCallback("handleSend", scope), sendQueued: claudeInputCallback("sendQueued", scope) };
  };
  for (const text of ["kept draft", "/compact focus", "/deep-research x"]) {
    const { calls, handleSend, sendQueued } = blocked(text);
    await handleSend();
    sendQueued("steer");
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(calls, { sent: [], steered: [], queuedPrompts: [], builtins: [], cleared: 0, unlocked: 0 },
      `${text}: the compaction blocked every send path and kept the draft`);
  }
  // pi's compaction queues the message as before: the guard is claude's alone.
  const piCalls = { steered: [], cleared: 0 };
  const piScope = {
    value: "queued message", attachedImages: [], claudeComposer: false, isCompacting: true,
    onAudioUnlock() {}, clearInput() { piCalls.cleared += 1; },
    canRunBuiltinSlashCommandWhileStreaming, isBareMcpCommand,
    onBuiltinCommand: async () => ({ handled: false }),
    onPromptWithStreamingBehavior() { throw new Error("a plain message never takes the / entry"); },
    onSteer: (message) => piCalls.steered.push(message),
    onFollowUp() {},
    runBuiltinCommand: async () => false,
  };
  claudeInputCallback("sendQueued", piScope)("steer");
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(piCalls.steered, ["queued message"], "pi's compaction queues the message");
  assert.equal(piCalls.cleared, 1, "pi's queue clears the composer as before");

  // The composer keeps its text while compacting.
  const draftKey = "claude:/tmp/claude-compaction-guard";
  setDraft(draftKey, { value: "kept", images: [] });
  try {
    const html = composer({ ...base, choosing: false, harness: "claude" }, { isCompacting: true, draftKey });
    assert.doesNotMatch(html, /<textarea[^>]*\bdisabled\b/, "the composer stays editable");
    assert.ok(html.includes(">kept</textarea>"), "the draft stays in the composer");
  } finally {
    clearDraft(draftKey);
  }
});

test("a claude session's model switch shows pi's spinner on the model selector only", () => {
  const open = { ...base, choosing: false, harness: "claude", model: "claude-sonnet-5-5", onModelChange() {}, onEffortChange() {} };
  const switching = composer({ ...open, modelSwitching: true }, {});
  assert.match(switching, /aria-busy="true"/, "the selector is busy while claude takes the switch");
  assert.match(switching, /style="[^"]*animation:spin 0\.8s linear infinite/, "pi's spinner");
  assert.match(switching, /title="Switching model"/, "pi's busy tooltip");
  const idle = composer(open, {});
  assert.doesNotMatch(idle, /aria-busy="true"/, "no spinner while idle");
  assert.doesNotMatch(idle, /animation:spin/, "no stray spinner");
  for (const [name, html] of [["switching", switching], ["idle", idle]]) {
    const effort = html.match(/<button[^>]*aria-label="Change reasoning level"[^>]*>/)?.[0] ?? "";
    assert.doesNotMatch(effort, /aria-busy/, `the effort control never spins (${name})`);
  }
});

test("a running claude turn hides the compact control (pi's rule); a compaction shows Stop compaction; an unknown state keeps it disabled", () => {
  const open = { ...base, choosing: false, harness: "claude", model: "claude-sonnet-5-5", onModelChange() {}, onEffortChange() {} };
  const running = composer(open, { onCompact() {}, isStreaming: true });
  assert.match(running, /class="model-selector[^"]*is-disabled/, "the model selector stays rendered, disabled");
  assert.match(running, /<button[^>]*disabled=""[^>]*aria-label="Change reasoning level"/, "the effort control stays rendered, disabled");
  assert.doesNotMatch(running, /aria-label="Compact context"/, "the compact control is hidden while a turn runs, as on pi");
  assert.doesNotMatch(running, /aria-label="Stop compaction"/);
  assert.match(running, /aria-label="Stop agent"/, "Stop stays");
  // pi's run-end stray-click guard now covers claude too (the earlier code excluded it).
  const source = readFileSync(new URL("./ChatInput.tsx", import.meta.url), "utf8");
  assert.match(source, /const isStrayClick = \(e: React\.MouseEvent\) => isRunEndStrayClick\(e\.detail, performance\.now\(\) - runEndedAtRef\.current\);/, "claude takes pi's stray-click guard");
  assert.doesNotMatch(source, /!claudeSession && isRunEndStrayClick/, "the claude exclusion is gone");
  // Not before the stream said the session is idle: rendered, disabled.
  const unknown = composer({ ...open, stateKnown: false }, { onCompact() {} });
  assert.match(unknown, /<button[^>]*disabled=""[^>]*aria-label="Compact context"/, "an unknown state keeps the control disabled");
  // claude's compaction is a turn: the control stays, as Stop compaction.
  const compacting = composer(open, { onCompact() {}, onAbortCompaction() {}, isStreaming: true, isCompacting: true });
  assert.match(compacting, /aria-label="Stop compaction"/);
  assert.doesNotMatch(compacting, /aria-label="Compact context"/);
  assert.match(compacting, /placeholder="claude is compacting; send when it ends\."/, "the #70 compacting placeholder");
  const compactingIdleComposer = composer(open, { onCompact() {}, onAbortCompaction() {}, isCompacting: true });
  assert.match(compactingIdleComposer, /aria-label="Stop compaction"/, "the control is Stop compaction however the compaction is shown");
  assert.match(compactingIdleComposer, /<button[^>]*disabled=""[^>]*aria-label="Send"/, "nothing is sent during claude's compaction");
  const idle = composer(open, { onCompact() {} });
  assert.match(idle, /aria-label="Compact context"/, "an idle claude session offers compaction");
  assert.doesNotMatch(idle, /<button[^>]*disabled=""[^>]*aria-label="Compact context"/, "idle is enabled");
  // A fresh composer of either harness offers no compact control (AC5: ChatWindow
  // passes onCompact only for an open session; the pin on that line is in ChatWindow.claude.test.mjs).
  assert.doesNotMatch(composer({ ...base, harness: "claude" }, {}), /aria-label="Compact context"/, "a fresh claude composer has none");
  assert.doesNotMatch(composer(base, {}), /aria-label="Compact context"/, "a fresh pi composer has none either");
});

test("a fresh claude composer offers model and effort, no compact, and keeps the system-claude hint", () => {
  const html = composer({ ...base, harness: "claude", onEffortChange() {} });
  assert.match(html, /class="model-selector/);
  assert.match(html, /aria-label="Change reasoning level"[\s\S]*?>default<\/span>/);
  assert.doesNotMatch(html, /aria-label="Compact context"/);
  assert.ok(html.includes(HINTS.en));
});

test("the effort menu's entries are claude's default entry and its five levels, each described in every locale, never off/minimal/auto", () => {
  assert.deepEqual([...CLAUDE_EFFORT_ENTRIES], ["default", "low", "medium", "high", "xhigh", "max"]);
  for (const locale of [enLocale, zhCNLocale, zhTWLocale]) {
    for (const key of ["claude.effortDefault", "claude.effortDefaultDesc", "chat.thinkingLow", "chat.thinkingMedium", "chat.thinkingHigh", "chat.thinkingXhigh", "chat.thinkingMax"]) {
      assert.ok(locale.messages[key], `${locale.id} ${key}`);
    }
  }
  assert.equal(zhCNLocale.messages["claude.effortDefault"], "默认");
});

test("a refused switch is one visible line under the selectors, prefixed in each locale", () => {
  for (const [locale, prefix] of [[enLocale, "Could not switch model: "], [zhCNLocale, "无法切换模型："], [zhTWLocale, "無法切換模型："]]) {
    const line = translator(locale)("claude.switchModelError", { error: "API error · model not changed" });
    assert.equal(line, `${prefix}API error · model not changed`);
    const html = render({ ...base, choosing: false, harness: "claude", switchError: line }, translator(locale));
    assert.match(html, /class="claude-switch-error" role="alert"/);
    assert.ok(html.includes(line), locale.id);
  }
});

test("claude's compact result line names only claude's pre-compaction count, in each locale", () => {
  for (const [locale, text] of [[enLocale, "Compacted from 17k tokens"], [zhCNLocale, "已压缩：压缩前 17k tokens"], [zhTWLocale, "已壓縮：壓縮前 17k tokens"]]) {
    assert.equal(translator(locale)("claude.compactResult", { tokens: "17k" }), text);
  }
  const html = composer({ ...base, choosing: false, harness: "claude", onModelChange() {}, onEffortChange() {} }, { onCompact() {}, compactResult: { reason: "manual", tokensBefore: 16739 } });
  assert.match(html, />Compacted from 17k tokens</);
  assert.doesNotMatch(html, /saved|->/);
  const pi = composer(base, { onCompact() {}, compactResult: { reason: "manual", tokensBefore: 16739, estimatedTokensAfter: 2978 } });
  assert.match(pi, /17k -&gt; 3k tokens/, "pi's line is unchanged");
});

// reverses #219's text-only claude composer: images attach on both harnesses.
test("a fresh claude composer and an open claude session both render the attach button", () => {
  assert.match(composer({ ...base, harness: "claude" }), /title="Attach files"/, "fresh claude composer");
  assert.match(composer({ ...base, choosing: false, harness: "claude", model: "claude-sonnet-5-5" }), /title="Attach files"/, "open claude session");
  assert.match(composer({ ...base, choosing: false, harness: "claude", model: "claude-sonnet-5-5" }, { isStreaming: true }), /title="Attach files"/, "while a claude turn runs");
  assert.match(composer(base), /title="Attach files"/, "pi keeps it");
});

test("a claude composer takes PNG/JPEG/GIF/WebP only and flags any other image for the visible refusal; pi's gate is unchanged", () => {
  const file = (type, size = 10) => ({ type, size });
  const picks = [file("image/png"), file("image/jpeg"), file("image/gif"), file("image/webp"), file("image/bmp"), file("image/svg+xml"), file("text/plain"), file("image/png", 10 * 1024 * 1024 + 1)];
  const claude = filterComposerImageFiles(picks, true);
  assert.deepEqual(claude.accepted.map((f) => f.type), ["image/png", "image/jpeg", "image/gif", "image/webp"]);
  assert.equal(claude.formatRefused, true);
  assert.equal(filterComposerImageFiles([file("image/png"), file("image/png", 10 * 1024 * 1024 + 1), file("text/plain")], true).formatRefused, false, "oversized and non-images stay silent");
  const pi = filterComposerImageFiles(picks, false);
  assert.deepEqual(pi.accepted.map((f) => f.type), ["image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/svg+xml"]);
  assert.equal(pi.formatRefused, false);
  for (const [locale, text] of [[enLocale, "Only PNG, JPEG, GIF and WebP images can be attached to a claude session."], [zhCNLocale, null], [zhTWLocale, null]]) {
    const value = locale.messages["chat.claudeImageFormats"];
    assert.ok(typeof value === "string" && /PNG/.test(value) && /JPEG/.test(value) && /GIF/.test(value) && /WebP/.test(value), locale.id);
    if (text) assert.equal(value, text);
  }
});

test("no unsupported-image warning for any claude model, even when pi's model is known text-only; pi still warns", () => {
  const draftKey = "claude:/tmp/claude-image-warning";
  const modelList = [{ id: "text-only", name: "Text Only", provider: "custom", input: ["text"] }];
  const piModel = { model: { provider: "custom", modelId: "text-only" }, modelList, isAutoModelSelection: true, draftKey };
  setDraft(draftKey, { value: "", images: [{ data: "aW1hZ2U=", mimeType: "image/png" }] });
  try {
    for (const model of ["default", "opus", "fable", "sonnet", "haiku", "claude-fable-5-1"]) {
      for (const state of [{ ...base, harness: "claude", model }, { ...base, choosing: false, harness: "claude", model }]) {
        const html = composer(state, piModel);
        assert.match(html, /<img/, `${model}: the image is attached`);
        assert.ok(!html.includes("Images may not be sent"), `${model}: no warning`);
      }
    }
    assert.ok(composer(base, piModel).includes("Images may not be sent"), "pi's rule is unchanged");
  } finally {
    clearDraft(draftKey);
  }
});

test("every claude string exists in en, zh-CN and zh-TW; the retired alias, preset, label and config-dir keys are gone", () => {
  const keys = Object.keys(enLocale.messages).filter((key) => key.startsWith("claude."));
  for (const locale of [zhCNLocale, zhTWLocale]) {
    assert.deepEqual(Object.keys(locale.messages).filter((key) => key.startsWith("claude.")).sort(), [...keys].sort(), locale.id);
  }
  for (const locale of [enLocale, zhCNLocale, zhTWLocale]) {
    assert.match(locale.messages["claude.notOnPath"], /PATH/);
    for (const key of Object.keys(locale.messages)) {
      assert.doesNotMatch(key, /^claude\.(alias|preset|configDir|settingsHeading|modelAliasLabel|modelLabel|fixedForSession|harnessPi|harnessClaude)/, `${locale.id} ${key}`);
    }
    // the "fixed for this session" line and its key are gone.
    assert.equal(locale.messages["claude.fixedLine"], undefined, locale.id);
  }
});

// polyforge#71 rewrites #219's text-only claude composer rule: while a claude
// turn runs the composer offers pi's own streaming controls — Steer and Stop — and
// no disabled Send button; Send returns once the turn ends. pi's own streaming
// composer never had a Send button, so the two harnesses now match.
// polyforge#71 (AC2): a claude composer's `/` menu lists only the pi built-ins a
// claude session's server really answers — `/compact` (the guarded compaction,
// the same handler the compact button takes) and `/session` — and neither the
// other pi built-ins nor a bare `/mcp` are routed to pi's built-in handler there;
// pi's own menu and routing are unchanged.
test("a claude session's slash menu lists only /compact and /session of pi's built-ins; pi keeps all seven", () => {
  assert.deepEqual(builtinSlashCommands(true).map((command) => command.name), ["compact", "session"]);
  assert.deepEqual(builtinSlashCommands(false).map((command) => command.name),
    ["compact", "auto-compact", "reload", "name", "session", "copy", "clone"]);
  assert.equal(canRunBuiltinSlashCommandWhileStreaming("/session", true), true, "/session runs while a claude turn streams, as on pi");
  assert.equal(canRunBuiltinSlashCommandWhileStreaming("/compact", true), false, "/compact waits for the turn to end, as on pi");
  assert.equal(offersBuiltinSlashCommandWhileStreaming("/mcp", true), false, "claude's /mcp is sent to claude as text, never pi's Settings › MCP");
  assert.equal(canRunBuiltinSlashCommandWhileStreaming("/mcp", true), false);
  assert.equal(offersBuiltinSlashCommandWhileStreaming("/mcp"), true, "pi's own routing is unchanged");
  assert.equal(canRunBuiltinSlashCommandWhileStreaming("/copy", true), false, "pi-only built-ins never reach pi's handler in a claude session");

  // The palette's list is exactly the built-ins the composer's harness offers —
  // streamed through the component's own combiner (builtinSlashCommands, its
  // availableWhileStreaming filter, then filterSlashCommands) — plus the session's
  // extension commands. Static markup never runs the effect that opens the
  // palette, so the combiner is asserted directly (filterSlashCommands orders
  // by name, not declaration order).
  const claudeIdle = filterSlashCommands(builtinSlashCommands(true), "", t);
  assert.deepEqual(claudeIdle.map((command) => command.name), ["compact", "session"]);
  const claudeStreaming = filterSlashCommands(builtinSlashCommands(true).filter((command) => command.availableWhileStreaming), "", t);
  assert.deepEqual(claudeStreaming.map((command) => command.name), ["session"], "while a turn runs only /session is offered, as on pi");
  const piIdle = filterSlashCommands(builtinSlashCommands(false), "", t);
  assert.deepEqual(piIdle.map((command) => command.name),
    ["auto-compact", "clone", "compact", "copy", "name", "reload", "session"]);
  const claudeWithOwn = filterSlashCommands([...builtinSlashCommands(true), { name: "deep-research", source: "extension", description: "claude's own" }], "", t);
  assert.deepEqual(claudeWithOwn.map((command) => command.name), ["compact", "session", "deep-research"],
    "claude's own commands (AC3) arrive beside the two built-ins through the session's command list");
});

test("while a claude turn runs the composer offers only Steer and Stop, no Send; Send returns when it ends", () => {
  const open = { ...base, choosing: false, harness: "claude" };
  const html = composer(open, { isStreaming: true, onSteer() {} });
  assert.doesNotMatch(html, /aria-label="Send"/, "a running claude turn still shows a Send button");
  assert.match(html, /aria-label="Deliver after the current response and its tool calls finish \(Stop interrupts them\)"/, "the composer offers pi's Steer");
  assert.match(html, /aria-label="Stop agent"/, "the composer offers Stop");
  assert.match(html, /placeholder="claude is answering; messages join this turn or wait for the next\."/, "the composer says what a message sent now does");
  const idle = composer({ ...base, choosing: false, harness: "claude" }, {});
  assert.match(idle, /aria-label="Send"/, "an idle claude session gets its Send button back");
  const pi = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(ChatInput, {
    onSend() {}, onAbort() {}, isStreaming: true,
  })));
  assert.doesNotMatch(pi, /aria-label="Send"/, "a pi run keeps its own streaming controls");
});

const NO_SHELL = {
  en: "claude sessions have no shell mode: text starting with ! is sent to claude as a message",
  "zh-CN": "claude 会话没有 Shell 模式：以 ! 开头的内容会作为普通消息发给 claude",
  "zh-TW": "claude 會話沒有 Shell 模式：以 ! 開頭的內容會作為一般訊息傳給 claude",
};
test("a claude composer never enters Bash mode: a leading ! shows the no-shell hint in en, zh-CN and zh-TW", () => {
  const draftKey = "claude:/tmp/claude-no-shell";
  setDraft(draftKey, { value: "!ls", images: [] });
  try {
    for (const state of [{ ...base, harness: "claude" }, { ...base, choosing: false, harness: "claude" }]) {
      const html = composer(state, { draftKey });
      assert.ok(html.includes(">!ls</textarea>"), "the draft is in the composer");
      assert.ok(!html.includes("Shell · output sent to model"), `${state.choosing ? "fresh" : "open"} claude composer: no Bash mode label`);
      assert.ok(!html.includes("var(--tool-bg)"), "no Bash mode border");
      const { before } = split(html);
      assert.ok(before.includes(NO_SHELL.en), "the hint sits where the Bash label was, above the bottom bar");
    }
    const pi = composer(base, { draftKey });
    assert.ok(pi.includes("Shell · output sent to model"), "a pi composer still enters Bash mode");
    assert.ok(!pi.includes(NO_SHELL.en), "no claude hint in a pi composer");
  } finally {
    clearDraft(draftKey);
  }
  for (const [locale, messages] of [["en", enLocale], ["zh-CN", zhCNLocale], ["zh-TW", zhTWLocale]]) {
    assert.equal(messages.messages["chat.claudeNoShell"], NO_SHELL[locale], locale);
  }
});

// ── 85a3 u4 (AC2): the claude model selector's 「设为默认」 star ────────────
test("the claude model selector receives the star only when the save handler is passed, marking the stored default", async () => {
  // ModelSelector renders its rows only while the popup is open (a measured
  // anchor), so the wiring is pinned at the props the selector receives: a
  // spy module captures them.
  let captured = null;
  const spyJiti = createJiti(import.meta.url, {
    jsx: { runtime: "automatic" },
    tsconfigPaths: true,
    // the main jiti instance's ChatInput already sits in Node's require cache
    // under its absolute path; a private module cache re-evaluates it here so
    // the spy module the selector receives is this instance's
    moduleCache: false,
    virtualModules: {
      "./ModelSelector": {
        ModelSelector: function ModelSelectorSpy(props) { captured = props; return null; },
      },
      // the re-evaluated ChatInput must read the same i18n context the test
      // provides, so useI18n stays the main instance's module
      "@/hooks/useI18n": await jiti.import("@/hooks/useI18n"),
    },
  });
  const { ChatInput: ChatInputSpy } = await spyJiti.import("./ChatInput.tsx");
  const renderSpy = (claude) => renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(ChatInputSpy, {
    onSend() {}, onAbort() {}, claude,
  })));
  // without the handler (the subagent profile form, say) the selector gets no star
  renderSpy({ ...base, harness: "claude" });
  assert.equal(captured.onSetDefault, undefined, "no save handler without the star");
  assert.equal(captured.defaultValue, undefined, "no filled marker without the star");
  // with it: the stored default marks the row (the native default, here opus)
  renderSpy({ ...base, harness: "claude", model: "sonnet", defaultModel: "opus", onSetDefaultModel() {} });
  assert.deepEqual(captured.defaultValue, { provider: "claude", modelId: "opus" });
  assert.equal(typeof captured.onSetDefault, "function", "the star's save handler");
});

test("the claude star's save passes the row's model name, not the provider (the adapter the selector calls)", () => {
  const saved = [];
  claudeInputCallback("claudeSaveDefault", { claude: { onSetDefaultModel: (model) => saved.push(model) } })(undefined, "claude-row-model");
  assert.deepEqual(saved, ["claude-row-model"]);
});
