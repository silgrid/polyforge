import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const config = await jiti.import("./claude-permission-config.ts");

function agentDir(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-permissions-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test("a missing or malformed file reads mode ask and push on; a write keeps the other key, is 0600 and refuses a non-object file", async (t) => {
  const dir = agentDir(t);
  const file = config.claudePermissionConfigPath(dir);
  assert.equal(file, path.join(dir, "claude-permissions.json"));

  // Missing, malformed or unknown values read as the defaults; never throws.
  const defaults = { mode: "ask", push: true };
  assert.deepEqual(config.readClaudePermissionConfig(dir), defaults);
  for (const text of ["{ not json", "null", "[1]", "\"deny\"", JSON.stringify({ version: 1, mode: "maybe", push: "yes" })]) {
    fs.writeFileSync(file, text);
    assert.deepEqual(config.readClaudePermissionConfig(dir), defaults, text);
  }
  fs.writeFileSync(file, JSON.stringify({ version: 1, mode: "deny" }));
  assert.deepEqual(config.readClaudePermissionConfig(dir), { mode: "deny", push: true });
  fs.writeFileSync(file, JSON.stringify({ version: 1, push: false }));
  assert.deepEqual(config.readClaudePermissionConfig(dir), { mode: "ask", push: false });

  // A write of one key keeps the other key and any unknown key; the file is private.
  fs.rmSync(file);
  assert.deepEqual(await config.writeClaudePermissionConfig({ mode: "deny" }, dir), { mode: "deny", push: true });
  assert.deepEqual(config.readClaudePermissionConfig(dir), { mode: "deny", push: true });
  assert.deepEqual(await config.writeClaudePermissionConfig({ push: false }, dir), { mode: "deny", push: false });
  assert.deepEqual(JSON.parse(fs.readFileSync(file, "utf8")), { version: 1, mode: "deny", push: false });
  if (process.platform !== "win32") assert.equal(fs.statSync(file).mode & 0o777, 0o600);
  fs.writeFileSync(file, JSON.stringify({ other: { keep: true }, mode: "deny", push: false }));
  await config.writeClaudePermissionConfig({ mode: "ask" }, dir);
  assert.deepEqual(JSON.parse(fs.readFileSync(file, "utf8")), { other: { keep: true }, mode: "ask", push: false, version: 1 });
  assert.deepEqual(fs.readdirSync(dir), ["claude-permissions.json"]);

  // A file that exists but is not a JSON object is refused, never replaced.
  for (const text of ["{ not json", "[1]", "\"deny\""]) {
    fs.writeFileSync(file, text);
    await assert.rejects(config.writeClaudePermissionConfig({ mode: "deny" }, dir), /refusing to overwrite/);
    assert.equal(fs.readFileSync(file, "utf8"), text);
  }
});
