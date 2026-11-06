import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
const list = await read("./tasks/route.ts");
const show = await read("./tasks/[id]/route.ts");

test("every task route is GET-only, gated in-route and dynamic", () => {
  for (const [name, source] of [["list", list], ["show", show]]) {
    const handlers = [...source.matchAll(/export (?:async )?function (\w+)/g)].map((m) => m[1]);
    assert.deepEqual(handlers, ["GET"], `${name} exports only GET (any other method is 405)`);
    assert.match(source, /import \{ isApiRequestAllowed \} from "@\/lib\/request-security"/, `${name} imports the gate`);
    assert.match(source, /if \(!isApiRequestAllowed\(request\)\)/, `${name} checks the gate in the route`);
    assert.match(source, /NextResponse\.json\(\{ error: "Forbidden" \}, \{ status: 403 \}\)/, `${name} answers a JSON 403`);
    assert.match(source, /export const dynamic = "force-dynamic"/, `${name} is force-dynamic`);
  }
});

test("the id check and the floor gate run before the show spawn; the floor gate before the list spawn", () => {
  const idCheck = show.indexOf("isPolyforgeTaskId(id)");
  const showCapability = show.indexOf("readPolyforgeTasksCapability()");
  const showSpawn = show.indexOf("showPolyforgeTask(id)");
  assert.ok(idCheck >= 0 && showCapability > idCheck && showSpawn > showCapability, "id check, then floor, then the one spawn");
  const listCapability = list.indexOf("readPolyforgeTasksCapability()");
  const listSpawn = list.indexOf("listPolyforgeTasks()");
  assert.ok(listCapability >= 0 && listSpawn > listCapability, "floor, then the one list spawn");
});

test("no task route answers 5xx, logs, reads env or exposes a write action", () => {
  for (const [name, source] of [["list", list], ["show", show]]) {
    const non2xx = [...source.matchAll(/status: (\d{3})/g)].map((m) => Number(m[1]));
    assert.ok(non2xx.every((status) => status === 400 || status === 403), `${name}: only 400/403 literals: ${non2xx}`);
    assert.doesNotMatch(source, /console\.(log|error|warn)/, `${name} never logs`);
    assert.doesNotMatch(source, /process\.env/, `${name} never builds text from an environment value`);
    assert.doesNotMatch(source, /spawnPolyforgeTask|cancelPolyforgeTask/, `${name} has no write action`);
    assert.match(source, /error instanceof PolyforgeTasksError \? error : new PolyforgeTasksError/, `${name} surfaces pf's own bounded text`);
  }
  assert.match(show, /return NextResponse\.json\(\{ error: "id must be a task id" \}, \{ status: 400 \}\)/);
  assert.match(show, /task: null/, "the show route degrades to an explicit null task");
});

test("the list route answers only the sanitized rows, timing facts included, never a raw pf row", async () => {
  // contract extension, not a relaxation: beside the passport
  // rows the answer names the resolved Cell (a profile name from `pf profile`),
  // never a raw pf row.
  assert.match(list, /const tasks = await listPolyforgeTasks\(\);[\s\S]{0,200}?const cell = await readPolyforgeResolvedCell\(\);\s*return NextResponse\.json\(\{ \.\.\.capability, tasks, \.\.\.\(cell \? \{ cell \} : \{\}\) \}\);/, "the rows come from the one passport");
  const libSource = await read("../../../lib/polyforge-tasks.ts");
  const passport = libSource.slice(libSource.indexOf("export function sanitizeTaskRow("), libSource.indexOf("export function sanitizeTaskListAnswer("));
  // / owner-intended 2026-10-30 contract extension, not a relaxation:
  // report_tail and reporter join the forwarded set; the full report stays unread.
  for (const field of ["started_at", "ended_at", "wall_consumed_ns", "report_tail", "reporter"]) assert.match(passport, new RegExp(`sanitized\\.${field} =`), `the row passport forwards ${field}`);
  assert.doesNotMatch(passport.replace(/\breport_tail\b|\breporter\b/g, ""), /report|created_at|origin|delivered|tokens_consumed/, "no other raw field is read by the row passport");
});
