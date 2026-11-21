// carries: lib/harness/claude.ts, lib/harness/claude-native-write.ts, lib/harness/claude-layers.ts, lib/claude-shared.ts
import "#test-env";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// 85a3-3 AC1: claude's custom model names are claude's own native
// `modelPicker.options` (the official settings reference: an options array of
// { model, label?, description?, behavesAs? } plus replaceBuiltInOptions,
// v2.1.242+; the isolated 2.1.329 native recording in the capsule's
// artifacts/build/native-evidence proves claude lists the names in its own
// model selector). The adapter's model write takes a `customModels` list: the
// full next state of the names. Expected values are hand-derived from the
// official reference and claude-shared's rules (a name `--model` takes, at
// most CLAUDE_CUSTOM_MODELS_MAX), never from the implementation's output:
// the floor 2.1.242, the metadata a surviving entry keeps, the append-only
// order, and the byte-identical file on every refusal. The version probe goes
// through the unit claude stub (lib/__fixtures__/claude-stub) with
// PI_TEST_CLAUDE_STUB_VERSION naming the fixed version under test.
const here = path.dirname(new URL(import.meta.url).pathname);
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-custom-models-"));
const agentDir = path.join(scratch, "agent");
fs.mkdirSync(agentDir);
const stubLog = path.join(scratch, "stub.jsonl");
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_CODING_AGENT_DIR = agentDir;
delete process.env.CLAUDE_CODE_DISABLE_BACKGROUND_TASKS;
const managedSettings = path.join(scratch, "managed-settings.json");

test.after(() => {
  fs.rmSync(scratch, { recursive: true, force: true });
});

/** A stub `claude` answering initialize with the given claude_code_version. */
function installVersionedStub(name, version) {
  const dir = path.join(scratch, name);
  fs.mkdirSync(dir);
  const binary = path.join(dir, "claude");
  fs.writeFileSync(binary, `#!/bin/sh\nexport PI_TEST_CLAUDE_STUB_VERSION=${version}\nexec node ${path.join(here, "..", "__fixtures__", "claude-stub", "claude.mjs")} "$@"\n`, { mode: 0o755 });
  return binary;
}
const okBinary = installVersionedStub("ok-bin", "2.1.326");
const belowFloorBinary = installVersionedStub("below-floor-bin", "2.1.200");
const unknownBinary = installVersionedStub("unknown-bin", "3.0.0");
const missingBinary = path.join(scratch, "no-such-claude");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const claude = await jiti.import("./claude.ts");
const adapter = claude.createClaudeAdapter();

/** A fresh write target: its own config dir, seeded with the given settings record. */
function writeTarget(name, record) {
  const configDir = path.join(scratch, name);
  fs.mkdirSync(configDir, { recursive: true });
  if (record !== undefined) fs.writeFileSync(path.join(configDir, "settings.json"), `${JSON.stringify(record, null, 2)}\n`);
  return configDir;
}
const settingsOf = (configDir) => JSON.parse(fs.readFileSync(path.join(configDir, "settings.json"), "utf8"));
const bytesOf = (configDir) => createHash("sha256").update(fs.readFileSync(path.join(configDir, "settings.json"))).digest("hex");
const ctx = (configDir, binary = okBinary) => ({
  claudeConfigDir: configDir,
  agentDir,
  claudeBinary: binary,
  env: {},
  managedSettingsPath: managedSettings,
});

test("customModels: the write replaces the names, keeps every kept entry's metadata and appends new entries", async () => {
  const configDir = writeTarget("replace-target", {
    model: "opus",
    effortLevel: "high",
    modelPicker: {
      replaceBuiltInOptions: true,
      options: [
        { model: "keep-a", label: "A label", description: "A description" },
        { model: "drop-b" },
        { model: "keep-c", behavesAs: "claude-opus-4-8" },
      ],
    },
  });
  const result = await adapter.write("model", { customModels: ["new-d", "keep-c", "keep-a"] }, ctx(configDir));
  assert.equal(result.status, "written", JSON.stringify(result));
  const written = settingsOf(configDir);
  // The official shape: the names replace the list; a surviving entry keeps
  // its own metadata and position, a new name is appended as { model }, and
  // the picker's other fields (replaceBuiltInOptions) are untouched.
  assert.deepEqual(written.modelPicker, {
    replaceBuiltInOptions: true,
    options: [
      { model: "keep-a", label: "A label", description: "A description" },
      { model: "keep-c", behavesAs: "claude-opus-4-8" },
      { model: "new-d" },
    ],
  });
  // The file's other keys stay.
  assert.equal(written.model, "opus");
  assert.equal(written.effortLevel, "high");
  // The read lists the written names (the read-write loop closes).
  const payload = await adapter.read("model", ctx(configDir));
  assert.deepEqual(payload.customModels, ["keep-a", "keep-c", "new-d"]);
});

test("customModels: an empty list writes the legal empty picker; a fresh config gains only the picker", async () => {
  const configDir = writeTarget("empty-target", {
    model: "opus",
    modelPicker: { replaceBuiltInOptions: true, options: [{ model: "old-one" }] },
  });
  const result = await adapter.write("model", { customModels: [] }, ctx(configDir));
  assert.equal(result.status, "written", JSON.stringify(result));
  const written = settingsOf(configDir);
  assert.deepEqual(written.modelPicker, { replaceBuiltInOptions: true, options: [] });

  const fresh = writeTarget("fresh-target");
  const created = await adapter.write("model", { customModels: ["first-custom"] }, ctx(fresh));
  assert.equal(created.status, "written", JSON.stringify(created));
  assert.deepEqual(settingsOf(fresh), { modelPicker: { options: [{ model: "first-custom" }] } });
});

test("customModels: duplicates collapse in order; an invalid change is refused with a reason and the file untouched", async () => {
  const configDir = writeTarget("invalid-target", { modelPicker: { options: [{ model: "keep-a" }] } });
  const before = bytesOf(configDir);
  const cases = [
    { change: { customModels: "claude-custom-1" }, reason: "array of model names" },
    { change: { customModels: ["ok-name", "not a model name!"] }, reason: "not a claude model name" },
    { change: { customModels: Array.from({ length: 51 }, (_, index) => `claude-custom-${index}`) }, reason: "capped at 50" },
  ];
  for (const { change, reason } of cases) {
    const refused = await adapter.write("model", change, ctx(configDir));
    assert.equal(refused.status, "refused", JSON.stringify(refused));
    assert.ok(refused.reason.includes(reason), `${refused.reason} names ${reason}`);
  }
  assert.equal(bytesOf(configDir), before, "a refused change touched the file");
  // Duplicates are one entry (the old reader's normalization), order kept.
  const deduped = await adapter.write("model", { customModels: ["claude-x", "claude-x", "claude-y"] }, ctx(configDir));
  assert.equal(deduped.status, "written", JSON.stringify(deduped));
  assert.deepEqual(settingsOf(configDir).modelPicker.options, [{ model: "claude-x" }, { model: "claude-y" }]);
});

test("customModels: the 2.1.242 floor, an unknown major and a missing claude all refuse without touching the file (AC1)", async () => {
  const configDir = writeTarget("floor-target", { model: "opus" });
  for (const [name, binary] of [["below the floor", belowFloorBinary], ["an unknown major", unknownBinary], ["no claude at all", missingBinary]]) {
    const before = bytesOf(configDir);
    const refused = await adapter.write("model", { customModels: ["claude-custom-1"] }, ctx(configDir, binary));
    assert.equal(refused.status, "refused", `${name}: ${JSON.stringify(refused)}`);
    assert.equal(bytesOf(configDir), before, `${name}: the file changed`);
    if (name === "below the floor") {
      assert.ok(refused.reason.includes("modelPicker"), `the floor names its key: ${refused.reason}`);
      assert.ok(refused.reason.includes("2.1.242"), `the floor names its version: ${refused.reason}`);
      assert.ok(refused.reason.includes("2.1.200"), `the floor names the found version: ${refused.reason}`);
    }
  }
});

test("customModels: a bad native picker shape is refused, never overwritten", async () => {
  const shapes = [
    { modelPicker: "junk" },
    { modelPicker: { options: "junk" } },
    { modelPicker: { options: ["not-an-object"] } },
    { modelPicker: { options: [{ label: "a row without a model" }] } },
  ];
  for (const [index, modelPicker] of shapes.entries()) {
    const configDir = writeTarget(`bad-shape-${index}`);
    fs.writeFileSync(path.join(configDir, "settings.json"), `${JSON.stringify({ model: "opus", modelPicker }, null, 2)}\n`);
    const before = bytesOf(configDir);
    const refused = await adapter.write("model", { customModels: ["claude-custom-1"] }, ctx(configDir));
    assert.equal(refused.status, "refused", JSON.stringify(refused));
    assert.ok(refused.reason.includes("modelPicker"), `shape ${index} names the picker: ${refused.reason}`);
    assert.equal(bytesOf(configDir), before, `shape ${index}: the file changed`);
  }
});
