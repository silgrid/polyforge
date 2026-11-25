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

// polyforge#257 (T2 止血) B8: an embedded panel with nothing to report drew an
// empty 52px footer bar under the skill detail (SkillsConfig.tsx rendered
// ConfigFooter unconditionally). The footer is the panel's one status line
// plus its actions, so it exists exactly when one of them does — and the test
// reads the rendered markup, so a call site that draws it unconditionally
// again turns red. Expected values come from the ticket's B8 sentence.
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { SkillsConfig, SkillsConfigFooter } = await jiti.import("./SkillsConfig.tsx");

const h = React.createElement;
const render = (element) => renderToStaticMarkup(h(I18nProvider, null, element));
const noop = () => {};
const footer = (props) => render(h(SkillsConfigFooter, {
  embedded: true, installable: false, updateCount: 0, checkingAll: false, updating: false,
  onClose: noop, onCheckUpdates: noop, ...props,
}));

test("the skills footer renders exactly while it has a status line or an action (B8)", () => {
  // The panel's own call site: an embedded panel with nothing to report draws no footer bar.
  const panel = render(h(SkillsConfig, { cwd: null, onClose: noop, embedded: true }));
  assert.ok(panel.includes("config-split-view"), "the panel rendered");
  assert.ok(!panel.includes("config-footer"), "an embedded panel with no update and no install action draws no footer");

  // The footer unit: each of the three reasons draws one; without any it draws nothing.
  assert.equal(footer({}), "", "no status line and no action draws no footer");
  assert.match(footer({ installable: true }), /class="config-footer"/, "the check-updates action needs a footer");
  assert.match(footer({ updateCount: 2 }), /class="config-footer"/, "an update count needs a footer");
  assert.match(footer({ embedded: false }), /class="config-footer"/, "a standalone panel keeps its Close button");
});
