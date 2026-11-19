// carries: lib/harness/effective.ts, lib/harness/compare.ts, lib/harness/adapters.ts, lib/harness/pi.ts, lib/harness/claude.ts
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// The read-back comparison (AC2, Q3 A+B): reasoning effort consistent and
// inconsistent — each harness's current value listed — and the approval mode
// unsupported on pi. The per-layer effective values carry their source and
// `inferred: true` (#85 d-5). Expected values are hand-written from the
// ticket; the fixtures are generated here (Q6 A). One project folder carries
// both harnesses' project layers (`.pi/` and `.claude/`), and one agent dir
// carries both pi's user layer and the Pi Web-stored claude files — as in a
// real machine, where the pi agent dir is where pi-web stores its own files.
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-harness-compare-"));
const agentDir = path.join(scratch, "agent");
const project = path.join(scratch, "project");
const claudeConfigDir = path.join(scratch, "claude-config");
fs.mkdirSync(path.join(project, ".pi"));
fs.mkdirSync(path.join(project, ".claude"));
fs.mkdirSync(claudeConfigDir);
fs.writeFileSync(path.join(agentDir, "settings.json"), JSON.stringify({
  defaultProvider: "anthropic",
  defaultModel: "claude-opus-4",
  defaultThinkingLevel: "medium",
}));
fs.writeFileSync(path.join(agentDir, "pi-web-claude-settings.json"), JSON.stringify({
  version: 1,
  launchPermissionMode: "acceptEdits",
  customModels: [],
  includeHookEvents: false,
}));
fs.writeFileSync(path.join(agentDir, "claude-permissions.json"), JSON.stringify({ mode: "deny", push: true }));
fs.writeFileSync(path.join(project, ".pi", "settings.json"), JSON.stringify({ defaultThinkingLevel: "low" }));
fs.writeFileSync(path.join(project, ".claude", "settings.json"), JSON.stringify({ model: "haiku" }));
fs.writeFileSync(path.join(project, ".claude", "settings.local.json"), JSON.stringify({ effortLevel: "low" }));
fs.writeFileSync(path.join(claudeConfigDir, "settings.json"), JSON.stringify({
  model: "sonnet",
  effortLevel: "medium",
  permissions: { defaultMode: "acceptEdits" },
}));
test.after(() => {
  fs.rmSync(scratch, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const effective = await jiti.import("./effective.ts");
const compare = await jiti.import("./compare.ts");
const pi = await jiti.import("./pi.ts");
const claude = await jiti.import("./claude.ts");
const fixtureCtx = () => ({ agentDir, cwd: project, claudeConfigDir, piVersion: "1.5.0", env: {} });

test("pi effective values: the project layer overrides the user layer, every value inferred", () => {
  const payload = effective.piEffectiveConfig(fixtureCtx());
  assert.deepEqual(payload.fields["reasoning-effort"], {
    value: "low",
    sourceScope: "project",
    sourcePath: path.join(project, ".pi", "settings.json"),
    inferred: true,
  });
  assert.deepEqual(payload.fields["model"], {
    value: "claude-opus-4",
    sourceScope: "user",
    sourcePath: path.join(agentDir, "settings.json"),
    inferred: true,
  });
  assert.deepEqual(payload.fields["provider"], {
    value: "anthropic",
    sourceScope: "user",
    sourcePath: path.join(agentDir, "settings.json"),
    inferred: true,
  });
});

test("claude effective values: layers in claude's order, plus the Pi Web-stored layer", () => {
  const payload = effective.claudeEffectiveConfig(fixtureCtx());
  assert.deepEqual(payload.fields["model"], {
    value: "haiku",
    sourceScope: "project",
    sourcePath: path.join(project, ".claude", "settings.json"),
    inferred: true,
  });
  assert.deepEqual(payload.fields["reasoning-effort"], {
    value: "low",
    sourceScope: "local",
    sourcePath: path.join(project, ".claude", "settings.local.json"),
    inferred: true,
  });
  assert.deepEqual(payload.fields["approval-mode"], {
    value: "acceptEdits",
    sourceScope: "user",
    sourcePath: path.join(claudeConfigDir, "settings.json"),
    inferred: true,
  });
  assert.deepEqual(payload.fields["pi-web:launch-permission-mode"], {
    value: "acceptEdits",
    sourceScope: "pi-web",
    sourcePath: path.join(agentDir, "pi-web-claude-settings.json"),
    inferred: true,
  });
  assert.deepEqual(payload.fields["pi-web:tool-permissions"], {
    value: "deny",
    sourceScope: "pi-web",
    sourcePath: path.join(agentDir, "claude-permissions.json"),
    inferred: true,
  });
});

test("the adapters' effective-config read carries the same values (u4 wiring)", async () => {
  const piPayload = await pi.createPiAdapter().read("effective-config", fixtureCtx());
  assert.equal(piPayload.fields["reasoning-effort"].value, "low");
  const claudePayload = await claude.createClaudeAdapter().read("effective-config", fixtureCtx());
  assert.equal(claudePayload.fields["reasoning-effort"].value, "low");
  assert.equal(claudePayload.fields["approval-mode"].value, "acceptEdits");
});

test("compare reasoning effort: consistent when both fixtures agree, each value listed", async () => {
  const result = await compare.compareField("reasoning-effort", fixtureCtx());
  assert.equal(result.status, "consistent");
  assert.deepEqual(result.values.pi, { supported: true, neutral: "low", raw: "low" });
  assert.deepEqual(result.values.claude, { supported: true, neutral: "low", raw: "low" });
});

test("compare reasoning effort: inconsistent lists each harness's current value (AC2)", async () => {
  const piProjectSettings = path.join(project, ".pi", "settings.json");
  const original = fs.readFileSync(piProjectSettings, "utf8");
  try {
    fs.writeFileSync(piProjectSettings, JSON.stringify({ defaultThinkingLevel: "high" }));
    const result = await compare.compareField("reasoning-effort", fixtureCtx());
    assert.equal(result.status, "inconsistent");
    assert.deepEqual(result.values.pi, { supported: true, neutral: "high", raw: "high" });
    assert.deepEqual(result.values.claude, { supported: true, neutral: "low", raw: "low" });
  } finally {
    fs.writeFileSync(piProjectSettings, original);
  }
});

test("compare approval mode: unsupported on pi, the claude value listed (Q3 B)", async () => {
  const result = await compare.compareField("approval-mode", fixtureCtx());
  assert.equal(result.status, "unsupported");
  assert.deepEqual(result.values.pi, { supported: false, neutral: null, raw: null });
  assert.deepEqual(result.values.claude, { supported: true, neutral: "accept-edits", raw: "acceptEdits" });
});

test("compareAll walks COMPARE_FIELDS in order", async () => {
  const results = await compare.compareAll(fixtureCtx());
  assert.deepEqual(results.map((entry) => entry.field), ["reasoning-effort", "approval-mode"]);
  assert.equal(results[0].status, "consistent");
  assert.equal(results[1].status, "unsupported");
});
