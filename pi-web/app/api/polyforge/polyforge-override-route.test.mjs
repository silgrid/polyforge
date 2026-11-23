// carries: app/api/polyforge/override/*, app/api/polyforge/watch/*, lib/polyforge-overrides*, hooks/usePolyforge*, lib/polyforge-conversation-pure*
// polyforge-override-route.test.mjs (u8, polyforge#25; polyforge#42 u2; polyforge#48 u3): the
// override face over the pf subprocess — the per-step view rides the
// watch route's response (`pf [--profile p] --json override <wi>` composed
// into the same answer, G11: the controls fetch nothing on selection), and
// polyforge#48 makes that composition opt-in (`?overrides=1`, the panel's ask
// while its held snapshot does not say the work item ended), the view read
// concurrent with the watch read (each bounded by its own read timeout, so a
// hung view read degrades to absent inside the watch read's own bound) and
// never composed for an ended ticket; the override route itself is POST-only
// (one action's argv); a refusal
// answers 409 {ok:false, error} with pf's typed words, never 5xx; the wi
// and profile are validated before any spawn and the host gate refuses
// first.
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
const exitedLog = join(work, "exit.log");
const mode = join(work, "mode");
mkdirSync(join(work, "data"));
writeFileSync(log, "");
writeFileSync(exitedLog, "");
writeFileSync(mode, "view");
writeFileSync(join(work, "pf"), `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
// Every invocation records its exit, so the test can wait for a dropped read
// (the route starts it beside the watch read and does not await it) to finish
// before the next reset clears the logs (polyforge#238 defect 3).
process.on("exit", () => { try { fs.appendFileSync(${JSON.stringify(exitedLog)}, JSON.stringify(args) + "\\n"); } catch {} });
// A read the route abandons on its timeout is killed with SIGTERM; terminate
// through process.exit so the exit record is written too.
for (const signal of ["SIGTERM", "SIGINT"]) process.on(signal, () => process.exit(0));
if (args[0] === "--version") { process.stdout.write("v2.21.0\\n"); process.exit(0); }
// polyforge#238 defect 3 reproduction input: on an ended ticket the route
// drops the override/params reads unawaited; letting those two children delay
// their own argv makes the ordering race deterministic for the loop harness.
// This is a controlled slow child (a scenario input), not a synchronization.
if (fs.readFileSync(${JSON.stringify(mode)}, "utf8") === "ended" && (args.includes("override") || args.includes("params"))) {
  const until = Date.now() + 300;
  while (Date.now() < until) { /* deliberate slow child */ }
}
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(args) + "\\n");
const mode = fs.readFileSync(${JSON.stringify(mode)}, "utf8");
const out = (v) => { process.stdout.write(JSON.stringify(v) + "\\n"); process.exit(0); };
if (args.includes("watch")) {
  const wi = args[args.indexOf("watch") + 1];
  if (mode === "unknown") out({ work_item: wi, known: false, events: [], last_seq: 0 });
  const answer = (workflowStatus) => out({ work_item: "wi_A1", slug: "pi#42", known: true, job: { status: "running" },
    execution: { workflow_status: workflowStatus, current_step: "build", updated_at: "2026-11-15T00:00:00Z",
      steps: { build: { status: "in_progress", started_at: "2026-11-15T00:00:01Z" }, merge: { status: "pending" } },
      declared_steps: [{ id: "build", skippable: false }, { id: "merge", skippable: true }] },
    events: [], last_seq: 3 });
  if (mode === "ended") answer("completed");
  // polyforge#48 AC3: the watch handler waits (bounded, deadline-polled) for
  // the override argv to appear in the log before it answers — only a
  // route that starts the view read TOGETHER WITH the watch read lets it
  // answer quickly; a serial route answers at the deadline (~3 s).
  if (mode === "wait-view") {
    const deadline = Date.now() + 3000;
    const check = () => {
      if (fs.readFileSync(${JSON.stringify(log)}, "utf8").includes("override") || Date.now() >= deadline) answer("running");
      else setTimeout(check, 25);
    };
    return check();
  }
  answer("running");
}
if (args.includes("params")) {
  if (mode === "garbage") { process.stdout.write("not json\\n"); process.exit(0); }
  if (mode === "view" || mode === "wait-view") {
    out({ work_item: "wi_A1", posture: "running", steps: [
      { id: "review", status: "in_progress", params: [{ name: "image", value: "reg/x:1", source: "step override" }] },
    ] });
  }
  out({ work_item: "wi_A1", posture: "running", steps: [
    { id: "review", status: "in_progress", params: [{ name: "image", value: "base-img", source: "bundle demo step review params.image" }] },
  ] });
}
if (args.includes("override")) {
  if (mode === "hang-view") { setTimeout(() => process.exit(0), 30000); return; }
  if (mode === "refuse-409") { process.stderr.write(JSON.stringify({ code: "DAEMON-409", detail: "step merge has a live dispatch (its status is in_progress): the overlay is for steps without one" }) + "\\n"); process.exit(1); }
  if (mode === "refuse-400-param") { process.stderr.write(JSON.stringify({ code: "DAEMON-400", detail: "the lane declares no step param \\\"nosuch\\\" on step review: a param is set only where the lane declares it" }) + "\\n"); process.exit(1); }
  if (mode === "refuse-400") { process.stderr.write(JSON.stringify({ code: "DAEMON-400", detail: "models: the pi model \\"absent\\" is not in this Cell's step-agent models.json" }) + "\\n"); process.exit(1); }
  if (mode === "garbage") { process.stdout.write("not json\\n"); process.exit(0); }
  if (mode === "view" || mode === "ended" || mode === "wait-view") {
    out({ work_item: "wi_A1", posture: "running", steps: [
      { id: "build", status: "in_progress", skippable: false },
      { id: "merge", status: "pending", skippable: true, models: ["pi:pf25/m-1"], models_by: "usr_1", effective: ["pi:pf25/m-1"], source: "step override" },
    ] });
  }
  out({ work_item: "wi_A1", changed: true, posture: "running", published: true, step: { id: "merge", status: "pending", skippable: true, models: ["pi:pf25/m-1"], models_by: "usr_2" } });
}
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(join(work, "pf"), 0o755);
process.env.PF_BIN = join(work, "pf");
process.env.POLYFORGE_DATA_DIR = join(work, "data");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { POST } = await jiti.import("./override/route.ts");
const { GET: WATCH_GET } = await jiti.import("./watch/route.ts");
const overrideSource = await readFileSync(new URL("./override/route.ts", import.meta.url), "utf8");
const watchSource = await readFileSync(new URL("./watch/route.ts", import.meta.url), "utf8");

test.after(() => {
  for (const [key, value] of Object.entries(saved)) { if (value === undefined) delete process.env[key]; else process.env[key] = value; }
  rmSync(work, { force: true, recursive: true });
});

const host = "127.0.0.1:30141";
const watchGet = (query) => new Request(`http://${host}/api/polyforge/watch${query}`, { method: "GET", headers: { host, origin: `http://${host}` } });
const post = (body, type = "application/json") => new Request(`http://${host}/api/polyforge/override`, {
  method: "POST", headers: { host, origin: `http://${host}`, "content-type": type }, body: typeof body === "string" ? body : JSON.stringify(body),
});
const argv = () => readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
const exited = () => readFileSync(exitedLog, "utf8").trim().split("\n").filter(Boolean).length;
// The watch route starts the composed reads beside the watch read and drops
// them unawaited once the ticket reads ended. Before a reset clears the logs,
// every pf child started since the last reset must have exited: a dropped read
// that is still running appends its argv after the reset, into the next test's
// log (polyforge#238 defect 3 — the reset raced the dropped read). The wait is
// bounded; a child that does not exit inside it fails the test, never a fixed
// sleep.
const reset = async (m = "view") => {
  const deadline = Date.now() + 15_000;
  while (exited() < argv().length) {
    if (Date.now() >= deadline) {
      assert.fail(`dropped pf read children did not exit before reset (${exited()} exited of ${argv().length} started)`);
    }
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  writeFileSync(mode, m); writeFileSync(log, ""); writeFileSync(exitedLog, "");
};
// polyforge#238 defect 3 (review r1#1): the watch route starts the override
// and params reads beside the watch read and drops them unawaited on an ended
// ticket. Before a reset clears the logs, both reads must have written their
// own argv — a reset that only counts exits cannot see a child that has not
// reached its argv append yet, and that late argv would land in the next
// test's log. Bounded; a timeout fails the test, never a fixed sleep.
const awaitDroppedReads = async () => {
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    const lines = argv();
    if (lines.some((a) => a.includes("override")) && lines.some((a) => a.includes("params"))) return;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  assert.fail("the dropped override/params reads did not write their own argv inside the bound");
};

test("both routes re-check the host/origin gate in-route; the override route is POST-only, the watch route GET-only, never 5xx", () => {
  for (const [name, source] of [["override", overrideSource], ["watch", watchSource]]) {
    assert.match(source, /isApiRequestAllowed\(request\)/, `${name} re-checks the host/origin gate in-route`);
    assert.doesNotMatch(source, /status: 5\d\d/, `${name} must never answer 5xx`);
  }
  assert.match(overrideSource, /hasJsonContentType\(request\)/, "the POST's content-type gate");
  // polyforge#42 (G11): the view read moved into the watch response, so the
  // override route's GET face has no caller left — it is gone, not kept.
  assert.doesNotMatch(overrideSource, /export async function GET/, "the override route keeps no GET face");
  assert.doesNotMatch(overrideSource, /export async function (PUT|DELETE|PATCH)/);
  assert.match(watchSource, /export async function GET\(request: Request\)/, "the watch route stays the one GET");
});

test("the watch route composes the override view into the same response (polyforge#42 G11: the snapshot carries it, the controls fetch nothing on selection)", async () => {
  await reset("view");
  const response = await WATCH_GET(watchGet("?wi=wi_A1&overrides=1"));
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.status?.wi, "wi_A1");
  assert.deepEqual(body.status?.declared_steps, [{ id: "build" }, { id: "merge" }]);
  assert.deepEqual(body.status?.skippableSteps, ["merge"]);
  assert.deepEqual(body.status?.overrideView, {
    wi: "wi_A1", posture: "running",
    steps: [
      { id: "build", status: "in_progress", skippable: false },
      { id: "merge", status: "pending", skippable: true, models: ["pi:pf25/m-1"], modelsBy: "usr_1", effective: ["pi:pf25/m-1"], source: "step override" },
    ],
  });
  assert.deepEqual([...argv()].sort(), [
    // polyforge#151 (85b2): the composed step-tier catalog's presence probe
    // (this stub answers only a bare --version, so the probe reads absent
    // and the composer stops there — a present pf adds its Host-pinned
    // config list read)
    ["--json", "--version"],
    ["--json", "override", "wi_A1"],
    ["--json", "params", "wi_A1"],
    ["--json", "watch", "wi_A1", "--once", "--demand", "--events", "40"],
  ], "one watch read, the two composed reads it starts and the catalog composer's probe (polyforge#48/#79/#151: the spawn order is not pinned — they start together)");
  assert.equal(body.status?.stepTiers?.reason, "polyforge is not installed on this machine", "the composed catalog degrades with its reason (this stub answers no --json --version, so the probe reads absent), never 5xx");
  assert.deepEqual(body.status?.stepTiers?.tiers, []);
});

test("polyforge#48 AC3: without overrides=1 the watch route never reads the view", async () => {
  await reset("view");
  const response = await WATCH_GET(watchGet("?wi=wi_A1"));
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.status?.wi, "wi_A1", "the watch snapshot itself survives");
  assert.equal(body.status?.overrideView, undefined, "no composed view without the ask");
  assert.ok(!argv().some((a) => a.includes("override")), "no override read is spawned at all");
});

test("polyforge#48 AC3: the view read runs concurrently with the watch read", async () => {
  await reset("wait-view");
  const started = Date.now();
  const response = await WATCH_GET(watchGet("?wi=wi_A1&overrides=1"));
  const elapsed = Date.now() - started;
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.status?.overrideView?.wi, "wi_A1", "the view composed while the watch read ran");
  // The stub's watch handler answers only once the override argv is in the
  // log (else at its 3 s deadline): a serial route — watch first, view after —
  // can never answer inside this bound.
  assert.ok(elapsed < 2500, `the watch read and the view read started together (${elapsed} ms)`);
});

test("polyforge#48 AC3: an ended ticket's answer never carries the view", async () => {
  await reset("ended");
  const response = await WATCH_GET(watchGet("?wi=wi_A1&overrides=1"));
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.status?.workflowStatus, "completed");
  assert.equal(body.status?.overrideView, undefined, "the composed view never rides an ended ticket's answer");
  assert.equal(body.status?.stepTiers, undefined, "the step-tier catalog composes for a live ticket only (85b2)");
  // The next reset waits for these dropped reads to exit before it clears
  // the logs; the argv wait first keeps a child that has not reached its argv
  // append yet from landing in the next test (polyforge#238 defect 3, r1#1).
  await awaitDroppedReads();
});

test("the composed view read names the Cell exactly like the watch read (a slug is a valid ref)", async () => {
  await reset("view");
  const response = await WATCH_GET(watchGet("?wi=demo%231&profile=lab&overrides=1"));
  await response.json();
  assert.deepEqual([...argv()].sort(), [
    ["--profile", "lab", "--json", "override", "demo#1"],
    ["--profile", "lab", "--json", "params", "demo#1"],
    ["--profile", "lab", "--json", "watch", "demo#1", "--once", "--demand", "--events", "40"],
  ]);
});

test("a garbage or refused view read degrades to a snapshot without it — the watch answer itself never fails", async () => {
  for (const m of ["garbage", "refuse-409"]) {
    await reset(m);
    const response = await WATCH_GET(watchGet("?wi=wi_A1&overrides=1"));
    assert.equal(response.status, 200, m);
    const body = await response.json();
    assert.equal(body.status?.wi, "wi_A1", `${m}: the watch snapshot survives`);
    assert.equal(body.status?.overrideView, undefined, `${m}: the view degrades to absent`);
  }
});

test("a hung view read times out on the read bound — the watch answer still returns the snapshot (polyforge#42 r1#1)", async () => {
  await reset("hang-view");
  const started = Date.now();
  // The test's own bound (a real condition raced against a deadline, not a
  // sleep): the route must answer inside it — the view read's own timeout
  // degrades it, on a regression the answer never comes. The parallel start
  // (polyforge#48) keeps the answer inside the watch read's own bound even
  // while the view read hangs: never the sum of both.
  const deadline = new Promise((resolve) => { const timer = setTimeout(() => resolve(null), 14_000); timer.unref?.(); });
  const response = await Promise.race([WATCH_GET(watchGet("?wi=wi_A1&overrides=1")), deadline]);
  assert.ok(response, "the watch answer did not arrive within the view read's bound");
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.status?.wi, "wi_A1", "the snapshot itself survives the hung view read");
  assert.equal(body.status?.overrideView, undefined, "the hung view read degrades to absent");
  assert.ok(Date.now() - started < 14_000, `the answer arrived inside the bound (${Date.now() - started} ms)`);
});

test("an unknown ref answers status null and never spawns the view read", async () => {
  await reset("unknown");
  const response = await WATCH_GET(watchGet("?wi=wi_A1"));
  assert.equal(response.status, 200);
  assert.equal((await response.json()).status, null);
  assert.ok(!argv().some((a) => a.includes("override")), "the composed view read never ran");
});

test("POST runs one action's argv and answers the result; --models and the skip switches reach pf", async () => {
  await reset("write");
  let response = await POST(post({ wi: "wi_A1", action: { kind: "models", step: "merge", models: ["pi:pf25/m-1", "pi:pf25/m-2"] } }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { ok: true, result: {
    wi: "wi_A1", changed: true, posture: "running", published: true,
    step: { id: "merge", status: "pending", skippable: true, models: ["pi:pf25/m-1"], modelsBy: "usr_2" },
  } });
  assert.deepEqual(argv(), [["--json", "override", "wi_A1", "--step", "merge", "--models", "pi:pf25/m-1,pi:pf25/m-2"]]);
  await reset("write");
  response = await POST(post({ wi: "wi_A1", profile: "lab", action: { kind: "skip", step: "merge", value: true } }));
  await response.json();
  assert.deepEqual(argv(), [["--profile", "lab", "--json", "override", "wi_A1", "--step", "merge", "--skip"]]);
  await reset("write");
  response = await POST(post({ wi: "wi_A1", action: { kind: "clearModels", step: "merge" } }));
  await response.json();
  assert.deepEqual(argv(), [["--json", "override", "wi_A1", "--step", "merge", "--clear-models"]]);
});

test("pf's refusal comes back 409 {ok:false, error} with the typed envelope's words", async () => {
  await reset("refuse-409");
  let response = await POST(post({ wi: "wi_A1", action: { kind: "skip", step: "merge", value: true } }));
  assert.equal(response.status, 409);
  assert.deepEqual(await response.json(), { ok: false, error: "DAEMON-409: step merge has a live dispatch (its status is in_progress): the overlay is for steps without one" });
  await reset("refuse-400");
  response = await POST(post({ wi: "wi_A1", action: { kind: "models", step: "merge", models: ["pi:absent"] } }));
  assert.equal(response.status, 409);
  assert.deepEqual(await response.json(), { ok: false, error: 'DAEMON-400: models: the pi model "absent" is not in this Cell\'s step-agent models.json' });
});

test("a bad wi or profile, no action, an unknown kind or an empty chain is a 400 before any spawn; garbage bodies stay decodable", async () => {
  await reset();
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


// polyforge#79 (u6): the param actions ride the same POST face — the argv
// validated in-route, an undeclared name's refusal answers 409 with the
// daemon's typed words (the panel shows them), and the watch route
// composes the params view beside the override view (the same opt-in,
// never for an ended ticket, never without it).
test("the param actions ride the POST face: set and clear, the refusal's words, the 400 before any spawn", async () => {
  await reset("write");
  let response = await POST(post({ wi: "wi_A1", action: { kind: "param", step: "review", name: "image", value: "reg/x:1" } }));
  assert.equal(response.status, 200);
  assert.equal((await response.json()).ok, true);
  assert.deepEqual(argv().at(-1), ["--json", "override", "wi_A1", "--step", "review", "--param", "image=reg/x:1"]);
  await reset("write");
  response = await POST(post({ wi: "wi_A1", action: { kind: "clearParam", step: "review", name: "image" } }));
  assert.equal((await response.json()).ok, true);
  assert.deepEqual(argv().at(-1), ["--json", "override", "wi_A1", "--step", "review", "--clear-param", "image"]);
  // the lane did not declare the name: the daemon's refusal is the 409 the panel shows
  await reset("refuse-400-param");
  response = await POST(post({ wi: "wi_A1", action: { kind: "param", step: "review", name: "nosuch", value: "x" } }));
  assert.equal(response.status, 409);
  assert.deepEqual(await response.json(), { ok: false, error: 'DAEMON-400: the lane declares no step param "nosuch" on step review: a param is set only where the lane declares it' });
  // an invalid action answers 400 before any spawn
  await reset("view");
  const before = argv().length;
  response = await POST(post({ wi: "wi_A1", action: { kind: "param", step: "review", name: "image", value: "two\nlines" } }));
  assert.equal(response.status, 400);
  assert.equal(argv().length, before, "an invalid action spawns nothing");
});

test("the watch route composes the params view beside the override view (the same opt-in, never for an ended ticket)", async () => {
  await reset("view");
  let response = await WATCH_GET(watchGet("?wi=wi_A1&overrides=1"));
  assert.equal(response.status, 200);
  let body = await response.json();
  assert.deepEqual(body.status?.paramsView, {
    wi: "wi_A1", posture: "running",
    steps: [{ id: "review", status: "in_progress", params: [{ name: "image", value: "reg/x:1", source: "step override" }] }],
  }, "the composed params view rides the snapshot (G11: the controls fetch nothing on selection)");
  assert.ok(argv().some((a) => JSON.stringify(a) === JSON.stringify(["--json", "params", "wi_A1"])), "the params read starts together with the watch read");
  // an ended ticket composes neither view — the reads may have started
  // (the one transitional ask before the panel knows, polyforge#48), but
  // the answer drops them
  await reset("ended");
  response = await WATCH_GET(watchGet("?wi=wi_A1&overrides=1"));
  body = await response.json();
  assert.equal(body.status?.overrideView, undefined, "an ended ticket composes no override view (polyforge#48)");
  assert.equal(body.status?.paramsView, undefined, "an ended ticket composes no params view either");
  // The route starts both composed reads beside the watch read and drops
  // them unawaited once the ticket reads ended (the established pattern):
  // under load a dropped stub child can append its argv after this answer.
  // A bounded wait for the reads' own argv keeps the next reset from racing
  // one (polyforge#151's flaky spawn-set find; polyforge#84's close red).
  const settled = Date.now() + 15_000;
  while (Date.now() < settled) {
    const lines = argv();
    if (lines.some((a) => a.includes("override")) && lines.some((a) => a.includes("params"))) break;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  // without the opt-in the params view is not asked for
  await reset("view");
  response = await WATCH_GET(watchGet("?wi=wi_A1"));
  body = await response.json();
  assert.equal(body.status?.paramsView, undefined);
  assert.equal(body.status?.overrideView, undefined);
  assert.ok(!argv().some((a) => a.includes("params")), "no opt-in, no params read");
});
