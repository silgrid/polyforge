// carries: components/SkillsConfig.tsx, components/SettingsPanel.tsx, lib/i18n/messages/en.ts, lib/i18n/messages/zh-CN.ts, lib/i18n/messages/zh-TW.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

// 85d u5: the row's 「应用到」 and the switch results as pure helpers — a
// submission reaches only the harnesses the row lists, the unified 「停用」
// becomes claude's own "user-invocable-only" word (owner Q4: 模型不可见、
// 用户仍可手动调用), a claude item names the native skillOverrides key and
// the row's scope layer, and a result without `error` is the only one that
// changes its row.
const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const {
  rowSwitchTargets,
  rowSwitchValues,
  rowSwitchItems,
  applyRowSwitchResults,
  rowsToSwitch,
  refusalResult,
  switchResultRow,
  SkillDetail,
} = await jiti.import("./SkillsConfig.tsx");

const TEXTS = { notListed: "not listed by this harness", pi: "pi", claude: "claude" };

const bothRow = {
  key: "/a/SKILL.md",
  scope: "user",
  name: "both",
  harnesses: ["pi", "claude"],
  sameName: [],
  pi: { name: "both", filePath: "/a/SKILL.md", disableModelInvocation: false, sourceInfo: {} },
  claude: { name: "both", filePath: "/a/SKILL.md" },
};
const piOnlyRow = {
  key: "/b/SKILL.md",
  scope: "user",
  name: "pi-only",
  harnesses: ["pi"],
  sameName: [],
  pi: { name: "pi-only", filePath: "/b/SKILL.md", disableModelInvocation: true, sourceInfo: {} },
};
const claudeOnlyRow = {
  key: "/c/SKILL.md",
  scope: "project",
  name: "claude-only",
  harnesses: ["claude"],
  sameName: [],
  claude: { name: "claude-only", filePath: "/c/SKILL.md", state: "name-only" },
};

test("the row's apply-to targets: a harness the row does not list is disabled with its reason", () => {
  const both = rowSwitchTargets(bothRow, TEXTS);
  assert.deepEqual(both.map((target) => [target.harness, target.disabled]), [["pi", undefined], ["claude", undefined]]);
  const claudeOnly = rowSwitchTargets(claudeOnlyRow, TEXTS);
  assert.equal(claudeOnly.length, 2);
  assert.equal(claudeOnly[0].harness, "pi");
  assert.equal(claudeOnly[0].disabled, true);
  assert.ok(claudeOnly[0].disabledReason?.includes("not listed"));
  assert.equal(claudeOnly[1].disabled, undefined);
});

test("the submission's items name each harness's own store: the path for pi, the native key and layer for claude (AC2)", () => {
  const items = rowSwitchItems(bothRow, ["pi", "claude"], "disabled");
  assert.deepEqual(items, [
    { skill: "/a/SKILL.md", harness: "pi", state: "disabled" },
    { skill: "both", harness: "claude", state: "user-invocable-only", scope: "user" },
  ]);
  const enabled = rowSwitchItems(bothRow, ["claude"], "enabled");
  assert.deepEqual(enabled, [{ skill: "both", harness: "claude", state: "enabled", scope: "user" }]);
  // a project row's claude item names the project layer (owner Q3 B)
  const project = rowSwitchItems(claudeOnlyRow, ["claude"], "disabled");
  assert.deepEqual(project, [{ skill: "claude-only", harness: "claude", state: "user-invocable-only", scope: "project" }]);
  // a submission never reaches a harness the row does not list
  assert.deepEqual(rowSwitchItems(piOnlyRow, ["pi", "claude"], "disabled"), [
    { skill: "/b/SKILL.md", harness: "pi", state: "disabled" },
  ]);
});

test("a result without error is the only one that changes its row; pi and claude update their own side", () => {
  const rows = [bothRow, piOnlyRow, claudeOnlyRow];
  const next = applyRowSwitchResults(rows, [
    { skill: "/b/SKILL.md", harness: "pi" },
    { skill: "claude-only", harness: "claude" },
    { skill: "both", harness: "pi", error: "Access denied" },
  ], { pi: "enabled", claude: "user-invocable-only" });
  assert.equal(next[0].pi.disableModelInvocation, false, "the failed pi item keeps its state");
  assert.equal(next[0].claude.state, undefined);
  assert.equal(next[1].pi.disableModelInvocation, false, "the successful pi item shows");
  assert.equal(next[2].claude.state, "user-invocable-only");
  assert.equal(next[0], rows[0], "the row whose item failed keeps its identity");
});

test("both sides of one row update independently when both succeed (review r1#2)", () => {
  const rows = [bothRow];
  const next = applyRowSwitchResults(rows, [
    { skill: "/a/SKILL.md", harness: "pi" },
    { skill: "both", harness: "claude" },
  ], { pi: "disabled", claude: "user-invocable-only" });
  assert.equal(next[0].pi.disableModelInvocation, true, "the pi side takes the sent state");
  assert.equal(next[0].claude.state, "user-invocable-only", "the claude side takes its own sent word, not lost behind the pi result");
});

test("the group switch acts on the rows whose pi side would change, pi alone (85c's conclusion, Q6)", () => {
  const rows = [bothRow, piOnlyRow, claudeOnlyRow];
  assert.deepEqual(rowsToSwitch(rows, true).map((row) => row.key), ["/b/SKILL.md"]);
  assert.deepEqual(rowsToSwitch(rows, false).map((row) => row.key), ["/a/SKILL.md"]);
  const allShown = rows.map((row) => row.pi ? { ...row, pi: { ...row.pi, disableModelInvocation: false } } : row);
  assert.deepEqual(rowsToSwitch(allShown, true), []);
});

// polyforge#259 T4 AC1 (review #2/#6): the undo's previous value is READ from the
// row, never derived from the state being written — pi's effective state and
// claude's own native word. Expected values come from the row fixture, not from
// the component's output; a "derive it from the new state" implementation
// returns "disabled" for the first case and "enabled" for claude's native word,
// and both assertions turn red.
test("the undo's previous values are the row's own, per harness", () => {
  const row = {
    key: "user:/skills/a/SKILL.md",
    scope: "user",
    name: "a",
    harnesses: ["pi", "claude"],
    sameName: [],
    pi: { filePath: "/skills/a/SKILL.md", disableModelInvocation: false, sourceInfo: {} },
    claude: { name: "a", filePath: "/skills/a/SKILL.md", state: "user-invocable-only" },
  };
  assert.deepEqual(rowSwitchValues(row, ["pi", "claude"]), [
    { harness: "pi", state: "enabled" },
    { harness: "claude", state: "user-invocable-only" },
  ], "a previous value was derived from the new state instead of read");
  // A pi side that hides the skill reads as disabled, and a claude side with no
  // stored word reads as enabled.
  const hidden = { ...row, pi: { ...row.pi, disableModelInvocation: true }, claude: { name: "a", filePath: "/skills/a/SKILL.md" } };
  assert.deepEqual(rowSwitchValues(hidden, ["pi", "claude"]), [
    { harness: "pi", state: "disabled" },
    { harness: "claude", state: "enabled" },
  ]);
  // A harness the row does not list carries nothing to restore.
  assert.deepEqual(rowSwitchValues({ ...row, claude: undefined }, ["pi", "claude"]), [{ harness: "pi", state: "enabled" }]);
});

// polyforge#259 T4 AC2 (review #3): a refused harness's row says it in Chinese and
// keeps the server's own words for its details fold. The expected line is the
// generic Chinese message — the server's text must never be the main line.
test("a refused harness reads in Chinese and keeps its own words folded", async () => {
  const t = (key, params) => {
    const table = { "serverError.unknown": "操作没有成功，服务端返回的原文在「详情」里。" };
    return (table[key] ?? key).replace("{}", params?.x ?? "");
  };
  const row = refusalResult(t, "pi", "EACCES: permission denied, open '/root/.pi/agent/pi-web-settings.json'");
  assert.equal(row.harness, "pi");
  assert.equal(row.ok, false);
  assert.equal(row.text, "操作没有成功，服务端返回的原文在「详情」里。", "the server's English became the main line");
  assert.match(row.details ?? "", /EACCES/, "the refusal's own words are not kept for the fold");
  const coded = refusalResult(t, "claude", { code: "config-cas-409", message: "changed since it was read" });
  assert.equal(coded.text, "serverError.conflict", "a code does not answer its own message key");
});

// polyforge#259 T4 AC2 (review r3#1, owner 2A: 前端按 code i18n): a refused
// switch item the route marks `code: "skills-write-refused"` reads as the
// forbidden line, not the generic one. Expected text hand-copied from
// lib/i18n/messages/zh-CN.ts `serverError.forbidden`. Caught change: the
// result row passing only `result.error` (the code dropped).
const ZH_FORBIDDEN = "服务端拒绝了这次改动，原文在「详情」里。";
const zhT = (key) => ({ "serverError.forbidden": ZH_FORBIDDEN, "skills.piWritten": "已写入 pi" }[key] ?? key);

test("a refused switch result reads in Chinese by the route's code (T4 AC2, owner 2A)", () => {
  const direct = refusalResult(zhT, "pi", { message: "Access denied", code: "skills-write-refused" });
  assert.equal(direct.text, ZH_FORBIDDEN, "refusalResult ignores the route's code");
  const row = switchResultRow(zhT, { skill: "/a/SKILL.md", harness: "pi", error: "Access denied", code: "skills-write-refused" });
  assert.equal(row.ok, false);
  assert.equal(row.text, ZH_FORBIDDEN, "the result row drops the route's code");
  assert.equal(row.details, "Access denied", "the refusal's own words are not kept for the fold");
  assert.deepEqual(switchResultRow(zhT, { skill: "/a/SKILL.md", harness: "pi" }), { harness: "pi", ok: true, text: "已写入 pi" });
});

// The detail view's two save-error slots (pi's row and claude's partition):
// the Chinese line is the visible text, the server's English only inside the
// details fold (docs/agents/settings-ui.md, owner 2A). Caught change: a
// view printing the server's words beside the line again.
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const outsideFolds = (html) => html.replace(/<details[\s\S]*?<\/details>/g, "");
const foldsOf = (html) => (html.match(/<details[\s\S]*?<\/details>/g) ?? []).join("");

test("a save error reads in Chinese, the server's words only in the fold (T4 AC2, review r3#1)", () => {
  const noop = () => {};
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(SkillDetail, {
    row: bothRow, piSkill: null, cwd: null, applyResults: null, applyBusy: false,
    saveError: { summary: ZH_FORBIDDEN, details: "skill file is outside the allowed roots" },
    claudeSaveError: { summary: "目标在此期间被别的写入改过，请重新读取后再试。", details: "settings.json changed since it was read" },
    checkingUpdate: false, updating: false, updateError: null,
    onCheckUpdate: noop, onUpdate: noop, onCleanupLegacy: noop, cleaningLegacy: false,
    onApplySwitch: noop, onWriteClaudeState: noop,
  })));
  const outside = outsideFolds(html);
  assert.ok(outside.includes(ZH_FORBIDDEN), "the pi save error's Chinese line is not the visible text");
  assert.ok(outside.includes("目标在此期间被别的写入改过，请重新读取后再试。"), "the claude save error's Chinese line is not the visible text");
  assert.ok(!outside.includes("skill file is outside the allowed roots"), "the pi save error's server words show outside the fold");
  assert.ok(!outside.includes("settings.json changed since it was read"), "the claude save error's server words show outside the fold");
  const folds = foldsOf(html);
  assert.ok(folds.includes("skill file is outside the allowed roots"), "the pi save error's server words are not in its fold");
  assert.ok(folds.includes("settings.json changed since it was read"), "the claude save error's server words are not in its fold");
});
