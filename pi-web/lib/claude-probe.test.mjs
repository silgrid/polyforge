import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { createJiti } from "jiti";
import { readStubLog } from "./__fixtures__/claude-stub-log.mjs";

// What the claude on PATH reports about itself: its `initialize` answer,
// asked of one claude with no prompt (zero credit), read from the 2.1.322 recording.
const here = path.dirname(fileURLToPath(import.meta.url));
const recording = path.join(here, "__fixtures__", "claude-2.1.322", "initialize");
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-probe-"));
const stubLog = path.join(scratch, "stub.jsonl");
// The unit stub reads the recordings next to its own directory.
fs.symlinkSync(path.join(here, "__fixtures__", "claude-2.1.322"), path.join(scratch, "claude-2.1.322"));
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_WEB_PASSWORD = "not-for-the-child";
test.after(() => fs.rmSync(scratch, { recursive: true, force: true }));

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const probe = await jiti.import("./claude-probe.ts");
const shared = await jiti.import("./claude-shared.ts");

/** A copy of the unit claude stub as `<dir>/claude`. */
function installStub(name) {
  const dir = path.join(scratch, name);
  fs.mkdirSync(dir);
  const binary = path.join(dir, "claude");
  fs.copyFileSync(path.join(here, "__fixtures__", "claude-stub", "claude.mjs"), binary);
  fs.chmodSync(binary, 0o755);
  return binary;
}
function installScript(name, body) {
  const dir = path.join(scratch, name);
  fs.mkdirSync(dir);
  const binary = path.join(dir, "claude");
  fs.writeFileSync(binary, body, { mode: 0o755 });
  return binary;
}
const recordedAnswer = () => {
  const frames = fs.readFileSync(path.join(recording, "stream.jsonl"), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
  return frames.find((frame) => frame.type === "control_response" && frame.response?.request_id === "init-1").response.response;
};
const ALIASES = [
  { value: "default", displayName: "Default (recommended)", resolvedModel: "claude-opus-5-5" },
  { value: "opus", displayName: "Opus", resolvedModel: "claude-opus-5-5" },
  { value: "fable", displayName: "Fable", resolvedModel: "claude-fable-5-2" },
  { value: "sonnet", displayName: "Sonnet", resolvedModel: "claude-sonnet-5-5" },
  { value: "haiku", displayName: "Haiku", resolvedModel: "claude-haiku-5-5" },
];
const CAPABILITIES = ["ui_surface_v1", "mcp_call_result_not_for_model_v1", "mcp_call_check_permissions_v1"];

test("the recorded 2.1.322 initialize answer yields the version, claude's alias list with resolved names and its capabilities", () => {
  assert.deepEqual(probe.claudeInitializeInfo(recordedAnswer()), { version: "2.1.322", models: ALIASES, capabilities: CAPABILITIES, recorded: true });
  // Tolerant: a malformed answer is no version, no model, no capability — never a throw.
  for (const value of [undefined, null, "x", {}, { models: "x", capabilities: [1], claude_code_version: 7 }, { models: [{ value: "-bad name" }, { value: 3 }, null] }]) {
    assert.deepEqual(probe.claudeInitializeInfo(value), { version: null, models: [], capabilities: [], recorded: false }, JSON.stringify(value));
  }
});

test("a probe runs one claude with no prompt, answers from its initialize response and caches it per binary", async () => {
  const binary = installStub("probe-bin");
  const answer = await probe.probeClaude(binary);
  assert.deepEqual(answer, { version: "2.1.322", models: ALIASES, capabilities: CAPABILITIES, recorded: true });
  const records = readStubLog(stubLog);
  const launches = records.filter((entry) => entry.kind === "probe");
  assert.equal(launches.length, 1, "one claude");
  assert.deepEqual(launches[0].argv, ["-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--no-session-persistence", "--permission-prompts", "none"]);
  assert.deepEqual([...probe.CLAUDE_PROBE_ARGV], launches[0].argv);
  assert.equal(launches[0].cwd, fs.realpathSync(os.homedir()), "run from the user's home");
  assert.ok(!launches[0].envKeys.includes("PI_WEB_PASSWORD"), "the child's environment is buildClaudeEnv's");
  const stdin = records.filter((entry) => entry.kind === "stdin").map((entry) => JSON.parse(entry.line));
  assert.equal(stdin.length, 1, "one line, no prompt");
  assert.equal(stdin[0].type, "control_request");
  assert.deepEqual(stdin[0].request, { subtype: "initialize" });
  assert.deepEqual(await probe.probeClaude(binary), answer, "the cached answer");
  assert.equal(readStubLog(stubLog).filter((entry) => entry.kind === "probe").length, 1, "the same binary is not asked twice");
  // An alias reads the full name the latest answer resolves it to; any other name is itself.
  assert.equal(probe.claudeResolvedModel("opus"), "claude-opus-5-5");
  assert.equal(probe.claudeResolvedModel("default"), "claude-opus-5-5");
  assert.equal(probe.claudeResolvedModel("fable"), "claude-fable-5-2");
  assert.equal(probe.claudeResolvedModel("claude-sonnet-5-5"), "claude-sonnet-5-5");
  assert.equal(probe.claudeResolvedModel("claude-custom-1"), "claude-custom-1");
  // A changed binary (another size and mtime) is asked again.
  fs.appendFileSync(binary, "\n// changed\n");
  fs.utimesSync(binary, new Date(), new Date(Date.now() + 5_000));
  assert.deepEqual(await probe.probeClaude(binary), answer);
  assert.equal(readStubLog(stubLog).filter((entry) => entry.kind === "probe").length, 2, "a changed binary is probed again");
});

test("a claude that never answers is an error after the deadline, never a throw", async () => {
  const deaf = installScript("deaf-bin", "#!/usr/bin/env node\nprocess.stdin.resume();\nsetInterval(() => {}, 1000);\n");
  const started = Date.now();
  const answer = await probe.probeClaude(deaf, { timeoutMs: 400 });
  assert.ok(Date.now() - started >= 400, "it waited for the deadline");
  assert.equal(typeof answer.error, "string");
  assert.match(answer.error, /initialize/);
  assert.equal(probe.CLAUDE_PROBE_DEADLINE_MS, 15_000);
  const failing = installScript("fail-bin", "#!/bin/sh\necho 'not logged in' >&2\nexit 3\n");
  const failed = await probe.probeClaude(failing, { timeoutMs: 5_000 });
  assert.match(failed.error, /exit|code 3/);
  const refused = installScript("refuse-bin", "#!/usr/bin/env node\nprocess.stdin.on('data', (d) => { const r = JSON.parse(String(d)); process.stdout.write(JSON.stringify({ type: 'control_response', response: { subtype: 'error', request_id: r.request_id, error: 'nope' } }) + '\\n'); });\n");
  assert.deepEqual(await probe.probeClaude(refused, { timeoutMs: 5_000 }), { error: "nope" });
  const missing = await probe.probeClaude(path.join(scratch, "nowhere", "claude"), { timeoutMs: 5_000 });
  assert.equal(typeof missing.error, "string");
  assert.ok(missing.error.length > 0);
});

test("a version outside CLAUDE_RECORDED_VERSIONS is flagged as not covered", async () => {
  for (const version of shared.CLAUDE_RECORDED_VERSIONS) assert.ok(shared.isClaudeRecordedVersion(version), version);
  for (const value of ["9.9.9", "2.1.321", "2.1.3220", "", null, undefined, 2]) assert.ok(!shared.isClaudeRecordedVersion(value), String(value));
  assert.equal(probe.claudeInitializeInfo({ ...recordedAnswer(), claude_code_version: "9.9.9" }).recorded, false);
  const binary = installStub("newer-bin");
  process.env.PI_TEST_CLAUDE_STUB_VERSION = "9.9.9";
  try {
    const answer = await probe.probeClaude(binary);
    assert.equal(answer.version, "9.9.9");
    assert.equal(answer.recorded, false);
    assert.deepEqual(answer.models, ALIASES, "the rest of the answer is the recording's");
  } finally {
    delete process.env.PI_TEST_CLAUDE_STUB_VERSION;
  }
});

// (owner ruling Q1=A): the three claude features Pi Web relies on are judged
// from Pi Web's own evidence, never from claude's capability names or `claude --help`.
test("Pi Web's claude features: approvals are unavailable until host prompts; background tasks and subagent text are available from the recorded 2.1.322, unavailable below it, unknown without a version, and CLAUDE_CODE_DISABLE_BACKGROUND_TASKS turns background tasks off", () => {
  // r3 (owner ruling 2026-11-13T07:40:00Z): host prompts are the
  // approval mode, the third argument — ask is --permission-prompts host, deny is none.
  const features = (version, env = {}, approvalMode = "deny") => Object.fromEntries(probe.claudePiWebFeatures(version, env, approvalMode).map((feature) => [feature.id, [feature.available, feature.basis]]));
  assert.deepEqual(probe.claudePiWebFeatures("2.1.322", {}, "ask").map((feature) => feature.id), ["approvals", "backgroundTasks", "subagentText"]);
  assert.equal("CLAUDE_PERMISSION_PROMPTS" in shared, false, "'s permission mode is the one source of --permission-prompts");
  assert.deepEqual(features("2.1.322", {}, "ask"), {
    approvals: [true, "host-prompts"],
    backgroundTasks: [true, "recorded"],
    subagentText: [true, "recorded"],
  });
  assert.deepEqual(features("2.1.322"), {
    approvals: [false, "no-host-prompts"],
    backgroundTasks: [true, "recorded"],
    subagentText: [true, "recorded"],
  });
  // A newer claude than the recordings: by the threshold (the fixtures' notice is u4's versionRecorded).
  assert.deepEqual(features("2.2.0"), { approvals: [false, "no-host-prompts"], backgroundTasks: [true, "recorded"], subagentText: [true, "recorded"] });
  for (const below of ["2.1.319", "2.1.312", "1.9.999"]) {
    assert.deepEqual(features(below), { approvals: [false, "no-host-prompts"], backgroundTasks: [false, "below-recorded"], subagentText: [false, "below-recorded"] }, below);
  }
  for (const unknown of [null, "", "not-a-version", "2.1.x"]) {
    assert.deepEqual(features(unknown), { approvals: [false, "no-host-prompts"], backgroundTasks: [null, "version-unknown"], subagentText: [null, "version-unknown"] }, String(unknown));
  }
  // claude 2.1.322 reads the variable as true for 1 / true / yes / on (any case, trimmed).
  for (const value of ["1", "true", "TRUE", " yes ", "on"]) {
    assert.deepEqual(features("2.1.322", { CLAUDE_CODE_DISABLE_BACKGROUND_TASKS: value }).backgroundTasks, [false, "env-disabled"], value);
    assert.deepEqual(features("2.1.322", { CLAUDE_CODE_DISABLE_BACKGROUND_TASKS: value }).subagentText, [true, "recorded"], value);
  }
  for (const value of ["", "0", "false", "no", "off", "2"]) {
    assert.deepEqual(features("2.1.322", { CLAUDE_CODE_DISABLE_BACKGROUND_TASKS: value }).backgroundTasks, [true, "recorded"], value);
  }
  // Without a version the variable changes nothing: background tasks stay unknown.
  assert.deepEqual(features(null, { CLAUDE_CODE_DISABLE_BACKGROUND_TASKS: "1" }).backgroundTasks, [null, "version-unknown"]);
});

// r3 (plan D12): approvals are available exactly when the global
// permission mode is ask (claude's approval requests come to Pi Web's dialog); #442 gates
// on no claude version, so neither does this.
test("approvals follow's permission mode, not the claude version: ask is available and deny is not, for a recorded, an unrecorded and an unknown version ( r3)", () => {
  const approvals = (version, approvalMode) => {
    const feature = probe.claudePiWebFeatures(version, {}, approvalMode).find((entry) => entry.id === "approvals");
    return [feature.available, feature.basis];
  };
  for (const version of ["2.1.322", "2.1.312", "9.9.9", "1.0.0", null]) {
    assert.deepEqual(approvals(version, "ask"), [true, "host-prompts"], `ask, ${version}`);
    assert.deepEqual(approvals(version, "deny"), [false, "no-host-prompts"], `deny, ${version}`);
  }
});

// r3: the probe ends claude the way claude ends — by closing its stdin — and
// signals only a child that outlives the grace period (the stub logs every signal).
test("a probe ends an answering claude by closing its stdin, signalling it only when it outlives the grace period ( r3)", async () => {
  const answerThen = (onEnd) => `#!/usr/bin/env node
const fs = require("node:fs");
const log = (kind) => fs.appendFileSync(process.argv[1] + ".log", kind + "\\n");
process.on("SIGTERM", () => { log("SIGTERM"); process.exit(143); });
process.stdin.on("data", (d) => { const r = JSON.parse(String(d)); process.stdout.write(JSON.stringify({ type: "control_response", response: { subtype: "success", request_id: r.request_id, response: { models: [] } } }) + "\\n"); });
process.stdin.on("end", () => { log("end"); ${onEnd} });
`;
  const settled = async (file, want) => {
    const deadline = Date.now() + 10_000;
    for (;;) {
      const lines = fs.existsSync(file) ? fs.readFileSync(file, "utf8").split("\n").filter(Boolean) : [];
      if (want(lines) || Date.now() > deadline) return lines;
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  };
  const polite = installScript("polite-bin", answerThen("process.exit(0);"));
  assert.equal(typeof (await probe.probeClaude(polite, { timeoutMs: 5_000 })).error, "undefined");
  const politeLog = await settled(`${polite}.log`, (lines) => lines.includes("end"));
  await new Promise((resolve) => setTimeout(resolve, 300));
  assert.deepEqual(fs.readFileSync(`${polite}.log`, "utf8").split("\n").filter(Boolean), ["end"], `claude saw its stdin close and no signal (${politeLog})`);

  const stubborn = installScript("stubborn-bin", answerThen("setInterval(() => {}, 1000);"));
  assert.equal(typeof (await probe.probeClaude(stubborn, { timeoutMs: 5_000 })).error, "undefined");
  assert.deepEqual(await settled(`${stubborn}.log`, (lines) => lines.includes("SIGTERM")), ["end", "SIGTERM"], "a child that outlives the grace period is signalled");
});
