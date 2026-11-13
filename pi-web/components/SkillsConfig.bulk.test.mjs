import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const { applySkillToggleResults, skillsToSwitch } = await jiti.import("./SkillsConfig.tsx");

const skills = [
  { name: "a", filePath: "/skills/a/SKILL.md", disableModelInvocation: false },
  { name: "b", filePath: "/skills/b/SKILL.md", disableModelInvocation: true },
  { name: "c", filePath: "/skills/c/SKILL.md", disableModelInvocation: true },
];

test("a group switch only targets the skills that would change", () => {
  assert.deepEqual(skillsToSwitch(skills, true).map((skill) => skill.name), ["b", "c"]);
  assert.deepEqual(skillsToSwitch(skills, false).map((skill) => skill.name), ["a"]);
  const allVisible = skills.map((skill) => ({ ...skill, disableModelInvocation: false }));
  assert.deepEqual(skillsToSwitch(allVisible, true), []);
});

test("a bulk result switches the skills that succeeded and keeps the failures as they were", () => {
  const next = applySkillToggleResults(skills, [
    { filePath: "/skills/b/SKILL.md" },
    { filePath: "/skills/c/SKILL.md", error: "Access denied" },
  ], false);

  assert.deepEqual(next.map((skill) => [skill.name, skill.disableModelInvocation]), [
    ["a", false],
    ["b", false],
    ["c", true],
  ]);
  assert.equal(next[0], skills[0], "untouched skills keep their identity");
});
