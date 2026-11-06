import "#test-env";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { createJiti } from "jiti";
import { NextRequest } from "next/server.js";

// GET/PUT /api/claude-permissions: the global claude permission settings and
// one claude session's override. A busy session is a live stub child mid-turn.
process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-permissions-route-"));
const agentDir = path.join(scratch, "agent");
const binDir = path.join(scratch, "bin");
const project = path.join(scratch, "project");
fs.mkdirSync(agentDir);
fs.mkdirSync(binDir);
fs.mkdirSync(project);
fs.copyFileSync(new URL("../../../lib/__fixtures__/claude-stub/claude.mjs", import.meta.url), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
fs.symlinkSync(decodeURIComponent(new URL("../../../lib/__fixtures__/claude-2.1.312", import.meta.url).pathname), path.join(scratch, "claude-2.1.312"));
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.CLAUDE_CONFIG_DIR = path.join(scratch, "claude-config");
const originalPath = process.env.PATH;
process.env.PATH = `${binDir}${path.delimiter}${originalPath}`;
process.env.PI_TEST_CLAUDE_STUB_LOG = path.join(scratch, "stub.jsonl");
process.env.PF_BIN = path.join(scratch, "no-pf");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { GET, PUT } = await jiti.import("./route.ts");
const index = await jiti.import("../../../lib/claude-index.ts");
const claude = await jiti.import("../../../lib/claude-session.ts");
const file = path.join(agentDir, "claude-permissions.json");

test.after(async () => {
  await claude.shutdownAllClaudeSessions();
  process.env.PATH = originalPath;
  fs.rmSync(scratch, { recursive: true, force: true });
});

async function newRecord() {
  const record = {
    id: randomUUID(),
    cwd: project,
    model: "claude-sonnet-5-5",
    created: new Date().toISOString(),
    modified: new Date().toISOString(),
    firstMessage: "",
    usage: { ...index.EMPTY_CLAUDE_USAGE },
  };
  await index.upsertClaudeSessionRecord(record);
  return record;
}

async function get(session) {
  const url = session === undefined ? "http://localhost/api/claude-permissions" : `http://localhost/api/claude-permissions?session=${encodeURIComponent(session)}`;
  const response = await GET(new NextRequest(url, { headers: { Host: "localhost" } }));
  assert.equal(response.status, 200);
  return response.json();
}

function put(body, headers = {}) {
  return PUT(new NextRequest("http://localhost/api/claude-permissions", {
    method: "PUT",
    headers: { Host: "localhost", Origin: "http://localhost", "Sec-Fetch-Site": "same-origin", "Content-Type": "application/json", ...headers },
    body: typeof body === "string" ? body : JSON.stringify(body),
  }));
}

async function waitFor(predicate, timeoutMs, message) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const value = await predicate();
    if (value) return value;
    await delay(25);
  }
  assert.fail(message);
}

test("GET reads the defaults and a claude session's override; PUT stores mode and push; a session override is 400 for a non-claude id or bad value and 409 while a turn runs", async () => {
  fs.rmSync(file, { force: true });
  const record = await newRecord();

  // Defaults: ask, push on; no session asked for, or an id that is no claude session, is null.
  assert.deepEqual(await get(), { mode: "ask", push: true, session: null });
  assert.deepEqual(await get(record.id), { mode: "ask", push: true, session: { override: null } });
  assert.deepEqual(await get(randomUUID()), { mode: "ask", push: true, session: null });

  // PUT stores either key, keeping the other.
  let response = await put({ mode: "deny" });
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { mode: "deny", push: true });
  response = await put({ push: false });
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { mode: "deny", push: false });
  assert.deepEqual(await get(), { mode: "deny", push: false, session: null });
  assert.deepEqual(JSON.parse(fs.readFileSync(file, "utf8")), { version: 1, mode: "deny", push: false });
  response = await put({ mode: "ask", push: true });
  assert.deepEqual(await response.json(), { mode: "ask", push: true });

  // Malformed bodies are refused before anything is written.
  const stored = fs.readFileSync(file, "utf8");
  for (const [body, status, headers] of [
    [{ mode: "always" }, 400],
    [{ push: "yes" }, 400],
    [{}, 400],
    [{ mode: "ask", extra: 1 }, 400],
    [["ask"], 400],
    ["{ nope", 400],
    [{ mode: "deny", session: record.id, override: "deny" }, 400],
    [{ mode: "deny" }, 415, { "Content-Type": "text/plain" }],
    [{ mode: "deny" }, 403, { Origin: "https://evil.example", "Sec-Fetch-Site": "cross-site" }],
  ]) {
    const refused = await put(body, headers);
    assert.equal(refused.status, status, JSON.stringify(body));
    assert.equal(typeof (await refused.json()).error, "string");
  }
  assert.equal(fs.readFileSync(file, "utf8"), stored, "a refused PUT wrote the settings file");

  // A session override: set, read back, cleared; the global settings are untouched.
  response = await put({ session: record.id, override: "deny" });
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { override: "deny", restarted: false });
  assert.equal(index.getClaudeSessionRecord(record.id).permissionMode, "deny");
  assert.deepEqual(await get(record.id), { mode: "ask", push: true, session: { override: "deny" } });
  response = await put({ session: record.id, override: null });
  assert.deepEqual(await response.json(), { override: null, restarted: false });
  assert.equal(index.getClaudeSessionRecord(record.id).permissionMode, undefined);
  assert.equal(fs.readFileSync(file, "utf8"), stored, "a session override wrote the global settings");

  // A non-claude id, a bad override value or a missing one: 400, nothing written.
  for (const body of [
    { session: randomUUID(), override: "deny" },
    { session: record.id, override: "always" },
    { session: record.id },
    { session: 7, override: "deny" },
  ]) {
    const refused = await put(body);
    assert.equal(refused.status, 400, JSON.stringify(body));
    assert.equal(typeof (await refused.json()).error, "string");
  }
  assert.equal(index.getClaudeSessionRecord(record.id).permissionMode, undefined);

  // An idle live child is ended so the next start carries the override.
  process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
  const idle = claude.startClaudeSession(record);
  response = await put({ session: record.id, override: "deny" });
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { override: "deny", restarted: true });
  await waitFor(() => !idle.isAlive(), 5_000, "the idle child was not ended");

  // A running turn refuses an override with 409 and changes nothing.
  process.env.PI_TEST_CLAUDE_STUB_MODE = "hang";
  const busy = claude.startClaudeSession(index.getClaudeSessionRecord(record.id));
  try {
    await busy.send({ type: "prompt", message: "long" });
    assert.equal(busy.isRunning(), true);
    const refused = await put({ session: record.id, override: "ask" });
    assert.equal(refused.status, 409);
    assert.equal(typeof (await refused.json()).error, "string");
    assert.equal(index.getClaudeSessionRecord(record.id).permissionMode, "deny");
    assert.equal(busy.isAlive(), true, "a refused override ended the child");
  } finally {
    await busy.shutdown();
  }
});
