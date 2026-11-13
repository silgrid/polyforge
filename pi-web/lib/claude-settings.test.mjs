import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// Settings › Claude's store: <pi agent dir>/pi-web-claude-settings.json,
// pi-web's own file beside (never inside) the Agent language's pi-web-settings.json.
const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const settings = await jiti.import("./claude-settings.ts");

const DEFAULTS = { launchPermissionMode: "default", customModels: [], includeHookEvents: false };

function agentDir(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-settings-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test("claude settings: a missing, malformed or unknown file reads the defaults", (t) => {
  const dir = agentDir(t);
  const file = settings.claudeSettingsPath(dir);
  assert.equal(file, path.join(dir, "pi-web-claude-settings.json"));
  assert.deepEqual(settings.readClaudeSettings(dir), DEFAULTS);
  for (const text of ["{ not json", "null", "[1]", "\"acceptEdits\""]) {
    fs.writeFileSync(file, text);
    assert.deepEqual(settings.readClaudeSettings(dir), DEFAULTS, text);
  }
  // Unknown values each read as their default; claude's other modes are not offered.
  fs.writeFileSync(file, JSON.stringify({ version: 1, launchPermissionMode: "bypassPermissions", customModels: "opus", includeHookEvents: "yes" }));
  assert.deepEqual(settings.readClaudeSettings(dir), DEFAULTS);
  // plan needs host approvals:'s claude tool permissions on ask ( r3).
  // On deny a stored plan reads as default; on ask (the default) it reads as plan.
  const permissionConfig = path.join(dir, "claude-permissions.json");
  fs.writeFileSync(permissionConfig, JSON.stringify({ version: 1, mode: "deny", push: false }));
  fs.writeFileSync(file, JSON.stringify({ version: 1, launchPermissionMode: "plan" }));
  assert.deepEqual(settings.readClaudeSettings(dir), DEFAULTS);
  fs.writeFileSync(permissionConfig, JSON.stringify({ version: 1, mode: "ask", push: false }));
  assert.deepEqual(settings.readClaudeSettings(dir), { ...DEFAULTS, launchPermissionMode: "plan" });
  fs.rmSync(permissionConfig);
  assert.deepEqual(settings.readClaudeSettings(dir), { ...DEFAULTS, launchPermissionMode: "plan" });
  // A valid file is read; an invalid or repeated custom name is dropped, not the file.
  fs.writeFileSync(file, JSON.stringify({
    version: 1,
    launchPermissionMode: "acceptEdits",
    customModels: ["opus[1m]", "-bad", "opus[1m]", 5, "", "claude-custom-1"],
    includeHookEvents: true,
  }));
  assert.deepEqual(settings.readClaudeSettings(dir), { launchPermissionMode: "acceptEdits", customModels: ["opus[1m]", "claude-custom-1"], includeHookEvents: true });
  assert.deepEqual(settings.normalizeClaudeSettings({ launchPermissionMode: "acceptEdits" }), { ...DEFAULTS, launchPermissionMode: "acceptEdits" });
});

test("a write round-trips, keeps other keys, is private and refuses a malformed file", async (t) => {
  const dir = agentDir(t);
  const file = settings.claudeSettingsPath(dir);
  const stored = { launchPermissionMode: "acceptEdits", customModels: ["claude-custom-1", "us.anthropic.claude-x-v1:0"], includeHookEvents: true };
  assert.deepEqual(await settings.writeClaudeSettings(stored, dir), stored);
  assert.deepEqual(settings.readClaudeSettings(dir), stored);
  assert.deepEqual(JSON.parse(fs.readFileSync(file, "utf8")), { version: 1, ...stored });
  if (process.platform !== "win32") assert.equal(fs.statSync(file).mode & 0o777, 0o600);
  // Only its own file: no temp file left, and the Agent language's file is never written.
  assert.deepEqual(fs.readdirSync(dir), ["pi-web-claude-settings.json"]);

  fs.writeFileSync(file, JSON.stringify({ other: { keep: true }, launchPermissionMode: "acceptEdits" }));
  await settings.writeClaudeSettings(DEFAULTS, dir);
  assert.deepEqual(JSON.parse(fs.readFileSync(file, "utf8")), { other: { keep: true }, version: 1, ...DEFAULTS });
  assert.deepEqual(settings.readClaudeSettings(dir), DEFAULTS);

  for (const text of ["{ not json", "[1]", "\"acceptEdits\""]) {
    fs.writeFileSync(file, text);
    await assert.rejects(settings.writeClaudeSettings(stored, dir), /refusing to overwrite/);
    assert.equal(fs.readFileSync(file, "utf8"), text);
  }
});
