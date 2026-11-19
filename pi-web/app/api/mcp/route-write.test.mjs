// carries: app/api/mcp/route.ts, lib/api-types.ts, lib/mcp-unified.ts, lib/claude-mcp-write.ts, lib/claude-mcp-read.ts, lib/mcp-undo.ts, lib/__fixtures__/claude-2.1.326-mcp/mcp/*
import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, existsSync, realpathSync } from "node:fs";
import { copyFile, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import test, { after, beforeEach } from "node:test";
import { ProjectTrustStore } from "@earendil-works/pi-coding-agent";
import { createJiti } from "jiti";

// POST /api/mcp writes the user's files, so every case runs against a
// PI_CODING_AGENT_DIR in a temporary folder, never the real ~/.pi/agent.
const root = realpathSync(await mkdtemp(join(tmpdir(), "pi-web-mcp-write-route-")));
const agentDir = join(root, "agent");
const cwd = join(root, "project");
const outside = join(root, "outside");
const previousAgentDir = process.env.PI_CODING_AGENT_DIR;
const previousDisable = process.env.PI_WEB_DISABLE_MCP;
process.env.PI_CODING_AGENT_DIR = agentDir;
delete process.env.PI_WEB_DISABLE_MCP;
await mkdir(agentDir, { recursive: true });
await mkdir(join(cwd, ".pi"), { recursive: true });
await mkdir(outside, { recursive: true });

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() } });
const { allowFileRoot } = await jiti.import("../../../lib/file-access.ts");
const { POST } = await jiti.import("./route.ts");
allowFileRoot(cwd);

after(async () => {
  if (previousAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = previousAgentDir;
  if (previousDisable === undefined) delete process.env.PI_WEB_DISABLE_MCP;
  else process.env.PI_WEB_DISABLE_MCP = previousDisable;
  await rm(root, { recursive: true, force: true });
});

const globalPath = join(agentDir, "mcp.json");
const projectPath = join(cwd, ".pi", "mcp.json");
const store = new ProjectTrustStore(agentDir);
const SECRET = "literal-header-secret-value";

const globalText = `${JSON.stringify({
  mcpServers: {
    docs: { url: "https://docs.example.com/mcp", headers: { Authorization: `Bearer ${SECRET}` } },
    lint: { command: "npx", args: ["lint-mcp"] },
    pw: { url: "https://pw.example.com/mcp", headers: { Authorization: "Bearer ${PI_WEB_PASSWORD}" }, enabled: false },
  },
  keep: "unknown keys stay",
}, null, 2)}\n`;
const projectText = `${JSON.stringify({ mcpServers: { repo: { command: "node", args: ["server.js"] } } }, null, 2)}\n`;

beforeEach(async () => {
  delete process.env.PI_WEB_DISABLE_MCP;
  await writeFile(globalPath, globalText);
  await rm(projectPath, { force: true, recursive: true });
  await writeFile(projectPath, projectText);
  store.set(cwd, null);
});

async function post(body, headers = {}) {
  const response = await POST(new Request("http://localhost/api/mcp", {
    method: "POST",
    headers: { host: "localhost", "Content-Type": "application/json", ...headers },
    body: typeof body === "string" ? body : JSON.stringify(body),
  }));
  return { status: response.status, body: await response.json() };
}

const readJson = async (path) => JSON.parse(await readFile(path, "utf8"));
const server = (body, scope, name) => body.servers.find((entry) => entry.scope === scope && entry.name === name);

test("a server is switched off and on in its file, and the answer is the overview after it", async () => {
  let { status, body } = await post({ action: "disable", scope: "global", name: "lint" });
  assert.equal(status, 200);
  assert.equal(server(body, "global", "lint").enabled, false);
  assert.ok(body.mcp && body.codemode && body.files, "the whole overview, as GET answers it");
  assert.deepEqual((await readJson(globalPath)).mcpServers.lint, { command: "npx", args: ["lint-mcp"], enabled: false });
  assert.equal((await readJson(globalPath)).keep, "unknown keys stay");

  ({ status, body } = await post({ action: "enable", scope: "global", name: "lint" }));
  assert.equal(status, 200);
  assert.equal(server(body, "global", "lint").enabled, true);
  assert.equal(await readFile(globalPath, "utf8"), globalText, "enabled: true removes the key, as the SDK does");
  assert.ok(!JSON.stringify(body).includes(SECRET));
});

test("a server's exposure is set in its file, codemode removing the key, and the answer is the overview after it", async () => {
  let { status, body } = await post({ action: "set-exposure", scope: "global", name: "docs", exposure: "direct" });
  assert.equal(status, 200);
  assert.equal(server(body, "global", "docs").exposure, "direct");
  assert.equal((await readJson(globalPath)).mcpServers.docs.exposure, "direct");
  assert.ok(!JSON.stringify(body).includes(SECRET));

  // The old name pi still reads is not offered, and an entry holding it lists as what it now means.
  ({ status, body } = await post({ action: "set-exposure", scope: "global", name: "docs", exposure: "codemode-deferred" }));
  assert.equal(status, 400);
  const aliased = JSON.parse(globalText);
  aliased.mcpServers.docs.exposure = "codemode-deferred";
  await writeFile(globalPath, JSON.stringify(aliased));
  ({ status, body } = await post({ action: "set-exposure", scope: "global", name: "lint", exposure: "codemode" }));
  assert.equal(status, 200);
  assert.equal(server(body, "global", "docs").exposure, "codemode");

  ({ status, body } = await post({ action: "set-exposure", scope: "global", name: "docs", exposure: "codemode" }));
  assert.equal(status, 200);
  assert.equal(server(body, "global", "docs").exposure, "codemode");
  assert.equal(await readFile(globalPath, "utf8"), globalText, "codemode, the default, removes the key, as the SDK does");

  // Nothing connects because of it, so an entry that references PI_WEB_PASSWORD may change too, and stays off.
  ({ status, body } = await post({ action: "set-exposure", scope: "global", name: "pw", exposure: "hidden" }));
  assert.equal(status, 200);
  assert.deepEqual([server(body, "global", "pw").exposure, server(body, "global", "pw").enabled], ["hidden", false]);

  // toolExposure is kept, and counted.
  const withOverrides = JSON.parse(globalText);
  withOverrides.mcpServers.lint.toolExposure = { lint_file: "direct", "fix_*": "hidden" };
  await writeFile(globalPath, JSON.stringify(withOverrides));
  ({ body } = await post({ action: "set-exposure", scope: "global", name: "lint", exposure: "deferred" }));
  assert.equal(server(body, "global", "lint").toolExposureCount, 2);
  assert.deepEqual((await readJson(globalPath)).mcpServers.lint, { command: "npx", args: ["lint-mcp"], toolExposure: { lint_file: "direct", "fix_*": "hidden" }, exposure: "deferred" });
});

test("an exposure change follows the same guards as a switch", async () => {
  for (const body of [
    { action: "set-exposure", scope: "global", name: "docs" },
    { action: "set-exposure", scope: "global", name: "docs", exposure: "sometimes" },
    { action: "set-exposure", scope: "elsewhere", name: "docs", exposure: "direct" },
  ]) {
    const response = await post(body);
    assert.equal(response.status, 400, JSON.stringify(body));
    assert.equal(response.body.reason, "invalid-request", JSON.stringify(body));
  }
  const missing = await post({ action: "set-exposure", scope: "global", name: "gone", exposure: "direct" });
  assert.deepEqual([missing.status, missing.body.reason], [409, "server-missing"]);
  const untrusted = await post({ action: "set-exposure", scope: "project", name: "repo", exposure: "direct", cwd });
  assert.deepEqual([untrusted.status, untrusted.body.reason], [403, "project-untrusted"]);
  assert.equal(await readFile(projectPath, "utf8"), projectText);
  process.env.PI_WEB_DISABLE_MCP = "1";
  const off = await post({ action: "set-exposure", scope: "global", name: "docs", exposure: "direct" });
  assert.deepEqual([off.status, off.body.reason], [409, "mcp-off"]);
  delete process.env.PI_WEB_DISABLE_MCP;
  assert.equal(await readFile(globalPath, "utf8"), globalText);

  await writeFile(globalPath, JSON.stringify({ mcpServers: { text: "x" } }));
  const notObject = await post({ action: "set-exposure", scope: "global", name: "text", exposure: "direct" });
  assert.deepEqual([notObject.status, notObject.body.reason], [409, "entry-not-object"]);
});

test("requests from another page, not sent as JSON, or naming nothing to do are refused", async () => {
  assert.deepEqual(
    await post({ action: "disable", scope: "global", name: "lint" }, { origin: "https://evil.example", "sec-fetch-site": "cross-site" }),
    { status: 403, body: { error: "Untrusted API request", reason: "request-denied" } },
  );
  assert.deepEqual(
    await post({ action: "disable", scope: "global", name: "lint" }, { "Content-Type": "text/plain" }),
    { status: 415, body: { error: "Content-Type must be application/json", reason: "content-type" } },
  );
  for (const body of [
    "{ not json",
    [],
    { action: "explode" },
    { action: "disable", scope: "elsewhere", name: "lint" },
    { action: "disable", scope: "global" },
    { action: "set-enabled", enabled: "yes", servers: [{ scope: "global", name: "lint" }] },
    { action: "set-enabled", enabled: true, servers: [] },
    { action: "undo" },
    { action: "disable", scope: "project", name: "repo" },
  ]) {
    const response = await post(body);
    assert.equal(response.status, 400, JSON.stringify(body));
    assert.equal(response.body.reason, "invalid-request", JSON.stringify(body));
  }
  assert.equal(
    (await post({ action: "disable", scope: "global", name: "lint", cwd: outside })).body.reason,
    "cwd-denied",
  );
  assert.equal((await post({ action: "disable", scope: "global", name: "lint", cwd: "project" })).body.reason, "cwd-invalid");
  assert.equal(await readFile(globalPath, "utf8"), globalText);
});

test("an untrusted project's file is never written; a trusted one's is", async () => {
  const untrusted = await post({ action: "disable", scope: "project", name: "repo", cwd });
  assert.equal(untrusted.status, 403);
  assert.equal(untrusted.body.reason, "project-untrusted");
  assert.equal(await readFile(projectPath, "utf8"), projectText);
  // An explicit false does not count either, nor does trust a parent's false overrides.
  store.set(cwd, false);
  assert.equal((await post({ action: "remove", scope: "project", name: "repo", cwd })).body.reason, "project-untrusted");

  store.set(root, true);
  store.set(cwd, null);
  const inherited = await post({ action: "disable", scope: "project", name: "repo", cwd });
  assert.equal(inherited.status, 200, "trust through a parent counts, as it does for the MCP host");
  assert.equal(server(inherited.body, "project", "repo").enabled, false);
  assert.equal((await readJson(projectPath)).mcpServers.repo.enabled, false);
  store.set(root, null);
});

test("an unreadable trust store keeps the project's file unwritten, and the global one writable", async (t) => {
  const trustPath = join(agentDir, "trust.json");
  t.after(() => rm(trustPath, { force: true }));
  await writeFile(trustPath, "{ not json");
  const refused = await post({ action: "disable", scope: "project", name: "repo", cwd });
  assert.deepEqual([refused.status, refused.body.reason], [409, "trust-unreadable"]);
  assert.equal(await readFile(projectPath, "utf8"), projectText);
  assert.equal((await post({ action: "disable", scope: "global", name: "lint", cwd })).status, 200);
});

test("while MCP is off nothing is written", async () => {
  process.env.PI_WEB_DISABLE_MCP = "1";
  for (const body of [
    { action: "disable", scope: "global", name: "lint" },
    { action: "remove", scope: "global", name: "lint" },
    { action: "set-enabled", enabled: false, servers: [{ scope: "global", name: "lint" }] },
    { action: "undo", token: "x" },
  ]) {
    const response = await post(body);
    assert.equal(response.status, 409, JSON.stringify(body));
    assert.equal(response.body.reason, "mcp-off");
  }
  assert.equal(await readFile(globalPath, "utf8"), globalText);
});

test("an entry that references PI_WEB_PASSWORD is never turned on, but can be turned off and removed", async () => {
  const refused = await post({ action: "enable", scope: "global", name: "pw" });
  assert.deepEqual([refused.status, refused.body.reason, refused.body.name], [409, "web-password", "pw"]);
  assert.equal(await readFile(globalPath, "utf8"), globalText);
  assert.equal((await post({ action: "disable", scope: "global", name: "pw" })).status, 200);
  assert.equal((await post({ action: "remove", scope: "global", name: "pw" })).status, 200);
  assert.equal((await readJson(globalPath)).mcpServers.pw, undefined);
});

test("the group switch answers per server, so a refused one leaves the rest switched", async () => {
  const { status, body } = await post({
    action: "set-enabled",
    enabled: true,
    cwd,
    servers: [
      { scope: "global", name: "lint" },
      { scope: "global", name: "pw" },
      { scope: "global", name: "gone" },
      { scope: "project", name: "repo" },
      { scope: "global", name: "docs" },
    ],
  });
  assert.equal(status, 200);
  assert.deepEqual(body.results.map(({ scope, name, reason }) => [scope, name, reason]), [
    ["global", "lint", undefined],
    ["global", "pw", "web-password"],
    ["global", "gone", "server-missing"],
    ["project", "repo", "project-untrusted"],
    ["global", "docs", undefined],
  ]);
  assert.equal(server(body, "global", "pw").enabled, false);
  assert.equal(await readFile(projectPath, "utf8"), projectText);

  const off = await post({ action: "set-enabled", enabled: false, servers: [{ scope: "global", name: "lint" }, { scope: "global", name: "docs" }] });
  assert.deepEqual(off.body.results, [{ scope: "global", name: "lint" }, { scope: "global", name: "docs" }]);
  const servers = (await readJson(globalPath)).mcpServers;
  assert.deepEqual([servers.lint.enabled, servers.docs.enabled], [false, false]);
});

test("remove answers with an undo token, never the entry, and undo puts it back where it stood", async () => {
  const removed = await post({ action: "remove", scope: "global", name: "docs" });
  assert.equal(removed.status, 200);
  assert.equal(server(removed.body, "global", "docs"), undefined);
  const { undo } = removed.body;
  assert.deepEqual(Object.keys(undo).sort(), ["expiresInMs", "name", "path", "scope", "token"]);
  assert.deepEqual([undo.scope, undo.name, undo.path], ["global", "docs", globalPath]);
  assert.ok(undo.expiresInMs > 59_000 && undo.expiresInMs <= 60_000);
  assert.ok(!JSON.stringify(removed.body).includes(SECRET), "the removed entry stays on the server");
  assert.equal((await readJson(globalPath)).mcpServers.docs, undefined);

  const restored = await post({ action: "undo", token: undo.token });
  assert.equal(restored.status, 200);
  assert.deepEqual(restored.body.restored, { scope: "global", name: "docs" });
  assert.equal(await readFile(globalPath, "utf8"), globalText, "back in its place, byte for byte");
  assert.ok(!JSON.stringify(restored.body).includes(SECRET));

  assert.deepEqual(
    [(await post({ action: "undo", token: undo.token })).status, (await post({ action: "undo", token: undo.token })).body.reason],
    [410, "undo-unavailable"],
  );
});

test("undo never replaces a server of the same name added since, and works again once it is gone", async () => {
  const { undo } = (await post({ action: "remove", scope: "global", name: "lint" })).body;
  const text = await readFile(globalPath, "utf8");
  const readded = JSON.parse(text);
  readded.mcpServers.lint = { command: "a-new-lint" };
  await writeFile(globalPath, JSON.stringify(readded, null, 2));
  const refused = await post({ action: "undo", token: undo.token });
  assert.deepEqual([refused.status, refused.body.reason, refused.body.name], [409, "undo-name-taken", "lint"]);
  assert.equal((await readJson(globalPath)).mcpServers.lint.command, "a-new-lint");
  await writeFile(globalPath, text);
  assert.equal((await post({ action: "undo", token: undo.token })).status, 200);
  assert.deepEqual((await readJson(globalPath)).mcpServers.lint, { command: "npx", args: ["lint-mcp"] });
});

test("a project removal is undone only while the project is still trusted", async () => {
  store.set(cwd, true);
  const { undo } = (await post({ action: "remove", scope: "project", name: "repo", cwd })).body;
  assert.deepEqual([undo.scope, undo.path], ["project", projectPath]);
  store.set(cwd, false);
  assert.equal((await post({ action: "undo", token: undo.token, cwd })).body.reason, "project-untrusted");
  store.set(cwd, true);
  assert.equal((await post({ action: "undo", token: undo.token, cwd })).status, 200);
  assert.equal(await readFile(projectPath, "utf8"), projectText);
});

test("a file that no longer parses, a server removed meanwhile, or a link outside are refused with the file untouched", async () => {
  await writeFile(globalPath, `{ "mcpServers": { "lint": { "env": { "T": '${SECRET}' } } } }`);
  const unparsable = await post({ action: "disable", scope: "global", name: "lint" });
  assert.deepEqual([unparsable.status, unparsable.body.reason, unparsable.body.path], [409, "unparsable", globalPath]);
  assert.ok(!JSON.stringify(unparsable.body).includes(SECRET), "the refusal never quotes the file");
  assert.match(await readFile(globalPath, "utf8"), new RegExp(SECRET));

  await writeFile(globalPath, globalText);
  const missing = await post({ action: "remove", scope: "global", name: "gone" });
  assert.deepEqual([missing.status, missing.body.reason, missing.body.name], [409, "server-missing", "gone"]);
  assert.equal((await post({ action: "disable", scope: "global", name: "gone" })).body.reason, "server-missing");

  if (process.platform !== "win32") {
    store.set(cwd, true);
    const target = join(outside, "mcp.json");
    await writeFile(target, projectText);
    await rm(projectPath);
    await symlink(target, projectPath);
    const linked = await post({ action: "disable", scope: "project", name: "repo", cwd });
    assert.deepEqual([linked.status, linked.body.reason], [409, "link-outside"]);
    assert.equal(await readFile(target, "utf8"), projectText);
  }
  assert.equal(await readFile(globalPath, "utf8"), globalText);
});

test("an entry that is not an object is refused for what it is, not as missing, and can be removed", async () => {
  const text = `${JSON.stringify({ mcpServers: { junk: "oops", lint: { command: "npx" } } }, null, 2)}\n`;
  await writeFile(globalPath, text);
  // The listing says so, so the panel can leave its switch off.
  const { body: overview } = await post({ action: "disable", scope: "global", name: "lint" });
  assert.equal(server(overview, "global", "junk").notAnObject, true);
  assert.equal(server(overview, "global", "lint").notAnObject, undefined);
  await writeFile(globalPath, text);
  for (const action of ["disable", "enable"]) {
    const refused = await post({ action, scope: "global", name: "junk" });
    assert.deepEqual([refused.status, refused.body.reason, refused.body.name, refused.body.path], [409, "entry-not-object", "junk", globalPath], action);
  }
  assert.equal(await readFile(globalPath, "utf8"), text);
  const bulk = await post({ action: "set-enabled", enabled: false, servers: [{ scope: "global", name: "junk" }, { scope: "global", name: "lint" }] });
  assert.equal(bulk.status, 200);
  assert.deepEqual(bulk.body.results.map(({ name, reason }) => [name, reason]), [["junk", "entry-not-object"], ["lint", undefined]]);
  assert.equal((await readJson(globalPath)).mcpServers.junk, "oops");
  const removed = await post({ action: "remove", scope: "global", name: "junk" });
  assert.equal(removed.status, 200);
  assert.equal(server(removed.body, "global", "junk"), undefined);
});

test("a global server is turned off and on in the project alone, through an override pi reads", async () => {
  // Only where a decision trusts the project, as for any project write.
  let { status, body } = await post({ action: "set-in-project", name: "lint", enabled: false, cwd });
  assert.equal(status, 403);
  assert.equal(body.reason, "project-untrusted");
  store.set(cwd, true);

  ({ status, body } = await post({ action: "set-in-project", name: "lint", enabled: false, cwd }));
  assert.equal(status, 200);
  assert.deepEqual((await readJson(projectPath)).mcpServers.lint, { enabled: false });
  assert.equal(await readFile(globalPath, "utf8"), globalText, "the global file is left alone");
  const override = server(body, "project", "lint");
  assert.deepEqual(override.override, { keys: ["enabled"] });
  assert.equal(override.enabled, false);
  assert.equal(override.command, "npx", "the row describes the global server it changes");
  assert.equal(server(body, "global", "lint").overriddenByProject, true);

  // On again keeps `enabled: true` written: it replaces the global value.
  ({ status, body } = await post({ action: "set-in-project", name: "lint", enabled: true, cwd }));
  assert.equal(status, 200);
  assert.deepEqual((await readJson(projectPath)).mcpServers.lint, { enabled: true });
  // The override's own switch and exposure keep the defaults written too.
  ({ status, body } = await post({ action: "disable", scope: "project", name: "lint", cwd }));
  ({ status, body } = await post({ action: "enable", scope: "project", name: "lint", cwd }));
  ({ status, body } = await post({ action: "set-exposure", scope: "project", name: "lint", exposure: "codemode", cwd }));
  assert.equal(status, 200);
  assert.deepEqual((await readJson(projectPath)).mcpServers.lint, { enabled: true, exposure: "codemode" });

  // Remove drops the override, and the global entry applies again.
  ({ status, body } = await post({ action: "remove", scope: "project", name: "lint", cwd }));
  assert.equal(status, 200);
  assert.equal(server(body, "project", "lint"), undefined);
  assert.equal(server(body, "global", "lint").overriddenByProject, undefined);

  // The project's own server of the name is not turned into an override.
  ({ status, body } = await post({ action: "set-in-project", name: "repo", enabled: false, cwd }));
  assert.equal(status, 409);
  assert.equal(body.reason, "server-missing", "repo is no global server");
  await writeFile(globalPath, JSON.stringify({ mcpServers: { ...JSON.parse(globalText).mcpServers, repo: { command: "g" } } }));
  ({ status, body } = await post({ action: "set-in-project", name: "repo", enabled: false, cwd }));
  assert.equal(status, 409);
  assert.equal(body.reason, "name-taken");
  assert.deepEqual((await readJson(projectPath)).mcpServers.repo, { command: "node", args: ["server.js"] });

  // A server that references PI_WEB_PASSWORD is never turned on, here either; off works.
  ({ status, body } = await post({ action: "set-in-project", name: "pw", enabled: true, cwd }));
  assert.equal(status, 409);
  assert.equal(body.reason, "web-password");
  ({ status } = await post({ action: "set-in-project", name: "pw", enabled: false, cwd }));
  assert.equal(status, 200);

  // It names a global server and a switch position, and needs the project.
  assert.equal((await post({ action: "set-in-project", name: "lint", cwd })).status, 400);
  assert.equal((await post({ action: "set-in-project", name: "lint", enabled: false })).status, 400);
});

// ---------------------------------------------------------------------------
// #85 85c: the unified list's claude half — apply-to, unify, and removing a
// claude entry. Claude is a stub on PATH recording its argv (AC2): Pi Web
// never writes .claude.json itself, so the claude side is asserted through
// the stub's log, and the pi files through their bytes.

const stubSource = new URL("../../../lib/__fixtures__/claude-2.1.326-mcp/mcp/stub-claude.mjs", import.meta.url).pathname;
const claudeConfigDir = join(root, "claude-cfg");
const claudeStatePath = join(claudeConfigDir, ".claude.json");
const dotMcpPath = join(cwd, ".mcp.json");
const previousPath = process.env.PATH;
const previousClaudeConfigDir = process.env.CLAUDE_CONFIG_DIR;
const previousStubLog = process.env.CLAUDE_MCP_STUB_LOG;
const previousStubFail = process.env.CLAUDE_MCP_STUB_FAIL;
const previousStubVersion = process.env.CLAUDE_MCP_STUB_VERSION;

/** The stub claude on PATH, its log and its config dir, for this one test. */
async function withClaudeStub(t, { version = "2.1.326" } = {}) {
  await mkdir(claudeConfigDir, { recursive: true });
  // One dir per version: the binary must be named `claude` (what
  // findClaudeBinary looks for), and probeClaude caches by its realpath.
  const binDir = join(root, `claude-bin-${version}`);
  await mkdir(binDir, { recursive: true });
  const binary = join(binDir, "claude");
  if (!existsSync(binary)) await copyFile(stubSource, binary);
  chmodSync(binary, 0o755);
  const log = join(root, `spawns-${version}-${t.name.replace(/\W+/g, "-")}.jsonl`);
  process.env.PATH = `${binDir}${delimiter}${previousPath ?? ""}`;
  process.env.CLAUDE_CONFIG_DIR = claudeConfigDir;
  process.env.CLAUDE_MCP_STUB_LOG = log;
  process.env.CLAUDE_MCP_STUB_VERSION = version;
  delete process.env.CLAUDE_MCP_STUB_FAIL;
  // claude's own shape for `claude mcp add-json -s user|local|project` (a real
  // 2.1.326 run): user and local in the state file, project in .mcp.json.
  await writeFile(claudeStatePath, JSON.stringify({
    mcpServers: {
      "shared-entry": { command: "npx", args: ["-y", "shared-one"], env: { A: "1", CLAUDE_ONLY_SECRET: "claude-literal-secret" } },
    },
    projects: {
      [cwd]: { mcpServers: { "local-entry": { type: "http", url: "https://local.example/mcp" } }, hasTrustDialogAccepted: false },
    },
  }, null, 2));
  await writeFile(dotMcpPath, JSON.stringify({ mcpServers: { "claude-proj": { command: "node", args: ["cp.js"] } } }, null, 2));
  t.after(() => {
    process.env.PATH = previousPath;
    if (previousClaudeConfigDir === undefined) delete process.env.CLAUDE_CONFIG_DIR;
    else process.env.CLAUDE_CONFIG_DIR = previousClaudeConfigDir;
    if (previousStubLog === undefined) delete process.env.CLAUDE_MCP_STUB_LOG;
    else process.env.CLAUDE_MCP_STUB_LOG = previousStubLog;
    if (previousStubFail === undefined) delete process.env.CLAUDE_MCP_STUB_FAIL;
    else process.env.CLAUDE_MCP_STUB_FAIL = previousStubFail;
    if (previousStubVersion === undefined) delete process.env.CLAUDE_MCP_STUB_VERSION;
    else process.env.CLAUDE_MCP_STUB_VERSION = previousStubVersion;
  });
  return { log, binary };
}

async function stubSpawns(log) {
  const text = await readFile(log, "utf8").catch(() => "");
  return text.split("\n").filter((line) => line !== "").map((line) => JSON.parse(line));
}

test("set-apply to pi alone removes only claude's copy; pi's files keep their bytes (AC3)", async (t) => {
  const { log } = await withClaudeStub(t);
  await writeFile(globalPath, JSON.stringify({
    mcpServers: { "shared-entry": { command: "npx", args: ["-y", "shared-one"], env: { A: "1" } } },
    keep: "unknown keys stay",
  }, null, 2));
  const before = await readFile(globalPath, "utf8");

  const { status, body } = await post({ action: "set-apply", scope: "global", name: "shared-entry", harnesses: ["pi"] });
  assert.equal(status, 200);
  assert.deepEqual((await stubSpawns(log)).map((spawn) => spawn.argv), [["mcp", "remove", "-s", "user", "shared-entry"]], "exactly one claude remove");
  assert.equal(await readFile(globalPath, "utf8"), before, "pi's mcp.json is byte-identical");
  assert.equal(JSON.stringify(body).includes("claude-literal-secret"), false);
  assert.ok(body.applied, "the answer reports per-harness results");
  assert.deepEqual(body.applied.results.map((result) => [result.harness, result.ok]), [["pi", true], ["claude", true]]);
});

test("set-apply to claude alone removes pi's copy only: no spawn, claude's files keep their bytes (AC3)", async (t) => {
  const { log } = await withClaudeStub(t);
  await writeFile(globalPath, JSON.stringify({ mcpServers: { "shared-entry": { command: "npx", args: ["-y", "shared-one"] } } }, null, 2));
  const stateBefore = await readFile(claudeStatePath, "utf8");
  const dotMcpBefore = await readFile(dotMcpPath, "utf8");

  const { status, body } = await post({ action: "set-apply", scope: "global", name: "shared-entry", harnesses: ["claude"] });
  assert.equal(status, 200);
  assert.deepEqual(await stubSpawns(log), [], "claude holds it, so keeping it there spawns nothing");
  assert.equal(await readFile(claudeStatePath, "utf8"), stateBefore);
  assert.equal(await readFile(dotMcpPath, "utf8"), dotMcpBefore);
  assert.deepEqual((await readJson(globalPath)).mcpServers, {}, "pi's copy is the one the change removes");
  assert.deepEqual(body.applied.results.map((result) => [result.harness, result.ok]), [["pi", true], ["claude", true]]);
});

test("set-apply adds a missing side from the other's native definition (AC3)", async (t) => {
  const { log } = await withClaudeStub(t);
  await writeFile(globalPath, JSON.stringify({ mcpServers: { "pi-own": { command: "npx", args: ["-y", "pi-one"], env: { TOKEN: "pi-raw" } } } }, null, 2));

  const { status, body } = await post({ action: "set-apply", scope: "global", name: "pi-own", harnesses: ["pi", "claude"] });
  assert.equal(status, 200);
  const spawns = await stubSpawns(log);
  assert.deepEqual(spawns.map((spawn) => spawn.argv), [
    ["mcp", "add-json", "-s", "user", "pi-own", JSON.stringify({ command: "npx", args: ["-y", "pi-one"], env: { TOKEN: "pi-raw" } })],
  ], "claude gets pi's definition, its own fields only, as one argv word");
  assert.equal(body.applied.results.find((result) => result.harness === "claude").ok, true);
});

test("a claude removal is undoable: the raw entry is held server-side and written back by undo (Q1 A)", async (t) => {
  const { log } = await withClaudeStub(t);
  const { status, body } = await post({ action: "remove", harness: "claude", scope: "global", name: "shared-entry" });
  assert.equal(status, 200);
  assert.ok(body.undo?.token);
  assert.equal(body.undo.harness, "claude");
  assert.deepEqual((await stubSpawns(log)).map((spawn) => spawn.argv), [["mcp", "remove", "-s", "user", "shared-entry"]]);

  const undone = await post({ action: "undo", token: body.undo.token });
  assert.equal(undone.status, 200);
  assert.deepEqual((await stubSpawns(log)).map((spawn) => spawn.argv), [
    ["mcp", "remove", "-s", "user", "shared-entry"],
    ["mcp", "add-json", "-s", "user", "shared-entry", JSON.stringify({ command: "npx", args: ["-y", "shared-one"], env: { A: "1", CLAUDE_ONLY_SECRET: "claude-literal-secret" } })],
  ], "undo writes the held raw definition back through the CLI");
});

test("unify writes only the other side, claude's as remove + add-json of the projected entry (AC3)", async (t) => {
  const { log } = await withClaudeStub(t);
  await writeFile(globalPath, JSON.stringify({
    mcpServers: { "shared-entry": { command: "npx", args: ["-y", "pi-version"], env: { A: "1" }, exposure: "direct" } },
  }, null, 2));
  const before = await readFile(globalPath, "utf8");

  const { status } = await post({ action: "unify", scope: "global", name: "shared-entry", from: "pi" });
  assert.equal(status, 200);
  assert.deepEqual((await stubSpawns(log)).map((spawn) => spawn.argv), [
    ["mcp", "remove", "-s", "user", "shared-entry"],
    ["mcp", "add-json", "-s", "user", "shared-entry", JSON.stringify({ command: "npx", args: ["-y", "pi-version"], env: { A: "1" } })],
  ], "pi's exposure never reaches claude (its CLI would drop it anyway)");
  assert.equal(await readFile(globalPath, "utf8"), before, "the side the value came from is not touched");
});

test("the local group is claude's alone: pi is refused as a target, claude removes work (Q2/Q3)", async (t) => {
  const { log } = await withClaudeStub(t);
  const toPi = await post({ action: "set-apply", scope: "local", name: "local-entry", harnesses: ["pi"], cwd });
  assert.equal(toPi.status, 409);
  assert.equal(toPi.body.reason, "harness-unsupported");
  assert.deepEqual(await stubSpawns(log), []);

  const { status, body } = await post({ action: "remove", harness: "claude", scope: "local", name: "local-entry", cwd });
  assert.equal(status, 200);
  assert.deepEqual((await stubSpawns(log)).map((spawn) => [spawn.argv, spawn.cwd]), [
    [["mcp", "remove", "-s", "local", "local-entry"], cwd],
  ], "claude's local scope is keyed by the project folder");
  assert.equal(body.undo.scope, "local");
});

test("a claude whose version the gate does not recognize is written to by nothing (AC2)", async (t) => {
  const { log } = await withClaudeStub(t, { version: "9.9.9" });
  await writeFile(globalPath, JSON.stringify({ mcpServers: { "shared-entry": { command: "npx", args: ["-y", "shared-one"] } } }, null, 2));
  const { status, body } = await post({ action: "set-apply", scope: "global", name: "shared-entry", harnesses: ["pi"] });
  assert.equal(status, 200);
  const claude = body.applied.results.find((result) => result.harness === "claude");
  assert.equal(claude.ok, false);
  assert.equal(claude.reason, "claude-unwritable");
  assert.deepEqual(await stubSpawns(log).then((spawns) => spawns.filter((spawn) => spawn.argv[0] === "mcp")), [], "nothing spawned for a version outside the range");
});
