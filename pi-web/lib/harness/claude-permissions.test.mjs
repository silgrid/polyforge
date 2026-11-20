// carries: lib/harness/claude.ts, lib/harness/pi.ts, lib/harness/concepts.ts, lib/harness/claude-native-write.ts
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";
import { ProjectTrustStore } from "@earendil-works/pi-coding-agent";

// polyforge#150 (85a4) AC1/AC2: the claude adapter reads the extended
// permission payload (sandbox, rules, denyInsteadOfPrompting, per-field
// provenance) and writes the native user-level settings.json through the
// shared write gate; pi answers 不做审批 / 无沙箱 and writes only a project
// trust decision. Expected values are hand-derived from the ticket (Q1 A's
// 询问→default / 自动审查→auto / 不询问即拒绝→dontAsk mapping, Q3 B's
// user-level-only write, claude's settings-layer order
// managed > local > project > user and its merged permission lists); the
// fixtures are generated here, never committed (85a1's Q6 A ruling).
const here = path.dirname(new URL(import.meta.url).pathname);
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-harness-cl-perm-"));
const configDir = path.join(scratch, "claude-config");
const agentDir = path.join(scratch, "agent");
const project = path.join(scratch, "project");
fs.mkdirSync(configDir, { recursive: true });
fs.mkdirSync(agentDir, { recursive: true });
fs.mkdirSync(path.join(project, ".claude"), { recursive: true });
fs.mkdirSync(path.join(project, ".pi"), { recursive: true });

// The user layer: claude's own settings.json, holding keys pi-web must keep.
fs.writeFileSync(path.join(configDir, "settings.json"), JSON.stringify({
  model: "opus",
  effortLevel: "high",
  env: { ANTHROPIC_CUSTOM_HEADER: "x" },
  permissions: { defaultMode: "acceptEdits", allow: ["WebSearch"], deny: ["Bash(rm:*)"] },
  sandbox: { enabled: true, autoAllowBashIfSandboxed: false, failIfUnavailable: true },
}));
// The project layer: shadows the user's defaultMode and adds an ask rule.
fs.writeFileSync(path.join(project, ".claude", "settings.json"), JSON.stringify({
  permissions: { defaultMode: "dontAsk", denyInsteadOfPrompting: true, ask: ["Read(~/.ssh/**)"] },
}));
// pi's trust store target: a project with trust-requiring resources.
fs.writeFileSync(path.join(project, ".pi", "mcp.json"), JSON.stringify({ mcpServers: {} }));
// claude's state file, with keys a trust write must preserve.
fs.writeFileSync(path.join(configDir, ".claude.json"), JSON.stringify({
  mcpServers: { "state-mcp": { command: "node" } },
  projects: { "/keep/other": { hasTrustDialogAccepted: true } },
}));

const stubLog = path.join(scratch, "stub.jsonl");
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_CODING_AGENT_DIR = agentDir;
delete process.env.CLAUDE_CODE_DISABLE_BACKGROUND_TASKS;
const managedSettings = path.join(scratch, "managed-settings.json");

/** A stub `claude` answering initialize with the given version (85a1's harness). */
function installVersionedStub(name, version) {
  const dir = path.join(scratch, name);
  fs.mkdirSync(dir);
  const binary = path.join(dir, "claude");
  fs.writeFileSync(binary, `#!/bin/sh\nexport PI_TEST_CLAUDE_STUB_VERSION=${version}\nexec node ${path.join(here, "..", "__fixtures__", "claude-stub", "claude.mjs")} "$@"\n`, { mode: 0o755 });
  return binary;
}
const okBinary = installVersionedStub("ok-bin", "2.1.326");
const unknownBinary = installVersionedStub("unknown-bin", "3.0.0");

test.after(() => {
  fs.rmSync(scratch, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const claudeModule = await jiti.import("./claude.ts");
const piModule = await jiti.import("./pi.ts");
const concepts = await jiti.import("./concepts.ts");
const claudeAdapter = claudeModule.createClaudeAdapter();
const piAdapter = piModule.createPiAdapter();

const fixtureCtx = (extra = {}) => ({
  claudeConfigDir: configDir,
  agentDir,
  claudeBinary: okBinary,
  env: {},
  managedSettingsPath: managedSettings,
  ...extra,
});
const userSettings = () => JSON.parse(fs.readFileSync(path.join(configDir, "settings.json"), "utf8"));
const stateFile = () => JSON.parse(fs.readFileSync(path.join(configDir, ".claude.json"), "utf8"));
const resetUserSettings = () => fs.writeFileSync(path.join(configDir, "settings.json"), JSON.stringify({
  model: "opus",
  effortLevel: "high",
  env: { ANTHROPIC_CUSTOM_HEADER: "x" },
  permissions: { defaultMode: "acceptEdits", allow: ["WebSearch"], deny: ["Bash(rm:*)"] },
  sandbox: { enabled: true, autoAllowBashIfSandboxed: false, failIfUnavailable: true },
}));

test("dontAsk is a neutral approval value: 不询问即拒绝 maps to deny-unprompted (Q1 A)", () => {
  assert.equal(concepts.neutralApprovalMode("dontAsk"), "deny-unprompted");
  assert.equal(concepts.neutralApprovalMode("default"), "ask");
  assert.equal(concepts.neutralApprovalMode("auto"), "auto");
});

test("read(permission): the project layer's dontAsk wins with its source, and the sandbox, rules and denyInsteadOfPrompting read back (AC1/AC2)", async () => {
  const payload = await claudeAdapter.read("permission", fixtureCtx({ cwd: project }));
  assert.equal(payload.approvalMode.raw, "dontAsk");
  assert.equal(payload.approvalMode.neutral, "deny-unprompted");
  assert.equal(payload.approvalMode.supported, true);
  assert.equal(payload.sources.defaultMode.scope, "project");
  assert.equal(payload.sources.defaultMode.path, path.join(project, ".claude", "settings.json"));
  // The sandbox object: enabled and autoAllow as booleans, other keys by name only.
  assert.deepEqual(payload.sandbox, {
    supported: true,
    enabled: true,
    autoAllowBashIfSandboxed: false,
    otherKeys: ["failIfUnavailable"],
  });
  assert.equal(payload.sources.sandbox.scope, "user");
  // claude merges its permission lists across layers.
  assert.deepEqual(payload.rules, { allow: ["WebSearch"], ask: ["Read(~/.ssh/**)"], deny: ["Bash(rm:*)"] });
  assert.equal(payload.sources.rules.scope, "project");
  // denyInsteadOfPrompting is claude's own boolean key (v2.1.319+), beside the mode.
  assert.equal(payload.denyInsteadOfPrompting, true);
  assert.equal(payload.sources.denyInsteadOfPrompting.scope, "project");
  // The claude trust of the project folder, from the state file.
  assert.deepEqual(payload.projectTrust, { trusted: false });
});

test("read(permission) without a cwd: no project layers, no trust answer", async () => {
  const payload = await claudeAdapter.read("permission", fixtureCtx());
  assert.equal(payload.approvalMode.raw, "acceptEdits");
  assert.equal(payload.approvalMode.neutral, "accept-edits");
  assert.equal(payload.sources.defaultMode.scope, "user");
  assert.equal(payload.denyInsteadOfPrompting, null);
  assert.deepEqual(payload.rules, { allow: ["WebSearch"], ask: [], deny: ["Bash(rm:*)"] });
  assert.equal(payload.projectTrust, null);
});

test("pi read(permission): no approval, no sandbox, no rules (AC1's 不做审批 / 无沙箱)", async () => {
  const payload = await piAdapter.read("permission", { agentDir, cwd: project, piVersion: "1.5.0" });
  assert.deepEqual(payload.approvalMode, { supported: false, neutral: null, raw: null });
  assert.deepEqual(payload.sandbox, { supported: false, enabled: null, autoAllowBashIfSandboxed: null, otherKeys: [] });
  assert.equal(payload.rules, null);
  assert.equal(payload.denyInsteadOfPrompting, null);
  assert.equal(payload.sources.defaultMode, null);
  assert.deepEqual(payload.projectTrust, { trusted: false });
});

test("write(permission) approvalMode: the unified value lands in the native user settings.json and nothing else moves (AC2)", async () => {
  resetUserSettings();
  const result = await claudeAdapter.write("permission", { approvalMode: "auto" }, fixtureCtx());
  assert.equal(result.status, "written");
  const settings = userSettings();
  assert.equal(settings.permissions.defaultMode, "auto");
  // Unknown fields, the other permission keys and the sandbox stay as they were.
  assert.equal(settings.model, "opus");
  assert.equal(settings.effortLevel, "high");
  assert.deepEqual(settings.env, { ANTHROPIC_CUSTOM_HEADER: "x" });
  assert.deepEqual(settings.permissions.allow, ["WebSearch"]);
  assert.deepEqual(settings.permissions.deny, ["Bash(rm:*)"]);
  assert.equal(settings.sandbox.enabled, true);
});

test("write(permission) acceptEdits, plan and bypassPermissions are claude's own extra values and stay writable (AC2)", async () => {
  for (const mode of ["acceptEdits", "plan", "bypassPermissions", "dontAsk", "default"]) {
    resetUserSettings();
    const result = await claudeAdapter.write("permission", { approvalMode: mode }, fixtureCtx());
    assert.equal(result.status, "written", mode);
    assert.equal(userSettings().permissions.defaultMode, mode);
  }
});

test("write(permission) refuse an approval mode claude does not have", async () => {
  const result = await claudeAdapter.write("permission", { approvalMode: "nope" }, fixtureCtx());
  assert.equal(result.status, "refused");
  assert.ok(result.reason.includes("nope"), result.reason);
});

test("write(permission) denyInsteadOfPrompting: claude's boolean key lands beside the mode (AC2, Q1 A)", async () => {
  resetUserSettings();
  const result = await claudeAdapter.write("permission", { denyInsteadOfPrompting: true }, fixtureCtx());
  assert.equal(result.status, "written");
  const settings = userSettings();
  assert.equal(settings.permissions.denyInsteadOfPrompting, true);
  assert.equal(settings.permissions.defaultMode, "acceptEdits");
});

test("write(permission) sandbox: enabled and autoAllowBashIfSandboxed land, claude's other sandbox keys stay (AC2)", async () => {
  resetUserSettings();
  const off = await claudeAdapter.write("permission", { sandboxEnabled: false }, fixtureCtx());
  assert.equal(off.status, "written");
  const on = await claudeAdapter.write("permission", { autoAllowBashIfSandboxed: true }, fixtureCtx());
  assert.equal(on.status, "written");
  const sandbox = userSettings().sandbox;
  assert.equal(sandbox.enabled, false);
  assert.equal(sandbox.autoAllowBashIfSandboxed, true);
  assert.equal(sandbox.failIfUnavailable, true);
});

test("write(permission) rules: allow/ask/deny replace the user layer's lists; an empty list removes its key (AC2)", async () => {
  resetUserSettings();
  const result = await claudeAdapter.write("permission", { rules: { allow: ["WebFetch(domain:example.com)"], ask: ["Read(~/secrets/**)"], deny: [] } }, fixtureCtx());
  assert.equal(result.status, "written");
  const permissions = userSettings().permissions;
  assert.deepEqual(permissions.allow, ["WebFetch(domain:example.com)"]);
  assert.deepEqual(permissions.ask, ["Read(~/secrets/**)"]);
  assert.equal("deny" in permissions, false);
  assert.equal(permissions.defaultMode, "acceptEdits");
});

test("write(permission) projectTrust: the state file records the decision and keeps every other key (AC1)", async () => {
  const result = await claudeAdapter.write("permission", { projectTrust: { trusted: true } }, fixtureCtx({ cwd: project }));
  assert.equal(result.status, "written");
  const state = stateFile();
  assert.equal(state.projects[project].hasTrustDialogAccepted, true);
  assert.deepEqual(state.mcpServers, { "state-mcp": { command: "node" } });
  assert.equal(state.projects["/keep/other"].hasTrustDialogAccepted, true);
  const back = await claudeAdapter.read("permission", fixtureCtx({ cwd: project }));
  assert.deepEqual(back.projectTrust, { trusted: true });
  // Untrusting records the explicit false.
  const undo = await claudeAdapter.write("permission", { projectTrust: { trusted: false } }, fixtureCtx({ cwd: project }));
  assert.equal(undo.status, "written");
  assert.equal(stateFile().projects[project].hasTrustDialogAccepted, false);
});

test("write(permission) refusals: an unknown change key, an empty change, a missing cwd for trust", async () => {
  const unknown = await claudeAdapter.write("permission", { nonsense: true }, fixtureCtx());
  assert.equal(unknown.status, "refused");
  const empty = await claudeAdapter.write("permission", {}, fixtureCtx());
  assert.equal(empty.status, "refused");
  const noCwd = await claudeAdapter.write("permission", { projectTrust: { trusted: true } }, fixtureCtx());
  assert.equal(noCwd.status, "refused");
});

test("write(permission) the gate: an unknown version, no claude on PATH and a malformed user settings.json all refuse (Q2 C/Q3 B)", async () => {
  const unknown = await claudeAdapter.write("permission", { approvalMode: "auto" }, fixtureCtx({ claudeBinary: unknownBinary }));
  assert.equal(unknown.status, "refused");
  assert.ok(unknown.reason.includes("3.0.0"), unknown.reason);
  const noBinary = await claudeAdapter.write("permission", { approvalMode: "auto" }, fixtureCtx({ claudeBinary: path.join(scratch, "no-such-bin", "claude") }));
  assert.equal(noBinary.status, "refused");
  fs.writeFileSync(path.join(configDir, "settings.json"), "[1, 2]");
  const malformed = await claudeAdapter.write("permission", { approvalMode: "auto" }, fixtureCtx());
  assert.equal(malformed.status, "refused");
  resetUserSettings();
});

test("pi write(permission): approval and sandbox are refused with pi's own reason; a trust decision writes trust.json (AC1)", async () => {
  const mode = await piAdapter.write("permission", { approvalMode: "ask" }, { agentDir, cwd: project, piVersion: "1.5.0" });
  assert.equal(mode.status, "refused");
  assert.ok(mode.reason.includes("approval"), mode.reason);
  const sandbox = await piAdapter.write("permission", { sandboxEnabled: true }, { agentDir, cwd: project, piVersion: "1.5.0" });
  assert.equal(sandbox.status, "refused");
  assert.ok(sandbox.reason.includes("sandbox"), sandbox.reason);
  const noCwd = await piAdapter.write("permission", { projectTrust: { trusted: true } }, { agentDir, piVersion: "1.5.0" });
  assert.equal(noCwd.status, "refused");

  const trusted = await piAdapter.write("permission", { projectTrust: { trusted: true } }, { agentDir, cwd: project, piVersion: "1.5.0" });
  assert.equal(trusted.status, "written");
  const entry = new ProjectTrustStore(agentDir).getEntry(project);
  assert.equal(entry.decision, true);
  const denied = await piAdapter.write("permission", { projectTrust: { trusted: false } }, { agentDir, cwd: project, piVersion: "1.5.0" });
  assert.equal(denied.status, "written");
  assert.equal(new ProjectTrustStore(agentDir).getEntry(project).decision, false);
});

test("the adapters declare permission and model writable (the harness list's 可写; 85a3 adds model)", () => {
  assert.deepEqual(claudeAdapter.capabilities.writableConcepts, ["auth", "permission", "model"]);
  assert.deepEqual(piAdapter.capabilities.writableConcepts, ["permission", "model"]);
});
