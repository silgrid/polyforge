// carries: lib/step-tier-defined-models.ts, lib/models-load.ts, lib/claude-models.ts, lib/step-tiers.ts
// polyforge#151 (85b2, u2): the step-tier write gate's defined-model set —
// the same projections the unified models page lists its rows from (Q2):
// pi's registered models (the ?scope=all read behind GET /api/models, one
// fact with the route through lib/models-load.ts) and claude's aliases plus
// its native modelPicker custom names (the GET /api/claude read's own half,
// one fact with the route through lib/claude-models.ts). A failed claude
// probe leaves `default` plus the custom names (the write gate never blocks
// an existing tier's edit on a probe failure); a pi model list that cannot
// load rejects (the gate accepts nothing it cannot compute).
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-step-tier-defined-"));
const agentDir = path.join(scratch, "agent");
const binDir = path.join(scratch, "bin");
const failDir = path.join(scratch, "fail-bin");
const ws = path.join(scratch, "ws");
fs.mkdirSync(agentDir);
fs.mkdirSync(binDir);
fs.mkdirSync(failDir);
fs.mkdirSync(ws);
// The unit claude stub (the 2.1.322 initialize recording) and one that cannot answer.
fs.copyFileSync(new URL("./__fixtures__/claude-stub/claude.mjs", import.meta.url), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
fs.symlinkSync(decodeURIComponent(new URL("./__fixtures__/claude-2.1.322", import.meta.url).pathname), path.join(scratch, "claude-2.1.322"));
fs.writeFileSync(path.join(failDir, "claude"), "#!/bin/sh\necho 'claude: not logged in' >&2\nexit 1\n", { mode: 0o755 });
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.PI_OFFLINE = "1";
// The custom names are claude's native modelPicker now (85a4): the config dir is the test's own.
const claudeConfig = path.join(scratch, "claude-config");
fs.mkdirSync(claudeConfig);
process.env.CLAUDE_CONFIG_DIR = claudeConfig;
delete process.env.CLAUDE_CODE_DISABLE_BACKGROUND_TASKS;
// Ambient provider auth would add models to the list (the models route test's isolation).
const ambientAuth = ["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"];
const previousAuth = ambientAuth.map((key) => [key, process.env[key]]);
for (const key of ambientAuth) delete process.env[key];
const originalPath = process.env.PATH;

test.after(() => {
  process.env.PATH = originalPath;
  for (const [key, value] of previousAuth) {
    if (value === undefined) delete process.env[key];
    else process.env[key] = value;
  }
  fs.rmSync(scratch, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
delete globalThis.__piWebProviderModelCatalog;
const { stepTierDefinedModels } = await jiti.import("./step-tier-defined-models.ts");
const { invalidateModelsCache } = await jiti.import("./models-cache.ts");

function writeAgentModels(value) {
  fs.writeFileSync(path.join(agentDir, "models.json"), typeof value === "string" ? value : JSON.stringify(value));
  invalidateModelsCache();
}

test("pi's defined models are the scope-all projection of models.json (the models page's own rows)", async () => {
  writeAgentModels({ providers: { acme: { baseUrl: "https://example.invalid/v1", apiKey: "sk-test", api: "openai-completions", models: [{ id: "one", name: "Acme One" }] } } });
  process.env.PATH = `${failDir}${path.delimiter}${originalPath}`;
  const defined = await stepTierDefinedModels(ws);
  assert.deepEqual(defined.pi, ["acme/one"], "exactly the registered models, nothing ambient");
  assert.deepEqual(defined.claude, ["default"], "a failed probe leaves the default alias");
});

test("claude's defined names are the probe's aliases plus the native modelPicker custom names", async () => {
  process.env.PATH = `${binDir}${path.delimiter}${originalPath}`;
  fs.writeFileSync(path.join(claudeConfig, "settings.json"), JSON.stringify({
    modelPicker: { options: [{ model: "claude-custom-1" }, { model: "opus" }, { model: "us.anthropic.claude-x-v1:0" }] },
  }));
  invalidateModelsCache();
  const defined = await stepTierDefinedModels(ws);
  assert.deepEqual(defined.claude, ["default", "opus", "fable", "sonnet", "haiku", "claude-custom-1", "us.anthropic.claude-x-v1:0"],
    "the aliases (deduped first) plus the custom names, a custom name that is an alias listed once");
});

test("a pi model list that cannot load rejects — the write gate accepts nothing it cannot compute", async () => {
  writeAgentModels("{invalid json");
  process.env.PATH = `${failDir}${path.delimiter}${originalPath}`;
  await assert.rejects(() => stepTierDefinedModels(ws), /models\.json|model/i);
});
