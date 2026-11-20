// carries: lib/harness/adapter.ts, lib/harness/concepts.ts, lib/harness/pi.ts, lib/harness/pi-native-write.ts, lib/harness/claude.ts, lib/harness/claude-native-write.ts
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// 85a3-1 AC1: the adapters read and write the model keys through the native
// files. Expected values are hand-written from the ticket and pi's settings
// schema (defaultProvider/defaultModel/defaultThinkingLevel, modelThinkingLevels
// keyed "provider/modelId") and claude's settings layers (model, effortLevel,
// modelSettings keyed by model name) — never from the implementation's output.
// The write gate recognizes the same major (owner ruling Q4: an unknown major
// or an unrecognized shape refuses, the file untouched); each model key carries
// its registered minimum version (pi 1.5.0, the installed SDK's schema; claude
// 2.1.312, the first recorded version). Fixtures are generated here, never
// committed; every payload is scanned for the sentinel strings planted in the
// credential files.
const here = path.dirname(new URL(import.meta.url).pathname);
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-harness-model-85a3-"));
const SENTINELS = ["PI85-SENTINEL-KEY", "PI85-SENTINEL-AUTH", "CL85-SENTINEL-CRED"];
const noSentinel = (value) => {
  const text = JSON.stringify(value);
  for (const sentinel of SENTINELS) assert.ok(!text.includes(sentinel), `payload leaks ${sentinel}: ${text}`);
};
const readJson = (p) => JSON.parse(fs.readFileSync(p, "utf8"));
const writeJson = (p, value) => fs.writeFileSync(p, `${JSON.stringify(value, null, 2)}\n`);

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const adapterMod = await jiti.import("./harness/adapter.ts");
const piAdapter = (await jiti.import("./harness/pi.ts")).createPiAdapter();
const claudeAdapter = (await jiti.import("./harness/claude.ts")).createClaudeAdapter();

test.after(() => {
  fs.rmSync(scratch, { recursive: true, force: true });
});

// ── the pi adapter: fixture agent dir ────────────────────────────────────
const piDir = path.join(scratch, "pi-agent");
fs.mkdirSync(piDir);
const piSettings = path.join(piDir, "settings.json");
writeJson(piSettings, {
  defaultProvider: "anthropic",
  defaultModel: "claude-opus-4",
  defaultThinkingLevel: "high",
  modelThinkingLevels: { "openrouter/qwen3": "low" },
  enabledModels: ["anthropic/*"],
});
const piModels = path.join(piDir, "models.json");
writeJson(piModels, { providers: { anthropic: { apiKey: "PI85-SENTINEL-KEY", baseUrl: "https://api.anthropic.com" } } });
writeJson(path.join(piDir, "auth.json"), { apiKeys: { anthropic: "PI85-SENTINEL-AUTH" } });
const piCtx = (extra = {}) => ({ agentDir: piDir, env: {}, ...extra });

test("pi provider definitions write through the adapter gate into models.json", async () => {
  const fixture = path.join(scratch, "provider-write");
  fs.mkdirSync(fixture);
  const ctx = { agentDir: fixture, env: {}, piVersion: "1.5.0" };
  writeJson(path.join(fixture, "settings.json"), { defaultModel: "keep-default" });
  const config = { providers: { gateway: { baseUrl: "https://gateway.example/v1", models: [{ id: "adaptive", reasoning: true, compat: { forceAdaptiveThinking: true } }] } } };
  const result = await piAdapter.write("model", { modelsConfig: config }, ctx);
  assert.equal(result.status, "written");
  assert.deepEqual(readJson(path.join(fixture, "models.json")), config);
  assert.deepEqual(readJson(path.join(fixture, "settings.json")), { defaultModel: "keep-default" });
  const before = fs.readFileSync(path.join(fixture, "models.json"), "utf8");
  const refused = await piAdapter.write("model", { modelsConfig: { providers: {} } }, { ...ctx, piVersion: "2.0.0" });
  assert.equal(refused.status, "refused");
  assert.equal(fs.readFileSync(path.join(fixture, "models.json"), "utf8"), before);
});

test("pi reads the model defaults and the per-model thinking levels from settings.json", async () => {
  const payload = await piAdapter.read("model", piCtx());
  assert.equal(payload.defaultProvider, "anthropic");
  assert.equal(payload.defaultModel, "claude-opus-4");
  assert.deepEqual(payload.reasoningEffort, { neutral: "high", raw: "high" });
  assert.deepEqual(payload.perModelReasoningEffort, { "openrouter/qwen3": { neutral: "low", raw: "low" } });
  assert.equal(payload.providers.length, 1);
  noSentinel(payload);
});

test("pi writes the default model, keeping the file's other keys and models.json untouched", async () => {
  const result = await piAdapter.write("model", { model: { provider: "openrouter", modelId: "qwen3" } }, piCtx());
  assert.equal(result.status, "written");
  const settings = readJson(piSettings);
  assert.equal(settings.defaultProvider, "openrouter");
  assert.equal(settings.defaultModel, "qwen3");
  // unrelated keys survive the merge
  assert.deepEqual(settings.enabledModels, ["anthropic/*"]);
  assert.deepEqual(settings.modelThinkingLevels, { "openrouter/qwen3": "low" });
  // models.json and the credential store are not model-default writes
  assert.deepEqual(readJson(piModels).providers.anthropic, { apiKey: "PI85-SENTINEL-KEY", baseUrl: "https://api.anthropic.com" });
  assert.ok(!JSON.stringify(result).includes("PI85-SENTINEL-KEY"), "the write result echoes a credential");
});

test("pi writes and removes the default thinking level", async () => {
  const set = await piAdapter.write("model", { thinkingLevel: "max" }, piCtx());
  assert.equal(set.status, "written");
  assert.equal(readJson(piSettings).defaultThinkingLevel, "max");
  const removed = await piAdapter.write("model", { thinkingLevel: null }, piCtx());
  assert.equal(removed.status, "written");
  assert.equal("defaultThinkingLevel" in readJson(piSettings), false);
});

test("pi writes and removes one model's thinking level, pruning empty containers", async () => {
  const set = await piAdapter.write("model", { modelThinkingLevel: { key: "anthropic/claude-opus-4", level: "xhigh" } }, piCtx());
  assert.equal(set.status, "written");
  const settings = readJson(piSettings);
  assert.deepEqual(settings.modelThinkingLevels, { "openrouter/qwen3": "low", "anthropic/claude-opus-4": "xhigh" });
  const removed = await piAdapter.write("model", { modelThinkingLevel: { key: "anthropic/claude-opus-4", level: null } }, piCtx());
  assert.equal(removed.status, "written");
  assert.deepEqual(readJson(piSettings).modelThinkingLevels, { "openrouter/qwen3": "low" });
});

test("pi refuses an unknown-major version, keeping settings.json untouched", async () => {
  const before = fs.readFileSync(piSettings, "utf8");
  const refused = await piAdapter.write("model", { thinkingLevel: "low" }, piCtx({ piVersion: "2.0.0" }));
  assert.equal(refused.status, "refused");
  assert.ok(refused.reason.includes("2.0.0"), refused.reason);
  assert.equal(fs.readFileSync(piSettings, "utf8"), before);
});

test("pi recognizes the same major: 1.9.0 writes, 1.4.0 is below the model keys' registered minimum", async () => {
  const written = await piAdapter.write("model", { thinkingLevel: "low" }, piCtx({ piVersion: "1.9.0" }));
  assert.equal(written.status, "written");
  assert.equal(readJson(piSettings).defaultThinkingLevel, "low");
  const before = fs.readFileSync(piSettings, "utf8");
  const below = await piAdapter.write("model", { thinkingLevel: "low" }, piCtx({ piVersion: "1.4.0" }));
  assert.equal(below.status, "refused");
  assert.ok(below.reason.includes("1.5.0"), below.reason);
  assert.equal(fs.readFileSync(piSettings, "utf8"), before);
});

test("pi refuses a settings.json whose top level is not a JSON object, never overwriting it", async () => {
  const broken = JSON.stringify([1, 2, 3]);
  fs.writeFileSync(piSettings, broken);
  try {
    const refused = await piAdapter.write("model", { thinkingLevel: "low" }, piCtx({ piVersion: "1.5.0" }));
    assert.equal(refused.status, "refused");
    assert.ok(refused.reason.includes("settings.json"), refused.reason);
    assert.equal(fs.readFileSync(piSettings, "utf8"), broken);
  } finally {
    writeJson(piSettings, {
      defaultProvider: "anthropic",
      defaultModel: "claude-opus-4",
      defaultThinkingLevel: "high",
      modelThinkingLevels: { "openrouter/qwen3": "low" },
      enabledModels: ["anthropic/*"],
    });
  }
});

test("pi refuses an unrecognized modelThinkingLevels shape and unknown or invalid changes", async () => {
  const before = fs.readFileSync(piSettings, "utf8");
  fs.writeFileSync(piSettings, JSON.stringify({ ...readJson(piSettings), modelThinkingLevels: "text" }));
  try {
    const refused = await piAdapter.write("model", { modelThinkingLevel: { key: "anthropic/claude-opus-4", level: "low" } }, piCtx());
    assert.equal(refused.status, "refused");
    assert.ok(refused.reason.includes("modelThinkingLevels"), refused.reason);
  } finally {
    fs.writeFileSync(piSettings, before);
  }
  assert.equal((await piAdapter.write("model", {}, piCtx())).status, "refused");
  assert.equal((await piAdapter.write("model", { nope: 1 }, piCtx())).status, "refused");
  assert.equal((await piAdapter.write("model", { thinkingLevel: "bogus" }, piCtx())).status, "refused");
});

// ── the claude adapter: fixture config dir and versioned stubs ───────────
const claudeDir = path.join(scratch, "claude-config");
fs.mkdirSync(claudeDir);
const claudeSettings = path.join(claudeDir, "settings.json");
writeJson(claudeSettings, {
  model: "opus",
  effortLevel: "high",
  modelSettings: { "claude-opus-5-5": { effort: "max", adaptiveThinking: true } },
  env: { OTHER_FLAG: "kept" },
  permissions: { defaultMode: "default" },
});
writeJson(path.join(claudeDir, ".credentials.json"), { claudeAiOauth: { accessToken: "CL85-SENTINEL-CRED" } });
const stubLog = path.join(scratch, "stub.jsonl");
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_CODING_AGENT_DIR = path.join(scratch, "agent");
delete process.env.CLAUDE_CODE_DISABLE_BACKGROUND_TASKS;

function installVersionedStub(name, version) {
  const dir = path.join(scratch, name);
  fs.mkdirSync(dir);
  const binary = path.join(dir, "claude");
  fs.writeFileSync(binary, `#!/bin/sh\nexport PI_TEST_CLAUDE_STUB_VERSION=${version}\nexec node ${path.join(here, "__fixtures__", "claude-stub", "claude.mjs")} "$@"\n`, { mode: 0o755 });
  return binary;
}
const okBinary = installVersionedStub("ok-bin", "2.1.326");
const nextMinorBinary = installVersionedStub("next-minor-bin", "2.2.0");
const unknownBinary = installVersionedStub("unknown-bin", "3.0.0");
const belowFloorBinary = installVersionedStub("below-floor-bin", "2.1.100");
const claudeCtx = (extra = {}) => ({
  claudeConfigDir: claudeDir,
  claudeBinary: okBinary,
  managedSettingsPath: path.join(scratch, "managed-settings.json"),
  env: {},
  ...extra,
});

test("claude reads the model defaults and the per-model efforts from modelSettings", async () => {
  const payload = await claudeAdapter.read("model", claudeCtx());
  assert.equal(payload.defaultModel, "opus");
  assert.deepEqual(payload.reasoningEffort, { neutral: "high", raw: "high" });
  assert.deepEqual(payload.perModelReasoningEffort, { "claude-opus-5-5": { neutral: "max", raw: "max" } });
  noSentinel(payload);
});

test("claude writes and removes the native model, keeping the file's other keys", async () => {
  const written = await claudeAdapter.write("model", { model: "sonnet" }, claudeCtx());
  assert.equal(written.status, "written");
  const settings = readJson(claudeSettings);
  assert.equal(settings.model, "sonnet");
  assert.equal(settings.effortLevel, "high");
  assert.deepEqual(settings.modelSettings, { "claude-opus-5-5": { effort: "max", adaptiveThinking: true } });
  assert.deepEqual(settings.env, { OTHER_FLAG: "kept" });
  assert.deepEqual(settings.permissions, { defaultMode: "default" });
  assert.ok(!JSON.stringify(written).includes("CL85-SENTINEL-CRED"), "the write result echoes a credential");
  const removed = await claudeAdapter.write("model", { model: null }, claudeCtx());
  assert.equal(removed.status, "written");
  assert.equal("model" in readJson(claudeSettings), false);
});

test("claude writes and removes the native effortLevel", async () => {
  const set = await claudeAdapter.write("model", { effortLevel: "max" }, claudeCtx());
  assert.equal(set.status, "written");
  assert.equal(readJson(claudeSettings).effortLevel, "max");
  const removed = await claudeAdapter.write("model", { effortLevel: null }, claudeCtx());
  assert.equal(removed.status, "written");
  assert.equal("effortLevel" in readJson(claudeSettings), false);
});

test("claude refuses unverified per-model and ultracode settings writes without changing native bytes", async () => {
  const before = fs.readFileSync(claudeSettings, "utf8");
  for (const change of [
    { modelEffort: { model: "claude-opus-5-5", effort: "low" } },
    { modelEffort: { model: "claude-opus-5-5", effort: null } },
    { effortLevel: "ultracode" },
  ]) {
    const refused = await claudeAdapter.write("model", change, claudeCtx());
    assert.equal(refused.status, "refused", JSON.stringify(change));
    assert.equal(fs.readFileSync(claudeSettings, "utf8"), before);
  }
});

test("claude refuses an unknown-major version, keeping settings.json untouched", async () => {
  const before = fs.readFileSync(claudeSettings, "utf8");
  const refused = await claudeAdapter.write("model", { model: "sonnet" }, claudeCtx({ claudeBinary: unknownBinary }));
  assert.equal(refused.status, "refused");
  assert.ok(refused.reason.includes("3.0.0"), refused.reason);
  assert.equal(fs.readFileSync(claudeSettings, "utf8"), before);
});

test("claude recognizes the same major: 2.2.0 writes, 2.1.100 is below the model keys' registered minimum", async () => {
  const written = await claudeAdapter.write("model", { effortLevel: "low" }, claudeCtx({ claudeBinary: nextMinorBinary }));
  assert.equal(written.status, "written");
  assert.equal(readJson(claudeSettings).effortLevel, "low");
  const before = fs.readFileSync(claudeSettings, "utf8");
  const below = await claudeAdapter.write("model", { effortLevel: "low" }, claudeCtx({ claudeBinary: belowFloorBinary }));
  assert.equal(below.status, "refused");
  assert.ok(below.reason.includes("2.1.312"), below.reason);
  assert.equal(fs.readFileSync(claudeSettings, "utf8"), before);
});

test("claude refuses a settings.json whose top level is not a JSON object, never overwriting it", async () => {
  const broken = JSON.stringify([1, 2, 3]);
  fs.writeFileSync(claudeSettings, broken);
  try {
    const refused = await claudeAdapter.write("model", { model: "sonnet" }, claudeCtx());
    assert.equal(refused.status, "refused");
    assert.ok(refused.reason.includes("settings.json"), refused.reason);
    assert.equal(fs.readFileSync(claudeSettings, "utf8"), broken);
  } finally {
    writeJson(claudeSettings, {
      model: "opus",
      effortLevel: "high",
      modelSettings: { "claude-opus-5-5": { effort: "max", adaptiveThinking: true } },
      env: { OTHER_FLAG: "kept" },
      permissions: { defaultMode: "default" },
    });
  }
});

test("claude refuses unknown or invalid changes", async () => {
  const before = fs.readFileSync(claudeSettings, "utf8");
  fs.writeFileSync(claudeSettings, JSON.stringify({ ...readJson(claudeSettings), modelSettings: "text" }));
  try {
    const refused = await claudeAdapter.write("model", { modelEffort: { model: "opus", effort: "low" } }, claudeCtx());
    assert.equal(refused.status, "refused");
    assert.ok(refused.reason.includes("modelSettings") || refused.reason.includes("modelEffort"), refused.reason);
  } finally {
    fs.writeFileSync(claudeSettings, before);
  }
  assert.equal((await claudeAdapter.write("model", {}, claudeCtx())).status, "refused");
  assert.equal((await claudeAdapter.write("model", { nope: 1 }, claudeCtx())).status, "refused");
  assert.equal((await claudeAdapter.write("model", { model: "-leading-dash" }, claudeCtx())).status, "refused");
  assert.equal((await claudeAdapter.write("model", { modelEffort: { model: "opus", effort: "low" } }, claudeCtx())).status, "refused");
  assert.equal((await claudeAdapter.write("model", { effortLevel: "bogus" }, claudeCtx())).status, "refused");
});

test("both adapters declare the model concept writable (85a3)", () => {
  assert.ok(piAdapter.capabilities.writableConcepts.includes("model"));
  assert.ok(claudeAdapter.capabilities.writableConcepts.includes("model"));
});
