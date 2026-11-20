// carries: lib/skills-unified.ts, lib/api-types.ts, lib/skills-service.ts
import "#test-env";
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test, { after } from "node:test";
import { createJiti } from "jiti";

// The unified skills listing's merge (85d u2): pi's loader listing and
// claude's adapter listing become one row per real file, grouped 用户 / 项目
// (owner Q2 B: same real file in one scope is one row with every harness
// label; same name on different files stays two rows with the shadow note).
// Expected values are hand-written from the ticket (AC1, Q2 B) — the fixture
// layout below decides which rows must come out, never the implementation.
const root = mkdtempSync(join(tmpdir(), "pi-web-skills-unified-"));

const writeSkill = (dir, name) => {
  mkdirSync(join(dir, name), { recursive: true });
  const filePath = join(dir, name, "SKILL.md");
  writeFileSync(filePath, `---\nname: ${name}\ndescription: ${name} skill\n---\nbody\n`);
  return filePath;
};

const piUserDir = join(root, "pi-user");
const piProjectDir = join(root, "pi-project");
const claudeUserDir = join(root, "claude-user");
const claudeProjectDir = join(root, "claude-project");
const sharedUser = writeSkill(piUserDir, "shared-skill");
const sharedProject = writeSkill(piProjectDir, "shared-proj-skill");
const piOnly = writeSkill(piUserDir, "pi-only-skill");
const piPathScoped = writeSkill(join(root, "declared"), "declared-skill");
const claudeOnly = writeSkill(claudeUserDir, "claude-only-skill");
const piDup = writeSkill(join(root, "dup-pi"), "dup-name");
const claudeDup = writeSkill(join(root, "dup-claude"), "dup-name");
const claudeProjectOnly = writeSkill(claudeProjectDir, "claude-proj-skill");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const { mergeSkillListings } = await jiti.import("./skills-unified.ts");

/** A pi loader SkillInfo for a real fixture file. */
const piSide = (name, filePath, scope) => ({
  name,
  description: `${name} skill`,
  filePath,
  baseDir: join(filePath, ".."),
  disableModelInvocation: false,
  sourceInfo: { scope, source: scope },
});

after(() => {
  rmSync(root, { recursive: true, force: true });
});

const sharedClaudeUser = { name: "shared-skill", path: sharedUser, source: "user" };

test("a file both harnesses list is one row with both labels, keyed by realpath (AC1, Q2 B)", () => {
  const rows = mergeSkillListings(
    [piSide("shared-skill", sharedUser, "user"), piSide("pi-only-skill", piOnly, "user")],
    { entries: [sharedClaudeUser, { name: "claude-only-skill", path: claudeOnly, source: "user" }] },
  );
  assert.equal(rows.length, 3);
  const shared = rows.find((row) => row.name === "shared-skill");
  assert.deepEqual(shared.harnesses, ["pi", "claude"]);
  assert.equal(shared.scope, "user");
  assert.equal(shared.pi.filePath, sharedUser);
  assert.equal(shared.claude.name, "shared-skill");
  assert.deepEqual(rows.find((row) => row.name === "pi-only-skill").harnesses, ["pi"]);
  assert.deepEqual(rows.find((row) => row.name === "claude-only-skill").harnesses, ["claude"]);
});

test("a symlinked file merges with its target, so one real file stays one row (Q2 B)", () => {
  const linkDir = join(root, "linked");
  mkdirSync(join(linkDir, "shared-skill"), { recursive: true });
  symlinkSync(sharedUser, join(linkDir, "shared-skill", "SKILL.md"));
  const rows = mergeSkillListings(
    [piSide("shared-skill", join(linkDir, "shared-skill", "SKILL.md"), "user")],
    { entries: [sharedClaudeUser] },
  );
  assert.equal(rows.length, 1);
  assert.deepEqual(rows[0].harnesses, ["pi", "claude"]);
});

test("same name on different files stays two rows, each naming the other (Q2 B 遮蔽)", () => {
  const rows = mergeSkillListings(
    [piSide("dup-name", piDup, "user")],
    { entries: [{ name: "dup-name", path: claudeDup, source: "user" }] },
  );
  assert.equal(rows.length, 2);
  const names = rows.map((row) => row.name);
  assert.deepEqual(names, ["dup-name", "dup-name"]);
  for (const row of rows) {
    assert.equal(row.sameName.length, 1);
    assert.notEqual(row.sameName[0], row.key);
    assert.ok(rows.some((other) => other.key === row.sameName[0] && other.key !== row.key));
  }
});

test("pi's global and path scopes are the user group; project scope is the project group (AC1)", () => {
  const rows = mergeSkillListings(
    [
      piSide("shared-skill", sharedUser, "user"),
      piSide("shared-proj-skill", sharedProject, "project"),
      piSide("declared-skill", piPathScoped, ""),
    ],
    {
      entries: [
        sharedClaudeUser,
        { name: "shared-proj-skill", path: sharedProject, source: "project" },
        { name: "claude-proj-skill", path: claudeProjectOnly, source: "project" },
      ],
    },
  );
  const userNames = rows.filter((row) => row.scope === "user").map((row) => row.name).sort();
  const projectNames = rows.filter((row) => row.scope === "project").map((row) => row.name).sort();
  assert.deepEqual(userNames, ["declared-skill", "shared-skill"]);
  assert.deepEqual(projectNames, ["claude-proj-skill", "shared-proj-skill"]);
});

test("the same real file in different scopes stays two rows, each keeping its own scope (Q2 B, review r1#3)", () => {
  // pi lists the file from a user-declared settings path, claude lists the
  // same file as a project skill: Q2 B merges only within one scope, so the
  // claude side must keep the project layer its switch writes to.
  const rows = mergeSkillListings(
    [piSide("shared-skill", sharedUser, "user")],
    { entries: [{ name: "shared-skill", path: sharedUser, source: "project" }] },
  );
  assert.equal(rows.length, 2, "cross-scope listings never merge into one row");
  assert.deepEqual(rows.map((row) => row.scope).sort(), ["project", "user"]);
  const claudeRow = rows.find((row) => row.claude);
  assert.equal(claudeRow.scope, "project", "the claude row keeps its own layer, so its switch writes .claude/settings.local.json");
  assert.deepEqual(claudeRow.harnesses, ["claude"]);
  assert.deepEqual(rows.find((row) => row.pi).harnesses, ["pi"]);
  assert.notEqual(claudeRow.key, rows.find((row) => row.pi).key, "the two rows keep distinct keys");
});

test("claude's stored override state rides its side as the native word; a pi-only row carries none", () => {
  const rows = mergeSkillListings(
    [piSide("shared-skill", sharedUser, "user"), piSide("pi-only-skill", piOnly, "user")],
    { entries: [sharedClaudeUser], overrides: { "shared-skill": "user-invocable-only" } },
  );
  assert.equal(rows.find((row) => row.name === "shared-skill").claude.state, "user-invocable-only");
  assert.equal(rows.find((row) => row.name === "pi-only-skill").claude, undefined);
});
