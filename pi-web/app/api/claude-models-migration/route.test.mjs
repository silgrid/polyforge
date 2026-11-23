// carries: app/api/claude-models-migration/route.ts, lib/claude-migration.ts
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// POST /api/claude-models-migration (85a3-3 AC2): the models page's protected
// migration POST. The guards run before anything else (trust, JSON-only, the
// {} | { confirm } body); {} answers the phase as it stands — an empty
// native target migrates at once (changed: true), a non-empty one answers the
// merge preview and writes nothing, illegal data and an unknown claude answer
// their reason with nothing written; { confirm } applies the preview under
// its digest, and a source or target that changed in between answers 409 with
// the fresh preview. Expected values are hand-derived from the owner rulings
// (Q2/Q3), never from the implementation's output. The `shadowed` hint is
// pinned at the lib level (the managed path is the server's own platform
// truth, not something a route test may plant).
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-models-migration-route-"));
const agentDir = path.join(scratch, "agent");
const claudeConfigDir = path.join(scratch, "claude-config");
fs.mkdirSync(agentDir, { recursive: true });
fs.mkdirSync(claudeConfigDir, { recursive: true });
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.CLAUDE_CONFIG_DIR = claudeConfigDir;

/** A stub `claude` answering initialize with the given claude_code_version. */
// The stub answers initialize from the 2.1.322 recording beside itself (its
// ../claude-2.1.322), so it lives one level under scratch and the fixture
// link sits in scratch; PI_TEST_CLAUDE_STUB_VERSION replaces the version
// each stub reports.
const stubDir = path.join(scratch, "stub-bin");
fs.mkdirSync(stubDir);
fs.copyFileSync(new URL("../../../lib/__fixtures__/claude-stub/claude.mjs", import.meta.url), path.join(stubDir, "claude.mjs"));
fs.symlinkSync(decodeURIComponent(new URL("../../../lib/__fixtures__/claude-2.1.322", import.meta.url).pathname), path.join(scratch, "claude-2.1.322"));
const okDir = path.join(scratch, "ok-bin");
fs.mkdirSync(okDir);
fs.writeFileSync(path.join(okDir, "claude"), `#!/bin/sh\nexport PI_TEST_CLAUDE_STUB_VERSION=2.1.326\nexec node ${path.join(stubDir, "claude.mjs")} "$@"\n`, { mode: 0o755 });
const unknownDir = path.join(scratch, "unknown-bin");
fs.mkdirSync(unknownDir);
fs.writeFileSync(path.join(unknownDir, "claude"), `#!/bin/sh\nexport PI_TEST_CLAUDE_STUB_VERSION=3.0.0\nexec node ${path.join(stubDir, "claude.mjs")} "$@"\n`, { mode: 0o755 });
process.env.PI_TEST_CLAUDE_STUB_LOG = path.join(scratch, "stub.jsonl");
const originalPath = process.env.PATH;
process.env.PATH = `${okDir}${path.delimiter}${originalPath}`;

test.after(() => {
  process.env.PATH = originalPath;
  fs.rmSync(scratch, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const { POST } = await jiti.import("./route.ts");

const oldSettingsPath = path.join(agentDir, "pi-web-claude-settings.json");
const nativeSettingsPath = path.join(claudeConfigDir, "settings.json");
const piWebSettingsPath = path.join(agentDir, "pi-web-settings.json");
const readJson = (file) => JSON.parse(fs.readFileSync(file, "utf8"));

const request = (body, headers = {}) => new Request("http://localhost/api/claude-models-migration", {
  method: "POST",
  headers: { "content-type": "application/json", host: "localhost", ...headers },
  ...(body === undefined ? {} : { body: typeof body === "string" ? body : JSON.stringify(body) }),
});

/** A clean slate with the given old customModels and native modelPicker. */
function reset({ oldModels, picker } = {}) {
  for (const file of [oldSettingsPath, nativeSettingsPath, piWebSettingsPath]) fs.rmSync(file, { force: true });
  if (oldModels !== undefined) {
    fs.writeFileSync(oldSettingsPath, JSON.stringify({ version: 1, launchPermissionMode: "acceptEdits", customModels: oldModels, includeHookEvents: true }));
  }
  if (picker !== undefined) {
    fs.writeFileSync(nativeSettingsPath, JSON.stringify({ modelPicker: picker }));
  }
  process.env.PATH = `${okDir}${path.delimiter}${originalPath}`;
}

test("the guards run before anything else: trust, content type, JSON and the {} | { confirm } body shape", async () => {
  reset();
  const denied = await POST(request({}, { host: "evil.example", origin: "https://evil.example", "sec-fetch-site": "cross-site" }));
  assert.equal(denied.status, 403);
  const notJson = await POST(new Request("http://localhost/api/claude-models-migration", { method: "POST", headers: { host: "localhost", "content-type": "text/plain" }, body: "{}" }));
  assert.equal(notJson.status, 415);
  const broken = await POST(request("not json at all"));
  assert.equal(broken.status, 400);
  assert.equal((await broken.json()).error, "invalid-json");
  for (const [name, body] of [["an array", ["x"]], ["a foreign key", { junk: 1 }], ["a non-string confirm", { confirm: 42 }]]) {
    const refused = await POST(request(body));
    assert.equal(refused.status, 400, `${name}: ${refused.status}`);
    assert.equal((await refused.json()).error, "invalid-body");
  }
  assert.equal(fs.existsSync(nativeSettingsPath), false, "a guarded request wrote something");
});

test("POST {} with nothing left answers done; an empty target migrates at once (Q2's automatic half)", async () => {
  reset();
  const nothing = await POST(request({}));
  assert.equal(nothing.status, 200);
  assert.deepEqual((await nothing.json()).status, "done");
  reset({ oldModels: ["claude-old-1", "claude-old-2"] });
  const migrated = await POST(request({}));
  assert.equal(migrated.status, 200);
  const body = await migrated.json();
  assert.equal(body.status, "done");
  assert.equal(body.changed, true);
  // The names land in claude's own native picker; the old file keeps its
  // other fields and loses only the customModels key.
  assert.deepEqual(readJson(nativeSettingsPath).modelPicker.options, [{ model: "claude-old-1" }, { model: "claude-old-2" }]);
  const old = readJson(oldSettingsPath);
  assert.equal("customModels" in old, false);
  assert.equal(old.launchPermissionMode, "acceptEdits");
  // Idempotent: a second POST is done and changes nothing.
  const again = await POST(request({}));
  assert.deepEqual(await again.json(), { status: "done", changed: false, shadowed: false });
});

test("POST {} answers the merge preview for a non-empty target and writes nothing until the confirm (Q3)", async () => {
  reset({ oldModels: ["both-sides", "claude-old-1"], picker: { options: [{ model: "native-keep", label: "K" }, { model: "both-sides" }] } });
  const before = fs.readFileSync(nativeSettingsPath, "utf8");
  const preview = await POST(request({}));
  assert.equal(preview.status, 200);
  const body = await preview.json();
  assert.equal(body.status, "preview");
  assert.deepEqual(body.native, [{ model: "native-keep", label: "K" }, { model: "both-sides" }]);
  assert.deepEqual(body.missing, ["claude-old-1"]);
  assert.match(body.digest, /^[0-9a-f]{64}$/);
  assert.equal(fs.readFileSync(nativeSettingsPath, "utf8"), before, "the preview wrote the native file");
  assert.deepEqual(readJson(oldSettingsPath).customModels, ["both-sides", "claude-old-1"]);

  // The confirm applies the preview; a changed target is a 409 conflict.
  const applied = await POST(request({ confirm: body.digest }));
  assert.equal(applied.status, 200);
  assert.equal((await applied.json()).status, "done");
  assert.deepEqual(readJson(nativeSettingsPath).modelPicker.options, [
    { model: "native-keep", label: "K" },
    { model: "both-sides" },
    { model: "claude-old-1" },
  ]);
  assert.equal("customModels" in readJson(oldSettingsPath), false);

  reset({ oldModels: ["claude-old-1"], picker: { options: [{ model: "native-keep" }] } });
  const stale = await POST(request({}));
  const staleDigest = (await stale.json()).digest;
  fs.writeFileSync(nativeSettingsPath, JSON.stringify({ modelPicker: { options: [{ model: "native-keep" }, { model: "user-added" }] } }));
  const conflict = await POST(request({ confirm: staleDigest }));
  assert.equal(conflict.status, 409);
  const fresh = await conflict.json();
  assert.equal(fresh.status, "conflict");
  assert.deepEqual(fresh.missing, ["claude-old-1"]);
  assert.notEqual(fresh.digest, staleDigest);
  const retried = await POST(request({ confirm: fresh.digest }));
  assert.equal((await retried.json()).status, "done");
});

test("illegal source data and an unknown claude version answer their reason with nothing written", async () => {
  reset({ oldModels: "junk-not-an-array" });
  const blocked = await POST(request({}));
  assert.equal(blocked.status, 200);
  const blockBody = await blocked.json();
  assert.equal(blockBody.status, "blocked");
  assert.ok(blockBody.reason.length > 0);
  assert.deepEqual(readJson(oldSettingsPath).customModels, "junk-not-an-array");
  assert.equal(fs.existsSync(nativeSettingsPath), false);

  reset({ oldModels: ["claude-old-1"] });
  process.env.PATH = `${unknownDir}${path.delimiter}${originalPath}`;
  const deferred = await POST(request({}));
  assert.equal(deferred.status, 200);
  const deferBody = await deferred.json();
  assert.equal(deferBody.status, "deferred");
  assert.ok(deferBody.reason.includes("3.0.0"), deferBody.reason);
  assert.equal(fs.existsSync(nativeSettingsPath), false, "a deferred phase wrote the native settings");
  assert.deepEqual(readJson(oldSettingsPath).customModels, ["claude-old-1"]);
});
