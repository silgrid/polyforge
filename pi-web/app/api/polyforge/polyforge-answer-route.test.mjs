// carries: app/api/polyforge/answer/*, lib/polyforge-status*
import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// POST /api/polyforge/answer (workflow contract v2 §5.2 / §5.4, change 14;
// workflow contract 4,): the job card's one input box runs
// `pf --json answer <wi> --text=<words>` with no question id — the daemon
// tells a reply from an interjection, and wakes a parked work item; the
// words go verbatim; the daemon's acknowledgement comes back as `ack`, a
// refusal as `error` with its typed `reason`. A fake pf (PF_BIN) records
// argv and answers the relay's JSON, or refuses.
const work = mkdtempSync(join(tmpdir(), "pf-answer-route-"));
const saved = { PF_BIN: process.env.PF_BIN, POLYFORGE_DATA_DIR: process.env.POLYFORGE_DATA_DIR };
const log = join(work, "argv.log");
const mode = join(work, "mode");
mkdirSync(join(work, "data"));
writeFileSync(log, "");
writeFileSync(mode, "reply");
writeFileSync(join(work, "pf"), `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
if (args[0] === "--version") { process.stdout.write("v2.21.0\\n"); process.exit(0); }
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(args) + "\\n");
const mode = fs.readFileSync(${JSON.stringify(mode)}, "utf8");
if (mode === "refuse-409") { process.stderr.write("DAEMON-409: wi_A1 is wrapped: no step agent to talk to\\n"); process.exit(1); }
if (mode === "refuse-400") { process.stderr.write("DAEMON-400: an owner message needs text\\n"); process.exit(1); }
if (mode === "other") { process.stdout.write(JSON.stringify({ status: "accepted" }) + "\\n"); process.exit(0); }
if (mode === "garbage") { process.stdout.write("not json\\n"); process.exit(0); }
// workflow contract 4: the daemon's typed refusal of a wake whose agent cannot start, as \`pf --json\` prints it
if (mode === "cannot-start") { process.stderr.write(JSON.stringify({ code: "DAEMON-409", detail: "wi_A1 is parked and its agent cannot start, so a message cannot wake it: no usable model: step build: model_error token=SECRET-MODEL-TOKEN — fix the cause, then \`pf resume wi_A1\` to continue, or \`pf cancel wi_A1\` to abandon it (nothing was recorded)", extra: { reason: "WAKE_CANNOT_START" } }) + "\\n"); process.exit(1); }
if (mode === "door-refused") { process.stderr.write("answer: wi_A1 is parked blocked; opening its door was refused, so nothing was recorded:\\n" + JSON.stringify({ code: "CORE-403", detail: "only the reporter, a maintainer or an admin resumes a blocked work item" }) + "\\n"); process.exit(1); }
if (mode === "wake") { process.stdout.write(JSON.stringify({ status: "accepted", at: "2026-11-11T10:00:01.000000Z", kind: "message", step: "build", spawn_n: 3, delivered: false, disposition: "wake", park: "blocked", wake: "step_blocked" }) + "\\n"); process.exit(0); }
process.stdout.write(JSON.stringify({ status: "accepted", at: "2026-11-10T10:00:01.000000Z", kind: mode === "message" ? "message" : "reply",
  ask_at: mode === "message" ? "" : "2026-11-10T10:00:00.000000Z", step: "build", spawn_n: 2, delivered: mode !== "message", disposition: mode === "message" ? "queued" : "started",
  note: mode === "message" ? "a turn is running token=SECRET-NOTE-TOKEN" : "" }) + "\\n");
`);
chmodSync(join(work, "pf"), 0o755);
process.env.PF_BIN = join(work, "pf");
process.env.POLYFORGE_DATA_DIR = join(work, "data");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { POST } = await jiti.import("./answer/route.ts");
const source = await readFile(new URL("./answer/route.ts", import.meta.url), "utf8");

test.after(() => {
  for (const [key, value] of Object.entries(saved)) { if (value === undefined) delete process.env[key]; else process.env[key] = value; }
  rmSync(work, { force: true, recursive: true });
});

const post = (body, { host = "127.0.0.1:30141", type = "application/json" } = {}) => new Request(`http://${host}/api/polyforge/answer`, {
  method: "POST", headers: { host, origin: `http://${host}`, "content-type": type }, body: typeof body === "string" ? body : JSON.stringify(body),
});
const argv = () => readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
const reset = (m = "reply") => { writeFileSync(mode, m); writeFileSync(log, ""); };

test("the route re-checks the host/origin gate in-route, POST only, never 5xx, no shell, no question id", () => {
  assert.match(source, /isApiRequestAllowed\(request\)/);
  assert.match(source, /hasJsonContentType\(request\)/);
  assert.doesNotMatch(source, /export async function (GET|PUT|DELETE|PATCH)/);
  assert.doesNotMatch(source, /status: 5\d\d/);
  assert.doesNotMatch(source, /questionId|question_id|--question|--option|option/, "no question id and no option: the words alone");
});

test("a message runs `pf --json answer <wi> --text=<words>` (with --profile for a named Cell), the words verbatim, and answers the relay's ack", async () => {
  reset("reply");
  let response = await POST(post({ wi: "wi_A1", text: "  use amber\n\n- bold  " }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { ok: true, ack: { kind: "reply", at: "2026-11-10T10:00:01.000000Z", askAt: "2026-11-10T10:00:00.000000Z", step: "build", spawnN: 2, delivered: true, disposition: "started" } });
  reset("message");
  response = await POST(post({ wi: "wi_A1", profile: "lab", text: "-x looks wrong, check the flag" }));
  const body = await response.json();
  assert.deepEqual(body, { ok: true, ack: { kind: "message", at: "2026-11-10T10:00:01.000000Z", step: "build", spawnN: 2, delivered: false, disposition: "queued", note: "a turn is running token=***" } },
    "an interjection's ack: queued, its note masked; no ask stamp");
  assert.deepEqual(argv(), [["--profile", "lab", "--json", "answer", "wi_A1", "--text=-x looks wrong, check the flag"]], "`=` form: words that start with - stay the flag's value");
});

test("pf output that is no relay ack — unreadable, or no reply / message kind — is an undelivered message (the words went out, the proof is missing)", async () => {
  reset("other");
  let response = await POST(post({ wi: "wi_A1", text: "yes" }));
  assert.deepEqual(await response.json(), { ok: true, ack: { kind: "message", delivered: false } });
  reset("garbage");
  response = await POST(post({ wi: "wi_A1", text: "yes" }));
  assert.deepEqual(await response.json(), { ok: true, ack: { kind: "message", delivered: false } });
});

test("an option is no field of the route any more (workflow contract v2 change 19): a body naming one sends the words alone", async () => {
  reset("reply");
  await POST(post({ wi: "wi_A1", option: "revert", text: "the hold was a false alarm" }));
  assert.deepEqual(argv(), [["--json", "answer", "wi_A1", "--text=the hold was a false alarm"]]);
});

test("pf's refusal comes back 409 {ok:false, error} with its text — a state conflict (DAEMON-409) and an invalid message (DAEMON-400) alike", async () => {
  reset("refuse-409");
  let response = await POST(post({ wi: "wi_A1", text: "hello" }));
  assert.equal(response.status, 409);
  assert.deepEqual(await response.json(), { ok: false, error: "DAEMON-409: wi_A1 is wrapped: no step agent to talk to" });
  reset("refuse-400");
  response = await POST(post({ wi: "wi_A1", text: "hello" }));
  assert.equal(response.status, 409);
  assert.deepEqual(await response.json(), { ok: false, error: "DAEMON-400: an owner message needs text" });
});

test("workflow contract 4: a message to a parked work item wakes it — the ack's disposition says so", async () => {
  reset("wake");
  const response = await POST(post({ wi: "wi_A1", text: "the docker engine is up again, go on" }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { ok: true, ack: { kind: "message", at: "2026-11-11T10:00:01.000000Z", step: "build", spawnN: 3, delivered: false, disposition: "wake" } });
  assert.deepEqual(argv(), [["--json", "answer", "wi_A1", "--text=the docker engine is up again, go on"]], "the same command: pf opens the door and wakes it");
});

test("workflow contract 4: a park whose agent cannot start comes back 409 with reason WAKE_CANNOT_START and the daemon's words (masked); another typed refusal keeps pf's context line", async () => {
  reset("cannot-start");
  let response = await POST(post({ wi: "wi_A1", text: "go on" }));
  assert.equal(response.status, 409);
  const body = await response.json();
  assert.equal(body.ok, false);
  assert.equal(body.reason, "WAKE_CANNOT_START");
  assert.match(body.error, /^DAEMON-409: wi_A1 is parked and its agent cannot start, so a message cannot wake it: no usable model: step build: model_error token=\*\*\* — fix the cause/);
  assert.ok(!body.error.includes("SECRET"), "masked");
  assert.ok(!body.error.includes("{"), "the envelope is unwrapped");
  reset("door-refused");
  response = await POST(post({ wi: "wi_A1", text: "go on" }));
  assert.equal(response.status, 409);
  assert.deepEqual(await response.json(), { ok: false, error: "answer: wi_A1 is parked blocked; opening its door was refused, so nothing was recorded:\nCORE-403: only the reporter, a maintainer or an admin resumes a blocked work item" },
    "no reason: an ordinary refusal");
});

test("a bad wi or profile, no text, a non-string text or one over the bound is a 400 before any spawn; the gate and the content type refuse first", async () => {
  reset();
  for (const body of [
    { wi: "demo#1", text: "x" },
    { wi: "wi_A1; rm -rf", text: "x" },
    { wi: "wi_A1", profile: "Bad Profile", text: "x" },
    { wi: "wi_A1", option: "revert" },
    { wi: "wi_A1" },
    { wi: "wi_A1", text: "   " },
    { wi: "wi_A1", text: 5 },
    { wi: "wi_A1", text: "x".repeat(16_001) },
  ]) {
    const response = await POST(post(body));
    assert.equal(response.status, 400, `${JSON.stringify(body).slice(0, 60)} is a 400`);
    assert.equal((await response.json()).ok, false);
  }
  assert.equal((await POST(post("{not json"))).status, 400);
  assert.equal((await POST(post({ wi: "wi_A1", text: "x" }, { host: "evil.example" }))).status, 403);
  assert.equal((await POST(post({ wi: "wi_A1", text: "x" }, { type: "text/plain" }))).status, 415);
  assert.deepEqual(argv(), [], "nothing was spawned");
});
