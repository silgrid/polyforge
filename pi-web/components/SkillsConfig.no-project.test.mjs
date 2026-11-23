// carries: components/SkillsConfig.tsx, components/SettingsPanel.tsx, lib/i18n/messages/en.ts, lib/i18n/messages/zh-CN.ts, lib/i18n/messages/zh-TW.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

// 85d u5: the no-project form of the panel (AC3, owner Q7 B) — the sidebar
// groups are 用户 alone (no 项目 group without a project), the add panel
// offers the global scope alone, and the install stays pi's own (`--agent
// pi`), the entry notes it.
const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const { skillRowGroups, addScopeChoices } = await jiti.import("./SkillsConfig.tsx");

const userRow = { key: "/u/SKILL.md", scope: "user", name: "u", harnesses: ["pi"], sameName: [] };
const projectRow = { key: "/p/SKILL.md", scope: "project", name: "p", harnesses: ["pi"], sameName: [] };

test("without a project the sidebar shows the user group alone (AC3)", () => {
  const rows = [userRow, projectRow];
  const noProject = skillRowGroups(rows, false);
  assert.deepEqual(noProject.map((group) => group.scope), ["user"]);
  assert.deepEqual(noProject[0].rows.map((row) => row.key), ["/u/SKILL.md"]);

  const withProject = skillRowGroups(rows, true);
  assert.deepEqual(withProject.map((group) => group.scope), ["user", "project"]);
  assert.deepEqual(withProject[1].rows.map((row) => row.key), ["/p/SKILL.md"]);

  // an empty group never renders
  assert.deepEqual(skillRowGroups([projectRow], false), []);
});

test("the add panel offers the global scope alone without a project (Q7 B)", () => {
  assert.deepEqual(addScopeChoices(false), ["global"]);
  assert.deepEqual(addScopeChoices(true), ["global", "project"]);
});
