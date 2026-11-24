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
