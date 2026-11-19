import "#test-env";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test, { after } from "node:test";
import { createJiti } from "jiti";

// The per-skill, user-confirmed legacy cleanup (owner Q3 B): the hidden
// preference is written before the `disable-model-invocation` line is removed,
// a target the guards refuse is never edited, and every SKILL.md byte outside
// the removed line survives.
const root = await mkdtemp(join(tmpdir(), "pi-web-skills-cleanup-"));
const previousAgentDir = process.env.PI_CODING_AGENT_DIR;

async function freshAgentDir(name) {
  const agentDir = join(root, `agent-${name}`);
  process.env.PI_CODING_AGENT_DIR = agentDir;
  await mkdir(join(agentDir, "skills"), { recursive: true });
  return agentDir;
}

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() } });
const { POST } = await jiti.import("./route.ts");

after(async () => {
  if (previousAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = previousAgentDir;
  await rm(root, { recursive: true, force: true });
});

async function writeSkill(dir, name, frontmatter = "") {
  const filePath = join(dir, name, "SKILL.md");
  await mkdir(join(dir, name), { recursive: true });
  await writeFile(filePath, `---\nname: ${name}\n${frontmatter}description: ${name} skill\n---\n\n# ${name}\n`);
  return filePath;
}

const readPreference = async (agentDir) => {
  try {
    return JSON.parse(await readFile(join(agentDir, "pi-web-settings.json"), "utf8")).skillVisibility?.pi ?? {};
  } catch {
    return {};
  }
};
const hash = (content) => createHash("sha256").update(content).digest("hex");

function cleanup(body) {
  return POST(new Request("http://localhost/api/skills/cleanup", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  }));
}

test("the confirmed cleanup writes the preference, then removes the line", async () => {
  const agentDir = await freshAgentDir("cleanup");
  const skill = await writeSkill(join(agentDir, "skills"), "legacy", "disable-model-invocation: true\n");

  const response = await cleanup({ filePath: skill });

  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { success: true, removed: true });
  assert.deepEqual(await readPreference(agentDir), { [skill]: "hidden" });
  const content = await readFile(skill, "utf8");
  assert.doesNotMatch(content, /disable-model-invocation/);
  assert.equal(content, "---\nname: legacy\ndescription: legacy skill\n---\n\n# legacy\n");
});

test("refused targets are never edited and get no preference", async () => {
  const agentDir = await freshAgentDir("refused");
  const skill = await writeSkill(join(agentDir, "skills"), "legacy", "disable-model-invocation: true\n");
  const outside = await writeSkill(join(root, "outside"), "outside", "disable-model-invocation: true\n");
  const before = hash(await readFile(outside, "utf8"));

  assert.equal((await cleanup({ filePath: outside })).status, 403);
  assert.equal((await cleanup({ filePath: join(agentDir, "skills", "missing", "SKILL.md") })).status, 404);
  assert.equal((await cleanup({ filePath: join(agentDir, "auth.json") })).status, 400);
  assert.equal((await cleanup({})).status, 400);

  assert.equal(hash(await readFile(outside, "utf8")), before);
  assert.deepEqual(await readPreference(agentDir), {});
  assert.match(await readFile(skill, "utf8"), /disable-model-invocation: true/);
});

test("a line the surgical editor refuses still leaves the preference written first", async () => {
  const agentDir = await freshAgentDir("flow");
  const flow = join(agentDir, "skills", "flow", "SKILL.md");
  await mkdir(join(agentDir, "skills", "flow"), { recursive: true });
  await writeFile(flow, "---\n{ name: flow, disable-model-invocation: true }\n---\nbody\n");
  const before = hash(await readFile(flow, "utf8"));

  const response = await cleanup({ filePath: flow });
  const body = await response.json();

  assert.equal(response.status, 500);
  assert.match(body.error, /unsupported frontmatter formatting/);
  assert.deepEqual(await readPreference(agentDir), { [flow]: "hidden" }, "the preference went in before the edit");
  assert.equal(hash(await readFile(flow, "utf8")), before);
});
