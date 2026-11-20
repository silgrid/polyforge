// carries: app/api/skills/route.ts, app/api/skills/content/route.ts, app/api/skills/check/route.ts, app/api/skills/update/route.ts, lib/skills-service.ts, lib/skills-unified.ts, lib/api-types.ts, lib/skill-file-access.ts, lib/skill-visibility-config.ts, lib/harness/claude.ts, lib/harness/claude-native-write.ts, lib/claude-shared.ts
import "#test-env";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test, { after } from "node:test";
import { createJiti } from "jiti";

// 85d: the switch is per harness (AC2) — { skill, harness, state } for one
// row, { items: [...] } for a batch. A pi item writes pi-web's own preference
// store (`skillVisibility.pi` in `<agentDir>/pi-web-settings.json`), a claude
// item writes claude's native `skillOverrides` (user scope:
// $CLAUDE_CONFIG_DIR/settings.json, project scope: .claude/settings.local.json
// of the request's cwd) through the adapter's version-gated write. Never a
// SKILL.md: every `*.md` in the fixture tree must stay byte-identical
// (SHA-256) across every toggle, and a write to one harness must leave the
// other harness's store untouched. Out-of-bounds paths stay 403, malformed
// input stays 400, and each batch item is reported on its own.
// Every test gets its own agent dir, so the preference store starts empty.
const root = await mkdtemp(join(tmpdir(), "pi-web-skills-route-"));
const previousAgentDir = process.env.PI_CODING_AGENT_DIR;
process.env.PI_CODING_AGENT_DIR = join(root, "agent");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() } });
const { PATCH, GET } = await jiti.import("./route.ts");

/** A stub `claude` answering initialize with the given version (the unit claude-stub). */
async function installVersionedStub(name, version) {
  const { mkdirSync, writeFileSync } = await import("node:fs");
  const dir = join(root, name);
  mkdirSync(dir, { recursive: true });
  const binary = join(dir, "claude");
  const here = new URL(".", import.meta.url).pathname;
  writeFileSync(binary, `#!/bin/sh
export PI_TEST_CLAUDE_STUB_VERSION=${version}
exec node ${join(here, "..", "..", "..", "lib", "__fixtures__", "claude-stub", "claude.mjs")} "$@"
`, { mode: 0o755 });
  return dir;
}

/** Puts the stub dir first on PATH and returns the restore function. */
const putOnPath = (dir) => {
  const previous = process.env.PATH;
  process.env.PATH = `${dir}${process.platform === "win32" ? ";" : ":"}${previous}`;
  return () => { process.env.PATH = previous; };
};

after(async () => {
  if (previousAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = previousAgentDir;
  await rm(root, { recursive: true, force: true });
});

async function freshAgentDir(name) {
  const agentDir = join(root, `agent-${name}`);
  process.env.PI_CODING_AGENT_DIR = agentDir;
  await mkdir(join(agentDir, "skills"), { recursive: true });
  return agentDir;
}

async function writeSkill(dir, name, frontmatter = "") {
  const filePath = join(dir, name, "SKILL.md");
  await mkdir(join(dir, name), { recursive: true });
  await writeFile(filePath, `---\nname: ${name}\n${frontmatter}description: ${name} skill\n---\n\n# ${name}\n`);
  return filePath;
}

async function mdHashes(dir) {
  const hashes = new Map();
  const walk = async (current) => {
    for (const entry of await readdir(current, { withFileTypes: true })) {
      const target = join(current, entry.name);
      if (entry.isDirectory()) await walk(target);
      else if (entry.isFile() && entry.name.endsWith(".md")) {
        hashes.set(target, createHash("sha256").update(await readFile(target)).digest("hex"));
      }
    }
  };
  await walk(dir);
  return hashes;
}

const readPreference = async (agentDir) => {
  try {
    return JSON.parse(await readFile(join(agentDir, "pi-web-settings.json"), "utf8")).skillVisibility?.pi ?? {};
  } catch {
    return {};
  }
};

function patchSkills(body, { cwd } = {}) {
  const url = new URL("http://localhost/api/skills");
  if (cwd) url.searchParams.set("cwd", cwd);
  return PATCH(new Request(url, {
    method: "PATCH",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  }));
}

function getSkills({ cwd } = {}) {
  const url = new URL("http://localhost/api/skills");
  if (cwd) url.searchParams.set("cwd", cwd);
  return GET(new Request(url));
}

test("GET without a cwd answers the user scope alone, both harnesses listed (AC3, AC1)", async () => {
  const agentDir = await freshAgentDir("get");
  await writeSkill(join(agentDir, "skills"), "user-pi-skill");
  const claudeConfig = join(root, "get-claude-config");
  process.env.CLAUDE_CONFIG_DIR = claudeConfig;
  await mkdir(join(claudeConfig, "skills", "user-cl-skill"), { recursive: true });
  await writeFile(join(claudeConfig, "skills", "user-cl-skill", "SKILL.md"), "---\nname: user-cl-skill\ndescription: claude skill\n---\nbody\n");

  const response = await getSkills();
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.ok(payload.rows.length > 0, "the user scope has rows");
  for (const row of payload.rows) {
    assert.equal(row.scope, "user", `row ${row.name} must be user scope`);
  }
  const names = payload.rows.map((row) => row.name);
  assert.ok(names.includes("user-pi-skill"));
  assert.ok(names.includes("user-cl-skill"));

  // a cwd the file access refuses stays 403, and a missing cwd is no longer a 400
  const denied = await getSkills({ cwd: join(root, "no-such-dir") });
  assert.equal(denied.status, 403);
});

test("a pi item writes the preference alone; every SKILL.md stays byte-identical, claude's two settings layers untouched (AC2, review r1#4)", async () => {
  const agentDir = await freshAgentDir("pi-item");
  const skillsDir = join(agentDir, "skills");
  const visible = await writeSkill(skillsDir, "visible");
  const hidden = await writeSkill(skillsDir, "hidden", "disable-model-invocation: true\n");
  const outside = await writeSkill(join(root, "outside"), "outside");
  const missing = join(skillsDir, "missing", "SKILL.md");
  // claude's own two layers a pi item must never touch (review r1#4): the
  // user settings with an existing override, and a project's settings.local.
  const claudeConfig = join(root, "pi-item-claude-config");
  process.env.CLAUDE_CONFIG_DIR = claudeConfig;
  await mkdir(join(claudeConfig, "skills", "cl-skill"), { recursive: true });
  const userSettings = join(claudeConfig, "settings.json");
  await writeFile(userSettings, JSON.stringify({ effortLevel: "low", skillOverrides: { "cl-skill": "name-only" } }));
  const piProject = join(root, "pi-item-project");
  await mkdir(join(piProject, ".claude", "skills", "cl-skill"), { recursive: true });
  const localSettings = join(piProject, ".claude", "settings.local.json");
  await writeFile(localSettings, JSON.stringify({ effortLevel: "low" }));
  const userSettingsBefore = await readFile(userSettings, "utf8");
  const localSettingsBefore = await readFile(localSettings, "utf8");
  const before = await mdHashes(root);

  const single = await patchSkills({ skill: visible, harness: "pi", state: "disabled" }, { cwd: piProject });
  assert.equal(single.status, 200);
  assert.deepEqual(await single.json(), { success: true });
  assert.deepEqual(await readPreference(agentDir), { [visible]: "hidden" });

  const batch = await patchSkills({ items: [
    { skill: visible, harness: "pi", state: "enabled" },
    { skill: hidden, harness: "pi", state: "disabled" },
    { skill: outside, harness: "pi", state: "disabled" },
    { skill: missing, harness: "pi", state: "disabled" },
    { skill: visible, harness: "pi", state: "enabled" },
  ] });
  assert.equal(batch.status, 200);
  assert.deepEqual(await batch.json(), {
    results: [
      { skill: visible, harness: "pi" },
      { skill: hidden, harness: "pi" },
      { skill: outside, harness: "pi", error: "Access denied" },
      { skill: missing, harness: "pi", error: "file not found" },
    ],
  });
  assert.deepEqual(await mdHashes(root), before, "no SKILL.md may change by a single byte");
  assert.deepEqual(await readPreference(agentDir), {
    [visible]: "shown",
    [hidden]: "hidden",
  });
  assert.equal(await readFile(userSettings, "utf8"), userSettingsBefore, "a pi item never writes claude's user settings.json");
  assert.equal(await readFile(localSettings, "utf8"), localSettingsBefore, "a pi item never writes the project's .claude/settings.local.json");

  // shown over the author's frontmatter, the file never parsed or edited
  const shown = await patchSkills({ skill: hidden, harness: "pi", state: "enabled" }, { cwd: piProject });
  assert.equal(shown.status, 200);
  assert.deepEqual(await readPreference(agentDir), {
    [visible]: "shown",
    [hidden]: "shown",
  });
  assert.deepEqual(await mdHashes(root), before);
  assert.equal(await readFile(userSettings, "utf8"), userSettingsBefore);
  assert.equal(await readFile(localSettings, "utf8"), localSettingsBefore);
});

test("a claude item writes only claude's own settings.json, the pi store untouched (AC2)", async () => {
  const agentDir = await freshAgentDir("claude-item");
  const skillFile = await writeSkill(join(agentDir, "skills"), "untouched-pi-skill");
  const claudeConfig = join(root, "claude-item-config");
  process.env.CLAUDE_CONFIG_DIR = claudeConfig;
  await mkdir(join(claudeConfig, "skills", "cl-skill"), { recursive: true });
  const clSkill = join(claudeConfig, "skills", "cl-skill", "SKILL.md");
  await writeFile(clSkill, "---\nname: cl-skill\ndescription: claude skill\n---\nbody\n");
  await writeFile(join(claudeConfig, "settings.json"), JSON.stringify({
    effortLevel: "low",
    skillOverrides: { "other-skill": "disabled" },
  }));
  const restorePath = putOnPath(await installVersionedStub("claude-ok-bin", "2.1.326"));
  const before = await mdHashes(root);

  try {
    const response = await patchSkills({ skill: "cl-skill", harness: "claude", state: "user-invocable-only" });
    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), { success: true });

    const settings = JSON.parse(await readFile(join(claudeConfig, "settings.json"), "utf8"));
    assert.equal(settings.effortLevel, "low", "the file's other keys stay");
    assert.equal(settings.skillOverrides["other-skill"], "disabled", "the other override stays");
    assert.equal(settings.skillOverrides["cl-skill"], "user-invocable-only");
    assert.deepEqual(await readPreference(agentDir), {}, "the pi store stays untouched");
    assert.deepEqual(await mdHashes(root), before, "no SKILL.md may change by a single byte");
  } finally {
    restorePath();
  }
  assert.equal((await readFile(clSkill, "utf8")).includes("cl-skill"), true);
});

test("a claude project item writes the cwd's .claude/settings.local.json (Q3 B)", async () => {
  const agentDir = await freshAgentDir("claude-proj");
  const claudeConfig = join(root, "claude-proj-config");
  process.env.CLAUDE_CONFIG_DIR = claudeConfig;
  await mkdir(claudeConfig, { recursive: true });
  await writeFile(join(claudeConfig, "settings.json"), JSON.stringify({ effortLevel: "low" }));
  const project = join(root, "claude-proj-project");
  await mkdir(join(project, ".claude", "skills", "proj-cl-skill"), { recursive: true });
  await writeFile(join(project, ".claude", "skills", "proj-cl-skill", "SKILL.md"), "---\nname: proj-cl-skill\n---\nbody\n");
  const { allowFileRoot } = await jiti.import("../../../../lib/file-access.ts");
  allowFileRoot(project);
  const restorePath = putOnPath(await installVersionedStub("claude-proj-bin", "2.1.326"));
  const before = await mdHashes(root);

  try {
    const response = await patchSkills(
      { skill: "proj-cl-skill", harness: "claude", state: "name-only", scope: "project" },
      { cwd: project },
    );
    assert.equal(response.status, 200);
    const local = JSON.parse(await readFile(join(project, ".claude", "settings.local.json"), "utf8"));
    assert.equal(local.skillOverrides["proj-cl-skill"], "name-only");
    assert.deepEqual(await readPreference(agentDir), {});
    const userSettings = JSON.parse(await readFile(join(claudeConfig, "settings.json"), "utf8"));
    assert.equal(userSettings.skillOverrides, undefined, "the user layer stays untouched (Q3 B)");
    assert.deepEqual(await mdHashes(root), before);
  } finally {
    restorePath();
  }
});

test("a claude item on a version below the feature floor refuses and writes nothing (风险4)", async () => {
  const agentDir = await freshAgentDir("claude-floor");
  const claudeConfig = join(root, "claude-floor-config");
  process.env.CLAUDE_CONFIG_DIR = claudeConfig;
  await mkdir(claudeConfig, { recursive: true });
  await writeFile(join(claudeConfig, "settings.json"), JSON.stringify({ effortLevel: "low" }));
  const restorePath = putOnPath(await installVersionedStub("claude-floor-bin", "2.1.312"));
  const before = await mdHashes(root);

  try {
    const response = await patchSkills({ skill: "cl-skill", harness: "claude", state: "user-invocable-only" });
    assert.equal(response.status, 400);
    const payload = await response.json();
    assert.ok(payload.error.includes("2.1.319"), payload.error);
    const settings = JSON.parse(await readFile(join(claudeConfig, "settings.json"), "utf8"));
    assert.equal(settings.skillOverrides, undefined);
    assert.deepEqual(await readPreference(agentDir), {});
    assert.deepEqual(await mdHashes(root), before);
  } finally {
    restorePath();
  }
});

test("malformed input is rejected before any store is written", async () => {
  const agentDir = await freshAgentDir("malformed");
  const skill = await writeSkill(join(agentDir, "skills"), "untouched");
  const claudeConfig = join(root, "malformed-claude-config");
  process.env.CLAUDE_CONFIG_DIR = claudeConfig;
  await mkdir(claudeConfig, { recursive: true });
  await writeFile(join(claudeConfig, "settings.json"), JSON.stringify({}));
  const before = await mdHashes(root);

  assert.equal((await patchSkills({ harness: "pi", state: "disabled" })).status, 400);
  assert.equal((await patchSkills({ skill: skill, state: "disabled" })).status, 400);
  assert.equal((await patchSkills({ skill: skill, harness: "team", state: "disabled" })).status, 400);
  assert.equal((await patchSkills({ skill: skill, harness: "pi", state: "loud" })).status, 400);
  assert.equal((await patchSkills({ skill: "", harness: "claude", state: "enabled" })).status, 400);
  assert.equal((await patchSkills({ skill: "cl", harness: "claude", state: "enabled", scope: "team" })).status, 400);
  assert.equal((await patchSkills({ skill: "cl", harness: "claude", state: "enabled", scope: "project" })).status, 400);
  assert.equal((await patchSkills({ skill: skill, harness: "pi", state: 42 })).status, 400);
  assert.equal((await patchSkills({ items: [skill] })).status, 400);
  assert.equal((await patchSkills({ items: [{ skill: skill, harness: "pi" }] })).status, 400);
  assert.equal((await patchSkills({ items: "no" })).status, 400);

  // a present-but-unknown state is the request's own malformed input: 400
  assert.equal((await patchSkills({ skill: "cl", harness: "claude", state: "loud" })).status, 400);
  assert.equal((await patchSkills({ items: [{ skill: "cl", harness: "claude", state: "loud" }] })).status, 400);

  // per-item guard refusals report on their own and do not stop the rest;
  // the claude item goes through the gated write on the stub's version
  const restorePath = putOnPath(await installVersionedStub("malformed-claude-bin", "2.1.326"));
  try {
    const batch = await patchSkills({ items: [
      { skill: skill, harness: "pi", state: "disabled" },
      { skill: join(agentDir, "auth.json"), harness: "pi", state: "disabled" },
      { skill: "cl", harness: "claude", state: "user-invocable-only" },
    ] });
    assert.equal(batch.status, 200);
    const results = (await batch.json()).results;
    assert.equal(results.length, 3);
    assert.deepEqual(results[0], { skill, harness: "pi" });
    assert.ok(results[1].error.includes("Not a skill file"));
    assert.deepEqual(results[2], { skill: "cl", harness: "claude" });
  } finally {
    restorePath();
  }

  assert.deepEqual(await mdHashes(root), before);
  assert.deepEqual(await readPreference(agentDir), { [skill]: "hidden" });
  const settings = JSON.parse(await readFile(join(claudeConfig, "settings.json"), "utf8"));
  assert.equal(settings.skillOverrides["cl"], "user-invocable-only");
});

test("only the pi side accepts a preference, however the file is named (legacy guard kept)", async () => {
  const agentDir = await freshAgentDir("auth");
  // auth.json and settings.json sit inside the agent dir, which the route
  // allows; a preference keyed by them would be nonsense.
  const authPath = join(agentDir, "auth.json");
  const auth = JSON.stringify({ provider: { type: "api_key", key: "test" } });
  await writeFile(authPath, auth);

  const single = await patchSkills({ skill: authPath, harness: "pi", state: "disabled" });
  assert.equal(single.status, 400);
  const batch = await patchSkills({ items: [{ skill: authPath, harness: "pi", state: "disabled" }] });
  assert.deepEqual(await batch.json(), { results: [{ skill: authPath, harness: "pi", error: "Not a skill file" }] });
  assert.equal(await readFile(authPath, "utf8"), auth);
  assert.deepEqual(await readPreference(agentDir), {});
});
