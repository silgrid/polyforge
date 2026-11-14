// carries: components/PolyforgeIntegrate*, lib/polyforge-integration.ts, app/api/polyforge/integration/*, hooks/usePolyforge.ts
// — the Polyforge panel's Work › 待集成 view on the contract-1
// integration model: per mapped project the work items whose
// `attrs.integration.state` is `landed` (slug, PR link, short merge commit,
// the waived mark; an item a red batch blamed shows its fix tickets and red
// rounds), read through the generic attrs list filter; the integrate batches
// with their steps and progress from the batch's own job/watch data; a
// batch's pending question answered in the panel through the job card's
// existing channel (`PolyforgeJobComposer` → `POST /api/polyforge/answer` →
// `pf --json answer <wi> --text=…`).
//
// The suite starts its own `next dev` with a stub `pf` first on PATH. The
// stub answers from a state file the suite rewrites: `config list` (the
// `[workspace]` mapping alpha, beta), `queue --project <p> --attr
// integration.state=landed` and `queue --project <p> --bundle integrate
// --limit 5` (hub list rows whose `attrs.integration` carry the real landed,
// waived and red-batch-blamed record shapes, reshaped to neutral names),
// `watch <wi> --once` (an integrate batch's real watch shape — collect →
// revert → full-gate → close — mid-run, with a pending question), `ops
// status` (the batch's job row) and `answer`. Every pf argv is appended to a
// ledger, so the read discipline is asserted, not assumed. No daemon, hub or
// model runs.
//
//   S454-1 (AC1, AC4) Work › 待集成 lists alpha's landed items — slug, PR
//          link, short merge commit, waived mark; the blamed item its fix
//          ticket and red rounds 1 — and beta shows pf's error.
//   S454-2 (AC2, AC4) alpha's running integrate batch shows its four steps in
//          order, the current step and done/declared progress; its job row
//          waits (`needs_input`) and the watch carries a question: the
//          question shows, an answer in the view runs `pf --json answer <wi>
//          --text=<words>` and the view reads once more.
//   S454-3 (AC3) parked on Jobs, the status poll never runs a landed or batch
//          queue read; with the hub unreachable (every queue read fails) each
//          project shows pf's error, and with config list failing too a ↻
//          shows the view's own read error; Jobs still renders its queue line
//          and job; across the suite no pf call names `ops integrate` or
//          `integration=1`.
//
// Every scenario prints `PASS S454-<n>`; a run with no failure ends
// `PASS all integrate-454 scenarios`.
//
//   node e2e/polyforge-integrate-454.mjs
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { chromium } from "playwright";
import { openPolyforgeDock, polyforgeDock as dock, waitForPolyforgeToggle } from "./polyforge-dock.mjs";
import { COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, root, suiteDist, writeFileAtomic, writeHostSession } from "./harness.mjs";

const distDir = suiteDist("polyforge-integrate-454");
assertNoDevServer(distDir);
rmSync(join(root, distDir), { force: true, recursive: true });

const work = mkdtempSync(join(tmpdir(), "pi-web-pf-454-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-integrate-454-session";
const HOST_TEXT = "E2E pf integrate 454 host session";
mkdirSync(project);
writeHostSession(agentDir, { id: SESSION, cwd: project, text: HOST_TEXT, timestamp: "2026-11-13T00:00:00.000Z" });

// ---- fixtures: the real attrs.integration and integrate-batch shapes, neutral names ----
const PR = (n) => `https://github.com/example/app/pull/${n}`;
const SHA_A = "98d40398c583cd6229675193f9d23d7d7e7123cb";
const SHA_B = "ec9e0033dd2f4cfc771cd982807e41f85cb9c807";
const SHA_C = "5a336035de4154d4d547f1be4e3463b62a3c6928";
const BLAMED = "wi_E2E454L21";
const WAIVED = "wi_E2E454L16";
const PLAIN = "wi_E2E454L22";
const BATCH = "wi_E2E454B30";
const RED_BATCH = "wi_E2E454B26";
const FIX = "wi_E2E454F27";
const QUESTION = "E2E-454 full-gate is red twice on the same head: retry it once more, or close the batch red?";
const ANSWER = "retry once more, then close red";
const BETA_ERROR = "queue: project beta: E2E-454 the hub refused the read";
const HUB_DOWN = "pf: aihub unreachable: dial tcp 192.0.2.1:443: connect: connection refused";
/** A hub list row (`pf --json queue` items[]) with the fields the passport drops. */
const hubRow = (id, slug, extra) => ({
  id, slug, seq: 1, project: "alpha", project_id: "prj_E2E454", goal: "E2E-454 goal", content: "E2E-454 content", status: "wrapped",
  priority: "normal", bundle_name: "develop", labels: [], created_at: "2026-11-12T00:00:00Z", ...extra,
});
const repo = (pr, sha) => ({ repo: "app", pr: PR(pr), head: SHA_C, light_verified_head: SHA_C, merge_commit: sha });
// develop's landed record, then a red batch's blame write on it (batch, red_rounds, red_heads, fix_tickets).
const landedRows = () => [
  hubRow(BLAMED, "alpha#21", { attrs: { integration: { state: "landed", repos: [repo(21, SHA_A)], merge_commits: { app: [SHA_A] },
    batch: RED_BATCH, red_rounds: 1, red_heads: { app: SHA_C }, fix_tickets: [FIX] } } }),
  hubRow(WAIVED, "alpha#16", { attrs: { integration: { state: "landed", repos: [repo(16, SHA_B)], merge_commits: { app: [SHA_B] }, waived: true } } }),
  hubRow(PLAIN, "alpha#22", { attrs: { integration: { state: "landed", repos: [repo(22, SHA_C)], merge_commits: { app: [SHA_C] } } } }),
];
const batchRows = () => [
  hubRow(BATCH, "alpha#30", { status: "running", bundle_name: "integrate" }),
  hubRow(RED_BATCH, "alpha#26", { status: "wrapped", bundle_name: "integrate" }),
];
const DECLARED = [{ id: "collect", on_fail: null }, { id: "revert", on_fail: null }, { id: "full-gate", on_fail: "revert" }, { id: "close", on_fail: null }];
// The integrate batch's `pf --json watch --once` shape, mid-run; the stub adds
// `execution.pending_question` while the state holds a question.
const ASK_AT = "2026-11-13T01:31:00Z";
const BATCH_WATCH = {
  work_item: BATCH, slug: "alpha#30", known: true,
  execution: {
    workflow_status: "running", current_step: "full-gate", updated_at: ASK_AT,
    steps: {
      close: { status: "pending" },
      collect: { status: "done", started_at: "2026-11-13T01:28:19Z" },
      "full-gate": { status: "running", started_at: "2026-11-13T01:29:00Z" },
      revert: { status: "done", started_at: "2026-11-13T01:28:41Z" },
    },
    declared_steps: DECLARED, rewinds: { agent: 0, max_rewinds: 3, owner: 0 },
  },
  hub_status: "running", events: [], last_seq: 9,
};

// ---- stub pf ----
const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const argvLog = join(stubDir, "argv.log");
const allLog = join(stubDir, "argv-all.log");
writeFileSync(allLog, "");
const fixture = (patch = {}) => ({
  ws: project, question: QUESTION, jobStatus: "needs_input", hubDown: false, configDown: false,
  landed: { alpha: landedRows() }, batches: { alpha: batchRows() }, failProject: "beta", ...patch,
});
const seed = (patch = {}) => { writeFileAtomic(stateFile, JSON.stringify(fixture(patch))); writeFileSync(argvLog, ""); };
const patchState = (patch) => writeFileAtomic(stateFile, JSON.stringify({ ...JSON.parse(readFileSync(stateFile, "utf8")), ...patch }));
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = ${JSON.stringify(stubDir)}; const args = process.argv.slice(2);
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify(args) + "\\n");
fs.appendFileSync(path.join(dir, "argv-all.log"), JSON.stringify(args) + "\\n");
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const save = () => { const tmp = path.join(dir, "state.json." + process.pid); fs.writeFileSync(tmp, JSON.stringify(s)); fs.renameSync(tmp, path.join(dir, "state.json")); };
const out = (v) => { const b = Buffer.from(JSON.stringify(v) + "\\n"); let o = 0;
  while (o < b.length) { try { o += fs.writeSync(1, b, o); } catch (e) { if (e.code !== "EAGAIN") throw e; } } process.exit(0); };
const fail = (text) => { process.stderr.write(text + "\\n"); process.exit(1); };
if (args.includes("--version")) { process.stdout.write("v2.21.0\\n"); process.exit(0); }
const rest = []; for (let i = 0; i < args.length; i += 1) { if (args[i] === "--profile") { i += 1; continue; } if (args[i] !== "--json") rest.push(args[i]); }
const flag = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] : undefined; };
if (rest[0] === "ops" && rest[1] === "status") out({ machine_id: "m-e2e", workspaces: [s.ws],
  jobs: [{ work_item_id: ${JSON.stringify(BATCH)}, slug: "alpha#30", status: s.jobStatus, spawn_n: 1, step_age_sec: 60, last_work_ago_sec: 5, candidate: "pi:e2e-model" }],
  queue: { running: 1, max_concurrent: 4 }, tasks: { running: 0, completed: 0 } });
if (rest[0] === "config" && rest[1] === "list") {
  if (s.configDown) fail(${JSON.stringify(HUB_DOWN)});
  out({ items: [{ key: "user_id", value: "u_e2e", source: "file" }, { key: '[workspace."' + s.ws + '"]', section: true, value: "alpha, beta" }] });
}
if (rest[0] === "config" && rest[1] === "workspace") out({ mapped: false, workspace: null });
if (rest[0] === "queue") {
  const project = flag("--project");
  if (s.hubDown) fail(${JSON.stringify(HUB_DOWN)});
  if (project === s.failProject) fail(${JSON.stringify(BETA_ERROR)});
  if (flag("--attr") === "integration.state=landed") out({ items: s.landed[project] || [], total: (s.landed[project] || []).length });
  if (flag("--bundle") === "integrate") out({ items: s.batches[project] || [], total: (s.batches[project] || []).length });
  out({ items: [], total: 0 });
}
if (rest[0] === "watch" && rest[1] === "decisions") out({ decisions: [], attention: [] });
if (rest[0] === "watch" && rest[1] === ${JSON.stringify(BATCH)}) {
  const w = ${JSON.stringify(BATCH_WATCH)};
  if (s.question) w.execution.pending_question = { id: ${JSON.stringify(ASK_AT)}, step: "full-gate", question: s.question, asked_at: ${JSON.stringify(ASK_AT)} };
  out(w);
}
if (rest[0] === "watch") out({ known: false });
if (rest[0] === "answer") {
  s.question = null; s.jobStatus = "running"; save();
  out({ status: "accepted", at: "2026-11-13T01:32:00Z", kind: "reply", ask_at: ${JSON.stringify(ASK_AT)}, step: "full-gate", spawn_n: 1, delivered: true, disposition: "started" });
}
if (rest[0] === "msg" && rest[1] === "list") out({ conversations: [] });
if (rest[0] === "msg" && rest[1] === "contacts") out({ contacts: [] });
if (rest[0] === "task" && rest[1] === "list") out({ tasks: [], now: new Date().toISOString() });
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
const ledger = (file) => readFileSync(file, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
const calls = () => ledger(argvLog);
const joined = (a) => a.join(" ");
const landedReads = () => calls().filter((a) => a.includes("queue") && a.includes("--attr")).length;
const batchReads = () => calls().filter((a) => a.includes("queue") && a.includes("--bundle") && a.includes("integrate")).length;
const statusReads = () => calls().filter((a) => a.includes("ops") && a.includes("status")).length;
const answers = () => calls().filter((a) => a.includes("answer"));

// ---- scenario bookkeeping and page helpers ----
const failures = [];
const check = createCheck({ failures });
const waitFor = createWaitFor({ ms: 30_000 });
const h = createHarness({
  locale: "zh-CN",
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN" },
  errors: ["pageerror", "5xx"],
  session: SESSION,
  ready: HOST_TEXT,
  server: (name) => ({ name, logFile: join(work, `server-${name}.log`), distDir: join(distDir, name),
    env: { PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: work, PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: "" } }),
});
const { errors, startServer, withPage } = h;
const textOf = async (locator) => ((await locator.count()) > 0 ? (await locator.first().innerText()).replace(/\s+/g, " ").trim() : null);
const topTab = (page, re) => dock(page).locator('[data-pf-tabstrip="top"] [role="tab"]').filter({ hasText: re }).first();
const workTabs = (page) => dock(page).locator('[data-pf-tabstrip="work"] [role="tab"]');
const workTab = (page, re) => workTabs(page).filter({ hasText: re }).first();
const view = (page) => dock(page).getByRole("region", { name: "待集成" });
async function openWork(page) {
  await openPolyforgeDock(page);
  await waitFor(async () => (await topTab(page, /工单/).count()) > 0, "the Work top-level tab exists", NAV_MS);
  await topTab(page, /工单/).click();
  // The rail renders its sub-tabs together: once 作业, 任务, 工单列表 are there, a 待集成 tab is there or absent.
  await waitFor(async () => (await workTabs(page).count()) >= 3, "the Work sub-tabs exist", NAV_MS);
}
async function openIntegrate(page) {
  await openWork(page);
  await waitFor(async () => (await workTab(page, /待集成/).count()) > 0, "a 待集成 Work sub-tab exists", 20_000);
  await workTab(page, /待集成/).click();
  await waitFor(async () => (await view(page).count()) > 0, "the 待集成 view renders", NAV_MS);
  return view(page);
}
const landed = (region, wi) => region.locator(`[data-pf-integrate-landed="${wi}"]`);
const hrefs = (locator) => locator.locator("a[href]").evaluateAll((links) => links.map((a) => [a.getAttribute("href"), a.target]));

try {
  await h.launchBrowser(() => chromium.launch());
  seed();
  await startServer("main");
  // Warm-up at the cold ceiling: the status probe's route compiles on first use.
  await withPage({ gotoTimeout: COLD_MS, readyTimeout: COLD_MS }, (page) => waitForPolyforgeToggle(page, COLD_MS));

  await check("S454-1", async () => {
    seed();
    await withPage({}, async (page) => {
      const region = await openIntegrate(page);
      const alpha = region.locator('[data-pf-integrate-project="alpha"]');
      // The integration route answers its first read cold in this page.
      await waitFor(async () => (await landed(alpha, BLAMED).count()) > 0, "alpha's landed items render", COLD_MS);
      assert.equal(await alpha.locator("[data-pf-integrate-landed]").count(), 3, "alpha lists its three landed items");
      const blamed = await textOf(landed(alpha, BLAMED));
      for (const want of ["alpha#21", SHA_A.slice(0, 7), FIX, "红轮次 1"]) assert.ok(blamed.includes(want), `the blamed item shows ${want} (got ${JSON.stringify(blamed)})`);
      assert.ok(!blamed.includes(SHA_A), "the merge commit is shown short");
      assert.equal(await landed(alpha, BLAMED).locator("[data-pf-integrate-waived]").count(), 0, "the blamed item is not waived");
      assert.deepEqual(await hrefs(landed(alpha, BLAMED)), [[PR(21), "_blank"]], "the blamed item links its PR in a new tab");
      const waived = await textOf(landed(alpha, WAIVED));
      for (const want of ["alpha#16", SHA_B.slice(0, 7)]) assert.ok(waived.includes(want), `the waived item shows ${want} (got ${JSON.stringify(waived)})`);
      assert.equal(await landed(alpha, WAIVED).locator("[data-pf-integrate-waived]").count(), 1, "the waived item carries the waived mark");
      assert.deepEqual(await hrefs(landed(alpha, WAIVED)), [[PR(16), "_blank"]], "the waived item links its PR");
      const plain = await textOf(landed(alpha, PLAIN));
      for (const want of ["alpha#22", SHA_C.slice(0, 7)]) assert.ok(plain.includes(want), `the plain item shows ${want} (got ${JSON.stringify(plain)})`);
      assert.equal(await landed(alpha, PLAIN).locator("[data-pf-integrate-waived]").count(), 0, "the plain item is not waived");
      assert.ok(!plain.includes("红轮次") && !plain.includes(FIX), `the plain item names no fix ticket or red rounds (got ${JSON.stringify(plain)})`);
      const beta = region.locator('[data-pf-integrate-project="beta"]');
      const betaError = await textOf(beta.locator("[data-pf-integrate-project-error]"));
      assert.ok(betaError && betaError.includes(BETA_ERROR), `beta shows pf's error (got ${JSON.stringify(betaError)})`);
      assert.equal(await beta.locator("[data-pf-integrate-landed]").count(), 0, "beta lists nothing");
    });
  });

  await check("S454-2", async () => {
    seed();
    await withPage({}, async (page) => {
      const region = await openIntegrate(page);
      const batch = region.locator(`[data-pf-integrate-batch="${BATCH}"]`);
      await waitFor(async () => (await batch.count()) > 0, "the running batch renders", NAV_MS);
      const steps = await batch.locator("[data-pf-integrate-step]").evaluateAll((nodes) => nodes.map((n) => [n.getAttribute("data-pf-integrate-step"), n.getAttribute("data-status")]));
      assert.deepEqual(steps, [["collect", "done"], ["revert", "done"], ["full-gate", "running"], ["close", "pending"]], "the four integrate steps in declared order with their status");
      assert.equal(await textOf(batch.locator("[data-pf-integrate-current]")), "full-gate", "the current step");
      assert.equal(await textOf(batch.locator("[data-pf-integrate-progress]")), "2/4", "done / declared steps");
      assert.ok((await textOf(batch)).includes("alpha#30"), "the batch shows its slug");
      const ended = region.locator(`[data-pf-integrate-batch="${RED_BATCH}"]`);
      assert.equal(await ended.count(), 1, "the ended batch is listed");
      assert.equal(await ended.locator("[data-pf-integrate-step]").count(), 0, "the ended batch has no live steps");
      const question = batch.locator("[data-pf-chat-question-text]");
      await waitFor(async () => ((await textOf(question)) ?? "").includes(QUESTION), "the batch's question shows", NAV_MS);
      const reads = batchReads();
      await batch.locator("[data-pf-chat-input]").fill(ANSWER);
      await batch.locator("[data-pf-chat-send]").click();
      await waitFor(() => answers().length === 1, "pf answer ran once");
      assert.deepEqual(answers()[0], ["--json", "answer", BATCH, `--text=${ANSWER}`], "the answer goes through the existing channel, no question id");
      await waitFor(() => batchReads() > reads, "the view reads again after the answer");
      await waitFor(async () => (await question.count()) === 0, "the answered question leaves the view");
    });
  });

  await check("S454-3", async () => {
    seed();
    await withPage({}, async (page) => {
      const integrationRequests = [];
      page.on("request", (request) => { if (new URL(request.url()).pathname === "/api/polyforge/integration") integrationRequests.push(request.url()); });
      await openWork(page);
      await workTab(page, /作业/).click();
      // The window opens on a status poll of this page, so a read the previous
      // scenario's closed page left in flight has landed before it.
      const first = statusReads();
      await waitFor(() => statusReads() > first, "a status poll ran", 60_000);
      const seen = statusReads(); const queued = landedReads() + batchReads();
      await waitFor(() => statusReads() >= seen + 2, "two more status polls ran", 60_000);
      assert.equal(landedReads() + batchReads(), queued, "parked on Jobs, no landed or batch queue read runs");
      assert.deepEqual(integrationRequests, [], "parked on Jobs, the page never asks the integration route");
      patchState({ hubDown: true });
      await waitFor(async () => (await workTab(page, /待集成/).count()) > 0, "a 待集成 Work sub-tab exists", 20_000);
      await workTab(page, /待集成/).click();
      const projectError = (name) => textOf(view(page).locator(`[data-pf-integrate-project="${name}"] [data-pf-integrate-project-error]`));
      await waitFor(async () => ((await projectError("alpha")) ?? "").includes(HUB_DOWN) && ((await projectError("beta")) ?? "").includes(HUB_DOWN),
        "every project shows pf's hub error", NAV_MS);
      // The mapping read fails too: the ↻ read's own error replaces the projects.
      patchState({ configDown: true });
      await view(page).locator("[data-pf-integrate-refresh]").click();
      const error = view(page).locator("[data-pf-integrate-error]");
      await waitFor(async () => ((await textOf(error)) ?? "").startsWith("待集成读取失败") && (await view(page).locator("[data-pf-integrate-project]").count()) === 0,
        "the view shows its read error", NAV_MS);
      await workTab(page, /作业/).click();
      await waitFor(async () => (await textOf(dock(page).locator("[data-pf-queue-count]"))) === "1 / 4", "Jobs still renders its queue line", NAV_MS);
      await waitFor(async () => (await dock(page).getByText("alpha#30").count()) > 0, "Jobs still renders the job", NAV_MS);
      assert.equal(await dock(page).getByText("Polyforge 不可用").count(), 0, "the panel itself stays available");
    });
    const banned = ledger(allLog).map(joined).filter((line) => /ops integrate|integration=1/.test(line));
    assert.deepEqual(banned, [], "no pf call names a removed integration face");
  });

  if (errors.length) console.log(`\nBROWSER ERRORS: ${errors.join(" | ")}`);
} catch (error) {
  failures.push("suite");
  console.log(`FAIL suite: ${String(error?.message ?? error).split("\n")[0]}`);
} finally {
  await h.close();
  if (failures.length) console.log(`\nFAILED ${failures.join(", ")} — ${failures.length} scenario(s) red`);
  else console.log("\nPASS all integrate-454 scenarios");
  console.log(`server log: ${join(work, "server-main.log")}`);
}
if (failures.length) process.exitCode = 1;
