import "#test-env";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, test } from "node:test";
import { createJiti } from "jiti";

// Settings › Polyforge names the Cell of every pf config call.
// The stub below answers like pf: an unscoped Cell command resolves its Cell
// from `--profile`, then PF_PROFILE, then the cwd's mapped Cell, then
// `default`; `--host` is the Host scope; `pf ops status` is the Host
// aggregate unless `--profile` targets a Cell. This test process runs with
// PF_PROFILE=alpha and its cwd inside alpha's workspace — the server
// condition of the bug — and every spawn the server libraries make is
// recorded with its exact argv, stdin and the Cell the stub resolved.

const jiti = createJiti(import.meta.url);
const work = realpathSync(mkdtempSync(join(tmpdir(), "pf-scope-367-")));
const bin = join(work, "bin");
const agentDir = join(work, "agent");
const project = join(work, "project");
const alphaWs = join(work, "ws-alpha");
const inAlpha = join(alphaWs, "sub");
for (const dir of [bin, agentDir, project, inAlpha]) mkdirSync(dir, { recursive: true });
const stub = join(bin, "pf");
const stateFile = join(work, "state.json");
const log = join(work, "argv.log");
const MULTI = ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1", "cell-migration-v1"];
const KEY = "E2E-W367-UNIT-KEY-SENTINEL";

const freshState = (caps) => ({
  caps,
  files: {
    default: { sha: "sha-default", step_timeout: "30m0s", mapping: [project] },
    alpha: { sha: "sha-alpha", step_timeout: "45m0s", mapping: [alphaWs] },
  },
  writes: [],
});
const setState = (caps) => writeFileSync(stateFile, JSON.stringify(freshState(caps)));
const state = () => JSON.parse(readFileSync(stateFile, "utf8"));
const calls = () => readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
const argvs = () => calls().map((call) => call.args).filter((args) => args.join(" ") !== "--version");
const reset = (caps = MULTI) => { setState(caps); writeFileSync(log, ""); };

writeFileSync(stub, `#!${process.execPath}
const fs = require("fs");
const s = JSON.parse(fs.readFileSync(${JSON.stringify(stateFile)}, "utf8"));
const args = process.argv.slice(2);
const writesKey = args.includes("set") && args.includes("api_key");
let stdin = ""; if (writesKey) { try { stdin = fs.readFileSync(0, "utf8"); } catch {} }
const cwd = process.cwd();
const record = { args, stdin, cwd, env: process.env.PF_PROFILE ?? "" };
const out = (doc) => { fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(record) + "\\n"); process.stdout.write(JSON.stringify(doc)); process.exit(0); };
const refuse = (text) => { fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(record) + "\\n"); process.stderr.write(text); process.exit(2); };
if (args[0] === "--version") { fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(record) + "\\n"); process.stdout.write("v2.20.21\\n"); process.exit(0); }
// global flags come before the verb, as in pf
let i = 0; let flagProfile = "";
for (; i < args.length; i += 1) {
  if (args[i] === "--json") continue;
  if (args[i] === "--profile") { flagProfile = args[i + 1]; i += 1; continue; }
  break;
}
if (!s.caps && (flagProfile || args.includes("--host"))) refuse("flag provided but not defined: " + (flagProfile ? "-profile" : "-host"));
const verb = args.slice(i).filter((a) => a !== "--host");
const host = args.includes("--host");
const within = (dir) => cwd === dir || cwd.startsWith(dir + "/");
// pf's resolver: --profile, PF_PROFILE, the cwd's mapped Cell, default (a
// pf without the multi-Cell protocol has no resolver: cli.toml only)
const resolved = s.caps ? flagProfile || process.env.PF_PROFILE || Object.keys(s.files).find((p) => s.files[p].mapping.some(within)) || "default" : "default";
record.cell = host ? "host" : resolved;
const file = s.files[host ? "default" : resolved];
const opt = (name) => { const at = verb.indexOf(name); return at >= 0 ? verb[at + 1] : ""; };
if (verb[0] === "version") { if (!s.caps) refuse("pf: unknown command version"); out({ version: "v2.20.21", capabilities: s.caps }); }
if (verb[0] === "config" && verb[1] === "list") {
  if (host) out({ file: { sha256: "sha-host", exists: true }, items: [{ key: "max_concurrent_jobs", value: 6, source: "file", class: "live" }] });
  out({ file: { sha256: file.sha, exists: true }, items: [
    { key: "step_timeout", value: file.step_timeout, source: "file", class: "live" },
    { key: "api_key", set: true, source: "file", class: "restart-only" },
    ...file.mapping.map((ws) => ({ key: '[workspace."' + ws + '"]', value: "acme", source: "file", section: true, class: "next-claim" })),
  ] });
}
if (verb[0] === "config" && verb[1] === "workspace") {
  const ws = file.mapping.find((w) => opt("--cwd") === w || opt("--cwd").startsWith(w + "/"));
  if (!ws) out({ mapped: false, workspace: null, yaml: null, steps: null });
  out({ mapped: true, workspace: ws, yaml: { path: ws + "/.polyforge.yaml", sha256: "yaml-" + resolved, exists: true }, steps: { acme: { grill: { declared: true, models: ["pi:old"], source: "file", class: "live" } } }, steps_complete: true });
}
if (verb[0] === "config" && (verb[1] === "set" || verb[1] === "unset")) {
  s.writes.push({ cell: record.cell, key: verb[2], value: verb[2] === "api_key" ? "<stdin>" : verb[3] ?? null, cwd: opt("--cwd") });
  fs.writeFileSync(${JSON.stringify(stateFile)}, JSON.stringify(s));
  out({ outcome: "applied" });
}
if (verb[0] === "config" && verb[1] === "reload") out({ reloaded: [record.cell] });
// pf ops status: the Host aggregate unless --profile targets a Cell (PF_PROFILE and the cwd never move it)
if (verb[0] === "ops" && verb[1] === "status") { record.cell = flagProfile || "host"; out({ queue: { max_concurrent: 6 }, supervision: { step_timeout: s.files[flagProfile || "default"].step_timeout } }); }
if (verb[0] === "workflows") out({ workflows_dir: "/wf", daemon_reachable: true, reason: "", workspace: opt("--cwd"), projects: ["acme"], write_capable: true, write_reason: "", bundles: [], bundle_errors: [] });
refuse("pf: unknown command " + verb.join(" "));
`);
chmodSync(stub, 0o755);

const saved = { cwd: process.cwd(), PF_BIN: process.env.PF_BIN, PF_PROFILE: process.env.PF_PROFILE, PI_CODING_AGENT_DIR: process.env.PI_CODING_AGENT_DIR };
before(() => {
  process.env.PF_BIN = stub;
  process.env.PF_PROFILE = "alpha";
  process.env.PI_CODING_AGENT_DIR = agentDir;
  process.chdir(inAlpha);
});
after(() => {
  process.chdir(saved.cwd);
  for (const name of ["PF_BIN", "PF_PROFILE", "PI_CODING_AGENT_DIR"]) {
    if (saved[name] === undefined) delete process.env[name];
    else process.env[name] = saved[name];
  }
  rmSync(work, { recursive: true, force: true });
});

const pure = await jiti.import("./polyforge-cells-pure.ts");
const read = await jiti.import("./polyforge-config.ts");
const write = await jiti.import("./polyforge-config-write.ts");
const workflows = await jiti.import("./polyforge-workflows.ts");
const DEFAULT = () => pure.parsePolyforgeScope({});
const expectState = (args) => JSON.parse(args[args.indexOf("--expect-state") + 1]);
const withoutState = (args) => args.slice(0, args.includes("--expect-state") ? args.indexOf("--expect-state") : args.length);

test("the stub resolves an unscoped call to alpha here, so the assertions below discriminate", () => {
  reset();
  const unscoped = spawnSync(stub, ["--json", "config", "list"], { cwd: inAlpha, env: { ...process.env, PF_PROFILE: "" }, encoding: "utf8" });
  assert.equal(JSON.parse(unscoped.stdout).file.sha256, "sha-alpha", "the cwd alone resolves alpha");
  const byEnv = spawnSync(stub, ["--json", "config", "list"], { cwd: project, env: { ...process.env, PF_PROFILE: "alpha" }, encoding: "utf8" });
  assert.equal(JSON.parse(byEnv.stdout).file.sha256, "sha-alpha", "PF_PROFILE alone resolves alpha");
  const pinned = spawnSync(stub, ["--profile", "default", "--json", "config", "list"], { cwd: inAlpha, encoding: "utf8" });
  assert.equal(JSON.parse(pinned.stdout).file.sha256, "sha-default", "the flag wins over both");
  assert.equal(process.env.PF_PROFILE, "alpha");
  assert.equal(process.cwd(), inAlpha);
});

test("the default pane's read is the default Cell's, every config verb pinned, ops status the Host's", async () => {
  reset();
  const doc = await read.readPolyforgeConfig(project, DEFAULT());
  assert.equal(doc.capable, true, doc.reason);
  assert.equal(doc.file.sha256, "sha-default");
  assert.equal(doc.items.find((item) => item.key === "step_timeout").value, "30m0s", "the default Cell's value, not alpha's 45m0s");
  assert.equal(doc.mapped, true, "the workspace read matched the default Cell's mapping");
  assert.deepEqual(argvs(), [
    ["--json", "version"],
    ["--profile", "default", "--json", "config", "list"],
    ["--json", "ops", "status"],
    ["--profile", "default", "--json", "config", "workspace", "--cwd", project],
  ]);
  assert.ok(calls().every((call) => call.env === "alpha" && call.cwd === inAlpha), "every spawn ran with PF_PROFILE=alpha inside alpha's workspace");
  assert.deepEqual(calls().filter((call) => call.args.includes("config")).map((call) => call.cell), ["default", "default"]);
});

test("the Host and a named Cell keep their own scope; only a named Cell reads its own status", async () => {
  reset();
  const host = await read.readPolyforgeConfig("", { host: true });
  assert.equal(host.file.sha256, "sha-host");
  assert.deepEqual(argvs(), [["--json", "version"], ["--json", "config", "list", "--host"], ["--json", "ops", "status"]]);
  reset();
  const alpha = await read.readPolyforgeConfig("", { profile: "alpha" });
  assert.equal(alpha.items.find((item) => item.key === "step_timeout").daemon.value, "45m0s", "alpha's own live value");
  assert.deepEqual(argvs(), [["--json", "version"], ["--profile", "alpha", "--json", "config", "list"], ["--profile", "alpha", "--json", "ops", "status"]]);
});

test("an unscoped write lands in the default Cell's file with that file's old value and CAS hash", async () => {
  reset();
  const result = await write.writePolyforgeConfig({ action: "set", key: "step_timeout", value: "31m", expect_state: { file_sha256: "sha-default", values: {} } });
  assert.equal(result.entry.old, "30m0s", "old is the default Cell's value");
  assert.equal(result.entry.profile, "default", "the history entry names its Cell");
  assert.deepEqual(state().writes, [{ cell: "default", key: "step_timeout", value: "31m", cwd: "" }]);
  const set = argvs().find((args) => args.includes("set"));
  assert.deepEqual(withoutState(set), ["--profile", "default", "--json", "config", "set", "step_timeout", "31m"]);
  assert.equal(expectState(set).file_sha256, "sha-default");
  for (const args of argvs()) {
    if (args.includes("config")) assert.deepEqual(args.slice(0, 2), ["--profile", "default"], `unpinned: ${args.join(" ")}`);
    if (args.includes("ops")) assert.deepEqual(args, ["--json", "ops", "status"], "reachability is the Host's");
  }
});

test("an unscoped api_key write: the default Cell, the key on stdin only", async () => {
  reset();
  await write.writePolyforgeConfig({ action: "set", key: "api_key", value: KEY, expect_state: { file_sha256: "sha-default" } });
  const set = calls().find((call) => call.args.includes("set"));
  assert.deepEqual(withoutState(set.args), ["--profile", "default", "--json", "config", "set", "api_key"]);
  assert.equal(set.stdin, `${KEY}\n`);
  assert.ok(!calls().some((call) => call.args.some((token) => token.includes(KEY))), "the key never rides argv");
  assert.deepEqual(state().writes, [{ cell: "default", key: "api_key", value: "<stdin>", cwd: "" }]);
  assert.ok(!readFileSync(join(agentDir, "polyforge-config-history.json"), "utf8").includes(KEY), "the history never holds the key");
});

test("an unscoped reload is the default Cell's, then the Host aggregate", async () => {
  reset();
  const result = await write.reloadPolyforgeConfig(DEFAULT());
  assert.deepEqual(result.reloaded, { reloaded: ["default"] });
  assert.deepEqual(argvs(), [["--json", "version"], ["--profile", "default", "--json", "config", "reload"], ["--json", "ops", "status"]]);
});

test("a rollback of an entry without a scope writes the default Cell's file back", async () => {
  reset();
  const legacy = { id: "legacy", at: "2026-11-01T00:00:00.000Z", scope: "cli", cwd: "", key: "step_timeout", old: "29m", new: "30m", secret: false, rollbackable: true };
  const rolled = await write.rollbackPolyforgeConfig({ id: legacy.id, key: legacy.key, old: legacy.old, entry: legacy });
  assert.equal(rolled.entry.profile, "default");
  assert.deepEqual(state().writes, [{ cell: "default", key: "step_timeout", value: "29m", cwd: "" }]);
  const set = argvs().find((args) => args.includes("set"));
  assert.deepEqual(withoutState(set), ["--profile", "default", "--json", "config", "set", "step_timeout", "29m"]);
  assert.equal(expectState(set).file_sha256, "sha-default", "a fresh CAS read of the default Cell's file");
  assert.ok(argvs().filter((args) => args.includes("config")).every((args) => args[0] === "--profile" && args[1] === "default"));
});

test("the workflow card reads and writes the default Cell's workspaces", async () => {
  reset();
  const doc = await workflows.readPolyforgeWorkflows(project, DEFAULT());
  assert.deepEqual(doc.workspaces.map((entry) => entry.workspace), [project], "the default Cell's mapping, not alpha's");
  assert.deepEqual(argvs(), [
    ["--json", "version"],
    ["--profile", "default", "--json", "config", "list"],
    ["--profile", "default", "--json", "config", "workspace", "--cwd", project],
    ["--profile", "default", "--json", "workflows", "--cwd", project],
  ]);
  reset();
  await write.writePolyforgeConfig({ action: "set", key: "steps.acme.grill.models", value: ["pi:new"], cwd: project, expect_state: { file_sha256: "yaml-default", values: {} } });
  assert.deepEqual(state().writes, [{ cell: "default", key: "steps.acme.grill.models", value: JSON.stringify(["pi:new"]), cwd: project }]);
  const set = argvs().find((args) => args.includes("set"));
  assert.deepEqual(withoutState(set), ["--profile", "default", "--json", "config", "set", "steps.acme.grill.models", JSON.stringify(["pi:new"]), "--cwd", project]);
  assert.ok(argvs().some((args) => args.join(" ") === `--profile default --json workflows --cwd ${project}`), "the capability read is the default Cell's too");
  // a named Cell's workflow card writes its own workspace's
  // yaml, pinned to that Cell (its mapping resolves the --cwd), never the default's.
  reset();
  await write.writePolyforgeConfig({ action: "set", key: "steps.acme.grill.models", value: ["pi:new"], cwd: alphaWs, profile: "alpha", expect_state: { file_sha256: "yaml-alpha", values: {} } });
  assert.deepEqual(state().writes, [{ cell: "alpha", key: "steps.acme.grill.models", value: JSON.stringify(["pi:new"]), cwd: alphaWs }], "a named-Cell workspace write lands in that Cell's workspace only");
  assert.ok(argvs().filter((args) => args.includes("config") || args.includes("workflows")).every((args) => args[0] === "--profile" && args[1] === "alpha"), "every verb of a named-Cell yaml write is pinned to it");
});

test("a pf without the multi-Cell protocol: the default Cell unflagged, the Host and named Cells refused with nothing scoped spawned", async () => {
  reset(null);
  const doc = await read.readPolyforgeConfig(project, DEFAULT());
  assert.equal(doc.capable, true, doc.reason);
  assert.deepEqual(argvs(), [
    ["--json", "version"],
    ["--json", "config", "list"],
    ["--json", "ops", "status"],
    ["--json", "config", "workspace", "--cwd", project],
  ]);
  await write.writePolyforgeConfig({ action: "set", key: "step_timeout", value: "31m" });
  assert.deepEqual(state().writes, [{ cell: "default", key: "step_timeout", value: "31m", cwd: "" }]);
  reset(null);
  const host = await read.readPolyforgeConfig("", { host: true });
  assert.equal(host.capable, false);
  assert.match(host.reason, /upgrade pf/);
  await assert.rejects(write.writePolyforgeConfig({ action: "set", key: "max_concurrent_jobs", value: 7, host: true }), (error) => error.status === 400 && /upgrade pf/.test(error.message));
  await assert.rejects(write.reloadPolyforgeConfig({ profile: "alpha" }), /upgrade pf/);
  assert.ok(!argvs().some((args) => args.includes("config") || args.includes("--profile") || args.includes("--host")), `scoped spawns: ${JSON.stringify(argvs())}`);
});

test("no unscoped config verb is left in the server libraries", () => {
  for (const name of ["polyforge-config.ts", "polyforge-config-write.ts", "polyforge-workflows.ts"]) {
    const source = readFileSync(new URL(`./${name}`, import.meta.url), "utf8");
    assert.doesNotMatch(source, /\[\s*"--json",\s*"(config|workflows)"/, `${name} spawns a config verb without its scope`);
  }
});
