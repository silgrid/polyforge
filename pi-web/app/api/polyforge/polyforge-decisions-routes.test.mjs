// carries: app/api/polyforge/decisions/*, lib/polyforge-decisions*
import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// the decision feed's two routes, driven through a fake pf
// (PF_BIN): GET /api/polyforge/decisions (`pf --json watch decisions`) and
// POST /api/polyforge/decisions/dismiss (`pf --json watch dismiss …`).
const work = mkdtempSync(join(tmpdir(), "pf-decisions-routes-"));
const previousPfBin = process.env.PF_BIN;
const log = join(work, "argv.log");
const mode = join(work, "mode");
const pf = join(work, "pf");
const FEED = {
  attention: 1,
  decisions: [
    { id: 14, at: "2026-11-09T10:14:00Z", kind: "work_item", profile: "default", wi: "wi_W14", slug: "demo#14", class: "failed",
      source: "failed_gate", phrase: "the workflow failed", attention: true, resolved: false, dismissed: false, extra: "SECRET-EXTRA" },
    { id: 13, at: "2026-11-09T10:13:00Z", kind: "task", profile: "default", task_id: "task_0123456789abcdef", class: "completed", source: "task",
      phrase: "the background task completed", text: "done", attention: false, resolved: false, dismissed: false },
    { id: "bad", kind: "work_item", class: "failed", attention: true, resolved: false, dismissed: false },
  ],
};
writeFileSync(pf, `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
if (args[0] === "--version") { process.stdout.write("v2.21.0\\n"); process.exit(0); }
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(args) + "\\n");
const mode = fs.readFileSync(${JSON.stringify(mode)}, "utf8");
if (mode === "fail") { process.stderr.write("watch: daemon refused (DAEMON-403)\\n"); process.exit(1); }
if (mode === "malformed") { process.stdout.write("not json\\n"); process.exit(0); }
if (args[2] === "decisions") process.stdout.write(JSON.stringify(${JSON.stringify(FEED)}));
else process.stdout.write(JSON.stringify({ dismissed: args.includes("--all") ? 5 : args.length - 3 }));
`);
chmodSync(pf, 0o755);
process.env.PF_BIN = pf;
writeFileSync(mode, "ok");
writeFileSync(log, "");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const statusLib = await jiti.import("../../../lib/polyforge-status.ts");
const { GET } = await jiti.import("./decisions/route.ts");
const { POST } = await jiti.import("./decisions/dismiss/route.ts");
const getSource = await readFile(new URL("./decisions/route.ts", import.meta.url), "utf8");
const dismissSource = await readFile(new URL("./decisions/dismiss/route.ts", import.meta.url), "utf8");

test.after(() => {
  if (previousPfBin === undefined) delete process.env.PF_BIN; else process.env.PF_BIN = previousPfBin;
  rmSync(work, { force: true, recursive: true });
});

const get = (host = "127.0.0.1:30141") => new Request(`http://${host}/api/polyforge/decisions`, { headers: { host } });
const post = (body, { host = "127.0.0.1:30141", type = "application/json" } = {}) => new Request(`http://${host}/api/polyforge/decisions/dismiss`, {
  method: "POST", headers: { host, origin: `http://${host}`, "content-type": type }, body: typeof body === "string" ? body : JSON.stringify(body),
});
const argv = () => readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
const reset = (value = "ok") => { writeFileSync(mode, value); writeFileSync(log, ""); };

test("both routes re-check the host/origin gate in-route; GET only on the feed, POST only on dismiss", async () => {
  assert.match(getSource, /isApiRequestAllowed\(request\)/);
  assert.match(dismissSource, /isApiRequestAllowed\(request\)/);
  assert.doesNotMatch(getSource, /export async function (POST|PUT|DELETE|PATCH)/);
  assert.doesNotMatch(dismissSource, /export async function (GET|PUT|DELETE|PATCH)/);
  for (const source of [getSource, dismissSource]) assert.doesNotMatch(source, /status: 5\d\d/, "never 5xx");
  reset();
  assert.equal((await GET(get("evil.example"))).status, 403);
  assert.equal((await POST(post({ all: true }, { host: "evil.example" }))).status, 403);
  assert.deepEqual(argv(), [], "a refused request spawns nothing");
});

test("GET answers the sanitized feed from exactly `pf --json watch decisions`", async () => {
  await statusLib.probePolyforgeVersion(true);
  reset();
  const response = await GET(get());
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.pfPresent, true);
  assert.equal(body.error, undefined);
  assert.deepEqual(body.feed.decisions.map((d) => d.id), [14, 13], "the malformed entry is dropped");
  assert.equal(body.feed.attention, 1);
  assert.ok(!JSON.stringify(body).includes("SECRET-EXTRA"), "no unknown field leaves");
  assert.deepEqual(argv(), [["--json", "watch", "decisions"]], "one Host-level read, no --profile");
});

test("GET degrades: pf's bounded stderr on failure, an unreadable answer, pf absent", async () => {
  reset("fail");
  assert.deepEqual(await (await GET(get())).json(), { pfPresent: true, feed: null, error: "watch: daemon refused (DAEMON-403)" });
  reset("malformed");
  const malformed = await (await GET(get())).json();
  assert.equal(malformed.feed, null);
  assert.match(malformed.error, /undecodable/);
  process.env.PF_BIN = join(work, "missing-pf");
  await statusLib.probePolyforgeVersion(true);
  try {
    const response = await GET(get());
    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), { pfPresent: false, feed: null });
  } finally {
    process.env.PF_BIN = pf;
    await statusLib.probePolyforgeVersion(true);
  }
});

test("POST dismiss: ids or all reach pf as argv, the count comes back", async () => {
  reset();
  let response = await POST(post({ ids: [12, 13, 12] }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { ok: true, dismissed: 2 });
  response = await POST(post({ all: true }));
  assert.deepEqual(await response.json(), { ok: true, dismissed: 5 });
  assert.deepEqual(argv(), [["--json", "watch", "dismiss", "12", "13"], ["--json", "watch", "dismiss", "--all"]]);
});

test("POST dismiss refuses before any spawn: not JSON, a bad body, an id that is no positive integer", async () => {
  reset();
  assert.equal((await POST(post({ all: true }, { type: "text/plain" }))).status, 415);
  assert.equal((await POST(post("{not json"))).status, 400);
  for (const body of [{}, { ids: [] }, { ids: [0] }, { ids: ["12"] }, { ids: [1.5] }, { ids: ["--all"] }, { all: "yes" }, { all: true, ids: [1] }]) {
    const response = await POST(post(body));
    assert.equal(response.status, 400, `${JSON.stringify(body)} is a 400`);
    const answer = await response.json();
    assert.equal(answer.ok, false);
    assert.ok(answer.error);
  }
  assert.deepEqual(argv(), [], "nothing was spawned");
});

// The wire shape `pf --json watch dismiss` prints is the daemon's
// DecisionDismissAnswer, exactly `{"dismissed":N}` (pinned on the Go side by
// TestWatchDismissJSONIsTheDaemonAnswer); an answer without that count is no
// success.
test("POST dismiss: an answer that carries no dismissed count is {ok:false, error}", async () => {
  reset("malformed");
  const response = await POST(post({ ids: [1] }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { ok: false, error: "pf answered an undecodable dismiss count" });
});

test("POST dismiss: pf's refusal answers 200 {ok:false, error} with its bounded stderr", async () => {
  reset("fail");
  const response = await POST(post({ ids: [1] }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { ok: false, error: "watch: daemon refused (DAEMON-403)" });
});
