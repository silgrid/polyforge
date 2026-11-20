// carries: lib/default-model-preference.ts, lib/default-model-client.ts
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// 85a3-1 AC2: the combined default 「harness + 模型」. The harness part has no
// native home, so per the owner's Q1 A ruling it lives in pi-web-settings.json's
// `defaultModel` key beside the Agent language and the claude preferences;
// expected values are hand-written from the ticket, never from the
// implementation's output: a missing or malformed record reads as pi, the
// model snapshot follows the harness, and a write keeps every other key.
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-default-model-85a3-"));
const agentDir = path.join(scratch, "agent");
fs.mkdirSync(agentDir);
process.env.PI_CODING_AGENT_DIR = agentDir;
const settingsPath = path.join(agentDir, "pi-web-settings.json");

test.after(() => {
  fs.rmSync(scratch, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const preference = await jiti.import("./default-model-preference.ts");
const readJson = () => JSON.parse(fs.readFileSync(settingsPath, "utf8"));

test("a missing preference file reads as the default: pi", () => {
  assert.deepEqual(preference.readDefaultModelPreference(), { harness: "pi" });
});

test("the combined default lands under defaultModel, keeping the file's other keys", async () => {
  fs.writeFileSync(settingsPath, JSON.stringify({ version: 1, agentLanguage: "zh-CN", claude: { pushReminders: false } }));
  const stored = await preference.writeDefaultModelPreference({ harness: "claude", claudeModel: "opus" });
  assert.deepEqual(stored, { harness: "claude", claudeModel: "opus" });
  const file = readJson();
  assert.equal(file.agentLanguage, "zh-CN");
  assert.deepEqual(file.claude, { pushReminders: false });
  assert.deepEqual(file.defaultModel, { harness: "claude", claudeModel: "opus" });
  assert.deepEqual(preference.readDefaultModelPreference(), { harness: "claude", claudeModel: "opus" });
});

test("a pi snapshot round-trips beside the harness", async () => {
  await preference.writeDefaultModelPreference({ harness: "pi", model: { provider: "acme", modelId: "one" } });
  assert.deepEqual(preference.readDefaultModelPreference(), { harness: "pi", model: { provider: "acme", modelId: "one" } });
  assert.deepEqual(readJson().defaultModel, { harness: "pi", model: { provider: "acme", modelId: "one" } });
});

test("a malformed or partial record reads tolerantly as the default", async () => {
  fs.writeFileSync(settingsPath, JSON.stringify({ defaultModel: "text" }));
  assert.deepEqual(preference.readDefaultModelPreference(), { harness: "pi" });
  fs.writeFileSync(settingsPath, JSON.stringify({ defaultModel: { harness: "bogus", claudeModel: "opus" } }));
  // an unknown harness reads as pi; a well-formed claude snapshot without its
  // harness is not a claude default
  assert.deepEqual(preference.readDefaultModelPreference(), { harness: "pi" });
  fs.writeFileSync(settingsPath, JSON.stringify({ defaultModel: { harness: "claude", claudeModel: 7 } }));
  assert.deepEqual(preference.readDefaultModelPreference(), { harness: "claude" });
  fs.writeFileSync(settingsPath, JSON.stringify({ defaultModel: { harness: "pi", model: { provider: "acme" } } }));
  assert.deepEqual(preference.readDefaultModelPreference(), { harness: "pi" });
});

test("a preference file that is not a JSON object refuses the write, never overwriting it", async () => {
  const broken = JSON.stringify([1, 2, 3]);
  fs.writeFileSync(settingsPath, broken);
  await assert.rejects(
    () => preference.writeDefaultModelPreference({ harness: "claude", claudeModel: "opus" }),
    /refusing to overwrite/,
  );
  assert.equal(fs.readFileSync(settingsPath, "utf8"), broken);
});

// ── the client half (85a3 u4): the composer's apply rules ────────────────
const client = await jiti.import("./default-model-client.ts");

test("parseDefaultModelFetch: a valid body parses, a partial one fills the route's defaults, garbage answers null", () => {
  assert.deepEqual(client.parseDefaultModelFetch({ defaultHarness: "claude", claudeDefaultModel: "opus", claudeDefaultEffort: "high" }), {
    defaultHarness: "claude",
    claudeDefaultModel: "opus",
    claudeDefaultEffort: "high",
  });
  assert.deepEqual(client.parseDefaultModelFetch({}), {
    defaultHarness: "pi",
    claudeDefaultModel: "default",
    claudeDefaultEffort: "default",
  });
  assert.deepEqual(client.parseDefaultModelFetch({ defaultHarness: "bogus", claudeDefaultModel: "-bad", claudeDefaultEffort: "nope" }), {
    defaultHarness: "pi",
    claudeDefaultModel: "default",
    claudeDefaultEffort: "default",
  });
  for (const garbage of [null, 7, "text", []]) assert.equal(client.parseDefaultModelFetch(garbage), null);
});

test("resolveFetchedComposerDefaults: an untouched composer takes all three fetched values", () => {
  const fetched = { defaultHarness: "claude", claudeDefaultModel: "opus", claudeDefaultEffort: "high" };
  const untouched = {
    carriedHarness: false, carriedClaudeModel: false, carriedClaudeEffort: false,
    pickedHarness: false, pickedClaudeModel: false, pickedClaudeEffort: false,
  };
  assert.deepEqual(client.resolveFetchedComposerDefaults(fetched, untouched), {
    harness: "claude",
    claudeModel: "opus",
    claudeEffort: "high",
  });
});

test("resolveFetchedComposerDefaults: a carried choice or the user's own pick always wins", () => {
  const fetched = { defaultHarness: "claude", claudeDefaultModel: "opus", claudeDefaultEffort: "high" };
  // the carried draft chose claude's model: the fetch fills the rest only
  assert.deepEqual(client.resolveFetchedComposerDefaults(fetched, {
    carriedHarness: false, carriedClaudeModel: true, carriedClaudeEffort: false,
    pickedHarness: false, pickedClaudeModel: false, pickedClaudeEffort: false,
  }), { harness: "claude", claudeEffort: "high" });
  // the user already switched the harness and picked an effort
  assert.deepEqual(client.resolveFetchedComposerDefaults(fetched, {
    carriedHarness: false, carriedClaudeModel: false, carriedClaudeEffort: false,
    pickedHarness: true, pickedClaudeModel: false, pickedClaudeEffort: true,
  }), { claudeModel: "opus" });
  // a carried harness and a picked claude model: only the effort is left
  assert.deepEqual(client.resolveFetchedComposerDefaults(fetched, {
    carriedHarness: true, carriedClaudeModel: false, carriedClaudeEffort: false,
    pickedHarness: false, pickedClaudeModel: true, pickedClaudeEffort: false,
  }), { claudeEffort: "high" });
});
