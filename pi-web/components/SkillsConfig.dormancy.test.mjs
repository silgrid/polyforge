// carries: components/SkillsConfig.tsx, components/SettingsPanel.tsx, lib/i18n/messages/en.ts, lib/i18n/messages/zh-CN.ts, lib/i18n/messages/zh-TW.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

// 85d u5: dormancy over unified rows — a row is dormant when every side that
// lists it is off (pi hidden, claude's override any word but "enabled"), and
// the author-hidden badge still keys on the declared frontmatter alone.
const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const { orderRowsByDormancy, skillAuthorDeclaredHidden } = await jiti.import("./SkillsConfig.tsx");

test("lists active rows before dormant rows while preserving their order", () => {
  const rows = [
    { key: "1", scope: "user", name: "dormant-a", harnesses: ["pi"], sameName: [], pi: { disableModelInvocation: true } },
    { key: "2", scope: "user", name: "active-a", harnesses: ["pi"], sameName: [], pi: { disableModelInvocation: false } },
    { key: "3", scope: "user", name: "dormant-b", harnesses: ["claude"], sameName: [], claude: { name: "dormant-b", state: "user-invocable-only" } },
    { key: "4", scope: "user", name: "active-b", harnesses: ["claude"], sameName: [], claude: { name: "active-b" } },
    { key: "5", scope: "user", name: "mixed", harnesses: ["pi", "claude"], sameName: [], pi: { disableModelInvocation: true }, claude: { name: "mixed", state: "enabled" } },
  ];

  assert.deepEqual(
    orderRowsByDormancy(rows).map((row) => row.name),
    ["active-a", "active-b", "mixed", "dormant-a", "dormant-b"],
  );
});

test("the author-hidden badge keys on the declared frontmatter, not the effective switch", () => {
  assert.equal(skillAuthorDeclaredHidden({ declaredDisableModelInvocation: true }), true);
  assert.equal(skillAuthorDeclaredHidden({ declaredDisableModelInvocation: true, disableModelInvocation: false }), true);
  assert.equal(skillAuthorDeclaredHidden({ declaredDisableModelInvocation: false }), false);
  assert.equal(skillAuthorDeclaredHidden({}), false, "a skill the loader never annotated has no author line");
});
