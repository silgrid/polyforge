import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-route-"));
const agentDir = path.join(scratch, "agent");
const binDir = path.join(scratch, "bin");
const failDir = path.join(scratch, "fail-bin");
fs.mkdirSync(binDir);
fs.mkdirSync(failDir);
// The unit claude stub, which answers `initialize` from the 2.1.322 recording next to its directory.
fs.copyFileSync(new URL("../../../lib/__fixtures__/claude-stub/claude.mjs", import.meta.url), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
fs.symlinkSync(decodeURIComponent(new URL("../../../lib/__fixtures__/claude-2.1.322", import.meta.url).pathname), path.join(scratch, "claude-2.1.322"));
// A claude that cannot answer: it exits before reading anything.
fs.writeFileSync(path.join(failDir, "claude"), "#!/bin/sh\necho 'claude: not logged in' >&2\nexit 1\n", { mode: 0o755 });
process.env.PI_CODING_AGENT_DIR = agentDir;
// Pi Web's background-task feature reads the child's environment: the tests set
// CLAUDE_CODE_DISABLE_BACKGROUND_TASKS themselves, never inherit the host's.
delete process.env.CLAUDE_CODE_DISABLE_BACKGROUND_TASKS;
const originalPath = process.env.PATH;
test.after(() => {
  process.env.PATH = originalPath;
  fs.rmSync(scratch, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const { GET } = await jiti.import("./route.ts");
const settings = await jiti.import("../../../lib/claude-settings.ts");

const ALIASES = [
  { value: "default", displayName: "Default (recommended)", resolvedModel: "claude-opus-5-5", source: "alias" },
  { value: "opus", displayName: "Opus", resolvedModel: "claude-opus-5-5", source: "alias" },
  { value: "fable", displayName: "Fable", resolvedModel: "claude-fable-5-2", source: "alias" },
  { value: "sonnet", displayName: "Sonnet", resolvedModel: "claude-sonnet-5-5", source: "alias" },
  { value: "haiku", displayName: "Haiku", resolvedModel: "claude-haiku-5-5", source: "alias" },
];
const CAPABILITIES = ["ui_surface_v1", "mcp_call_result_not_for_model_v1", "mcp_call_check_permissions_v1"];
const RECORDED = ["2.1.312", "2.1.319", "2.1.322"];
// r3: approvals follow's global permission mode; no
// claude-permissions.json reads as its default, ask.
const permissionConfig = path.join(agentDir, "claude-permissions.json");
const FEATURES_322 = [
  { id: "approvals", available: true, basis: "host-prompts" },
  { id: "backgroundTasks", available: true, basis: "recorded" },
  { id: "subagentText", available: true, basis: "recorded" },
];
const CUSTOM = { launchPermissionMode: "default", customModels: ["claude-custom-1", "opus", "us.anthropic.claude-x-v1:0"], includeHookEvents: false };

test("claude on PATH: the models are claude's aliases from its initialize answer plus the custom names, the default is default, with the version and capabilities", async () => {
  process.env.PATH = `${binDir}${path.delimiter}${originalPath}`;
  const bare = await (await GET()).json();
  assert.deepEqual(bare, {
    available: true,
    models: ALIASES,
    defaultModel: "default",
    version: "2.1.322",
    versionRecorded: true,
    recordedVersions: RECORDED,
    capabilities: CAPABILITIES,
    features: FEATURES_322,
  });
  assert.equal(fs.existsSync(agentDir), false, "reading availability creates no directory");
  await settings.writeClaudeSettings(CUSTOM);
  const body = await (await GET()).json();
  assert.deepEqual(body.models, [
    ...ALIASES,
    // A custom name that is already one of claude's aliases is listed once, as the alias.
    { value: "claude-custom-1", source: "custom" },
    { value: "us.anthropic.claude-x-v1:0", source: "custom" },
  ]);
  assert.equal(body.defaultModel, "default");
  assert.equal(body.probeError, undefined);
  for (const retired of ["configDir", "presets", "defaultPreset", "modelAliases"]) assert.equal(body[retired], undefined, retired);
});

test("a failed probe still offers default plus the custom names, with the error", async () => {
  process.env.PATH = `${failDir}${path.delimiter}${originalPath}`;
  const body = await (await GET()).json();
  assert.equal(body.available, true, "claude is on PATH");
  assert.deepEqual(body.models, [
    { value: "default", source: "alias" },
    { value: "claude-custom-1", source: "custom" },
    { value: "opus", source: "custom" },
    { value: "us.anthropic.claude-x-v1:0", source: "custom" },
  ]);
  assert.equal(body.defaultModel, "default");
  assert.equal(body.version, null);
  assert.equal(body.versionRecorded, null, "an unknown version is neither covered nor not");
  assert.deepEqual(body.capabilities, []);
  assert.deepEqual(body.recordedVersions, RECORDED);
  assert.equal(typeof body.probeError, "string");
  assert.match(body.probeError, /not logged in|exit/);
});

test("no claude on PATH: unavailable with its reason, still no side effect", async () => {
  process.env.PATH = path.join(scratch, "empty");
  const body = await (await GET()).json();
  assert.equal(body.available, false);
  assert.equal(body.reason, "not-on-path");
  assert.equal(body.configDir, undefined);
  assert.equal(body.version, null);
  assert.deepEqual(body.models.map((model) => model.value), ["default", "claude-custom-1", "opus", "us.anthropic.claude-x-v1:0"]);
  assert.equal(body.defaultModel, "default");
});

test("GET /api/claude answers Pi Web's three claude features with their basis next to claude's own capabilities verbatim", async () => {
  process.env.PATH = `${binDir}${path.delimiter}${originalPath}`;
  const body = await (await GET()).json();
  assert.deepEqual(body.capabilities, CAPABILITIES, "claude's own names, never mapped");
  assert.deepEqual(body.features, FEATURES_322);
  // The child's environment (Pi Web's, minus the withheld names) decides background tasks.
  process.env.CLAUDE_CODE_DISABLE_BACKGROUND_TASKS = "1";
  try {
    const disabled = await (await GET()).json();
    assert.deepEqual(disabled.features.find((feature) => feature.id === "backgroundTasks"), { id: "backgroundTasks", available: false, basis: "env-disabled" });
  } finally {
    delete process.env.CLAUDE_CODE_DISABLE_BACKGROUND_TASKS;
  }
  // A failed probe still answers the features: no version, so the last two are unknown.
  process.env.PATH = `${failDir}${path.delimiter}${originalPath}`;
  const failed = await (await GET()).json();
  assert.deepEqual(failed.features, [
    { id: "approvals", available: true, basis: "host-prompts" },
    { id: "backgroundTasks", available: null, basis: "version-unknown" },
    { id: "subagentText", available: null, basis: "version-unknown" },
  ]);

  //'s global mode on deny: Pi Web starts claude with --permission-prompts none.
  process.env.PATH = `${binDir}${path.delimiter}${originalPath}`;
  fs.mkdirSync(agentDir, { recursive: true });
  fs.writeFileSync(permissionConfig, JSON.stringify({ version: 1, mode: "deny", push: false }));
  try {
    const denied = await (await GET()).json();
    assert.deepEqual(denied.features, [{ id: "approvals", available: false, basis: "no-host-prompts" }, ...FEATURES_322.slice(1)]);
  } finally {
    fs.rmSync(permissionConfig, { force: true });
  }
  const asked = await (await GET()).json();
  assert.deepEqual(asked.features, FEATURES_322, "with the file gone, the default ask makes approvals available");
});
