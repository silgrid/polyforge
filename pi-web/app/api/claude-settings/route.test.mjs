import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test, { after } from "node:test";
import { createJiti } from "jiti";
import { NextRequest } from "next/server.js";

// GET/PUT /api/claude-settings: Settings › Claude, the only writer of
// <pi agent dir>/pi-web-claude-settings.json.
const originalAgentDir = process.env.PI_CODING_AGENT_DIR;
const agentDir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-settings-route-"));
process.env.PI_CODING_AGENT_DIR = agentDir;
after(() => {
  fs.rmSync(agentDir, { recursive: true, force: true });
  if (originalAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = originalAgentDir;
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const { GET, PUT } = await jiti.import("./route.ts");
const file = path.join(agentDir, "pi-web-claude-settings.json");
// the global claude permission mode (absent: ask); plan follows it (r3).
const permissionConfig = path.join(agentDir, "claude-permissions.json");
const DEFAULTS = { launchPermissionMode: "default", customModels: [], includeHookEvents: false };

function put(body, headers = {}) {
  return new NextRequest("http://localhost/api/claude-settings", {
    method: "PUT",
    headers: { Host: "localhost", Origin: "http://localhost", "Sec-Fetch-Site": "same-origin", "Content-Type": "application/json", ...headers },
    body: typeof body === "string" ? body : JSON.stringify(body),
  });
}

async function read() {
  const response = await GET();
  assert.equal(response.status, 200);
  return response.json();
}

test("GET answers the stored settings and PUT stores them for the next GET", async () => {
  fs.rmSync(file, { force: true });
  assert.deepEqual(await read(), { ...DEFAULTS, planAvailable: true });

  const response = await PUT(put({ launchPermissionMode: "acceptEdits", customModels: ["claude-custom-e2e-1", "opus[1m]", "claude-custom-e2e-1"], includeHookEvents: true }));
  assert.equal(response.status, 200);
  const saved = { launchPermissionMode: "acceptEdits", customModels: ["claude-custom-e2e-1", "opus[1m]"], includeHookEvents: true };
  assert.deepEqual(await response.json(), saved);
  assert.deepEqual(await read(), { ...saved, planAvailable: true });
  assert.deepEqual(JSON.parse(fs.readFileSync(file, "utf8")), { version: 1, ...saved });
  // The Agent language's file is not this route's.
  assert.equal(fs.existsSync(path.join(agentDir, "pi-web-settings.json")), false);

  const back = await PUT(put(DEFAULTS));
  assert.equal(back.status, 200);
  assert.deepEqual(await back.json(), DEFAULTS);
  assert.deepEqual(await read(), { ...DEFAULTS, planAvailable: true });
});

test("PUT refuses plan while Pi Web has no host approvals, naming the reason", async () => {
  // r3: no host approvals is the claude tool permissions on Deny silently.
  fs.writeFileSync(permissionConfig, JSON.stringify({ version: 1, mode: "deny", push: false }));
  try {
    fs.writeFileSync(file, JSON.stringify({ version: 1, ...DEFAULTS }));
    const before = fs.readFileSync(file, "utf8");
    assert.deepEqual(await read(), { ...DEFAULTS, planAvailable: false });
    const response = await PUT(put({ launchPermissionMode: "plan", customModels: [], includeHookEvents: false }));
    assert.equal(response.status, 400);
    const { error } = await response.json();
    assert.match(error, /plan/i);
    assert.match(error, /Ask in a dialog/);
    assert.equal(fs.readFileSync(file, "utf8"), before);
  } finally {
    fs.rmSync(permissionConfig, { force: true });
  }
});

test("GET answers whether plan is available from's global permission mode and PUT stores plan under ask ( r3)", async () => {
  fs.rmSync(permissionConfig, { force: true });
  fs.rmSync(file, { force: true });
  assert.equal((await read()).planAvailable, true, "the default global mode (ask) offers plan");
  const response = await PUT(put({ launchPermissionMode: "plan", customModels: [], includeHookEvents: false }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { ...DEFAULTS, launchPermissionMode: "plan" });
  assert.equal(JSON.parse(fs.readFileSync(file, "utf8")).launchPermissionMode, "plan");
  assert.deepEqual(await read(), { ...DEFAULTS, launchPermissionMode: "plan", planAvailable: true });
  fs.writeFileSync(permissionConfig, JSON.stringify({ version: 1, mode: "ask", push: false }));
  try {
    assert.equal((await read()).planAvailable, true, "ask stored explicitly offers plan");
  } finally {
    fs.rmSync(permissionConfig, { force: true });
  }
  fs.rmSync(file, { force: true });
});

test("PUT refuses an invalid custom model name, a non-JSON or an untrusted request and writes nothing", async () => {
  fs.writeFileSync(file, JSON.stringify({ version: 1, launchPermissionMode: "acceptEdits", customModels: ["claude-custom-1"], includeHookEvents: true }));
  const before = fs.readFileSync(file, "utf8");
  const valid = { launchPermissionMode: "default", customModels: ["claude-custom-2"], includeHookEvents: false };
  const many = Array.from({ length: 51 }, (_, i) => `claude-custom-${i}`);
  const cases = [
    [put({ ...valid, customModels: ["-bad name"] }), 400, /-bad name/],
    [put({ ...valid, customModels: ["has space"] }), 400, /has space/],
    [put({ ...valid, customModels: [""] }), 400],
    [put({ ...valid, customModels: ["x".repeat(129)] }), 400],
    [put({ ...valid, customModels: [5] }), 400],
    [put({ ...valid, customModels: many }), 400, /50/],
    [put({ ...valid, customModels: "opus" }), 400],
    [put({ ...valid, launchPermissionMode: "bypassPermissions" }), 400],
    [put({ ...valid, includeHookEvents: "yes" }), 400],
    [put({ ...valid, extra: 1 }), 400],
    [put({ launchPermissionMode: "default", includeHookEvents: false }), 400],
    [put([valid]), 400],
    [put("{ nope"), 400],
    [put(valid, { "Content-Type": "text/plain" }), 415],
    [put(valid, { Origin: "https://evil.example", "Sec-Fetch-Site": "cross-site" }), 403],
    [put(valid, { Host: "evil.example" }), 403],
  ];
  for (const [request, status, pattern] of cases) {
    const response = await PUT(request);
    assert.equal(response.status, status);
    const { error } = await response.json();
    assert.equal(typeof error, "string");
    if (pattern) assert.match(error, pattern);
  }
  assert.equal(fs.readFileSync(file, "utf8"), before);

  // A file the store cannot keep is refused, not replaced.
  fs.writeFileSync(file, "{ broken");
  const refused = await PUT(put(valid));
  assert.equal(refused.status, 500);
  assert.match((await refused.json()).error, /refusing to overwrite/);
  assert.equal(fs.readFileSync(file, "utf8"), "{ broken");
  fs.rmSync(file, { force: true });
});
