import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
const route = await read("./cards/route.ts");
const reader = await read("../../../lib/polyforge-cards.ts");

test("the card route is GET-only, gated in-route and dynamic", () => {
  const handlers = [...route.matchAll(/export (?:async )?function (\w+)/g)].map((m) => m[1]);
  assert.deepEqual(handlers, ["GET"], "only GET (any other method is 405)");
  assert.match(route, /import \{ isApiRequestAllowed \} from "@\/lib\/request-security"/);
  assert.match(route, /if \(!isApiRequestAllowed\(request\)\)/);
  assert.match(route, /NextResponse\.json\(\{ error: "Forbidden" \}, \{ status: 403 \}\)/);
  assert.match(route, /export const dynamic = "force-dynamic"/);
});

test("the only non-2xx answer is the gate's 403; no log, no env, no write", () => {
  const statuses = [...route.matchAll(/status: (\d{3})/g)].map((m) => Number(m[1]));
  assert.deepEqual(statuses, [403], `only the 403 literal: ${statuses}`);
  assert.doesNotMatch(route, /console\.(log|error|warn)/);
  assert.doesNotMatch(route, /process\.env/);
  assert.doesNotMatch(route, /request\.json|formData|writeFile|appendFile/);
  assert.match(route, /error instanceof PolyforgeError \? error\.message : "polyforge is unavailable"/, "only pf's own bounded text surfaces");
  assert.match(route, /cards: null/, "a degraded read answers an explicit null card list");
});

test("the job set comes from pf's own job list; the client supplies only validated cursors", () => {
  const presence = route.indexOf("probePolyforgePresent()");
  const ops = route.indexOf("getPolyforgeOpsStatus()");
  const cursors = route.indexOf("parseCardCursors(");
  const cards = route.indexOf("readPolyforgeCards(status.jobs, cursors");
  assert.ok(presence >= 0 && ops > presence && cursors > ops && cards > cursors, "presence, ops status, cursor parse, then the reader");
  const params = [...route.matchAll(/searchParams\.get\("(\w+)"\)/g)].map((m) => m[1]);
  assert.deepEqual(params, ["cursors"], "no wi/path/file query is read");
});

test("the reader validates the wi id and finds it in the daemon's record before any path is built", () => {
  assert.match(reader, /if \(!isPolyforgeWorkItemId\(job\.wi\)\) return bareCard\(job\);/);
  assert.match(reader, /join\(root, "events", `wi-\$\{job\.wi\}\.jsonl`\)/);
  // the record comes from the job's own Cell (its WorkRef.profile)
  assert.match(reader, /\(await recordsOf\(cellRoot\)\)\.get\(job\.wi\)/);
  assert.match(reader, /realInside\(candidate, roots\)/, "a transcript must resolve inside the capsule or the data root");
  assert.doesNotMatch(reader, /console\.(log|error|warn)/);
  assert.doesNotMatch(reader, /writeFile|appendFile|unlink|rmSync|mkdir/, "the reader never writes");
});

test("the route runs end to end against a fixture: decodable 200 with cards, malformed cursors ignored", async (t) => {
  const { mkdtempSync, rmSync, mkdirSync, writeFileSync } = await import("node:fs");
  const { tmpdir } = await import("node:os");
  const { join } = await import("node:path");
  const { createJiti } = await import("jiti");
  const jiti = createJiti(import.meta.url);
  const lib = await jiti.import("../../../lib/polyforge-cards.ts");
  const pure = await jiti.import("../../../lib/polyforge-cards-pure.ts");
  const root = mkdtempSync(join(tmpdir(), "pf-cards-route-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(join(root, "events"));
  writeFileSync(join(root, "events", "wi-wi_route01.jsonl"), `${JSON.stringify({ at: "2026-10-30T01:00:00Z", kind: "job_started" })}\n`);
  const job = { wi: "wi_route01", slug: "w#1", status: "queued", stepAgeSec: 0, idleAgeSec: 0, candidate: "", spawnN: 0 };
  for (const raw of [null, "not-json", JSON.stringify({ "../x": "../y" }), JSON.stringify({ wi_route01: "../../etc/passwd" })]) {
    const cards = await lib.readPolyforgeCards([job], pure.parseCardCursors(raw), Date.now(), root);
    const body = JSON.parse(JSON.stringify({ pfPresent: true, cards }));
    assert.equal(body.cards.length, 1);
    assert.equal(body.cards[0].startedAt, "2026-10-30T01:00:00Z");
    assert.deepEqual(body.cards[0].progress, { available: false, entries: [] });
  }
});

test("the response carries narration only: no tool entry, name or command reaches the wire", async (t) => {
  const { mkdtempSync, rmSync, mkdirSync, writeFileSync } = await import("node:fs");
  const { tmpdir } = await import("node:os");
  const { join } = await import("node:path");
  const { createJiti } = await import("jiti");
  const jiti = createJiti(import.meta.url);
  const lib = await jiti.import("../../../lib/polyforge-cards.ts");
  const root = mkdtempSync(join(tmpdir(), "pf-cards-route-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const capsule = join(root, "capsules", "wi_route02");
  for (const dir of [join(root, "events"), join(capsule, "runtime-state"), join(capsule, "session")]) mkdirSync(dir, { recursive: true });
  const transcript = join(capsule, "session", "s.jsonl");
  writeFileSync(join(root, "runtime-state.json"), JSON.stringify({ version: 1, jobs: [{ work_item_id: "wi_route02", capsule_dir: capsule }] }));
  writeFileSync(join(capsule, "runtime-state", "execution.json"), JSON.stringify({ current_step: "code", steps: { code: { status: "in_progress" } } }));
  writeFileSync(join(capsule, "runtime-state", "spawn-journal.jsonl"), `${JSON.stringify({ step_id: "code", transcript, harness: "pi" })}\n`);
  writeFileSync(transcript, [
    { type: "message", id: "a", message: { role: "assistant", content: [{ type: "text", text: "NARRATION-ONE" }, { type: "toolCall", id: "c", name: "ROUTE-TOOL-NAME", arguments: { command: "ROUTE-COMMAND --x" } }] } },
    { type: "message", id: "r", message: { role: "toolResult", toolName: "ROUTE-TOOL-NAME", content: [{ type: "text", text: "ROUTE-RESULT" }] } },
  ].map((row) => JSON.stringify(row)).join("\n") + "\n");
  const job = { wi: "wi_route02", slug: "w#2", status: "running", stepAgeSec: 0, idleAgeSec: 0, candidate: "", spawnN: 1 };
  const body = JSON.stringify({ pfPresent: true, cards: await lib.readPolyforgeCards([job], new Map(), Date.now(), root) });
  const parsed = JSON.parse(body);
  assert.deepEqual(parsed.cards[0].progress.entries.map((entry) => entry.text), ["NARRATION-ONE"]);
  assert.equal(parsed.cards[0].currentStep, "code");
  for (const leak of ["ROUTE-TOOL-NAME", "ROUTE-COMMAND", "ROUTE-RESULT", '"kind"', '"tool"']) assert.ok(!body.includes(leak), `the response leaks ${leak}`);
});
