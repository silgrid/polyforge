// carries: components/SkillsConfig.tsx, components/SettingsPanel.tsx, lib/i18n/messages/en.ts, lib/i18n/messages/zh-CN.ts, lib/i18n/messages/zh-TW.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

// 85d u5: the group switch over unified rows — the switch acts on the rows
// whose pi side would change (85c's conclusion: the group 「全部停用」 acts on
// pi alone), the round reports per item, and only a result without error
// changes its row. (The batch wire itself is pinned by the route tests.)
const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const { applyRowSwitchResults, rowsToSwitch } = await jiti.import("./SkillsConfig.tsx");

const row = (name, off) => ({
  key: `/${name}/SKILL.md`,
  scope: "user",
  name,
  harnesses: ["pi"],
  sameName: [],
  pi: { name, filePath: `/${name}/SKILL.md`, disableModelInvocation: off, sourceInfo: {} },
});
const rows = [row("a", false), row("b", true), row("c", true)];

test("a group switch only targets the rows whose pi side would change", () => {
  assert.deepEqual(rowsToSwitch(rows, true).map((entry) => entry.name), ["b", "c"]);
  assert.deepEqual(rowsToSwitch(rows, false).map((entry) => entry.name), ["a"]);
  const allVisible = rows.map((entry) => ({ ...entry, pi: { ...entry.pi, disableModelInvocation: false } }));
  assert.deepEqual(rowsToSwitch(allVisible, true), []);
});

test("a bulk result switches the rows that succeeded and keeps the failures as they were", () => {
  const next = applyRowSwitchResults(rows, [
    { skill: "/b/SKILL.md", harness: "pi" },
    { skill: "/c/SKILL.md", harness: "pi", error: "Access denied" },
  ], { pi: "enabled" });

  assert.deepEqual(next.map((entry) => [entry.name, entry.pi.disableModelInvocation]), [
    ["a", false],
    ["b", false],
    ["c", true],
  ]);
  assert.equal(next[0], rows[0], "untouched rows keep their identity");
});
