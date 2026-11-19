import "#test-env";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test, { after } from "node:test";
import { createJiti } from "jiti";

// The skill switch writes pi-web's own preference store
// (`<agentDir>/pi-web-settings.json`, `skillVisibility.pi`), never a SKILL.md:
// every `*.md` in the fixture tree must stay byte-identical (SHA-256) across
// single and batch toggles, while out-of-bounds paths stay 403, non-skill or
// malformed input stays 400, and each batch item is reported on its own.
// Every test gets its own agent dir, so the preference store starts empty.
const root = await mkdtemp(join(tmpdir(), "pi-web-skills-route-"));
const previousAgentDir = process.env.PI_CODING_AGENT_DIR;
process.env.PI_CODING_AGENT_DIR = join(root, "agent");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() } });
const { PATCH } = await jiti.import("./route.ts");

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

function patchSkills(body) {
  return PATCH(new Request("http://localhost/api/skills", {
    method: "PATCH",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  }));
}

test("a batch switches every listed skill by preference and keeps every SKILL.md byte-identical", async () => {
  const agentDir = await freshAgentDir("batch");
  const skillsDir = join(agentDir, "skills");
  const visible = await writeSkill(skillsDir, "visible");
  const hidden = await writeSkill(skillsDir, "hidden", "disable-model-invocation: true\n");
  const outside = await writeSkill(join(root, "outside"), "outside");
  const missing = join(skillsDir, "missing", "SKILL.md");
  const before = await mdHashes(root);

  const response = await patchSkills({
    filePaths: [visible, hidden, outside, missing, visible],
    disableModelInvocation: true,
  });

  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), {
    results: [
      { filePath: visible },
      { filePath: hidden },
      { filePath: outside, error: "Access denied" },
      { filePath: missing, error: "file not found" },
    ],
  });
  assert.deepEqual(await mdHashes(root), before, "no SKILL.md may change by a single byte");
  assert.deepEqual(await readPreference(agentDir), {
    [visible]: "hidden",
    [hidden]: "hidden",
  });
});

test("a single toggle writes the preference, shows override the author's frontmatter", async () => {
  const agentDir = await freshAgentDir("single");
  const skill = await writeSkill(join(agentDir, "skills"), "single", "disable-model-invocation: true\n");
  const before = await mdHashes(root);

  const response = await patchSkills({ filePath: skill, disableModelInvocation: true });
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { success: true });
  assert.deepEqual(await readPreference(agentDir), { [skill]: "hidden" });

  const shown = await patchSkills({ filePath: skill, disableModelInvocation: false });
  assert.equal(shown.status, 200);
  assert.deepEqual(await mdHashes(root), before, "the author's frontmatter line stays in the file");
  assert.deepEqual(await readPreference(agentDir), { [skill]: "shown" });

  const refused = await patchSkills({ filePath: join(root, "outside", "outside", "SKILL.md"), disableModelInvocation: true });
  assert.equal(refused.status, 403);
});

test("any frontmatter formatting toggles, because the file is never parsed or edited", async () => {
  const agentDir = await freshAgentDir("flow");
  const unsupported = join(agentDir, "skills", "flow", "SKILL.md");
  await mkdir(join(agentDir, "skills", "flow"), { recursive: true });
  await writeFile(unsupported, "---\n{ name: flow, disable-model-invocation: true }\n---\nbody\n");
  const before = await mdHashes(root);

  const response = await patchSkills({ filePaths: [unsupported], disableModelInvocation: false });
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { results: [{ filePath: unsupported }] });
  assert.deepEqual(await mdHashes(root), before);
  assert.deepEqual(await readPreference(agentDir), { [unsupported]: "shown" });
});

test("malformed input is rejected before any preference is written", async () => {
  const agentDir = await freshAgentDir("malformed");
  const skill = await writeSkill(join(agentDir, "skills"), "untouched");
  const before = await mdHashes(root);

  assert.equal((await patchSkills({ filePaths: [skill, 42], disableModelInvocation: true })).status, 400);
  assert.equal((await patchSkills({ filePaths: skill, disableModelInvocation: true })).status, 400);
  assert.equal((await patchSkills({ filePaths: [skill], disableModelInvocation: "yes" })).status, 400);
  assert.equal((await patchSkills({ filePaths: [skill] })).status, 400);
  assert.equal((await patchSkills({ filePath: skill })).status, 400);
  assert.deepEqual(await mdHashes(root), before);
  assert.deepEqual(await readPreference(agentDir), {});
});

test("only markdown files get a preference, however they are named in the request", async () => {
  const agentDir = await freshAgentDir("auth");
  // auth.json and settings.json sit inside the agent dir, which the route
  // allows; a preference keyed by them would be nonsense.
  const authPath = join(agentDir, "auth.json");
  const auth = JSON.stringify({ provider: { type: "api_key", key: "test" } });
  await writeFile(authPath, auth);

  const single = await patchSkills({ filePath: authPath, disableModelInvocation: true });
  assert.equal(single.status, 400);
  const batch = await patchSkills({ filePaths: [authPath], disableModelInvocation: true });
  assert.deepEqual(await batch.json(), { results: [{ filePath: authPath, error: "Not a skill file" }] });
  assert.equal(await readFile(authPath, "utf8"), auth);
  assert.deepEqual(await readPreference(agentDir), {});
});
