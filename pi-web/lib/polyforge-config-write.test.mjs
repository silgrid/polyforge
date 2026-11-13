import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);

/** A tiny stub `pf` first on PATH: it answers `--version`, `config list`,
 *  `config workspace --cwd`, `ops status` and records every set/unset
 *  argv/stdin, so the write lib's real-old-value and CAS-hash behavior can be
 *  asserted without a machine pf. */
function stubTree({ workspaceYaml = true, version = "v2.20.6", writeCapable = true } = {}) {
  const work = mkdtempSync(join(tmpdir(), "pf-write-"));
  const bin = join(work, "bin");
  const agent = join(work, "agent");
  const ws = join(work, "ws");
  mkdirSync(bin, { recursive: true });
  mkdirSync(agent, { recursive: true });
  mkdirSync(ws, { recursive: true });
  const cliToml = join(work, "cli.toml");
  const yaml = join(ws, ".polyforge.yaml");
  writeFileSync(cliToml, "server = \"x\"\napi_key = \"current\"\n[daemon]\nmax_concurrent_jobs = 6\n");
  if (workspaceYaml) writeFileSync(yaml, "steps:\n  alpha:\n    grill: { models: [\"pi:before\"] }\n");
  const argvLog = join(bin, "argv.log");
  writeFileSync(argvLog, "");
  writeFileSync(join(bin, "state.json"), JSON.stringify({ cliMax: 6, cliInterval: 30 }));
  const sha = (path) => createHash("sha256").update(readFileSync(path)).digest("hex");
  writeFileSync(join(bin, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path"); const crypto = require("crypto");
const dir = __dirname;
const state = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const args = process.argv.slice(2);
let stdin = ""; try { stdin = fs.readFileSync(0, "utf8"); } catch {}
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify({ args, stdin }) + "\\n");
const sha = (p) => crypto.createHash("sha256").update(fs.readFileSync(p)).digest("hex");
const rest = args.filter((a) => a !== "--json");
if (args.includes("--version")) { process.stdout.write(${JSON.stringify(version)} + "\\n"); process.exit(0); }
if (rest[0] === "workflows") { process.stdout.write(JSON.stringify({ daemon_reachable: true, write_capable: ${JSON.stringify(writeCapable)}, write_reason: ${JSON.stringify(writeCapable ? "" : "the daemon reports no workflows dir, so the step keys cannot be checked")}, bundles: [], bundle_errors: [] }) + "\\n"); process.exit(0); }
if (rest[0] === "config" && rest[1] === "list") {
  process.stdout.write(JSON.stringify({ file: { path: ${JSON.stringify(cliToml)}, sha256: sha(${JSON.stringify(cliToml)}), exists: true }, items: [
    { key: "max_concurrent_jobs", value: String(state.cliMax), source: "file", class: "live", schema: { kind: "int", min: 1 } },
    { key: "api_key", set: true, source: "file", class: "restart-only" },
    { key: "summarize.models", value: ["pi:a"], source: "file", class: "live", schema: { kind: "model-list" } },
    { key: "summarize.interval_sec", value: state.cliInterval, source: state.cliInterval === 60 ? "default" : "file", class: "live", schema: { kind: "int", min: 10, max: 3600 } },
  ] }) + "\\n"); process.exit(0);
}
if (rest[0] === "config" && rest[1] === "workspace") {
  const yamlExists = fs.existsSync(${JSON.stringify(yaml)});
  process.stdout.write(JSON.stringify({ mapped: true, workspace: ${JSON.stringify(ws)}, yaml: { path: ${JSON.stringify(yaml)}, sha256: yamlExists ? sha(${JSON.stringify(yaml)}) : null, exists: yamlExists }, steps: { alpha: {
    grill: { declared: true, models: ["pi:before"], source: "file", class: "live" },
    plan: { declared: false, models: null, source: "default", class: "live" },
    merge: { declared: false, models: null, source: "default", class: "live", skip: true },
  } }, steps_complete: true }) + "\\n"); process.exit(0);
}
if (rest[0] === "ops" && rest[1] === "status") { process.stdout.write(JSON.stringify({ queue: { max_concurrent: state.cliMax } }) + "\\n"); process.exit(0); }
if (rest[0] === "config" && (rest[1] === "set" || rest[1] === "unset")) {
  if (rest[2] === "max_concurrent_jobs") { state.cliMax = Number(rest[3]); fs.writeFileSync(path.join(dir, "state.json"), JSON.stringify(state)); }
  if (rest[2] === "summarize.interval_sec") { state.cliInterval = rest[1] === "set" ? Number(rest[3]) : 60; fs.writeFileSync(path.join(dir, "state.json"), JSON.stringify(state)); }
  process.stdout.write(JSON.stringify({ outcome: "applied" }) + "\\n"); process.exit(0);
}
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
  chmodSync(join(bin, "pf"), 0o755);
  return { work, bin, agent, cliToml, ws, yaml, argvLog, sha };
}

function withPath(tree, t) {
  const oldPath = process.env.PATH;
  const oldAgent = process.env.PI_CODING_AGENT_DIR;
  process.env.PATH = `${tree.bin}${delimiter}${oldPath}`;
  process.env.PI_CODING_AGENT_DIR = tree.agent;
  t.after(() => {
    process.env.PATH = oldPath;
    if (oldAgent === undefined) delete process.env.PI_CODING_AGENT_DIR;
    else process.env.PI_CODING_AGENT_DIR = oldAgent;
    rmSync(tree.work, { recursive: true, force: true });
  });
}

const calls = (tree) => readFileSync(tree.argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
const reset = (tree) => writeFileSync(tree.argvLog, "");

test("a cli write records the file's real old value, not the previous history value", async (t) => {
  const tree = stubTree();
  withPath(tree, t);
  const lib = await jiti.import("./polyforge-config-write.ts");

  const result = await lib.writePolyforgeConfig({
    action: "set",
    key: "max_concurrent_jobs",
    value: 7,
    cwd: tree.ws,
    expect_state: { file_sha256: tree.sha(tree.cliToml), values: { max_concurrent_jobs: "6", api_key: { set: true } } },
  });
  assert.equal(result.entry.old, "6", "old must be the file's real value");
  assert.equal(result.entry.new, 7);
  assert.equal(result.entry.cwd, "", "cli entries are machine-scoped");
  const setCall = calls(tree).find((call) => call.args.includes("set"));
  assert.ok(setCall, "the write reached pf");
  assert.ok(setCall.args.includes("--expect-state"), "the read snapshot reaches pf");
});

// summarize.* is a flat cli.toml key — no workspace, the
// cli.toml CAS, the real old value from `pf config list`.
test("a summarize write is a cli.toml write: no --cwd, the cli.toml hash, the list's old value, and a rollback the same way", async (t) => {
  const tree = stubTree({ version: "v2.20.21" });
  withPath(tree, t);
  const lib = await jiti.import("./polyforge-config-write.ts");

  const write = await lib.writePolyforgeConfig({
    action: "set",
    key: "summarize.interval_sec",
    value: 40,
    cwd: tree.ws,
    expect_state: { file_sha256: tree.sha(tree.cliToml), values: { "summarize.interval_sec": 30 } },
  });
  assert.equal(write.entry.old, 30, "cli.toml's real old value");
  assert.equal(write.entry.scope, "cli", "a summarize entry is a cli.toml entry");
  assert.equal(write.entry.cwd, "", "a cli.toml entry keys by no workspace");
  const setCall = calls(tree).find((call) => call.args.includes("set"));
  assert.deepEqual(setCall.args.filter((arg) => arg !== "--json").slice(0, 4), ["config", "set", "summarize.interval_sec", "40"]);
  assert.ok(!setCall.args.includes("--cwd") && !setCall.args.includes("--workspace"), "summarize writes carry no path flag");
  assert.equal(JSON.parse(setCall.args[setCall.args.indexOf("--expect-state") + 1]).file_sha256, tree.sha(tree.cliToml), "the cli.toml CAS reaches pf");
  assert.ok(!calls(tree).some((call) => call.args.includes("workspace")), "no config workspace read for a summarize write");

  reset(tree);
  const rolled = await lib.rollbackPolyforgeConfig({ key: "summarize.interval_sec", old: 30, cwd: "/some/other/place", entry: write.entry });
  const rollbackCall = calls(tree).find((call) => call.args.includes("set"));
  assert.ok(rollbackCall && !rollbackCall.args.includes("--cwd"), "the rollback is the same cli.toml write");
  assert.equal(rollbackCall.args[rollbackCall.args.indexOf("summarize.interval_sec") + 1], "30");
  assert.equal(JSON.parse(rollbackCall.args[rollbackCall.args.indexOf("--expect-state") + 1]).file_sha256, tree.sha(tree.cliToml), "a rollback CASes on the cli.toml hash");
  assert.notEqual(rolled.entry.id, write.entry.id, "a rollback appends a new history entry");
  assert.equal(rolled.entry.old, 40, "the rollback's entry records the list's current value");
});

test("a summarize unset and a pre-#286 workspace-scoped history entry both roll through the flat cli.toml path", async (t) => {
  const tree = stubTree({ version: "v2.20.21" });
  withPath(tree, t);
  const lib = await jiti.import("./polyforge-config-write.ts");

  await lib.writePolyforgeConfig({ action: "unset", key: "summarize.interval_sec", cwd: tree.ws, expect_state: { file_sha256: tree.sha(tree.cliToml) } });
  const unsetCall = calls(tree).find((call) => call.args.includes("unset"));
  assert.ok(unsetCall, "the unset reached pf");
  assert.ok(!unsetCall.args.includes("--cwd"), "a summarize unset carries no --cwd");

  // An entry recorded before #286 (scope workspace, the yaml's path): the
  // scope comes from the key, so it rolls back as a cli.toml write.
  reset(tree);
  const old = { id: "pre-286", at: "2026-11-01T00:00:00.000Z", scope: "workspace", cwd: tree.ws, key: "summarize.interval_sec", old: 90, new: 30, secret: false, rollbackable: true };
  const rolled = await lib.rollbackPolyforgeConfig({ key: old.key, old: old.old, entry: old });
  const setCall = calls(tree).find((call) => call.args.includes("set"));
  assert.ok(setCall && !setCall.args.includes("--cwd"), "the pre-#286 rollback is a flat cli.toml set");
  assert.ok(!calls(tree).some((call) => call.args.includes("workspace")), "no config workspace read for the rollback");
  assert.equal(rolled.entry.scope, "cli");
  assert.equal(rolled.entry.new, 90);

  // An entry with no old value rolls back to `unset`, not an error.
  reset(tree);
  await lib.rollbackPolyforgeConfig({ key: old.key, old: null, entry: { ...old, old: null } });
  const rollbackUnset = calls(tree).find((call) => call.args.includes("unset"));
  assert.ok(rollbackUnset && !rollbackUnset.args.includes("--cwd"), "a null old value rolls back to a flat unset");
});

test("below the summarize floor — 2.20.20, a missing or an unparseable version — a summarize write is refused 400 before any write spawn", async (t) => {
  for (const version of ["v2.20.20", "not-a-version", ""]) {
    const tree = stubTree({ version });
    withPath(tree, t);
    const lib = await jiti.import("./polyforge-config-write.ts");
    const found = version === "v2.20.20" ? /found v2\.20\.20/ : /found an unparseable version/;
    await assert.rejects(
      lib.writePolyforgeConfig({ action: "set", key: "summarize.language", value: "English", expect_state: { file_sha256: tree.sha(tree.cliToml), values: {} } }),
      (error) => error.status === 400 && /summarize settings need pf 2\.20\.21 or newer/.test(error.message) && found.test(error.message),
      `version ${JSON.stringify(version)}`,
    );
    assert.ok(!calls(tree).some((call) => call.args.includes("set") || call.args.includes("workspace")), "no write and no workspace read reached pf");
    process.env.PATH = process.env.PATH.split(delimiter).filter((entry) => entry !== tree.bin).join(delimiter);
  }
});

test("memory_policy is never a writable key", async (t) => {
  const tree = stubTree({ version: "v2.20.21" });
  withPath(tree, t);
  const lib = await jiti.import("./polyforge-config-write.ts");
  for (const key of ["memory_policy", "memory_policy.default.record", "memory_policy.alpha.skip"]) {
    assert.equal(lib.isPanelWritableKey(key), false, key);
    assert.equal(lib.WRITABLE_KEYS.has(key), false, key);
  }
  await assert.rejects(lib.writePolyforgeConfig({ action: "set", key: "memory_policy.default.record", value: ["x"] }), /is not writable from the panel/);
  assert.ok(!calls(tree).some((call) => call.args.includes("set")), "nothing reached pf config set");
});

test("a step write to a mapped workspace with no yaml reaches pf with an absent expectation", async (t) => {
  const tree = stubTree({ workspaceYaml: false, version: "v2.20.21" });
  withPath(tree, t);
  const lib = await jiti.import("./polyforge-config-write.ts");

  // The panel computes `file_sha256: hash ?? null`; with no yaml there is no
  // hash, so the expectation is an explicit JSON null meaning "absent" — not
  // an omitted field (which pf reads as "no hash check").
  await lib.writePolyforgeConfig({
    action: "set",
    key: "steps.alpha.plan.models",
    value: ["pi:a", "pi:b"],
    cwd: tree.ws,
    expect_state: { file_sha256: null, values: {} },
  });
  const setCall = calls(tree).find((call) => call.args.includes("set"));
  assert.ok(setCall, "the write reached pf");
  assert.equal(setCall.args[setCall.args.indexOf("--cwd") + 1], tree.ws);
  const idx = setCall.args.indexOf("--expect-state");
  assert.ok(idx >= 0, "the absent expectation reaches pf");
  const state = JSON.parse(setCall.args[idx + 1]);
  assert.ok(Object.prototype.hasOwnProperty.call(state, "file_sha256"), "file_sha256 must be present, not omitted");
  assert.equal(state.file_sha256, null, "a yaml-less workspace sends an explicit-null (absent) expectation");

  // A rollback on a still-absent yaml computes the same absent expectation.
  reset(tree);
  await lib.rollbackPolyforgeConfig({
    key: "steps.alpha.plan.models",
    old: ["pi:a"],
    entry: { id: "w22-absent", at: new Date().toISOString(), scope: "workspace", cwd: tree.ws, key: "steps.alpha.plan.models", old: null, new: ["pi:a", "pi:b"], secret: false, rollbackable: true },
  });
  const rollbackCall = calls(tree).find((call) => call.args.includes("set"));
  assert.ok(rollbackCall, "the rollback reached pf");
  assert.equal(rollbackCall.args[rollbackCall.args.indexOf("--cwd") + 1], tree.ws);
  const rollbackIdx = rollbackCall.args.indexOf("--expect-state");
  assert.ok(rollbackIdx >= 0, "the rollback's absent expectation reaches pf");
  const rollbackState = JSON.parse(rollbackCall.args[rollbackIdx + 1]);
  assert.ok(Object.prototype.hasOwnProperty.call(rollbackState, "file_sha256"), "the rollback's file_sha256 must be present, not omitted");
  assert.equal(rollbackState.file_sha256, null, "the rollback of a yaml-less workspace is absent, not unchecked");
});

// the step-chain keys ride the same write path.
test("a step chain write reaches pf as a JSON list on the workspace, with the yaml CAS and the real old value", async (t) => {
  const tree = stubTree({ version: "v2.20.16" });
  withPath(tree, t);
  const lib = await jiti.import("./polyforge-config-write.ts");
  const result = await lib.writePolyforgeConfig({
    action: "set",
    key: "steps.alpha.grill.models",
    value: ["claude:x", "pi:y"],
    cwd: tree.ws,
    expect_state: { file_sha256: tree.sha(tree.yaml), values: {} },
  });
  assert.deepEqual(result.entry.old, ["pi:before"], "old is the declared chain read through pf");
  assert.equal(result.entry.scope, "workspace");
  assert.equal(result.entry.cwd, tree.ws);
  assert.equal(result.message, "applied", "a live key is applied");
  const setCall = calls(tree).find((call) => call.args.includes("set"));
  assert.deepEqual(setCall.args.slice(0, 5), ["--json", "config", "set", "steps.alpha.grill.models", JSON.stringify(["claude:x", "pi:y"])]);
  assert.equal(setCall.args[setCall.args.indexOf("--cwd") + 1], tree.ws);
  assert.equal(JSON.parse(setCall.args[setCall.args.indexOf("--expect-state") + 1]).file_sha256, tree.sha(tree.yaml));
  assert.ok(calls(tree).some((call) => call.args.includes("workflows")), "the daemon capability was read");
});

test("a step chain clear is an unset, and the history rolls a chain write back", async (t) => {
  const tree = stubTree({ version: "v2.20.16" });
  withPath(tree, t);
  const lib = await jiti.import("./polyforge-config-write.ts");
  const write = await lib.writePolyforgeConfig({ action: "set", key: "steps.alpha.grill.models", value: ["pi:x"], cwd: tree.ws });
  assert.deepEqual(write.entry.old, ["pi:before"]);
  reset(tree);
  await lib.writePolyforgeConfig({ action: "unset", key: "steps.alpha.grill.models", cwd: tree.ws });
  assert.ok(calls(tree).some((call) => call.args.join(" ").includes("unset steps.alpha.grill.models")));
  reset(tree);
  await lib.rollbackPolyforgeConfig({ key: write.entry.key, entry: write.entry });
  const rollback = calls(tree).find((call) => call.args.join(" ").includes(`set steps.alpha.grill.models ${JSON.stringify(["pi:before"])}`));
  assert.ok(rollback, "rollback re-sets the old chain");
  assert.equal(rollback.args[rollback.args.indexOf("--cwd") + 1], tree.ws, "rollback writes the entry's own workspace");
});

test("step writes are refused before any write spawn: bad shape, a daemon that cannot check the step keys, a key off the allowlist", async (t) => {
  const tree = stubTree({ version: "v2.20.16", writeCapable: false });
  withPath(tree, t);
  const lib = await jiti.import("./polyforge-config-write.ts");
  const noWrite = () => !calls(tree).some((call) => call.args.includes("set") || call.args.includes("unset"));
  await assert.rejects(lib.writePolyforgeConfig({ action: "set", key: "steps.alpha.grill.attrs.question_batch_size", value: 3, cwd: tree.ws }), /not writable/);
  await assert.rejects(lib.writePolyforgeConfig({ action: "set", key: "steps.alpha.grill.models", value: "pi:a", cwd: tree.ws }), /at least one model candidate/);
  await assert.rejects(lib.writePolyforgeConfig({ action: "set", key: "steps.alpha.grill.models", value: ["pi:a"], cwd: tree.ws }), /no workflows dir/);
  await assert.rejects(lib.writePolyforgeConfig({ action: "set", key: "steps.alpha.grill.budget", value: 1, cwd: tree.ws }), /not writable/);
  await assert.rejects(lib.writePolyforgeConfig({ action: "set", key: "steps.alpha.grill.models", value: ["pi:a"] }), /needs cwd/);
  assert.ok(noWrite(), "no refusal reached a pf write");
  assert.equal(lib.isPanelWritableKey("steps.alpha.grill.models"), true);
  assert.equal(lib.isPanelWritableKey("steps.alpha.default.models"), true);
  assert.equal(lib.isPanelWritableKey("steps.alpha.grill.attrs.question_batch_size"), false, "step attrs are gone");
  assert.equal(lib.isPanelWritableKey("steps.alpha.models"), false);
});

test("below the card floor a step write names 2.20.15", async (t) => {
  const tree = stubTree({ version: "v2.20.14" });
  withPath(tree, t);
  const lib = await jiti.import("./polyforge-config-write.ts");
  await assert.rejects(lib.writePolyforgeConfig({ action: "set", key: "steps.alpha.grill.models", value: ["pi:a"], cwd: tree.ws }), /2\.20\.15 or newer/);
});

// the nine non-secret cli.toml keys ride the same write path.
test("every non-secret cli.toml key reaches pf as config set <key> <value>, empty string keys are refused first", async (t) => {
  const tree = stubTree();
  withPath(tree, t);
  const lib = await jiti.import("./polyforge-config-write.ts");
  const values = {
    daemon_socket: "/run/pf.sock", user_id: "u_1", claude_bin: "/usr/bin/claude",
    harness_root: "/opt/pf", operator_agent_dir: "/home/op/.pi", step_timeout: "45m", stall_window: "10m", max_concurrent_jobs: 9,
  };
  for (const [key, value] of Object.entries(values)) {
    assert.equal(lib.isPanelWritableKey(key), true, `${key} is on the allowlist`);
    reset(tree);
    await lib.writePolyforgeConfig({ action: "set", key, value, expect_state: { file_sha256: tree.sha(tree.cliToml), values: {} } });
    const setCall = calls(tree).find((call) => call.args.includes("set"));
    assert.deepEqual(setCall.args.slice(0, 5), ["--json", "config", "set", key, String(value)], key);
    assert.ok(!setCall.args.includes("--cwd"), `${key} is a cli.toml key: no workspace cwd`);
  }
  // a Cell's mapping rows are written from its 工作区 card
  // (whole [workspace."<abs path>"] sections); anything else shaped like one stays off the allowlist
  assert.equal(lib.isPanelWritableKey("[workspace.\"/x\"]"), true, "a mapping section is writable");
  assert.equal(lib.isPanelWritableKey("[workspace.\"relative/x\"]"), false, "a relative mapping path is not");
  assert.equal(lib.isPanelWritableKey("[workspace.\"/x\\\"y\"]"), false, "a quoted mapping path is not");
  reset(tree);
  for (const key of ["daemon_socket", "user_id", "claude_bin", "harness_root", "operator_agent_dir"]) {
    await assert.rejects(lib.writePolyforgeConfig({ action: "set", key, value: "  " }), /must be a non-empty string/, key);
  }
  // server is a Cell's identity — never written in place (a
  // different AIHub or user is a new profile), refused before any pf call
  assert.equal(lib.isPanelWritableKey("server"), false);
  await assert.rejects(lib.writePolyforgeConfig({ action: "set", key: "server", value: "http://127.0.0.1:9911" }), /new profile/);
  for (const scope of [{ profile: "alpha" }, { profile: "default" }]) {
    await assert.rejects(lib.writePolyforgeConfig({ action: "set", key: "server", value: "http://x", ...scope }), /new profile/);
  }
  assert.ok(!calls(tree).some((call) => call.args.includes("set")), "an empty value or an identity key never reached pf");
});

test("a restart-only key rollback (daemon_socket — server is an identity key, never written by the panel) is a fresh read + the same CAS write: it records a new entry and restores the old value", async (t) => {
  const tree = stubTree();
  withPath(tree, t);
  // The stub's list reports daemon_socket from state, and a set advances it.
  const pf = readFileSync(join(tree.bin, "pf"), "utf8")
    .replace('{ key: "api_key", set: true, source: "file", class: "restart-only" },', '{ key: "api_key", set: true, source: "file", class: "restart-only" },\n    { key: "daemon_socket", value: state.daemon_socket ?? "/old.sock", source: "file", class: "restart-only" },')
    .replace('if (rest[2] === "max_concurrent_jobs")', 'if (rest[2] === "daemon_socket" && rest[1] === "set") { state.daemon_socket = rest[3]; fs.writeFileSync(path.join(dir, "state.json"), JSON.stringify(state)); }\n  if (rest[2] === "max_concurrent_jobs")');
  writeFileSync(join(tree.bin, "pf"), pf);
  const lib = await jiti.import("./polyforge-config-write.ts");
  const write = await lib.writePolyforgeConfig({ action: "set", key: "daemon_socket", value: "/new.sock", expect_state: { file_sha256: tree.sha(tree.cliToml), values: {} } });
  assert.equal(write.entry.old, "/old.sock");
  assert.equal(write.entry.rollbackable, true);
  assert.equal(write.message, "saved; the value applies after a daemon restart", "a restart-only key never claims to be applied");
  reset(tree);
  const rolled = await lib.rollbackPolyforgeConfig({ id: write.entry.id, key: "daemon_socket", old: write.entry.old, entry: write.entry });
  const setCall = calls(tree).find((call) => call.args.includes("set"));
  assert.deepEqual(setCall.args.slice(0, 5), ["--json", "config", "set", "daemon_socket", "/old.sock"]);
  assert.ok(setCall.args.includes("--expect-state"), "the rollback carries the CAS expectation");
  assert.notEqual(rolled.entry.id, write.entry.id, "a rollback is a new history entry");
  assert.equal(rolled.entry.old, "/new.sock");
  assert.equal(rolled.entry.new, "/old.sock");
  assert.ok(!calls(tree).some((call) => /reload|shutdown|ensure/.test(call.args.join(" "))), "no daemon reload/shutdown/ensure");
});

test(" (review s17#2): a scoped region write records its file in the history entry and its rollback writes that same file back (--host / --profile)", async (t) => {
  const tree = stubTree();
  withPath(tree, t);
  const pf = readFileSync(join(tree.bin, "pf"), "utf8")
    .replace('const rest = args.filter((a) => a !== "--json");', 'const rest = args.filter((a, i) => a !== "--json" && a !== "--profile" && args[i - 1] !== "--profile");')
    .replace('if (rest[0] === "workflows")', 'if (rest[0] === "version") { process.stdout.write(JSON.stringify({ version: "v9", capabilities: ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"] }) + "\\n"); process.exit(0); }\nif (rest[0] === "workflows")')
    .replace('{ key: "api_key", set: true, source: "file", class: "restart-only" },', '{ key: "api_key", set: true, source: "file", class: "restart-only" },\n    { key: "daemon_socket", value: "/old.sock", source: "file", class: "restart-only" },\n    { key: "user_id", value: "u_old", source: "file", class: "cli-only" },');
  writeFileSync(join(tree.bin, "pf"), pf);
  const lib = await jiti.import("./polyforge-config-write.ts");
  const strip = (args) => args.filter((a) => a !== "--expect-state" && !a.startsWith("{"));

  const host = await lib.writePolyforgeConfig({ action: "set", key: "daemon_socket", value: "/new.sock", host: true, expect_state: { file_sha256: null } });
  assert.equal(host.entry.host, true, "the Host write records its scope");
  assert.equal(host.entry.profile, undefined);
  reset(tree);
  await lib.rollbackPolyforgeConfig({ id: host.entry.id, key: "daemon_socket", old: host.entry.old, entry: host.entry });
  const hostCalls = calls(tree);
  const hostRead = hostCalls.find((call) => call.args.includes("list"));
  assert.ok(hostRead.args.includes("--host"), "the rollback reads the Host file");
  const hostSet = hostCalls.find((call) => call.args.includes("set"));
  assert.deepEqual(strip(hostSet.args), ["--json", "config", "set", "daemon_socket", "--host", "/old.sock"], "the rollback writes the Host file back");
  assert.equal(JSON.parse(hostSet.args[hostSet.args.indexOf("--expect-state") + 1]).file_sha256, tree.sha(tree.cliToml), "a fresh CAS read");

  reset(tree);
  const cell = await lib.writePolyforgeConfig({ action: "set", key: "user_id", value: "u_new", profile: "alpha", expect_state: { file_sha256: null } });
  assert.equal(cell.entry.profile, "alpha", "a Cell write records its profile");
  reset(tree);
  const rolled = await lib.rollbackPolyforgeConfig({ id: cell.entry.id, key: "user_id", old: cell.entry.old, entry: cell.entry });
  const cellSet = calls(tree).find((call) => call.args.includes("set"));
  assert.deepEqual(strip(cellSet.args), ["--profile", "alpha", "--json", "config", "set", "user_id", "u_old"], "the rollback writes alpha's file back, never the CLI-resolved one");
  assert.equal(rolled.entry.profile, "alpha", "the rollback's own entry keeps the scope");
});

// a project's skip switch of a skippable step is a workspace
// step key, written through the same pf config set|unset path as a chain.
test(" the skip switch writes through pf config set", async (t) => {
  const tree = stubTree({ version: "v2.20.16" });
  withPath(tree, t);
  const lib = await jiti.import("./polyforge-config-write.ts");
  const schema = await jiti.import("./polyforge-config-schema.ts");

  assert.equal(lib.isPanelWritableKey("steps.alpha.merge.skip"), true);
  assert.deepEqual(schema.parseStepWriteKey("steps.alpha.merge.skip"), { project: "alpha", step: "merge" });
  for (const bad of ["steps.alpha.merge.attrs", "steps.alpha.merge.budget", "steps.alpha.skip", "steps.alpha.merge.skip.x"]) {
    assert.equal(schema.parseStepWriteKey(bad), null, bad);
    assert.equal(lib.isPanelWritableKey(bad), false, bad);
  }
  assert.equal(schema.stepValueError("steps.alpha.merge.skip", true), null);
  assert.equal(schema.stepValueError("steps.alpha.merge.skip", false), null);
  for (const value of ["true", 1, ["pi:a"], null]) {
    assert.ok(schema.stepValueError("steps.alpha.merge.skip", value), `a non-boolean skip ${JSON.stringify(value)} is refused`);
  }
  assert.ok(schema.stepValueError("steps.alpha.merge.models", true), "a models key still takes a list");

  const result = await lib.writePolyforgeConfig({
    action: "set",
    key: "steps.alpha.merge.skip",
    value: true,
    cwd: tree.ws,
    expect_state: { file_sha256: tree.sha(tree.yaml), values: {} },
  });
  assert.equal(result.entry.scope, "workspace");
  assert.equal(result.entry.new, true);
  assert.equal(result.message, "saved; applies at the next claim", "the skip switch is fixed when a work item is claimed");
  const setCall = calls(tree).find((call) => call.args.includes("set"));
  assert.deepEqual(setCall.args.slice(0, 5), ["--json", "config", "set", "steps.alpha.merge.skip", "true"]);
  assert.equal(setCall.args[setCall.args.indexOf("--cwd") + 1], tree.ws);
  assert.equal(JSON.parse(setCall.args[setCall.args.indexOf("--expect-state") + 1]).file_sha256, tree.sha(tree.yaml));

  reset(tree);
  await lib.writePolyforgeConfig({ action: "set", key: "steps.alpha.merge.skip", value: false, cwd: tree.ws });
  assert.deepEqual(calls(tree).find((call) => call.args.includes("set")).args.slice(0, 5), ["--json", "config", "set", "steps.alpha.merge.skip", "false"]);

  reset(tree);
  await lib.writePolyforgeConfig({ action: "unset", key: "steps.alpha.merge.skip", cwd: tree.ws });
  const unsetCall = calls(tree).find((call) => call.args.includes("unset"));
  assert.deepEqual(unsetCall.args.slice(0, 4), ["--json", "config", "unset", "steps.alpha.merge.skip"]);
  assert.equal(unsetCall.args[unsetCall.args.indexOf("--cwd") + 1], tree.ws);

  reset(tree);
  await assert.rejects(lib.writePolyforgeConfig({ action: "set", key: "steps.alpha.merge.skip", value: "yes", cwd: tree.ws }), /skip/);
  assert.ok(!calls(tree).some((call) => call.args.includes("set")), "a non-boolean skip never reaches pf");
});

// r1#1: pf config workspace reports a step's skip, so an unset's
// history keeps the old value and its rollback sets it back.
test(" the skip switch's unset records the old value and rolls back to set", async (t) => {
  const tree = stubTree({ version: "v2.20.16" });
  withPath(tree, t);
  const lib = await jiti.import("./polyforge-config-write.ts");
  const unset = await lib.writePolyforgeConfig({ action: "unset", key: "steps.alpha.merge.skip", cwd: tree.ws });
  assert.equal(unset.entry.old, true, "the unset's history keeps the skip pf reported");
  reset(tree);
  await lib.rollbackPolyforgeConfig({ id: unset.entry.id, key: "steps.alpha.merge.skip", old: unset.entry.old, entry: unset.entry });
  const setCall = calls(tree).find((call) => call.args.includes("set"));
  assert.deepEqual(setCall.args.slice(0, 5), ["--json", "config", "set", "steps.alpha.merge.skip", "true"]);
});
