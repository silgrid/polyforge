import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

// source pins of the two read-only Work › 工单列表 routes.
const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
const list = await read("./work-items/route.ts");
const show = await read("./work-items/[id]/route.ts");

test("every work-item route is GET-only, gated in-route and dynamic", () => {
  for (const [name, source] of [["list", list], ["show", show]]) {
    const handlers = [...source.matchAll(/export (?:async )?function (\w+)/g)].map((m) => m[1]);
    assert.deepEqual(handlers, ["GET"], `${name} exports only GET (any other method is 405)`);
    assert.match(source, /import \{ isApiRequestAllowed \} from "@\/lib\/request-security"/, `${name} imports the gate`);
    assert.match(source, /if \(!isApiRequestAllowed\(request\)\)/, `${name} checks the gate in the route`);
    assert.match(source, /NextResponse\.json\(\{ error: "Forbidden" \}, \{ status: 403 \}\)/, `${name} answers a JSON 403`);
    assert.match(source, /export const dynamic = "force-dynamic"/, `${name} is force-dynamic`);
  }
});

test("the gate, then the id check, then the floor, then the one spawn; the list: floor then its one spawn", () => {
  const gate = show.indexOf("isApiRequestAllowed(request)");
  const idCheck = show.indexOf("isPolyforgeWorkItemId(id)");
  const showCapability = show.indexOf("readPolyforgeWorkItemsCapability()");
  const showSpawn = show.indexOf("showPolyforgeWorkItem(id)");
  assert.ok(gate >= 0 && idCheck > gate && showCapability > idCheck && showSpawn > showCapability, "gate, id check, floor, then the one spawn");
  assert.equal(show.split("showPolyforgeWorkItem(").length - 1, 1, "exactly one detail read");
  const listCapability = list.indexOf("readPolyforgeWorkItemsCapability()");
  const listSpawn = list.indexOf("listPolyforgeWorkItems()");
  assert.ok(list.indexOf("isApiRequestAllowed(request)") < listCapability && listCapability >= 0 && listSpawn > listCapability, "gate, floor, then the one list read");
  assert.equal(list.split("listPolyforgeWorkItems(").length - 1, 1, "exactly one list read");
});

test("no work-item route answers 5xx, logs, reads env or exposes a write action", () => {
  for (const [name, source] of [["list", list], ["show", show]]) {
    const non2xx = [...source.matchAll(/status: (\d{3})/g)].map((m) => Number(m[1]));
    assert.ok(non2xx.every((status) => status === 400 || status === 403), `${name}: only 400/403 literals: ${non2xx}`);
    assert.doesNotMatch(source, /console\.(log|error|warn)/, `${name} never logs`);
    assert.doesNotMatch(source, /process\.env/, `${name} never builds text from an environment value`);
    assert.doesNotMatch(source, /execFile|spawn\(|fetch\(/, `${name} spawns nothing itself and calls no aihub HTTP`);
    assert.doesNotMatch(source, /\b(cancel|claim|create|close|edit|update|delete)\w*\(/i, `${name} has no write action`);
    assert.match(source, /error instanceof PolyforgeWorkItemsError \? error : new PolyforgeWorkItemsError/, `${name} surfaces pf's own bounded text`);
  }
  assert.ok(!/status: 400/.test(list), "the list route has no 400");
  assert.match(show, /return NextResponse\.json\(\{ error: "id must be a work item id" \}, \{ status: 400 \}\)/);
  assert.match(show, /item: null/, "the show route degrades to an explicit null item");
});

test("the list route degrades as a whole: every failure is an empty table with a zero total", () => {
  assert.match(list, /const \{ items, total \} = await listPolyforgeWorkItems\(\);\s*return NextResponse\.json\(\{ \.\.\.capability, items, total \}\);/,
    "the rows come from the one passport");
  assert.match(list, /if \(!capability\.capable\) \{\s*return NextResponse\.json\(\{ \.\.\.capability, items: \[\], total: 0 \}\);/);
  assert.match(list, /return NextResponse\.json\(\{ \.\.\.capability, items: \[\], total: 0, error: failure\.message \}\);/);
  assert.match(show, /const item = await showPolyforgeWorkItem\(id\);\s*return NextResponse\.json\(\{ \.\.\.capability, item \}\);/);
});
