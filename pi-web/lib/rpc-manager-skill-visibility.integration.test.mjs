import "#test-env";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test, { after } from "node:test";
import { fauxAssistantMessage, fauxProvider, fauxText } from "@earendil-works/pi-ai";
import { DefaultResourceLoader, SettingsManager } from "@earendil-works/pi-coding-agent";
import { createJiti } from "jiti";

// AC2's session behavior through the real session path: startRpcSession (the
// one every browser session goes through) must hide a preference-hidden skill
// from the system prompt while /skill:name keeps working, a「shown」preference
// must override the author's frontmatter line, and a later switch must reach
// the next session. The named-skills projection composition stays pinned at
// the loader level beside it.
const root = await mkdtemp(join(tmpdir(), "pi-web-skill-visibility-rpc-"));
const previousAgentDir = process.env.PI_CODING_AGENT_DIR;
after(async () => {
  if (previousAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = previousAgentDir;
  await rm(root, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { interopDefault: true, moduleCache: false });
const { startRpcSession } = await jiti.import("./rpc-manager.ts");
const { writeSkillVisibility } = await jiti.import("./skill-visibility-config.ts");
const { createSubagentSkillsBinding } = await jiti.import("./subagent-skills.ts");

/** One fixture: hidden-skill (preference hidden), author-skill (author line, preference shown), visible-skill. */
async function freshFixture(name) {
  const agentDir = join(root, `agent-${name}`);
  const cwd = join(root, `cwd-${name}`);
  process.env.PI_CODING_AGENT_DIR = agentDir;
  await mkdir(join(cwd), { recursive: true });
  const paths = {};
  for (const [skill, frontmatter, body] of [
    ["hidden-skill", "", "HIDDEN_SKILL_BODY"],
    ["author-skill", "disable-model-invocation: true\n", "AUTHOR_SKILL_BODY"],
    ["visible-skill", "", "VISIBLE_SKILL_BODY"],
  ]) {
    paths[skill] = join(agentDir, "skills", skill, "SKILL.md");
    await mkdir(join(agentDir, "skills", skill), { recursive: true });
    await writeFile(paths[skill], `---\nname: ${skill}\n${frontmatter}description: ${skill}\n---\n\n${body}\n`);
  }
  await writeSkillVisibility([
    { filePath: paths["hidden-skill"], visibility: "hidden" },
    { filePath: paths["author-skill"], visibility: "shown" },
  ], agentDir);
  return { agentDir, cwd, paths };
}

const messageText = (message) =>
  typeof message.content === "string" ? message.content : JSON.stringify(message.content ?? "");

test("a startRpcSession session hides a preference-hidden skill while /skill:name keeps working", async (t) => {
  const { cwd } = await freshFixture("prompt");
  const { session } = await startRpcSession("skill-visibility-rpc-prompt", "", cwd, {});
  t.after(() => void session.shutdown());

  const state = await session.send({ type: "get_state" });
  assert.match(state.systemPrompt, /<available_skills>/);
  assert.match(state.systemPrompt, /visible-skill/);
  assert.match(state.systemPrompt, /author-skill/, "a shown preference overrides the author's hidden line");
  assert.doesNotMatch(state.systemPrompt, /hidden-skill/, "a hidden preference keeps the skill out of the prompt");

  // Manually invocable: the command list (the browser palette's source) keeps it.
  const commands = await session.send({ type: "get_commands" });
  assert.ok(commands.commands.some((command) => command.name === "skill:hidden-skill"));

  // And the command expands: a faux model receives the skill's body.
  const faux = fauxProvider({ models: [{ id: "faux-skill-visibility" }] });
  session.inner.modelRuntime.registerNativeProvider(faux.provider);
  await session.send({ type: "set_model", provider: faux.provider.id, modelId: "faux-skill-visibility" });
  faux.setResponses([fauxAssistantMessage([fauxText("done")])]);
  await session.send({ type: "prompt", message: "/skill:hidden-skill" });
  // The run settles asynchronously: poll the transcript against a time
  // deadline, never read it once (r2#8).
  const deadline = Date.now() + 10_000;
  while (session.inner.messages.length === 0 && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  const lastUser = [...session.inner.messages].reverse().find((message) => message.role === "user");
  assert.ok(lastUser, "the prompt produced a user message");
  assert.match(messageText(lastUser), /HIDDEN_SKILL_BODY/, "the skill command expanded to the skill's body");
});

test("switching the preference back shows the skill in the next startRpcSession session", async (t) => {
  const { cwd, paths } = await freshFixture("restore");

  const before = await startRpcSession("skill-visibility-rpc-before", "", cwd, {});
  t.after(() => void before.session.shutdown());
  const beforeState = await before.session.send({ type: "get_state" });
  assert.doesNotMatch(beforeState.systemPrompt, /hidden-skill/, "the hidden preference hides it");

  await writeSkillVisibility([{ filePath: paths["hidden-skill"], visibility: "shown" }]);

  const after = await startRpcSession("skill-visibility-rpc-after", "", cwd, {});
  t.after(() => void after.session.shutdown());
  const afterState = await after.session.send({ type: "get_state" });
  assert.match(afterState.systemPrompt, /hidden-skill/);
});

test("a subagent session reopened through startRpcSession hides the preference-hidden skill", async (t) => {
  const { cwd } = await freshFixture("subagent");
  // A persisted subagent session in the shape a hidden preference must reach:
  // append mode, load_skills: true, no named list — the catalog is its skills.
  const sessionFile = join(root, "subagent-child.jsonl");
  const header = { type: "session", version: 3, id: "skill-visibility-subagent-child", timestamp: new Date().toISOString(), cwd };
  const snapshot = { type: "custom", customType: "pi-web:subagent", data: {
    version: 1, parentSessionId: "skill-visibility-parent", parentSessionPath: join(root, "parent.jsonl"),
    parentToolCallId: "toolu_subagent", profile: "scoped", description: "subagent", task: "work",
    runInBackground: false, createdAt: new Date().toISOString(),
    resourceSnapshot: { version: 1, appendSystemPrompt: [], tools: ["read"], loadSkills: true, loadExtensions: false },
  } };
  await writeFile(sessionFile, `${JSON.stringify(header)}\n${JSON.stringify(snapshot)}\n`);
  t.after(() => rm(sessionFile, { force: true }));

  const { session } = await startRpcSession("skill-visibility-subagent-child", sessionFile);
  t.after(() => void session.shutdown());

  const state = await session.send({ type: "get_state" });
  assert.match(state.systemPrompt, /<available_skills>/);
  assert.match(state.systemPrompt, /visible-skill/);
  assert.doesNotMatch(state.systemPrompt, /hidden-skill/, "a hidden preference leaves a subagent's catalog too");
});

test("the named-skills projection composes after the preference at the loader level", async (t) => {
  const { agentDir, cwd, paths } = await freshFixture("named");
  const binding = createSubagentSkillsBinding({ loadSkills: true, skills: ["hidden-skill"] });
  const { sessionSkillsOverride } = await jiti.import("./rpc-manager.ts");
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
