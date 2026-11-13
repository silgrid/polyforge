// carries: lib/polyforge-verify-388*
// — Work › Jobs: the queue counter separates the jobs holding a
// job slot from the jobs whose daemon-run verification waits in the heavy-run
// admission queue (such a job holds no slot, so the status
// snapshot's `queue.running` leaves it out), and each queued job's card shows
// its queue position and reason, from the job row's `verifyQueue` (the
// status route's sanitized `verify_queue`).
//
// The suite starts its own `next dev` and drives the panel through its
// existing HTTP faces, mocked in the browser with the routes' documented wire
// shapes (`/api/polyforge/status`, `/cards`, `/watch`), the panel-213
// convention: no pf CLI, daemon or model call. Every scenario prints
// `PASS S392-<n>`; the run ends `PASS all verify-queue-392 scenarios`. At the
// pre-implementation tree every scenario is assertion-level red: the counter
// reads `2 / 16` with six jobs queued and no card carries a queue mark.
//
//   S392-1 (AC1) eight jobs, six queued: `2 运行 · 6 等 verification / 16`;
//          none queued: `2 / 16`, exactly as before.
//   S392-2 (AC2) each queued card shows `verification（排队 #n）` and its
//          reason, the `pf ops status` VERIFY face `queued #n: <reason>` on
//          hover; an unqueued card has no mark; the selected queued job's
//          current step reads `verification（排队 #n）`.
//   S392-3 (AC3) one queued verification is admitted and another job newly
//          queues: the counter and the cards follow on the running poll,
//          with no reload.
//
//   node e2e/polyforge-verify-queue-392.mjs
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { openPolyforgeDock as openDock, polyforgeDock as dock } from "./polyforge-dock.mjs";
import { ACTION_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, mockContextUsage, mockPolyforge, NO_MESSAGES, NO_TASKS, suiteDist, writeHostSession } from "./harness.mjs";

const distDir = suiteDist("polyforge-verify-queue-392");
assertNoDevServer(distDir);
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-392-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-verify-queue-392-session";
const HOST_TEXT = "E2E pf verify queue 392 host session";
const EPOCH = "2026-11-08T03:00:00.000Z";
mkdirSync(project);
writeHostSession(agentDir, { id: SESSION, cwd: project, text: HOST_TEXT, timestamp: EPOCH });

// ---- fixture: eight running jobs; two hold a slot, six wait for admission ----
const LOAD = "load 9.20 at or above the threshold 8";
const SLOTS = "heavy-run slots full (1/1 running)";
const job = (n) => ({ wi: `wi_E2E392J${n}`, slug: `${n}`, status: "running",
  stepAgeSec: 60 + n, idleAgeSec: n, candidate: "pi:e2e/model", spawnN: 1 });
const JOBS = [1, 2, 3, 4, 5, 6, 7, 8].map(job);
const queued = (position, reason) => ({ position, reason, since: EPOCH });
// J1 (full bar) and J2 (build) hold slots; J3..J8 queue at positions 1..6.
const FIRST = new Map([[3, queued(1, SLOTS)], [4, queued(2, LOAD)], [5, queued(3, LOAD)], [6, queued(4, LOAD)], [7, queued(5, LOAD)], [8, queued(6, LOAD)]]);
const withQueue = (queue) => JOBS.map((entry, i) => (queue.has(i + 1) ? { ...entry, verifyQueue: queue.get(i + 1) } : { ...entry }));
const card = (entry) => ({ job: entry, goal: `E2E 392 job ${entry.slug}`, startedAt: EPOCH,
  steps: [{ id: "build", state: "timed", durationSec: 30, runs: 1 }, { id: "verification", state: "unstarted", runs: 0 }],
  currentStep: "verification", progress: { available: true, entries: [] } });
// The one mutable fixture every route mock answers from.
let fixture;
const setFixture = (running, jobs) => { fixture = { running, jobs }; };
const statusBody = () => ({ pfPresent: true, status: { queue: { running: fixture.running, max: 16 }, jobs: fixture.jobs, tasks: { running: 0, completed: 0 } } });
const cardsBody = () => ({ pfPresent: true, cards: fixture.jobs.map(card) });
const watchBody = (wi) => {
  const entry = fixture.jobs.find((candidate) => candidate.wi === wi) ?? fixture.jobs[0];
  return { status: { wi: entry.wi, slug: entry.slug, workflowStatus: "running", currentStep: "verification", updatedAt: EPOCH,
    steps: [{ name: "build", status: "done", startedAt: EPOCH }, { name: "verification", status: "pending" }],
    declared_steps: [{ id: "build" }, { id: "verification" }], events: [] } };
};

// ---- scenario bookkeeping and helpers ----
const failures = [];
const check = createCheck({ failures });
const CONTEXT_USAGE = { percent: 10, contextWindow: 200_000, tokens: 20_000 };
const h = createHarness({
  locale: "zh-CN",
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN" },
  timeout: ACTION_MS,
  errors: ["pageerror"],
  session: SESSION,
  ready: HOST_TEXT,
  async setup(page) {
    await mockPolyforge(page, {
      status: () => statusBody(),
      cards: () => cardsBody(),
      watch: (url) => watchBody(url.searchParams.get("wi")),
      tasks: NO_TASKS,
      messages: { ...NO_MESSAGES, pfVersion: "v2.20.22" },
    });
    // both context reads answer one fixture, so no live read wins.
    await mockContextUsage(page, SESSION, CONTEXT_USAGE);
  },
});
const { errors, withPage } = h;
const topTab = (page, re) => dock(page).locator('[role="tablist"]').first().getByRole("tab").filter({ hasText: re }).first();
const cardOf = (page, n) => dock(page).locator(`[data-pf-card-wi="wi_E2E392J${n}"]`);
const counter = (page) => dock(page).locator("[data-pf-queue-count]");
async function openJobs(page) {
  await openDock(page);
  await topTab(page, /工单|work/i).waitFor({ timeout: 20_000 });
  await topTab(page, /工单|work/i).click();
  await waitFor(async () => (await dock(page).locator("[data-pf-card]").count()) === 8, "eight job cards render", 30_000);
}
// Bounded poll of a real condition (own deadline, 200 ms steps).
const waitFor = createWaitFor({ ms: 20_000, done: (value) => value === true, describe: (label, last) => `${label} (last: ${JSON.stringify(last)})` });
const textOf = async (locator) => ((await locator.count()) > 0 ? (await locator.first().innerText()).replace(/\s+/g, " ").trim() : null);
async function awaitCounter(page, want) {
  let seen;
  await waitFor(async () => { seen = await textOf(counter(page)); return seen === want || seen; }, `the queue counter reads ${JSON.stringify(want)}`);
}
async function queueMark(page, n) {
  const mark = cardOf(page, n).locator("[data-pf-verify-queue]");
  if ((await mark.count()) === 0) return null;
  return { text: (await mark.first().innerText()).replace(/\s+/g, " ").trim(), title: await mark.first().getAttribute("title") };
}
async function awaitMarks(page, want, label) {
  await waitFor(async () => {
    const got = {};
    for (const n of [1, 2, 3, 4, 5, 6, 7, 8]) {
      const mark = await queueMark(page, n);
      got[n] = mark ? mark.title : null;
    }
    return JSON.stringify(got) === JSON.stringify(want) || got;
  }, label);
}
const face = (q) => `queued #${q.position}: ${q.reason}`;
const marksFor = (queue) => Object.fromEntries([1, 2, 3, 4, 5, 6, 7, 8].map((n) => [n, queue.has(n) ? face(queue.get(n)) : null]));

try {
  await h.launchBrowser(() => chromium.launch());
  await h.startServer({ name: "polyforge-verify-queue-392", logFile: join(work, "server.log"), distDir, env: {
    PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: "" } });

  await check("S392-1", async () => {
    setFixture(2, withQueue(FIRST));
    await withPage(async (page) => {
      await openJobs(page);
      await awaitCounter(page, "2 运行 · 6 等 verification / 16");
    });
    setFixture(2, withQueue(new Map()));
    await withPage(async (page) => {
      await openJobs(page);
      await awaitCounter(page, "2 / 16");
    });
  });

  await check("S392-2", async () => {
    setFixture(2, withQueue(FIRST));
    await withPage(async (page) => {
      await openJobs(page);
      await awaitMarks(page, marksFor(FIRST), "each queued card carries the VERIFY face on hover, the others none");
      for (const [n, q] of FIRST) {
        const mark = await queueMark(page, n);
        assert.ok(mark.text.includes(`verification（排队 #${q.position}）`), `card ${n} shows its step as verification（排队 #${q.position}） (got ${JSON.stringify(mark.text)})`);
        assert.ok(mark.text.includes(q.reason), `card ${n} shows the reason ${JSON.stringify(q.reason)} (got ${JSON.stringify(mark.text)})`);
        assert.doesNotMatch(mark.text, /pending|in_progress|待开始|进行中/, `card ${n}: no raw step status`);
      }
      // The selected queued job's current step reads the same label.
      await cardOf(page, 5).click();
      await waitFor(async () => {
        const facts = await textOf(dock(page).locator("[data-pf-detail-summary]"));
        return Boolean(facts && facts.includes("当前步骤") && facts.includes("verification（排队 #3）")) || facts;
      }, "the selected queued job's current step reads verification（排队 #3）");
    });
  });

  await check("S392-3", async () => {
    setFixture(2, withQueue(FIRST));
    await withPage(async (page) => {
      await openJobs(page);
      await awaitCounter(page, "2 运行 · 6 等 verification / 16");
      await awaitMarks(page, marksFor(FIRST), "the first snapshot's queue marks");
      const reloads = [];
      page.on("framenavigated", (frame) => { if (frame === page.mainFrame()) reloads.push(frame.url()); });
      // J3 is admitted (its verify_queue disappears, it now holds a slot); the
      // rest move up one place.
      const admitted = new Map([[4, queued(1, LOAD)], [5, queued(2, LOAD)], [6, queued(3, LOAD)], [7, queued(4, LOAD)], [8, queued(5, LOAD)]]);
      setFixture(3, withQueue(admitted));
      await awaitCounter(page, "3 运行 · 5 等 verification / 16");
      await awaitMarks(page, marksFor(admitted), "the admitted job loses its mark, the others move up");
      // J1 finishes its build and its verification newly queues behind them.
      const joined = new Map([...admitted, [1, queued(6, LOAD)]]);
      setFixture(2, withQueue(joined));
      await awaitCounter(page, "2 运行 · 6 等 verification / 16");
      await awaitMarks(page, marksFor(joined), "the newly queued job gains its mark");
      assert.deepEqual(reloads, [], "no page navigation happened");
    });
  });

  if (errors.length) console.log(`\nBROWSER ERRORS: ${errors.join(" | ")}`);
  if (failures.length) {
    console.log(`\nFAILED ${failures.join(", ")} — ${failures.length} scenario(s) red`);
    process.exitCode = 1;
  } else {
    console.log("\nPASS all verify-queue-392 scenarios");
  }
} finally {
  await h.close();
  rmSync(work, { force: true, recursive: true });
}
