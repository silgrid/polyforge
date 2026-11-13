import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { ChatInput, ClaudeComposerControls, CLAUDE_EFFORT_ENTRIES, claudeModelOptions, filterComposerImageFiles } = await jiti.import("./ChatInput.tsx");
const { clearDraft, setDraft } = await jiti.import("@/lib/draft-store");
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

test("a running claude turn (or a state not known yet) disables the model, effort and compact controls (rendered, not hidden); Stop stays", () => {
  const open = { ...base, choosing: false, harness: "claude", model: "claude-sonnet-5-5", onModelChange() {}, onEffortChange() {} };
  for (const [state, extra] of [[open, { isStreaming: true }], [open, { isCompacting: true }], [{ ...open, stateKnown: false }, {}]]) {
    const html = composer(state, { onCompact() {}, ...extra });
    assert.match(html, /class="model-selector[^"]*is-disabled/, JSON.stringify(extra));
    assert.match(html, /<button[^>]*disabled=""[^>]*aria-label="Change reasoning level"/, JSON.stringify(extra));
  }
  assert.match(composer(open, { onCompact() {}, isStreaming: true }), /<button[^>]*disabled=""[^>]*aria-label="Compact context"/);
  assert.match(composer({ ...open, stateKnown: false }, { onCompact() {} }), /<button[^>]*disabled=""[^>]*aria-label="Compact context"/, "not before the stream said the session is idle");
  assert.match(composer(open, { onCompact() {}, isStreaming: true }), /aria-label="Stop agent"/);
  const compacting = composer(open, { onCompact() {}, onAbortCompaction() {}, isCompacting: true });
  assert.match(compacting, /aria-label="Stop compaction"/);
  assert.match(compacting, /<button[^>]*disabled=""[^>]*aria-label="Send"/, "nothing is sent during claude's compaction");
  const idle = composer(open, { onCompact() {} });
  assert.doesNotMatch(idle, /<button[^>]*disabled=""[^>]*aria-label="Change reasoning level"/);
  assert.doesNotMatch(idle, /<button[^>]*disabled=""[^>]*aria-label="Compact context"/);
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

test("while a claude turn runs, Send stays visible but disabled, and the composer says why", () => {
  const html = composer({ ...base, choosing: false, harness: "claude" }, { isStreaming: true });
  assert.match(html, /<button type="button" disabled="" aria-label="Send"/);
  assert.match(html, /placeholder="claude is answering; the next message can be sent when this turn ends\."/);
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
