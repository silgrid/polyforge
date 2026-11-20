// carries: app/api/models/default/route.ts, lib/default-model-preference.ts, lib/default-preferences.ts
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// Boundary regression for the review follow-up: a JSON body of null (or a
// primitive) must answer the controlled 400, never surface as a TypeError
// from reading properties off the parsed value. Beside it, 85a3-1's own bar:
// the star's 「harness + 模型」 combined default — the claude branch writes
// claude's native model through the adapter's gated write and the same
// combined setting, the pi branch writes pi's native defaults the same way,
// and the reasoning star's one write reaches both natives (一写多读) while a
// pi-specific level stays pi's alone. Expected values come from the ticket;
// the claude half runs against the unit claude stub answering 2.1.326.
const here = path.dirname(new URL(import.meta.url).pathname);
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-models-default-route-"));
const agentDir = path.join(scratch, "agent");
const claudeConfigDir = path.join(scratch, "claude-config");
const project = path.join(scratch, "project");
const binDir = path.join(scratch, "bin");
fs.mkdirSync(agentDir);
fs.mkdirSync(claudeConfigDir);
fs.mkdirSync(project);
fs.mkdirSync(binDir);
fs.writeFileSync(path.join(agentDir, "models.json"), JSON.stringify({
  providers: {
    acme: {
      baseUrl: "https://example.invalid/v1",
      apiKey: "sk-test",
      api: "openai-completions",
      models: [{ id: "one", name: "Acme One" }],
    },
  },
}, null, 2));
fs.writeFileSync(path.join(agentDir, "settings.json"), JSON.stringify({}));
fs.writeFileSync(path.join(claudeConfigDir, "settings.json"), JSON.stringify({
  env: { OTHER_FLAG: "kept" },
}));
fs.copyFileSync(path.join(here, "..", "..", "..", "lib", "__fixtures__", "claude-stub", "claude.mjs"), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
const stubLog = path.join(scratch, "stub.jsonl");
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_TEST_CLAUDE_STUB_VERSION = "2.1.326";
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.CLAUDE_CONFIG_DIR = claudeConfigDir;
process.env.PI_WEB_ALLOWED_ROOT_PREFIXES = os.tmpdir();
process.env.PI_OFFLINE = "1";
// Ambient provider auth must not add models to the scope check's answer.
for (const key of ["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "DEEPSEEK_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY"]) {
  delete process.env[key];
}
const originalPath = process.env.PATH;
process.env.PATH = `${binDir}${path.delimiter}${originalPath}`;

test.after(() => {
  process.env.PATH = originalPath;
  fs.rmSync(scratch, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { PUT, GET } = await jiti.import("./route.ts");

function call(body, method = "PUT") {
  const handler = method === "PUT" ? PUT : GET;
  return handler(new Request("http://localhost/api/models/default", {
    method,
    headers: { host: "localhost", "content-type": "application/json" },
    ...(method === "PUT" ? { body: typeof body === "string" ? body : JSON.stringify(body) } : {}),
  }));
}
const readJson = (p) => JSON.parse(fs.readFileSync(p, "utf8"));
const piSettings = path.join(agentDir, "settings.json");
const claudeSettings = path.join(claudeConfigDir, "settings.json");
const piWebSettings = path.join(agentDir, "pi-web-settings.json");

test("a JSON-null body answers the controlled 400, not a TypeError", async () => {
  const response = await call(null);
  assert.equal(response.status, 400);
  assert.deepEqual(await response.json(), { error: "Invalid JSON body" });
});

test("JSON-primitive bodies answer the controlled 400 as well", async () => {
  for (const primitive of [7, "plain text", true]) {
    const response = await call(primitive);
    assert.equal(response.status, 400, `body: ${JSON.stringify(primitive)}`);
  }
});

test("the claude star writes claude's native model and the same combined setting (AC2)", async () => {
  const response = await call({ harness: "claude", model: "opus" });
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { ok: true, harness: "claude", model: "opus" });
  assert.equal(readJson(claudeSettings).model, "opus");
  // the file's other keys stay
  assert.deepEqual(readJson(claudeSettings).env, { OTHER_FLAG: "kept" });
  assert.deepEqual(readJson(piWebSettings).defaultModel, { harness: "claude", claudeModel: "opus" });
});

test("the claude branch refuses an invalid model, and an unknown harness is refused before anything runs", async () => {
  const invalid = await call({ harness: "claude" });
  assert.equal(invalid.status, 400);
  assert.ok((await invalid.json()).error.includes("model"));
  const unknown = await call({ harness: "bogus" });
  assert.equal(unknown.status, 400);
  assert.ok((await unknown.json()).error.includes("bogus"));
});

test("the pi star writes the native defaults through the adapter and the same combined setting (AC2)", async () => {
  const response = await call({ cwd: project, provider: "acme", modelId: "one" });
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.ok, true);
  assert.deepEqual(body.defaultModel, { provider: "acme", modelId: "one" });
  const settings = readJson(piSettings);
  assert.equal(settings.defaultProvider, "acme");
  assert.equal(settings.defaultModel, "one");
  assert.deepEqual(readJson(piWebSettings).defaultModel, { harness: "pi", model: { provider: "acme", modelId: "one" } });
});

test("the reasoning star's one write reaches both natives (AC3 一写多读)", async () => {
  const response = await call({ cwd: project, thinkingLevel: "high" });
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.defaultThinkingLevel, "high");
  assert.deepEqual(body.unified, { pi: "written", claude: "written" });
  assert.equal(readJson(piSettings).defaultThinkingLevel, "high");
  assert.equal(readJson(claudeSettings).effortLevel, "high");
});

test("pi's own level stays pi's alone; the stored default is not a model choice", async () => {
  const response = await call({ cwd: project, thinkingLevel: "off" });
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.unified.claude, "skipped");
  assert.ok(body.unified.claudeReason.includes("pi's own"));
  assert.equal(readJson(piSettings).defaultThinkingLevel, "off");
  // claude's effortLevel keeps its own value (high, the unified write above)
  assert.equal(readJson(claudeSettings).effortLevel, "high");
  // the reasoning star alone leaves the combined default as the model star left it
  assert.deepEqual(readJson(piWebSettings).defaultModel, { harness: "pi", model: { provider: "acme", modelId: "one" } });
});

test("a claude half that cannot run never rolls pi's half back (the refusal is named, pi stays written)", async () => {
  const broken = JSON.stringify([1, 2, 3]);
  const original = fs.readFileSync(claudeSettings, "utf8");
  fs.writeFileSync(claudeSettings, broken);
  try {
    const response = await call({ cwd: project, thinkingLevel: "medium" });
    assert.equal(response.status, 200);
    const body = await response.json();
    assert.equal(body.unified.pi, "written");
    assert.equal(body.unified.claude, "refused");
    assert.ok(body.unified.claudeReason.includes("settings.json"), body.unified.claudeReason);
    assert.equal(readJson(piSettings).defaultThinkingLevel, "medium");
    assert.equal(fs.readFileSync(claudeSettings, "utf8"), broken);
  } finally {
    fs.writeFileSync(claudeSettings, original);
  }
});

test("GET answers the stored default harness and claude's native defaults (Q2 A)", async () => {
  const response = await call(null, "GET");
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), {
    defaultHarness: "pi",
    claudeDefaultModel: "opus",
    claudeDefaultEffort: "high",
  });
});

test("GET reads an unset claude native default as the alias and the unset effort", async () => {
  const original = fs.readFileSync(claudeSettings, "utf8");
  fs.writeFileSync(claudeSettings, JSON.stringify({ env: {} }));
  const piWebOriginal = fs.readFileSync(piWebSettings, "utf8");
  fs.writeFileSync(piWebSettings, JSON.stringify({ version: 1 }));
  try {
    const response = await call(null, "GET");
    assert.deepEqual(await response.json(), {
      defaultHarness: "pi",
      claudeDefaultModel: "default",
      claudeDefaultEffort: "default",
    });
  } finally {
    fs.writeFileSync(claudeSettings, original);
    fs.writeFileSync(piWebSettings, piWebOriginal);
  }
});
