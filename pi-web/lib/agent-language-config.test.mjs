import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const config = await jiti.import("./agent-language-config.ts");

function agentDir(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-agent-language-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test("a missing, malformed or unknown config reads as English", (t) => {
  const dir = agentDir(t);
  const file = config.agentLanguageConfigPath(dir);
  assert.equal(file, path.join(dir, "pi-web-settings.json"));
  assert.equal(config.readStoredAgentLanguage(dir), "en");
  fs.writeFileSync(file, "{ not json");
  assert.equal(config.readStoredAgentLanguage(dir), "en");
  fs.writeFileSync(file, "null");
  assert.equal(config.readStoredAgentLanguage(dir), "en");
  fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "klingon" }));
  assert.equal(config.readStoredAgentLanguage(dir), "en");
  fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "zh-CN" }));
  assert.equal(config.readStoredAgentLanguage(dir), "zh-CN");
});

test("a write round-trips, keeps other keys and is private", async (t) => {
  const dir = agentDir(t);
  const file = config.agentLanguageConfigPath(dir);
  assert.equal(await config.writeStoredAgentLanguage("zh-CN", dir), "zh-CN");
  assert.equal(config.readStoredAgentLanguage(dir), "zh-CN");
  assert.deepEqual(JSON.parse(fs.readFileSync(file, "utf8")), { version: 1, agentLanguage: "zh-CN" });
  if (process.platform !== "win32") assert.equal(fs.statSync(file).mode & 0o777, 0o600);
  fs.writeFileSync(file, JSON.stringify({ other: { keep: true }, agentLanguage: "zh-CN" }));
  await config.writeStoredAgentLanguage("en", dir);
  assert.deepEqual(JSON.parse(fs.readFileSync(file, "utf8")), { other: { keep: true }, agentLanguage: "en", version: 1 });
  assert.deepEqual(fs.readdirSync(dir), ["pi-web-settings.json"]);
});

test("a malformed file is refused, not replaced", async (t) => {
  const dir = agentDir(t);
  const file = config.agentLanguageConfigPath(dir);
  for (const text of ["{ not json", "[1]", "\"zh-CN\""]) {
    fs.writeFileSync(file, text);
    await assert.rejects(config.writeStoredAgentLanguage("zh-CN", dir), /refusing to overwrite/);
    assert.equal(fs.readFileSync(file, "utf8"), text);
  }
});
