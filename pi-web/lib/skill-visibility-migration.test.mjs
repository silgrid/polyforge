import "#test-env";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test, { after } from "node:test";
import { createJiti } from "jiti";

// Legacy `disable-model-invocation: true` handling (owner Q3 A+B): the import
// turns every author-hidden skill with no stored preference into a `hidden`
// preference and never touches a file; the cleanup is per-skill and writes the
// preference before it removes the line, so an edit that cannot be made
// surgically still leaves the preference in place. Every test gets its own
// agent dir, so the preference store starts empty.
const root = await mkdtemp(join(tmpdir(), "pi-web-skill-visibility-migration-"));
after(async () => {
  await rm(root, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, moduleCache: false });
const { importLegacySkillVisibility, cleanupLegacyDisableModelInvocation } =
  await jiti.import("./skill-visibility-migration.ts");

async function freshAgentDir(name) {
  const agentDir = join(root, `agent-${name}`);
  await mkdir(join(agentDir, "skills"), { recursive: true });
  return agentDir;
}

const readPreference = async (agentDir) => {
  try {
    return JSON.parse(await readFile(join(agentDir, "pi-web-settings.json"), "utf8")).skillVisibility?.pi ?? {};
  } catch {
    return {};
  }
};
const hash = async (file) => createHash("sha256").update(await readFile(file)).digest("hex");

async function writeSkill(agentDir, name, frontmatter = "") {
  const filePath = join(agentDir, "skills", name, "SKILL.md");
  await mkdir(join(agentDir, "skills", name), { recursive: true });
  await writeFile(filePath, `---\nname: ${name}\n${frontmatter}description: ${name} skill\n---\n\n# ${name}\n`);
  return filePath;
}

test("the import turns author-hidden skills into hidden preferences and touches no file", async () => {
  const agentDir = await freshAgentDir("import");
  const declaredHidden = await writeSkill(agentDir, "declared-hidden", "disable-model-invocation: true\n");
  const declaredVisible = await writeSkill(agentDir, "declared-visible");
  const explicitFalse = await writeSkill(agentDir, "explicit-false", "disable-model-invocation: false\n");
  const prefShown = await writeSkill(agentDir, "pref-shown", "disable-model-invocation: true\n");
  await writeFile(join(agentDir, "pi-web-settings.json"), JSON.stringify({
    skillVisibility: { pi: { [prefShown]: "shown" } },
  }));
  const files = [declaredHidden, declaredVisible, explicitFalse, prefShown];
  const before = await Promise.all(files.map(hash));

  const visibility = await importLegacySkillVisibility([
    { filePath: declaredHidden, disableModelInvocation: true },
    { filePath: declaredVisible, disableModelInvocation: false },
    { filePath: explicitFalse, disableModelInvocation: false },
    { filePath: prefShown, disableModelInvocation: true },
  ], agentDir);

  assert.deepEqual([...visibility], [[prefShown, "shown"], [declaredHidden, "hidden"]]);
  assert.deepEqual(await readPreference(agentDir), {
    [prefShown]: "shown",
    [declaredHidden]: "hidden",
  });
  assert.deepEqual(await Promise.all(files.map(hash)), before);
});

test("importing again writes nothing the second time", async () => {
  const agentDir = await freshAgentDir("idempotent");
  const skill = await writeSkill(agentDir, "idempotent", "disable-model-invocation: true\n");
  await importLegacySkillVisibility([{ filePath: skill, disableModelInvocation: true }], agentDir);
  const settingsFile = join(agentDir, "pi-web-settings.json");
  const content = await readFile(settingsFile, "utf8");
  const mtime = (await stat(settingsFile)).mtimeMs;

  const again = await importLegacySkillVisibility([{ filePath: skill, disableModelInvocation: true }], agentDir);

  assert.deepEqual([...again], [[skill, "hidden"]]);
  assert.equal(await readFile(settingsFile, "utf8"), content);
  assert.equal((await stat(settingsFile)).mtimeMs, mtime);
});

test("the cleanup writes the hidden preference, then removes the line surgically", async () => {
  const agentDir = await freshAgentDir("cleanup");
  const skill = await writeSkill(agentDir, "cleanup-target", "disable-model-invocation: true\n");
  const result = await cleanupLegacyDisableModelInvocation(skill, agentDir);

  assert.deepEqual(result, { removed: true });
  assert.deepEqual(await readPreference(agentDir), { [skill]: "hidden" });
  const content = await readFile(skill, "utf8");
  assert.doesNotMatch(content, /disable-model-invocation/);
  // The surgical edit keeps every other line exactly as the author wrote it.
  assert.equal(
    content,
    `---\nname: cleanup-target\ndescription: cleanup-target skill\n---\n\n# cleanup-target\n`,
  );
});

test("a line the surgical editor refuses still leaves the preference written first", async () => {
  const agentDir = await freshAgentDir("flow");
  const skill = join(agentDir, "skills", "flow", "SKILL.md");
  await mkdir(join(agentDir, "skills", "flow"), { recursive: true });
  await writeFile(skill, "---\n{ name: flow, disable-model-invocation: true }\n---\nbody\n");
  const before = await hash(skill);

  await assert.rejects(cleanupLegacyDisableModelInvocation(skill, agentDir), /unsupported frontmatter formatting/);
  assert.deepEqual(await readPreference(agentDir), { [skill]: "hidden" }, "the preference went in before the edit");
  assert.equal(await hash(skill), before);
});

test("a file without the legacy line is left alone and gets no preference", async () => {
  const agentDir = await freshAgentDir("clean");
  const skill = await writeSkill(agentDir, "already-clean");
  const before = await hash(skill);

  const result = await cleanupLegacyDisableModelInvocation(skill, agentDir);

  assert.deepEqual(result, { removed: false });
  assert.equal(await hash(skill), before);
  assert.deepEqual(await readPreference(agentDir), {});
});
