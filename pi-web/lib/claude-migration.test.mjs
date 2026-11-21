// carries: lib/claude-migration.ts, lib/claude-preferences.ts, lib/pi-web-settings.ts, lib/agent-language-config.ts, lib/harness/claude-native-write.ts, lib/harness/claude.ts
import "#test-env";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// polyforge#150 (85a4) AC3 + polyforge#178 (85a3-3) AC2: the two self-stored
// claude files — pi-web-claude-settings.json and claude-permissions.json —
// migrate field by field into what now owns them. 85a4's startup flow owns
// everything except customModels: launchPermissionMode → the native
// permissions.defaultMode, the channel/push/hook fields → pi-web-settings.json's
// `claude object` (native values win; write, verify, delete, mark — idempotent
// and interrupt-safe). 85a3-3 moved the custom names to the models page's own
// phase (owner rulings Q2/Q3/Q4): the startup flow never writes modelPicker and
// keeps the old settings file while it still holds a customModels key; the
// migration step the page's POST runs migrates an empty native target at once,
// answers a merge preview for a non-empty one (the native rows win, only
// missing names append) and writes nothing until the confirm carries the
// preview's digest — a changed source or target is a conflict with a fresh
// preview; after the target reads back only the old file's customModels key is
// removed (never the whole file); illegal or over-cap data reports a blockage
// and is kept; the done marker is keyed by the target config root. Expected
// values are hand-derived from those rulings, never from the implementation's
// output. The write gate's rule stays: an unknown claude version defers the
// native writes and the old files stay. The fixtures are generated here (85a1's
// Q6 A).
const here = path.dirname(new URL(import.meta.url).pathname);
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-migration-"));
const agentDir = path.join(scratch, "agent");
const claudeConfigDir = path.join(scratch, "claude-config");
fs.mkdirSync(agentDir, { recursive: true });
fs.mkdirSync(claudeConfigDir, { recursive: true });

function installVersionedStub(name, version) {
  const dir = path.join(scratch, name);
  fs.mkdirSync(dir);
  const binary = path.join(dir, "claude");
  fs.writeFileSync(binary, `#!/bin/sh\nexport PI_TEST_CLAUDE_STUB_VERSION=${version}\nexec node ${path.join(here, "__fixtures__", "claude-stub", "claude.mjs")} "$@"\n`, { mode: 0o755 });
  return binary;
}
const okBinary = installVersionedStub("ok-bin", "2.1.326");
const unknownBinary = installVersionedStub("unknown-bin", "3.0.0");
const stubLog = path.join(scratch, "stub.jsonl");
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_CODING_AGENT_DIR = agentDir;

test.after(() => {
  fs.rmSync(scratch, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const migration = await jiti.import("./claude-migration.ts");
const preferences = await jiti.import("./claude-preferences.ts");
const agentLanguage = await jiti.import("./agent-language-config.ts");

const oldSettingsPath = path.join(agentDir, "pi-web-claude-settings.json");
const oldPermissionsPath = path.join(agentDir, "claude-permissions.json");
const nativeSettingsPath = path.join(claudeConfigDir, "settings.json");
const piWebSettingsPath = path.join(agentDir, "pi-web-settings.json");
const managedSettingsPath = path.join(scratch, "managed-settings.json");

const opts = (extra = {}) => ({
  agentDir,
  claudeConfigDir,
  claudeBinary: okBinary,
  env: {},
  managedSettingsPath,
  ...extra,
});

/** Seeds both old files with the given fields (defaults from the ticket's era). */
function seedOld({ launch = "acceptEdits", models = ["claude-custom-1", "claude-custom-2"], hooks = true, channel = "deny", push = false } = {}) {
  fs.writeFileSync(oldSettingsPath, JSON.stringify({ version: 1, launchPermissionMode: launch, customModels: models, includeHookEvents: hooks }));
  fs.writeFileSync(oldPermissionsPath, JSON.stringify({ version: 1, mode: channel, push }));
}

/** A clean slate: nothing migrated, no old files, no native settings. */
function reset() {
  for (const file of [oldSettingsPath, oldPermissionsPath, nativeSettingsPath, piWebSettingsPath, managedSettingsPath]) fs.rmSync(file, { force: true });
  migration.resetClaudeMigrationForTests();
}

const readJson = (file) => JSON.parse(fs.readFileSync(file, "utf8"));
const markerOf = (dir = claudeConfigDir) => fs.existsSync(piWebSettingsPath)
  ? readJson(piWebSettingsPath).claudeCustomModelsMigration?.[dir]
  : undefined;

// ── the startup flow (85a4): every field but customModels ────────────────

test("the startup migration moves its own fields, keeps the settings file while it holds customModels and writes no marker (85a3-3 Q2)", async () => {
  reset();
  seedOld();
  const result = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(result.ran, true);
  assert.ok(result.summary.includes("customModels await"), result.summary);
  // The preference fields and the launch mode land where they now live.
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "deny", pushReminders: false, includeHookEvents: true });
  assert.equal(readJson(nativeSettingsPath).permissions.defaultMode, "acceptEdits");
  // customModels never migrated here: no modelPicker, the old settings file
  // kept with its customModels key, the old permissions file gone, no marker.
  assert.equal(readJson(nativeSettingsPath).modelPicker, undefined);
  assert.deepEqual(readJson(oldSettingsPath).customModels, ["claude-custom-1", "claude-custom-2"]);
  assert.equal(fs.existsSync(oldPermissionsPath), false, "claude-permissions.json was kept");
  assert.equal(readJson(piWebSettingsPath).claudeMigrated, undefined);
});

test("once the customModels phase cleared the key, the next startup deletes the old settings file and marks (the two flows compose)", async () => {
  reset();
  seedOld();
  await migration.ensureClaudeSelfStoredMigration(opts());
  await migration.claudeCustomModelsMigrationStep(opts());
  assert.equal("customModels" in readJson(oldSettingsPath), false, "the step removed the whole file");
  const again = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(again.ran, true);
  assert.equal(fs.existsSync(oldSettingsPath), false, "the old settings file was kept past its migration");
  assert.equal(readJson(piWebSettingsPath).claudeMigrated, true);
});

test("an old settings file without a customModels key — or holding an empty list — still goes at startup (85a4's own case)", async () => {
  reset();
  fs.writeFileSync(oldSettingsPath, JSON.stringify({ version: 1, launchPermissionMode: "acceptEdits", includeHookEvents: true }));
  fs.writeFileSync(oldPermissionsPath, JSON.stringify({ version: 1, mode: "ask", push: true }));
  const result = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(result.ran, true);
  assert.equal(fs.existsSync(oldSettingsPath), false);
  assert.equal(readJson(piWebSettingsPath).claudeMigrated, true);
  // An empty list carries nothing for the phase: the create-time migration
  // (claude-create.test.mjs) still deletes the file and marks.
  reset();
  fs.writeFileSync(oldSettingsPath, JSON.stringify({ version: 1, customModels: [], includeHookEvents: true }));
  fs.writeFileSync(oldPermissionsPath, JSON.stringify({ version: 1, mode: "ask", push: true }));
  const emptied = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(emptied.ran, true);
  assert.equal(fs.existsSync(oldSettingsPath), false, "an empty customModels list kept the file");
  assert.equal(readJson(piWebSettingsPath).claudeMigrated, true);
});

test("idempotent: once the marker is written a later run skips and touches nothing (Q4 B)", async () => {
  reset();
  seedOld();
  await migration.ensureClaudeSelfStoredMigration(opts());
  await migration.claudeCustomModelsMigrationStep(opts());
  await migration.ensureClaudeSelfStoredMigration(opts());
  const nativeBefore = fs.readFileSync(nativeSettingsPath, "utf8");
  const again = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(again.ran, false);
  assert.ok(again.reason.includes("migrated"), again.reason);
  assert.equal(fs.readFileSync(nativeSettingsPath, "utf8"), nativeBefore);
});

test("replay after an interruption: old files already gone and no marker just writes the marker (Q4 B)", async () => {
  reset();
  // A crash between deleting the old files and writing the marker.
  fs.writeFileSync(piWebSettingsPath, JSON.stringify({ version: 1, claude: { approvalChannel: "ask", pushReminders: true, includeHookEvents: false } }));
  const result = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(result.ran, true);
  assert.equal(readJson(piWebSettingsPath).claudeMigrated, true);
  assert.equal(fs.existsSync(nativeSettingsPath), false, "the replay wrote a native file for nothing");
});

test("native wins on the permission mode; a non-empty native picker never drops the old names any more (85a3-3 Q3 over 85a4's native-wins)", async () => {
  reset();
  fs.writeFileSync(nativeSettingsPath, JSON.stringify({
    model: "opus",
    permissions: { defaultMode: "auto" },
    modelPicker: { options: [{ model: "claude-native-model" }] },
  }));
  seedOld();
  const result = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(result.ran, true);
  const native = readJson(nativeSettingsPath);
  assert.equal(native.permissions.defaultMode, "auto", "the stored mode overwrote the native one");
  assert.deepEqual(native.modelPicker.options, [{ model: "claude-native-model" }], "the startup flow wrote modelPicker");
  assert.equal(native.model, "opus");
  // The old names survive for the preview: the settings file stays, the
  // permissions file (85a4's own) still goes, the marker waits.
  assert.deepEqual(readJson(oldSettingsPath).customModels, ["claude-custom-1", "claude-custom-2"]);
  assert.equal(fs.existsSync(oldPermissionsPath), false);
  assert.equal(readJson(piWebSettingsPath).claudeMigrated, undefined);
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "deny", pushReminders: false, includeHookEvents: true });
});

test("the write gate defers only the native writes — the preference fields migrate first, and a preference changed meanwhile survives the completing run (review r1#3, Q2 C)", async () => {
  reset();
  seedOld();
  const deferred = await migration.ensureClaudeSelfStoredMigration(opts({ claudeBinary: unknownBinary }));
  assert.equal(deferred.ran, false);
  assert.ok(deferred.reason.includes("3.0.0"), deferred.reason);
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "deny", pushReminders: false, includeHookEvents: true });
  assert.equal(fs.existsSync(oldSettingsPath), true, "a deferred migration deleted an old file");
  assert.equal(fs.existsSync(oldPermissionsPath), true);
  assert.equal(fs.existsSync(nativeSettingsPath), false, "a deferred migration wrote the native settings");
  await preferences.writeClaudePreferences({ pushReminders: true }, agentDir);
  const done = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(done.ran, true);
  assert.equal(readJson(nativeSettingsPath).permissions.defaultMode, "acceptEdits");
  // 85a3-3: the settings file stays (its customModels key awaits the phase).
  assert.equal(fs.existsSync(oldSettingsPath), true);
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "deny", pushReminders: true, includeHookEvents: true });
});

test("a malformed old file blocks none of its own and none of the rest (Q4 B)", async () => {
  reset();
  fs.writeFileSync(oldSettingsPath, "not json at all");
  fs.writeFileSync(oldPermissionsPath, JSON.stringify({ version: 1, mode: "ask", push: true }));
  const result = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(result.ran, true);
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "ask", pushReminders: true, includeHookEvents: false });
  assert.equal(fs.existsSync(oldSettingsPath), false, "the unreadable file was kept");
  assert.equal(fs.existsSync(oldPermissionsPath), false);
  assert.equal(readJson(piWebSettingsPath).claudeMigrated, true);
});

test("plan migrates only while the old channel was ask, matching the old reader (r3)", async () => {
  reset();
  seedOld({ launch: "plan", channel: "deny" });
  await migration.ensureClaudeSelfStoredMigration(opts());
  await migration.claudeCustomModelsMigrationStep(opts());
  assert.equal(readJson(nativeSettingsPath).permissions.defaultMode, "default");
  reset();
  seedOld({ launch: "plan", channel: "ask" });
  await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(readJson(nativeSettingsPath).permissions.defaultMode, "plan");
});

test("with no old files the startup run is a no-op that only writes the marker", async () => {
  reset();
  const result = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(result.ran, true);
  assert.equal(fs.existsSync(nativeSettingsPath), false, "a no-op migration wrote a native settings file");
  assert.equal(readJson(piWebSettingsPath).claudeMigrated, true);
});

test("the preferences default to ask, push on, hooks off; a patch keeps the file's other keys (the Agent language's among them)", async () => {
  reset();
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "ask", pushReminders: true, includeHookEvents: false });
  await agentLanguage.writeStoredAgentLanguage("zh-CN", agentDir);
  const stored = await preferences.writeClaudePreferences({ approvalChannel: "deny" }, agentDir);
  assert.deepEqual(stored, { approvalChannel: "deny", pushReminders: true, includeHookEvents: false });
  const file = readJson(piWebSettingsPath);
  assert.equal(file.agentLanguage, "zh-CN");
  assert.deepEqual(file.claude, { approvalChannel: "deny", pushReminders: true, includeHookEvents: false });
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "deny", pushReminders: true, includeHookEvents: false });
  await preferences.writeClaudePreferences({ includeHookEvents: true, pushReminders: false }, agentDir);
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "deny", pushReminders: false, includeHookEvents: true });
});

test("a preference key the claude object already holds is never overwritten by a later migration pass (review r1#3)", async () => {
  reset();
  seedOld({ channel: "ask", push: true, hooks: false });
  await migration.ensureClaudeSelfStoredMigration(opts({ claudeBinary: unknownBinary }));
  await preferences.writeClaudePreferences({ approvalChannel: "deny" }, agentDir);
  await migration.ensureClaudeSelfStoredMigration(opts());
  await migration.claudeCustomModelsMigrationStep(opts());
  await migration.ensureClaudeSelfStoredMigration(opts());
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "deny", pushReminders: true, includeHookEvents: false });
  assert.equal(readJson(piWebSettingsPath).claudeMigrated, true);
});

// ── the customModels phase (85a3-3 AC2): the models page's POST ──────────

test("an empty native target migrates at once, only the source's customModels key is cleared and the marker is keyed by the config root (Q3/Q4)", async () => {
  reset();
  seedOld();
  const step = await migration.claudeCustomModelsMigrationStep(opts());
  assert.deepEqual({ status: step.status, changed: step.changed, shadowed: step.shadowed }, { status: "done", changed: true, shadowed: false });
  // The names land as claude's own rows, written through the adapter's gate.
  assert.deepEqual(readJson(nativeSettingsPath).modelPicker.options, [{ model: "claude-custom-1" }, { model: "claude-custom-2" }]);
  // Only the customModels key goes; the old file's other fields stay for 85a4.
  const old = readJson(oldSettingsPath);
  assert.equal("customModels" in old, false, "the whole old file was deleted");
  assert.equal(old.launchPermissionMode, "acceptEdits");
  assert.equal(old.includeHookEvents, true);
  assert.equal(markerOf()?.done, true, "no done marker for the target root");
  // A second step on the same root is a no-op; another root gets its own marker.
  const again = await migration.claudeCustomModelsMigrationStep(opts());
  assert.deepEqual({ status: again.status, changed: again.changed }, { status: "done", changed: false });
  const otherRoot = path.join(scratch, "other-claude-config");
  fs.mkdirSync(otherRoot, { recursive: true });
  const other = await migration.claudeCustomModelsMigrationStep(opts({ claudeConfigDir: otherRoot }));
  assert.deepEqual({ status: other.status, changed: other.changed }, { status: "done", changed: false });
  assert.equal(markerOf(otherRoot)?.done, true, "a new config root was left without its own marker");
});

test("a non-empty native picker answers a merge preview: the native rows win, only missing names append, nothing is written (Q3)", async () => {
  reset();
  seedOld({ models: ["both-sides", "claude-old-1"] });
  fs.writeFileSync(nativeSettingsPath, JSON.stringify({
    modelPicker: {
      replaceBuiltInOptions: true,
      options: [
        { model: "native-keep", label: "K" },
        { model: "both-sides", description: "the native row wins" },
      ],
    },
  }));
  const before = fs.readFileSync(nativeSettingsPath, "utf8");
  const step = await migration.claudeCustomModelsMigrationStep(opts());
  assert.equal(step.status, "preview");
  assert.deepEqual(step.native, [{ model: "native-keep", label: "K" }, { model: "both-sides" }]);
  assert.deepEqual(step.missing, ["claude-old-1"]);
  assert.match(step.digest, /^[0-9a-f]{64}$/, "the digest is the preview's version token");
  assert.equal(fs.readFileSync(nativeSettingsPath, "utf8"), before, "the preview wrote the native file");
  assert.deepEqual(readJson(oldSettingsPath).customModels, ["both-sides", "claude-old-1"]);
  assert.equal(markerOf(), undefined, "the preview wrote the marker");
});

test("the confirm applies the preview: the native metadata survives, the missing names append, the source key clears (Q3)", async () => {
  reset();
  seedOld({ models: ["both-sides", "claude-old-1"] });
  fs.writeFileSync(nativeSettingsPath, JSON.stringify({
    modelPicker: {
      replaceBuiltInOptions: true,
      options: [
        { model: "native-keep", label: "K" },
        { model: "both-sides", description: "the native row wins" },
      ],
    },
  }));
  const step = await migration.claudeCustomModelsMigrationStep(opts());
  const applied = await migration.applyClaudeCustomModelsMigration(opts(), step.digest);
  assert.deepEqual({ status: applied.status, changed: applied.changed, shadowed: applied.shadowed }, { status: "done", changed: true, shadowed: false });
  assert.deepEqual(readJson(nativeSettingsPath).modelPicker, {
    replaceBuiltInOptions: true,
    options: [
      { model: "native-keep", label: "K" },
      { model: "both-sides", description: "the native row wins" },
      { model: "claude-old-1" },
    ],
  });
  assert.equal("customModels" in readJson(oldSettingsPath), false);
  assert.equal(markerOf()?.done, true);
});

test("a changed target between preview and confirm is a conflict with a fresh preview; the fresh digest then applies (Q2)", async () => {
  reset();
  seedOld({ models: ["claude-old-1"] });
  fs.writeFileSync(nativeSettingsPath, JSON.stringify({ modelPicker: { options: [{ model: "native-keep" }] } }));
  const step = await migration.claudeCustomModelsMigrationStep(opts());
  // The user's own edit lands between the preview and the confirm.
  fs.writeFileSync(nativeSettingsPath, JSON.stringify({ modelPicker: { options: [{ model: "native-keep" }, { model: "user-added" }] } }));
  const conflict = await migration.applyClaudeCustomModelsMigration(opts(), step.digest);
  assert.equal(conflict.status, "conflict");
  assert.deepEqual(conflict.preview.missing, ["claude-old-1"]);
  assert.notEqual(conflict.preview.digest, step.digest);
  assert.equal(markerOf(), undefined, "the conflict wrote the marker");
  const applied = await migration.applyClaudeCustomModelsMigration(opts(), conflict.preview.digest);
  assert.equal(applied.status, "done");
  assert.deepEqual(readJson(nativeSettingsPath).modelPicker.options, [
    { model: "native-keep" },
    { model: "user-added" },
    { model: "claude-old-1" },
  ]);
});

test("a source the target already fully covers clears its key without a re-append (crash replay between write and clear)", async () => {
  reset();
  seedOld({ models: ["claude-custom-1"] });
  // The crash already wrote the target and verified it; only the clear is left.
  fs.writeFileSync(nativeSettingsPath, JSON.stringify({
    modelPicker: { options: [{ model: "claude-custom-1", label: "landed before the crash" }] },
  }));
  const before = fs.readFileSync(nativeSettingsPath, "utf8");
  const step = await migration.claudeCustomModelsMigrationStep(opts());
  assert.deepEqual({ status: step.status, changed: step.changed }, { status: "done", changed: true });
  assert.equal(fs.readFileSync(nativeSettingsPath, "utf8"), before, "the replay rewrote the target");
  assert.equal("customModels" in readJson(oldSettingsPath), false);
});

test("an empty customModels list clears the key without any native write", async () => {
  reset();
  seedOld({ models: [] });
  const step = await migration.claudeCustomModelsMigrationStep(opts());
  assert.deepEqual({ status: step.status, changed: step.changed }, { status: "done", changed: true });
  assert.equal(fs.existsSync(nativeSettingsPath), false, "an empty list wrote the native settings");
  assert.equal("customModels" in readJson(oldSettingsPath), false);
  assert.equal(markerOf()?.done, true);
});

test("illegal or over-cap source data reports a blockage, keeps the source and writes nothing (Q3)", async () => {
  reset();
  const cases = [
    ["junk-string"],
    [["claude-custom-1", 42]],
    [Array.from({ length: 51 }, (_, index) => `claude-custom-${index}`)],
  ].map(([value]) => value);
  for (const [index, models] of cases.entries()) {
    fs.writeFileSync(oldSettingsPath, JSON.stringify({ version: 1, customModels: models }));
    const step = await migration.claudeCustomModelsMigrationStep(opts());
    assert.equal(step.status, "blocked", `case ${index}: ${JSON.stringify(step)}`);
    assert.ok(step.reason.length > 0);
    assert.equal(fs.existsSync(nativeSettingsPath), false, `case ${index} wrote the native settings`);
    assert.deepEqual(readJson(oldSettingsPath).customModels, models, `case ${index} touched the source`);
    assert.equal(markerOf(), undefined, `case ${index} wrote the marker`);
  }
});

test("an unknown claude version defers the phase: nothing is written, the source stays (AC1's refusal duty)", async () => {
  reset();
  seedOld();
  const step = await migration.claudeCustomModelsMigrationStep(opts({ claudeBinary: unknownBinary }));
  assert.equal(step.status, "deferred");
  assert.ok(step.reason.includes("3.0.0"), step.reason);
  assert.equal(fs.existsSync(nativeSettingsPath), false);
  assert.deepEqual(readJson(oldSettingsPath).customModels, ["claude-custom-1", "claude-custom-2"]);
  assert.equal(markerOf(), undefined);
});

test("a bad native picker shape blocks the phase with the write's own preflight reason (坏形状拒写)", async () => {
  reset();
  seedOld({ models: ["claude-custom-1"] });
  fs.writeFileSync(nativeSettingsPath, JSON.stringify({ modelPicker: { options: "junk" } }));
  const before = fs.readFileSync(nativeSettingsPath, "utf8");
  const step = await migration.claudeCustomModelsMigrationStep(opts());
  assert.equal(step.status, "blocked");
  assert.ok(step.reason.includes("modelPicker"), step.reason);
  assert.equal(fs.readFileSync(nativeSettingsPath, "utf8"), before);
  assert.deepEqual(readJson(oldSettingsPath).customModels, ["claude-custom-1"]);
});

test("a managed modelPicker shadows the user target: the answers carry shadowed for the「已保存、未生效」hint (Q6)", async () => {
  reset();
  seedOld({ models: ["claude-old-1"] });
  fs.writeFileSync(managedSettingsPath, JSON.stringify({ modelPicker: { options: [{ model: "managed-only" }] } }));
  fs.writeFileSync(nativeSettingsPath, JSON.stringify({ modelPicker: { options: [{ model: "native-keep" }] } }));
  const step = await migration.claudeCustomModelsMigrationStep(opts());
  assert.equal(step.status, "preview");
  assert.equal(step.shadowed, true);
  const applied = await migration.applyClaudeCustomModelsMigration(opts(), step.digest);
  assert.equal(applied.status, "done");
  assert.equal(applied.shadowed, true);
});

test("with nothing to migrate the step only writes the per-root marker (the no-op first visit)", async () => {
  reset();
  const step = await migration.claudeCustomModelsMigrationStep(opts());
  assert.deepEqual({ status: step.status, changed: step.changed, shadowed: step.shadowed }, { status: "done", changed: false, shadowed: false });
  assert.equal(fs.existsSync(nativeSettingsPath), false);
  assert.equal(markerOf()?.done, true);
  // A settings file whose customModels key never existed marks and keeps the
  // file (its other fields are 85a4's to move).
  reset();
  fs.writeFileSync(oldSettingsPath, JSON.stringify({ version: 1, launchPermissionMode: "acceptEdits" }));
  const keyed = await migration.claudeCustomModelsMigrationStep(opts());
  assert.deepEqual({ status: keyed.status, changed: keyed.changed }, { status: "done", changed: false });
  assert.equal(fs.existsSync(oldSettingsPath), true, "the step deleted a file it does not own");
  assert.equal(markerOf()?.done, true);
});
