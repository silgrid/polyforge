// carries: lib/skills-service.ts, lib/skills-unified.ts, lib/api-types.ts, lib/skill-lock.ts, lib/project-trust.ts, lib/skill-visibility-config.ts, lib/skill-visibility-migration.ts
import "#test-env";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test, { after } from "node:test";
import { createJiti } from "jiti";

// The skills listing (Settings › Skills, the chat slash palette) loads no
// extensions (#1226): they add skills only through `resources_discover` at
// session_start, which a listing never runs. Every extension below counts its
// import, and the listing must still show global, package and project skills.
const root = await mkdtemp(join(tmpdir(), "pi-web-skills-service-"));
// A project's .agents/skills are looked up in every folder up to its git root,
// or the filesystem root outside a repository. On Windows tmpdir() lies inside
// the developer's profile, whose own ~/.agents/skills would join the listing:
// the fixture tree is made a repository root so the lookup ends there.
await mkdir(join(root, ".git"));
const agentDir = join(root, "agent");
const home = join(root, "home");
const pkg = join(root, "local-pkg");
const trustedProject = join(root, "trusted");
const untrustedProject = join(root, "untrusted");
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.HOME = home;
// Windows: os.homedir() reads %USERPROFILE%, not $HOME.
process.env.USERPROFILE = home;
process.env.PI_OFFLINE = "1";
// 85d u2: claude's half of the unified listing reads this config dir
// (claudeConfigDir(env) reads it at call time), and pi lists the same
// directory through its settings skillPaths so one real file shows on both
// sides — the realpath merge's live case.
const claudeConfig = join(root, "claude-config");
process.env.CLAUDE_CONFIG_DIR = claudeConfig;

const countedExtension = (name) => `
(globalThis as any).__piWebSkillsTestImports ??= [];
(globalThis as any).__piWebSkillsTestImports.push(${JSON.stringify(name)});
export default function (pi: any) {
  pi.on("resources_discover", () => ({ skillPaths: [${JSON.stringify(join(root, "discovered"))}] }));
}
`;

async function skill(dir, name) {
  await mkdir(join(dir, name), { recursive: true });
  await writeFile(join(dir, name, "SKILL.md"), `---\nname: ${name}\ndescription: ${name} skill\n---\nbody\n`);
}

await mkdir(home, { recursive: true });
await mkdir(join(agentDir, "extensions"), { recursive: true });
await writeFile(join(agentDir, "extensions", "global.ts"), countedExtension("global"));
await skill(join(agentDir, "skills"), "global-skill");
await skill(join(root, "discovered"), "discovered-skill");

await mkdir(join(pkg, "extensions"), { recursive: true });
await writeFile(join(pkg, "extensions", "index.ts"), countedExtension("package"));
await skill(join(pkg, "skills"), "package-skill");
await writeFile(join(pkg, "package.json"), JSON.stringify({
  name: "local-pkg",
  version: "1.0.0",
  pi: { skills: ["./skills"], extensions: ["./extensions"] },
}));
await writeFile(join(agentDir, "settings.json"), JSON.stringify({ packages: [pkg], skills: [join(claudeConfig, "skills", "shared-cl-skill")] }));

for (const project of [trustedProject, untrustedProject]) {
  await mkdir(join(project, ".pi", "extensions"), { recursive: true });
  await writeFile(join(project, ".pi", "extensions", "project.ts"), countedExtension(project));
  await skill(join(project, ".pi", "skills"), "project-pi-skill");
  await skill(join(project, ".agents", "skills"), "project-agents-skill");
  await skill(join(project, ".claude", "skills"), "project-cl-skill");
}

// 85d u2: claude's user half — a shared directory pi also lists through its
// settings skillPaths, and a claude-only skill with a stored native override.
await mkdir(join(claudeConfig, "skills", "shared-cl-skill"), { recursive: true });
await writeFile(join(claudeConfig, "skills", "shared-cl-skill", "SKILL.md"), "---\nname: shared-cl-skill\ndescription: shared skill\n---\nbody\n");
await mkdir(join(claudeConfig, "skills", "claude-only-skill"), { recursive: true });
await writeFile(join(claudeConfig, "skills", "claude-only-skill", "SKILL.md"), "---\nname: claude-only-skill\ndescription: claude only skill\n---\nbody\n");
await writeFile(join(claudeConfig, "settings.json"), JSON.stringify({
  skillOverrides: { "claude-only-skill": "name-only" },
}));

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { loadSkillsWithInstallInfo, loadUnifiedSkills } = await jiti.import("./skills-service.ts");
const { projectTrustReloadOptions, trustProject } = await jiti.import("./project-trust.ts");
const { DefaultResourceLoader } = await import("@earendil-works/pi-coding-agent");

trustProject(trustedProject, agentDir);

const imports = () => globalThis.__piWebSkillsTestImports ?? [];
const names = (skills) => skills.map((entry) => entry.name).sort();

after(async () => {
  await rm(root, { recursive: true, force: true });
});

test("the listing imports no extension and still lists global, package and trusted project skills", async () => {
  const listed = await loadSkillsWithInstallInfo(trustedProject);
  assert.deepEqual(imports(), []);
  assert.equal(listed.projectResourcesLoaded, true);
  assert.deepEqual(names(listed.skills), [
    "global-skill",
    "package-skill",
    "project-agents-skill",
    "project-pi-skill",
    "shared-cl-skill",
  ]);
});

test("an untrusted project's skills stay out, and its extensions are not imported", async () => {
  const listed = await loadSkillsWithInstallInfo(untrustedProject);
  assert.deepEqual(imports(), []);
  assert.equal(listed.projectResourcesLoaded, false);
  assert.ok(!names(listed.skills).includes("project-pi-skill"));
  assert.ok(names(listed.skills).includes("global-skill"));
});

test("loading the extensions would list exactly the same skills", async () => {
  const listed = await loadSkillsWithInstallInfo(trustedProject);
  const withExtensions = new DefaultResourceLoader({ cwd: trustedProject, agentDir });
  await withExtensions.reload(projectTrustReloadOptions(trustedProject, agentDir));
  assert.ok(imports().length > 0, "the full loader imports the extensions");
  const paths = (skills) => skills.map((entry) => [entry.name, entry.filePath]).sort();
  assert.deepEqual(paths(listed.skills), paths(withExtensions.getSkills().skills));
  // `resources_discover` runs only at session_start, which neither loader reaches.
  assert.ok(!names(listed.skills).includes("discovered-skill"));
});

const hash = (file) => createHash("sha256").update(readFileSync(file)).digest("hex");
const settingsFile = join(agentDir, "pi-web-settings.json");

async function writeAgentSkill(name, frontmatter = "") {
  const filePath = join(agentDir, "skills", name, "SKILL.md");
  await mkdir(join(agentDir, "skills", name), { recursive: true });
  await writeFile(filePath, `---\nname: ${name}\n${frontmatter}description: ${name} skill\n---\nbody\n`);
  return filePath;
}

async function storePreference(entries) {
  const store = JSON.parse(await readFile(settingsFile, "utf8"));
  store.skillVisibility ??= {};
  store.skillVisibility.pi ??= {};
  Object.assign(store.skillVisibility.pi, entries);
  await writeFile(settingsFile, JSON.stringify(store, null, 2));
}

test("the listing imports an author-hidden skill as a hidden preference and never edits the file", async () => {
  const legacy = await writeAgentSkill("legacy-author-hidden", "disable-model-invocation: true\n");
  await writeFile(settingsFile, `${JSON.stringify({ version: 1, agentLanguage: "zh-CN" }, null, 2)}\n`);
  const before = hash(legacy);

  const listed = await loadSkillsWithInstallInfo(trustedProject);
  const entry = listed.skills.find((skill) => skill.name === "legacy-author-hidden");

  assert.ok(entry, "the imported skill stays listed");
  assert.equal(entry.declaredDisableModelInvocation, true);
  assert.equal(entry.disableModelInvocation, true, "the import hides it, as its legacy line did");
  const store = JSON.parse(await readFile(settingsFile, "utf8"));
  assert.equal(store.skillVisibility.pi[legacy], "hidden");
  assert.equal(store.agentLanguage, "zh-CN", "the store keeps its other keys");
  assert.equal(hash(legacy), before, "the SKILL.md is never edited");
});

test("a stored preference wins: shown overrides the author, hidden hides an author-visible skill", async () => {
  const authorHidden = await writeAgentSkill("author-hidden-shown", "disable-model-invocation: true\n");
  const plain = await writeAgentSkill("pref-hidden");
  await storePreference({ [authorHidden]: "shown", [plain]: "hidden" });

  const listed = await loadSkillsWithInstallInfo(trustedProject);
  const byName = new Map(listed.skills.map((skill) => [skill.name, skill]));

  assert.equal(byName.get("author-hidden-shown").declaredDisableModelInvocation, true);
  assert.equal(byName.get("author-hidden-shown").disableModelInvocation, false, "shown overrides the author's line");
  assert.equal(byName.get("pref-hidden").declaredDisableModelInvocation, false);
  assert.equal(byName.get("pref-hidden").disableModelInvocation, true, "hidden hides an author-visible skill");
  assert.equal(byName.get("legacy-author-hidden").disableModelInvocation, true, "the earlier import still holds");
  assert.equal(byName.get("global-skill").disableModelInvocation, false, "an untouched skill keeps the author's value");
});

// ── 85d u2: the unified listing merges both sides (AC1) and answers the
// no-project mode with the user scope alone (AC3) ─────────────────────
test("the unified listing merges pi and claude into user and project rows (AC1)", async () => {
  const unified = await loadUnifiedSkills(trustedProject);
  const rows = unified.rows;
  const find = (name, scope) => rows.find((row) => row.name === name && row.scope === scope);

  // both sides present, grouped 用户 / 项目
  assert.ok(find("global-skill", "user"), "pi's user skill is listed");
  assert.ok(find("claude-only-skill", "user"), "claude's user skill is listed");
  assert.ok(find("project-cl-skill", "project"), "claude's project skill is listed");
  assert.ok(find("project-pi-skill", "project"), "pi's project skill is listed");

  // one real file both harnesses list is one row with both labels (Q2 B)
  const shared = find("shared-cl-skill", "user");
  assert.ok(shared, "the shared skill is listed");
  assert.deepEqual(shared.harnesses, ["pi", "claude"]);
  assert.equal(shared.claude.name, "shared-cl-skill");

  // claude's native override state rides its side untouched
  assert.equal(find("claude-only-skill", "user").claude.state, "name-only");
  assert.deepEqual(find("claude-only-skill", "user").harnesses, ["claude"]);

  // the untrusted-project listing keeps its shape: no claude project rows for a cwd it never saw
  const untrusted = await loadUnifiedSkills(untrustedProject);
  assert.ok(!untrusted.rows.some((row) => row.name === "project-cl-skill"));
});

test("without a project the unified listing answers the user scope alone (AC3)", async () => {
  const unified = await loadUnifiedSkills(null);
  assert.ok(unified.rows.length > 0, "the user scope has rows");
  for (const row of unified.rows) {
    assert.equal(row.scope, "user", `row ${row.name} must be user scope`);
  }
  assert.ok(unified.rows.some((row) => row.name === "shared-cl-skill" && row.harnesses.includes("claude")));
  assert.ok(!unified.rows.some((row) => row.name === "project-pi-skill" || row.name === "project-cl-skill"));
  assert.equal(unified.projectResourcesLoaded, true);
});
