import "#test-env";
import assert from "node:assert/strict";
import { after, test } from "node:test";
import { createJiti } from "jiti";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const jiti = createJiti(import.meta.url);
const pure = await jiti.import("./polyforge-cells-pure.ts");
const cells = await jiti.import("./polyforge-cells.ts");

// ---- pure ----

test("profile names follow pf's syntax; default is reserved for a new Cell", () => {
  for (const ok of ["alpha", "a", "a-1", "default", "x".repeat(32)]) assert.ok(pure.isPolyforgeProfileName(ok), ok);
  for (const bad of ["", "Alpha", "-a", "a_b", "x".repeat(33), 3, null]) assert.ok(!pure.isPolyforgeProfileName(bad), String(bad));
  assert.ok(pure.isNewCellName("alpha"));
  assert.ok(!pure.isNewCellName("default"));
});

test("the capability gate needs every multi-Cell capability, never a version compare", () => {
  assert.ok(pure.hasPolyforgeCellCapabilities(["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1", "cell-migration-v1"]));
  assert.ok(!pure.hasPolyforgeCellCapabilities(["cells-v1", "config-host-cell-v1"]));
  assert.ok(!pure.hasPolyforgeCellCapabilities([]));
  assert.ok(!pure.hasPolyforgeCellCapabilities(undefined));
  assert.deepEqual(pure.sanitizePolyforgeCapabilities({ version: "v9", capabilities: ["cells-v1", 3] }), ["cells-v1"]);
  assert.deepEqual(pure.sanitizePolyforgeCapabilities({ version: "v2.20.0" }), [], "an older pf reports none");
});

test("scope: --profile goes before the verb, --host after it, and the two exclude each other", () => {
  assert.deepEqual(pure.polyforgeScopePrefix({ profile: "alpha" }), ["--profile", "alpha"]);
  assert.deepEqual(pure.polyforgeScopeSuffix({ profile: "alpha" }), []);
  assert.deepEqual(pure.polyforgeScopeSuffix({ host: true }), ["--host"]);
  assert.deepEqual(pure.polyforgeScopePrefix({ host: true }), []);
  // the default Cell is named like any other Cell
  assert.deepEqual(pure.polyforgeScopePrefix({ profile: "default" }), ["--profile", "default"]);
  assert.deepEqual(pure.parsePolyforgeScope({ host: true }), { host: true });
  assert.deepEqual(pure.parsePolyforgeScope({ profile: "alpha" }), { profile: "alpha" });
  assert.throws(() => pure.parsePolyforgeScope({ host: true, profile: "alpha" }), /exclude/);
  assert.throws(() => pure.parsePolyforgeScope({ profile: "Bad Name" }), /profile/);
});

test("an absent scope is the default Cell, never the one pf would resolve", () => {
  for (const body of [{}, { profile: "" }, { profile: null }, { host: false }]) {
    assert.deepEqual(pure.parsePolyforgeScope(body), { profile: "default" }, JSON.stringify(body));
  }
  assert.deepEqual(pure.parsePolyforgeScope({ profile: "default" }), { profile: "default" });
  assert.equal(pure.isPolyforgeDefaultScope({ profile: "default" }), true);
  assert.equal(pure.isPolyforgeDefaultScope({ profile: "alpha" }), false);
  assert.equal(pure.isPolyforgeDefaultScope({ host: true }), false);
  assert.equal(pure.polyforgeNamedCell({ profile: "alpha" }), "alpha");
  assert.equal(pure.polyforgeNamedCell({ profile: "default" }), null);
  assert.equal(pure.polyforgeNamedCell({ host: true }), null);
});

test("a multi-Cell pf gets every scope explicitly; a pre-Cell pf addresses only the default Cell, unflagged", () => {
  const multi = ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"];
  assert.deepEqual(pure.polyforgeScopeArgv({ profile: "default" }, multi), { prefix: ["--profile", "default"], suffix: [] });
  assert.deepEqual(pure.polyforgeScopeArgv({ profile: "alpha" }, multi), { prefix: ["--profile", "alpha"], suffix: [] });
  assert.deepEqual(pure.polyforgeScopeArgv({ host: true }, multi), { prefix: [], suffix: ["--host"] });
  assert.deepEqual(pure.polyforgeDefaultScopeArgv(multi), { prefix: ["--profile", "default"], suffix: [] });
  for (const older of [[], ["cells-v1"], ["cells-v1", "config-host-cell-v1"]]) {
    assert.deepEqual(pure.polyforgeScopeArgv({ profile: "default" }, older), { prefix: [], suffix: [] }, `${older}: one file, no resolver`);
    assert.deepEqual(pure.polyforgeDefaultScopeArgv(older), { prefix: [], suffix: [] });
    assert.equal(pure.polyforgeScopeArgv({ profile: "alpha" }, older), null, `${older}: no named Cell`);
    assert.equal(pure.polyforgeScopeArgv({ host: true }, older), null, `${older}: no Host scope`);
  }
});

test("the Cell list sanitizer keeps the registry facts and drops anything else", () => {
  const rows = pure.sanitizePolyforgeCells({
    dir: "/x",
    profiles: [
      { profile: "default", state: "serving", valid: true, server: "http://hub:1", workspaces: ["/ws"], running: 1, pending: 2, max_concurrent: 4, api_key: "SECRET" },
      { profile: "alpha", state: "serving", valid: true, workspaces: [], execution_disabled: "no model-credential source declared", draining: true, pending_change: "rotation", cell_max_concurrent_jobs: 1 },
      { profile: "Bad", state: "serving" },
    ],
  });
  assert.equal(rows.length, 2);
  assert.ok(!JSON.stringify(rows).includes("SECRET"), "no credential ever leaves");
  assert.equal(rows[0].maxConcurrent, 4);
  assert.equal(rows[1].cellMaxConcurrentJobs, 1);
  assert.equal(pure.polyforgeCellNotice(rows[1]), "rotation", "draining wins the notice");
  assert.equal(pure.polyforgeCellNotice(rows[0]), null);
  assert.equal(pure.sanitizePolyforgeCells({}), null);
});

test("a Cell's list entry shows one state, the worst first", () => {
  const cell = (patch) => ({ profile: "alpha", state: "serving", valid: true, workspaces: ["/ws"], running: 0, pending: 0, draining: false, ...patch });
  assert.equal(pure.polyforgeCellListState(cell({})), "serving");
  assert.equal(pure.polyforgeCellListState(cell({ executionDisabled: "no workspace mapped" })), "execution-disabled");
  assert.equal(pure.polyforgeCellListState(cell({ draining: true, pendingChange: "rotation", executionDisabled: "x" })), "pending-restart", "a pending change outranks a closed execution");
  assert.equal(pure.polyforgeCellListState(cell({ state: "degraded", draining: true })), "degraded", "a degraded Cell outranks a pending change");
  assert.equal(pure.polyforgeCellListState(cell({ state: "starting" })), "degraded", "a state pf names but the page does not know is never shown as serving");
  assert.equal(pure.polyforgeCellListState(cell({ valid: false, state: "invalid" })), "degraded");
  assert.equal(pure.polyforgeCellListState(cell({ valid: false, state: "removed" })), "removed");
});

// ---- server, against a stub pf ----

const work = mkdtempSync(join(tmpdir(), "pf-cells-"));
const stub = join(work, "pf");
const state = join(work, "state.json");
const log = join(work, "argv.log");
const setState = (patch) => writeFileSync(state, JSON.stringify({ caps: ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"], fail: "", ...patch }));
writeFileSync(stub, `#!${process.execPath}
const fs = require("fs"); const args = process.argv.slice(2);
const s = JSON.parse(fs.readFileSync(${JSON.stringify(state)}, "utf8"));
let input = ""; try { input = fs.readFileSync(0, "utf8"); } catch {}
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify({ args, stdin: input }) + "\\n");
if (args.includes("version")) { process.stdout.write(JSON.stringify({ version: "v9", capabilities: s.caps })); process.exit(0); }
if (s.fail) { process.stderr.write(JSON.stringify({ code: "DAEMON-409", detail: s.fail })); process.exit(1); }
if (args.includes("list")) { process.stdout.write(JSON.stringify({ profiles: [{ profile: "default", state: "serving", valid: true, workspaces: [] }] })); process.exit(0); }
process.stdout.write(JSON.stringify({ outcomes: [] }));
`);
chmodSync(stub, 0o755);
const savedBin = process.env.PF_BIN;
process.env.PF_BIN = stub;
after(() => {
  if (savedBin === undefined) delete process.env.PF_BIN; else process.env.PF_BIN = savedBin;
  rmSync(work, { recursive: true, force: true });
});
const calls = () => existsSync(log) ? readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((l) => JSON.parse(l)) : [];
const reset = (patch = {}) => { setState(patch); rmSync(log, { force: true }); };

test("the read goes through pf cell list and reports the capability gate", async () => {
  reset();
  const read = await cells.readPolyforgeCells();
  assert.equal(read.supported, true);
  assert.deepEqual(read.cells.map((c) => c.profile), ["default"]);
  assert.deepEqual(calls().map((c) => c.args), [["--json", "version"], ["--json", "cell", "list"]]);
  reset({ caps: [] });
  const old = await cells.readPolyforgeCells();
  assert.equal(old.supported, false);
  assert.equal(old.cells, null);
  assert.match(old.upgradeHint, /upgrade pf/);
  assert.deepEqual(calls().map((c) => c.args), [["--json", "version"]], "no list read without the capability");
});

test("add validates before any spawn, puts the key on stdin only, and gates on the capability", async () => {
  reset();
  await assert.rejects(cells.addPolyforgeCell({ profile: "default", server: "http://h:1", apiKey: "k" }), /not be default/);
  await assert.rejects(cells.addPolyforgeCell({ profile: "alpha", server: "ftp://h", apiKey: "k" }), /http/);
  await assert.rejects(cells.addPolyforgeCell({ profile: "alpha", server: "http://h:1", apiKey: "" }), /apiKey/);
  assert.deepEqual(calls(), [], "a refused request spawns nothing");
  await cells.addPolyforgeCell({ profile: "alpha", server: "http://h:1", apiKey: "K-SECRET", modelSource: "shared-default" });
  const add = calls().find((c) => c.args.includes("add"));
  assert.deepEqual(add.args, ["--json", "cell", "add", "alpha", "--server", "http://h:1", "--model-source", "shared-default"]);
  assert.ok(!add.args.join(" ").includes("K-SECRET"), "the key never rides argv");
  assert.equal(add.stdin, "K-SECRET\n");
  reset({ caps: ["cells-v1"] });
  await assert.rejects(cells.addPolyforgeCell({ profile: "alpha", server: "http://h:1", apiKey: "k" }), /upgrade pf/);
  assert.ok(!calls().some((c) => c.args.includes("add")), "no write on a pf without the capability");
});

test("update/repair/remove spawn pf cell; default cannot be removed; pf's refusal keeps its code", async () => {
  reset();
  await cells.changePolyforgeCell("alpha", "repair");
  assert.ok(calls().some((c) => c.args.join(" ") === "--json cell repair alpha"));
  await assert.rejects(cells.changePolyforgeCell("default", "remove"), /cannot be removed/);
  await assert.rejects(cells.changePolyforgeCell("../x", "update"), /profile/);
  reset({ fail: "Cell alpha still holds work" });
  await assert.rejects(cells.changePolyforgeCell("alpha", "remove"), (error) => error.status === 409 && /holds work/.test(error.message));
});
