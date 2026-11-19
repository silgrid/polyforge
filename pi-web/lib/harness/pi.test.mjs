// carries: lib/harness/pi.ts, lib/harness/redact.ts
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";
import { ProjectTrustStore } from "@earendil-works/pi-coding-agent";

// The pi adapter against a fixture agent directory (owner ruling Q6 A:
// fixtures are generated in the test, never committed). Expected values are
// hand-written from the ticket (polyforge#141 AC1/AC3, Q2 C, Q4 A); every
// payload is scanned for the sentinel strings placed in the credential
// files, so a leaked value turns this red.
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-harness-pi-"));
const agentDir = path.join(scratch, "agent");
const project = path.join(scratch, "project");
fs.mkdirSync(path.join(agentDir, "skills", "global-skill"), { recursive: true });
fs.mkdirSync(path.join(project, ".pi", "skills", "proj-skill"), { recursive: true });
fs.writeFileSync(path.join(agentDir, "settings.json"), JSON.stringify({
  defaultProvider: "anthropic",
  defaultModel: "claude-opus-4",
  defaultThinkingLevel: "high",
}));
fs.writeFileSync(path.join(agentDir, "models.json"), JSON.stringify({
  providers: {
    anthropic: { name: "anthropic", baseUrl: "https://api.anthropic.com", apiKey: "sk-ant-PI-SENTINEL-KEY" },
    openrouter: { apiKey: "or-PI-SENTINEL-2" },
    nokey: { baseUrl: "https://nokey.example" },
  },
}));
fs.writeFileSync(path.join(agentDir, "auth.json"), JSON.stringify({ apiKeys: { anthropic: "PI-SENTINEL-AUTH" } }));
fs.writeFileSync(path.join(agentDir, "mcp.json"), JSON.stringify({
  mcpServers: {
    github: { url: "https://mcp.github.com/?token=PI-SENTINEL-MCP", headers: { Authorization: "Bearer PI-SENTINEL-MCP-AUTH" } },
    local: { command: "npx", args: ["-y", "server", "--token", "PI-SENTINEL-ARG"], env: { SECRET: "PI-SENTINEL-ENV" } },
  },
}));
fs.writeFileSync(path.join(agentDir, "skills", "global-skill", "SKILL.md"), "---\nname: global-skill\ndescription: a fixture skill\n---\nbody\n");
fs.writeFileSync(path.join(project, ".pi", "settings.json"), JSON.stringify({ defaultThinkingLevel: "low" }));
fs.writeFileSync(path.join(project, ".pi", "mcp.json"), JSON.stringify({
  mcpServers: { projmcp: { command: "node", args: ["server.js"] } },
}));
fs.writeFileSync(path.join(project, ".pi", "skills", "proj-skill", "SKILL.md"), "---\nname: proj-skill\ndescription: a fixture project skill\n---\nbody\n");
new ProjectTrustStore(agentDir).set(project, true);
process.env.PI_CODING_AGENT_DIR = agentDir;
const SENTINELS = [
  "PI-SENTINEL-KEY", "PI-SENTINEL-2", "PI-SENTINEL-AUTH", "PI-SENTINEL-MCP",
  "PI-SENTINEL-MCP-AUTH", "PI-SENTINEL-ARG", "PI-SENTINEL-ENV", "PI-SENTINEL-GROQ",
];
test.after(() => {
  fs.rmSync(scratch, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const pi = await jiti.import("./pi.ts");
const adapter = pi.createPiAdapter();
const fixtureCtx = (extra = {}) => ({
  agentDir,
  cwd: project,
  // Explicit, not the runner's environment: the automation-env and setKeys
  // assertions must not depend on which credential variables the machine sets
  // (r1#1).
  env: { PI_CODING_AGENT_DIR: agentDir, GROQ_API_KEY: "PI-SENTINEL-GROQ" },
  ...extra,
});
const noSentinel = (payload) => {
  const text = JSON.stringify(payload);
  for (const sentinel of SENTINELS) assert.ok(!text.includes(sentinel), `payload leaks ${sentinel}: ${text}`);
};

test("probeVersion: the injected SDK version decides the known range (Q2 C)", async () => {
  const known = await adapter.probeVersion(fixtureCtx({ piVersion: "1.5.0" }));
  assert.equal(known.version, "1.5.0");
  assert.equal(known.known, true);
  assert.equal(known.knownRange, "pi 1.5.x");
  const unknown = await adapter.probeVersion(fixtureCtx({ piVersion: "1.4.0" }));
  assert.equal(unknown.known, false);
  assert.ok(unknown.reason?.includes("1.4.0"));
});

test("probeVersion without an injected version reads the installed SDK package", async () => {
  const probe = await adapter.probeVersion({ agentDir });
  const sdkPackage = JSON.parse(fs.readFileSync(path.join(process.cwd(), "node_modules/@earendil-works/pi-coding-agent/package.json"), "utf8"));
  assert.equal(probe.version, sdkPackage.version);
  assert.equal(probe.known, true);
});

test("probeVersion: an unrecognized top-level structure refuses a known-range version (Q2 C)", async () => {
  const settings = path.join(agentDir, "settings.json");
  const original = fs.readFileSync(settings, "utf8");
  try {
    fs.writeFileSync(settings, "[1, 2, 3]");
    const probe = await adapter.probeVersion(fixtureCtx({ piVersion: "1.5.0" }));
    assert.equal(probe.known, false);
    assert.ok(probe.reason?.includes("settings.json"));
  } finally {
    fs.writeFileSync(settings, original);
  }
  const models = path.join(agentDir, "models.json");
  const originalModels = fs.readFileSync(models, "utf8");
  try {
    fs.writeFileSync(models, JSON.stringify({ providers: "text" }));
    const probe = await adapter.probeVersion(fixtureCtx({ piVersion: "1.5.0" }));
    assert.equal(probe.known, false);
    assert.ok(probe.reason?.includes("models.json"));
  } finally {
    fs.writeFileSync(models, originalModels);
  }
});

test("write: an out-of-range version is refused with a reason; a known one answers not-implemented (Q1 A)", async () => {
  const refused = await adapter.write("model", {}, fixtureCtx({ piVersion: "1.4.0" }));
  assert.equal(refused.status, "refused");
  assert.ok(refused.reason.includes("1.4.0"));
  const notImplemented = await adapter.write("model", {}, fixtureCtx({ piVersion: "1.5.0" }));
  assert.equal(notImplemented.status, "not-implemented");
  assert.ok(notImplemented.reason.includes("model"));
});

test("capabilities: pi declares its concepts, scopes and config root (AC1)", () => {
  const caps = adapter.capabilities;
  assert.deepEqual(caps.concepts, ["auth", "config-root", "model", "permission", "skills", "mcp", "env", "effective-config"]);
  assert.deepEqual(caps.scopes, ["user", "project"]);
  assert.equal(caps.configRootEnv, "PI_CODING_AGENT_DIR");
  assert.equal(caps.credentialsFollowConfigRoot, true);
  assert.deepEqual(caps.writableConcepts, []);
  assert.deepEqual(caps.readOnlyConcepts, ["env", "effective-config"]);
});

test("read auth: existence and env names only, status unknown (Q4 A)", async () => {
  const payload = await adapter.read("auth", fixtureCtx());
  assert.deepEqual(payload, {
    status: "unknown",
    credentialStore: { exists: true, path: path.join(agentDir, "auth.json") },
    automationEnv: { ANTHROPIC_AUTH_TOKEN: false, ANTHROPIC_API_KEY: false, ANTHROPIC_OAUTH_TOKEN: false, OPENAI_API_KEY: false, AZURE_OPENAI_API_KEY: false, DEEPSEEK_API_KEY: false, NVIDIA_API_KEY: false, GEMINI_API_KEY: false, GROQ_API_KEY: true, CEREBRAS_API_KEY: false, XAI_API_KEY: false, FIREWORKS_API_KEY: false, TOGETHER_API_KEY: false, OPENROUTER_API_KEY: false, AI_GATEWAY_API_KEY: false, ZAI_API_KEY: false, MISTRAL_API_KEY: false, MINIMAX_API_KEY: false },
    credentialsFollow: true,
  });
  noSentinel(payload);
});

test("read config-root", async () => {
  const payload = await adapter.read("config-root", fixtureCtx());
  assert.deepEqual(payload, { path: agentDir, env: "PI_CODING_AGENT_DIR", credentialsFollow: true });
});

test("read model: defaults from settings.json, providers as existence only", async () => {
  const payload = await adapter.read("model", fixtureCtx());
  assert.deepEqual(payload, {
    defaultProvider: "anthropic",
    defaultModel: "claude-opus-4",
    reasoningEffort: { neutral: "high", raw: "high" },
    providers: [
      { name: "anthropic", hasApiKey: true },
      { name: "openrouter", hasApiKey: true },
      { name: "nokey", hasApiKey: false },
    ],
    customModels: [],
  });
  noSentinel(payload);
});

test("read permission: pi declares no approval mode; project trust from trust.json", async () => {
  const payload = await adapter.read("permission", fixtureCtx());
  assert.deepEqual(payload.approvalMode, { supported: false, neutral: null, raw: null });
  assert.equal(payload.projectTrust?.trusted, true);
  const noCwd = await adapter.read("permission", { agentDir, env: {} });
  assert.equal(noCwd.projectTrust, null);
});

test("read skills: name/path/source only, the loader's own scopes", async () => {
  const payload = await adapter.read("skills", fixtureCtx());
  const sources = new Map(payload.entries.map((entry) => [entry.source, entry]));
  assert.equal(sources.size, 2);
  assert.equal(sources.get("user").name, "global-skill");
  assert.equal(sources.get("user").path, path.join(agentDir, "skills", "global-skill", "SKILL.md"));
  assert.equal(sources.get("project").name, "proj-skill");
  assert.equal(sources.get("project").path, path.join(project, ".pi", "skills", "proj-skill", "SKILL.md"));
});

test("read mcp: masked url and args, header and env names without values", async () => {
  const payload = await adapter.read("mcp", fixtureCtx());
  const byName = new Map(payload.servers.map((server) => [server.name, server]));
  assert.deepEqual([...byName.keys()].sort(), ["github", "local", "projmcp"]);
  const github = byName.get("github");
  assert.equal(github.scope, "user");
  assert.equal(github.url.masked.includes("PI-SENTINEL-MCP"), false);
  assert.equal(github.url.hadSecret, true);
  assert.deepEqual(github.headerNames, ["Authorization"]);
  const local = byName.get("local");
  assert.deepEqual(local.command, "npx");
  assert.equal(local.args.masked, true);
  assert.deepEqual(local.envNames, ["SECRET"]);
  const projmcp = byName.get("projmcp");
  assert.equal(projmcp.scope, "project");
  noSentinel(payload);
});

test("read mcp without a cwd lists the global servers only", async () => {
  const payload = await adapter.read("mcp", { agentDir, env: {} });
  assert.deepEqual(payload.servers.map((server) => server.name).sort(), ["github", "local"]);
  noSentinel(payload);
});

test("read env: key names only, never values", async () => {
  const payload = await adapter.read("env", fixtureCtx());
  assert.equal(payload.target, "pi");
  assert.deepEqual(payload.setKeys, ["GROQ_API_KEY", "PI_CODING_AGENT_DIR"]);
});

test("read effective-config answers a payload (values land with the compare unit)", async () => {
  const payload = await adapter.read("effective-config", fixtureCtx());
  assert.ok(payload);
  assert.equal(typeof payload.fields, "object");
});

test("read of an unknown concept answers null", async () => {
  assert.equal(await adapter.read("no-such-concept", fixtureCtx()), null);
});

test("reads never create pi's settings lock files", async () => {
  for (const concept of ["auth", "config-root", "model", "permission", "skills", "mcp", "env", "effective-config"]) {
    await adapter.read(concept, fixtureCtx());
  }
  const locks = fs.readdirSync(agentDir).filter((entry) => entry.includes(".lock"));
  assert.deepEqual(locks, []);
});
