import "#test-env";
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// GET /api/polyforge/progress?wi= — the job detail's
// execution-progress read face (the whole-job snapshot). The handler runs for real (jiti) against a
// throwaway daemon data root; the source pins cover what a run cannot (no
// other handler, no log/env/write).
const source = readFileSync(new URL("./progress/route.ts", import.meta.url), "utf8");
const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { GET } = await jiti.import("./progress/route.ts");

const BASE = "http://127.0.0.1:30141/api/polyforge/progress";
const SECRETS = ["SECRET-TOOL-RESULT", "SECRET-THINKING", "SECRET-PROMPT", "SECRET-TOKEN", "SECRET-SYSTEM"];
const jsonl = (records) => records.map((r) => JSON.stringify(r)).join("\n") + "\n";

function fixture(t) {
  const work = mkdtempSync(join(tmpdir(), "pf-progress-route-"));
  const previous = process.env.POLYFORGE_DATA_DIR;
  const root = join(work, "data");
  mkdirSync(join(root, "events"), { recursive: true });
  process.env.POLYFORGE_DATA_DIR = root;
  t.after(() => {
    if (previous === undefined) delete process.env.POLYFORGE_DATA_DIR;
    else process.env.POLYFORGE_DATA_DIR = previous;
    rmSync(work, { recursive: true, force: true });
  });
  const jobs = [];
  const capsule = (wi, transcript, content) => {
    const dir = join(work, "capsules", wi);
    mkdirSync(join(dir, "runtime-state"), { recursive: true });
    mkdirSync(join(dir, "session"), { recursive: true });
    const path = transcript ?? join(dir, "session", "s.jsonl");
    writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ step_id: "code", spawn_n: 1, started_at: "2026-11-01T00:00:00Z", transcript: path, harness: "pi" }]));
    if (content !== undefined) writeFileSync(path, content);
    jobs.push({ work_item_id: wi, capsule_dir: dir });
    writeFileSync(join(root, "runtime-state.json"), JSON.stringify({ version: 1, jobs }));
    return dir;
  };
  return { work, root, capsule };
}

const get = async (query, init = {}) => {
  // The gate trusts a loopback Host header (as a local browser sends).
  const headers = { host: "127.0.0.1:30141", ...init.headers };
  const response = await GET(new Request(`${BASE}${query}`, { ...init, headers }));
  return { status: response.status, text: await response.text() };
};

test("the progress route is GET-only, gated in-route, dynamic, and never logs, reads env directly or writes", () => {
  const handlers = [...source.matchAll(/export (?:async )?function (\w+)/g)].map((m) => m[1]);
  assert.deepEqual(handlers, ["GET"], "only GET (any other method is 405)");
  assert.match(source, /if \(!isApiRequestAllowed\(request\)\)/);
  assert.match(source, /NextResponse\.json\(\{ error: "Forbidden" \}, \{ status: 403 \}\)/);
  assert.match(source, /export const dynamic = "force-dynamic"/);
  const statuses = [...source.matchAll(/status: (\d{3})/g)].map((m) => Number(m[1])).sort();
  // a third 400 refuses a malformed `profile` (the work
  // reference's Cell) before any path is built
  assert.deepEqual(statuses, [400, 400, 400, 403], "only the three 400s and the gate's 403");
  assert.doesNotMatch(source, /console\.(log|error|warn)|process\.env|request\.json|formData|writeFile|appendFile/);
  const params = [...source.matchAll(/\.get\("(\w+)"\)/g)].map((m) => m[1]);
  // `profile` names the work reference's Cell (validated, never a path)
  assert.deepEqual(params, ["wi", "profile"], "no path/file query is read");
});

test("end to end — the whole-job body: text and thinking narration + one masked tool line + lifecycle lines; no tool result, prompt or raw credential", async (t) => {
  const f = fixture(t);
  const dir = f.capsule("wi_route210a", undefined, jsonl([
    { type: "session", id: "s" },
    { type: "message", id: "u", message: { role: "user", content: "SECRET-PROMPT" } },
    { type: "message", id: "n", message: { role: "assistant", content: [{ type: "text", text: "ROUTE-NARRATION token=SECRET-TOKEN" }] } },
    { type: "message", id: "tc", message: { role: "assistant", content: [{ type: "thinking", thinking: "ROUTE-THINKING token=SECRET-TOKEN" }, { type: "toolCall", id: "c", name: "bash", arguments: { command: "curl --token=SECRET-TOKEN https://x.invalid" } }] } },
    { type: "message", id: "tr", message: { role: "toolResult", toolName: "bash", content: [{ type: "text", text: "SECRET-TOOL-RESULT" }] } },
  ]));
  writeFileSync(join(f.root, "events", "wi-wi_route210a.jsonl"), jsonl([
    { at: "2026-11-01T00:00:00Z", kind: "step_started", body: { step: "code", spawn_n: 1, model: "pi:m" } },
    { at: "2026-11-01T00:00:01Z", kind: "needs_input", body: { step: "code", question: "SECRET-PROMPT" } },
    { at: "2026-11-01T00:00:02Z", kind: "published", body: { version: 1 } },
    { at: "2026-11-01T00:00:03Z", kind: "push_failed", body: { error: "push token=SECRET-TOKEN" } },
  ]));
  const { status, text } = await get("?wi=wi_route210a");
  assert.equal(status, 200);
  const body = JSON.parse(text);
  assert.deepEqual(Object.keys(body).sort(), ["available", "lines", "omitted"]);
  assert.equal(body.available, true);
  assert.equal(body.omitted, 0);
  assert.deepEqual(body.lines.map(({ kind, text: line, variant, tone, event }) => [kind, line.replace(/^\d{2}:\d{2}:\d{2} /, ""), variant, tone, event]), [
    ["event", "▶ code #1 · pi:m", undefined, "accent", "step_started"],
    ["narration", "ROUTE-NARRATION token=***", undefined, undefined, undefined],
    ["narration", "ROUTE-THINKING token=***", "thinking", undefined, undefined],
    ["tool", "bash: curl --token=*** https://x.invalid", undefined, undefined, undefined],
    ["event", "? code", undefined, "amber", "needs_input"],
    ["event", "⚠ push token=***", undefined, "red", "push_failed"],
  ], "untimed transcript records sit at the spawn's start; dropped kinds produce no line");
  // the wire keys per kind — event lines add their time `at`, tool
  // lines the four light tool fields; full input/output never ride this answer.
  assert.deepEqual(body.lines.map((line) => Object.keys(line).sort().join(",")), [
    "at,event,id,kind,text,tone",
    "id,kind,text",
    "id,kind,text,variant",
    "id,kind,text,tool",
    "at,event,id,kind,text,tone",
    "at,event,id,kind,text,tone",
  ]);
  assert.equal(body.lines[0].at, Date.parse("2026-11-01T00:00:00Z"));
  assert.deepEqual(body.lines[3].tool, { name: "bash", argument: "curl --token=*** https://x.invalid", status: "ok" }, "paired with its (id-less) result, no timestamps so no duration");
  for (const secret of SECRETS) assert.ok(!text.includes(secret), `the response leaks ${secret}`);
  for (const word of ["toolResult", "user", "system", dir, f.root]) assert.ok(!text.includes(word), `the response carries ${word}`);
});

test("the route keeps narration whitespace (mask only, no cap) and carries no tool input or output", async (t) => {
  const f = fixture(t);
  const narration = `P1.\n\n- item\n\n\`\`\`\ncode\n\`\`\`\n  indented token=SECRET-TOKEN ${"L".repeat(1500)}`;
  f.capsule("wi_route271a", undefined, jsonl([
    { type: "message", id: "n", message: { role: "assistant", content: [{ type: "text", text: narration }] } },
    { type: "message", id: "tc", message: { role: "assistant", content: [{ type: "toolCall", id: "c", name: "bash", arguments: { command: "ls", extra: "FULL-INPUT-ONLY" } }] } },
    { type: "message", id: "tr", message: { role: "toolResult", toolCallId: "c", isError: true, content: [{ type: "text", text: "SECRET-TOOL-RESULT" }] } },
  ]));
  const { status, text } = await get("?wi=wi_route271a");
  assert.equal(status, 200);
  const body = JSON.parse(text);
  assert.equal(body.lines[0].text, narration.replace("token=SECRET-TOKEN", "token=***"), "exact whitespace, masked, uncapped");
  assert.deepEqual(body.lines[1].tool, { name: "bash", argument: "ls", status: "error" });
  assert.ok(!text.includes("FULL-INPUT-ONLY") && !text.includes("SECRET-TOOL-RESULT") && !text.includes("SECRET-TOKEN"));
});

test("a wrong-shaped or unknown wi is a 400 before any transcript read; no path leaves", async (t) => {
  const f = fixture(t);
  f.capsule("wi_route210b", undefined, jsonl([{ type: "message", id: "n", message: { role: "assistant", content: [{ type: "text", text: "KNOWN-ONLY" }] } }]));
  for (const query of ["", "?wi=", "?wi=../../etc/passwd", "?wi=wi_route210b/../x", "?wi=workbench%23210", "?wi=wi_route210unknown"]) {
    const { status, text } = await get(query);
    assert.equal(status, 400, `refused: ${query || "no wi"}`);
    assert.ok(!text.includes("KNOWN-ONLY") && !text.includes(f.root), `nothing read or named for ${query}`);
  }
});

test(" (N6): a missing, unreadable or non-JSON job index answers 200 available:false; a malformed id stays 400", async (t) => {
  const f = fixture(t);
  f.capsule("wi_route238n", undefined, jsonl([{ type: "message", id: "n", message: { role: "assistant", content: [{ type: "text", text: "INDEXED" }] } }]));
  const index = join(f.root, "runtime-state.json");
  const saved = readFileSync(index, "utf8");
  for (const [label, damage] of [["missing", () => rmSync(index)], ["non-JSON", () => writeFileSync(index, "{half")], ["unreadable", () => { rmSync(index); mkdirSync(index); }]]) {
    damage();
    const { status, text } = await get("?wi=wi_route238n");
    assert.equal(status, 200, label);
    assert.deepEqual(JSON.parse(text), { available: false, lines: [], omitted: 0 }, label);
    assert.ok(!text.includes(f.root) && !text.includes("INDEXED"), `${label}: nothing read or named`);
    assert.equal((await get("?wi=../../etc/passwd")).status, 400, `${label}: a malformed id is still refused first`);
    rmSync(index, { recursive: true, force: true });
    writeFileSync(index, saved);
  }
  assert.equal((await get("?wi=wi_route238unknown")).status, 400, "a readable index without the job stays 400");
});

test("an outside, missing or partial transcript adds no line; a record without a capsule degrades to 200 available:false", async (t) => {
  const f = fixture(t);
  const outside = join(f.work, "outside.jsonl");
  writeFileSync(outside, jsonl([{ type: "message", id: "o", message: { role: "assistant", content: [{ type: "text", text: "OUTSIDE" }] } }]));
  f.capsule("wi_route210c", outside);
  f.capsule("wi_route210d");
  f.capsule("wi_route210e", undefined, '{"type":"message","id":"half","message":{"role":"assistant","content":[{"type":"text","text":"HALF"');
  const records = JSON.parse(readFileSync(join(f.root, "runtime-state.json"), "utf8"));
  records.jobs.push({ work_item_id: "wi_route210f" });
  writeFileSync(join(f.root, "runtime-state.json"), JSON.stringify(records));
  for (const wi of ["wi_route210c", "wi_route210d", "wi_route210e"]) {
    const { status, text } = await get(`?wi=${wi}`);
    assert.equal(status, 200, wi);
    assert.deepEqual(JSON.parse(text), { available: true, lines: [], omitted: 0 }, wi);
    assert.ok(!text.includes("OUTSIDE") && !text.includes("HALF"), wi);
  }
  const { status, text } = await get("?wi=wi_route210f");
  assert.equal(status, 200);
  assert.deepEqual(JSON.parse(text), { available: false, lines: [], omitted: 0 });
});

test("a cross-site request is refused by the existing gate with 403", async (t) => {
  const f = fixture(t);
  f.capsule("wi_route210g", undefined, jsonl([{ type: "message", id: "n", message: { role: "assistant", content: [{ type: "text", text: "GATED" }] } }]));
  const { status, text } = await get("?wi=wi_route210g", { headers: { "sec-fetch-site": "cross-site" } });
  assert.equal(status, 403);
  assert.ok(!text.includes("GATED"));
});
