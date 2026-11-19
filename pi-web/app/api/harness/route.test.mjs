// carries: app/api/harness/route.ts, app/api/harness/[harness]/[concept]/route.ts, app/api/harness/compare/route.ts, lib/harness/route-context.ts
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// The three harness routes (AC3): the harness list with capabilities, every
// concept's read, and the comparison — every response scanned for the
// sentinel strings planted in the credential files (the ticket's list:
// claude settings env values, key-like fields in .claude.json,
// .credentials.json, pi's auth.json, models.json's apiKey, MCP headers and
// url parameters), and the stub log checked for a forbidden command. The
// claude on PATH is the unit stub answering initialize as 2.1.326.
const here = path.dirname(new URL(import.meta.url).pathname);
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-harness-route-"));
const agentDir = path.join(scratch, "agent");
const claudeConfigDir = path.join(scratch, "claude-config");
const project = path.join(scratch, "project");
const outside = path.join(scratch, "outside");
const binDir = path.join(scratch, "bin");
fs.mkdirSync(path.join(agentDir, "skills", "route-skill"), { recursive: true });
fs.mkdirSync(path.join(claudeConfigDir, "skills", "cl-skill"), { recursive: true });
fs.mkdirSync(path.join(project, ".pi"), { recursive: true });
fs.mkdirSync(path.join(project, ".claude", "skills", "p-skill"), { recursive: true });
fs.mkdirSync(outside);
fs.mkdirSync(binDir);
fs.writeFileSync(path.join(agentDir, "settings.json"), JSON.stringify({
  defaultProvider: "anthropic",
  defaultModel: "route-model",
  defaultThinkingLevel: "high",
}));
fs.writeFileSync(path.join(agentDir, "models.json"), JSON.stringify({
  providers: { anthropic: { apiKey: "sk-RT-SENTINEL-KEY" } },
}));
fs.writeFileSync(path.join(agentDir, "auth.json"), JSON.stringify({ apiKeys: { anthropic: "RT-SENTINEL-AUTH" } }));
fs.writeFileSync(path.join(agentDir, "mcp.json"), JSON.stringify({
  mcpServers: {
    pmcp: { url: "https://pmcp.example/?token=RT-SENTINEL-PI-MCP", headers: { Authorization: "Bearer RT-SENTINEL-PI-H" } },
  },
}));
fs.writeFileSync(path.join(agentDir, "skills", "route-skill", "SKILL.md"), "---\nname: route-skill\ndescription: x\n---\n");
fs.writeFileSync(path.join(claudeConfigDir, "settings.json"), JSON.stringify({
  model: "opus",
  effortLevel: "low",
  env: { ANTHROPIC_CUSTOM_HEADER: "RT-SENTINEL-ENV" },
  mcpServers: { cmcp: { url: "https://cmcp.example/?token=RT-SENTINEL-MCP-URL", headers: { Authorization: "RT-SENTINEL-MCP-H" } } },
  permissions: { defaultMode: "default" },
}));
fs.writeFileSync(path.join(claudeConfigDir, ".credentials.json"), JSON.stringify({ claudeAiOauth: { accessToken: "RT-SENTINEL-CRED" } }));
fs.writeFileSync(path.join(claudeConfigDir, ".claude.json"), JSON.stringify({
  apiKeyHelper: "RT-SENTINEL-HELPER",
  mcpServers: { smcp: { command: "npx", args: ["--token", "RT-SENTINEL-ARG"] } },
  projects: {},
}));
fs.writeFileSync(path.join(claudeConfigDir, "skills", "cl-skill", "SKILL.md"), "---\nname: cl-skill\ndescription: x\n---\n");
fs.writeFileSync(path.join(project, ".pi", "settings.json"), JSON.stringify({ defaultThinkingLevel: "low" }));
fs.writeFileSync(path.join(project, ".claude", "settings.json"), JSON.stringify({ model: "sonnet" }));
fs.writeFileSync(path.join(project, ".claude", "settings.local.json"), JSON.stringify({ effortLevel: "low" }));
fs.writeFileSync(path.join(project, ".claude", "skills", "p-skill", "SKILL.md"), "---\nname: p-skill\ndescription: x\n---\n");
fs.writeFileSync(path.join(project, ".mcp.json"), JSON.stringify({
  mcpServers: { dmcp: { url: "https://dmcp.example/?token=RT-SENTINEL-DOTMCP" } },
}));
const state = JSON.parse(fs.readFileSync(path.join(claudeConfigDir, ".claude.json"), "utf8"));
state.projects[project] = { hasTrustDialogAccepted: true };
fs.writeFileSync(path.join(claudeConfigDir, ".claude.json"), JSON.stringify(state));
// The unit claude stub, answering initialize from the 2.1.322 recording.
fs.copyFileSync(path.join(here, "..", "..", "..", "lib", "__fixtures__", "claude-stub", "claude.mjs"), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
fs.symlinkSync(path.join(here, "..", "..", "..", "lib", "__fixtures__", "claude-2.1.322"), path.join(scratch, "claude-2.1.322"));
const stubLog = path.join(scratch, "stub.jsonl");
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_TEST_CLAUDE_STUB_VERSION = "2.1.326";
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.CLAUDE_CONFIG_DIR = claudeConfigDir;
delete process.env.CLAUDE_CODE_DISABLE_BACKGROUND_TASKS;
const originalPath = process.env.PATH;
process.env.PATH = `${binDir}${path.delimiter}${originalPath}`;
const SENTINELS = [
  "RT-SENTINEL-KEY", "RT-SENTINEL-AUTH", "RT-SENTINEL-PI-MCP", "RT-SENTINEL-PI-H",
  "RT-SENTINEL-ENV", "RT-SENTINEL-MCP-URL", "RT-SENTINEL-MCP-H", "RT-SENTINEL-CRED",
  "RT-SENTINEL-HELPER", "RT-SENTINEL-ARG", "RT-SENTINEL-DOTMCP",
];
const CONCEPTS = ["auth", "config-root", "model", "permission", "skills", "mcp", "env", "effective-config"];
test.after(() => {
  process.env.PATH = originalPath;
  delete process.env.PI_TEST_CLAUDE_STUB_VERSION;
  delete process.env.CLAUDE_CONFIG_DIR;
  fs.rmSync(scratch, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const listRoute = await jiti.import("./route.ts");
const conceptRoute = await jiti.import("./[harness]/[concept]/route.ts");
const compareRoute = await jiti.import("./compare/route.ts");
const { allowFileRoot } = await jiti.import("../../../lib/file-access.ts");
globalThis.__piAllowedRootsCache = undefined;
for (const folder of [project, outside]) allowFileRoot(folder);

const request = (url) => new Request(`http://localhost${url}`, { headers: { host: "localhost" } });
const noSentinel = (text, what) => {
  for (const sentinel of SENTINELS) assert.ok(!text.includes(sentinel), `${what} leaks ${sentinel}: ${text}`);
};

test("the harness list answers both adapters with version, known and capabilities", async () => {
  const response = await listRoute.GET(request("/api/harness"));
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.deepEqual(body.harnesses.map((entry) => entry.id), ["pi", "claude"]);
  const [pi, claude] = body.harnesses;
  const sdkPackage = JSON.parse(fs.readFileSync(path.join(process.cwd(), "node_modules/@earendil-works/pi-coding-agent/package.json"), "utf8"));
  assert.equal(pi.version, sdkPackage.version);
  assert.equal(pi.versionKnown, true);
  assert.equal(pi.writable, true);
  assert.deepEqual(pi.capabilities.concepts, CONCEPTS);
  assert.equal(pi.capabilities.configRootEnv, "PI_CODING_AGENT_DIR");
  assert.equal(claude.version, "2.1.326");
  assert.equal(claude.versionKnown, true);
  assert.deepEqual(claude.capabilities.concepts, CONCEPTS);
  assert.equal(claude.capabilities.configRootEnv, "CLAUDE_CONFIG_DIR");
  noSentinel(JSON.stringify(body), "the harness list");
});

test("every concept read of both harnesses answers its payload with no sentinel anywhere", async () => {
  for (const harness of ["pi", "claude"]) {
    for (const concept of CONCEPTS) {
      const response = await conceptRoute.GET(
        request(`/api/harness/${harness}/${concept}?cwd=${encodeURIComponent(project)}`),
        { params: Promise.resolve({ harness, concept }) },
      );
      assert.equal(response.status, 200, `${harness}/${concept}`);
      const body = await response.json();
      assert.equal(body.harness, harness);
      assert.equal(body.concept, concept);
      assert.ok(body.data, `${harness}/${concept} read nothing`);
      noSentinel(JSON.stringify(body), `${harness}/${concept}`);
    }
  }
});

test("an unknown harness or concept answers 404", async () => {
  const noHarness = await conceptRoute.GET(request("/api/harness/nope/auth"), { params: Promise.resolve({ harness: "nope", concept: "auth" }) });
  assert.equal(noHarness.status, 404);
  const noConcept = await conceptRoute.GET(request("/api/harness/pi/nope"), { params: Promise.resolve({ harness: "pi", concept: "nope" }) });
  assert.equal(noConcept.status, 404);
});

test("a cwd outside the allowed roots answers 403, a relative one 400", async () => {
  const denied = await conceptRoute.GET(request(`/api/harness/pi/auth?cwd=${encodeURIComponent(path.join(scratch, "not-registered"))}`), { params: Promise.resolve({ harness: "pi", concept: "auth" }) });
  assert.equal(denied.status, 403);
  const relative = await conceptRoute.GET(request("/api/harness/pi/auth?cwd=relative/path"), { params: Promise.resolve({ harness: "pi", concept: "auth" }) });
  assert.equal(relative.status, 400);
});

test("a cross-site request is refused by the API guard", async () => {
  const crossSite = (url) => new Request(`http://localhost${url}`, { headers: { host: "localhost", "sec-fetch-site": "cross-site" } });
  const response = await listRoute.GET(crossSite("/api/harness"));
  assert.equal(response.status, 403);
  const concept = await conceptRoute.GET(crossSite("/api/harness/pi/auth"), { params: Promise.resolve({ harness: "pi", concept: "auth" }) });
  assert.equal(concept.status, 403);
  const comparison = await compareRoute.GET(crossSite("/api/harness/compare"));
  assert.equal(comparison.status, 403);
});

test("the compare route answers both fields, one field, and refuses an unknown one", async () => {
  const all = await compareRoute.GET(request(`/api/harness/compare?cwd=${encodeURIComponent(project)}`));
  assert.equal(all.status, 200);
  const body = await all.json();
  assert.deepEqual(body.results.map((entry) => entry.field), ["reasoning-effort", "approval-mode"]);
  assert.equal(body.results[0].status, "consistent");
  assert.deepEqual(body.results[0].values, {
    pi: { supported: true, neutral: "low", raw: "low" },
    claude: { supported: true, neutral: "low", raw: "low" },
  });
  assert.equal(body.results[1].status, "unsupported");
  noSentinel(JSON.stringify(body), "the compare route");
  const one = await compareRoute.GET(request(`/api/harness/compare?cwd=${encodeURIComponent(project)}&field=approval-mode`));
  const oneBody = await one.json();
  assert.deepEqual(oneBody.results.map((entry) => entry.field), ["approval-mode"]);
  const unknown = await compareRoute.GET(request(`/api/harness/compare?field=bogus`));
  assert.equal(unknown.status, 400);
});

test("the stub log shows no forbidden command", () => {
  const lines = fs.readFileSync(stubLog, "utf8").split("\n").filter(Boolean);
  assert.ok(lines.length > 0, "the stub was never launched");
  for (const line of lines) {
    const entry = JSON.parse(line);
    const argv = Array.isArray(entry.argv) ? entry.argv : [];
    assert.ok(!argv.some((arg) => arg.includes("auth")), `a forbidden command was launched: ${line}`);
    const stdin = Array.isArray(entry.stdin) ? entry.stdin : [];
    for (const frame of stdin) {
      assert.ok(!frame.includes("auth print"), `a forbidden command was requested: ${frame}`);
    }
  }
});
