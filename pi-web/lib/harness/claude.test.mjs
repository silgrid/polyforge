// carries: lib/harness/claude.ts, lib/harness/claude-native-write.ts
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// The claude adapter against a fixture config directory (Q6 A: fixtures are
// generated here, never committed). The version probe goes through the unit
// claude stub answering `initialize` (lib/__fixtures__/claude-stub), with
// PI_TEST_CLAUDE_STUB_VERSION replacing its claude_code_version — the same
// recording claude-probe.test.mjs uses. Expected values are hand-written from
// the ticket (AC1/AC3, Q2 C, Q4 A) and claude's settings-layer order
// (managed > .claude/settings.local.json > .claude/settings.json >
// $CLAUDE_CONFIG_DIR/settings.json); every payload is scanned for the
// sentinel strings placed in the credential files.
const here = path.dirname(new URL(import.meta.url).pathname);
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-harness-claude-"));
const configDir = path.join(scratch, "claude-config");
const agentDir = path.join(scratch, "agent");
const project = path.join(scratch, "project");
fs.mkdirSync(path.join(configDir, "skills", "global-cl-skill"), { recursive: true });
fs.mkdirSync(path.join(project, ".claude", "skills", "proj-cl-skill"), { recursive: true });
fs.mkdirSync(agentDir);
fs.writeFileSync(path.join(configDir, "settings.json"), JSON.stringify({
  model: "opus",
  effortLevel: "high",
  env: { ANTHROPIC_CUSTOM_HEADER: "CL-SENTINEL-ENV-VALUE", OTHER_FLAG: "plain-value" },
  mcpServers: {
    "global-mcp": { url: "https://mcp.example/?key=CL-SENTINEL-MCP-URL", headers: { "X-Secret": "CL-SENTINEL-MCP-HEADER" } },
  },
  permissions: { defaultMode: "acceptEdits" },
  skillOverrides: { "disabled-skill": "disabled" },
  // 85a4: the custom names are claude's own native modelPicker.options (the
  // retired pi-web-stored list migrated into the user layer).
  modelPicker: { options: [{ model: "claude-custom-1" }] },
}));
fs.writeFileSync(path.join(configDir, ".credentials.json"), JSON.stringify({ claudeAiOauth: { accessToken: "CL-SENTINEL-CRED" } }));
fs.writeFileSync(path.join(configDir, ".claude.json"), JSON.stringify({
  mcpServers: { "state-mcp": { command: "npx", args: ["-y", "s", "--token", "CL-SENTINEL-MCP-ARG"] } },
  apiKeyHelper: "CL-SENTINEL-KEY-HELPER",
  projects: {},
}));
fs.writeFileSync(path.join(configDir, "skills", "global-cl-skill", "SKILL.md"), "---\nname: global-cl-skill\ndescription: a fixture skill\n---\nbody\n");
fs.writeFileSync(path.join(project, ".claude", "settings.json"), JSON.stringify({
  model: "sonnet",
  effortLevel: "medium",
  mcpServers: { "proj-mcp": { command: "node", args: ["proj-server.js"] } },
}));
fs.writeFileSync(path.join(project, ".claude", "settings.local.json"), JSON.stringify({
  effortLevel: "low",
  env: { LOCAL_VAR: "CL-SENTINEL-LOCAL-VALUE" },
  mcpServers: { "local-mcp": { command: "node", args: ["local-server.js"] } },
}));
fs.writeFileSync(path.join(project, ".claude", "skills", "proj-cl-skill", "SKILL.md"), "---\nname: proj-cl-skill\ndescription: a fixture project skill\n---\nbody\n");
fs.writeFileSync(path.join(project, ".mcp.json"), JSON.stringify({
  mcpServers: { dotmcp: { url: "https://dotmcp.example/?token=CL-SENTINEL-DOTMCP" } },
}));

// Project trust: the state file's projects entry.
const statePath = path.join(configDir, ".claude.json");
const state = JSON.parse(fs.readFileSync(statePath, "utf8"));
state.projects[project] = { hasTrustDialogAccepted: true };
fs.writeFileSync(statePath, JSON.stringify(state));

const stubLog = path.join(scratch, "stub.jsonl");
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_CODING_AGENT_DIR = agentDir;
delete process.env.CLAUDE_CODE_DISABLE_BACKGROUND_TASKS;
// The managed settings path is injected per ctx (r1#5): the real system path
// must not leak a machine's managed layer into these expectations.
const managedSettings = path.join(scratch, "managed-settings.json");
const SENTINELS = [
  "CL-SENTINEL-CRED", "CL-SENTINEL-ENV-VALUE", "CL-SENTINEL-LOCAL-VALUE", "CL-SENTINEL-MCP-URL",
  "CL-SENTINEL-MCP-HEADER", "CL-SENTINEL-MCP-ARG", "CL-SENTINEL-DOTMCP", "CL-SENTINEL-KEY-HELPER",
  "CL-SENTINEL-OAUTH",
];
test.after(() => {
  fs.rmSync(scratch, { recursive: true, force: true });
});

/** A stub `claude` that answers initialize with the given claude_code_version, through the unit stub. */
function installVersionedStub(name, version) {
  const dir = path.join(scratch, name);
  fs.mkdirSync(dir);
  const binary = path.join(dir, "claude");
  fs.writeFileSync(binary, `#!/bin/sh\nexport PI_TEST_CLAUDE_STUB_VERSION=${version}\nexec node ${path.join(here, "..", "__fixtures__", "claude-stub", "claude.mjs")} "$@"\n`, { mode: 0o755 });
  return binary;
}
const okBinary = installVersionedStub("ok-bin", "2.1.326");
const unknownBinary = installVersionedStub("unknown-bin", "3.0.0");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const claude = await jiti.import("./claude.ts");
const claudeLayers = await jiti.import("./claude-layers.ts");
const adapter = claude.createClaudeAdapter();
const fixtureCtx = (extra = {}) => ({
  claudeConfigDir: configDir,
  agentDir,
  cwd: project,
  claudeBinary: okBinary,
  // Explicit, not the runner's environment: the automation-env assertion must
  // not depend on which credential variables the machine sets (r1#1).
  env: { CLAUDE_CODE_OAUTH_TOKEN: "CL-SENTINEL-OAUTH" },
  managedSettingsPath: managedSettings,
  ...extra,
});
const noSentinel = (payload) => {
  const text = JSON.stringify(payload);
  for (const sentinel of SENTINELS) assert.ok(!text.includes(sentinel), `payload leaks ${sentinel}: ${text}`);
};

test("probeVersion: 2.1.326 is known, 3.0.0 is refused with its reason (Q2 C)", async () => {
  const known = await adapter.probeVersion(fixtureCtx());
  assert.equal(known.version, "2.1.326");
  assert.equal(known.known, true);
  assert.equal(known.knownRange, "claude 2.x");
  const unknown = await adapter.probeVersion(fixtureCtx({ claudeBinary: unknownBinary }));
  assert.equal(unknown.version, "3.0.0");
  assert.equal(unknown.known, false);
  assert.ok(unknown.reason?.includes("3.0.0"));
});

test("probeVersion goes through probeClaude's one launch: the stub log shows the probe argv and initialize request", async () => {
  const log = fs.readFileSync(stubLog, "utf8");
  const launches = log.split("\n").filter(Boolean).map((line) => JSON.parse(line));
  const probe = launches.find((entry) => Array.isArray(entry.argv) && entry.argv.includes("--input-format"));
  assert.ok(probe, "the probe launch is missing from the stub log");
  assert.deepEqual(probe.argv, ["-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--no-session-persistence", "--permission-prompts", "none"]);
});

test("probeVersion: an unrecognized top-level structure refuses a known-range version (Q2 C)", async () => {
  const settingsPath = path.join(configDir, "settings.json");
  const original = fs.readFileSync(settingsPath, "utf8");
  try {
    fs.writeFileSync(settingsPath, "[1, 2, 3]");
    const probe = await adapter.probeVersion(fixtureCtx());
    assert.equal(probe.known, false);
    assert.ok(probe.reason?.includes("settings.json"));
  } finally {
    fs.writeFileSync(settingsPath, original);
  }
});

test("write: an unknown-major version is refused with a reason; the model write parses (85a3)", async () => {
  const refused = await adapter.write("model", {}, fixtureCtx({ claudeBinary: unknownBinary }));
  assert.equal(refused.status, "refused");
  assert.ok(refused.reason.includes("3.0.0"));
  const empty = await adapter.write("model", {}, fixtureCtx());
  assert.equal(empty.status, "refused");
  assert.ok(empty.reason.includes("empty"), empty.reason);
});

test("capabilities: claude declares its concepts, scopes, config root, login commands and the auth write (85a2)", () => {
  const caps = adapter.capabilities;
  assert.deepEqual(caps.concepts, ["auth", "config-root", "model", "permission", "skills", "mcp", "env", "effective-config"]);
  assert.deepEqual(caps.scopes, ["managed", "user", "project", "local"]);
  assert.equal(caps.configRootEnv, "CLAUDE_CONFIG_DIR");
  // credentialsFollowConfigRoot is platform-derived (AC7): darwin's Keychain
  // does not follow; the account tests pin the function, this pins the
  // declared value on the test platform (linux)
  assert.equal(caps.credentialsFollowConfigRoot, process.platform !== "darwin");
  assert.deepEqual(caps.writableConcepts, ["auth", "permission", "model"]);
  // the terminal login whitelist's data source (85a2 AC3): the interactive
  // session's two commands, none Cell-scoped (the 2026-11-19 Q3 revision)
  assert.deepEqual(caps.loginCommands, [
    { id: "auth-login", argv: ["claude", "auth", "login"], cell: false },
    { id: "setup-token", argv: ["claude", "setup-token"], cell: false },
  ]);
});

// ── the auth write (85a2 AC6): the credential lands in the native env
// block of the target's settings.json, the file's other keys kept, an
// unrecognized format refused, the value never echoed ──────────────────
const writeDir = path.join(scratch, "write-config");
fs.mkdirSync(writeDir);
fs.writeFileSync(path.join(writeDir, "settings.json"), JSON.stringify({
  model: "opus",
  env: { OTHER_FLAG: "kept-value" },
  permissions: { defaultMode: "default" },
}));

test("write auth: the credential lands in the env block, the file's other keys kept, the value never echoed", async () => {
  const result = await adapter.write("auth", { kind: "gateway", value: "CL-SENTINEL-WRITE-TOKEN", baseUrl: "https://gw.example" }, fixtureCtx({ claudeConfigDir: writeDir }));
  assert.equal(result.status, "written");
  assert.ok(!JSON.stringify(result).includes("CL-SENTINEL-WRITE-TOKEN"), "the write result echoes the value");
  assert.ok(result.summary && result.summary.includes("ANTHROPIC_AUTH_TOKEN"), result.summary);
  const written = JSON.parse(fs.readFileSync(path.join(writeDir, "settings.json"), "utf8"));
  assert.equal(written.model, "opus");
  assert.equal(written.permissions.defaultMode, "default");
  assert.equal(written.env.OTHER_FLAG, "kept-value");
  assert.equal(written.env.ANTHROPIC_BASE_URL, "https://gw.example");
  assert.equal(written.env.ANTHROPIC_AUTH_TOKEN, "CL-SENTINEL-WRITE-TOKEN");
  // the oauth-token and api-key kinds write their own key
  await adapter.write("auth", { kind: "oauth-token", value: "sk-ant-oat01-CL-SENTINEL" }, fixtureCtx({ claudeConfigDir: writeDir }));
  const after = JSON.parse(fs.readFileSync(path.join(writeDir, "settings.json"), "utf8"));
  assert.equal(after.env.CLAUDE_CODE_OAUTH_TOKEN, "sk-ant-oat01-CL-SENTINEL");
  assert.equal(after.env.ANTHROPIC_AUTH_TOKEN, "CL-SENTINEL-WRITE-TOKEN");
  await adapter.write("auth", { kind: "api-key", value: "sk-CL-SENTINEL-KEY" }, fixtureCtx({ claudeConfigDir: writeDir }));
  const last = JSON.parse(fs.readFileSync(path.join(writeDir, "settings.json"), "utf8"));
  assert.equal(last.env.ANTHROPIC_API_KEY, "sk-CL-SENTINEL-KEY");
});

test("write auth: a malformed change and an unrecognized settings format are refused, nothing written", async () => {
  for (const change of [{}, { kind: "no-such-kind", value: "x" }, { kind: "api-key" }, { kind: "gateway", value: "x" }]) {
    const refused = await adapter.write("auth", change, fixtureCtx({ claudeConfigDir: writeDir }));
    assert.equal(refused.status, "refused", JSON.stringify(change));
    assert.ok(refused.reason && !refused.reason.includes("SENTINEL"), refused.reason);
  }
  const tornDir = path.join(scratch, "torn-config");
  fs.mkdirSync(tornDir);
  fs.writeFileSync(path.join(tornDir, "settings.json"), "{not json");
  const before = fs.readFileSync(path.join(tornDir, "settings.json"), "utf8");
  const refused = await adapter.write("auth", { kind: "api-key", value: "CL-SENTINEL-TORN" }, fixtureCtx({ claudeConfigDir: tornDir }));
  assert.equal(refused.status, "refused");
  assert.ok(refused.reason?.includes("unrecognized"), refused.reason);
  assert.ok(!refused.reason?.includes("SENTINEL"));
  assert.equal(fs.readFileSync(path.join(tornDir, "settings.json"), "utf8"), before);
  // the write gate still refuses an unknown version first
  const gated = await adapter.write("auth", { kind: "api-key", value: "x" }, fixtureCtx({ claudeBinary: unknownBinary, claudeConfigDir: writeDir }));
  assert.equal(gated.status, "refused");
  assert.ok(gated.reason.includes("3.0.0"));
});

test("read auth: the credentials file as existence only, env names only, status unknown (Q4 A)", async () => {
  const payload = await adapter.read("auth", fixtureCtx());
  assert.deepEqual(payload, {
    status: "unknown",
    credentialStore: { exists: true, path: path.join(configDir, ".credentials.json") },
    automationEnv: { ANTHROPIC_API_KEY: false, ANTHROPIC_AUTH_TOKEN: false, CLAUDE_CODE_OAUTH_TOKEN: true },
    credentialsFollow: true,
  });
  noSentinel(payload);
});

test("read config-root", async () => {
  const payload = await adapter.read("config-root", fixtureCtx());
  assert.deepEqual(payload, { path: configDir, env: "CLAUDE_CONFIG_DIR", credentialsFollow: true });
});

test("read model: layers in claude's order, local over project over user", async () => {
  const payload = await adapter.read("model", fixtureCtx());
  assert.deepEqual(payload, {
    defaultProvider: null,
    defaultModel: "sonnet",
    reasoningEffort: { neutral: "low", raw: "low" },
    perModelReasoningEffort: {},
    providers: [],
    customModels: ["claude-custom-1"],
  });
});

test("read permission: the effective defaultMode and the state file's project trust", async () => {
  const payload = await adapter.read("permission", fixtureCtx());
  assert.deepEqual(payload.approvalMode, { supported: true, neutral: "accept-edits", raw: "acceptEdits" });
  assert.equal(payload.projectTrust?.trusted, true);
  const noCwd = await adapter.read("permission", { claudeConfigDir: configDir, agentDir, claudeBinary: okBinary, env: {}, managedSettingsPath: managedSettings });
  assert.equal(noCwd.projectTrust, null);
});

test("read skills: directories plus the native skillOverrides", async () => {
  const payload = await adapter.read("skills", fixtureCtx());
  const sources = new Map(payload.entries.map((entry) => [entry.source, entry]));
  assert.equal(sources.size, 2);
  assert.equal(sources.get("user").name, "global-cl-skill");
  assert.equal(sources.get("user").path, path.join(configDir, "skills", "global-cl-skill", "SKILL.md"));
  assert.equal(sources.get("project").name, "proj-cl-skill");
  assert.deepEqual(payload.overrides, { "disabled-skill": "disabled" });
});

test("read mcp: every layer masked, header and env names without values", async () => {
  const payload = await adapter.read("mcp", fixtureCtx());
  const byName = new Map(payload.servers.map((server) => [server.name, server]));
  assert.deepEqual([...byName.keys()].sort(), ["dotmcp", "global-mcp", "local-mcp", "proj-mcp", "state-mcp"]);
  assert.equal(byName.get("global-mcp").scope, "user");
  assert.equal(byName.get("global-mcp").url.hadSecret, true);
  assert.deepEqual(byName.get("global-mcp").headerNames, ["X-Secret"]);
  assert.equal(byName.get("state-mcp").scope, "user");
  assert.equal(byName.get("state-mcp").args.masked, true);
  assert.equal(byName.get("local-mcp").scope, "local");
  assert.equal(byName.get("proj-mcp").scope, "project");
  assert.equal(byName.get("dotmcp").scope, "project");
  assert.equal(byName.get("dotmcp").url.hadSecret, true);
  noSentinel(payload);
});

test("read env: the settings layers' env block as key names only", async () => {
  const payload = await adapter.read("env", fixtureCtx());
  assert.equal(payload.target, "claude");
  assert.deepEqual(payload.setKeys, ["ANTHROPIC_CUSTOM_HEADER", "LOCAL_VAR", "OTHER_FLAG"]);
});

test("read effective-config answers a payload (values land with the compare unit)", async () => {
  const payload = await adapter.read("effective-config", fixtureCtx());
  assert.ok(payload);
  assert.equal(typeof payload.fields, "object");
});

test("read of an unknown concept answers null", async () => {
  assert.equal(await adapter.read("no-such-concept", fixtureCtx()), null);
});

test("the state file follows $CLAUDE_CONFIG_DIR and otherwise lives at ~/.claude.json (self-review finding: the reads must resolve it the same way the probe does)", async () => {
  assert.equal(claudeLayers.claudeStateFilePath({ env: { CLAUDE_CONFIG_DIR: "/tmp/x" } }), "/tmp/x/.claude.json");
  assert.equal(claudeLayers.claudeStateFilePath({ env: {} }), path.join(os.homedir(), ".claude.json"));
  // A permission read without the claudeConfigDir override resolves both the
  // config root and the state file through ctx.env, like the probe does.
  const payload = await adapter.read("permission", { agentDir, cwd: project, claudeBinary: okBinary, env: { CLAUDE_CONFIG_DIR: configDir }, managedSettingsPath: managedSettings });
  assert.equal(payload.projectTrust?.trusted, true);
});

test("with CLAUDE_CONFIG_DIR unset the reads still find the state file at ~/.claude.json (pins the 308c6020 fix; r1#2)", async () => {
  // #test-env isolates HOME to a private directory, so ~/.claude.json is ours to write.
  const homeStatePath = path.join(os.homedir(), ".claude.json");
  assert.ok(!fs.existsSync(path.join(os.homedir(), ".claude", ".claude.json")), "the old code read <config dir>/.claude.json; it must not exist here");
  fs.writeFileSync(homeStatePath, JSON.stringify({
    mcpServers: { homestate: { command: "node", args: ["home-server.js"] } },
    projects: { [project]: { hasTrustDialogAccepted: true } },
  }));
  try {
    const ctx = { agentDir, cwd: project, claudeBinary: okBinary, env: {}, managedSettingsPath: managedSettings };
    const permission = await adapter.read("permission", ctx);
    // Red on 308c6020^: the old read resolved the state file as
    // <config dir>/.claude.json (~/.claude/.claude.json, absent), so the state
    // entry was never found and trusted read false.
    assert.equal(permission.projectTrust?.trusted, true);
    const mcp = await adapter.read("mcp", ctx);
    // Red on 308c6020^ for the same reason: the homestate server was absent.
    assert.ok(mcp.servers.some((server) => server.name === "homestate"), `homestate missing: ${JSON.stringify(mcp)}`);
  } finally {
    fs.rmSync(homeStatePath, { force: true });
  }
});

test("a higher layer with permissions but no defaultMode does not shadow a lower layer's defaultMode (pins the 308c6020 fix; r1#2)", async () => {
  const localPath = path.join(project, ".claude", "settings.local.json");
  const original = fs.readFileSync(localPath, "utf8");
  try {
    fs.writeFileSync(localPath, JSON.stringify({ permissions: { allow: ["Bash"] } }));
    const payload = await adapter.read("permission", fixtureCtx());
    // Red on 308c6020^: the old read took the first layer holding a
    // `permissions` key — the local layer, whose permissions has no
    // defaultMode — so raw read null instead of the user layer's acceptEdits.
    assert.deepEqual(payload.approvalMode, { supported: true, neutral: "accept-edits", raw: "acceptEdits" });
  } finally {
    fs.writeFileSync(localPath, original);
  }
});
