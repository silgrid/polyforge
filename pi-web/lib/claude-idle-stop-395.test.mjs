// carries: lib/claude-session.ts, lib/claude-adapter.ts, lib/__fixtures__/claude-stub/*, lib/__fixtures__/claude-2.1.319/*
// AC3 with fixture ③ itself (idle-interrupt/): the turn ended, a background
// agent still running, Stop writes nothing to the idle claude — whose own answer to an
// interrupt then would stop that agent, as recorded — and signals nothing.
import "#test-env";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { createJiti } from "jiti";
import { readStubLog } from "./__fixtures__/claude-stub-log.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
// A scratch agent dir, config dir and HOME, and a PATH whose `claude` is the unit stub.
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-idle-stop-395-"));
const agentDir = path.join(scratch, "agent");
const configDir = path.join(scratch, "claude-config");
const homeDir = path.join(scratch, "home");
const binDir = path.join(scratch, "bin");
const workDir = path.join(scratch, "work");
const stubLog = path.join(scratch, "stub.jsonl");
for (const dir of [agentDir, binDir, workDir, homeDir]) fs.mkdirSync(dir);
fs.copyFileSync(path.join(here, "__fixtures__", "claude-stub", "claude.mjs"), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
// The stub reads the recordings next to its own directory.
for (const name of ["claude-2.1.312", "claude-2.1.319"]) fs.symlinkSync(path.join(here, "__fixtures__", name), path.join(scratch, name));
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.HOME = homeDir;
process.env.CLAUDE_CONFIG_DIR = configDir;
process.env.PATH = `${binDir}${path.delimiter}${process.env.PATH}`;
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";

const jiti = createJiti(import.meta.url, { alias: { "@": path.dirname(here) }, interopDefault: true });
const claude = await jiti.import("./claude-session.ts");
const index = await jiti.import("./claude-index.ts");

test.after(async () => {
  await claude.shutdownAllClaudeSessions();
  for (const entry of records().filter((record) => record.kind === "grandchild")) {
    try { process.kill(entry.grandchildPid, "SIGKILL"); } catch { /* gone */ }
  }
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
const stdinOf = (pid) => records().filter((entry) => entry.kind === "stdin" && entry.pid === pid).map((entry) => JSON.parse(entry.line));
const alive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
};

async function newRecord() {
  const record = {
    id: randomUUID(),
    cwd: workDir,
    model: "claude-haiku-5-5",
    created: new Date().toISOString(),
    modified: new Date().toISOString(),
    firstMessage: "",
    usage: { ...index.EMPTY_CLAUDE_USAGE },
  };
  await index.upsertClaudeSessionRecord(record);
  return record;
}
function collect(session) {
  const events = [];
  session.onEvent((event) => events.push(event));
  return events;
}
const count = (events, type) => events.filter((event) => event.type === type).length;

test("AC3 ③: after the turn ends with a background agent running, Stop leaves claude, its process group and the agent alone", async () => {
  process.env.PI_TEST_CLAUDE_STUB_MODE = "idle-interrupt";
  process.env.PI_TEST_CLAUDE_STUB_GRANDCHILD = "1";
  try {
    const session = claude.startClaudeSession(await newRecord());
    const events = collect(session);
    const pid = session.pid;
    const grandchild = await waitFor(() => records().find((entry) => entry.kind === "grandchild" && entry.pid === pid), 5_000, "the stub started no background process");
    await session.send({ type: "prompt", message: "launch the agent" });
    await waitFor(() => count(events, "agent_settled") === 1, 5_000, "the turn did not end at its result");
    assert.equal(session.hasUnsettledSubagents(), true, "the background agent is not running in the recording");
    await session.send({ type: "abort" });
    await delay(claude.CLAUDE_INTERRUPT_DEADLINE_MS + 1_500);
    assert.deepEqual(stdinOf(pid).map((line) => line.type), ["user"], "Stop reached the idle claude");
    assert.ok(!records().some((entry) => entry.kind === "signal" && entry.pid === pid), "the process group was signalled");
    assert.equal(alive(pid), true, "the claude process exited");
    assert.equal(alive(grandchild.grandchildPid), true, "the background process was killed");
    assert.equal(session.hasUnsettledSubagents(), true, "the background agent was reported stopped");
    assert.ok(!events.some((event) => event.type === "claude_subagent" && event.run?.status !== "running"), "a run was settled by Stop");
    await session.shutdown();
  } finally {
    delete process.env.PI_TEST_CLAUDE_STUB_GRANDCHILD;
  }
});
