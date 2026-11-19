// carries: lib/claude-mcp-read.ts, lib/__fixtures__/claude-2.1.326/mcp/*
// polyforge#144 (#85 85c, AC1, Q6 B): claude's MCP entries are read from the
// native files only — user from the state file's top-level mcpServers, local
// from its projects[<cwd>].mcpServers (matched by real path too), project from
// <cwd>/.mcp.json; manual entries read back; disabledMcpServers read-only;
// values masked; nothing spawns. The fixture is the shape claude 2.1.326
// itself wrote for `claude mcp add-json -s user|local|project` (captured from
// a real run in a temp HOME), so the reader is pinned to claude's own bytes.
import "#test-env";
import assert from "node:assert/strict";
import { existsSync, realpathSync } from "node:fs";
import { copyFile, mkdir, mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { moduleCache: false });
const { readClaudeMcpServers, claudeMcpStatePath } = await jiti.import("./claude-mcp-read.ts");

const fixtureDir = new URL("./__fixtures__/claude-2.1.326/mcp/", import.meta.url).pathname;

async function temp(t) {
  // Real paths, so the paths reported compare equal on a host whose temp folder is a link.
  const root = realpathSync(await mkdtemp(join(tmpdir(), "pi-web-claude-mcp-read-")));
  t.after(() => rm(root, { recursive: true, force: true }));
  return root;
}

/** A temp config dir holding the real-shape fixture state file. */
async function configFixture(t) {
  const root = await temp(t);
  const configDir = join(root, "cfg");
  await mkdir(configDir, { recursive: true });
  await copyFile(join(fixtureDir, "claude.json"), join(configDir, ".claude.json"));
  return { root, configDir, env: { CLAUDE_CONFIG_DIR: configDir } };
}

function byName(servers, scope) {
  return Object.fromEntries(servers.filter((server) => !scope || server.scope === scope).map((server) => [server.name, server]));
}

test("user entries read back from the state file, masked, with claude's own placement", async (t) => {
  const { configDir, env } = await configFixture(t);
  const read = readClaudeMcpServers({ env });
  assert.equal(read.configPath, join(configDir, ".claude.json"));
  assert.equal(read.stateReadable, true);
  const { demoUser, manualHttp } = byName(read.servers, "global");
  // The fixture is claude's own output; these are its bytes, not the reader's.
  assert.equal(demoUser.name, "demo-user");
  assert.equal(demoUser.scope, "global");
  assert.equal(demoUser.sourcePath, join(configDir, ".claude.json"));
  assert.equal(demoUser.command, "npx");
  assert.deepEqual(demoUser.args, ["-y", "server"]);
  assert.deepEqual(demoUser.envNames, ["A", "SECRET_TOKEN"]);
  assert.equal(manualHttp.url, "https://api.example.com/mcp?token=•••");
  assert.deepEqual(manualHttp.headerNames, ["Authorization", "X-Custom"]);
  assert.equal(manualHttp.type, "http");
  // No env or header value, and no secret the URL carried, reaches the result.
  const text = JSON.stringify(read);
  assert.ok(!text.includes("fixture-secret-value"));
  assert.ok(!text.includes("fixture-header-secret"));
  assert.ok(!text.includes("fixture-token-value"));
});

test("the state file sits in ~/.claude.json when CLAUDE_CONFIG_DIR is unset, not in ~/.claude", () => {
  // claudeConfigDir() falls back to ~/.claude, which never holds the state file;
  // claudeMcpStatePath() must answer claude's own placement instead.
  assert.equal(claudeMcpStatePath({ env: {} }), join(homedir(), ".claude.json"));
});

test("disabledMcpServers is brought back read-only", async (t) => {
  const { env } = await configFixture(t);
  const read = readClaudeMcpServers({ env });
  assert.deepEqual(read.disabledMcpServers, ["demo-proj"]);
});

test("local entries are keyed by the cwd claude resolved, a symlink included", async (t) => {
  const root = await temp(t);
  const realProj = join(root, "real-project");
  await mkdir(realProj, { recursive: true });
  const linkProj = join(root, "link-project");
  await symlink(realProj, linkProj);
  const configDir = join(root, "cfg");
  await mkdir(configDir, { recursive: true });
  // claude keys the local scope by the absolute path it resolved, so a folder
  // reached through a link is keyed by its real path: write the state file the
  // way claude itself would have from the real project.
  await writeFile(join(configDir, ".claude.json"), JSON.stringify({
    projects: {
      [realProj]: {
        mcpServers: { "demo-local": { command: "node", args: ["s.js"], env: { TOKEN: "v" } } },
        hasTrustDialogAccepted: false,
      },
    },
  }));
  const read = readClaudeMcpServers({ cwd: linkProj, env: { CLAUDE_CONFIG_DIR: configDir } });
  const local = byName(read.servers, "local");
  assert.deepEqual(Object.keys(local), ["demo-local"]);
  assert.equal(local["demo-local"].command, "node");
  assert.deepEqual(local["demo-local"].args, ["s.js"]);
  assert.deepEqual(local["demo-local"].envNames, ["TOKEN"]);
  // And the direct path reads the same entry.
  const direct = readClaudeMcpServers({ cwd: realProj, env: { CLAUDE_CONFIG_DIR: configDir } });
  assert.deepEqual(Object.keys(byName(direct.servers, "local")), ["demo-local"]);
});

test("project entries come from <cwd>/.mcp.json; a disabled name is marked, read-only", async (t) => {
  const root = await temp(t);
  const proj = join(root, "proj");
  await mkdir(proj, { recursive: true });
  const configDir = join(root, "cfg");
  await mkdir(configDir, { recursive: true });
  await copyFile(join(fixtureDir, "claude.json"), join(configDir, ".claude.json"));
  // claude mcp add-json -s project writes this file with these bytes.
  await writeFile(join(proj, ".mcp.json"), JSON.stringify({
    mcpServers: { "demo-proj": { command: "node", args: ["s.js"] } },
  }, null, 2));
  const read = readClaudeMcpServers({ cwd: proj, env: { CLAUDE_CONFIG_DIR: configDir } });
  const { demoProj } = byName(read.servers, "project");
  assert.equal(demoProj.name, "demo-proj");
  assert.equal(demoProj.sourcePath, join(proj, ".mcp.json"));
  assert.equal(demoProj.disabled, true, "disabledMcpServers names it, so the row shows it read-only");
  assert.equal(read.disabledMcpServers.includes("demo-proj"), true);
  // Without a cwd no project or local entry is listed.
  const bare = readClaudeMcpServers({ env: { CLAUDE_CONFIG_DIR: configDir } });
  assert.equal(bare.servers.some((server) => server.scope !== "global"), false);
});

test("a state file that is not a JSON object lists no user or local entry, and the project file still reads", async (t) => {
  const root = await temp(t);
  const proj = join(root, "proj");
  const configDir = join(root, "cfg");
  await mkdir(proj, { recursive: true });
  await mkdir(configDir, { recursive: true });
  await writeFile(join(configDir, ".claude.json"), "[]");
  await writeFile(join(proj, ".mcp.json"), JSON.stringify({ mcpServers: { ok: { command: "x" } } }));
  const read = readClaudeMcpServers({ cwd: proj, env: { CLAUDE_CONFIG_DIR: configDir } });
  assert.equal(read.stateReadable, false);
  assert.deepEqual(read.servers.map((server) => server.name), ["ok"]);
  assert.deepEqual(read.disabledMcpServers, []);
});

test("a missing state file reads as none, not as an error", async (t) => {
  const root = await temp(t);
  const configDir = join(root, "cfg");
  await mkdir(configDir, { recursive: true });
  const read = readClaudeMcpServers({ env: { CLAUDE_CONFIG_DIR: configDir } });
  assert.equal(read.stateReadable, true);
  assert.deepEqual(read.servers, []);
  assert.equal(existsSync(read.configPath), false);
});
