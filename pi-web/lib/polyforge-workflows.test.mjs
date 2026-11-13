import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// the workflow card's read transport.
const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-workflows.ts");
// every read names its Cell; these cases are the default Cell's
const DEFAULT_CELL = { profile: "default" };
const route = await readFile(new URL("../app/api/polyforge/workflows/route.ts", import.meta.url), "utf8");

const WORKFLOWS = {
  workflows_dir: "/wf", daemon_reachable: true, reason: "", workspace: "/ws", projects: ["alpha", "beta"],
  write_capable: true, write_reason: "", extra_field: "dropped",
  task: { alpha: { models: ["pi:d"], from: "steps.alpha.default.models" } },
  bundles: [{
    name: "feature-full",
    defaults: { model_candidates: ["pi:ff"], effective: { alpha: { models: ["pi:d"], from: "steps.alpha.default.models" } } },
    steps: [
      // an older pf's attrs ( removed them) are dropped
      { id: "grill", model_candidates: ["pi:g"], attrs: [{ name: "question_batch_size", type: "int", default: 0 }], effective: { alpha: { models: ["pi:g"], from: "bundle feature-full step grill model_candidates" } } },
      { id: "spec" },
    ],
  }],
  bundle_errors: [{ bundle: "broken", reason: "yaml: line 2" }],
};
const WORKSPACE = {
  mapped: true, workspace: "/ws", yaml: { path: "/ws/.polyforge.yaml", sha256: "abc", exists: true },
  steps: { alpha: { grill: { declared: true, models: ["claude:x"], source: "file", class: "live", attrs: { question_batch_size: { value: 2, declared: true, source: "file", class: "live", default: 0, schema: { kind: "int" } } } } } },
};

test("the card's floor is 2.20.15, its own, the config panel's untouched", async () => {
  assert.equal(lib.POLYFORGE_WORKFLOW_CARD_MINIMUM_PF_VERSION, "2.20.15");
  const config = await jiti.import("./polyforge-config.ts");
  assert.equal(config.POLYFORGE_CONFIG_PANEL_MINIMUM_PF_VERSION, "2.20.6");
});

test("the workflows sanitizer keeps the documented fields only", () => {
  const doc = lib.sanitizeWorkflowsDoc(WORKFLOWS);
  assert.equal(doc.extra_field, undefined);
  assert.deepEqual(doc.projects, ["alpha", "beta"]);
  assert.equal(doc.bundles[0].steps[0].attrs, undefined, "a step's attrs are dropped");
  assert.equal(doc.bundles[0].steps[1].model_candidates, undefined, "a step declaring no candidates gets none");
  assert.deepEqual(doc.bundle_errors, [{ bundle: "broken", reason: "yaml: line 2" }]);
  assert.equal(doc.task.alpha.from, "steps.alpha.default.models");
  assert.equal(lib.sanitizeWorkflowsDoc({ nope: true }), null);
});

test("the workspace sanitizer keeps declared values and drops attrs", () => {
  const ws = lib.sanitizeWorkflowWorkspace(WORKSPACE);
  assert.equal(ws.yaml.sha256, "abc");
  assert.deepEqual(ws.steps.alpha.grill.models, ["claude:x"]);
  assert.equal(ws.steps.alpha.grill.attrs, undefined, "a step entry's attrs are dropped");
  assert.equal(lib.sanitizeWorkflowWorkspace({ mapped: false }), null);
});

function fakePf(t, { version = "v2.20.16", workflows = WORKFLOWS, workspace = WORKSPACE, failWorkflows = false } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "pf-wf-"));
  const log = join(dir, "calls.log");
  writeFileSync(join(dir, "workflows.json"), JSON.stringify(workflows));
  writeFileSync(join(dir, "workspace.json"), JSON.stringify(workspace));
  const bin = join(dir, "pf");
  writeFileSync(bin, `#!/bin/sh
echo "$*" >> "${log}"
case "$*" in
  --version) echo "${version}" ;;
  *" workflows"*) ${failWorkflows ? 'echo "pf: boom" >&2; exit 1' : `cat "${dir}/workflows.json"`} ;;
  *"config workspace"*) cat "${dir}/workspace.json" ;;
  *) exit 2 ;;
esac
`);
  chmodSync(bin, 0o755);
  const previous = process.env.PF_BIN;
  process.env.PF_BIN = bin;
  t.after(() => {
    if (previous === undefined) delete process.env.PF_BIN;
    else process.env.PF_BIN = previous;
    rmSync(dir, { recursive: true, force: true });
  });
  return () => readFileSync(log, "utf8").trim().split("\n");
}

test("with no cli.toml mapping, every read spawns the cwd's two pf reads afresh (no cache)", async (t) => {
  const calls = fakePf(t);
  const first = await lib.readPolyforgeWorkflows("/ws/sub", DEFAULT_CELL);
  assert.equal(first.capable, true);
  assert.equal(first.reason, undefined);
  assert.equal(first.workflows.bundles[0].name, "feature-full");
  assert.equal(first.workspace.steps.alpha.grill.declared, true);
  await lib.readPolyforgeWorkflows("/ws/sub", DEFAULT_CELL);
  const reads = calls().filter((line) => line !== "--version");
  // one capability read per call pins the default Cell (this
  // stub reports none: a pre-Cell pf, so the reads stay unflagged)
  assert.equal(reads.filter((line) => line === "--json version").length, 2, "one capability read per call");
  assert.equal(reads.filter((line) => line === "--json workflows --cwd /ws/sub").length, 2, "one fresh pf workflows per call");
  assert.equal(reads.filter((line) => line === "--json config workspace --cwd /ws/sub").length, 2, "one fresh workspace read per call");
  assert.equal(reads.filter((line) => line === "--json config list").length, 2, "one mapping read per call (this stub has none)");
  assert.equal(reads.length, 8);
  assert.equal(first.workspaces.length, 1, "the cwd alone is the one workspace");
});

test("below the floor the read is a capable:false field and spawns no read verb", async (t) => {
  const calls = fakePf(t, { version: "v2.20.14" });
  const old = await lib.readPolyforgeWorkflows("/ws", DEFAULT_CELL);
  assert.deepEqual(calls(), ["--version"]);
  assert.equal(old.capable, false);
  assert.match(old.reason, /2\.20\.15/);
  assert.equal(old.workflows, null);
});

test("a failed workflows read keeps the workspace facts and names the failure", async (t) => {
  fakePf(t, { failWorkflows: true });
  const read = await lib.readPolyforgeWorkflows("/ws", DEFAULT_CELL);
  assert.equal(read.capable, true);
  assert.equal(read.workflows, null);
  assert.match(read.reason, /boom/);
});

test("an unreachable daemon is the document's reason", async (t) => {
  fakePf(t, { workflows: { ...WORKFLOWS, daemon_reachable: false, reason: "the daemon is not reachable", bundles: [] } });
  const read = await lib.readPolyforgeWorkflows("/ws", DEFAULT_CELL);
  assert.equal(read.workflows.daemon_reachable, false);
  assert.match(read.reason, /not reachable/);
});

test("the route is GET-only, gated, dynamic, never 5xx and caches nothing", () => {
  const handlers = [...route.matchAll(/export (?:async )?function (\w+)/g)].map((m) => m[1]);
  assert.deepEqual(handlers, ["GET"], "only GET (POST is 405)");
  assert.match(route, /if \(!isApiRequestAllowed\(request\)\)/);
  assert.match(route, /export const dynamic = "force-dynamic"/);
  const statuses = [...route.matchAll(/status: (\d{3})/g)].map((m) => Number(m[1]));
  // the pane's Cell rides ?profile=; a malformed one is the
  // config route's same 400 — still never a 5xx
  assert.deepEqual(statuses, [403, 400]);
  assert.match(route, /readPolyforgeWorkflows\(cwd, scope\)/);
  assert.doesNotMatch(route, /^(let|const) \w*[cC]ache/m, "no module-level cache");
});

// the union of every cli.toml-mapped workspace.
const MAPPED = {
  "/w/a": ["polyforge", "pi", "tsshd"],
  "/w/b": ["ieops", "workbench"],
  "/w/c": ["demo"],
};
function unionPf(t, { fail = "", failYaml = "" } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "pf-union-"));
  const log = join(dir, "calls.log");
  writeFileSync(log, "");
  const list = { file: { sha256: "cli", exists: true }, items: [
    { key: "server", value: "http://x", class: "restart-only" },
    ...Object.entries(MAPPED).map(([path, projects]) => ({ key: `[workspace."${path}"]`, value: projects.join(","), class: "restart-only", section: true })),
    { key: '[workspace."relative/path"]', value: "nope", class: "restart-only", section: true },
  ] };
  const bin = join(dir, "pf");
  writeFileSync(bin, `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(log)}, args.join(" ") + "\\n");
const mapped = ${JSON.stringify(MAPPED)};
const at = args.indexOf("--cwd"); const cwd = at >= 0 ? args[at + 1] : "";
const out = (doc) => { process.stdout.write(JSON.stringify(doc)); process.exit(0); };
if (args[0] === "--version") { process.stdout.write("v2.20.17\\n"); process.exit(0); }
if (args.includes("list")) out(${JSON.stringify(list)});
if (args.includes("workflows")) {
  if (cwd === ${JSON.stringify(fail)}) { process.stderr.write("pf: cannot read " + cwd + "/.polyforge.yaml: permission denied"); process.exit(1); }
  out({ ...${JSON.stringify(WORKFLOWS)}, workspace: cwd, projects: mapped[cwd] ?? [] });
}
if (args.includes("workspace")) {
  if (cwd === ${JSON.stringify(failYaml)}) { process.stderr.write("pf: yaml unreadable"); process.exit(1); }
  out({ mapped: true, workspace: cwd, yaml: { path: cwd + "/.polyforge.yaml", sha256: "sha-" + cwd, exists: true }, steps: {} });
}
process.exit(2);
`);
  chmodSync(bin, 0o755);
  const previous = process.env.PF_BIN;
  process.env.PF_BIN = bin;
  t.after(() => {
    if (previous === undefined) delete process.env.PF_BIN;
    else process.env.PF_BIN = previous;
    rmSync(dir, { recursive: true, force: true });
  });
  return () => readFileSync(log, "utf8").trim().split("\n").filter((line) => line && line !== "--version");
}

test("the mapping parser keeps absolute [workspace] paths in pf's order, once", () => {
  assert.deepEqual(lib.mappedWorkspacesFromList({ items: [
    { key: '[workspace."/b"]', section: true }, { key: "server" }, { key: '[workspace."/a"]' }, { key: '[workspace."/b"]' }, { key: '[workspace."-x"]' },
  ] }), ["/b", "/a"]);
  assert.deepEqual(lib.mappedWorkspacesFromList(null), []);
});

test("the union reads 1 mapping + 2 per mapped workspace, fresh each call, never per project", async (t) => {
  const calls = unionPf(t);
  const read = await lib.readPolyforgeWorkflows("/w/b/sub", DEFAULT_CELL);
  assert.deepEqual(read.workspaces.map((entry) => entry.workspace), ["/w/a", "/w/b", "/w/c"]);
  assert.deepEqual(read.workspaces.flatMap((entry) => entry.workflows.projects), ["polyforge", "pi", "tsshd", "ieops", "workbench", "demo"]);
  assert.equal(read.workspaces[2].declared.yaml.path, "/w/c/.polyforge.yaml", "each workspace carries its yaml path");
  assert.equal(read.workspaces[2].declared.yaml.sha256, "sha-/w/c");
  assert.equal(read.workflows.workspace, "/w/b", "the top-level fields stay the settings cwd's own workspace");
  assert.equal(read.workspace.workspace, "/w/b");
  assert.equal(read.reason, undefined);
  await lib.readPolyforgeWorkflows("/w/b/sub", DEFAULT_CELL);
  const reads = calls();
  assert.equal(reads.filter((line) => line === "--json version").length, 2, "one capability read per call");
  assert.equal(reads.filter((line) => line === "--json config list").length, 2);
  for (const path of Object.keys(MAPPED)) {
    assert.equal(reads.filter((line) => line === `--json workflows --cwd ${path}`).length, 2, path);
    assert.equal(reads.filter((line) => line === `--json config workspace --cwd ${path}`).length, 2, path);
  }
  assert.equal(reads.length, 2 * (1 + 1 + 2 * 3), "no per-project spawn, nothing cached");
});

test("a failed workspace degrades alone: its reason, no project; the others stay readable", async (t) => {
  unionPf(t, { fail: "/w/c", failYaml: "/w/a" });
  const read = await lib.readPolyforgeWorkflows("/w/b", DEFAULT_CELL);
  const byPath = Object.fromEntries(read.workspaces.map((entry) => [entry.workspace, entry]));
  assert.match(byPath["/w/c"].reason, /permission denied/);
  assert.equal(byPath["/w/c"].workflows, null);
  assert.match(byPath["/w/a"].reason, /yaml unreadable/, "no yaml CAS: no write for that workspace either");
  assert.equal(byPath["/w/b"].reason, undefined);
  assert.equal(read.reason, undefined, "one workspace's failure is not the page's reason");
  const pure = await jiti.import("./polyforge-workflows-pure.ts");
  const union = pure.workflowUnion(read);
  assert.deepEqual(union.projects.map((entry) => entry.project), ["ieops", "workbench"]);
  assert.deepEqual(union.failures.map((entry) => entry.workspace), ["/w/a", "/w/c"]);
});
