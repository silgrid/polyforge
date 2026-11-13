import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const configModule = await jiti.import("./claude-config-dir.ts");
const { claudeConfigDir, claudeSidecarFiles, claudeTranscriptFile, removeClaudeTranscript } = configModule;

function scratch(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-config-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function withEnv(t, values) {
  const previous = Object.fromEntries(Object.keys(values).map((name) => [name, process.env[name]]));
  for (const [name, value] of Object.entries(values)) {
    if (value === undefined) delete process.env[name];
    else process.env[name] = value;
  }
  t.after(() => {
    for (const [name, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[name];
      else process.env[name] = value;
    }
  });
}

test("the config dir is the inherited CLAUDE_CONFIG_DIR (trimmed) when pi-web has one", (t) => {
  const dir = scratch(t);
  assert.equal(claudeConfigDir({ CLAUDE_CONFIG_DIR: dir }), dir);
  assert.equal(claudeConfigDir({ CLAUDE_CONFIG_DIR: `  ${dir}  ` }), dir);
  withEnv(t, { CLAUDE_CONFIG_DIR: dir });
  assert.equal(claudeConfigDir(), dir);
});

test("without CLAUDE_CONFIG_DIR it is ~/.claude of pi-web's HOME, never the pi agent dir", (t) => {
  const home = scratch(t);
  withEnv(t, { HOME: home, CLAUDE_CONFIG_DIR: undefined, PI_CODING_AGENT_DIR: path.join(home, ".pi", "agent") });
  assert.equal(claudeConfigDir(), path.join(home, ".claude"));
  assert.equal(claudeConfigDir({ CLAUDE_CONFIG_DIR: "  " }), path.join(home, ".claude"));
  assert.equal(fs.existsSync(path.join(home, ".claude")), false, "resolving the dir creates nothing");
});

test("the module no longer creates a dedicated dir or a settings.json", () => {
  assert.equal(configModule.ensureClaudeConfigDir, undefined);
  assert.equal(configModule.claudeSettingsPath, undefined);
  assert.equal(configModule.DEFAULT_CLAUDE_SETTINGS, undefined);
});

test("a transcript under the retired <pi agent dir>/claude is never used", (t) => {
  const home = scratch(t);
  const agentDir = path.join(home, ".pi", "agent");
  withEnv(t, { HOME: home, CLAUDE_CONFIG_DIR: undefined, PI_CODING_AGENT_DIR: agentDir });
  const id = "11111111-2222-3333-4444-555555555555";
  const old = path.join(agentDir, "claude", "projects", "-x", `${id}.jsonl`);
  fs.mkdirSync(path.dirname(old), { recursive: true });
  fs.writeFileSync(old, "{}\n");
  assert.equal(claudeTranscriptFile(id), null);
  assert.equal(removeClaudeTranscript(id), false);
  assert.ok(fs.existsSync(old), "the old file is left as it is");
  const current = path.join(home, ".claude", "projects", "-x", `${id}.jsonl`);
  fs.mkdirSync(path.dirname(current), { recursive: true });
  fs.writeFileSync(current, "{}\n");
  assert.equal(claudeTranscriptFile(id), current);
});

test("a transcript is found by id under any projects subdirectory and removed alone", (t) => {
  const dir = scratch(t);
  const projects = path.join(dir, "projects");
  fs.mkdirSync(path.join(projects, "-tmp-a"), { recursive: true });
  fs.mkdirSync(path.join(projects, "-tmp-b"), { recursive: true });
  const mine = path.join(projects, "-tmp-b", "11111111-2222-3333-4444-555555555555.jsonl");
  const other = path.join(projects, "-tmp-a", "99999999-2222-3333-4444-555555555555.jsonl");
  fs.writeFileSync(mine, "{}\n");
  fs.writeFileSync(other, "{}\n");
  assert.equal(claudeTranscriptFile("11111111-2222-3333-4444-555555555555", dir), mine);
  assert.equal(claudeTranscriptFile("00000000-0000-0000-0000-000000000000", dir), null);
  assert.equal(claudeTranscriptFile("../-tmp-a/x", dir), null);
  assert.equal(removeClaudeTranscript("11111111-2222-3333-4444-555555555555", dir), true);
  assert.ok(!fs.existsSync(mine));
  assert.ok(fs.existsSync(other));
  assert.equal(removeClaudeTranscript("11111111-2222-3333-4444-555555555555", dir), false);
});

test("no projects dir reads as no transcript", (t) => {
  assert.equal(claudeTranscriptFile("abc", scratch(t)), null);
});

test("a session's subagent sidecars are found beside its transcript, and none without one", (t) => {
  const dir = scratch(t);
  const id = "11111111-2222-3333-4444-555555555555";
  const project = path.join(dir, "projects", "-tmp-b");
  const sidecars = path.join(project, id, "subagents");
  fs.mkdirSync(sidecars, { recursive: true });
  fs.writeFileSync(path.join(project, `${id}.jsonl`), "{}\n");
  for (const name of ["agent-b2.jsonl", "agent-b2.meta.json", "agent-a1.jsonl", "notes.txt", "agent-../x.jsonl".replace("/", "_")]) {
    fs.writeFileSync(path.join(sidecars, name), "{}\n");
  }
  assert.deepEqual(claudeSidecarFiles(id, dir).map((entry) => [entry.agentId, path.basename(entry.file), path.basename(entry.metaFile)]), [
    ["a1", "agent-a1.jsonl", "agent-a1.meta.json"],
    ["b2", "agent-b2.jsonl", "agent-b2.meta.json"],
  ]);
  assert.deepEqual(claudeSidecarFiles("00000000-0000-0000-0000-000000000000", dir), [], "no transcript, no sidecars");
  fs.rmSync(sidecars, { recursive: true });
  assert.deepEqual(claudeSidecarFiles(id, dir), [], "no subagents dir reads as none");
});
