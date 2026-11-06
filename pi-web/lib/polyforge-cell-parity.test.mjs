// carries: lib/polyforge-config-write.ts, lib/polyforge-workflows.ts, lib/polyforge-config-schema.ts, lib/polyforge-config-scope.ts
// carries: app/api/polyforge/workflows/route.ts
// — a named Cell's settings work like the default Cell's: its
// 工作区 mapping is written through the existing config writer with
// `--profile <name>` (`[workspace."<path>"]` projects), its workflow card
// reads and writes its own workspaces with `--profile <name>`, and a mapping
// the Host refuses (a path equal to or nested under another Cell's workspace)
// comes back with the Host's own message. The stub answers the real pf wire:
// `--json version` capabilities, the scoped `config list`, `config
// workspace`, `config set|unset` (a mapping write notifies the daemon, which
// refuses an overlapping Cell with DAEMON-409 "Cell … was not changed"), and
// `workflows`.
import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, test } from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const work = realpathSync(mkdtempSync(join(tmpdir(), "pf-parity-385-")));
const bin = join(work, "bin");
const agentDir = join(work, "agent");
const project = join(work, "project");
const publicWs = join(work, "ws-public");
const freshWs = join(work, "ws-fresh");
for (const dir of [bin, agentDir, project, publicWs, freshWs]) mkdirSync(dir, { recursive: true });
const stub = join(bin, "pf");
const stateFile = join(work, "state.json");
const log = join(work, "argv.log");
const MULTI = ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"];
const KEY = "E2E-W385-UNIT-KEY-SENTINEL";

const reset = (files) => {
  writeFileSync(stateFile, JSON.stringify({
    files: files ?? {
      default: { sha: 1, mapping: { [project]: ["acme"] } },
      public: { sha: 1, mapping: { [publicWs]: ["pub"] } },
      empty: { sha: 1, mapping: {} },
    },
    writes: [],
  }));
  writeFileSync(log, "");
};
const state = () => JSON.parse(readFileSync(stateFile, "utf8"));
const calls = () => readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
const argvs = () => calls().map((call) => call.args).filter((args) => args.join(" ") !== "--version");
const withoutState = (args) => args.slice(0, args.includes("--expect-state") ? args.indexOf("--expect-state") : args.length);
const expectState = (args) => JSON.parse(args[args.indexOf("--expect-state") + 1]);

writeFileSync(stub, `#!${process.execPath}
const fs = require("fs");
const s = JSON.parse(fs.readFileSync(${JSON.stringify(stateFile)}, "utf8"));
const args = process.argv.slice(2);
const keyWrite = args.includes("set") && args.includes("api_key");
let stdin = ""; if (keyWrite) { try { stdin = fs.readFileSync(0, "utf8"); } catch {} }
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify({ args, stdin }) + "\\n");
const out = (doc) => { process.stdout.write(JSON.stringify(doc)); process.exit(0); };
const fail = (code, detail) => { process.stderr.write(JSON.stringify({ error: { code, detail } })); process.exit(1); };
if (args[0] === "--version") { process.stdout.write("v2.20.21\\n"); process.exit(0); }
let i = 0; let profile = "";
for (; i < args.length; i += 1) {
  if (args[i] === "--json") continue;
  if (args[i] === "--profile") { profile = args[i + 1]; i += 1; continue; }
  break;
}
const verb = args.slice(i).filter((a) => a !== "--host");
const host = args.includes("--host");
const cell = host ? "default" : profile || "default";
const file = s.files[cell];
const opt = (name) => { const at = verb.indexOf(name); return at >= 0 ? verb[at + 1] : ""; };
const save = () => fs.writeFileSync(${JSON.stringify(stateFile)}, JSON.stringify(s));
const section = (path) => '[workspace."' + path + '"]';
const nested = (a, b) => a === b || a.startsWith(b + "/") || b.startsWith(a + "/");
if (verb[0] === "version") out({ version: "v2.20.21", capabilities: ${JSON.stringify(MULTI)} });
if (verb[0] === "config" && verb[1] === "list") {
  if (host) out({ file: { sha256: "sha-host", exists: true }, items: [{ key: "max_concurrent_jobs", value: 6, source: "file", class: "live" }] });
  out({ file: { sha256: "sha-" + cell + "-" + file.sha, exists: true }, items: [
    { key: "step_timeout", value: "30m0s", source: "file", class: "live" },
    { key: "api_key", set: true, source: "file", class: "restart-only" },
    ...Object.entries(file.mapping).map(([ws, projects]) => ({ key: section(ws), value: projects.join(","), source: "file", section: true, class: "next-claim" })),
  ] });
}
if (verb[0] === "config" && verb[1] === "workspace") {
  const ws = Object.keys(file.mapping).find((w) => opt("--cwd") === w || opt("--cwd").startsWith(w + "/"));
  if (!ws) out({ mapped: false, workspace: null, yaml: null, steps: null });
  out({ mapped: true, workspace: ws, yaml: { path: ws + "/.polyforge.yaml", sha256: "yaml-" + cell, exists: true }, steps: { pub: { grill: { declared: true, models: ["pi:old"], source: "file", class: "live" } } }, steps_complete: true });
}
if (verb[0] === "config" && (verb[1] === "set" || verb[1] === "unset")) {
  const key = verb[2];
  const m = /^\\[workspace\\."(.*)"\\]$/.exec(key);
  if (m) {
    // pf writes the Cell's file, then the daemon's reload refuses a Cell
    // whose workspace overlaps another Cell's (#348): DAEMON-409.
    if (verb[1] === "set") file.mapping[m[1]] = verb[3].replace(/^projects=/, "").split(",").filter(Boolean);
    else delete file.mapping[m[1]];
    file.sha += 1;
    s.writes.push({ cell, key, value: verb[3] ?? null, cwd: opt("--cwd") });
    save();
    for (const [other, f] of Object.entries(s.files)) {
      if (other === cell || verb[1] !== "set") continue;
      for (const ws of Object.keys(f.mapping)) {
        if (nested(m[1], ws)) fail("DAEMON-409", 'Cell "' + cell + '" was not changed: conflicts with ' + (other === "default" ? "the default Cell" : "Cell " + other) + ": workspace " + m[1] + " overlaps " + ws);
      }
    }
    out({ outcomes: [{ key, class: "next-claim", status: "next-claim" }] });
  }
  s.writes.push({ cell, key, value: key === "api_key" ? "<stdin>" : verb[3] ?? null, cwd: opt("--cwd") });
  file.sha += 1;
  save();
  out({ outcome: "applied" });
}
if (verb[0] === "config" && verb[1] === "reload") out({ reloaded: [cell] });
if (verb[0] === "ops" && verb[1] === "status") out({ queue: { max_concurrent: 6 }, supervision: { step_timeout: "30m0s" } });
if (verb[0] === "workflows") out({ workflows_dir: "/wf", daemon_reachable: true, reason: "", workspace: opt("--cwd"), projects: ["pub"], write_capable: true, write_reason: "", bundles: [], bundle_errors: [] });
process.stderr.write("pf: unknown command " + verb.join(" ")); process.exit(2);
`);
chmodSync(stub, 0o755);

const saved = { PF_BIN: process.env.PF_BIN, PF_PROFILE: process.env.PF_PROFILE, PI_CODING_AGENT_DIR: process.env.PI_CODING_AGENT_DIR };
before(() => {
  process.env.PF_BIN = stub;
  delete process.env.PF_PROFILE;
  process.env.PI_CODING_AGENT_DIR = agentDir;
});
after(() => {
  for (const name of ["PF_BIN", "PF_PROFILE", "PI_CODING_AGENT_DIR"]) {
    if (saved[name] === undefined) delete process.env[name];
    else process.env[name] = saved[name];
  }
  rmSync(work, { recursive: true, force: true });
});

const write = await jiti.import("./polyforge-config-write.ts");
const workflows = await jiti.import("./polyforge-workflows.ts");
const PUBLIC = { profile: "public" };

test("a named Cell's mapping is added, edited and removed through the config writer with --profile, its file's CAS and history", async () => {
  reset();
  const key = `[workspace."${freshWs}"]`;
  const added = await write.writePolyforgeConfig({ action: "set", key, value: ["pub", "docs"], profile: "public", expect_state: { file_sha256: "sha-public-1", values: {} } });
  const set = argvs().find((args) => args.includes("set"));
  assert.deepEqual(withoutState(set), ["--profile", "public", "--json", "config", "set", key, "projects=pub,docs"]);
  assert.equal(expectState(set).file_sha256, "sha-public-1", "the Cell file's CAS hash reaches pf");
  assert.ok(!set.includes("--cwd"), "a mapping write is a Cell-file write: no --cwd");
  assert.deepEqual(state().files.public.mapping[freshWs], ["pub", "docs"], "the mapping landed in the public Cell's file");
  assert.equal(state().files.default.mapping[freshWs], undefined, "nothing landed in the default Cell's file");
  assert.equal(added.entry.profile, "public", "the history entry names its Cell");
  assert.equal(added.entry.key, key);
  assert.equal(added.entry.old, null, "a new mapping had no old value");
  assert.deepEqual(added.entry.new, ["pub", "docs"]);
  for (const args of argvs()) if (args.includes("config")) assert.deepEqual(args.slice(0, 2), ["--profile", "public"], `unpinned: ${args.join(" ")}`);

  reset();
  const edited = await write.writePolyforgeConfig({ action: "set", key: `[workspace."${publicWs}"]`, value: "pub, extra", profile: "public", expect_state: { file_sha256: "sha-public-1", values: {} } });
  assert.deepEqual(withoutState(argvs().find((args) => args.includes("set"))), ["--profile", "public", "--json", "config", "set", `[workspace."${publicWs}"]`, "projects=pub,extra"]);
  assert.equal(edited.entry.old, "pub", "the edit records the file's real old projects");

  reset();
  const removed = await write.writePolyforgeConfig({ action: "unset", key: `[workspace."${publicWs}"]`, profile: "public", expect_state: { file_sha256: "sha-public-1", values: {} } });
  assert.deepEqual(withoutState(argvs().find((args) => args.includes("unset"))), ["--profile", "public", "--json", "config", "unset", `[workspace."${publicWs}"]`]);
  assert.equal(state().files.public.mapping[publicWs], undefined, "the mapping left the public Cell's file");
  assert.equal(removed.entry.new, null);

  reset();
  const rolled = await write.rollbackPolyforgeConfig({ key: removed.entry.key, old: removed.entry.old, entry: removed.entry });
  assert.deepEqual(withoutState(argvs().find((args) => args.includes("set"))), ["--profile", "public", "--json", "config", "set", `[workspace."${publicWs}"]`, "projects=pub"]);
  assert.equal(rolled.entry.profile, "public", "the rollback writes the Cell's own file back");
});

test("a mapping value pf could misread is refused before any write", async () => {
  reset();
  for (const [key, value, why] of [
    ['[workspace."relative/path"]', ["pub"], "a relative path"],
    [`[workspace."${freshWs}\\"x"]`, ["pub"], "a quote in the path"],
    [`[workspace."${freshWs}"]`, [], "no project"],
    [`[workspace."${freshWs}"]`, ["pub", "bad name"], "a project with a space"],
    [`[workspace."${freshWs}"]`, ["a=b"], "a project with an ="],
  ]) {
    await assert.rejects(write.writePolyforgeConfig({ action: "set", key, value, profile: "public" }), (error) => error.status === 400, why);
  }
  assert.deepEqual(state().writes, [], "nothing was written");
  assert.ok(!argvs().some((args) => args.includes("set")), "no write spawn");
});

test("an overlapping or nested mapping comes back with the Host's message, a 409", async () => {
  reset();
  for (const path of [project, join(project, "nested")]) {
    await assert.rejects(
      write.writePolyforgeConfig({ action: "set", key: `[workspace."${path}"]`, value: ["pub"], profile: "public" }),
      (error) => error.status === 409 && error.message === `Cell "public" was not changed: conflicts with the default Cell: workspace ${path} overlaps ${project}`,
      path,
    );
  }
});

test("a named Cell's api_key goes to its own file on stdin only", async () => {
  reset();
  await write.writePolyforgeConfig({ action: "set", key: "api_key", value: KEY, profile: "public", expect_state: { file_sha256: "sha-public-1" } });
  const set = calls().find((call) => call.args.includes("set"));
  assert.deepEqual(withoutState(set.args), ["--profile", "public", "--json", "config", "set", "api_key"]);
  assert.equal(set.stdin, `${KEY}\n`);
  assert.ok(!calls().some((call) => call.args.some((token) => token.includes(KEY))), "the key never rides argv");
  assert.ok(!readFileSync(join(agentDir, "polyforge-config-history.json"), "utf8").includes(KEY), "the history never holds the key");
});

test("a named Cell's workflow card reads its own mapping and writes its own workspace yaml with --profile", async () => {
  reset();
  const doc = await workflows.readPolyforgeWorkflows(project, PUBLIC);
  assert.equal(doc.capable, true, doc.reason);
  assert.deepEqual(doc.workspaces.map((entry) => entry.workspace), [publicWs], "the public Cell's mapping, not the default's");
  assert.deepEqual(argvs(), [
    ["--json", "version"],
    ["--profile", "public", "--json", "config", "list"],
    ["--profile", "public", "--json", "config", "workspace", "--cwd", publicWs],
    ["--profile", "public", "--json", "workflows", "--cwd", publicWs],
  ]);
  reset();
  const result = await write.writePolyforgeConfig({ action: "set", key: "steps.pub.grill.models", value: ["pi:new"], cwd: publicWs, profile: "public", expect_state: { file_sha256: "yaml-public", values: {} } });
  const set = argvs().find((args) => args.includes("set"));
  assert.deepEqual(withoutState(set), ["--profile", "public", "--json", "config", "set", "steps.pub.grill.models", JSON.stringify(["pi:new"]), "--cwd", publicWs]);
  assert.ok(argvs().some((args) => args.join(" ") === `--profile public --json workflows --cwd ${publicWs}`), "the capability read is the Cell's own");
  assert.equal(result.entry.profile, "public", "the step entry names its Cell, so its rollback lands there too");
  for (const args of argvs()) if (args.includes("config") || args.includes("workflows")) assert.deepEqual(args.slice(0, 2), ["--profile", "public"], `unpinned: ${args.join(" ")}`);
  reset();
  await assert.rejects(
    write.writePolyforgeConfig({ action: "set", key: "steps.pub.grill.models", value: ["pi:new"], cwd: publicWs, host: true }),
    (error) => error.status === 400 && /Host/.test(error.message),
  );
  assert.deepEqual(state().writes, [], "a Host-scoped yaml write is refused before any write");
});

test("a named Cell with no workspace says so and never reads the settings cwd under its profile", async () => {
  reset();
  const doc = await workflows.readPolyforgeWorkflows(project, { profile: "empty" });
  assert.equal(doc.capable, true);
  assert.deepEqual(doc.workspaces, [], "no workspace is read");
  assert.match(doc.reason ?? "", /Cell empty maps no workspace/);
  assert.deepEqual(argvs(), [["--json", "version"], ["--profile", "empty", "--json", "config", "list"]]);
});

test("the workflows route passes the page's Cell through", () => {
  const route = readFileSync(new URL("../app/api/polyforge/workflows/route.ts", import.meta.url), "utf8");
  // a boolean check: a mismatch must not dump the whole route into the report
  assert.ok(/parsePolyforgeScope\(\{ profile: params\.get\("profile"\) \?\? undefined \}\)/.test(route), "the route parses the page's ?profile= into a scope");
  assert.ok(/readPolyforgeWorkflows\(cwd, scope\)/.test(route), "the route reads the workflows of that scope");
});
