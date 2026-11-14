// carries: app/api/polyforge/override/*, lib/polyforge-overrides*
// polyforge-override-route.test.mjs (u8, polyforge#25): the override face
// over the pf subprocess — GET runs `pf [--profile p] --json override <wi>`
// (the per-step view), POST runs one action's argv; a refusal answers 409
// {ok:false, error} with pf's typed words, never 5xx; the wi and profile
// are validated before any spawn and the host gate refuses first.
import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const work = mkdtempSync(join(tmpdir(), "pf-override-route-"));
const saved = { PF_BIN: process.env.PF_BIN, POLYFORGE_DATA_DIR: process.env.POLYFORGE_DATA_DIR };
const log = join(work, "argv.log");
const mode = join(work, "mode");
mkdirSync(join(work, "data"));
writeFileSync(log, "");
writeFileSync(mode, "view");
writeFileSync(join(work, "pf"), `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
if (args[0] === "--version") { process.stdout.write("v2.21.0\\n"); process.exit(0); }
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(args) + "\\n");
const mode = fs.readFileSync(${JSON.stringify(mode)}, "utf8");
if (mode === "refuse-409") { process.stderr.write(JSON.stringify({ code: "DAEMON-409", detail: "step merge has a live dispatch (its status is in_progress): the overlay is for steps without one" }) + "\\n"); process.exit(1); }
if (mode === "refuse-400") { process.stderr.write(JSON.stringify({ code: "DAEMON-400", detail: "models: the pi model \\"absent\\" is not in this Cell's step-agent models.json" }) + "\\n"); process.exit(1); }
if (mode === "garbage") { process.stdout.write("not json\\n"); process.exit(0); }
if (mode === "view") {
  process.stdout.write(JSON.stringify({ work_item: "wi_A1", posture: "running", steps: [
    { id: "build", status: "in_progress", skippable: false },
    { id: "merge", status: "pending", skippable: true, models: ["pi:pf25/m-1"], models_by: "usr_1", effective: ["pi:pf25/m-1"], source: "step override" },
  ] }) + "\\n");
  process.exit(0);
}
process.stdout.write(JSON.stringify({ work_item: "wi_A1", changed: true, posture: "running", published: true, step: { id: "merge", status: "pending", skippable: true, models: ["pi:pf25/m-1"], models_by: "usr_2" } }) + "\\n");
`);
chmodSync(join(work, "pf"), 0o755);
process.env.PF_BIN = join(work, "pf");
process.env.POLYFORGE_DATA_DIR = join(work, "data");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { GET, POST } = await jiti.import("./override/route.ts");
const source = await readFileSync(new URL("./override/route.ts", import.meta.url), "utf8");

test.after(() => {
  for (const [key, value] of Object.entries(saved)) { if (value === undefined) delete process.env[key]; else process.env[key] = value; }
  rmSync(work, { force: true, recursive: true });
});

const host = "127.0.0.1:30141";
const get = (query) => new Request(`http://${host}/api/polyforge/override${query}`, { method: "GET", headers: { host, origin: `http://${host}` } });
const post = (body, type = "application/json") => new Request(`http://${host}/api/polyforge/override`, {
  method: "POST", headers: { host, origin: `http://${host}`, "content-type": type }, body: typeof body === "string" ? body : JSON.stringify(body),
});
const argv = () => readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
const reset = (m = "view") => { writeFileSync(mode, m); writeFileSync(log, ""); };

test("the route re-checks the host/origin gate in-route, GET and POST only, never 5xx", () => {
  assert.match(source, /isApiRequestAllowed\(request\)/);
  assert.match(source, /hasJsonContentType\(request\)/, "the POST's content-type gate");
  assert.doesNotMatch(source, /export async function (PUT|DELETE|PATCH)/);
  assert.doesNotMatch(source, /status: 5\d\d/);
});

test("GET runs `pf --json override <wi>` (with --profile for a named Cell) and answers the sanitized view", async () => {
  reset("view");
  let response = await GET(get("?wi=wi_A1"));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { status: {
    wi: "wi_A1", posture: "running",
    steps: [
      { id: "build", status: "in_progress", skippable: false },
      { id: "merge", status: "pending", skippable: true, models: ["pi:pf25/m-1"], modelsBy: "usr_1", effective: ["pi:pf25/m-1"], source: "step override" },
    ],
  } });
  response = await GET(get("?wi=demo%231&profile=lab"));
  await response.json();
  assert.deepEqual(argv(), [["--json", "override", "wi_A1"], ["--profile", "lab", "--json", "override", "demo#1"]],
    "the wi and the profile go to pf as typed (a slug is a valid ref)");
});

test("GET answers 200 {status:null} on every pf failure — garbage, a refusal — never 5xx", async () => {
  reset("garbage");
  let response = await GET(get("?wi=wi_A1"));
  assert.equal(response.status, 200);
  assert.equal((await response.json()).status, null);
  reset("refuse-409");
  response = await GET(get("?wi=wi_A1"));
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.status, null);
  assert.match(body.error, /DAEMON-409/);
});

test("POST runs one action's argv and answers the result; --models and the skip switches reach pf", async () => {
  reset("write");
  let response = await POST(post({ wi: "wi_A1", action: { kind: "models", step: "merge", models: ["pi:pf25/m-1", "pi:pf25/m-2"] } }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { ok: true, result: {
    wi: "wi_A1", changed: true, posture: "running", published: true,
    step: { id: "merge", status: "pending", skippable: true, models: ["pi:pf25/m-1"], modelsBy: "usr_2" },
  } });
  assert.deepEqual(argv(), [["--json", "override", "wi_A1", "--step", "merge", "--models", "pi:pf25/m-1,pi:pf25/m-2"]]);
  reset("write");
  response = await POST(post({ wi: "wi_A1", profile: "lab", action: { kind: "skip", step: "merge", value: true } }));
  await response.json();
  assert.deepEqual(argv(), [["--profile", "lab", "--json", "override", "wi_A1", "--step", "merge", "--skip"]]);
  reset("write");
  response = await POST(post({ wi: "wi_A1", action: { kind: "clearModels", step: "merge" } }));
  await response.json();
  assert.deepEqual(argv(), [["--json", "override", "wi_A1", "--step", "merge", "--clear-models"]]);
});

test("pf's refusal comes back 409 {ok:false, error} with the typed envelope's words", async () => {
  reset("refuse-409");
  let response = await POST(post({ wi: "wi_A1", action: { kind: "skip", step: "merge", value: true } }));
  assert.equal(response.status, 409);
  assert.deepEqual(await response.json(), { ok: false, error: "DAEMON-409: step merge has a live dispatch (its status is in_progress): the overlay is for steps without one" });
  reset("refuse-400");
  response = await POST(post({ wi: "wi_A1", action: { kind: "models", step: "merge", models: ["pi:absent"] } }));
  assert.equal(response.status, 409);
  assert.deepEqual(await response.json(), { ok: false, error: 'DAEMON-400: models: the pi model "absent" is not in this Cell\'s step-agent models.json' });
});

test("a bad wi or profile, no action, an unknown kind or an empty chain is a 400 before any spawn; garbage bodies stay decodable", async () => {
  reset();
  for (const body of [
    { wi: "wi_A1; rm -rf" },
    { wi: "wi_A1", profile: "Bad Profile", action: { kind: "skip", step: "merge", value: true } },
    { wi: "wi_A1" },
    { wi: "wi_A1", action: { kind: "nope", step: "merge" } },
    { wi: "wi_A1", action: { kind: "models", step: "merge", models: [] } },
    { wi: "wi_A1", action: { kind: "models", step: "merge", models: ["pi:a,b"] } },
  ]) {
    const response = await POST(post(body));
    assert.equal(response.status, 400, JSON.stringify(body));
    assert.equal(typeof (await response.json()).error, "string");
  }
  assert.deepEqual(argv(), [], "nothing is spawned from a refused body");
  const bad = await POST(post("not json", "text/plain"));
  assert.ok(bad.status === 405 || bad.status === 415 || bad.status === 400, "a non-JSON content type is refused before any spawn");
});
