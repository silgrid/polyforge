// carries: app/api/polyforge/params/*
// polyforge-params-route.test.mjs (u6, polyforge#79): the work-item
// detail's params read face — GET /api/polyforge/params?wi=<slug|id> —
// exactly one `pf [--profile p] --json params <wi>` spawn over the shared
// read timeout, the id and profile validated BEFORE any spawn, the
// answer through the view's passport. Degradation-first: pf absent, a
// refusal, a hang or an undecodable body answer 200 with
// `params: null` (a garbage answer is no view, never a 5xx); the only
// non-2xx answers are the gate's 403 and the malformed-ref 400.
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const work = mkdtempSync(join(tmpdir(), "pf-params-route-"));
const saved = { PF_BIN: process.env.PF_BIN, POLYFORGE_DATA_DIR: process.env.POLYFORGE_DATA_DIR };
const log = join(work, "argv.log");
const mode = join(work, "mode");
mkdirSync(join(work, "data"), { recursive: true });
writeFileSync(log, "");
writeFileSync(mode, "view");
writeFileSync(join(work, "pf"), `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
if (args[0] === "--version") { process.stdout.write("v2.22.0\\n"); process.exit(0); }
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(args) + "\\n");
const mode = fs.readFileSync(${JSON.stringify(mode)}, "utf8");
const out = (v) => { process.stdout.write(JSON.stringify(v) + "\\n"); process.exit(0); };
if (mode === "hang") { setTimeout(() => process.exit(0), 30000); return; }
if (mode === "refuse") { process.stderr.write(JSON.stringify({ code: "DAEMON-409", detail: "the lane no-such does not load on this daemon" }) + "\\n"); process.exit(1); }
if (mode === "garbage") { process.stdout.write("not json\\n"); process.exit(0); }
out({ work_item: "wi_A1", posture: "queued", steps: [
  { id: "build", params: [{ name: "image", value: "base-img", source: "bundle demo step build params.image" }] },
  { id: "review", params: [{ name: "image", value: "reg/x:1", source: "step override" }, { name: "flag", value: "", source: "bundle demo step review params.flag" }] },
] });
`);
chmodSync(join(work, "pf"), 0o755);
process.env.PF_BIN = join(work, "pf");
process.env.POLYFORGE_DATA_DIR = join(work, "data");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { GET } = await jiti.import("./params/route.ts");
const source = await readFile(new URL("./params/route.ts", import.meta.url), "utf8");

test.after(() => {
  for (const [key, value] of Object.entries(saved)) { if (value === undefined) delete process.env[key]; else process.env[key] = value; }
  rmSync(work, { force: true, recursive: true });
});

const host = "127.0.0.1:30141";
const get = (query) => new Request(`http://${host}/api/polyforge/params${query}`, { method: "GET", headers: { host, origin: `http://${host}` } });
const argv = () => readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
const reset = (m = "view") => { writeFileSync(mode, m); writeFileSync(log, ""); };

test("the route is GET-only, gated in-route, force-dynamic and never 5xx; the ref is validated before any spawn", () => {
  const handlers = [...source.matchAll(/export (?:async )?function (\w+)/g)].map((m) => m[1]);
  assert.deepEqual(handlers, ["GET"], "the route exports only GET");
  assert.match(source, /if \(!isApiRequestAllowed\(request\)\)/, "the gate in-route");
  assert.match(source, /export const dynamic = "force-dynamic"/);
  assert.doesNotMatch(source, /status: 5\d\d/, "never 5xx");
  assert.doesNotMatch(source, /console\.(log|error|warn)/, "never logs");
  assert.ok(source.indexOf("isPolyforgeWiRef(wi)") < source.indexOf("await getPolyforgeParams"), "the ref is validated before the spawn");
});

test("a foreign origin is the gate's 403 — the only other non-2xx beside the malformed-ref 400", async () => {
  reset("view");
  const response = await GET(new Request("http://evil.example:9/api/polyforge/params?wi=wi_A1", {
    method: "GET", headers: { host: "evil.example:9", origin: "http://evil.example:9" },
  }));
  assert.equal(response.status, 403);
  assert.deepEqual(await response.json(), { error: "Forbidden" });
  assert.deepEqual(argv(), [], "the gate refuses before any spawn");
});

test("one params read answers the view through the passport", async () => {
  reset("view");
  const response = await GET(get("?wi=wi_A1"));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { params: {
    wi: "wi_A1", posture: "queued",
    steps: [
      { id: "build", status: "not started", params: [{ name: "image", value: "base-img", source: "bundle demo step build params.image" }] },
      { id: "review", status: "not started", params: [
        { name: "image", value: "reg/x:1", source: "step override" },
        { name: "flag", value: "", source: "bundle demo step review params.flag" },
      ] },
    ],
  } });
  assert.deepEqual(argv(), [["--json", "params", "wi_A1"]]);
});

test("a named Cell prefixes --profile; a malformed ref or profile answers 400 before any spawn", async () => {
  reset("view");
  const response = await GET(get("?wi=pi%2342&profile=lab"));
  assert.equal(response.status, 200);
  assert.ok((await response.json()).params, "a slug ref reads too");
  assert.deepEqual(argv().at(-1), ["--profile", "lab", "--json", "params", "pi#42"]);
  for (const bad of ["?wi=", "?wi=not--a--ref", "?wi=wi_A1&profile=Bad%20Name"]) {
    const refused = await GET(get(bad));
    assert.equal(refused.status, 400, `${bad} answers 400`);
  }
  assert.deepEqual(argv().length, 1, "the malformed refs spawn nothing");
});

test("a refusal is the 200 params:null with pf's typed words; an undecodable body is params:null; no 5xx", async () => {
  reset("refuse");
  let response = await GET(get("?wi=wi_A1"));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { params: null, error: "DAEMON-409: the lane no-such does not load on this daemon" });
  reset("garbage");
  response = await GET(get("?wi=wi_A1"));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { params: null });
});
