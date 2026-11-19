// carries: lib/claude-mcp-write.ts, lib/__fixtures__/claude-2.1.326-mcp/mcp/*
// polyforge#144 (#85 85c, AC2, Q1 A / Q4 A): claude's MCP entries are written
// only through `claude mcp add-json|remove -s <scope>` — Pi Web never writes
// `~/.claude.json` itself; the argv is claude 2.1.326's own documented usage
// (`add-json [options] <name> <json>`, `-s, --scope <scope>`), the version is
// gated before any spawn, a modification is remove + add-json with the staged
// old definition rolled back, and `mcp list` / `mcp get` never run. The stub
// records every spawn; the assertions are hand-written from that usage.
import "#test-env";
import assert from "node:assert/strict";
import { chmodSync } from "node:fs";
import { realpathSync } from "node:fs";
import { mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { moduleCache: false });
const { claudeMcpAddJson, claudeMcpRemove, claudeMcpWriteEntry } = await jiti.import("./claude-mcp-write.ts");

const stubBinary = new URL("./__fixtures__/claude-2.1.326-mcp/mcp/stub-claude.mjs", import.meta.url).pathname;
chmodSync(stubBinary, 0o755);

async function stub(t) {
  const root = realpathSync(await mkdtemp(join(tmpdir(), "pi-web-claude-mcp-write-")));
  t.after(() => rm(root, { recursive: true, force: true }));
  const log = join(root, "spawns.jsonl");
  const cwd = join(root, "proj");
  await mkdir(cwd, { recursive: true });
  const env = {
    ...process.env,
    CLAUDE_MCP_STUB_LOG: log,
    CLAUDE_CONFIG_DIR: join(root, "cfg"),
  };
  return { root, log, cwd, env };
}

async function spawns(log) {
  const text = await readFile(log, "utf8").catch(() => "");
  return text.split("\n").filter((line) => line !== "").map((line) => JSON.parse(line));
}

const KNOWN = "2.1.326";

test("add-json spawns exactly claude's documented argv, always with -s, cwd and config dir as read", async (t) => {
  const { log, cwd, env } = await stub(t);
  const entry = { command: "npx", args: ["-y", "server"], env: { A: "1" } };
  const outcome = await claudeMcpAddJson("global", "demo", entry, { binary: stubBinary, version: KNOWN, cwd, env });
  assert.deepEqual(outcome, { ok: true });
  assert.deepEqual(await spawns(log), [{
    argv: ["mcp", "add-json", "-s", "user", "demo", JSON.stringify(entry)],
    cwd,
    claudeConfigDir: env.CLAUDE_CONFIG_DIR,
  }], "global is claude's user scope, and the JSON is one argv word");
});

test("the local and project scopes run in the project folder, local as claude's own", async (t) => {
  const { log, cwd, env } = await stub(t);
  assert.deepEqual(await claudeMcpRemove("local", "demo", { binary: stubBinary, version: KNOWN, cwd, env }), { ok: true });
  assert.deepEqual(await claudeMcpAddJson("project", "demo", { command: "x" }, { binary: stubBinary, version: KNOWN, cwd, env }), { ok: true });
  const recorded = await spawns(log);
  assert.deepEqual(recorded[0].argv, ["mcp", "remove", "-s", "local", "demo"]);
  assert.equal(recorded[0].cwd, cwd);
  assert.deepEqual(recorded[1].argv, ["mcp", "add-json", "-s", "project", "demo", "{\"command\":\"x\"}"]);
  assert.equal(recorded[1].cwd, cwd);
});

test("a local or project write without the project folder is refused, spawning nothing", async (t) => {
  const { log, env } = await stub(t);
  for (const scope of ["local", "project"]) {
    const outcome = await claudeMcpAddJson(scope, "demo", { command: "x" }, { binary: stubBinary, version: KNOWN, env });
    assert.equal(outcome.ok, false);
    assert.equal(outcome.reason, "invalid-request");
  }
  assert.deepEqual(await spawns(log), []);
});

test("an unrecognized version refuses before any spawn; a missing binary too", async (t) => {
  const { log, cwd, env } = await stub(t);
  const refused = await claudeMcpAddJson("global", "demo", { command: "x" }, { binary: stubBinary, version: "9.9.9", cwd, env });
  assert.equal(refused.ok, false);
  assert.equal(refused.reason, "version-unrecognized");
  const missing = await claudeMcpAddJson("global", "demo", { command: "x" }, { binary: "/nonexistent/claude", version: "unknown", cwd, env });
  assert.equal(missing.ok, false);
  assert.equal(missing.reason, "claude-missing");
  const unprobed = await claudeMcpRemove("global", "demo", { binary: "/nonexistent/claude", cwd, env });
  assert.equal(unprobed.ok, false);
  assert.equal(unprobed.reason, "claude-missing", "no binary, so no version could be probed");
  assert.deepEqual(await spawns(log), [], "no version it does not recognize ever spawns");
});

test("a failed command is reported by its exit code, never by the child's output", async (t) => {
  const { log, cwd, env } = await stub(t);
  const failing = { ...env, CLAUDE_MCP_STUB_FAIL: "remove" };
  const outcome = await claudeMcpRemove("global", "demo", { binary: stubBinary, version: KNOWN, cwd, env: failing });
  assert.equal(outcome.ok, false);
  assert.equal(outcome.reason, "remove-failed");
  assert.match(outcome.error, /exit 1/);
  assert.equal(/demo/.test(outcome.error), false, "the error never quotes the argv or the output");
});

test("a modification is remove then add-json; a failed add-json rolls the staged definition back", async (t) => {
  const { log, cwd, env } = await stub(t);
  const next = { command: "npx", args: ["-y", "v2"] };
  const previous = { command: "npx", args: ["-y", "v1"] };
  const ok = await claudeMcpWriteEntry("global", "demo", next, { binary: stubBinary, version: KNOWN, cwd, env, previous });
  assert.deepEqual(ok, { ok: true });
  assert.deepEqual((await spawns(log)).map((spawn) => spawn.argv), [
    ["mcp", "remove", "-s", "user", "demo"],
    ["mcp", "add-json", "-s", "user", "demo", JSON.stringify(next)],
  ]);

  const rollbackRoot = await mkdtemp(join(tmpdir(), "pi-web-claude-mcp-write-rollback-"));
  t.after(() => rm(rollbackRoot, { recursive: true, force: true }));
  const rollbackLog = join(rollbackRoot, "spawns.jsonl");
  const failing = {
    ...env,
    CLAUDE_MCP_STUB_LOG: rollbackLog,
    CLAUDE_MCP_STUB_STATE: join(rollbackRoot, "state"),
    CLAUDE_MCP_STUB_FAIL: "add-first",
  };
  const failed = await claudeMcpWriteEntry("global", "demo", next, {
    binary: stubBinary,
    version: KNOWN,
    cwd,
    env: failing,
    previous,
  });
  assert.equal(failed.ok, false);
  assert.equal(failed.reason, "add-failed");
  assert.equal(failed.rolledBack, true);
  assert.deepEqual((await spawns(rollbackLog)).map((spawn) => spawn.argv), [
    ["mcp", "remove", "-s", "user", "demo"],
    ["mcp", "add-json", "-s", "user", "demo", JSON.stringify(next)],
    ["mcp", "add-json", "-s", "user", "demo", JSON.stringify(previous)],
  ], "the rollback writes the staged old definition back");
});

test("a modification whose rollback also fails says so, and never reports success", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "pi-web-claude-mcp-write-both-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const log = join(root, "spawns.jsonl");
  const cwd = join(root, "proj");
  await mkdir(cwd, { recursive: true });
  const failing = {
    ...process.env,
    CLAUDE_MCP_STUB_LOG: log,
    CLAUDE_CONFIG_DIR: join(root, "cfg"),
    CLAUDE_MCP_STUB_FAIL: "add",
  };
  const failed = await claudeMcpWriteEntry("global", "demo", { command: "x" }, {
    binary: stubBinary,
    version: KNOWN,
    cwd,
    env: failing,
  });
  assert.equal(failed.ok, false);
  assert.equal(failed.reason, "add-failed");
  assert.equal(failed.rolledBack, undefined, "no previous definition was staged, so nothing was written back");
});
