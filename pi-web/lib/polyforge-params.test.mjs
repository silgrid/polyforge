// carries: lib/polyforge-params*, app/api/polyforge/params/*
// polyforge-params.test.mjs (u5, polyforge#79): the params read face's
// pure halves — the wire sanitization of `pf [--profile p] --json params
// <wi>`'s answer (one row per declared step, every declared param's
// final value and source) and the spawn: the argv, the one read timeout
// every pf read shares, a refusal as the typed PolyforgeError and an
// undecodable body as null (a garbage answer is no view, never a 5xx).
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const lib = await jiti.import("./polyforge-params.ts");

test("the params floor is documentation-only (the read degrades like the override view's)", () => {
  assert.equal(typeof lib.POLYFORGE_PARAMS_MINIMUM_PF_VERSION, "string");
  assert.match(lib.POLYFORGE_PARAMS_MINIMUM_PF_VERSION, /^2\.2[0-9]\.[0-9]+$/);
});

test("the view's wire sanitization keeps every declared param's final value and source", () => {
  const view = lib.sanitizeParamsView({
    work_item: "wi_A1",
    posture: "queued",
    steps: [
      { id: "build", status: "", params: [{ name: "image", value: "base-img", source: "bundle demo step build params.image" }] },
      { id: "review", params: [
        { name: "flag", value: "on", source: "step override" },
        { name: "image", value: "reg/x:1", source: "steps.proj.review.params.image" },
      ] },
      { id: "merge", params: [] },
    ],
  });
  assert.deepEqual(view, {
    wi: "wi_A1",
    posture: "queued",
    steps: [
      { id: "build", status: "not started", params: [{ name: "image", value: "base-img", source: "bundle demo step build params.image" }] },
      { id: "review", status: "not started", params: [
        { name: "flag", value: "on", source: "step override" },
        { name: "image", value: "reg/x:1", source: "steps.proj.review.params.image" },
      ] },
      { id: "merge", status: "not started", params: [] },
    ],
  });
  assert.equal(lib.sanitizeParamsView(null), null);
  assert.equal(lib.sanitizeParamsView({ work_item: "", posture: "queued", steps: [] }), null, "no work item names no view");
  assert.equal(lib.sanitizeParamsView({ work_item: "wi_A1", posture: "queued", steps: [{ id: "" }] }), null, "a row without an id is no view");
  const junk = lib.sanitizeParamsView({
    work_item: "wi_A1", posture: "queued",
    steps: [{ id: "build", params: [{ name: "x", value: 3, source: {} }, { value: "y" }, "junk"] }],
  });
  assert.equal(junk.steps[0].params.length, 1, "a param without a name and junk entries drop");
  assert.equal(junk.steps[0].params[0].name, "x");
  assert.equal(junk.steps[0].params[0].value, "", "a non-string value keeps the empty string");
});

test("the spawn is `pf [--json] params <wi>` over the shared read timeout: a refusal is the typed error, an undecodable body is null", async () => {
  const work = mkdtempSync(join(tmpdir(), "pf-params-lib-"));
  const saved = { PF_BIN: process.env.PF_BIN, POLYFORGE_DATA_DIR: process.env.POLYFORGE_DATA_DIR };
  const argvLog = join(work, "argv.log");
  mkdirSync(join(work, "data"), { recursive: true });
  writeFileSync(argvLog, "");
  writeFileSync(join(work, "pf"), `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(argvLog)}, JSON.stringify(args) + "\\n");
if (args.includes("refuse")) { process.stderr.write("pf: DAEMON-409: the lane does not load on this daemon\\n"); process.exit(1); }
if (args.includes("garbage")) { process.stdout.write("not json\\n"); process.exit(0); }
process.stdout.write(JSON.stringify({ work_item: "wi_A1", posture: "queued", steps: [
  { id: "build", params: [{ name: "image", value: "base-img", source: "bundle demo step build params.image" }] },
] }) + "\\n");
`);
  chmodSync(join(work, "pf"), 0o755);
  process.env.PF_BIN = join(work, "pf");
  process.env.POLYFORGE_DATA_DIR = join(work, "data");
  const argvLines = async () => (await readFile(argvLog, "utf8")).trim().split("\n").map((line) => JSON.parse(line));
  try {
    const view = await lib.getPolyforgeParams("wi_A1");
    assert.deepEqual(view, {
      wi: "wi_A1", posture: "queued",
      steps: [{ id: "build", status: "not started", params: [{ name: "image", value: "base-img", source: "bundle demo step build params.image" }] }],
    });
    assert.deepEqual(await argvLines(), [["--json", "params", "wi_A1"]]);

    await assert.rejects(() => lib.getPolyforgeParams("wi_refuse"), /the lane does not load on this daemon/);
    assert.equal(await lib.getPolyforgeParams("wi_garbage"), null);

    const profiled = await lib.getPolyforgeParams("wi_A1", "lab");
    assert.ok(profiled, "a named Cell's read prefixes --profile");
    assert.deepEqual((await argvLines()).at(-1), ["--profile", "lab", "--json", "params", "wi_A1"]);
  } finally {
    process.env.PF_BIN = saved.PF_BIN;
    process.env.POLYFORGE_DATA_DIR = saved.POLYFORGE_DATA_DIR;
    rmSync(work, { recursive: true, force: true });
  }
});
