import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
const search = await read("./memory/route.ts");
const show = await read("./memory/[id]/route.ts");
const lib = await readFile(new URL("../../../lib/polyforge-memory.ts", import.meta.url), "utf8");

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
  const projects = search.indexOf("readPolyforgeMemoryProjects(validated.value.cwd)");
  const membership = search.indexOf("scope.projects.includes(validated.value.project)");
  const spawn = search.indexOf("searchPolyforgeMemory(validated.value)");
  assert.ok(validateCall >= 0 && searchCapability > validateCall, "shape validation precedes the search face's version probe (D4)");
  assert.ok(projects > searchCapability, "the capability gate precedes the config read");
  assert.ok(membership > projects, "membership is judged on the config read that defines the dropdown");
  assert.ok(spawn > membership, "the recall spawn runs last, after every refusal path");
  const idCheck = show.indexOf("isPolyforgeMemoryId(id)");
  const showCapability = show.indexOf("readPolyforgeMemoryCapability()");
  const showSpawn = show.indexOf("showPolyforgeMemory(id)");
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
  const projects = search.indexOf("readPolyforgeMemoryProjects(browse.value.cwd)");
  const membership = search.indexOf("browseScope.projects.includes(browse.value.project)");
  const spawn = search.indexOf("browsePolyforgeMemory(browse.value)");
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
