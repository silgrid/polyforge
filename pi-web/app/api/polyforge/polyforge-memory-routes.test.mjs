import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
const search = await read("./memory/route.ts");
const show = await read("./memory/[id]/route.ts");
const lib = await readFile(new URL("../../../lib/polyforge-memory.ts", import.meta.url), "utf8");

// ---- polyforge#120 u2 (r2#1): the writer filter's HTTP behaviour, over a
// stub pf whose version the test swaps. The route's GET is called directly
// (the polyforge-params-route pattern); the stub logs every argv, so a
// refusal is proven by what never spawned, not by source text. ----
const work = mkdtempSync(join(tmpdir(), "pf-memory-route-"));
const saved = { PF_BIN: process.env.PF_BIN, POLYFORGE_DATA_DIR: process.env.POLYFORGE_DATA_DIR };
const argvLog = join(work, "argv.log");
const versionFile = join(work, "version");
mkdirSync(join(work, "data"), { recursive: true });
writeFileSync(argvLog, "");
writeFileSync(versionFile, "v0.2.3");
writeFileSync(join(work, "pf"), `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(argvLog)}, JSON.stringify(args) + "\\n");
const out = (v) => { process.stdout.write(JSON.stringify(v) + "\\n"); process.exit(0); };
if (args[0] === "--version") { process.stdout.write(fs.readFileSync(${JSON.stringify(versionFile)}, "utf8").trim() + "\\n"); process.exit(0); }
if (args[0] === "--json" && args[1] === "--version") out({ version: fs.readFileSync(${JSON.stringify(versionFile)}, "utf8").trim(), capabilities: [] });
const rest = args.filter((a) => a !== "--json");
if (rest[0] === "config" && rest[1] === "workspace") out({ mapped: true, workspace: "/ws" });
if (rest[0] === "config" && rest[1] === "list") out({ items: [
  { key: "user_id", value: "u1", source: "file" },
  { key: '[workspace."/ws"]', section: true, value: "workbench" },
] });
if (rest[0] === "recall") out({ items: [{ id: "mem_e2eRoute0001", type: "doc.note", title: "attributed", visibility: "project", status: "active",
  tags: [], related: [], work_item_id: null, created_at: "2026-11-17T00:00:00Z", updated_at: "2026-11-17T00:00:00Z", content_len: 3,
  owner_display_name: "E2E Ada", writer_kind: "step",
  writer_details: { work_item: "wi_e2e", step: "build", attempt: 1, model: "pi:e2e", evil: "E2E-NESTED-SENTINEL" } }], total: 1 });
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(join(work, "pf"), 0o755);
process.env.PF_BIN = join(work, "pf");
process.env.POLYFORGE_DATA_DIR = join(work, "data");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { GET } = await jiti.import("./memory/route.ts");
const status = await jiti.import("@/lib/polyforge-status");

test.after(() => {
  for (const [key, value] of Object.entries(saved)) { if (value === undefined) delete process.env[key]; else process.env[key] = value; }
  rmSync(work, { force: true, recursive: true });
});

const host = "127.0.0.1:30141";
const get = (query) => new Request(`http://${host}/api/polyforge/memory${query}`, { method: "GET", headers: { host, origin: `http://${host}` } });
const argv = () => readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
/** Swap the stub's pf version (a new pf is a new probe: refresh the one
 *  presence probe's cache so the route reads the new version). */
async function atVersion(version) {
  writeFileSync(versionFile, version + "\n");
  await status.probePolyforgeVersion(true);
  writeFileSync(argvLog, "");
}
const onlyProbes = () => argv().every((call) => call.includes("--version"));

test("the writer filter's shape refusal: an illegal kind answers 400 with no pf process at all (r2#1)", async () => {
  await atVersion("v0.2.3");
  const refused = await GET(get(`?cwd=/ws&project=workbench&type=*&q=kw&writer_kind=human`));
  assert.equal(refused.status, 400, `an illegal kind must answer 400, got ${refused.status}`);
  assert.deepEqual(argv(), [], "the shape refusal runs before any pf process, the version probe included");
});

test("a legal writer kind on a filter-incapable pf answers 400 naming the 0.2.3 floor — no config or recall spawn, on both faces (r2#1)", async () => {
  await atVersion("v0.2.2");
  const browse = await GET(get(`?cwd=/ws&project=workbench&type=*&writer_kind=step`));
  assert.equal(browse.status, 400, `the browse face must refuse, got ${browse.status}`);
  const browseBody = await browse.json();
  assert.match(browseBody.error, /0\.2\.3/, "the refusal names the filter's own floor, never the browse floor (Q4)");
  assert.ok(browseBody.error.includes("v0.2.2"), "the refusal names the found version");
  assert.ok(onlyProbes(), `only the version probe may run under a refused filter: ${JSON.stringify(argv())}`);
  const keyword = await GET(get(`?cwd=/ws&project=workbench&type=*&q=kw&writer_kind=session`));
  assert.equal(keyword.status, 400, `the search face must refuse too, got ${keyword.status}`);
  assert.match((await keyword.json()).error, /0\.2\.3/);
  assert.ok(onlyProbes(), `the search refusal spawns nothing past the probe: ${JSON.stringify(argv())}`);
  // A v2.20.x pf that browses meets the same refusal (the retired line is
  // below the filter's floor, never below the browse floor).
  await atVersion("v2.20.16");
  const retired = await GET(get(`?cwd=/ws&project=workbench&type=*&writer_kind=session`));
  assert.equal(retired.status, 400);
  assert.match((await retired.json()).error, /0\.2\.3/);
  assert.ok(onlyProbes(), JSON.stringify(argv()));
});

test("a legal writer kind on a filter-capable pf rides the recall argv on both faces and the hits carry the attribution (r2#1)", async () => {
  await atVersion("v0.2.3");
  const browse = await GET(get(`?cwd=/ws&project=workbench&type=*&writer_kind=step`));
  assert.equal(browse.status, 200, `the browse face must answer, got ${browse.status}`);
  const body = await browse.json();
  assert.equal(body.writerFilterSupported, true, "the capability answer states the filter's own gate");
  assert.equal(body.hits.length, 1);
  assert.equal(body.hits[0].author_display_name, "E2E Ada", "the passported hit carries the author's display name");
  assert.equal(body.hits[0].writer_kind, "step");
  assert.deepEqual(body.hits[0].writer_details, { work_item: "wi_e2e", step: "build", attempt: 1, model: "pi:e2e" },
    "an unknown writer-details key never passes the passport");
  assert.ok(!JSON.stringify(body).includes("E2E-NESTED-SENTINEL") && !JSON.stringify(body).includes("owner_user_id"));
  assert.deepEqual(argv().at(-1), ["--json", "recall", "--project", "workbench", "--limit", "20", "--sort", "updated", "--writer-kind", "step"],
    `the browse recall carries the kind: ${JSON.stringify(argv())}`);
  const keyword = await GET(get(`?cwd=/ws&project=workbench&type=*&q=kw&writer_kind=session`));
  assert.equal(keyword.status, 200);
  assert.deepEqual(argv().at(-1), ["--json", "recall", "--project", "workbench", "--limit", "200", "--writer-kind", "session", "kw"],
    `the keyword recall carries the same kind, the keyword last: ${JSON.stringify(argv())}`);
});


test("every memory route re-checks isApiRequestAllowed in-route and is dynamic", () => {
  for (const [name, source] of [["search", search], ["show", show]]) {
    assert.match(source, /import \{ isApiRequestAllowed \} from "@\/lib\/request-security"/, `${name} imports the gate`);
    assert.match(source, /if \(!isApiRequestAllowed\(request\)\)/, `${name} checks the gate in the route`);
    assert.match(source, /NextResponse\.json\(\{ error: "Forbidden" \}, \{ status: 403 \}\)/, `${name} answers a JSON 403`);
    assert.match(source, /export const dynamic = "force-dynamic"/, `${name} is force-dynamic`);
  }
});

test("validation and the id check run before any pf spawn call (source order)", () => {
  // The search face: its validateMemorySearch call must precede ITS OWN
  // capability probe (the scope face above it needs no validation).
  const validateCall = search.indexOf("const validated = validateMemorySearch({");
  const capabilityCalls = [...search.matchAll(/await readPolyforgeMemoryCapability\(\)/g)].map((m) => m.index);
  const searchCapability = capabilityCalls[capabilityCalls.length - 1];
  const projects = search.indexOf("readPolyforgeMemoryProjects(validated.value.cwd, pin)");
  const membership = search.indexOf("scope.projects.includes(validated.value.project)");
  const spawn = search.indexOf("searchPolyforgeMemory(validated.value, pin)");
  assert.ok(validateCall >= 0 && searchCapability > validateCall, "shape validation precedes the search face's version probe (D4)");
  assert.ok(projects > searchCapability, "the capability gate precedes the config read");
  assert.ok(membership > projects, "membership is judged on the config read that defines the dropdown");
  assert.ok(spawn > membership, "the recall spawn runs last, after every refusal path");
  const idCheck = show.indexOf("isPolyforgeMemoryId(id)");
  const showCapability = show.indexOf("readPolyforgeMemoryCapability()");
  const showSpawn = show.indexOf("showPolyforgeMemory(id, pin)");
  assert.ok(idCheck >= 0 && showCapability > idCheck && showSpawn > showCapability, "the id check precedes every spawn on the show route");
});

test("no memory route answers 5xx, logs, or echoes the dropped identity fields", () => {
  for (const [name, source] of [["search", search], ["show", show]]) {
    assert.doesNotMatch(source, /status:\s*5\d\d/, `${name} carries no 5xx literal`);
    assert.doesNotMatch(source, /console\.(log|error|warn)/, `${name} never logs`);
    assert.doesNotMatch(source, /owner_user_id|project_id|content_sha|content_excerpt/, `${name} never names a dropped field`);
    assert.doesNotMatch(source, /process\.env/, `${name} never builds text from an environment value`);
  }
});

test("the search face never selects or forwards a body; only the show face does", () => {
  assert.doesNotMatch(search, /content/, "the search route carries no content notion at all");
  assert.match(lib, /sanitizeMemorySearchAnswer/, "the search answer goes through the hit passport");
  assert.match(lib, /sanitizeMemoryDetail/, "the single memory goes through the detail passport");
  assert.match(show, /memory: null/, "the show route degrades to an explicit null memory");
});

test("the search route answers 400 through the shared validator and the show route through the id check", () => {
  assert.match(search, /return NextResponse\.json\(\{ error: validated\.error \}, \{ status: 400 \}\)/);
  assert.match(search, /return NextResponse\.json\(\{ error: "project must be one of the workspace's projects" \}, \{ status: 400 \}\)/);
  assert.match(show, /return NextResponse\.json\(\{ error: "id must be a memory id" \}, \{ status: 400 \}\)/);
  // The non-2xx paths are exactly the gate's 403 and these 400s.
  for (const source of [search, show]) {
    const non2xx = [...source.matchAll(/status: (\d{3})/g)].map((m) => Number(m[1]));
    assert.ok(non2xx.every((status) => status === 400 || status === 403), `only 400/403 literals: ${non2xx}`);
  }
});

test("every failure degrades to 200 through an error or reason field, never a capability invention", () => {
  for (const [name, source] of [["search", search], ["show", show]]) {
    assert.match(source, /error instanceof PolyforgeMemoryError \? error(?:\.message| : new PolyforgeMemoryError)/, `${name} surfaces pf's own bounded text`);
    const catches = [...source.matchAll(/catch \(error\)/g)];
    assert.ok(catches.length >= 1, `${name} catches spawn failures`);
    assert.doesNotMatch(source, /capable: true,\s*error/, `${name} never invents a capability on failure`);
  }
});

test("the browse face (no q, a project) validates, gates, checks membership and only then spawns one recall", () => {
  const scopeGuard = search.indexOf("if (q === null && project === null)");
  const browseGuard = search.indexOf("if (q === null) {");
  const validate = search.indexOf("const browse = validateMemoryBrowse({");
  const capability = search.indexOf("await readPolyforgeMemoryCapability()", validate);
  const projects = search.indexOf("readPolyforgeMemoryProjects(browse.value.cwd, pin)");
  const membership = search.indexOf("browseScope.projects.includes(browse.value.project)");
  const spawn = search.indexOf("browsePolyforgeMemory(browse.value, pin)");
  const searchValidate = search.indexOf("const validated = validateMemorySearch({");
  assert.ok(scopeGuard >= 0 && browseGuard > scopeGuard, "the scope face (no project) is decided before the browse face");
  assert.ok(validate > browseGuard && capability > validate, "shape validation precedes the browse face's version probe");
  assert.ok(projects > capability && membership > projects && spawn > membership, "the recall spawn runs after every refusal path");
  assert.ok(searchValidate > spawn, "the keyword search face is unchanged and comes after browse");
  assert.match(search, /return NextResponse\.json\(\{ error: browse\.error \}, \{ status: 400 \}\)/);
  assert.match(search, /total: page\.total, next_cursor: page\.next_cursor/, "the browse answer carries total and the cursor");
  assert.match(lib, /sanitizeMemoryBrowseAnswer/, "the browse answer goes through the page passport");
});

test("the memory routes export GET only", () => {
  for (const [name, source] of [["search", search], ["show", show]]) {
    const handlers = [...source.matchAll(/export (?:async )?function (\w+)/g)].map((m) => m[1]);
    assert.deepEqual(handlers, ["GET"], `${name} exports only GET`);
  }
});
