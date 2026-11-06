import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const { orderSkillsByDormancy } = await jiti.import("./SkillsConfig.tsx");

test("lists active skills before dormant skills while preserving their order", () => {
  const skills = [
    { name: "dormant-a", disableModelInvocation: true },
    { name: "active-a", disableModelInvocation: false },
    { name: "dormant-b", disableModelInvocation: true },
    { name: "active-b", disableModelInvocation: false },
  ];

  assert.deepEqual(
    orderSkillsByDormancy(skills).map((skill) => skill.name),
    ["active-a", "active-b", "dormant-a", "dormant-b"],
  );
});
