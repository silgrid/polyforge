// carries: lib/claude-migration.ts, lib/claude-preferences.ts, lib/pi-web-settings.ts, lib/agent-language-config.ts
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// polyforge#150 (85a4) AC3: the two self-stored claude files —
// pi-web-claude-settings.json and claude-permissions.json — migrate field by
// field into what now owns them (claude's native user settings.json for
// launchPermissionMode → permissions.defaultMode and customModels →
// modelPicker.options; pi-web-settings.json's `claude` object for the fields
// with no native home: the approval channel, push reminders and hook
// activity), then both old files are deleted and the migration is marked, so
// nothing ever writes them again. Expected values are hand-derived from the
// ticket's Q4 B / Q5 A rulings (copy, write, re-read, verify, mark, delete;
// repeatable and interrupt-safe; native values win over stored ones) and the
// write gate's rule that an unknown claude version defers the whole
// migration. The fixtures are generated here (85a1's Q6 A).
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

const opts = (extra = {}) => ({
  agentDir,
  claudeConfigDir,
  claudeBinary: okBinary,
  env: {},
  managedSettingsPath: path.join(scratch, "managed-settings.json"),
  ...extra,
});

/** Seeds both old files with the given fields (defaults from the ticket's era). */
function seedOld({ launch = "acceptEdits", models = ["claude-custom-1", "claude-custom-2"], hooks = true, channel = "deny", push = false } = {}) {
  fs.writeFileSync(oldSettingsPath, JSON.stringify({ version: 1, launchPermissionMode: launch, customModels: models, includeHookEvents: hooks }));
  fs.writeFileSync(oldPermissionsPath, JSON.stringify({ version: 1, mode: channel, push }));
}

/** A clean slate: nothing migrated, no old files, no native settings. */
function reset() {
  for (const file of [oldSettingsPath, oldPermissionsPath, nativeSettingsPath, piWebSettingsPath]) fs.rmSync(file, { force: true });
  migration.resetClaudeMigrationForTests();
}

test("the full migration: every field lands where it now lives, both old files are deleted and the marker is written (AC3)", async () => {
  reset();
  seedOld();
  const result = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.deepEqual(result, { ran: true, summary: "migrated includeHookEvents, mode, push, launchPermissionMode, customModels" });
  // The native user settings.json owns the launch permission mode and the custom names.
  const native = JSON.parse(fs.readFileSync(nativeSettingsPath, "utf8"));
  assert.equal(native.permissions.defaultMode, "acceptEdits");
  assert.deepEqual(native.modelPicker.options, [{ model: "claude-custom-1" }, { model: "claude-custom-2" }]);
  // The fields with no native home are Pi Web preferences now.
  const stored = JSON.parse(fs.readFileSync(piWebSettingsPath, "utf8"));
  assert.deepEqual(stored.claude, { approvalChannel: "deny", pushReminders: false, includeHookEvents: true });
  assert.equal(stored.claudeMigrated, true);
  // Both old files are gone, and the readers answer the migrated values.
  assert.equal(fs.existsSync(oldSettingsPath), false, "pi-web-claude-settings.json was kept");
  assert.equal(fs.existsSync(oldPermissionsPath), false, "claude-permissions.json was kept");
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "deny", pushReminders: false, includeHookEvents: true });
});

test("idempotent: a second run with the marker skips and touches nothing (Q4 B)", async () => {
  reset();
  seedOld();
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
  assert.equal(JSON.parse(fs.readFileSync(piWebSettingsPath, "utf8")).claudeMigrated, true);
  assert.equal(fs.existsSync(nativeSettingsPath), false, "the replay wrote a native file for nothing");
});

test("native wins: an existing native defaultMode or modelPicker is never overwritten; the old files still go (Q4 B)", async () => {
  reset();
  fs.writeFileSync(nativeSettingsPath, JSON.stringify({
    model: "opus",
    permissions: { defaultMode: "auto" },
    modelPicker: { options: [{ model: "claude-native-model" }] },
  }));
  seedOld();
  const result = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(result.ran, true);
  const native = JSON.parse(fs.readFileSync(nativeSettingsPath, "utf8"));
  assert.equal(native.permissions.defaultMode, "auto", "the stored mode overwrote the native one");
  assert.deepEqual(native.modelPicker.options, [{ model: "claude-native-model" }], "the stored custom names overwrote the native picker");
  assert.equal(native.model, "opus");
  assert.equal(fs.existsSync(oldSettingsPath), false);
  // The preference fields still migrate: they have no native home.
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "deny", pushReminders: false, includeHookEvents: true });
});

test("the write gate defers only the native writes — the preference fields migrate first, and a preference changed meanwhile survives the completing run (review r1#3, Q2 C)", async () => {
  reset();
  seedOld();
  const deferred = await migration.ensureClaudeSelfStoredMigration(opts({ claudeBinary: unknownBinary }));
  assert.equal(deferred.ran, false);
  assert.ok(deferred.reason.includes("3.0.0"), deferred.reason);
  // The preference fields have no claude-native home, so no version gate
  // applies: they already migrated while the native writes defer.
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "deny", pushReminders: false, includeHookEvents: true });
  assert.equal(fs.existsSync(oldSettingsPath), true, "a deferred migration deleted an old file");
  assert.equal(fs.existsSync(oldPermissionsPath), true);
  assert.equal(JSON.parse(fs.readFileSync(piWebSettingsPath, "utf8")).claudeMigrated, undefined, "a deferred migration wrote the marker");
  assert.equal(fs.existsSync(nativeSettingsPath), false, "a deferred migration wrote the native settings");
  // A preference the user changes during the deferral is the user's: the
  // completing run keeps it and never overwrites it with the old file's value.
  await preferences.writeClaudePreferences({ pushReminders: true }, agentDir);
  const done = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(done.ran, true);
  assert.equal(fs.existsSync(oldSettingsPath), false);
  assert.equal(JSON.parse(fs.readFileSync(nativeSettingsPath, "utf8")).permissions.defaultMode, "acceptEdits");
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
  assert.equal(JSON.parse(fs.readFileSync(piWebSettingsPath, "utf8")).claudeMigrated, true);
});

test("plan migrates only while the old channel was ask, matching the old reader (r3)", async () => {
  reset();
  seedOld({ launch: "plan", channel: "deny" });
  await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(JSON.parse(fs.readFileSync(nativeSettingsPath, "utf8")).permissions.defaultMode, "default");
  reset();
  seedOld({ launch: "plan", channel: "ask" });
  await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(JSON.parse(fs.readFileSync(nativeSettingsPath, "utf8")).permissions.defaultMode, "plan");
});

test("with no old files the run is a no-op that only writes the marker", async () => {
  reset();
  const result = await migration.ensureClaudeSelfStoredMigration(opts());
  assert.equal(result.ran, true);
  assert.equal(fs.existsSync(nativeSettingsPath), false, "a no-op migration wrote a native settings file");
  assert.equal(JSON.parse(fs.readFileSync(piWebSettingsPath, "utf8")).claudeMigrated, true);
});

test("the preferences default to ask, push on, hooks off; a patch keeps the file's other keys (the Agent language's among them)", async () => {
  reset();
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "ask", pushReminders: true, includeHookEvents: false });
  await agentLanguage.writeStoredAgentLanguage("zh-CN", agentDir);
  const stored = await preferences.writeClaudePreferences({ approvalChannel: "deny" }, agentDir);
  assert.deepEqual(stored, { approvalChannel: "deny", pushReminders: true, includeHookEvents: false });
  const file = JSON.parse(fs.readFileSync(piWebSettingsPath, "utf8"));
  assert.equal(file.agentLanguage, "zh-CN");
  assert.deepEqual(file.claude, { approvalChannel: "deny", pushReminders: true, includeHookEvents: false });
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "deny", pushReminders: true, includeHookEvents: false });
  await preferences.writeClaudePreferences({ includeHookEvents: true, pushReminders: false }, agentDir);
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "deny", pushReminders: false, includeHookEvents: true });
});


test("a preference key the claude object already holds is never overwritten by a later migration pass (review r1#3)", async () => {
  reset();
  seedOld({ channel: "ask", push: true, hooks: false });
  // A first deferral migrated the preference fields; the user then changed
  // the channel before any native write could run.
  await migration.ensureClaudeSelfStoredMigration(opts({ claudeBinary: unknownBinary }));
  await preferences.writeClaudePreferences({ approvalChannel: "deny" }, agentDir);
  await migration.ensureClaudeSelfStoredMigration(opts());
  assert.deepEqual(preferences.readClaudePreferences(agentDir), { approvalChannel: "deny", pushReminders: true, includeHookEvents: false });
  assert.equal(JSON.parse(fs.readFileSync(piWebSettingsPath, "utf8")).claudeMigrated, true);
});
