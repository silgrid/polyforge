// carries: lib/claude-shared.ts, lib/claude-probe.ts, lib/claude-adapter.ts, lib/claude-session.ts
// carries: lib/__fixtures__/claude-2.1.322/initialize/*, lib/__fixtures__/claude-2.1.312/compact/*
// polyforge#71 (AC3): claude's own commands reach the `/` menu — the startup
// probe reads the `commands` of claude's initialize answer, the init frames' own
// `terminal_slash_commands` are subtracted at runtime (never hardcoded), then the
// exclusion table (each entry with its reason) and `compact` (pi's built-in /compact
// owns the guarded path, AC2). What survives is offered beside pi's built-ins and
// sent to claude verbatim as `/<name> <参数>`. Expected values come from the
// recordings themselves: the 2.1.322 initialize answer (48 commands) and the 2.1.312
// init frames' terminal list — 48 − 4 terminal − 16 table − 1 compact = 27.
import "#test-env";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { createJiti } from "jiti";

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-commands-"));
const agentDir = path.join(scratch, "agent");
const configDir = path.join(scratch, "claude-config");
const homeDir = path.join(scratch, "home");
const binDir = path.join(scratch, "bin");
const workDir = path.join(scratch, "work");
const stubLog = path.join(scratch, "stub.jsonl");
for (const dir of [agentDir, binDir, workDir, homeDir]) fs.mkdirSync(dir);
// The stub is copied (0755) as `claude` onto PATH; it reads the recordings from
// its own directory's parent, so both fixture trees are symlinked into the scratch.
fs.copyFileSync(new URL("./__fixtures__/claude-stub/claude.mjs", import.meta.url), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
for (const name of ["claude-2.1.312", "claude-2.1.322"]) {
  fs.symlinkSync(decodeURIComponent(new URL(`./__fixtures__/${name}`, import.meta.url).pathname), path.join(scratch, name));
}
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.HOME = homeDir;
process.env.CLAUDE_CONFIG_DIR = configDir;
process.env.PATH = `${binDir}${path.delimiter}${process.env.PATH}`;
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_WEB_PASSWORD = "not-for-the-child";
// Each stdin line replays the next segment of the compact/ recording, whose init
// frames carry the terminal-only list.
process.env.PI_TEST_CLAUDE_STUB_MODE = "compact";
process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const probe = await jiti.import("./claude-probe.ts");
const shared = await jiti.import("./claude-shared.ts");
const adapter = await jiti.import("./claude-adapter.ts");
const claude = await jiti.import("./claude-session.ts");
const index = await jiti.import("./claude-index.ts");

test.after(async () => {
  await claude.shutdownAllClaudeSessions();
  fs.rmSync(scratch, { recursive: true, force: true });
});

/** claude 2.1.322's recorded initialize answer (lib/__fixtures__/claude-2.1.322/initialize/). */
const recordedInitializeAnswer = () => {
  const frames = fs.readFileSync(new URL("./__fixtures__/claude-2.1.322/initialize/stream.jsonl", import.meta.url), "utf8")
    .split("\n").filter(Boolean).map((line) => JSON.parse(line));
  return frames.find((frame) => frame.type === "control_response").response.response;
};

/** The terminal-only names the recorded 2.1.312 init frames report. */
const recordedTerminalSlashCommands = () => {
  const events = fs.readFileSync(new URL("./__fixtures__/claude-2.1.312/compact/stream.jsonl", import.meta.url), "utf8")
    .split("\n").filter(Boolean).map((line) => JSON.parse(line));
  return events.find((event) => event.type === "system" && event.subtype === "init").terminal_slash_commands;
};

test("the probe reads the initialize answer's commands: all 48, names, descriptions and argument hints", () => {
  const info = probe.claudeInitializeInfo(recordedInitializeAnswer());
  assert.equal(info.commands?.length ?? 0, 48, "claude 2.1.322's initialize answer lists 48 commands");
  const deep = info.commands.find((command) => command.name === "deep-research");
  // An empty argument hint carries nothing and is dropped.
  assert.deepEqual(deep, { name: "deep-research", description: deep.description, builtin: true });
  assert.match(deep.description, /fan-out web searches/i);
  // The ticket's exclusion-table names are all among them, as recorded.
  for (const name of ["clear", "rename", "model", "effort", "fast", "mcp", "config", "output-style", "autocompact", "usage", "context", "list-agents", "agents", "__remote-workflow", "workflow-launch-exec", "compact"]) {
    assert.ok(info.commands.some((command) => command.name === name), `the recording lists /${name}`);
  }
  // Tolerant: a malformed commands field reads as none, never a throw.
  for (const value of [undefined, null, "x", {}, { commands: "x" }, { commands: [{ name: 3 }, null, {}] }]) {
    assert.deepEqual(probe.claudeInitializeInfo(value).commands, [], JSON.stringify(value));
  }
});

test("claudeSlashCommands subtracts the init frames' terminal names and the exclusion table: 48 − 4 − 15 − 1 = 28", () => {
  const commands = probe.claudeInitializeInfo(recordedInitializeAnswer()).commands ?? [];
  const terminal = recordedTerminalSlashCommands();
  assert.deepEqual(terminal, ["doctor", "color", "focus", "reload-plugins"], "the recorded init frames' own list");
  const offered = shared.claudeSlashCommands(commands, terminal);
  // Derived by hand from the recording's order, minus the terminal four, the
  // sixteen table entries and compact (pi's built-in owns it, AC2).
  assert.deepEqual(offered.map((command) => command.name), [
    "deep-research", "design", "design-sync", "dataviz", "update-config", "verify", "debug", "code-review", "simplify",
    "batch", "fewer-permission-prompts", "loop", "claude-api", "workflow-authoring", "run", "run-skill-generator",
    "plugin-authoring", "auto-mode-setup", "heapdump", "init", "reload-skills", "security-review", "insights", "recap",
    "goal", "design-consent", "design-revoke", "team-onboarding",
  ]);
  assert.equal(offered.length, 28);
  // No terminal-only name, no table entry, survives.
  for (const name of [...terminal, "clear", "rename", "model", "effort", "fast", "mcp", "config", "output-style", "autocompact", "usage", "context", "list-agents", "agents", "__remote-workflow", "workflow-launch-exec", "compact"]) {
    assert.ok(!offered.some((command) => command.name === name), `/${name} must not be listed`);
  }
  // Before any init frame arrived, only the table and compact are subtracted.
  const beforeInit = shared.claudeSlashCommands(commands, []);
  assert.deepEqual(beforeInit.map((command) => command.name).filter((name) => terminal.includes(name)), [...terminal]);
  // An empty answer offers nothing.
  assert.deepEqual(shared.claudeSlashCommands([], terminal), []);
});

test("the exclusion table carries the ticket's names, each with its reason", () => {
  for (const name of ["clear", "rename", "model", "effort", "fast", "mcp", "config", "output-style", "autocompact", "usage", "context", "list-agents", "agents", "__remote-workflow", "workflow-launch-exec"]) {
    const reason = shared.CLAUDE_COMMAND_EXCLUSIONS.get(name);
    assert.ok(typeof reason === "string" && reason.length > 5, `/${name} needs its written reason`);
  }
  assert.equal(shared.CLAUDE_COMMAND_EXCLUSIONS.size, 15, "exactly the ticket's fifteen, and nothing more");
});

test("the translator reads every init frame's terminal_slash_commands", () => {
  const recorded = fs.readFileSync(new URL("./__fixtures__/claude-2.1.312/compact/stream.jsonl", import.meta.url), "utf8")
    .split("\n").filter(Boolean).map((line) => JSON.parse(line));
  const translator = new adapter.ClaudeStreamTranslator(() => 1000);
  assert.equal(translator.terminalSlashCommands, null, "before any frame: nothing claimed");
  for (const event of recorded) translator.push(event);
  assert.deepEqual(translator.terminalSlashCommands, ["doctor", "color", "focus", "reload-plugins"]);
  // A frame without the field keeps the last known list; a malformed one is ignored.
  translator.push({ type: "system", subtype: "init", session_id: "x" });
  assert.deepEqual(translator.terminalSlashCommands, ["doctor", "color", "focus", "reload-plugins"]);
  translator.push({ type: "system", subtype: "init", terminal_slash_commands: "x" });
  assert.deepEqual(translator.terminalSlashCommands, ["doctor", "color", "focus", "reload-plugins"]);
});

test("a live session's get_commands answers the filtered list as extension commands", async () => {
  const record = {
    id: randomUUID(),
    cwd: workDir,
    model: "claude-haiku-5-5",
    created: new Date().toISOString(),
    modified: new Date().toISOString(),
  };
  index.upsertClaudeSessionRecord(record);
  const session = claude.startClaudeSession(record);
  try {
    // One turn: the stub replays the compact recording's first segment, whose
    // init frame carries the terminal-only list, so the runtime exclusion
    // applies. `prompt` resolves before the turn streams: wait for its end
    // (prompt_done) like the window test does, never a fixed delay.
    const events = [];
    session.onEvent((event) => events.push(event));
    await session.send({ type: "prompt", message: "first" });
    const deadline = Date.now() + 30_000;
    while (!events.some((event) => event.type === "prompt_done")) {
      if (Date.now() > deadline) assert.fail("the replayed turn never ended (no prompt_done)");
      await delay(25);
    }
    const answer = await session.send({ type: "get_commands" });
    const names = answer.commands.map((command) => command.name);
    assert.deepEqual(names, [
      "deep-research", "design", "design-sync", "dataviz", "update-config", "verify", "debug", "code-review", "simplify",
      "batch", "fewer-permission-prompts", "loop", "claude-api", "workflow-authoring", "run", "run-skill-generator",
      "plugin-authoring", "auto-mode-setup", "heapdump", "init", "reload-skills", "security-review", "insights", "recap",
      "goal", "design-consent", "design-revoke", "team-onboarding",
    ]);
    assert.ok(answer.commands.every((command) => command.source === "extension"), "listed like pi's extension commands");
    const deep = answer.commands.find((command) => command.name === "deep-research");
    assert.match(deep.description, /fan-out web searches/i);
  } finally {
    await claude.shutdownAllClaudeSessions();
  }
});
