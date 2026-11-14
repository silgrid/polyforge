import "#test-env";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { createJiti } from "jiti";
import { readStubLog } from "./__fixtures__/claude-stub-log.mjs";

// The in-session permission-mode switch: claude's own `set_permission_mode`
// control request on a live child (lib/__fixtures__/claude-2.1.322/permission-mode/), the
// index alone without one, plan offered only to a session whose permission prompts come to
// Pi Web (ask mode; on deny claude would refuse plan's ExitPlanMode
// r3, owner ruling 2026-11-13T07:40:00Z). A scratch agent dir, claude config dir, HOME
// and a PATH whose `claude` is the stub: nothing here touches the operator's ~/.claude.
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-permission-"));
const agentDir = path.join(scratch, "agent");
const binDir = path.join(scratch, "bin");
const workDir = path.join(scratch, "work");
const homeDir = path.join(scratch, "home");
const stubLog = path.join(scratch, "stub.jsonl");
for (const dir of [agentDir, binDir, workDir, homeDir]) fs.mkdirSync(dir);
fs.copyFileSync(new URL("./__fixtures__/claude-stub/claude.mjs", import.meta.url), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
fs.symlinkSync(decodeURIComponent(new URL("./__fixtures__/claude-2.1.312", import.meta.url).pathname), path.join(scratch, "claude-2.1.312"));
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.HOME = homeDir;
process.env.CLAUDE_CONFIG_DIR = path.join(scratch, "claude-config");
process.env.PATH = `${binDir}${path.delimiter}${process.env.PATH}`;
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";
process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const claude = await jiti.import("./claude-session.ts");
const index = await jiti.import("./claude-index.ts");
const permissionConfig = path.join(agentDir, "claude-permissions.json");
/** the global permission mode for the duration of `fn` (absent file: its default, ask). */
async function withGlobalMode(mode, fn) {
  fs.writeFileSync(permissionConfig, JSON.stringify({ version: 1, mode, push: false }));
  try {
    return await fn();
  } finally {
    fs.rmSync(permissionConfig, { force: true });
  }
}

test.after(async () => {
  await claude.shutdownAllClaudeSessions();
  fs.rmSync(scratch, { recursive: true, force: true });
});

async function waitFor(predicate, timeoutMs, message) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const value = predicate();
    if (value) return value;
    await delay(25);
  }
  assert.fail(message);
}

const records = () => readStubLog(stubLog);
const launches = () => records().filter((entry) => entry.kind === "launch");
const controlsOf = (pid) => records()
  .filter((entry) => entry.kind === "stdin" && entry.pid === pid)
  .map((entry) => JSON.parse(entry.line))
  .filter((line) => line.type === "control_request");
/** What a command settled to: its value, or `{ thrown }` with the error's text. */
const outcome = (promise) => promise.then((value) => value, (error) => ({ thrown: error instanceof Error ? error.message : String(error) }));

async function newRecord(extra = {}) {
  const record = {
    id: randomUUID(),
    cwd: workDir,
    model: "claude-sonnet-5-5",
    created: new Date().toISOString(),
    modified: new Date().toISOString(),
    firstMessage: "",
    usage: { ...index.EMPTY_CLAUDE_USAGE },
    ...extra,
  };
  await index.upsertClaudeSessionRecord(record);
  return record;
}

async function started(record) {
  const session = claude.startClaudeSession(index.getClaudeSessionRecord(record.id));
  return { session, launch: await waitFor(() => launches().find((entry) => entry.pid === session.pid), 5_000, "no launch") };
}

test("a live child gets claude's set_permission_mode control request and the accepted mode is recorded", async () => {
  const record = await newRecord();
  const { session, launch } = await started(record);
  assert.ok(!launch.argv.includes("--permission-mode"), "a default session starts without the flag");
  const before = launches().length;
  assert.deepEqual(await outcome(session.send({ type: "set_permission_mode", mode: "acceptEdits" })), { launchPermissionMode: "acceptEdits" });
  assert.deepEqual(controlsOf(session.pid).map((line) => line.request), [{ subtype: "set_permission_mode", mode: "acceptEdits" }]);
  assert.equal(launches().length, before, "the same child, no new launch");
  assert.ok(session.isAlive());
  assert.equal(index.getClaudeSessionRecord(record.id).launchPermissionMode, "acceptEdits");
  assert.equal((await session.send({ type: "get_state" })).claudeLaunchPermissionMode, "acceptEdits");
  assert.equal(claude.claudeSessionInfo(index.getClaudeSessionRecord(record.id)).claude.launchPermissionMode, "acceptEdits");
  await session.shutdown();

  // The next child of the session starts with the recorded mode; switching back to default drops the flag.
  const resumed = await started(record);
  assert.equal(resumed.launch.argv[resumed.launch.argv.indexOf("--permission-mode") + 1], "acceptEdits");
  assert.deepEqual(await outcome(resumed.session.send({ type: "set_permission_mode", mode: "default" })), { launchPermissionMode: "default" });
  assert.equal(index.getClaudeSessionRecord(record.id).launchPermissionMode, "default");
  await resumed.session.shutdown();
  const plain = await started(record);
  assert.ok(!plain.launch.argv.includes("--permission-mode"));
  await plain.session.shutdown();
});

test("claude's invalid_mode refusal keeps the session's mode and returns claude's text", async (t) => {
  // claude 2.1.322's own refusal, as recorded (the stub answers with it).
  const recorded = fs.readFileSync(new URL("./__fixtures__/claude-2.1.322/permission-mode/stream.jsonl", import.meta.url), "utf8")
    .split("\n").filter(Boolean).map((line) => JSON.parse(line))
    .find((event) => event.type === "control_response" && event.response.subtype === "error").response;
  process.env.PI_TEST_CLAUDE_STUB_REFUSE_PERMISSION_MODE = "1";
  t.after(() => delete process.env.PI_TEST_CLAUDE_STUB_REFUSE_PERMISSION_MODE);
  const record = await newRecord({ launchPermissionMode: "acceptEdits" });
  const { session } = await started(record);
  assert.deepEqual(await outcome(session.send({ type: "set_permission_mode", mode: "default" })), {
    launchPermissionMode: "acceptEdits",
    rejected: true,
    error: recorded.error,
    errorCode: recorded.error_code,
  });
  assert.equal(index.getClaudeSessionRecord(record.id).launchPermissionMode, "acceptEdits");
  assert.equal((await session.send({ type: "get_state" })).claudeLaunchPermissionMode, "acceptEdits");
  assert.ok(session.isAlive(), "a refusal keeps the child");
  await session.shutdown();
});

test("with no live child the switch only writes the index and the next child starts with it", async () => {
  const record = await newRecord();
  const before = launches().length;
  assert.deepEqual(await outcome(claude.setIdleClaudeSetting(record.id, { type: "set_permission_mode", mode: "acceptEdits" })), { launchPermissionMode: "acceptEdits", pending: true });
  assert.equal(launches().length, before, "nothing is spawned");
  assert.equal(index.getClaudeSessionRecord(record.id).launchPermissionMode, "acceptEdits");

  // A child that has exited takes the switch the same way.
  const { session } = await started(record);
  assert.equal(session.argv[session.argv.indexOf("--permission-mode") + 1], "acceptEdits");
  await session.shutdown();
  const after = launches().length;
  assert.deepEqual(await outcome(session.send({ type: "set_permission_mode", mode: "default" })), { launchPermissionMode: "default", pending: true });
  assert.equal(launches().length, after, "nothing is spawned");
  assert.equal(index.getClaudeSessionRecord(record.id).launchPermissionMode, "default");
  const next = await started(record);
  assert.ok(!next.launch.argv.includes("--permission-mode"));
  await next.session.shutdown();
});

test("plan is refused while Pi Web has no host approvals and nothing reaches the child", async () => {
  // r3: "no host approvals" is deny, the global mode here.
  await withGlobalMode("deny", async () => {
    const record = await newRecord({ launchPermissionMode: "acceptEdits" });
    const { session } = await started(record);
    assert.equal(session.permissionMode, "deny");
    for (const mode of ["plan", "bypassPermissions", "bogus", undefined]) {
      const result = await outcome(session.send({ type: "set_permission_mode", mode }));
      assert.equal(typeof result.thrown, "string", String(mode));
      assert.match(result.thrown, mode === "plan" ? /Ask in a dialog/ : /Not a claude permission mode/, String(mode));
    }
    assert.match((await outcome(claude.setIdleClaudeSetting(record.id, { type: "set_permission_mode", mode: "plan" }))).thrown ?? "", /Ask in a dialog/);
    assert.deepEqual(controlsOf(session.pid), [], "no control request reached claude");
    assert.equal(index.getClaudeSessionRecord(record.id).launchPermissionMode, "acceptEdits");
    assert.ok(session.isAlive());
    await session.shutdown();
  });
});

test("plan reaches a child launched with Pi Web's permission prompts (ask) and is recorded; it is refused, reaching nothing, for a child launched with deny and, with no live child, when the session would launch with deny ( r3)", async () => {
  // A child launched under the global default (ask): plan goes to claude and is recorded.
  const asked = await newRecord();
  const { session: askChild } = await started(asked);
  assert.equal(askChild.permissionMode, "ask");
  assert.equal((await askChild.send({ type: "get_state" })).claudePlanAvailable, true);
  assert.notEqual(claude.claudeSessionInfo(index.getClaudeSessionRecord(asked.id)).claude.planAvailable, false);
  assert.deepEqual(await outcome(askChild.send({ type: "set_permission_mode", mode: "plan" })), { launchPermissionMode: "plan" });
  assert.deepEqual(controlsOf(askChild.pid).map((line) => line.request), [{ subtype: "set_permission_mode", mode: "plan" }]);
  assert.equal(index.getClaudeSessionRecord(asked.id).launchPermissionMode, "plan");
  await askChild.shutdown();

  // A child whose record overrides to deny: refused with the reason, nothing reaches claude.
  const denied = await newRecord({ launchPermissionMode: "acceptEdits", permissionMode: "deny" });
  const { session: denyChild } = await started(denied);
  assert.equal(denyChild.permissionMode, "deny");
  assert.equal((await denyChild.send({ type: "get_state" })).claudePlanAvailable, false);
  assert.equal(claude.claudeSessionInfo(index.getClaudeSessionRecord(denied.id)).claude.planAvailable, false);
  const refused = await outcome(denyChild.send({ type: "set_permission_mode", mode: "plan" }));
  assert.match(refused.thrown ?? "", /Ask in a dialog/);
  assert.deepEqual(controlsOf(denyChild.pid), [], "plan reached a child launched with deny");
  assert.equal(index.getClaudeSessionRecord(denied.id).launchPermissionMode, "acceptEdits");
  await denyChild.shutdown();

  // No live child: the mode the next launch would use decides (the record's override, else the global mode).
  await withGlobalMode("deny", async () => {
    const before = launches().length;
    const plain = await newRecord({ launchPermissionMode: "acceptEdits" });
    assert.match((await outcome(claude.setIdleClaudeSetting(plain.id, { type: "set_permission_mode", mode: "plan" }))).thrown ?? "", /Ask in a dialog/);
    assert.equal(index.getClaudeSessionRecord(plain.id).launchPermissionMode, "acceptEdits");
    const overridden = await newRecord({ permissionMode: "ask" });
    assert.deepEqual(await outcome(claude.setIdleClaudeSetting(overridden.id, { type: "set_permission_mode", mode: "plan" })), { launchPermissionMode: "plan", pending: true });
    assert.equal(index.getClaudeSessionRecord(overridden.id).launchPermissionMode, "plan");
    assert.equal(launches().length, before, "nothing is spawned");
  });
});
