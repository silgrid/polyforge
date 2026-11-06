import "#test-env";
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// GET /api/polyforge/progress/tool?wi=&id= — one tool call's
// full masked detail, on demand. The handlers run for real (jiti) against a
// throwaway daemon data root; the source pins cover what a run cannot.
const source = readFileSync(new URL("./progress/tool/route.ts", import.meta.url), "utf8");
const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { GET } = await jiti.import("./progress/tool/route.ts");
const { GET: progressGET } = await jiti.import("./progress/route.ts");

const ORIGIN = "http://127.0.0.1:30141/api/polyforge/progress";
const jsonl = (records) => records.map((r) => JSON.stringify(r)).join("\n") + "\n";
const at = (sec) => new Date(Date.parse("2026-11-01T00:00:00Z") + sec * 1000).toISOString();

function fixture(t) {
  const work = mkdtempSync(join(tmpdir(), "pf-progress-tool-route-"));
  const previous = process.env.POLYFORGE_DATA_DIR;
  const root = join(work, "data");
  mkdirSync(join(root, "events"), { recursive: true });
  process.env.POLYFORGE_DATA_DIR = root;
  t.after(() => {
    if (previous === undefined) delete process.env.POLYFORGE_DATA_DIR;
    else process.env.POLYFORGE_DATA_DIR = previous;
    rmSync(work, { recursive: true, force: true });
  });
  const wi = "wi_tool271a";
  const dir = join(work, "capsules", wi);
  mkdirSync(join(dir, "runtime-state"), { recursive: true });
  mkdirSync(join(dir, "session"), { recursive: true });
  const transcript = join(dir, "session", "s.jsonl");
  writeFileSync(transcript, jsonl([
    { type: "message", id: "c1", timestamp: at(3), message: { role: "assistant", content: [{ type: "toolCall", id: "k1", name: "bash", arguments: { command: "curl --token=SECRET-TOKEN https://x.invalid", extra: `FULL-INPUT ${"i".repeat(4000)}` } }] } },
    { type: "message", id: "r1", timestamp: at(4), message: { role: "toolResult", toolCallId: "k1", isError: false, content: [{ type: "text", text: `FULL-OUTPUT ${"o".repeat(9000)}` }] } },
    { type: "message", id: "c2", timestamp: at(5), message: { role: "assistant", content: [{ type: "toolCall", id: "k2", name: "read", arguments: { path: "PENDING" } }] } },
  ]));
  writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ step_id: "code", spawn_n: 1, started_at: at(0), transcript, harness: "pi" }]));
  writeFileSync(join(root, "runtime-state.json"), JSON.stringify({ version: 1, jobs: [{ work_item_id: wi, capsule_dir: dir }] }));
  return { work, root, wi, dir, transcript };
}

const call = async (handler, path, query, headers = {}) => {
  const response = await handler(new Request(`${ORIGIN}${path}${query}`, { headers: { host: "127.0.0.1:30141", ...headers } }));
  return { status: response.status, text: await response.text() };
};
const get = (query, headers) => call(GET, "/tool", query, headers);
async function toolIds(wi) {
  const { text } = await call(progressGET, "", `?wi=${wi}`);
  return JSON.parse(text).lines.filter((line) => line.kind === "tool").map((line) => line.id);
}

test("the detail route is GET-only, gated in-route like the progress route, dynamic, and never logs, reads env directly or writes", () => {
  const handlers = [...source.matchAll(/export (?:async )?function (\w+)/g)].map((m) => m[1]);
  assert.deepEqual(handlers, ["GET"], "only GET (any other method is 405)");
  assert.match(source, /if \(!isApiRequestAllowed\(request\)\)/);
  assert.match(source, /NextResponse\.json\(\{ error: "Forbidden" \}, \{ status: 403 \}\)/);
  assert.match(source, /export const dynamic = "force-dynamic"/);
  const statuses = [...source.matchAll(/status: (\d{3})/g)].map((m) => Number(m[1])).sort();
  // a fourth 400 refuses a malformed `profile` (the work
  // reference's Cell) before any path is built
  assert.deepEqual(statuses, [400, 400, 400, 400, 403], "only the four 400s and the gate's 403");
  assert.doesNotMatch(source, /console\.(log|error|warn)|process\.env|request\.json|formData|writeFile|appendFile/);
  const params = [...source.matchAll(/\.get\("(\w+)"\)/g)].map((m) => m[1]);
  // `profile` names the work reference's Cell (validated, never a path)
  assert.deepEqual(params, ["wi", "id", "profile"], "no path/file query is read");
  assert.ok(source.indexOf("isProgressToolId(id)") < source.indexOf("readJobIndex(root)"), "both references are validated before any read");
});

test("200 available: the complete masked input and output of exactly that call; a pending call has a null output", async (t) => {
  const f = fixture(t);
  const [first, pending] = await toolIds(f.wi);
  const { status, text } = await get(`?wi=${f.wi}&id=${encodeURIComponent(first)}`);
  assert.equal(status, 200);
  const body = JSON.parse(text);
  assert.equal(body.available, true);
  assert.equal(body.hasResult, true);
  assert.equal(body.tool.name, "bash");
  assert.equal(body.tool.status, "ok");
  assert.equal(body.tool.durationMs, 1000);
  assert.ok(body.tool.input.includes(`FULL-INPUT ${"i".repeat(4000)}`), "the input is uncapped");
  assert.ok(body.tool.input.includes("token=***"), "and masked");
  assert.equal(body.tool.output, `FULL-OUTPUT ${"o".repeat(9000)}`, "the output is uncapped");
  assert.ok(!text.includes("SECRET-TOKEN") && !text.includes(f.work), "no raw credential and no path");
  const open = JSON.parse((await get(`?wi=${f.wi}&id=${encodeURIComponent(pending)}`)).text);
  assert.deepEqual([open.available, open.hasResult, open.tool.output, open.tool.status], [true, false, null, "running"]);
});

test("400 before any read: a malformed wi, a malformed id, a wi with neither index record nor event log", async (t) => {
  const f = fixture(t);
  const [first] = await toolIds(f.wi);
  for (const query of [`?wi=${encodeURIComponent("../../etc/passwd")}&id=${first}`, `?id=${first}`, `?wi=${f.wi}&id=not-an-id`, `?wi=${f.wi}`,
    `?wi=${f.wi}&id=${encodeURIComponent(`../${first}`)}`, `?wi=wi_missing01&id=${first}`]) {
    const { status, text } = await get(query);
    assert.equal(status, 400, query);
    assert.ok(!text.includes(f.work) && !text.includes("FULL-"), `nothing read or named for ${query}`);
  }
  writeFileSync(join(f.root, "events", "wi-wi_missing01.jsonl"), jsonl([{ at: at(1), kind: "step_started", body: { step: "x" } }]));
  const dropped = await get(`?wi=wi_missing01&id=${first}`);
  assert.equal(dropped.status, 200, "a dropped job with an event log is a known reference");
  assert.deepEqual(JSON.parse(dropped.text).available, false, "but has no transcript to read");
});

test("200 available:false, never 5xx and never a path: an unreadable index, a moved transcript, an unknown call", async (t) => {
  const f = fixture(t);
  const [first] = await toolIds(f.wi);
  const answers = [];
  answers.push(await get(`?wi=${f.wi}&id=0123456789abcdef.0.0`));
  renameSync(f.transcript, `${f.transcript}.gone`);
  answers.push(await get(`?wi=${f.wi}&id=${first}`));
  renameSync(`${f.transcript}.gone`, f.transcript);
  writeFileSync(join(f.root, "runtime-state.json"), "{not json");
  answers.push(await get(`?wi=${f.wi}&id=${first}`));
  for (const { status, text } of answers) {
    assert.equal(status, 200);
    const body = JSON.parse(text);
    assert.equal(body.available, false);
    assert.ok(typeof body.error === "string" && body.error.length > 0);
    assert.deepEqual(Object.keys(body).sort(), ["available", "error"]);
    assert.ok(!text.includes(f.work) && !text.includes("/session/") && !text.includes("/capsules/"), text);
  }
});

test("a cross-site request gets the progress route's own 403, with no detail", async (t) => {
  const f = fixture(t);
  const [first] = await toolIds(f.wi);
  const cross = { "sec-fetch-site": "cross-site", origin: "http://evil.invalid" };
  const refused = await get(`?wi=${f.wi}&id=${first}`, cross);
  const progress = await call(progressGET, "", `?wi=${f.wi}`, cross);
  assert.equal(refused.status, 403);
  assert.deepEqual([refused.status, refused.text], [progress.status, progress.text], "the same gate answer");
  assert.ok(!refused.text.includes("FULL-"));
});
