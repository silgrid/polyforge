import "#test-env";
import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { DefaultResourceLoader, SettingsManager } from "@earendil-works/pi-coding-agent";
import { createJiti } from "jiti";
const { createSubagentSkillsBinding } = await createJiti(import.meta.url).import("./subagent-skills.ts");

// Off is pi's `noSkills`: like `pi --no-skills`, it still keeps the skills an extension provides.
test("explicit disable keeps extension-contributed skills, as pi's noSkills does", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "skill-extension-off-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const file = join(dir, "EXTRA.md");
  await writeFile(file, "---\nname: extra\ndescription: extra routing\n---\nEXTRA_BODY");
  const binding = createSubagentSkillsBinding({ loadSkills: false, skills: ["extra"] });
  const loader = new DefaultResourceLoader({ cwd: dir, agentDir: dir, settingsManager: SettingsManager.inMemory(), noExtensions: true, ...binding.loaderOptions });
  await loader.reload();
  loader.extendResources({ skillPaths: [{ path: file, metadata: { source: "extension-test", origin: "extension", scope: "user" } }] });
  assert.deepEqual(loader.getSkills().skills.map((skill) => skill.name), ["extra"]);
});

test("named discovery preserves SDK diagnostics while hiding collision winners privately", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "skill-diagnostics-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  for (const folder of ["first", "second"]) {
    await mkdir(join(dir, "skills", folder), { recursive: true });
    await writeFile(join(dir, "skills", folder, "SKILL.md"), `---\nname: same\ndescription: routing\n---\n${folder}`);
  }
  const ordinary = new DefaultResourceLoader({ cwd: dir, agentDir: dir, settingsManager: SettingsManager.inMemory(), noExtensions: true });
  await ordinary.reload();
  const binding = createSubagentSkillsBinding({ loadSkills: true, skills: ["same"] });
  const scoped = new DefaultResourceLoader({ cwd: dir, agentDir: dir, settingsManager: SettingsManager.inMemory(), noExtensions: true, ...binding.loaderOptions });
  await scoped.reload();
  assert.ok(ordinary.getSkills().diagnostics.length > 0);
  assert.deepEqual(scoped.getSkills().diagnostics, ordinary.getSkills().diagnostics);
  assert.deepEqual(scoped.getSkills().skills, []);
});

const INSTRUCTION = "Use Simplified Chinese (简体中文) for replies, narration and reports. Keep code, code comments, commit messages, files you write, technical terms and identifiers as they are, or as the project's conventions require. Thinking in Chinese is best effort only.";

test("an exact subagent prompt is followed by the Agent language instruction for 中文 only, composed once", async () => {
  const run = async (options) => {
    const binding = createSubagentSkillsBinding(options);
    const handlers = [];
    for (const factory of binding.loaderOptions.extensionFactories ?? []) {
      (factory.factory ?? factory)({ on: (event, handler) => handlers.push([event, handler]) });
    }
    const results = [];
    for (let index = 0; index < 2; index += 1) {
      for (const [, handler] of handlers) results.push(await handler({ systemPrompt: "PI PROMPT" }));
    }
    return { binding, results };
  };
  const zh = await run({ loadSkills: false, exactSystemPrompt: "EXACT", agentLanguage: "zh-CN" });
  assert.equal(zh.binding.getExactSystemPrompt(), `EXACT\n\n${INSTRUCTION}`);
  assert.deepEqual(zh.results, [{ systemPrompt: `EXACT\n\n${INSTRUCTION}` }, { systemPrompt: `EXACT\n\n${INSTRUCTION}` }]);
  const en = await run({ loadSkills: false, exactSystemPrompt: "EXACT", agentLanguage: "en" });
  assert.equal(en.binding.getExactSystemPrompt(), "EXACT");
  assert.deepEqual(en.results, [{ systemPrompt: "EXACT" }, { systemPrompt: "EXACT" }]);
  // Append mode carries the instruction in appendSystemPrompt: the binding adds nothing.
  const append = await run({ loadSkills: false, agentLanguage: "zh-CN" });
  assert.deepEqual(append.results, []);
  assert.equal(append.binding.getExactSystemPrompt, undefined);
});
