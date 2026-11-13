import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const shared = await jiti.import("./claude-shared.ts");
const fixturesDir = new URL("./__fixtures__/", import.meta.url);

// replaced the four full names pinned to claude 2.1.312 (AC4): the models come
// from claude's own initialize answer, and a session's model is any name `--model` takes.
test("a claude model is an alias or a full name: a valid name stays and anything else reads as default; there is no fixed list", () => {
  for (const name of ["CLAUDE_MODELS", "isClaudeModel", "LEGACY_CLAUDE_MODELS"]) assert.equal(shared[name], undefined, name);
  assert.equal(shared.DEFAULT_CLAUDE_MODEL, "default");
  for (const name of ["default", "opus", "sonnet", "haiku", "fable", "opus[1m]", "claude-opus-5-5", "claude-fable-5-1", "claude-custom-e2e-1", "us.anthropic.claude-sonnet-5-5-v1:0", "claude-sonnet@20260101", "x".repeat(128)]) {
    assert.ok(shared.isClaudeModelName(name), name);
    assert.equal(shared.normalizeClaudeModel(name), name, name);
  }
  for (const value of ["", "-bad", "--model", "has space", "x".repeat(129), "a\nb", "toString()", undefined, null, 1, {}]) {
    assert.ok(!shared.isClaudeModelName(value), String(value));
    assert.equal(shared.normalizeClaudeModel(value), "default", String(value));
  }
});

test("CLAUDE_RECORDED_VERSIONS is exactly the recorded fixture directories", () => {
  const recorded = fs.readdirSync(fixturesDir, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && /^claude-\d+\.\d+\.\d+$/.test(entry.name))
    .map((entry) => entry.name.slice("claude-".length))
    .sort();
  assert.ok(recorded.length > 0);
  assert.deepEqual([...shared.CLAUDE_RECORDED_VERSIONS], recorded);
  assert.deepEqual([...shared.CLAUDE_RECORDED_VERSIONS], ["2.1.312", "2.1.319", "2.1.322"]);
});

test("the alias, preset and flag helpers of #219 are gone", () => {
  for (const name of ["CLAUDE_MODEL_ALIASES", "CLAUDE_TOOL_PRESETS", "DEFAULT_CLAUDE_MODEL_ALIAS", "DEFAULT_CLAUDE_TOOL_PRESET", "isClaudeModelAlias", "isClaudeToolPreset", "claudeModelFlag"]) {
    assert.equal(shared[name], undefined, name);
  }
});

test("claudeFirstMessage: the typed text as is, else [Image] with images, else empty", () => {
  assert.equal(shared.CLAUDE_IMAGE_FIRST_MESSAGE, "[Image]");
  assert.equal(shared.claudeFirstMessage("hello ", 2), "hello ");
  assert.equal(shared.claudeFirstMessage("", 1), "[Image]");
  assert.equal(shared.claudeFirstMessage("  ", 1), "[Image]");
  assert.equal(shared.claudeFirstMessage("", 0), "");
});

test("claude's effort levels are exactly low/medium/high/xhigh/max plus the default entry", () => {
  assert.deepEqual([...shared.CLAUDE_EFFORT_LEVELS], ["low", "medium", "high", "xhigh", "max"]);
  assert.equal(shared.CLAUDE_EFFORT_DEFAULT, "default");
  for (const level of shared.CLAUDE_EFFORT_LEVELS) assert.ok(shared.isClaudeEffort(level) && shared.isClaudeEffortLevel(level), level);
  assert.ok(shared.isClaudeEffort("default") && !shared.isClaudeEffortLevel("default"));
  for (const invented of ["off", "minimal", "auto", "", null, undefined, 3]) assert.ok(!shared.isClaudeEffort(invented), String(invented));
});

test("a stored effort reads tolerantly: a level stays, anything else is the default entry", () => {
  for (const level of shared.CLAUDE_EFFORT_LEVELS) assert.equal(shared.normalizeClaudeEffort(level), level);
  for (const value of [undefined, null, "default", "off", "minimal", "auto", "MAX", 7, {}]) assert.equal(shared.normalizeClaudeEffort(value), "default", String(value));
});

test("no effort level is hidden without a recording of claude refusing it", () => {
  assert.deepEqual(shared.CLAUDE_EFFORT_EXCLUSIONS, {});
});

test("every claude feature threshold is a recorded fixture version", () => {
  assert.deepEqual([...shared.CLAUDE_PI_WEB_FEATURES], ["approvals", "backgroundTasks", "subagentText"]);
  assert.deepEqual(Object.keys(shared.CLAUDE_FEATURE_MIN_VERSION).sort(), ["backgroundTasks", "subagentText"]);
  for (const [feature, version] of Object.entries(shared.CLAUDE_FEATURE_MIN_VERSION)) {
    assert.ok(shared.CLAUDE_RECORDED_VERSIONS.includes(version), `${feature}: ${version} is not a recorded version`);
  }
  assert.equal(shared.compareClaudeVersions("2.1.322", "2.1.322"), 0);
  assert.ok(shared.compareClaudeVersions("2.1.322", "2.1.319") > 0);
  assert.ok(shared.compareClaudeVersions("2.1.99", "2.1.322") < 0, "numeric, not lexical");
  assert.ok(shared.compareClaudeVersions("2.2", "2.1.322") > 0);
  assert.ok(shared.compareClaudeVersions("2.1.322.1", "2.1.322") > 0);
  for (const [a, b] of [["2.1.x", "2.1.322"], ["", "2.1.322"], ["2.1.322", "v2"], [null, "2.1.322"]]) {
    assert.equal(shared.compareClaudeVersions(a, b), null, `${a} vs ${b}`);
  }
});

// r3 (owner ruling 2026-11-13T07:40:00Z):'s naming wins on the default
// branch, so #445's launch permission mode (claude's --permission-mode) has its own names.
test("the launch permission mode is its own vocabulary: default, acceptEdits and plan under CLAUDE_LAUNCH_PERMISSION_MODES, apart from's approval modes ( r3)", () => {
  assert.deepEqual(shared.CLAUDE_LAUNCH_PERMISSION_MODES, ["default", "acceptEdits", "plan"]);
  assert.equal(shared.CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT, "default");
  assert.equal(typeof shared.isClaudeLaunchPermissionMode, "function");
  assert.ok(shared.isClaudeLaunchPermissionMode("plan"));
  assert.ok(!shared.isClaudeLaunchPermissionMode("ask"));
  assert.ok(!(shared.CLAUDE_PERMISSION_MODES ?? []).includes("acceptEdits"), "CLAUDE_PERMISSION_MODES is's approval modes, never the launch mode");
});
