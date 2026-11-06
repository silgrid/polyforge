// carries: hooks/usePolyforgeProgress*, app/api/polyforge/progress/route.ts
// — Polyforge job detail v2: summary → DAG → whole-job execution progress.
//
// Authored PRE-CODE against artifacts/spec.md (Acceptance criteria + `####
// Scenario` blocks); it does not read the implementation. The suite drives only
// the deterministic interfaces `plan.md` pinned for this change: the existing
// Jobs tab (`[data-pf-card-wi]`, the deselect control), the read face at
// `GET /api/polyforge/progress?wi=` and the new `data-pf-*` hooks
// (`detail-summary`, `summary-goal`, `goal-toggle`, `detail-graph`,
// `detail-scroll`, `progress`, `progress-omitted`, `progress-kind`,
// `progress-tone`). The existing status/cards/watch reads keep their documented
// wire shapes and are mocked for the UI scenarios; the progress route itself is
// exercised against a throwaway daemon data root so its whole-job enumeration,
// event whitelist, exact `omitted` count and N6 degradation are real.
//
// At the pre-implementation tree every kept scenario is assertion-level RED: the
// detail has no summary/graph/scroll anchors and the old progress body carries
// `entries`, not `lines`/`omitted`, and a missing job index answers 400 instead
// of 200 `available:false`. Every scenario prints `PASS S238-<n>` on success (its
// binding marker).
//
//   node e2e/polyforge-238.mjs
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { openPolyforgeDock as openDock, polyforgeDock as dock } from "./polyforge-dock.mjs";
import { createCheck, createHarness, createWaitFor, mockPolyforge, reply, writeHostSession } from "./harness.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
process.env.PI_WEB_DIST_DIR ||= ".next/e2e/polyforge-238";
if (process.env.PI_WEB_DIST_DIR.startsWith(".next/e2e/")) rmSync(join(root, process.env.PI_WEB_DIST_DIR), { recursive: true, force: true });
assert.ok(!existsSync(join(root, process.env.PI_WEB_DIST_DIR, "dev/lock")), "Run in a checkout without an active dev server");

const work = mkdtempSync(join(tmpdir(), "pi-web-pf-238-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const dataRoot = join(work, "data");
const SESSION = "e2e-pf-238-session";
const ROUTE = "/api/polyforge/progress";
const UI_WAIT = 8_000;
mkdirSync(project);
mkdirSync(join(dataRoot, "events"), { recursive: true });
writeHostSession(agentDir, { id: SESSION, cwd: project, text: "E2E pf 238 host session", timestamp: "2026-11-01T00:00:00.000Z" });

// ── UI fixtures: documented wire shapes of the existing reads ──────────────
const GOAL = "feat: 作业详情三段重构——summary first, then the step graph, then the whole-job execution progress as the last block; the goal folds to three lines and expands on demand";
const job = (wi, slug, status, stepAgeSec = 125) => ({ wi, slug, status, stepAgeSec, idleAgeSec: 2, candidate: "pi:e2e/model", spawnN: 1 });
const UI = job("wi_S238UI01", "task#238", "running", 125);
const DONE = job("wi_S238UI02", "task#239", "completed", 900);
const step = (id, durationSec, runs) => ({ id, state: "timed", durationSec, runs });
const STEPS = ["grill", "spec", "plan", "code", "review"];
const cards = [
  { job: UI, goal: GOAL, startedAt: new Date(Date.now() - 300_000).toISOString(), steps: [step("grill", 15, 1), step("spec", 45, 1), step("plan", 35, 2), step("code", 7, 1), { id: "review", state: "unstarted", runs: 0 }], currentStep: "code", progress: { available: false, entries: [] } },
  { job: DONE, goal: "finished job", startedAt: new Date(Date.now() - 900_000).toISOString(), steps: STEPS.map((id) => step(id, 10, 1)), currentStep: "review", progress: { available: false, entries: [] } },
];
const status = { pfPresent: true, status: { queue: { running: 1, max: 4 }, jobs: [UI, DONE], tasks: { running: 0, completed: 0 } } };
const uiStarted = Date.now() - 300_000;
const uiAt = (sec) => new Date(uiStarted + sec * 1_000).toISOString();
const watchBase = (jobStatus, workflowStatus, steps, summary = {}) => ({ status: {
  wi: jobStatus.wi, slug: jobStatus.slug, workflowStatus, currentStep: "code", updatedAt: new Date().toISOString(),
  steps, declared_steps: STEPS.map((id) => ({ id })), events: [],
  ...summary,
} });
const watchUI = () => watchBase(UI, "running", [
  { name: "grill", status: "done", startedAt: uiAt(0) }, { name: "spec", status: "done", startedAt: uiAt(15) },
  { name: "plan", status: "done", startedAt: uiAt(60) }, { name: "code", status: "in_progress", startedAt: uiAt(120) },
  { name: "review", status: "pending" },
], {
  summary: "E2E-S238-SUMMARY", summary_age_sec: 3, summary_error: "E2E-S238-SUMMARY-ERROR", summary_capped: true,
  progress: { command: "E2E-S238-COMMAND-ROW --token=SECRET", command_age_sec: 5, tool_calls: 2, recent_files: ["e2e/recent.ts"] },
  origin: { kind: "chat", owner: "u_e2e238" },
});
const watchDONE = () => watchBase(DONE, "completed", STEPS.map((id) => ({ name: id, status: "done", startedAt: uiAt(0) })));
const line = (id, kind, text, extra = {}) => ({ id, kind, text, ...extra });
const eventLine = (id, text, event, tone) => line(id, "event", text, { event, ...(tone ? { tone } : {}) });
const BASE_LINES = [
  line("l1", "narration", "E2E-S238-UI-NARRATION"),
  line("l2", "tool", "bash: curl --token=*** https://e2e.invalid"),
  eventLine("l3", "00:00:01 ▶ code #1 · pi:e2e/model", "step_started", "accent"),
  eventLine("l4", "00:00:02 ■ code +15s", "step_done", "dim"),
];
// three spawn blocks (a done, a failed and the newest at
// needs_input), a leading line before the first start, and enough lines in
// the newest block for the detail region to scroll.
const blockLines = () => [
  line("b0", "narration", "E2E-S238-LEADING"),
  eventLine("b1", "00:00:01 ▶ grill #1 · pi:e2e/model", "step_started", "accent"),
  line("b2", "narration", "E2E-S238-BLOCK-1"),
  eventLine("b3", "00:00:02 ■ grill +15s", "step_done", "dim"),
  eventLine("b4", "00:00:20 ▶ spec #1 · pi:e2e/model", "step_started", "accent"),
  line("b5", "narration", "E2E-S238-BLOCK-2"),
  eventLine("b6", "00:00:30 ✕ spec · gate-failed", "step_failed", "red"),
  eventLine("b7", "00:00:40 ▶ spec #2 · claude:e2e-model", "step_started", "accent"),
  line("b8", "narration", "E2E-S238-BLOCK-3"),
  eventLine("b9", "00:00:50 ? spec", "needs_input", "amber"),
  ...Array.from({ length: 40 }, (_, i) => line(`b10-${i}`, "narration", `E2E-S238-BLOCK-3-TAIL-${String(i + 1).padStart(2, "0")}`)),
];
const progressBody = (lines = BASE_LINES, omitted = 0) => ({ available: true, lines, omitted });
const bulkLines = (count) => Array.from({ length: count }, (_, i) => line(`bulk-${i}`, "narration", `E2E-S238-BULK-${String(i + 1).padStart(3, "0")}`));

// ── route fixture: a throwaway daemon data root with real capsules ─────────
const MAIN = "wi_S238Main01";
const MANY = "wi_S238Many01";
const TERMINAL_DONE = "wi_S238Done01";
const TERMINAL_CANCEL = "wi_S238Cancel01";
const eventBase = Date.parse("2026-11-01T10:00:00Z");
const eventAt = (sec) => new Date(eventBase + sec * 1_000).toISOString();
const capsule = (wi) => join(work, "capsules", wi);
const ensureCapsule = (wi) => {
  const dir = capsule(wi);
  mkdirSync(join(dir, "runtime-state"), { recursive: true });
  mkdirSync(join(dir, "session"), { recursive: true });
  return dir;
};
const jsonl = (records) => records.map((record) => JSON.stringify(record)).join("\n") + "\n";
const piSession = (id, ts) => ({ type: "session", version: 3, id, timestamp: ts, cwd: project });
const piMessage = (id, ts, content) => ({ type: "message", id, parentId: null, timestamp: ts, message: { role: "assistant", content } });
const piText = (id, ts, text) => piMessage(id, ts, [{ type: "text", text }]);
const claudeMessage = (id, ts, content) => ({ type: "assistant", id, timestamp: ts, message: { role: "assistant", content } });
const spawn = (wi, atSec, stepId, spawnN, transcript, harness) => ({ at: eventAt(atSec), work_item: wi, step_id: stepId, spawn_n: spawnN, started_at: eventAt(atSec), transcript, harness });
const event = (wi, sec, kind, body = {}) => ({ at: eventAt(sec), kind, work_item: wi, body, seq: sec + 1 });

const mainDir = ensureCapsule(MAIN);
const spawn1 = join(mainDir, "session", "spawn1.jsonl");
const spawn2 = join(mainDir, "session", "spawn2.jsonl");
const spawn3 = join(mainDir, "session", "spawn3.jsonl");
writeFileSync(spawn1, jsonl([
  piSession("s1", eventAt(0)),
  piText("p1", eventAt(5), "E2E-S238-SPAWN1-NARRATION"),
  piMessage("p1t", eventAt(10), [
    { type: "thinking", thinking: "E2E-S238-THINKING" },
    { type: "toolCall", id: "t1", name: "bash", arguments: { command: "curl --token=SECRET-S238-TOKEN https://e2e.invalid" } },
  ]),
  { type: "message", id: "u1", parentId: null, timestamp: eventAt(11), message: { role: "user", content: "E2E-S238-SECRET-USER" } },
  { type: "message", id: "tr1", parentId: null, timestamp: eventAt(12), message: { role: "toolResult", toolName: "bash", content: [{ type: "text", text: "E2E-S238-SECRET-TOOL-RESULT" }] } },
]));
writeFileSync(spawn2, jsonl([
  piSession("s2", eventAt(20)),
  piText("p2", eventAt(25), "E2E-S238-SPAWN2-NARRATION"),
]));
writeFileSync(spawn3, jsonl([
  { type: "user", timestamp: eventAt(64), message: { role: "user", content: "E2E-S238-SECRET-CLAUDE-USER" } },
  claudeMessage("c1", eventAt(65), [
    { type: "text", text: "E2E-S238-SPAWN3-CLAUDE" },
    { type: "tool_use", name: "bash", input: { command: "echo E2E-S238-CLAUDE-TOOL" } },
  ]),
]));
writeFileSync(join(mainDir, "runtime-state", "spawn-journal.jsonl"), jsonl([
  spawn(MAIN, 0, "grill", 1, spawn1, "pi"),
  spawn(MAIN, 20, "spec", 1, spawn2, "pi"),
  spawn(MAIN, 60, "spec", 2, spawn3, "claude"),
]));
const MAIN_EVENTS = [
  event(MAIN, 0, "job_started", { spawn_n: 1 }),
  event(MAIN, 0, "step_started", { step: "grill", spawn_n: 1, model: "pi:e2e/model" }),
  event(MAIN, 15, "step_done", { step: "grill", spawn_n: 1 }),
  event(MAIN, 20, "step_started", { step: "spec", spawn_n: 1, model: "pi:e2e/model" }),
  event(MAIN, 30, "step_failed", { step: "spec", spawn_n: 1, cause: "gate-failed" }),
  event(MAIN, 32, "step_dead", { step: "plan", spawn_n: 1, cause: "timeout-after-3-retries" }),
  event(MAIN, 35, "needs_recovery", { reason: "recovery reason", detail: "recovery detail", spawn_n: 1 }),
  event(MAIN, 36, "push_failed", { error: "push error token=SECRET-S238-TOKEN", version: 7 }),
  event(MAIN, 40, "published", { version: 7 }),
  event(MAIN, 41, "candidate_switched", { step: "spec", from: "pi:e2e/model", to: "claude:e2e-model" }),
  event(MAIN, 42, "envelope_rejected", { step: "spec", detail: "E2E-S238-ENVELOPE" }),
  event(MAIN, 43, "authored_tests_rebaselined", { step: "spec", files: ["E2E-S238-REBASELINED"] }),
  event(MAIN, 45, "needs_input", { step: "grill", spawn_n: 1 }),
  event(MAIN, 46, "answered", { step: "grill", spawn_n: 1 }),
  event(MAIN, 47, "gate_verdict", { step: "spec", spawn_n: 1 }),
  event(MAIN, 48, "rewind", { gate: "spec-review", target: "spec", spawn_n: 1 }),
  event(MAIN, 60, "step_started", { step: "spec", spawn_n: 2, model: "claude:e2e-model" }),
  event(MAIN, 70, "step_done", { step: "spec", spawn_n: 2 }),
  event(MAIN, 75, "terminal", { status: "failed" }),
  event(MAIN, 76, "job_stopped", { spawn_n: 2 }),
  event(MAIN, 80, "transcript_missing", { spawn_n: 2 }),
  event(MAIN, 81, "exec_init", { spawn_n: 2 }),
];
writeFileSync(join(dataRoot, "events", `wi-${MAIN}.jsonl`), jsonl(MAIN_EVENTS));

const manyDir = ensureCapsule(MANY);
const manyTranscript = join(manyDir, "session", "many.jsonl");
writeFileSync(manyTranscript, jsonl([
  piSession("many", eventAt(0)),
  ...Array.from({ length: 2005 }, (_, i) => piText(`m${i}`, new Date(eventBase + (i + 1) * 1_000).toISOString(), `E2E-S238-MANY-${String(i + 1).padStart(4, "0")}`)),
]));
writeFileSync(join(manyDir, "runtime-state", "spawn-journal.jsonl"), jsonl([spawn(MANY, 0, "many", 1, manyTranscript, "pi")]));

for (const [wi, terminalStatus] of [[TERMINAL_DONE, "completed"], [TERMINAL_CANCEL, "cancelled"]]) {
  ensureCapsule(wi);
  writeFileSync(join(dataRoot, "events", `wi-${wi}.jsonl`), jsonl([event(wi, 10, "terminal", { status: terminalStatus })]));
}

const runtimeState = join(dataRoot, "runtime-state.json");
const writeRuntimeState = () => writeFileSync(runtimeState, JSON.stringify({ version: 1, jobs: [MAIN, MANY, TERMINAL_DONE, TERMINAL_CANCEL].map((wi) => ({
  work_item_id: wi, capsule_dir: capsule(wi),
})) }));
writeRuntimeState();
// ── scenario bookkeeping and browser helpers ───────────────────────────────
const failures = [];
const check = createCheck({ failures });
let base;
const card = (page, wi) => dock(page).locator(`[data-pf-card-wi="${wi}"]`);
const h = createHarness({
  viewport: { width: 1280, height: 900 },
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "en", "pi-web:polyforge-event-mode": "verbose" },
  session: SESSION,
  ready: "E2E pf 238 host session",
  async setup(page) {
    const progress = { status: 200, body: progressBody(), hits: [] };
    page.__progress = progress;
    page.__progressRequests = [];
    await mockPolyforge(page, {
      status,
      cards: () => ({ pfPresent: true, cards }),
      watch: (url) => (url.searchParams.get("wi") === DONE.wi || url.searchParams.get("wi") === DONE.slug ? watchDONE() : watchUI()),
      progress: (url, route) => {
        progress.hits.push(Date.now());
        if (progress.status !== 200) return reply(route, { error: "E2E stub failure" }, progress.status);
        return progress.body;
      },
      "progress/tool": () => progress.tool ?? { available: false, error: "E2E stub" },
    });
    page.on("request", (request) => { if (new URL(request.url()).pathname === ROUTE) page.__progressRequests.push(Date.now()); });
  },
  async after(page, { clock = false }) {
    if (clock) { await page.clock.install({ time: new Date() }); await page.clock.pauseAt(new Date(Date.now() + 2000)); }
  },
});
const { withPage } = h;
async function openJob(page, wi) {
  await openDock(page); await card(page, wi).waitFor({ timeout: 30_000 }); await card(page, wi).click();
  await dock(page).getByRole("button", { name: /back to job list|deselect/i }).waitFor({ timeout: 10_000 });
}
const openUI = (page) => openJob(page, UI.wi);
const openDONE = (page) => openJob(page, DONE.wi);
const waitFor = createWaitFor({ every: 150 });
const tick = (page, ms) => (ms > 0 ? page.clock.runFor(ms) : Promise.resolve());
const progressBlock = (page) => dock(page).locator("[data-pf-progress]").first();
const summaryBlock = (page) => dock(page).locator("[data-pf-detail-summary]").first();
// the block mounts before the watch snapshot that carries the
// summary fields lands; under `next start` a read right after mount beats it.
const summaryLanded = (page) => summaryBlock(page).filter({ hasText: "E2E-S238-SUMMARY-ERROR" }).waitFor({ timeout: UI_WAIT });
const scrollRegion = (page) => dock(page).locator("[data-pf-detail-scroll]").first();
const text = async (locator) => (await locator.innerText()).replace(/\s+/g, " ").trim();
async function snapshot(wi) {
  const response = await fetch(`${base}${ROUTE}?wi=${wi}`, { signal: AbortSignal.timeout(30_000) });
  assert.equal(response.status, 200, `the progress read answers 200 for ${wi}`);
  return response.json();
}
const byEvent = (body, kind) => (body.lines ?? []).filter((entry) => entry.event === kind);
const textIndex = (body, needle) => (body.lines ?? []).findIndex((entry) => entry.text.includes(needle));
const nextIndex = (body, marker, from) => {
  const lines = body.lines ?? [];
  for (let index = from + 1; index < lines.length; index += 1) {
    if (marker.startsWith("E2E-") ? lines[index].text.includes(marker) : lines[index].event === marker) return index;
  }
  return -1;
};

try {
  const logFile = join(work, "server.log");
  ({ base } = await h.startServer({ name: "polyforge-238", logFile, env: { PI_WEB_DIST_DIR: process.env.PI_WEB_DIST_DIR,
    PI_CODING_AGENT_DIR: agentDir, POLYFORGE_DATA_DIR: dataRoot, PI_WEB_ALLOWED_ROOT_PREFIXES: work } }));
  await h.launchBrowser(() => chromium.launch());

  await check("S238-01", () => withPage({}, async (page) => {
    page.__progress.body = progressBody(bulkLines(260));
    await openUI(page);
    await summaryBlock(page).waitFor({ timeout: UI_WAIT });
    await dock(page).locator("[data-pf-detail-graph]").first().waitFor({ timeout: UI_WAIT });
    await progressBlock(page).waitFor({ timeout: UI_WAIT });
    await waitFor(async () => (await progressBlock(page).locator("[data-pf-progress-kind]").count()) > 100, "the long progress renders");
    const result = await dock(page).evaluate((scope) => {
      const q = (selector) => scope.querySelector(selector);
      const summary = q("[data-pf-detail-summary]"); const graph = q("[data-pf-detail-graph]");
      const progress = q("[data-pf-progress]"); const scroll = q("[data-pf-detail-scroll]");
      const follows = (a, b) => Boolean(a && b && a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
      return { summary: Boolean(summary), graph: Boolean(graph), progress: Boolean(progress), scroll: Boolean(scroll),
        summaryBeforeGraph: follows(summary, graph), graphBeforeProgress: follows(graph, progress),
        insideScroll: [summary, graph, progress].every((el) => el && scroll && scroll.contains(el)) };
    });
    assert.ok(result.summary && result.graph && result.progress && result.scroll, `all three blocks and the scroll region exist: ${JSON.stringify(result)}`);
    assert.ok(result.summaryBeforeGraph, "the summary block is first");
    assert.ok(result.graphBeforeProgress, "the step graph is second and the progress is last");
    assert.ok(result.insideScroll, "all three blocks are reachable in the detail's one scroll region");
  }));

  await check("S238-02", () => withPage({}, async (page) => {
    await openUI(page);
    const goal = summaryBlock(page).locator("[data-pf-summary-goal]").first();
    const toggle = summaryBlock(page).locator("[data-pf-goal-toggle]").first();
    await goal.waitFor({ timeout: UI_WAIT }); await toggle.waitFor({ timeout: UI_WAIT });
    assert.ok((await text(goal)).includes(GOAL), "the summary shows the full goal text");
    assert.equal(await toggle.getAttribute("aria-expanded"), "false", "the goal starts folded");
    const folded = await goal.evaluate((el) => getComputedStyle(el).webkitLineClamp);
    await toggle.click();
    await waitFor(async () => (await toggle.getAttribute("aria-expanded")) === "true", "the goal expands");
    const expanded = await goal.evaluate((el) => getComputedStyle(el).webkitLineClamp);
    assert.notEqual(expanded, folded, `the toggle removes the three-line clamp (${folded} → ${expanded})`);
    assert.ok(["none", "unset", "", "0"].includes(expanded), `the expanded goal is unclamped: ${expanded}`);
    await toggle.click();
    await waitFor(async () => (await toggle.getAttribute("aria-expanded")) === "false", "the goal collapses again");
  }));

  // (#238 N3): the job's startedAt is fixed at module load, before
  // the cold server start, so the page reads it on the frozen injected clock set
  // to the fixture's own "now" (startedAt + 5 minutes): `/5m/` cannot drift.
  await check("S238-03", () => withPage({ clock: true }, async (page) => {
    await page.clock.setSystemTime(new Date(uiStarted + 300_000));
    await openUI(page);
    await summaryBlock(page).waitFor({ timeout: UI_WAIT });
    const body = await text(summaryBlock(page));
    assert.ok(body.includes("code"), `the summary names the current step: ${body}`);
    assert.ok(body.includes("pi:e2e/model"), `the summary names the model: ${body}`);
    assert.ok(body.includes("#1"), `the summary carries the spawn number: ${body}`);
    assert.match(body, /125s|2m|2 m/, `the step age is in the summary: ${body}`);
    assert.match(body, /\bidle 2s\b/, `the idle age is in the summary: ${body}`);
    assert.match(body, /\d{2}:\d{2}:\d{2}/, `the job start clock is in the summary: ${body}`);
    assert.match(body, /5m/, `the total elapsed time is in the summary: ${body}`);
  }));

  await check("S238-04", () => withPage({}, async (page) => {
    await openUI(page);
    await summaryLanded(page);
    const withSummary = await text(summaryBlock(page));
    for (const expected of ["E2E-S238-SUMMARY", "3s", "E2E-S238-SUMMARY-ERROR"]) {
      assert.ok(withSummary.includes(expected), `the summary narration carries ${expected}: ${withSummary}`);
    }
    assert.match(withSummary, /cap/i, `the reached cap is marked: ${withSummary}`);
    await dock(page).getByRole("button", { name: /back to job list|deselect/i }).click();
    await card(page, DONE.wi).click();
    await dock(page).getByRole("button", { name: /back to job list|deselect/i }).waitFor({ timeout: 10_000 });
    await summaryBlock(page).waitFor({ timeout: UI_WAIT });
    const without = await text(summaryBlock(page));
    assert.doesNotMatch(without, /Summary|summary/i, `no summary heading appears without fields: ${without}`);
  }));

  await check("S238-05", () => withPage({}, async (page) => {
    await openUI(page);
    await summaryLanded(page);
    const body = await text(summaryBlock(page));
    for (const expected of ["Tool calls", "2", "Recent files", "e2e/recent.ts", "Origin", "u_e2e238"]) {
      assert.ok(body.includes(expected), `the summary carries ${expected}: ${body}`);
    }
    const detail = await text(dock(page));
    assert.ok(!detail.includes("E2E-S238-COMMAND-ROW"), `the command row is gone from the detail: ${detail}`);
    assert.doesNotMatch(detail, /\bCommand\b/, `no command label remains: ${detail}`);
  }));

  await check("S238-06", async () => {
    const body = await snapshot(MAIN);
    assert.ok(Array.isArray(body.lines) && body.lines.length > 0, "the read face carries the whole-job line stream");
    const order = [
      "step_started", "E2E-S238-SPAWN1-NARRATION", "step_done", "step_started",
      "E2E-S238-SPAWN2-NARRATION", "step_failed", "step_started", "E2E-S238-SPAWN3-CLAUDE", "step_done",
    ];
    let last = -1;
    for (const marker of order) {
      const index = nextIndex(body, marker, last);
      assert.ok(index > last, `the stream order reaches ${marker} after its predecessor (index ${index}, last ${last})`);
      last = index;
    }
    const starts = byEvent(body, "step_started").map((entry) => entry.text);
    assert.ok(starts.some((entry) => /grill #1/.test(entry)), `the first spawn's start line is present: ${JSON.stringify(starts)}`);
    assert.ok(starts.some((entry) => /spec #2/.test(entry)), `the rerun's start line is present: ${JSON.stringify(starts)}`);
    assert.ok(textIndex(body, "E2E-S238-SPAWN2-NARRATION") >= 0 && textIndex(body, "E2E-S238-SPAWN3-CLAUDE") >= 0, "both the earlier spawn and its rerun have lines");
    const thinking = body.lines.find((entry) => entry.kind === "narration" && entry.variant === "thinking");
    assert.ok(thinking && thinking.text === "E2E-S238-THINKING", `the pi thinking block is a thinking narration line: ${JSON.stringify(thinking)}`);
  });

  await check("S238-07", async () => {
    const body = await snapshot(MAIN);
    assert.ok(textIndex(body, "E2E-S238-SPAWN1-NARRATION") >= 0, "the first step's narration stays in the stream");
    assert.ok(textIndex(body, "bash: curl --token=*** https://e2e.invalid") >= 0, "the first step's tool line stays in the stream");
    assert.ok(byEvent(body, "step_done").some((entry) => /grill/.test(entry.text)), "the first step's end line stays in the stream");
    assert.ok(textIndex(body, "E2E-S238-SPAWN3-CLAUDE") > textIndex(body, "E2E-S238-SPAWN1-NARRATION"), "the later spawn joins after the earlier step's lines");
  });

  await check("S238-08", () => withPage({}, async (page) => {
    page.__progress.body = progressBody([line("done-1", "narration", "E2E-S238-TERMINAL"), ...BASE_LINES]);
    await openDONE(page);
    await progressBlock(page).waitFor({ timeout: UI_WAIT });
    await waitFor(async () => (await progressBlock(page).locator("[data-pf-progress-kind]").count()) > 0, "the finished job's progress renders");
    const body = await text(progressBlock(page));
    assert.ok(body.includes("E2E-S238-TERMINAL"), `a terminated job shows its lines: ${body}`);
    assert.doesNotMatch(body, /not running|Loading/i, `the block is not replaced by a not-running or loading message: ${body}`);
  }));

  await check("S238-09", () => withPage({}, async (page) => {
    const body = await snapshot(MAIN);
    assert.ok(Array.isArray(body.lines), "the read face answers a line stream");
    assert.ok(body.lines.length > 0 && body.lines.length < 2000, `the under-bound stream is short: ${body.lines.length}`);
    assert.equal(body.omitted, 0, "an under-bound stream omits nothing");
    page.__progress.body = progressBody(BASE_LINES, 0);
    await openUI(page);
    await progressBlock(page).waitFor({ timeout: UI_WAIT });
    await waitFor(async () => (await progressBlock(page).locator("[data-pf-progress-kind]").count()) > 0, "the under-bound progress renders");
    assert.equal(await progressBlock(page).locator("[data-pf-progress-omitted]").count(), 0, "no omitted marker appears under the bound");
  }));

  await check("S238-10", () => withPage({}, async (page) => {
    const body = await snapshot(MANY);
    assert.ok(Array.isArray(body.lines), "the read face answers a line stream");
    assert.equal(body.lines.length, 2005, "the read face returns every line ( removed the 2000-line window)");
    assert.equal(body.omitted, 0, "a complete stream omits nothing");
    assert.ok(body.lines.some((entry) => entry.text === "E2E-S238-MANY-2005"), "the newest line is kept");
    assert.ok(body.lines.some((entry) => entry.text === "E2E-S238-MANY-0001"), "the oldest line is kept too");
    page.__progress.body = progressBody(bulkLines(3), 0);
    await openUI(page);
    await progressBlock(page).waitFor({ timeout: UI_WAIT });
    assert.equal(await progressBlock(page).locator("[data-pf-progress-omitted]").count(), 0, "no omitted marker for a complete stream");
  }));

  await check("S238-11", () => withPage({}, async (page) => {
    page.__progress.body = progressBody(bulkLines(220));
    await openUI(page);
    await progressBlock(page).waitFor({ timeout: UI_WAIT });
    await scrollRegion(page).waitFor({ timeout: UI_WAIT });
    await waitFor(async () => (await progressBlock(page).locator("[data-pf-progress-kind]").count()) > 100, "the long progress renders");
    // owner-intended change: narration now renders through the transcript's
    // Markdown body, which clips its horizontal overflow (`overflow-x: hidden`,
    // which CSS reports as `overflow-y: auto`). The guarantee is unchanged: no
    // element in the progress block is a vertical scroller — any element whose
    // overflow-y computes to auto/scroll has no height bound (`max-height: none`)
    // and no vertical overflow, and the progress block itself never does.
    const result = await dock(page).evaluate((scope) => {
      const progress = scope.querySelector("[data-pf-progress]");
      const nested = [progress, ...(progress?.querySelectorAll("*") ?? [])].filter((el) => {
        const style = getComputedStyle(el);
        if (!/(auto|scroll)/.test(style.overflowY)) return false;
        return el === progress || style.maxHeight !== "none" || el.scrollHeight > el.clientHeight + 1;
      });
      return { inside: Boolean(progress && scope.querySelector("[data-pf-detail-scroll]")?.contains(progress)), nested: nested.length,
        virtual: Boolean(progress?.querySelector("[data-virtualised], [data-virtualized]")) };
    });
    assert.ok(result.inside, "the progress block sits inside the detail's one scroll region");
    assert.equal(result.nested, 0, "the progress block introduces no scroller of its own");
    assert.equal(result.virtual, false, "the progress block is not virtualised");
  }));

  await check("S238-12", () => withPage({ clock: true }, async (page) => {
    page.__progress.body = progressBody(bulkLines(260));
    await openUI(page);
    await progressBlock(page).waitFor({ timeout: UI_WAIT });
    const scroll = scrollRegion(page);
    await scroll.waitFor({ timeout: UI_WAIT });
    const latest = dock(page).locator("[data-pf-detail-latest]");
    // code review: the fake clock fakes requestAnimationFrame, and the
    // chunked renderer appends the tail one chunk per frame. The snapshot read is
    // real time, so a fixed tick can run before the stubbed fetch lands and its
    // frame would then never fire. Advance the clock inside the wait: every
    // iteration lets whatever frame is pending run.
    await waitFor(async () => {
      await tick(page, 50);
      return (await progressBlock(page).locator("[data-pf-progress-kind]").count()) > 200;
    }, "the long progress renders");
    // (owner intentional change, reversing #238's open-at-top):
    // every selection opens the whole detail at its bottom.
    await waitFor(async () => {
      await tick(page, 50);
      return scroll.evaluate((el) => el.scrollTop + el.clientHeight >= el.scrollHeight - 8);
    }, "opening the detail ends at its bottom");
    assert.equal(await latest.count(), 0, "the latest control is hidden at the bottom");
    await scroll.evaluate((el) => { el.scrollTop = Math.min(120, el.scrollHeight - el.clientHeight); el.dispatchEvent(new Event("scroll")); });
    const middle = await scroll.evaluate((el) => el.scrollTop);
    await waitFor(async () => (await latest.count()) === 1, "the latest control appears off the bottom");
    page.__progress.body = progressBody([...bulkLines(260), line("new-1", "narration", "E2E-S238-NEW-1")]);
    await tick(page, 2000);
    await waitFor(async () => {
      await tick(page, 50);
      return (await text(progressBlock(page))).includes("E2E-S238-NEW-1");
    }, "the first new line arrives");
    const held = await scroll.evaluate((el) => el.scrollTop);
    assert.ok(Math.abs(held - middle) <= 1, `a non-pinned reader does not move (${middle} → ${held})`);
    assert.equal(await latest.count(), 1, "the latest control stays while the reader is off the bottom");
    await latest.locator("button").evaluate((el) => el.click());
    await waitFor(async () => {
      await tick(page, 50);
      return scroll.evaluate((el) => el.scrollTop + el.clientHeight >= el.scrollHeight - 8);
    }, "one click jumps to the bottom");
    assert.equal(await latest.count(), 0, "the latest control is hidden again once at the bottom");
    page.__progress.body = progressBody([...bulkLines(260), line("new-1", "narration", "E2E-S238-NEW-1"), line("new-2", "narration", "E2E-S238-NEW-2")]);
    await tick(page, 2000);
    await waitFor(async () => {
      await tick(page, 50);
      return (await text(progressBlock(page))).includes("E2E-S238-NEW-2");
    }, "the second new line arrives");
    const pinned = await scroll.evaluate((el) => ({ top: el.scrollTop, bottom: el.scrollTop + el.clientHeight, height: el.scrollHeight }));
    assert.ok(pinned.bottom >= pinned.height - 8, `a reader restored to following stays at the bottom (${JSON.stringify(pinned)})`);
    assert.equal(await latest.count(), 0, "following leaves no latest control");
    // (replacing #247's N4 seed): a short stream opens at its
    // bottom too, and when it grows it is followed before any scroll.
    page.__progress.body = progressBody(bulkLines(2));
    await dock(page).getByRole("button", { name: /back to job list|deselect/i }).click();
    await card(page, UI.wi).click();
    await waitFor(async () => (await progressBlock(page).locator("[data-pf-progress-kind]").count()) === 2, "the short progress renders");
    const fits = await scroll.evaluate((el) => ({ top: el.scrollTop, client: el.clientHeight, height: el.scrollHeight }));
    assert.ok(fits.height <= fits.client + 8, `the short detail fits its region (${JSON.stringify(fits)})`);
    page.__progress.body = progressBody(bulkLines(260));
    await tick(page, 2000); await delay(200);
    // the chunked renderer appends the tail one chunk per animation
    // frame, and the fake clock gates requestAnimationFrame. Advance the clock
    // inside the wait, as the earlier waits here do, so the chunk after the first
    // 200 lines can run; the assertion itself is unchanged.
    await waitFor(async () => {
      await tick(page, 50);
      return (await progressBlock(page).locator("[data-pf-progress-kind]").count()) > 200;
    }, "the grown progress renders");
    const followed = await scroll.evaluate((el) => ({ top: el.scrollTop, bottom: el.scrollTop + el.clientHeight, height: el.scrollHeight }));
    assert.ok(followed.top > 0 && followed.bottom >= followed.height - 8, `a fitting detail that grows is followed to its bottom (${JSON.stringify(followed)})`);
  }));

  await check("S238-25", () => withPage({}, async (page) => {
    page.__progress.body = progressBody([
      eventLine("d1", "00:00:01 ▶ grill #1 · pi:e2e/model", "step_started", "accent"),
      line("d2", "narration", "E2E-S238-FIRST"),
      eventLine("d3", "00:00:20 ▶ spec #1 · pi:e2e/model", "step_started", "accent"),
      line("d4", "narration", "E2E-S238-SECOND"),
    ]);
    await openUI(page);
    const blocks = progressBlock(page).locator("[data-pf-progress-block]");
    await waitFor(async () => (await blocks.count()) === 2, "two spawn blocks render");
    const expanded = () => blocks.locator("[data-pf-progress-block-toggle]").evaluateAll((els) => els.map((el) => el.getAttribute("aria-expanded")));
    assert.deepEqual(await expanded(), ["false", "true"], "only the newest of the two starts expanded");
    page.__progress.body = progressBody([
      eventLine("d1", "00:00:01 ▶ grill #1 · pi:e2e/model", "step_started", "accent"),
      line("d2", "narration", "E2E-S238-FIRST"),
      eventLine("d3", "00:00:20 ▶ spec #1 · pi:e2e/model", "step_started", "accent"),
      line("d4", "narration", "E2E-S238-SECOND"),
      eventLine("d5", "00:00:40 ▶ spec #2 · pi:e2e/model", "step_started", "accent"),
      line("d6", "narration", "E2E-S238-THIRD"),
    ]);
    await waitFor(async () => (await text(progressBlock(page))).includes("E2E-S238-THIRD"), "the new spawn's block arrives");
    assert.equal(await blocks.count(), 3, "a third spawn is its own block");
    assert.deepEqual(await expanded(), ["false", "false", "true"], "the default expansion moves to the new block while the untouched prior one folds");
    assert.match(await blocks.nth(2).locator("[data-pf-progress-block-toggle]").innerText(), /spec #2 ▶ · 2 lines/, "the new running block is marked ▶");
  }));

  await check("S238-23", () => withPage({}, async (page) => {
    page.__progress.body = progressBody(blockLines());
    await openUI(page);
    await progressBlock(page).waitFor({ timeout: UI_WAIT });
    const blocks = progressBlock(page).locator("[data-pf-progress-block]");
    await waitFor(async () => (await blocks.count()) === 3, "three spawn blocks render");
    const headers = await blocks.locator("[data-pf-progress-block-toggle]").evaluateAll((els) => els.map((el) => el.textContent));
    assert.match(headers[0], /▸ grill #1 ✓ · 3 lines/, `the first header carries step, spawn, status and count (${headers[0]})`);
    assert.match(headers[1], /▸ spec #1 ✕ · 3 lines/, `the failed block's header (${headers[1]})`);
    assert.match(headers[2], /▾ spec #2 \? · \d+ lines/, `the newest needs-input block's header (${headers[2]})`);
    assert.deepEqual(await blocks.locator("[data-pf-progress-block-toggle]").evaluateAll((els) => els.map((el) => el.getAttribute("aria-expanded"))), ["false", "false", "true"], "only the newest block starts expanded");
    const openText = await text(progressBlock(page));
    assert.ok(openText.includes("E2E-S238-LEADING"), "lines before the first start stay unfolded");
    assert.ok(openText.includes("E2E-S238-BLOCK-3") && openText.includes("E2E-S238-BLOCK-3-TAIL-40"), "the newest block's body is visible");
    assert.ok(!openText.includes("E2E-S238-BLOCK-1") && !openText.includes("E2E-S238-BLOCK-2"), "older block bodies are folded");
    // The no-truncation identity: leading lines plus every header count are
    // exactly the snapshot's line count, and expanding every block shows every
    // line's full text.
    const counts = await blocks.evaluateAll((els) => els.map((el) => Number(el.getAttribute("data-pf-progress-block-count"))));
    const total = blockLines().length;
    assert.equal(1 + counts.reduce((sum, count) => sum + count, 0), total, `leading + header counts = ${total} (${counts})`);
    for (const header of await blocks.locator("[data-pf-progress-block-toggle]").all()) {
      if ((await header.getAttribute("aria-expanded")) === "false") await header.evaluate((el) => el.click());
    }
    // owner-intended change: each head absorbs its block's step_started and
    // terminal (step_done / step_failed / step_dead) line — 3 starts + 2 terminals
    // here — so the expanded rows plus the absorbed lines are every line, and each
    // absorbed line's full text is on its head's title.
    const absorbed = 3 + 2;
    await waitFor(async () => (await progressBlock(page).locator("[data-pf-progress-kind]").count()) === total - absorbed, "expanding every block shows every line");
    const titles = (await blocks.locator("[data-pf-progress-block-toggle]").evaluateAll((els) => els.map((el) => el.getAttribute("title")))).join("\n");
    for (const absorbedLine of blockLines().filter((entry) => ["step_started", "step_done", "step_failed"].includes(entry.event))) {
      assert.ok(titles.includes(absorbedLine.text) && titles.includes(absorbedLine.event), `the head title carries ${absorbedLine.event}: ${absorbedLine.text}`);
    }
    assert.equal(await progressBlock(page).locator('[data-pf-progress-kind="event"]').count(), 1, "only the non-absorbed needs_input line stays a marker row");
    const expandedText = await text(progressBlock(page));
    for (const marker of ["E2E-S238-LEADING", "E2E-S238-BLOCK-1", "E2E-S238-BLOCK-2", "E2E-S238-BLOCK-3", "E2E-S238-BLOCK-3-TAIL-40"]) {
      assert.ok(expandedText.includes(marker), `the fully expanded stream keeps ${marker}`);
    }
    // An expand/collapse round trip returns the older block to folded.
    await blocks.nth(0).locator("[data-pf-progress-block-toggle]").evaluate((el) => el.click());
    await waitFor(async () => (await blocks.nth(0).locator("[data-pf-progress-block-toggle]").getAttribute("aria-expanded")) === "false", "the first block folds again");
    assert.ok(!(await text(progressBlock(page))).includes("E2E-S238-BLOCK-1"), "the folded body is hidden again");
  }));

  await check("S238-24", () => withPage({}, async (page) => {
    page.__progress.body = progressBody(blockLines());
    await openUI(page);
    const scroll = scrollRegion(page);
    await scroll.waitFor({ timeout: UI_WAIT });
    const blocks = progressBlock(page).locator("[data-pf-progress-block]");
    await waitFor(async () => (await blocks.count()) === 3, "three spawn blocks render");
    const latest = dock(page).locator("[data-pf-detail-latest]");
    const atBottom = () => scroll.evaluate((el) => el.scrollTop + el.clientHeight >= el.scrollHeight - 8);
    await waitFor(atBottom, "the detail opens at its bottom");
    assert.equal(await latest.count(), 0, "the latest control is hidden at the bottom");
    // Collapsing while pinned keeps the reader pinned and at the bottom.
    await blocks.nth(2).locator("[data-pf-progress-block-toggle]").evaluate((el) => el.click());
    await waitFor(async () => (await blocks.nth(2).locator("[data-pf-progress-block-toggle]").getAttribute("aria-expanded")) === "false", "the newest block folds");
    await waitFor(atBottom, "a collapse while pinned stays at the bottom");
    assert.equal(await latest.count(), 0, "a collapse while pinned leaves the latest control hidden");
    // Expanding again grows the content; scrollTop stays, so the reader leaves
    // the bottom, unpins and the control appears (no jump).
    await blocks.nth(2).locator("[data-pf-progress-block-toggle]").evaluate((el) => el.click());
    await waitFor(async () => (await latest.count()) === 1, "expanding at the bottom unpins and shows the control");
    await latest.locator("button").evaluate((el) => el.click());
    await waitFor(atBottom, "the control returns the reader to the bottom");
    // Expanding an OLDER block keeps scrollTop unchanged and unpins.
    const before = await scroll.evaluate((el) => el.scrollTop);
    await blocks.nth(0).locator("[data-pf-progress-block-toggle]").evaluate((el) => el.click());
    await waitFor(async () => (await blocks.nth(0).locator("[data-pf-progress-block-toggle]").getAttribute("aria-expanded")) === "true", "the old block expands");
    const after = await scroll.evaluate((el) => el.scrollTop);
    assert.ok(Math.abs(after - before) <= 1, `expanding an older block does not move the region (${before} → ${after})`);
    await waitFor(async () => (await latest.count()) === 1, "expanding an older block unpins and shows the control");
    // A later poll does not pull an unpinned reader back to the bottom.
    const held = await scroll.evaluate((el) => el.scrollTop);
    page.__progress.body = progressBody([...blockLines(), line("late", "narration", "E2E-S238-LATE")]);
    await waitFor(async () => (await text(progressBlock(page))).includes("E2E-S238-LATE"), "the later line arrives");
    const afterPoll = await scroll.evaluate((el) => el.scrollTop);
    assert.ok(Math.abs(afterPoll - held) <= 1, `a poll does not move an unpinned reader (${held} → ${afterPoll})`);
    assert.equal(await latest.count(), 1, "the latest control stays after the poll");
  }));

  // review s10#1: a thinking narration renders dim as well as
  // italic — the Markdown body's own `color: var(--text)` must not win over the
  // dim treatment (#251). Computed colours, not source.
  await check("S238-26", () => withPage({}, async (page) => {
    page.__progress.body = progressBody([
      line("c1", "narration", "E2E-S238-PLAIN-PARA"),
      line("c2", "narration", "E2E-S238-THINK-PARA **E2E-S238-THINK-STRONG**", { variant: "thinking" }),
    ]);
    await openUI(page);
    await waitFor(async () => (await text(progressBlock(page))).includes("E2E-S238-THINK-PARA"), "both narrations render");
    const colours = await progressBlock(page).evaluate((block) => {
      const probe = document.createElement("span");
      probe.style.color = "var(--text-dim)";
      block.appendChild(probe);
      const dim = getComputedStyle(probe).color;
      probe.remove();
      const plain = block.querySelector('[data-pf-progress-kind="narration"]:not([data-pf-progress-variant]) p');
      const thinking = block.querySelector('[data-pf-progress-variant="thinking"] p');
      return { dim, plain: plain && getComputedStyle(plain).color, thinking: thinking && getComputedStyle(thinking).color,
        style: thinking && getComputedStyle(thinking).fontStyle };
    });
    assert.ok(colours.plain && colours.thinking, `both narrations render Markdown paragraphs: ${JSON.stringify(colours)}`);
    assert.equal(colours.thinking, colours.dim, `the thinking paragraph is the dim colour: ${JSON.stringify(colours)}`);
    assert.notEqual(colours.plain, colours.dim, `plain narration keeps the text colour: ${JSON.stringify(colours)}`);
    assert.equal(colours.style, "italic", "the thinking paragraph stays italic");
  }));

  // review s10#2: a tool card's expand/collapse and its detail
  // arriving follow the #261 fold rules. Expanding the last card while pinned
  // grows the content below an unchanged scrollTop: the reader unpins, sees
  // ↓ Latest and a later poll leaves the view alone; collapsing at the bottom
  // stays pinned and keeps following.
  await check("S238-27", () => withPage({}, async (page) => {
    const toolLine = line("t-follow", "tool", "bash: echo E2E-S238-CARD", { tool: { name: "bash", argument: "echo E2E-S238-CARD", status: "ok", durationMs: 1000 } });
    const base = [...bulkLines(80), toolLine];
    page.__progress.body = progressBody(base);
    page.__progress.tool = { available: true, tool: { name: "bash", argument: "echo E2E-S238-CARD", status: "ok", durationMs: 1000, isError: false,
      input: "echo E2E-S238-CARD", output: Array.from({ length: 80 }, (_, i) => `E2E-S238-CARD-OUT-${String(i + 1).padStart(2, "0")}`).join("\n") } };
    await openUI(page);
    const scroll = scrollRegion(page);
    await scroll.waitFor({ timeout: UI_WAIT });
    const latest = dock(page).locator("[data-pf-detail-latest]");
    const atBottom = () => scroll.evaluate((el) => el.scrollTop + el.clientHeight >= el.scrollHeight - 8);
    const header = progressBlock(page).locator('[data-pf-progress-kind="tool"] button[aria-expanded]').first();
    await header.waitFor({ timeout: UI_WAIT });
    await waitFor(atBottom, "the detail opens at its bottom");
    assert.equal(await latest.count(), 0, "the latest control is hidden at the bottom");
    const before = await scroll.evaluate((el) => el.scrollTop);
    await header.evaluate((el) => el.click());
    await waitFor(async () => (await text(progressBlock(page))).includes("E2E-S238-CARD-OUT-80"), "the expanded card shows its detail");
    await waitFor(async () => (await latest.count()) === 1, "expanding the card at the bottom unpins and shows the control");
    const held = await scroll.evaluate((el) => el.scrollTop);
    assert.ok(Math.abs(held - before) <= 1, `the expansion does not move the region (${before} → ${held})`);
    page.__progress.body = progressBody([...base, line("card-late", "narration", "E2E-S238-CARD-LATE")]);
    await waitFor(async () => (await text(progressBlock(page))).includes("E2E-S238-CARD-LATE"), "a later poll's line arrives");
    const afterPoll = await scroll.evaluate((el) => el.scrollTop);
    assert.ok(Math.abs(afterPoll - held) <= 1, `a poll does not pull the reader off the opened card (${held} → ${afterPoll})`);
    assert.equal(await latest.count(), 1, "the latest control stays after the poll");
    await latest.locator("button").evaluate((el) => el.click());
    await waitFor(atBottom, "the control returns the reader to the bottom");
    await header.evaluate((el) => el.click());
    await waitFor(async () => (await header.getAttribute("aria-expanded")) === "false", "the card collapses");
    await waitFor(atBottom, "a collapse at the bottom stays at the bottom");
    assert.equal(await latest.count(), 0, "a collapse at the bottom leaves the control hidden");
    page.__progress.body = progressBody([...base, line("card-late", "narration", "E2E-S238-CARD-LATE"), line("card-late-2", "narration", "E2E-S238-CARD-LATE-2")]);
    await waitFor(async () => (await text(progressBlock(page))).includes("E2E-S238-CARD-LATE-2"), "the next poll's line arrives");
    await waitFor(atBottom, "a reader still pinned after the collapse keeps following");
    assert.equal(await latest.count(), 0, "following leaves no latest control");
  }));

  // review s10#3: narration over the Markdown limit (100 000
  // chars) renders inline and in full as plain wrapped text — no click gate,
  // no height-bounded box, no nested scroller in the progress area.
  await check("S238-28", () => withPage({}, async (page) => {
    const huge = `E2E-S238-HUGE-HEAD\n${"word ".repeat(21_000)}\nE2E-S238-HUGE-TAIL`;
    assert.ok(huge.length > 100_000, "the fixture is over the Markdown limit");
    page.__progress.body = progressBody([line("h1", "narration", huge), line("h2", "narration", "E2E-S238-AFTER-HUGE")]);
    await openUI(page);
    await waitFor(async () => (await text(progressBlock(page))).includes("E2E-S238-AFTER-HUGE"), "the stream renders");
    const row = progressBlock(page).locator('[data-pf-progress-kind="narration"]').first();
    const result = await row.evaluate((el) => ({
      text: el.innerText,
      buttons: el.querySelectorAll("button").length,
      boxed: [el, ...el.querySelectorAll("*")].filter((node) => {
        const style = getComputedStyle(node);
        return style.maxHeight !== "none" || /(auto|scroll)/.test(style.overflowY) || node.scrollHeight > node.clientHeight + 1;
      }).length,
      wrap: [...el.querySelectorAll("*")].some((node) => getComputedStyle(node).whiteSpace === "pre-wrap"),
    }));
    assert.equal(result.buttons, 0, "no click-to-reveal control");
    assert.equal(result.boxed, 0, "no height-bounded or scrolling box");
    assert.ok(result.wrap, "the text wraps (pre-wrap)");
    assert.ok(result.text.includes("E2E-S238-HUGE-HEAD") && result.text.includes("E2E-S238-HUGE-TAIL"), "the whole entry is visible inline");
    assert.ok(result.text.length >= huge.length - 10, `nothing is cut (${result.text.length} of ${huge.length})`);
  }));

  await check("S238-13", async () => {
    const body = await snapshot(MAIN);
    const started = byEvent(body, "step_started");
    const grill = started.find((entry) => entry.text.includes("grill"));
    assert.ok(grill, `the step_started line is present: ${JSON.stringify(started)}`);
    assert.match(grill.text, /^\d{2}:\d{2}:\d{2} ▶ grill #1 · pi:e2e\/model$/, `the ruled start format (${grill.text})`);
    assert.equal(grill.tone, "accent", "the start line is accent-coloured");
  });

  await check("S238-14", async () => {
    const body = await snapshot(MAIN);
    const done = byEvent(body, "step_done");
    const grill = done.find((entry) => entry.text.includes("grill"));
    assert.ok(grill, `the step_done line is present: ${JSON.stringify(done)}`);
    assert.match(grill.text, /^\d{2}:\d{2}:\d{2} ■ grill \+15s$/, `the ruled end format and duration (${grill.text})`);
    assert.equal(grill.tone, "dim", "the end line is dim");
  });

  await check("S238-15", async () => {
    const body = await snapshot(MAIN);
    const failed = byEvent(body, "step_failed")[0];
    const dead = byEvent(body, "step_dead")[0];
    assert.ok(failed, "the step_failed line is present");
    assert.match(failed.text, /^\d{2}:\d{2}:\d{2} ✕ spec · gate-failed$/, `the failure line names the step and cause (${failed.text})`);
    assert.equal(failed.tone, "red", "the failure line is red");
    assert.ok(dead, "the step_dead line is present");
    assert.match(dead.text, /^\d{2}:\d{2}:\d{2} ✕ plan · timeout-after-3-retries$/, `the death line carries its cause (${dead.text})`);
    assert.equal(dead.tone, "red", "the death line is red");
  });

  await check("S238-16", async () => {
    const body = await snapshot(MAIN);
    const recovery = byEvent(body, "needs_recovery")[0];
    const push = byEvent(body, "push_failed")[0];
    assert.ok(recovery && push, "both fault events produce a line");
    assert.match(recovery.text, /⚠ recovery reason · recovery detail/, `the recovery line carries reason and detail (${recovery.text})`);
    assert.equal(recovery.tone, "red", "the recovery line is red");
    assert.match(push.text, /⚠ push error token=\*\*\*/, `the push line carries the masked error (${push.text})`);
    assert.equal(push.tone, "red", "the push line is red");
    assert.ok(!JSON.stringify(body).includes("SECRET-S238-TOKEN"), "the raw credential never leaves the read face");
  });

  await check("S238-17", async () => {
    const body = await snapshot(MAIN);
    assert.ok(Array.isArray(body.lines) && body.lines.length > 0, "the whole-job stream is present with its retained lines");
    for (const kind of ["candidate_switched", "envelope_rejected", "authored_tests_rebaselined"]) {
      assert.equal(byEvent(body, kind).length, 0, `${kind} produces no line`);
    }
    const joined = (body.lines ?? []).map((entry) => entry.text).join("\n");
    assert.doesNotMatch(joined, /E2E-S238-ENVELOPE|E2E-S238-REBASELINED/, "no hidden-kind body field leaks into a line");
  });

  await check("S238-18", async () => {
    const body = await snapshot(MAIN);
    const needsInput = byEvent(body, "needs_input")[0];
    assert.ok(needsInput, "needs_input produces one line");
    assert.equal(needsInput.tone, "amber", "needs_input is amber");
    const rewind = byEvent(body, "rewind")[0];
    assert.ok(rewind, "rewind produces one line");
    assert.match(rewind.text, /spec-review → spec/, `the rewind line names gate and target (${rewind.text})`);
    for (const kind of ["answered", "gate_verdict", "job_stopped"]) {
      const entry = byEvent(body, kind)[0];
      assert.ok(entry, `${kind} produces one line`);
      assert.equal(entry.tone ?? "neutral", "neutral", `${kind} stays neutral`);
    }
    const terminalFailed = byEvent(body, "terminal")[0];
    assert.ok(terminalFailed, "terminal produces one line");
    assert.equal(terminalFailed.tone, "red", "a failed terminal line is red");
    const completed = byEvent(await snapshot(TERMINAL_DONE), "terminal")[0];
    assert.ok(completed, "a completed job's terminal line is present");
    assert.equal(completed.tone, "green", "a completed terminal line is green");
    const cancelled = byEvent(await snapshot(TERMINAL_CANCEL), "terminal")[0];
    assert.ok(cancelled, "a cancelled job's terminal line is present");
    assert.equal(cancelled.tone ?? "neutral", "neutral", "a cancelled terminal line is neutral");
  });

  await check("S238-19", async () => {
    const body = await snapshot(MAIN);
    assert.ok((body.lines ?? []).length > 0, "the stream itself is non-empty");
    for (const kind of ["published", "transcript_missing", "exec_init", "job_started"]) {
      assert.equal(byEvent(body, kind).length, 0, `${kind} produces no line`);
    }
    const joined = (body.lines ?? []).map((entry) => entry.text).join("\n");
    assert.doesNotMatch(joined, /sync ×|sync x/, "no collapsed sync noise row is produced");
  });

  await check("S238-20", () => withPage({}, async (page) => {
    await openUI(page);
    await progressBlock(page).waitFor({ timeout: UI_WAIT });
    assert.equal(await dock(page).getByRole("button", { name: /^(simple|verbose)$/i }).count(), 0, "no simple/verbose event-mode control exists");
    assert.doesNotMatch(await text(dock(page)), /\bCommand\b/, "no command row label exists");
    assert.doesNotMatch(await text(dock(page)), /sync ×|sync x/i, "no collapsed sync noise row exists");
    // owner-intended change: the block's step_started / step_done lines are
    // absorbed into its group head — the head (inside the progress block) names
    // the step and carries both lines' text and raw kind on its title.
    const head = progressBlock(page).locator("[data-pf-progress-block-toggle]").first();
    await waitFor(async () => (await head.count()) === 1 && (await head.getAttribute("title") ?? "").includes("▶ code"), "events render only inside the progress block");
    const progressText = await text(progressBlock(page));
    assert.ok(progressText.includes("code #1"), `the event's step lives in the progress block's head: ${progressText}`);
    const headTitle = await head.getAttribute("title");
    assert.ok(headTitle.includes("step_started") && headTitle.includes("▶ code #1") && headTitle.includes("step_done") && headTitle.includes("■ code +15s"), `the absorbed lifecycle lines are on the head: ${headTitle}`);
    const storage = await page.evaluate(() => localStorage.getItem("pi-web:polyforge-event-mode"));
    assert.equal(storage, "verbose", "the retired browser preference is never read, rewritten or cleared");
  }));

  await check("S238-21", () => withPage({}, async (page) => {
    page.__progress.status = 500;
    await openUI(page);
    await progressBlock(page).waitFor({ timeout: UI_WAIT });
    await waitFor(async () => /Progress unavailable/i.test(await text(progressBlock(page))), "the unavailable state after a failed first read", 6_000);
    assert.doesNotMatch(await text(progressBlock(page)), /Loading/i, "a failed first read never stays on Loading");
    page.__progress.status = 200;
    page.__progress.body = progressBody([line("recovered", "narration", "E2E-S238-RECOVERED")]);
    await waitFor(async () => (await text(progressBlock(page))).includes("E2E-S238-RECOVERED"), "a later good read replaces the unavailable state", 6_000);
    assert.doesNotMatch(await text(progressBlock(page)), /Progress unavailable/i, "the unavailable state is gone after recovery");
  }));

  await check("S238-22", async () => {
    const raw = async (wi) => {
      const response = await fetch(`${base}${ROUTE}?wi=${encodeURIComponent(wi)}`, { signal: AbortSignal.timeout(30_000) });
      return { status: response.status, text: await response.text() };
    };
    renameSync(runtimeState, `${runtimeState}.bak`);
    try {
      const missing = await raw(MAIN);
      assert.equal(missing.status, 200, "a missing job index degrades instead of refusing");
      const body = JSON.parse(missing.text);
      assert.equal(body.available, false, "the missing index answers available:false");
      assert.deepEqual(body.lines, [], "the degraded body carries an empty line list");
      assert.equal(body.omitted, 0, "the degraded body omits nothing");
      assert.doesNotMatch(missing.text, /\/etc\/passwd|capsules/, "no filesystem path leaves the degraded response");
    } finally {
      renameSync(`${runtimeState}.bak`, runtimeState);
    }
    const malformed = await raw("../../etc/passwd");
    assert.equal(malformed.status, 400, "a malformed id stays 400 before any file read");
    const unknown = await raw("wi_S238Unknown01");
    assert.equal(unknown.status, 400, "a work item with neither an index record nor an event log stays 400");
    // the daemon drops a terminated job from its index (dropJob);
    // the route then serves the job's event log, final terminal line included.
    writeFileSync(runtimeState, JSON.stringify({ version: 1, jobs: [MAIN, MANY, TERMINAL_CANCEL].map((wi) => ({ work_item_id: wi, capsule_dir: capsule(wi) })) }));
    try {
      const dropped = await raw(TERMINAL_DONE);
      assert.equal(dropped.status, 200, "a job dropped from the index is served from its event log");
      const body = JSON.parse(dropped.text);
      assert.equal(body.available, true, "the dropped job's event log is available");
      assert.equal(body.omitted, 0);
      assert.deepEqual(body.lines.map((line) => [line.kind, line.event, line.tone]), [["event", "terminal", "green"]], "the dropped job keeps its final terminal line");
      assert.doesNotMatch(dropped.text, /capsules|pi-web-pf-238-e2e-/, "no filesystem path leaves the event-log response");
      assert.equal((await raw("wi_S238Unknown01")).status, 400, "a never-known wi stays 400 after the drop");
    } finally {
      writeRuntimeState();
    }
  });
} catch (error) {
  failures.push("suite");
  console.log(`FAIL suite: ${String(error?.message ?? error).split("\n")[0]}`);
} finally {
  await h.close();
  if (failures.length) console.log(`\nFAILED ${failures.join(", ")} — ${failures.length} scenario(s) red`);
  else console.log("\nPASS all authored scenarios");
  console.log(`server log: ${join(work, "server.log")}`);
}
if (failures.length) process.exitCode = 1;
