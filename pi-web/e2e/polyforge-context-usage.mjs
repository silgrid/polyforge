// carries: lib/polyforge-status*
// — Work › Jobs: each job's card shows the running step
// session's context-window usage (the chat header's meter text), and the job
// detail shows its tokens over the window and the model.
//
// The suite starts its own `next dev` with a stub `pf` on PATH: `pf --json
// ops status` answers the jobs written to the stub's state.json, each running
// job row carrying the daemon's `context: {tokens?, window?, percent?, model?,
// as_of?}`, so the server's sanitizer and its window fallback (the chat
// header's catalog: here the pi model registry's models.json) run for real.
// The daemon data root is empty: every card is the job's bare card. No model
// or daemon is called. Every scenario prints `PASS S<n>`; the run ends `PASS
// all polyforge-context-usage scenarios`. At the pre-implementation tree the
// scenarios are assertion-level red: no card carries `[data-pf-context-meter]`
// and the detail has no `[data-pf-detail-context]` row.
//
//   S1 two jobs with different usage: A (pi) `42% / 1.0M`, B (claude)
//      `75% / 200k`, the hover naming the model.
//   S2 unknown values mislead nowhere: C (running, no context) has no meter and
//      no `%`/`NaN`/`undefined` on its card; D (window, tokens unknown) shows
//      `? / 1.0M`; E (tokens, no window) has no meter.
//   S3 A's next step session (new usage and model) shows on the existing
//      cadence: the meter follows to `20% / 400k`; only pre-existing polyforge
//      routes are requested, and the status and cards reads keep their 3 s
//      average spacing (no new, faster loop).
//   S4 the detail of A reads `420,000 / 1,000,000` and its model; C's detail
//      has no context row.
//   S5 F (pi, tokens and model, no window from the daemon) gets the window of
//      the pi model registry (models.json `contextWindow: 400000`): `25% / 400k`.
//
// The carries header names only lib/polyforge-status* (the sanitizer and the
// window fallback): components/PolyforgePanel* is already carried by
// polyforge-cards.mjs and polyforge.mjs, and a third declaration would select
// more than 8 browser suites for the light-tier fixture
// (lib/e2e-carries.test.mjs).
//
//   node e2e/polyforge-context-usage.mjs
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { chromium } from "playwright";
import { openPolyforgeDock as openDock, polyforgeDock as dock } from "./polyforge-dock.mjs";
import { ACTION_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, suiteDist, writeFileAtomic, writeHostSession } from "./harness.mjs";

const distDir = suiteDist("polyforge-context-usage");
assertNoDevServer(distDir);
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-context-e2e-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(work, "project");
const dataRoot = join(work, "polyforge-data");
const SESSION = "e2e-pf-context-usage-session";
const HOST_TEXT = "E2E pf context usage host session";
const EPOCH = "2026-11-13T04:00:00.000Z";
const LATER = "2026-11-13T04:05:00.000Z";
mkdirSync(project);
mkdirSync(dataRoot, { recursive: true });
writeHostSession(agentDir, { id: SESSION, cwd: project, text: HOST_TEXT, timestamp: EPOCH });
// The pi model registry the chat header reads: one provider whose model's
// window is the catalog fallback S5 expects (never contacted).
writeFileSync(join(agentDir, "models.json"), JSON.stringify({
  providers: {
    "e2e-ctx": {
      baseUrl: "http://127.0.0.1:9/v1",
      api: "openai-completions",
      apiKey: "e2e-ctx-key",
      models: [{ id: "catalog-model", name: "E2E Catalog Model", contextWindow: 400000, maxTokens: 8192 }],
    },
  },
}));

// ---- fixture: six running jobs, the daemon's status rows ----
const row = (letter, n, candidate, context) => ({ work_item_id: `wi_E2Ectx000${letter}`, slug: `${n}`, status: "running",
  spawn_n: 1, step_age_sec: 60, last_work_ago_sec: 2, candidate, session_fingerprint: "fp-ctx", origin: { kind: "chat", owner: "u_e2e" },
  ...(context ? { context } : {}) });
const A_FIRST = { tokens: 420000, window: 1000000, percent: 42, model: "example-gateway/model-a", as_of: EPOCH };
const A_NEXT = { tokens: 80000, window: 400000, percent: 20, model: "example-gateway/model-b", as_of: LATER };
const jobsWith = (aContext) => [
  row("A", 1, "pi:example-gateway/model-a", aContext),
  row("B", 2, "claude:claude-model-e2e", { tokens: 150000, window: 200000, percent: 75, model: "claude-model-e2e", as_of: EPOCH }),
  row("C", 3, "pi:e2e-ctx/model-c"),
  row("D", 4, "pi:e2e-ctx/model-d", { window: 1000000, as_of: EPOCH }),
  row("E", 5, "pi:e2e-ctx/model-e", { tokens: 5000, as_of: EPOCH }),
  row("F", 6, "pi:e2e-ctx/catalog-model", { tokens: 100000, model: "e2e-ctx/catalog-model", as_of: EPOCH }),
];
const WI = (letter) => `wi_E2Ectx000${letter}`;

// ---- stub pf ----
const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const setOps = (jobs) => writeFileAtomic(stateFile, JSON.stringify({
  ops: { machine_id: "m-e2e", workspaces: [work], env_names: ["E2E_ENV"], jobs, queue: { pending: 0, running: jobs.length, max_concurrent: 8 } },
}));
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename)); const args = process.argv.slice(2);
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const out = (v) => { const b = Buffer.from(JSON.stringify(v) + "\\n"); let o = 0;
  while (o < b.length) { try { o += fs.writeSync(1, b, o); } catch (e) { if (e.code !== "EAGAIN") throw e; } } process.exit(0); };
if (args.includes("--version")) { process.stdout.write("v2.20.11\\n"); process.exit(0); }
const rest = args.filter((a) => a !== "--json");
if (rest[0] === "ops" && rest[1] === "status") out(s.ops);
if (rest[0] === "watch") {
  const wi = rest[1]; const job = s.ops.jobs.find((j) => j.work_item_id === wi || j.slug === wi);
  out({ work_item: job ? job.work_item_id : wi, slug: job ? job.slug : wi, known: true, job: { status: "running" },
    execution: { workflow_status: "running", current_step: "build", steps: { build: { status: "in_progress", started_at: "${EPOCH}" } } },
    events: [], last_seq: 0 });
}
if (rest[0] === "msg") out({ conversations: [] });
if (rest[0] === "task" && rest[1] === "list") out({ tasks: [], now: new Date().toISOString() });
if (rest[0] === "config" && rest[1] === "list") out({ items: [] });
if (rest[0] === "config" && rest[1] === "workspace") out({ mapped: false, workspace: null });
if (rest[0] === "recall") out({ items: [], total: 0 });
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
setOps(jobsWith(A_FIRST));

// The panel's polyforge routes before this change: a request to any other
// polyforge path is a new route.
const EXISTING_ROUTES = new Set(["answer", "cards", "cells", "config", "decisions", "detect", "install", "memory", "messages",
  "progress", "projects", "status", "tasks", "users", "watch", "work-items", "workflows"]);
const STATUS = "/api/polyforge/status";
const CARDS = "/api/polyforge/cards";
/** The existing cadence (hooks/usePolyforge.ts: 3 s status poll; the cards poll's 3 s default). */
const CADENCE_MS = 3000;

// ---- scenario bookkeeping and helpers ----
const failures = [];
const check = createCheck({ failures });
const h = createHarness({
  locale: "zh-CN",
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN" },
  timeout: ACTION_MS,
  errors: ["pageerror", "5xx"],
  session: SESSION,
  ready: HOST_TEXT,
  async setup(page) {
    page.__api = [];
    page.on("request", (request) => {
      const { pathname } = new URL(request.url());
      if (pathname.startsWith("/api/")) page.__api.push({ at: Date.now(), path: pathname });
    });
  },
});
const { errors, withPage } = h;
// Bounded poll of a real condition (own deadline, 200 ms steps): `true` ends it,
// anything else is the last value the failure names.
const waitFor = createWaitFor({ ms: 30_000, done: (value) => value === true, describe: (label, last) => `${label} (last: ${JSON.stringify(last)})` });
const topTab = (page, re) => dock(page).locator('[role="tablist"]').first().getByRole("tab").filter({ hasText: re }).first();
const cardOf = (page, letter) => dock(page).locator(`[data-pf-card-wi="${WI(letter)}"]`);
const textOf = async (locator) => ((await locator.count()) > 0 ? (await locator.first().innerText()).replace(/\s+/g, " ").trim() : null);
async function openJobs(page) {
  await openDock(page);
  await topTab(page, /工单|work/i).waitFor({ timeout: 20_000 });
  await topTab(page, /工单|work/i).click();
  await waitFor(async () => (await dock(page).locator("[data-pf-card]").count()) === 6 || await dock(page).locator("[data-pf-card]").count(), "six job cards render", 60_000);
}
const meterOf = async (page, letter) => {
  const meter = cardOf(page, letter).locator("[data-pf-context-meter]");
  if ((await meter.count()) === 0) return null;
  return { text: (await meter.first().innerText()).trim(), title: await meter.first().getAttribute("title") };
};
async function awaitMeter(page, letter, want, label) {
  let seen;
  await waitFor(async () => { seen = await meterOf(page, letter); return seen?.text === want || seen; }, label);
  return seen;
}
const detailContext = (page) => dock(page).locator("[data-pf-detail-context]");

try {
  await h.launchBrowser(() => chromium.launch());
  await h.startServer({ name: "polyforge-context-usage", logFile: join(work, "server.log"), distDir, env: {
    PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: home, POLYFORGE_DATA_DIR: dataRoot,
    PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: "" } });

  await check("S1", async () => {
    setOps(jobsWith(A_FIRST));
    await withPage(async (page) => {
      await openJobs(page);
      const a = await awaitMeter(page, "A", "42% / 1.0M", "A (pi) shows 42% / 1.0M");
      assert.ok(a.title?.includes("example-gateway/model-a"), `A's meter names its model on hover (got ${JSON.stringify(a.title)})`);
      const b = await awaitMeter(page, "B", "75% / 200k", "B (claude) shows 75% / 200k");
      assert.ok(b.title?.includes("claude-model-e2e"), `B's meter names its model on hover (got ${JSON.stringify(b.title)})`);
    });
  });

  await check("S2", async () => {
    setOps(jobsWith(A_FIRST));
    await withPage(async (page) => {
      await openJobs(page);
      // Settled: the known meters are on screen, so the unknown ones had the same read.
      await awaitMeter(page, "A", "42% / 1.0M", "A's meter (the read is in)");
      await awaitMeter(page, "D", "? / 1.0M", "D (tokens unknown) shows ? / 1.0M, never a number");
      assert.equal(await meterOf(page, "C"), null, "C (no context) has no meter");
      const cText = await textOf(cardOf(page, "C"));
      for (const misleading of ["%", "NaN", "undefined", "0%"]) {
        assert.ok(!cText.includes(misleading), `C's card shows no ${misleading} (got ${JSON.stringify(cText)})`);
      }
      assert.equal(await meterOf(page, "E"), null, "E (no window) has no meter");
      const eText = await textOf(cardOf(page, "E"));
      assert.ok(!eText.includes("%") && !eText.includes("NaN") && !eText.includes("undefined"), `E's card shows no percent (got ${JSON.stringify(eText)})`);
    });
  });

  await check("S3", async () => {
    setOps(jobsWith(A_FIRST));
    await withPage(async (page) => {
      await openJobs(page);
      await awaitMeter(page, "A", "42% / 1.0M", "A's first step session");
      const reloads = [];
      page.on("framenavigated", (frame) => { if (frame === page.mainFrame()) reloads.push(frame.url()); });
      const since = Date.now();
      setOps(jobsWith(A_NEXT));
      const next = await awaitMeter(page, "A", "20% / 400k", "A's meter follows its next step session");
      assert.ok(next.title?.includes("example-gateway/model-b"), `the hover names the new session's model (got ${JSON.stringify(next.title)})`);
      // Measure once the panel has run a few of its own ticks after the change.
      const after = (path) => page.__api.filter((r) => r.at >= since && r.path === path).map((r) => r.at);
      await waitFor(() => after(STATUS).length >= 4 || after(STATUS).length, "four status reads after the change", 40_000);
      await waitFor(() => after(CARDS).length >= 4 || after(CARDS).length, "four card reads after the change", 40_000);
      const routes = [...new Set(page.__api.filter((r) => r.path.startsWith("/api/polyforge/")).map((r) => r.path.split("/")[3]))];
      const fresh = routes.filter((route) => !EXISTING_ROUTES.has(route));
      assert.deepEqual(fresh, [], `only pre-existing polyforge routes are read (saw ${JSON.stringify(routes)})`);
      for (const [path, at] of [[STATUS, after(STATUS)], [CARDS, after(CARDS)]]) {
        // The average spacing: a late timer tick never reads as a faster loop.
        const spacing = (at.at(-1) - at[0]) / (at.length - 1);
        assert.ok(spacing >= CADENCE_MS * 0.8, `${path} keeps its ${CADENCE_MS} ms cadence (average ${Math.round(spacing)} ms over ${at.length} reads)`);
      }
      assert.deepEqual(reloads, [], "no page navigation happened");
    });
  });

  await check("S4", async () => {
    setOps(jobsWith(A_FIRST));
    await withPage(async (page) => {
      await openJobs(page);
      await awaitMeter(page, "A", "42% / 1.0M", "A's meter (the read is in)");
      await cardOf(page, "A").click();
      await waitFor(async () => {
        const text = await textOf(detailContext(page));
        return Boolean(text?.includes("420,000 / 1,000,000") && text.includes("example-gateway/model-a")) || text;
      }, "A's detail reads 420,000 / 1,000,000 and its model");
      const summary = await textOf(dock(page).locator("[data-pf-detail-summary]"));
      assert.ok(summary?.includes("上下文"), `the row is labelled 上下文 (got ${JSON.stringify(summary)})`);
      await cardOf(page, "C").click();
      await waitFor(async () => {
        const selected = await cardOf(page, "C").getAttribute("aria-pressed");
        const summaryText = await textOf(dock(page).locator("[data-pf-detail-summary]"));
        const rows = await detailContext(page).count();
        return (selected === "true" && Boolean(summaryText?.includes("pi:e2e-ctx/model-c")) && rows === 0) || { selected, rows, summaryText };
      }, "C's detail has no context row");
    });
  });

  await check("S5", async () => {
    setOps(jobsWith(A_FIRST));
    await withPage(async (page) => {
      await openJobs(page);
      const f = await awaitMeter(page, "F", "25% / 400k", "F (pi, no daemon window) shows the registry's 400k window: 25% / 400k");
      assert.ok(f.title?.includes("e2e-ctx/catalog-model"), `F's meter names its model (got ${JSON.stringify(f.title)})`);
    });
  });

  if (errors.length) {
    console.log(`\nBROWSER ERRORS: ${errors.join(" | ")}`);
    failures.push("browser errors");
  }
  if (failures.length) {
    console.log(`\nFAILED ${failures.join(", ")} — ${failures.length} scenario(s) red`);
    process.exitCode = 1;
  } else {
    console.log("\nPASS all polyforge-context-usage scenarios");
  }
} finally {
  await h.close();
  rmSync(work, { force: true, recursive: true });
}
