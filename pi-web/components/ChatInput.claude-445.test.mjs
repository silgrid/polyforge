// carries: components/ChatInput.tsx, lib/i18n/messages/*
// AC2: an open claude session switches its permission mode from the
// composer (claude's set_permission_mode, temporary for that session). default is
// labelled as Claude Code's own setting (owner ruling Q2=A: no --permission-mode, the
// user's ~/.claude decides); plan is offered, enabled when the session's permission prompts
// come to Pi Web ('s ask) and otherwise disabled with its reason as visible text
// ( r3, owner ruling 2026-11-13T07:40:00Z).
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { ChatInput, ClaudeLaunchPermissionModeMenu } = await jiti.import("./ChatInput.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { enLocale } = await jiti.import("@/lib/i18n/messages/en");
const { zhCNLocale } = await jiti.import("@/lib/i18n/messages/zh-CN");
const { zhTWLocale } = await jiti.import("@/lib/i18n/messages/zh-TW");

const translator = (locale) => (key, params = {}) => String(locale.messages[key] ?? key).replace(/\{(\w+)\}/g, (_, name) => String(params[name] ?? ""));
const t = translator(enLocale);
/** Markup with React's entity escapes undone, so visible text compares as written. */
const text = (html) => html.replace(/&#x27;/g, "'").replace(/&quot;/g, "\"").replace(/&amp;/g, "&");

const DEFAULT_LABEL = {
  en: "Default (follows Claude Code's own settings)",
  "zh-CN": "默认（按 Claude Code 自身设置）",
  "zh-TW": "預設（依 Claude Code 自身設定）",
};
// r3 (D11): plan needs's claude tool permissions on 「Ask in a dialog」.
const PLAN_UNAVAILABLE = {
  en: "Not available: plan mode needs claude tool permissions set to Ask in a dialog (Settings › General) — on Deny silently claude would refuse to leave plan mode",
  "zh-CN": "暂不可用：plan 模式需要 claude 工具授权为「弹窗询问」（设置 › 常规）——在「静默拒绝」下 claude 会拒绝退出 plan 模式",
  "zh-TW": "暫不可用：plan 模式需要 claude 工具授權為「彈窗詢問」（設定 › 一般）——在「靜默拒絕」下 claude 會拒絕退出 plan 模式",
};
const KEYS = [
  "claude.permissionModeLabel",
  "claude.permissionModeDefault",
  "claude.permissionModeAcceptEditsDesc",
  "claude.permissionModePlanDesc",
  "claude.permissionModePlanUnavailable",
  "claude.permissionModeError",
];

const base = { choosing: false, harness: "claude", model: "default", effort: "default", availability: { available: true, models: ["default", "opus", "fable", "sonnet", "haiku"] }, onModelChange() {}, onEffortChange() {} };
function composer(claude, extra = {}) {
  return text(renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(ChatInput, {
    onSend() {}, onAbort() {}, claude, ...extra,
  }))));
}
/** The permission-mode button: its opening tag and visible text. */
function control(html) {
  const match = html.match(/<button([^>]*aria-label="Permission mode"[^>]*)>([\s\S]*?)<\/button>/);
  return match ? { attrs: match[1], label: match[2].replace(/<[^>]+>/g, "") } : null;
}
const menu = (mode, planAvailable, tr = t) => text(renderToStaticMarkup(React.createElement(ClaudeLaunchPermissionModeMenu, { mode, planAvailable, t: tr, onSelect() {} })));
/** Each menu row: the option's opening tag and its visible text. */
const rows = (html) => [...html.matchAll(/<button([^>]*role="option"[^>]*)>([\s\S]*?)<\/button>/g)].map((m) => ({ attrs: m[1], text: m[2].replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim() }));

test("an open claude session shows the permission-mode control with default and acceptEdits; default reads as Claude Code's own setting and plan is disabled with its reason as visible text", () => {
  assert.equal(typeof ClaudeLaunchPermissionModeMenu, "function");
  const open = { ...base, launchPermissionMode: "default", planAvailable: false, onLaunchPermissionModeChange() {} };
  const atDefault = control(composer(open, { onCompact() {} }));
  assert.ok(atDefault, "no permission-mode control in an open claude session");
  assert.equal(atDefault.label.trim(), DEFAULT_LABEL.en, "default is labelled as Claude Code's own setting");
  assert.doesNotMatch(atDefault.attrs, /disabled=""/);
  assert.equal(control(composer({ ...open, launchPermissionMode: "acceptEdits" })).label.trim(), "acceptEdits");
  // Not idle-guarded: claude takes a permission-mode switch mid-turn.
  assert.doesNotMatch(control(composer(open, { isStreaming: true })).attrs, /disabled=""/, "usable while a turn runs");

  const options = rows(menu("default", false));
  assert.equal(options.length, 3);
  const [byDefault, acceptEdits, plan] = options;
  assert.ok(byDefault.text.startsWith(DEFAULT_LABEL.en), byDefault.text);
  assert.match(byDefault.attrs, /aria-selected="true"/);
  assert.ok(acceptEdits.text.startsWith("acceptEdits"), acceptEdits.text);
  assert.ok(acceptEdits.text.includes(enLocale.messages["claude.permissionModeAcceptEditsDesc"]), acceptEdits.text);
  assert.match(acceptEdits.attrs, /aria-selected="false"/);
  for (const usable of [byDefault, acceptEdits]) assert.doesNotMatch(usable.attrs, /disabled=""/);
  // plan: offered, disabled, and why is text in the row, never a tooltip only.
  assert.ok(plan.text.startsWith("plan"), plan.text);
  assert.match(plan.attrs, /disabled=""/);
  const reason = enLocale.messages["claude.permissionModePlanUnavailable"];
  assert.equal(reason, PLAN_UNAVAILABLE.en);
  assert.ok(plan.text.includes(reason), plan.text);
  assert.match(rows(menu("acceptEdits", false))[1].attrs, /aria-selected="true"/);
});

test("a fresh claude composer shows no permission-mode control, and every label exists in en, zh-CN and zh-TW", () => {
  assert.equal(typeof ClaudeLaunchPermissionModeMenu, "function");
  assert.equal(control(composer({ ...base, choosing: true, onHarnessChange() {} })), null, "a fresh claude composer: Settings › Claude decides");
  assert.equal(control(composer({ ...base, choosing: true, harness: "pi" })), null, "pi");
  assert.equal(control(composer({ ...base, choosing: false, harness: "pi" })), null, "a pi session");

  for (const [id, locale] of [["en", enLocale], ["zh-CN", zhCNLocale], ["zh-TW", zhTWLocale]]) {
    for (const key of KEYS) assert.ok(typeof locale.messages[key] === "string" && locale.messages[key].trim(), `${id} ${key}`);
    assert.equal(locale.messages["claude.permissionModeDefault"], DEFAULT_LABEL[id], id);
    assert.match(locale.messages["claude.permissionModeError"], /\{error\}/, id);
    assert.equal(locale.messages["claude.permissionModePlanUnavailable"], PLAN_UNAVAILABLE[id], id);
    const [byDefault, , plan] = rows(menu("default", false, translator(locale)));
    assert.ok(byDefault.text.startsWith(DEFAULT_LABEL[id]), `${id}: ${byDefault.text}`);
    assert.ok(plan.text.includes(locale.messages["claude.permissionModePlanUnavailable"]), `${id}: ${plan.text}`);
  }
  assert.equal(zhCNLocale.messages["claude.permissionModeDefault"], "默认（按 Claude Code 自身设置）");
});

test("an open claude session whose permission prompts come to Pi Web offers plan as an enabled choice with its description ( r3)", () => {
  const open = { ...base, launchPermissionMode: "plan", planAvailable: true, onLaunchPermissionModeChange() {} };
  assert.equal(control(composer(open)).label.trim(), "plan");
  const [, , plan] = rows(menu("default", true));
  assert.ok(plan.text.startsWith("plan"), plan.text);
  assert.doesNotMatch(plan.attrs, /disabled=""/, "plan is disabled though the session's permission prompts come to Pi Web");
  assert.match(plan.attrs, /aria-selected="false"/);
  assert.ok(plan.text.includes(enLocale.messages["claude.permissionModePlanDesc"]), plan.text);
  assert.ok(!plan.text.includes(enLocale.messages["claude.permissionModePlanUnavailable"]), plan.text);
  assert.match(rows(menu("plan", true))[2].attrs, /aria-selected="true"/);
});

// r3 (owner ruling 2026-11-13T07:40:00Z): owns settings.claudePermission*
// (its ask/deny tool permissions); Settings › Claude's launch permission mode has its own keys.
test("the launch permission mode's settings labels have their own keys, apart from's settings.claudePermission* ( r3)", () => {
  const LAUNCH = {
    en: ["Default permission mode", "The permission mode a new claude session starts with (--permission-mode); an open session can switch its own from the composer."],
    "zh-CN": ["默认权限模式", "新建 claude 会话启动时的权限模式（--permission-mode）；已打开的会话可在输入框旁切换自己的模式。"],
    "zh-TW": ["預設權限模式", "新建 claude 工作階段啟動時的權限模式（--permission-mode）；已開啟的工作階段可在輸入框旁切換自己的模式。"],
  };
  for (const [id, locale] of [["en", enLocale], ["zh-CN", zhCNLocale], ["zh-TW", zhTWLocale]]) {
    assert.equal(locale.messages["settings.claudeLaunchPermissionMode"], LAUNCH[id][0], id);
    assert.equal(locale.messages["settings.claudeLaunchPermissionModeDescription"], LAUNCH[id][1], id);
    assert.ok(!LAUNCH[id].includes(locale.messages["settings.claudePermissionMode"]), `${id}: settings.claudePermissionMode is's key`);
  }
});
