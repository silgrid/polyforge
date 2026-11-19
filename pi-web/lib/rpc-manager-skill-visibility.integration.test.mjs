import "#test-env";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test, { after } from "node:test";
import { fauxAssistantMessage, fauxProvider, fauxText } from "@earendil-works/pi-ai";
import {
  createAgentSession,
  DefaultResourceLoader,
  ModelRuntime,
  SessionManager,
  SettingsManager,
} from "@earendil-works/pi-coding-agent";
import { createJiti } from "jiti";

// A session built the way rpc-manager builds one: pi-web's skill visibility
// preference (sessionSkillsOverride) hides a skill from the system prompt while
// /skill:name keeps working, a "shown" preference overrides the author's
// frontmatter, a later switch reaches the next session, and the subagent
// named-skills projection still loads the hidden skill by name.
const agentDir = await mkdtemp(join(tmpdir(), "pi-web-skill-visibility-agent-"));
process.env.PI_CODING_AGENT_DIR = agentDir;

const jiti = createJiti(import.meta.url, { interopDefault: true, moduleCache: false });
const { sessionSkillsOverride } = await jiti.import("./rpc-manager.ts");
const { writeSkillVisibility } = await jiti.import("./skill-visibility-config.ts");
const { createSubagentSkillsBinding } = await jiti.import("./subagent-skills.ts");

const hiddenSkill = join(agentDir, "skills", "hidden-skill", "SKILL.md");
const authorSkill = join(agentDir, "skills", "author-skill", "SKILL.md");
const visibleSkill = join(agentDir, "skills", "visible-skill", "SKILL.md");
for (const [filePath, frontmatter, name] of [
  [hiddenSkill, "", "hidden-skill"],
  [authorSkill, "disable-model-invocation: true\n", "author-skill"],
  [visibleSkill, "", "visible-skill"],
]) {
  await mkdir(join(agentDir, "skills", name), { recursive: true });
  await writeFile(filePath, `---\nname: ${name}\n${frontmatter}description: ${name}\n---\n\n${name.toUpperCase()}_BODY\n`);
}
await writeSkillVisibility([
  { filePath: hiddenSkill, visibility: "hidden" },
  { filePath: authorSkill, visibility: "shown" },
], agentDir);

after(async () => {
  await rm(agentDir, { recursive: true, force: true });
});

const faux = fauxProvider({ models: [{ id: "faux-skill-visibility" }] });
let modelRuntime;

test.before(async () => {
  modelRuntime = await ModelRuntime.create({
    authPath: join(agentDir, "auth.json"),
    modelsPath: null,
    refreshOnCreate: false,
  });
  modelRuntime.registerNativeProvider(faux.provider);
});

async function startSession(t) {
  const cwd = await mkdtemp(join(tmpdir(), "pi-web-skill-visibility-cwd-"));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  const override = sessionSkillsOverride(agentDir);
  const resourceLoader = new DefaultResourceLoader({
    cwd,
    agentDir,
    settingsManager: SettingsManager.inMemory(),
    ...(override ? { skillsOverride: override } : {}),
  });
  await resourceLoader.reload();
  const { session } = await createAgentSession({
    cwd,
    agentDir,
    modelRuntime,
    model: faux.getModel("faux-skill-visibility"),
    sessionManager: SessionManager.inMemory(cwd),
    settingsManager: SettingsManager.inMemory(),
    resourceLoader,
  });
  return { session, resourceLoader };
}

const messageText = (message) =>
  typeof message.content === "string" ? message.content : JSON.stringify(message.content ?? "");

test("a hidden skill stays out of the prompt while /skill:name still invokes it", async (t) => {
  const { session, resourceLoader } = await startSession(t);

  const prompt = session.systemPrompt;
  assert.match(prompt, /<available_skills>/);
  assert.match(prompt, /visible-skill/);
  assert.doesNotMatch(prompt, /hidden-skill/, "a hidden preference keeps the skill out of the prompt");
  assert.match(prompt, /author-skill/, "a shown preference overrides the author's hidden line");

  // Still manually invocable: the loader's table — the source of the skill:
  // command list — keeps the skill, and the command expands its body.
  const listed = resourceLoader.getSkills().skills.find((skill) => skill.name === "hidden-skill");
  assert.equal(listed.disableModelInvocation, true);
  faux.setResponses([fauxAssistantMessage([fauxText("done")])]);
  await session.prompt("/skill:hidden-skill");
  const userMessages = session.messages.filter((message) => message.role === "user");
  assert.match(
    messageText(userMessages.at(-1)),
    /HIDDEN_SKILL_BODY/,
    "the skill command expanded to the skill's body",
  );
});

test("the named-skills subagent projection is unaffected, and the hidden skill is still nameable", async (t) => {
  const cwd = await mkdtemp(join(tmpdir(), "pi-web-skill-visibility-named-"));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  const binding = createSubagentSkillsBinding({ loadSkills: true, skills: ["hidden-skill"] });
  const composed = sessionSkillsOverride(agentDir, binding.loaderOptions.skillsOverride);
  const resourceLoader = new DefaultResourceLoader({
    cwd,
    agentDir,
    settingsManager: SettingsManager.inMemory(),
    noExtensions: true,
    ...binding.loaderOptions,
    ...(composed ? { skillsOverride: composed } : {}),
  });
  await resourceLoader.reload();
  assert.deepEqual(resourceLoader.getSkills().skills, [], "the named projection still hides the catalog");

  const handlers = [];
  for (const factory of binding.loaderOptions.extensionFactories ?? []) {
    (factory.factory ?? factory)({ on: (event, handler) => handlers.push([event, handler]) });
  }
  const results = [];
  for (const [, handler] of handlers) results.push(await handler({ systemPrompt: "PI PROMPT" }));
  assert.ok(
    results.some((result) => /HIDDEN_SKILL_BODY/.test(result?.systemPrompt ?? "")),
    "a preference-hidden skill still loads by name",
  );
});

test("switching the preference back shows the skill in the next session", async (t) => {
  const before = await startSession(t);
  assert.doesNotMatch(before.session.systemPrompt, /hidden-skill/);

  await writeSkillVisibility([{ filePath: hiddenSkill, visibility: "shown" }], agentDir);

  const after = await startSession(t);
  assert.match(after.session.systemPrompt, /hidden-skill/);
});
