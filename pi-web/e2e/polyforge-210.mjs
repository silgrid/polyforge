// carries: hooks/usePolyforgeProgress*, app/api/polyforge/progress/route.ts
// carries: app/api/polyforge/polyforge-progress-routes*
// — Polyforge job detail re-layout: title row, DAG timings, live progress.
//
// (owner-intentional change, by ruling — not a regression): this
// suite now asserts the #238 destination where it reverses parts of #210 and
// pi#112. The detail is three blocks — summary (title row, folded goal + toggle,
// facts, summarize narration, tool calls / recent files / origin) → DAG →
// execution progress last, no fourth block; the command row and the
// simple/verbose event-mode toggle are gone; the progress is the whole job's
// stream (`{available, lines:[{id, kind, text, tone?, event?}], omitted}`, every
// spawn, every line) instead of the current step's newest 50
// entries; a missing job index answers 200 `available:false` (N6). Every #210
// guarantee that #238 keeps (title row, DAG timing line, 2 s cadence, last-good
// read, whitelist, masking, 400/403/405, no path, no inner scroller) is still
// asserted, none weakened; the over-bound omitted marker is the authored
// e2e/polyforge-238.mjs suite's (S238-10). Absence checks settle on a probe
// request the page issues after the fact (N8), never a fixed sleep.
//
// Originally authored PRE-CODE against artifacts/spec.md (Acceptance criteria + `####
// Scenario` blocks). The suite drives the panel through the existing Jobs tab
// (`[data-pf-card-wi]`, the deselect control) and the new read face at
// `GET /api/polyforge/progress?wi=`; the exact route path and the four
// `data-pf-*` test hooks are the deterministic interfaces `plan.md` pinned
// (the spec leaves them to plan). Existing /api/polyforge/{status,cards,watch}
// reads are mocked with their documented wire shapes for the UI scenarios;
// the new route is exercised against a throwaway daemon data root so its
// whitelist, id/path containment and step tail are real.
//
// At the pre-implementation tree every kept scenario is assertion-level RED:
// no title row, no DAG timing line, no progress block, and the new route
// answers 404. Every scenario prints `PASS S210-<n>` on success (its binding
// marker).
//
//   node e2e/polyforge-210.mjs
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
process.env.PI_WEB_DIST_DIR ||= ".next/e2e/polyforge-210";
if (process.env.PI_WEB_DIST_DIR.startsWith(".next/e2e/")) rmSync(join(root, process.env.PI_WEB_DIST_DIR), { recursive: true, force: true });
assert.ok(!existsSync(join(root, process.env.PI_WEB_DIST_DIR, "dev/lock")), "Run in a checkout without an active dev server");

const work = mkdtempSync(join(tmpdir(), "pi-web-pf-210-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const dataRoot = join(work, "data");
const SESSION = "e2e-pf-210-session";
const ROUTE = "/api/polyforge/progress";
const POLL_KEY = "pi-web:polyforge-poll-interval";
const PROBE = "/api/polyforge/e2e-settle-probe";
const EPOCH = "2026-10-31T00:00:00.000Z";
const GOAL = "feat: pi-web 作业卡片详情区重构——自上而下三段、无嵌套滚动与实时进度";
const SHORT = "pi-web 作业卡片详情区重构";
const NO_BOUNDARY = "fix: plain whole goal without a clause boundary";
const jsonl = (records) => records.map((r) => JSON.stringify(r)).join("\n") + "\n";
mkdirSync(project);
mkdirSync(dataRoot, { recursive: true });
writeHostSession(agentDir, { id: SESSION, cwd: project, text: "E2E pf 210 host session", timestamp: EPOCH });

// ── UI fixture: documented wire shapes of the existing reads ───────────────
const job = (wi, slug, status, stepAgeSec = 30) => ({ wi, slug, status, stepAgeSec, idleAgeSec: 2, candidate: "pi:e2e/model", spawnN: 1 });
const A = job("wi_E2E210A01", "task#9210", "running", 125);
const B = job("wi_E2E210B01", "task#9211", "needs_input", 61);
const C = job("wi_E2E210C01", "task#9212", "paused", 900);
const step = (id, durationSec, runs) => ({ id, state: "timed", durationSec, runs });
const A_STEPS = ["step-grill", "step-spec", "step-plan", "step-fail", "step-code", "step-review"];
const cards = [
  { job: A, goal: GOAL, startedAt: EPOCH, steps: [step("step-grill", 15, 1), step("step-spec", 45, 1), step("step-plan", 35, 2), { id: "step-fail", state: "unknown", runs: 0 }, step("step-code", 7, 1), { id: "step-review", state: "unstarted", runs: 0 }], currentStep: "step-code", progress: { available: false, entries: [] } },
  { job: B, goal: null, startedAt: EPOCH, steps: [], currentStep: "step-code", progress: { available: false, entries: [] } },
  { job: C, goal: NO_BOUNDARY, startedAt: EPOCH, steps: [], currentStep: "step-code", progress: { available: false, entries: [] } },
];
const status = { pfPresent: true, status: { queue: { running: 1, max: 4 }, jobs: [A, B, C], tasks: { running: 0, completed: 0 } } };
const started = Date.now() - 300_000;
const at = (sec) => new Date(started + sec * 1_000).toISOString();
const watchA = () => ({ status: {
  wi: A.wi, slug: A.slug, workflowStatus: "running", currentStep: "step-code", updatedAt: EPOCH,
  steps: [
    { name: "step-grill", status: "done", startedAt: at(0) }, { name: "step-spec", status: "done", startedAt: at(15) },
    { name: "step-plan", status: "done", startedAt: at(60) }, { name: "step-fail", status: "failed", startedAt: at(95) },
    { name: "step-code", status: "in_progress", startedAt: at(120) }, { name: "step-review", status: "pending" },
  ],
  declared_steps: A_STEPS.map((id) => ({ id })),
  events: [{ at: at(0), kind: "job_started", work_item: A.wi, body: {}, seq: 1 }],
  summary: "E2E-SUMMARY", summary_age_sec: 3,
  progress: { command: "E2E-command --token=SECRET", command_age_sec: 5, tool_calls: 2, recent_files: ["e2e/recent.ts"] },
  origin: { kind: "chat", owner: "u_e2e210" },
} });
const watchB = () => ({ status: { wi: B.wi, slug: B.slug, workflowStatus: "needs_input", currentStep: "step-code", updatedAt: EPOCH,
  steps: [{ name: "step-code", status: "pending" }], declared_steps: [{ id: "step-code" }], events: [] } });
const watchC = () => ({ status: { wi: C.wi, slug: C.slug, workflowStatus: "paused", currentStep: "step-code", updatedAt: EPOCH,
  steps: [{ name: "step-code", status: "blocked", startedAt: at(0) }], declared_steps: [{ id: "step-code" }], events: [] } });
const entry = (n, text, kind = "narration") => ({ id: `e${n}`, kind, text });
const eventLine = (n, text, event, tone) => ({ id: `ev${n}`, kind: "event", text, event, ...(tone ? { tone } : {}) });
const snapshot = (lines, omitted = 0) => ({ available: true, lines, omitted });
const CODE_LINES = [
  eventLine(0, "00:00:01 ▶ step-code #1 · pi:e2e/model", "step_started", "accent"),
  entry(1, "E2E-STEP-CODE-NARRATION"),
  entry(2, "bash: curl --token=*** https://e2e.invalid", "tool"),
];
const defaultProgress = () => snapshot(CODE_LINES);

// ── real read-face fixture: one daemon data root, capsule transcripts ──────
const routeWi = (n) => `wi_E2E210Route${n}`;
const caps = {};
for (const n of [1, 2, 3, 4, 5]) {
  const wi = routeWi(n);
  caps[wi] = join(work, "capsules", wi);
  mkdirSync(join(caps[wi], "runtime-state"), { recursive: true });
  mkdirSync(join(caps[wi], "session"), { recursive: true });
}
const piText = (id, text) => ({ type: "message", id, parentId: null, timestamp: EPOCH, message: { role: "assistant", content: [{ type: "text", text }] } });
const pi = (id, content) => ({ type: "message", id, parentId: null, timestamp: EPOCH, message: { role: "assistant", content } });
const routePi = join(caps[routeWi(1)], "session", "pi.jsonl");
writeFileSync(routePi, jsonl([
  { type: "session", id: "s", timestamp: EPOCH },
  { type: "message", id: "u", parentId: null, timestamp: EPOCH, message: { role: "user", content: "SECRET-PROMPT" } },
  piText("n", "E2E-ROUTE-NARRATION token=SECRET-TOKEN"),
  pi("tc", [{ type: "thinking", thinking: "E2E-ROUTE-THINKING" }, { type: "toolCall", id: "c", name: "bash", arguments: { command: "curl --token=SECRET-TOKEN https://e2e.invalid" } }]),
  { type: "message", id: "tr", parentId: null, timestamp: EPOCH, message: { role: "toolResult", toolName: "bash", content: [{ type: "text", text: "SECRET-TOOL-RESULT" }] } },
]));
const routeReview = join(caps[routeWi(1)], "session", "review.jsonl");
writeFileSync(routeReview, jsonl([{ ...piText("r", "E2E-ROUTE-REVIEW-NARRATION"), timestamp: "2026-10-31T00:00:30.000Z" }]));
mkdirSync(join(dataRoot, "events"), { recursive: true });
writeFileSync(join(dataRoot, "events", `wi-${routeWi(1)}.jsonl`), jsonl([
  { at: EPOCH, kind: "step_started", work_item: routeWi(1), body: { step: "code", spawn_n: 1, model: "pi:e2e/model" } },
  { at: "2026-10-31T00:00:20.000Z", kind: "step_done", work_item: routeWi(1), body: { step: "code", spawn_n: 1 } },
  { at: "2026-10-31T00:00:25.000Z", kind: "step_started", work_item: routeWi(1), body: { step: "review", spawn_n: 1, model: "pi:e2e/model" } },
]));
const manyPi = join(caps[routeWi(5)], "session", "many.jsonl");
writeFileSync(manyPi, jsonl([...Array.from({ length: 60 }, (_, i) => piText(`m${i}`, `E2E-MANY-${String(i + 1).padStart(2, "0")}`))]));
const partialPi = join(caps[routeWi(4)], "session", "partial.jsonl");
writeFileSync(partialPi, '{"type":"message","id":"half","message":{"role":"assistant","content":[{"type":"text","text":"E2E-HALF"');
const journal = (wi, transcript, stepId = "code", more = []) => writeFileSync(join(caps[wi], "runtime-state", "spawn-journal.jsonl"), jsonl([{ at: EPOCH, work_item: wi, step_id: stepId, spawn_n: 1, started_at: EPOCH, transcript, harness: "pi" }, ...more]));
journal(routeWi(1), routePi, "code", [{ at: "2026-10-31T00:00:25.000Z", work_item: routeWi(1), step_id: "review", spawn_n: 1, started_at: "2026-10-31T00:00:25.000Z", transcript: routeReview, harness: "pi" }]);
journal(routeWi(2), "/etc/passwd");
journal(routeWi(3), join(caps[routeWi(3)], "session", "missing.jsonl"));
journal(routeWi(4), partialPi);
journal(routeWi(5), manyPi);
const runtimeState = join(dataRoot, "runtime-state.json");
writeFileSync(runtimeState, JSON.stringify({ version: 1, jobs: [1, 2, 3, 4, 5].map((n) => ({
  work_item_id: routeWi(n), capsule_dir: caps[routeWi(n)], transcript: n === 3 ? join(caps[routeWi(3)], "session", "missing.jsonl") : undefined,
})) }));

// ── scenario bookkeeping and browser helpers ───────────────────────────────
const failures = [];
const check = createCheck({ failures });
let base;
const card = (page, wi) => dock(page).locator(`[data-pf-card-wi="${wi}"]`);
const progressRequests = (page) => page.__progressRequests;
const h = createHarness({
  viewport: { width: 1280, height: 900 },
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "en" },
  session: SESSION,
  ready: "E2E pf 210 host session",
  async setup(page) {
  const progress = { status: 200, body: defaultProgress(), hits: [] };
  page.__progress = progress;
  page.__progressRequests = [];
  // The daemon drops a finished job from runtime-state.json and `pf ops
  // status` (runtime dropJob): `__dropA` serves A's absence from both lists
  // and the watch read failing for it, as on real data. Its progress read
  // gets the route's real answer after the drop: 200 from
  // the event log alone, lifecycle lines only (`__dropProgress`), which the
  // open detail must merge into its held stream, never replace it with.
  page.__dropA = false;
  page.__dropProgress = snapshot([]);
  page.__droppedReads = 0;
  // PROBE (the settle probe) and every other path answer {}.
  await mockPolyforge(page, {
    status: () => {
      if (page.__dropA) page.__droppedReads += 1;
      return page.__dropA ? { ...status, status: { ...status.status, jobs: status.status.jobs.filter((j) => j.wi !== A.wi) } } : status;
    },
    cards: () => {
      if (page.__dropA) page.__droppedReads += 1;
      return { pfPresent: true, cards: page.__dropA ? cards.filter((c) => c.job.wi !== A.wi) : cards };
    },
    watch: (url, route) => {
      const wi = url.searchParams.get("wi");
      if (page.__dropA && (wi === A.wi || wi === A.slug)) return reply(route, { error: "E2E job dropped" }, 404);
      return wi === B.wi || wi === B.slug ? watchB() : wi === C.wi || wi === C.slug ? watchC() : watchA();
    },
    progress: (url, route) => {
      progress.hits.push(Date.now());
      if (page.__dropA && url.searchParams.get("wi") === A.wi) return page.__dropProgress;
      if (progress.status !== 200) return reply(route, { error: "E2E stub failure" }, progress.status);
      return progress.body;
    },
  });
  page.on("request", (request) => { if (new URL(request.url()).pathname === ROUTE) page.__progressRequests.push(Date.now()); });
  },
  async after(page, { clock = false }) {
    if (clock) { await page.clock.install({ time: new Date() }); await page.clock.pauseAt(new Date(Date.now() + 2000)); }
  },
});
const { withPage } = h;
// owner-intended change B waits for an answer, so the
// Polyforge button's opening walks to B's detail first; wait for that landing
// and B's one progress read (a non-running selection is never polled), then
// count A's reads from zero.
async function openA(page) {
  await openDock(page);
  await waitFor(async () => (await detail(page).innerText()).includes(B.slug), "the opening walks to the waiting job B");
  await waitFor(() => progressRequests(page).length >= 1, "B's one progress read");
  page.__progressRequests = [];
  await card(page, A.wi).waitFor({ timeout: 30_000 }); await card(page, A.wi).click();
  await dock(page).getByRole("button", { name: /back to job list|deselect/i }).waitFor({ timeout: 10_000 });
}
const waitFor = createWaitFor({ every: 150 });
const tick = (page, ms) => (ms > 0 ? page.clock.runFor(ms) : Promise.resolve());
// N8: an absence check settles on a real condition — the page issues a probe
// request after whatever the clock tick fired and the probe's answer lands,
// so every earlier request of the page has been dispatched and observed.
const settle = (page) => page.evaluate((probe) => fetch(probe).then((response) => response.text()), PROBE);
const detail = (page) => dock(page).locator("[data-pf-detail-title]").first();
const progressBlock = (page) => dock(page).locator("[data-pf-progress]").first();
const timing = (page, id) => dock(page).locator(`[data-pf-step-timing="${id}"]`).first();
const titleText = async (page) => (await detail(page).innerText()).replace(/\s+/g, " ").trim();
// the title row mounts before the job's watch snapshot lands, and
// the hover's current step and the summary fields come with that snapshot.
// Under `next start` a read right after mount can beat it; wait (bounded) for it.
const watchLanded = (page) => dock(page).locator('[data-pf-detail-title][title*="Current step"]').first().waitFor({ timeout: 10_000 });
async function order(page, labels) {
  return dock(page).evaluate((scope, wanted) => {
    const leaf = (text) => [...scope.querySelectorAll("*")].find((el) => el.children.length === 0 && (el.textContent || "").trim().startsWith(text));
    const summary = scope.querySelector("[data-pf-detail-summary]");
    const title = scope.querySelector("[data-pf-detail-title]");
    const dag = scope.querySelector("[data-pf-detail-graph]");
    const progress = scope.querySelector("[data-pf-progress]");
    const region = scope.querySelector("[data-pf-detail-scroll]");
    const found = { summary, title, dag, progress };
    for (const label of wanted) found[label] = leaf(label);
    const follows = (a, b) => Boolean(a && b && a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
    const inSummary = wanted.every((label) => Boolean(summary && found[label] && summary.contains(found[label])));
    // The progress block is the last block: nothing in the region follows it
    // outside it.
    const last = Boolean(region && progress && [...region.querySelectorAll("*")].every((el) => progress.contains(el) || !follows(progress, el)));
    return { present: Object.fromEntries(Object.entries(found).map(([k, v]) => [k, Boolean(v)])),
      order: { title: summary?.contains(title) ?? false, facts: follows(title, found.Model), summary: follows(found.Model, found.Summary),
        tools: follows(found.Summary, found["Tool calls"]), recent: follows(found["Tool calls"], found["Recent files"]),
        origin: follows(found["Recent files"], found.Origin), inSummary, dag: follows(found.Origin, dag) && !summary?.contains(dag),
        progress: follows(dag, progress), last } };
  }, labels);
}
const SUMMARY_LABELS = ["Model", "Summary", "Tool calls", "Recent files", "Origin"];

try {
  const logFile = join(work, "server.log");
  ({ base } = await h.startServer({ name: "polyforge-210", logFile, env: { PI_WEB_DIST_DIR: process.env.PI_WEB_DIST_DIR,
    PI_CODING_AGENT_DIR: agentDir, POLYFORGE_DATA_DIR: dataRoot, PI_WEB_ALLOWED_ROOT_PREFIXES: work } }));
  await h.launchBrowser(() => chromium.launch());

  await check("S210-01", () => withPage({}, async (page) => {
    await openA(page); await detail(page).waitFor({ timeout: 10_000 });
    // Bounded wait for every summary block before one ordering read: under a loaded full bar the
    // detail mounts before its summary labels render (verification s17).
    let result = await order(page, SUMMARY_LABELS);
    for (const deadline = Date.now() + 30_000; !Object.values(result.present).every(Boolean) && Date.now() < deadline;) {
      await delay(250); result = await order(page, SUMMARY_LABELS);
    }
    for (const [key, present] of Object.entries(result.present)) assert.ok(present, `${key} is rendered`);
    for (const [key, ok] of Object.entries(result.order)) assert.ok(ok, `the detail block order holds at ${key}: ${JSON.stringify(result)}`);
    const text = await dock(page).innerText();
    assert.doesNotMatch(text, /\bCommand\b|E2E-command/, "the command row is gone");
  }));

  await check("S210-02", () => withPage({}, async (page) => {
    await openA(page); await detail(page).waitFor({ timeout: 10_000 }); await watchLanded(page);
    const text = await titleText(page);
    assert.ok(text.includes(A.slug), `the title row carries the slug: ${text}`);
    assert.ok(text.includes(SHORT), `the title row carries the heuristic short title: ${text}`);
    assert.ok(text.includes("Running"), `the title row carries the localized job status: ${text}`);
    assert.doesNotMatch(text, /feat:|自上而下/, "the prefix and the later clauses are not in the short title");
    const deselect = await detail(page).getByRole("button", { name: /back to job list|deselect/i }).count();
    assert.equal(deselect, 1, "the deselect control sits in the title row");
    const hover = await detail(page).getAttribute("title");
    assert.ok(hover?.includes(GOAL), `the hover carries the full goal: ${hover}`);
    assert.ok(hover?.includes("Current step") && hover.includes("step-code"), `the hover carries the current step: ${hover}`);
    const style = await detail(page).evaluate((scope, short) => {
      const el = [...scope.querySelectorAll("*")].find((node) => node.children.length === 0 && (node.textContent || "").trim() === short);
      if (!el) return null;
      const s = getComputedStyle(el); return { whiteSpace: s.whiteSpace, overflow: s.overflow, textOverflow: s.textOverflow };
    }, SHORT);
    assert.ok(style, "the short title has its own element");
    assert.ok(style.whiteSpace === "nowrap" && /hidden/.test(style.overflow) && style.textOverflow === "ellipsis", `one-line ellipsis: ${JSON.stringify(style)}`);
    // the hover is no longer the only way to the full goal.
    const goal = dock(page).locator("[data-pf-detail-summary] [data-pf-summary-goal]").first();
    assert.equal((await goal.innerText()).trim(), GOAL, "the summary shows the full goal");
    assert.equal(await dock(page).locator("[data-pf-detail-summary] [data-pf-goal-toggle]").getAttribute("aria-expanded"), "false", "folded, with its expand control");
  }));

  await check("S210-03", () => withPage({}, async (page) => {
    await openDock(page); await card(page, C.wi).click();
    await dock(page).getByRole("button", { name: /back to job list|deselect/i }).waitFor({ timeout: 10_000 });
    const text = await titleText(page);
    assert.ok(text.includes("plain whole goal without a clause boundary"), `the boundary-free goal stays whole: ${text}`);
    assert.ok(!text.includes("fix:"), "the conventional-commit prefix is stripped");
    await dock(page).getByRole("button", { name: /back to job list|deselect/i }).click();
    await card(page, B.wi).click();
    await dock(page).getByRole("button", { name: /back to job list|deselect/i }).waitFor({ timeout: 10_000 });
    const fallback = await titleText(page);
    assert.ok(fallback.includes(B.slug) && fallback.includes("Needs input"), `the no-goal title row carries slug + status: ${fallback}`);
    assert.ok(!/undefined|null|E2E/i.test(fallback), `no invented title is rendered: ${fallback}`);
    assert.equal(await dock(page).locator("[data-pf-summary-goal], [data-pf-goal-toggle]").count(), 0, "no goal line and no toggle without a goal");
  }));

  await check("S210-04", () => withPage({}, async (page) => {
    await openA(page); await timing(page, "step-grill").waitFor({ timeout: 10_000 });
    assert.equal((await timing(page, "step-grill").innerText()).trim(), "+15s", "a done step shows its start-to-next-start duration");
    assert.equal((await timing(page, "step-spec").innerText()).trim(), "+45s", "the next done step keeps the same semantics");
    assert.match(await timing(page, "step-plan").innerText(), /×2/, "a rerun step keeps its rerun count on the timing line");
    const node = dock(page).getByRole("listitem").filter({ hasText: "step-grill" }).first();
    assert.match(await node.getAttribute("title"), /\d{2}:\d{2}:\d{2}/, "the node hover carries the start time");
  }));

  await check("S210-05", () => withPage({ clock: true }, async (page) => {
    await openA(page); await timing(page, "step-code").waitFor({ timeout: 10_000 });
    const first = (await timing(page, "step-code").innerText()).trim();
    assert.match(first, /^\+\d+[smh]/, `the in-progress step shows a live age: ${first}`);
    await tick(page, 3000);
    await waitFor(async () => (await timing(page, "step-code").innerText()).trim() !== first, "the live age grows with the watch poll");
    for (const id of ["step-fail", "step-review"]) {
      const line = (await timing(page, id).innerText()).trim();
      assert.equal(line, "", `${id} keeps an empty timing line (${line})`);
    }
    const all = await dock(page).innerText();
    assert.doesNotMatch(all, /NaN|-\d+[smh]/, "no NaN or negative timing is rendered");
  }));

  await check("S210-06", () => withPage({ clock: true }, async (page) => {
    await openA(page); await progressBlock(page).waitFor({ timeout: 10_000 });
    await waitFor(async () => /E2E-STEP-CODE-NARRATION/.test(await progressBlock(page).innerText()), "the narration entry");
    const kinds = await progressBlock(page).locator("[data-pf-progress-kind]").evaluateAll((els) => els.map((el) => el.getAttribute("data-pf-progress-kind")));
    assert.ok(kinds.includes("narration") && kinds.includes("tool"), `both whitelisted kinds render: ${JSON.stringify(kinds)}`);
    assert.match(await progressBlock(page).innerText(), /bash: curl --token=\*\*\*/, "the tool line carries the masked command");
    // owner-intended change: the tool line is the shared tool card — one
    // aria-expanded header whose title is the full masked header text.
    const toolHeader = progressBlock(page).locator('[data-pf-progress-kind="tool"] button[aria-expanded]');
    assert.equal(await toolHeader.count(), 1, "the tool line is one card with one header control");
    assert.equal(await toolHeader.getAttribute("title"), "bash: curl --token=*** https://e2e.invalid", "the card header carries the full masked text on hover");
    // owner-intended change: the block's step_started line is absorbed into its
    // group head (its text and raw kind on the head's title); a lifecycle line
    // that is not absorbed (needs_input below) stays a marker row with its tone.
    const startHead = progressBlock(page).locator("[data-pf-progress-block-toggle]").first();
    const startTitle = await startHead.getAttribute("title");
    assert.ok(startTitle.includes("step_started") && startTitle.includes("00:00:01 ▶ step-code #1 · pi:e2e/model"), `the head carries the absorbed start line: ${startTitle}`);
    // (reverses #210's current-step-only tail by ruling): a later
    // step's lines join the stream and the earlier step's lines stay.
    page.__progress.body = snapshot([...CODE_LINES, eventLine(8, "00:00:20 ▶ step-review #1 · pi:e2e/model", "step_started", "accent"), entry(9, "E2E-STEP-REVIEW-NARRATION"),
      eventLine(10, "00:00:21 ? step-review", "needs_input", "amber")]);
    await tick(page, 2000);
    await waitFor(async () => /E2E-STEP-REVIEW-NARRATION/.test(await progressBlock(page).innerText()), "the later step's lines join");
    assert.equal(await progressBlock(page).locator('[data-pf-progress-kind="event"]').getAttribute("data-pf-progress-tone"), "amber", "a lifecycle line carries its tone");
    // (owner intentional change): two spawns form two folded
    // blocks and only the newest is expanded, so the earlier block's lines are
    // one click away rather than silently dropped. Expand it, then assert the
    // stream order is unchanged.
    const firstBlock = progressBlock(page).locator("[data-pf-progress-block-toggle]").first();
    assert.match(await firstBlock.innerText(), /code #1 · 3 lines/, "the earlier block's header names its step, spawn and count");
    await firstBlock.evaluate((el) => el.click());
    await waitFor(async () => /E2E-STEP-CODE-NARRATION/.test(await progressBlock(page).innerText()), "the earlier step's lines expand");
    const text = await progressBlock(page).innerText();
    assert.ok(text.indexOf("E2E-STEP-CODE-NARRATION") >= 0 && text.indexOf("E2E-STEP-CODE-NARRATION") < text.indexOf("E2E-STEP-REVIEW-NARRATION"), `the earlier step's lines stay, in order: ${text}`);
  }));

  await check("S210-07", () => withPage({}, async (page) => {
    page.__progress.body = { available: false, lines: [], omitted: 0 };
    await openA(page); await progressBlock(page).waitFor({ timeout: 10_000 });
    await waitFor(async () => /unavailable|no progress|empty|不可用|无进度/i.test(await progressBlock(page).innerText()), "the localized unavailable state");
    assert.doesNotMatch(await progressBlock(page).innerText(), /E2E-STEP|bash:/, "no fabricated line is shown");
  }));

  await check("S210-08", async () => {
    const response = await fetch(`${base}${ROUTE}?wi=${routeWi(1)}`, { signal: AbortSignal.timeout(30_000) });
    assert.equal(response.status, 200, "a valid wi answers 200");
    const text = await response.text(); const body = JSON.parse(text);
    assert.deepEqual(Object.keys(body).sort(), ["available", "lines", "omitted"], `the whole-job wire shape: ${text}`);
    assert.equal(body.available, true, "the stream is available");
    assert.equal(body.omitted, 0, "nothing omitted under the bound");
    assert.ok(body.lines.some((e) => e.kind === "narration" && e.text.includes("E2E-ROUTE-NARRATION token=***")), `narration is shown masked: ${text}`);
    assert.ok(body.lines.some((e) => e.kind === "narration" && e.variant === "thinking" && e.text === "E2E-ROUTE-THINKING"), `non-empty thinking is a thinking narration line: ${text}`);
    assert.ok(body.lines.some((e) => e.kind === "tool" && /bash: curl --token=\*\*\*/.test(e.text)), `one tool line is shown masked: ${text}`);
    const narration = (needle) => body.lines.findIndex((e) => e.text.includes(needle));
    assert.ok(narration("E2E-ROUTE-NARRATION") >= 0 && narration("E2E-ROUTE-REVIEW-NARRATION") > narration("E2E-ROUTE-NARRATION"), `every spawn's lines, in order: ${text}`);
    const events = body.lines.filter((e) => e.kind === "event");
    assert.deepEqual(events.map((e) => [e.event, e.tone]), [["step_started", "accent"], ["step_done", "dim"], ["step_started", "accent"]], `the lifecycle lines carry tone and raw kind: ${text}`);
    assert.match(events[0].text, /^\d{2}:\d{2}:\d{2} ▶ code #1 · pi:e2e\/model$/);
    assert.match(events[1].text, /^\d{2}:\d{2}:\d{2} ■ code \+20s$/);
    for (const line of body.lines) {
      if (line.kind !== "event") assert.ok(!("tone" in line) && !("event" in line), `tone/event ride event lines only: ${JSON.stringify(line)}`);
    }
    for (const secret of ["SECRET-TOOL-RESULT", "SECRET-PROMPT", "SECRET-TOKEN"]) assert.ok(!text.includes(secret), `the response leaks ${secret}`);
    for (const kind of ["toolResult", "user", "system"]) assert.ok(!text.includes(kind), `the response carries no ${kind} record`);
    assert.ok(!text.includes(work), "no path leaves the read face");
  });

  await check("S210-09", () => withPage({ clock: true, storage: { [POLL_KEY]: "1" } }, async (page) => {
    await openA(page); await progressBlock(page).waitFor({ timeout: 10_000 });
    await waitFor(() => progressRequests(page).length >= 1, "the immediate read");
    await tick(page, 1900); await settle(page);
    assert.equal(progressRequests(page).length, 1, "no request before the fixed 2 s window elapses");
    await tick(page, 300);
    await waitFor(() => progressRequests(page).length >= 2, "the first 2 s tick");
    const beforeHide = progressRequests(page).length;
    await page.evaluate(() => {
      Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
      Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await tick(page, 6000); await settle(page);
    assert.equal(progressRequests(page).length, beforeHide, "a hidden page issues no progress request");
    await page.evaluate(() => {
      Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
      Object.defineProperty(document, "hidden", { configurable: true, get: () => false });
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await waitFor(() => progressRequests(page).length > beforeHide, "becoming visible reads immediately");
    await dock(page).getByRole("button", { name: /back to job list|deselect/i }).click();
    await card(page, B.wi).click();
    await waitFor(() => progressRequests(page).length > beforeHide + 1, "a non-running selection reads once", 5_000);
    const once = progressRequests(page).length;
    await tick(page, 6000); await settle(page);
    assert.equal(progressRequests(page).length, once, "a non-running selection is never polled");
  }));

  await check("S210-10", async () => {
    // (reverses #210's newest-50 cap by ruling) and
    // (removed the 2000-line window): every line of the job is served and rendered.
    const response = await fetch(`${base}${ROUTE}?wi=${routeWi(5)}`, { signal: AbortSignal.timeout(30_000) });
    const body = await response.json();
    assert.equal(response.status, 200); assert.equal(body.lines.length, 60, "the read face serves every line under the bound");
    assert.equal(body.omitted, 0, "and omits nothing");
    assert.equal(body.lines[0].text, "E2E-MANY-01", "the oldest line is kept");
    assert.equal(body.lines.at(-1).text, "E2E-MANY-60", "the newest line is last");
    await withPage({}, async (page) => {
      page.__progress.body = snapshot(Array.from({ length: 60 }, (_, i) => entry(i, `E2E-LINE-${String(i + 1).padStart(2, "0")}`)));
      await openA(page); await progressBlock(page).waitFor({ timeout: 10_000 });
      await waitFor(async () => (await progressBlock(page).locator("[data-pf-progress-kind]").count()) === 60, "all 60 lines render");
      assert.equal(await progressBlock(page).locator("[data-pf-progress-omitted]").count(), 0, "no omitted marker under the bound");
      // owner-intended change: narration renders as Markdown in full (no cap, no
      // ellipsis), so its full text is the visible text itself rather than a hover
      // title; the guarantee — every line's full text is reachable — is unchanged.
      const all = await progressBlock(page).locator("[data-pf-progress-kind]").evaluateAll((els) => els.map((el) => el.innerText.trim()));
      assert.deepEqual(all, Array.from({ length: 60 }, (_, i) => `E2E-LINE-${String(i + 1).padStart(2, "0")}`), "each line shows its full text");
      // owner-intended change: the Markdown body clips horizontal overflow, which
      // CSS reports as overflow-y auto; no element may still be a vertical scroller —
      // none with a height bound or vertical overflow, and never the area itself.
      const nested = await progressBlock(page).evaluate((scope) => [scope, ...scope.querySelectorAll("*")].some((el) => {
        const style = getComputedStyle(el);
        return /(auto|scroll)/.test(style.overflowY) && (el === scope || style.maxHeight !== "none" || el.scrollHeight > el.clientHeight + 1);
      }));
      assert.equal(nested, false, "the progress area has no inner vertical scroller");
    });
  });

  await check("S210-11", () => withPage({ clock: true }, async (page) => {
    await openA(page); await progressBlock(page).waitFor({ timeout: 10_000 });
    await waitFor(async () => /E2E-STEP-CODE-NARRATION/.test(await progressBlock(page).innerText()), "the first good tail");
    page.__progress.status = 500;
    const before = page.__progress.hits.length;
    await tick(page, 2000);
    await waitFor(() => page.__progress.hits.length > before, "the failed read is issued"); await settle(page);
    assert.match(await progressBlock(page).innerText(), /E2E-STEP-CODE-NARRATION/, "a failed read keeps the previous snapshot");
    page.__progress.status = 200; page.__progress.body = { available: false, lines: [], omitted: 0 };
    const degraded = page.__progress.hits.length;
    await tick(page, 2000);
    await waitFor(() => page.__progress.hits.length > degraded, "the degraded read is issued"); await settle(page);
    assert.match(await progressBlock(page).innerText(), /E2E-STEP-CODE-NARRATION/, "a degraded read keeps the previous snapshot too");
    assert.doesNotMatch(await progressBlock(page).innerText(), /error|failed/i, "no extra error readout is rendered");
  }));

  await check("S210-12", async () => {
    const get = async (path, init = {}) => { const response = await fetch(`${base}${path}`, { signal: AbortSignal.timeout(30_000), ...init }); return { status: response.status, text: await response.text() }; };
    for (const query of ["", "?wi=", "?wi=../../etc/passwd", "?wi=wi_E2E210Unknown1"]) {
      const bad = await get(`${ROUTE}${query}`);
      assert.equal(bad.status, 400, `a wrong-shaped or unknown wi is refused before any transcript read (${query || "no wi"})`);
      assert.ok(!bad.text.includes("root:"), "no transcript content leaves a refused request");
    }
    const outside = await get(`${ROUTE}?wi=${routeWi(2)}`);
    assert.equal(outside.status, 200, "a transcript outside the capsule/data root degrades with 200");
    assert.deepEqual(JSON.parse(outside.text).lines, [], "the outside path is not read (it adds no line)");
    assert.ok(!outside.text.includes("root:"), "the outside file's content never leaves");
    const partial = await get(`${ROUTE}?wi=${routeWi(4)}`);
    assert.equal(partial.status, 200, "a partially written transcript answers 200, never 5xx");
    assert.deepEqual(JSON.parse(partial.text).lines, [], "a partially written line is not read");
    // (N6): a missing job index degrades instead of refusing.
    renameSync(runtimeState, `${runtimeState}.bak`);
    try {
      const missing = await get(`${ROUTE}?wi=${routeWi(1)}`);
      assert.equal(missing.status, 200, "a missing job index answers 200");
      assert.deepEqual(JSON.parse(missing.text), { available: false, lines: [], omitted: 0 }, "a missing job index is available:false");
      assert.equal((await get(`${ROUTE}?wi=../../etc/passwd`)).status, 400, "a malformed id stays 400 without an index");
    } finally {
      renameSync(`${runtimeState}.bak`, runtimeState);
    }
    const gated = await get(`${ROUTE}?wi=${routeWi(1)}`, { headers: { "sec-fetch-site": "cross-site" } });
    assert.equal(gated.status, 403, "the new route sits behind the existing /api gate");
    const post = await get(`${ROUTE}?wi=${routeWi(1)}`, { method: "POST", headers: { "content-type": "application/json" }, body: "{}" });
    assert.equal(post.status, 405, "the read face is GET only");
  });

  await check("S210-13", async () => {
    await withPage({}, async (page) => {
      await openA(page); await detail(page).waitFor({ timeout: 10_000 }); await watchLanded(page);
      await progressBlock(page).waitFor({ timeout: 10_000 });
      const result = await order(page, SUMMARY_LABELS);
      for (const [key, ok] of Object.entries(result.order)) assert.ok(ok, `the three-block order holds at ${key}: ${JSON.stringify(result)}`);
      // (reverses pi#112's toggle by ruling): no mode control, no
      // collapsed sync row, no step-timing block in the summary.
      assert.equal(await dock(page).getByRole("button", { name: /^(simple|verbose)$/i }).count(), 0, "the event-mode toggle is gone");
      assert.equal(await dock(page).getByText("Step timing", { exact: true }).count(), 0, "the step-timing block has left for the DAG");
      assert.doesNotMatch(await dock(page).innerText(), /sync ×/, "no collapsed sync row");
    });
  });

  await check("S210-14", async () => {
    // N7: exact strings, not vacuous alternations.
    const read = (path) => import("node:fs/promises").then((fs) => fs.readFile(join(root, path), "utf8"));
    const docs = await read("docs/agents/polyforge-panel.md");
    for (const needle of ["`GET /api/polyforge/progress?wi=<wi_id>`, whole-job", "`PROGRESS_POLL_MS` = 2000", "no per-line length cap", "`variant:\"thinking\"`", "`PROGRESS_RENDER_CHUNK` = 200", "**Order ("]) {
      assert.ok(docs.includes(needle), `the panel note documents ${needle}`);
    }
    const map = await read("AGENTS.md");
    assert.ok(map.includes("  polyforge/progress/route.ts     GET ?wi=wi_… the job detail's execution progress: the whole job's complete stream"), "the file map names the progress route and its whole-job face");
    const expected = {
      en: ['"polyforge.progress.title": "Execution progress"', '"polyforge.progress.empty": "No progress yet"', '"polyforge.progress.unavailable": "Progress unavailable"'],
      "zh-CN": ['"polyforge.progress.title": "执行进度"', '"polyforge.progress.empty": "暂无进度"', '"polyforge.progress.unavailable": "进度不可用"'],
      "zh-TW": ['"polyforge.progress.title": "執行進度"', '"polyforge.progress.empty": "暫無進度"', '"polyforge.progress.unavailable": "進度不可用"'],
    };
    for (const [name, needles] of Object.entries(expected)) {
      const source = await read(`lib/i18n/messages/${name}.ts`);
      assert.ok(!source.includes("polyforge.stepTiming"), `the dead step-timing key leaves ${name}`);
      for (const needle of needles) assert.ok(source.includes(needle), `${name} carries ${needle}`);
      assert.match(source, /"polyforge\.progress\.omitted": "[^"]*\{count\}[^"]*"/, `${name} carries the omitted marker with its count`);
    }
  });

  await check("S210-15", () => withPage({ clock: true }, async (page) => {
    // code review: the open detail of a job that finishes keeps
    // its last progress after the daemon drops the job from both lists.
    // the route then answers from the event log alone
    // (lifecycle lines only, the same event ids); the detail keeps its
    // narration and tool lines and gains the terminal line it lacked.
    const done = eventLine(8, "00:00:25 ✓ step-code · done-8s", "step_done");
    const terminal = eventLine(9, "00:00:30 ■ terminal · done", "terminal", "green");
    page.__progress.body = snapshot([...CODE_LINES, done]);
    page.__dropProgress = snapshot([CODE_LINES[0], done, terminal]);
    await openA(page); await progressBlock(page).waitFor({ timeout: 10_000 });
    // owner-intended change: the block's step_done line is absorbed into its
    // group head — its text and raw kind are on the head's title, not a body row.
    const head = progressBlock(page).locator("[data-pf-progress-block-toggle]").first();
    const headTitle = async () => ((await head.count()) ? (await head.getAttribute("title")) ?? "" : "");
    await waitFor(async () => /done-8s/.test(await headTitle()) && /E2E-STEP-CODE-NARRATION/.test(await progressBlock(page).innerText()), "the finished job's last lines render");
    page.__dropA = true;
    // Both lists answer without A at least twice (the dock's 3 s cadence), and
    // the progress read the status change triggers has settled.
    await waitFor(async () => { if (page.__droppedReads < 4) await tick(page, 3000); return page.__droppedReads >= 4; }, "both lists answer without the finished job");
    await settle(page);
    const text = await progressBlock(page).innerText();
    for (const needle of ["E2E-STEP-CODE-NARRATION", "bash: curl --token=***", "terminal · done"]) assert.ok(text.includes(needle), `the last stream stays visible and gains the terminal line (${needle}): ${text}`);
    assert.ok((await headTitle()).includes("done-8s") && (await headTitle()).includes("step_done"), `the absorbed step_done line stays on the head: ${await headTitle()}`);
    assert.ok(text.indexOf("bash: curl --token=***") < text.indexOf("terminal · done"), `the terminal line follows the held stream: ${text}`);
    assert.doesNotMatch(text, /Progress unavailable|Loading/, "the stream is not replaced by a state line");
    assert.equal(await detail(page).count(), 1, "the detail stays open");
  }));
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
