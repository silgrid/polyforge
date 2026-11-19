import "#test-env";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test, { after } from "node:test";
import { createJiti } from "jiti";

// The skill visibility preference: `skillVisibility.pi` in
// `<agentDir>/pi-web-settings.json`, keyed by the loader-reported skill file
// path. Expected values come from the ticket's settled decisions (Q1 B, Q2 A,
// Q5 A): a whole batch lands in one atomic write, other keys and other
// harness dimensions survive, and the tri-state rule is
// hidden / shown / unset-keeps-the-author's-value.
const root = await mkdtemp(join(tmpdir(), "pi-web-skill-visibility-"));
after(async () => {
  await rm(root, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, moduleCache: false });
const {
  readSkillVisibility,
  writeSkillVisibility,
  effectiveDisableModelInvocation,
  skillVisibilityOverride,
  composeSkillsOverrides,
} = await jiti.import("./skill-visibility-config.ts");

const settingsPath = (agentDir) => join(agentDir, "pi-web-settings.json");

async function makeAgentDir(name, initial) {
  const agentDir = join(root, name);
  await mkdir(agentDir, { recursive: true });
  if (initial !== undefined) await writeFile(settingsPath(agentDir), initial);
  return agentDir;
}

test("a whole batch lands in one write, keeping other keys and other harness dimensions", async () => {
  const agentDir = await makeAgentDir(
    "batch",
    `${JSON.stringify({
      version: 1,
      agentLanguage: "zh-CN",
      skillVisibility: { claude: { "/other/harness/SKILL.md": "hidden" } },
    }, null, 2)}\n`,
  );

  await writeSkillVisibility([
    { filePath: "/pi/skills/a/SKILL.md", visibility: "hidden" },
    { filePath: "/pi/skills/b/SKILL.md", visibility: "shown" },
  ], agentDir);

  const parsed = JSON.parse(await readFile(settingsPath(agentDir), "utf8"));
  assert.equal(parsed.agentLanguage, "zh-CN");
  assert.deepEqual(parsed.skillVisibility.claude, { "/other/harness/SKILL.md": "hidden" });
  assert.deepEqual(parsed.skillVisibility.pi, {
    "/pi/skills/a/SKILL.md": "hidden",
    "/pi/skills/b/SKILL.md": "shown",
  });
  assert.deepEqual(
    [...readSkillVisibility(agentDir)].sort(),
    [["/pi/skills/a/SKILL.md", "hidden"], ["/pi/skills/b/SKILL.md", "shown"]],
  );
});

test("a removed entry keeps the rest of its harness dimension", async () => {
  const agentDir = await makeAgentDir("remove", `${JSON.stringify({
    skillVisibility: {
      pi: { "/pi/a/SKILL.md": "hidden", "/pi/b/SKILL.md": "shown" },
      claude: { "/other/harness/SKILL.md": "hidden" },
    },
  })}\n`);

  await writeSkillVisibility([{ filePath: "/pi/a/SKILL.md", visibility: null }], agentDir);

  const parsed = JSON.parse(await readFile(settingsPath(agentDir), "utf8"));
  assert.deepEqual(parsed.skillVisibility.pi, { "/pi/b/SKILL.md": "shown" });
  assert.deepEqual(parsed.skillVisibility.claude, { "/other/harness/SKILL.md": "hidden" });
  assert.equal(readSkillVisibility(agentDir).get("/pi/a/SKILL.md"), undefined);
});

test("a file that is not a JSON object is refused, never overwritten", async () => {
  for (const [name, content] of [["array", "[1, 2]\n"], ["broken", "{oops\n"]]) {
    const agentDir = await makeAgentDir(name, content);
    await assert.rejects(
      writeSkillVisibility([{ filePath: "/pi/a/SKILL.md", visibility: "hidden" }], agentDir),
    );
    assert.equal(await readFile(settingsPath(agentDir), "utf8"), content);
  }
});

test("a missing, malformed or unknown store reads as no preference", async () => {
  assert.equal(readSkillVisibility(join(root, "no-such-agent-dir")).size, 0);
  const malformed = await makeAgentDir("malformed", "{oops\n");
  assert.equal(readSkillVisibility(malformed).size, 0);
  const partial = await makeAgentDir("partial", `${JSON.stringify({
    skillVisibility: { pi: {
      "/pi/ok/SKILL.md": "hidden",
      "/pi/weird/SKILL.md": "sometimes",
      7: "hidden",
    } },
  })}\n`);
  assert.deepEqual([...readSkillVisibility(partial)], [["/pi/ok/SKILL.md", "hidden"]]);
});

test("the tri-state rule: hidden hides, shown shows over the author, unset keeps the author", () => {
  const visibility = new Map([["/pi/hidden/SKILL.md", "hidden"], ["/pi/shown/SKILL.md", "shown"]]);
  assert.equal(effectiveDisableModelInvocation(false, visibility, "/pi/hidden/SKILL.md"), true);
  assert.equal(effectiveDisableModelInvocation(true, visibility, "/pi/shown/SKILL.md"), false);
  assert.equal(effectiveDisableModelInvocation(true, visibility, "/pi/untouched/SKILL.md"), true);
  assert.equal(effectiveDisableModelInvocation(false, visibility, "/pi/untouched/SKILL.md"), false);
});

test("the override applies the tri-state to a loader result and keeps untouched skills", () => {
  const base = {
    skills: [
      { name: "a", filePath: "/pi/hidden/SKILL.md", disableModelInvocation: false },
      { name: "b", filePath: "/pi/shown/SKILL.md", disableModelInvocation: true },
      { name: "c", filePath: "/pi/untouched/SKILL.md", disableModelInvocation: true },
    ],
    diagnostics: [],
  };
  const override = skillVisibilityOverride(new Map([
    ["/pi/hidden/SKILL.md", "hidden"],
    ["/pi/shown/SKILL.md", "shown"],
  ]));
  const result = override(base);
  assert.deepEqual(
    result.skills.map((skill) => [skill.name, skill.disableModelInvocation]),
    [["a", true], ["b", false], ["c", true]],
  );
  assert.equal(result.diagnostics, base.diagnostics);
  assert.equal(skillVisibilityOverride(new Map()), undefined);
});

test("composes two overrides in order, first then second", () => {
  const base = { skills: [{ name: "a" }], diagnostics: [] };
  const seen = [];
  const first = (received) => {
    seen.push(["first", received.skills[0].name]);
    return { ...received, skills: [{ name: `first:${received.skills[0].name}` }], diagnostics: received.diagnostics };
  };
  const second = (received) => {
    seen.push(["second", received.skills[0].name]);
    return received;
  };
  composeSkillsOverrides(first, second)(base);
  assert.deepEqual(seen, [["first", "a"], ["second", "first:a"]]);
  assert.equal(composeSkillsOverrides(), undefined);
  assert.equal(composeSkillsOverrides(undefined, second), second);
  assert.equal(composeSkillsOverrides(first, undefined), first);
});
