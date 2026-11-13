// carries: components/PolyforgePanel*, hooks/usePolyforge.*, lib/polyforge-status*, app/api/polyforge/status/*, hooks/usePolyforgePanelPreferences*, lib/polyforge-panel-preferences*, lib/polyforge-cards*, app/api/polyforge/cards/*, app/api/polyforge/watch/*
// carries: app/api/polyforge/polyforge-cards-routes*, app/api/polyforge/decisions/*, lib/polyforge-decisions*
// carries: components/PolyforgeWorkItems*
// — Polyforge Jobs tab card stream: end-to-end acceptance pass.
//
// Authored pre-code against artifacts/spec.md. The suite drives the new
// surfaces only through the one read face the spec names (GET
// /api/polyforge/cards) and the Jobs tab's card markup (`data-pf-card`,
// `data-pf-card-wi`), with a stub `pf` and a throwaway daemon data root
// (`$HOME/.local/share/polyforge`) holding controlled event, capsule and
// transcript fixtures for both harness formats. Every scenario logs PASS/FAIL
// and the run exits 1 on any FAIL; at the pre-implementation baseline no card
// or route exists, so every kept scenario is assertion-level red.
//
//   node e2e/polyforge-cards.mjs
//
// extends it with the stacked Work layout (S419-*): the top
// part keeps the row (the list left, the sub-tab rail right), the selected
// item's detail below spans the whole panel width — S419-1 for Jobs, S419-2
// for Tasks and 工单列表 (their reads mocked in the browser). The suite already carries
// components/PolyforgePanel* and is inside the light tier's #485 selection, so
// the scenarios add no declaring suite to a capped path.
import assert from "node:assert/strict";
import { appendFileSync, chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { chromium } from "playwright";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, suiteDist, writeFileAtomic, writeHostSession } from "./harness.mjs";
import { openPolyforgeDock as openDock, polyforgeDock as dock, polyforgeToggle as pfButton } from "./polyforge-dock.mjs";
import { NAV_MS } from "./timeouts.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-cards-e2e-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(work, "project");
const dataRoot = join(home, ".local", "share", "polyforge");
const SESSION = "e2e-pf-cards-session";
const CARDS = "/api/polyforge/cards";
const STATUS = "/api/polyforge/status";
const BARRIER = "/__e2e_request_barrier";
const TITLE_KEY = "pi-web:polyforge-title-length";
const POLL_KEY = "pi-web:polyforge-poll-interval";
// the goal fixture is 84 code points, well past the new
// 60-code-point default, so "the default truncation hides the tail" keeps
// holding with room to spare (the allowlist maps the goal from the capsule
// fixture, so S3/S15/S16 read the same text).
const GOAL = `E2E-GOAL-START-${"G".repeat(60)}-GOALTAIL`;
const EPOCH = "2026-10-30T01:00:00.000Z";
mkdirSync(project);
mkdirSync(dataRoot, { recursive: true });
writeHostSession(agentDir, { id: SESSION, cwd: project, text: "E2E pf cards host session", timestamp: EPOCH });

// ---- jobs and daemon records ----
const job = (wi, slug, status, spawn, step, idle, candidate) => ({ work_item_id: wi, slug, status, spawn_n: spawn,
  step_age_sec: step, last_work_ago_sec: idle, candidate, session_fingerprint: "fp-cards", origin: { kind: "chat", owner: "u_e2e" } });
const A = job("wi_E2EcardsA01", "task#9101", "running", 3, 125, 4, "pi:e2e/card-model");
const B = job("wi_E2EcardsB01", "task#9102", "needs_input", 1, 61, 9, "claude:e2e/card-model");
const T = job("wi_E2EcardsB02", "task#9105", "paused", 2, 900, 880, "codex:e2e/card-model");
const C = job("wi_E2EcardsC01", "task#9103", "queued", 0, 0, 12, "pi:e2e/c-model");
const D = job("wi_E2EcardsD01", "task#9104", "queued", 0, 0, 30, "pi:e2e/no-item");
const E = job("wi_E2EcardsE01", "task#9106", "running", 1, 5, 1, "pi:e2e/new");
const capsule = (wi) => join(work, "capsules", wi);
const pi = (id, content) => ({ type: "message", id, parentId: null, timestamp: EPOCH, message: { role: "assistant", content } });
const claude = (content) => ({ type: "assistant", timestamp: EPOCH, message: { role: "assistant", content } });
const piText = (marker) => pi(`pi-${marker}`, [{ type: "text", text: marker }]);
const piLines = (from, to) => Array.from({ length: to - from + 1 }, (_, i) => piText(`A-PROGRESS-${String(from + i).padStart(3, "0")}`));
const at = (min, sec = 0) => new Date(Date.parse(EPOCH) + min * 60_000 + sec * 1_000).toISOString();
const ev = (wi, n, kind, body) => ({ at: at(n), kind, work_item: wi, body, seq: n + 1 });
const eventsA = [
  ev(A.work_item_id, 0, "job_started", { spawn_n: 1 }), ev(A.work_item_id, 30, "job_started", { spawn_n: 2 }),
  ev(A.work_item_id, 0.1, "step_started", { step: "step-grill" }), ev(A.work_item_id, 0.2, "step_done", { step: "step-grill" }),
  ev(A.work_item_id, 0.3, "step_started", { step: "step-spec" }), ev(A.work_item_id, 0.5, "step_done", { step: "step-spec" }),
  ev(A.work_item_id, 0.55, "step_started", { step: "step-plan" }), ev(A.work_item_id, 1.05, "step_done", { step: "step-plan" }),
  ev(A.work_item_id, 1.1, "step_started", { step: "step-plan" }), ev(A.work_item_id, 1.15, "step_done", { step: "step-plan" }),
];
const jobs = [A, B, T, C, D, E];

function writeCapsule(meta) {
  const dir = capsule(meta.wi);
  mkdirSync(join(dir, "runtime-state"), { recursive: true });
  mkdirSync(join(dir, "workflows", "feature-full"), { recursive: true });
  mkdirSync(join(dir, "session"), { recursive: true });
  writeFileSync(join(dir, "capsule.json"), `${JSON.stringify({ work_item_id: meta.wi, bundle_name: "feature-full", project: "workbench" }, null, 2)}\n`);
  writeFileSync(join(dir, "workflows", "feature-full", "workflow.yaml"),
    `name: feature-full\nsteps:\n${meta.steps.map((id) => `  - { id: ${id} }`).join("\n")}\n`);
  if (meta.goal !== null) {
    writeFileSync(join(dir, "runtime-state", "work-item.json"),
      `${JSON.stringify({ id: meta.wi, slug: meta.slug, project: "workbench", goal: meta.goal }, null, 2)}\n`);
    writeFileSync(join(dir, "runtime-state", "execution.json"), `${JSON.stringify({
      current_step: meta.current ?? meta.steps[0], workflow_status: "running", steps: meta.stepsStatus,
    }, null, 2)}\n`);
    writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"),
      `${JSON.stringify({ at: meta.journalAt, work_item: meta.wi, step_id: meta.current ?? meta.steps[0], spawn_token: "e2e#1",
        started_at: meta.journalAt, session_handle: "e2e", spawn_n: 1, resume: false, transcript: meta.transcript, harness: meta.harness })}\n`);
  }
}

const aPi = join(capsule(A.work_item_id), "session", "session-e2e#1.jsonl");
const bClaude = join(capsule(B.work_item_id), "session", "session-e2e#2.jsonl");
const cMissing = join(capsule(C.work_item_id), "session", "missing.jsonl");
const aStatus = { "step-grill": { status: "done" }, "step-spec": { status: "done" }, "step-plan": { status: "done" },
  "step-nostart": { status: "done" }, "step-skip": { status: "skipped" }, "step-code": { status: "in_progress", step_started_at: at(0.2) }, "step-review": { status: "pending" } };
writeCapsule({ wi: A.work_item_id, slug: A.slug, goal: GOAL, steps: ["step-grill", "step-spec", "step-plan", "step-nostart", "step-skip", "step-code", "step-review"],
  stepsStatus: aStatus, current: "step-code", transcript: aPi, harness: "pi", journalAt: at(0.2) });
writeCapsule({ wi: B.work_item_id, slug: B.slug, goal: "E2E-B-GOAL", steps: ["step-code"],
  stepsStatus: { "step-code": { status: "done" } }, current: "step-code", transcript: bClaude, harness: "claude", journalAt: at(1) });
writeCapsule({ wi: T.work_item_id, slug: T.slug, goal: "E2E-T-GOAL", steps: ["step-code"],
  stepsStatus: { "step-code": { status: "blocked" } }, current: "step-code", transcript: cMissing, harness: "pi", journalAt: at(1) });
writeCapsule({ wi: C.work_item_id, slug: C.slug, goal: "E2E-C-GOAL", steps: ["step-code"],
  stepsStatus: { "step-code": { status: "pending" } }, current: "step-code", transcript: cMissing, harness: "pi", journalAt: at(2) });
writeCapsule({ wi: E.work_item_id, slug: E.slug, goal: "E2E-E-GOAL appended", steps: ["step-code"],
  stepsStatus: { "step-code": { status: "in_progress" } }, current: "step-code", transcript: cMissing, harness: "pi", journalAt: at(3) });
writeCapsule({ wi: D.work_item_id, slug: D.slug, goal: null, steps: [] });

function writeIndex() {
  const records = jobs.map((j) => ({ work_item_id: j.work_item_id, project: "workbench", slug: j.slug, state: j.status === "paused" ? "paused" : "running",
    capsule_dir: capsule(j.work_item_id), bundle_name: "feature-full", spawn_n: j.spawn_n,
    transcript: j === A ? aPi : j === B ? bClaude : cMissing }));
  writeFileSync(join(dataRoot, "runtime-state.json"), `${JSON.stringify({ version: 1, jobs: records }, null, 2)}\n`);
}
function writeEvents() {
  mkdirSync(join(dataRoot, "events"), { recursive: true });
  writeFileSync(join(dataRoot, "events", `wi-${A.work_item_id}.jsonl`), eventsA.map((e) => JSON.stringify(e)).join("\n") + "\n");
  for (const [j, minute] of [[B, 1], [T, 1], [C, 2], [E, 3]]) {
    writeFileSync(join(dataRoot, "events", `wi-${j.work_item_id}.jsonl`),
      `${JSON.stringify({ at: at(minute), kind: "job_started", work_item: j.work_item_id, body: { spawn_n: 1 }, seq: 1 })}\n`);
  }
  writeFileSync(join(dataRoot, "events", `wi-${D.work_item_id}.jsonl`), "");
}
function writeTranscripts() {
  const records = [...piLines(1, 240),
    pi("pi-tool", [{ type: "toolCall", id: "tc-pi", name: "bash", arguments: { command: "printf --token=PI-SECRET-TOKEN" } }]),
    { type: "message", id: "pi-result", parentId: null, timestamp: EPOCH, message: { role: "toolResult", content: [{ type: "text", text: "PI-SECRET-TOOL-RESULT" }], toolName: "bash" } },
    pi("pi-think", [{ type: "thinking", thinking: "PI-SECRET-THINKING" }]),
    { type: "message", id: "pi-user", parentId: null, timestamp: EPOCH, message: { role: "user", content: "PI-SECRET-USER-PROMPT" } }];
  writeFileSync(aPi, records.map((r) => JSON.stringify(r)).join("\n") + "\n");
  const claudeRecords = [
    { type: "user", message: { role: "user", content: [{ type: "text", text: "CLAUDE-SECRET-PROMPT" }] } },
    claude([{ type: "text", text: "CLAUDE-VISIBLE-ASSISTANT" }, { type: "tool_use", id: "tu-1", name: "bash", input: { command: "printf --token=CLAUDE-SECRET-TOKEN" } }]),
    { type: "user", message: { role: "user", content: [{ type: "tool_result", tool_use_id: "tu-1", content: "CLAUDE-SECRET-TOOL-RESULT" }] } },
    claude([{ type: "thinking", thinking: "CLAUDE-SECRET-THINKING" }, { type: "text", text: "CLAUDE-SECOND-VISIBLE" }]),
  ];
  writeFileSync(bClaude, claudeRecords.map((r) => JSON.stringify(r)).join("\n") + "\n");
}
const appendA = (lines) => appendFileSync(aPi, lines.map((line) => JSON.stringify(line)).join("\n") + "\n");

// ---- stub pf ----
const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const slugs = Object.fromEntries(jobs.map((j) => [j.work_item_id, j.slug]));
// the step facts left the card for the job detail, which reads
// them from `pf watch`. A's watch snapshot: the declared order, every step
// state (a done step with no start, a skipped one, the running one, a pending
// one) and fixed start times, so the running step's live age grows.
const A_STEP_IDS = ["step-grill", "step-spec", "step-plan", "step-nostart", "step-skip", "step-code", "step-review"];
// Rebuilt on every setOps, so the running step is seconds old when a
// scenario starts and its age shows at second granularity.
const watchA = (now = Date.now()) => {
  const startedAt = (sec) => new Date(now - 300_000 + sec * 1_000).toISOString();
  return { known: true, work_item: A.work_item_id, slug: A.slug, job: { status: "running" },
  execution: { workflow_status: "running", current_step: "step-code", steps: {
    "step-grill": { status: "done", started_at: startedAt(0) }, "step-spec": { status: "done", started_at: startedAt(60) },
    "step-plan": { status: "done", started_at: startedAt(120) }, "step-nostart": { status: "done" }, "step-skip": { status: "skipped" },
    "step-code": { status: "in_progress", started_at: startedAt(295) }, "step-review": { status: "pending" } },
  declared_steps: A_STEP_IDS.map((id) => ({ id })) },
  events: [{ at: startedAt(295), kind: "step_started", work_item: A.work_item_id, body: { step: "step-code" }, seq: 1 }], last_seq: 1 };
};
const setOps = (listed) => writeFileAtomic(stateFile, JSON.stringify({ mode: "ok", slugs, watchA: watchA(),
  ops: { machine_id: "m-e2e", workspaces: [work], workflows_dir: join(work, "wf"), env_names: ["E2E_ENV"], jobs: listed,
    queue: { pending: 0, running: 2, max_concurrent: 4 } } }));
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename)); const args = process.argv.slice(2);
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const out = (v) => { const b = Buffer.from(JSON.stringify(v) + "\\n"); let o = 0;
  while (o < b.length) { try { o += fs.writeSync(1, b, o); } catch (e) { if (e.code !== "EAGAIN") throw e; } } process.exit(0); };
if (args.includes("--version")) { process.stdout.write("v2.20.11\\n"); process.exit(0); }
const rest = args.filter((a) => a !== "--json");
if (rest[0] === "ops" && rest[1] === "status") { fs.appendFileSync(path.join(dir, "ops-status.log"), Date.now() + "\\n"); out(s.ops); }
if (rest[0] === "watch") {
  const wi = rest[1];
  if (wi === s.watchA.work_item || wi === s.watchA.slug) out(s.watchA);
  out({ work_item: wi, slug: s.slugs[wi] || wi, known: true, job: { status: "running" },
    execution: { workflow_status: "running", current_step: "step-code", steps: { "step-code": { status: "in_progress" } } },
    events: [{ at: new Date().toISOString(), kind: "step_started", work_item: wi, body: { step: "step-code" }, seq: 1 }], last_seq: 1 });
}
if (rest[0] === "msg") out({ conversations: [] });
if (rest[0] === "task" && rest[1] === "list") out({ tasks: [], now: new Date().toISOString() });
if (rest[0] === "config" && rest[1] === "list") out({ items: [] });
if (rest[0] === "config" && rest[1] === "workspace") out({ mapped: false, workspace: null });
if (rest[0] === "recall") out({ items: [], total: 0 });
if (rest[0] === "memory" && rest[1] === "show") { process.stderr.write("pf: not found\\n"); process.exit(4); }
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
writeIndex(); writeEvents(); writeTranscripts(); setOps([T, C, B, A]); // ops order intentionally not the card order
const opsStatusLog = join(stubDir, "ops-status.log");
/** Start times (ms) of every `pf ops status` spawn the stub served. */
const opsSpawns = () => (existsSync(opsStatusLog) ? readFileSync(opsStatusLog, "utf8").split("\n").filter(Boolean).map(Number) : []);

// ---- scenario bookkeeping ----
const check = createCheck();
const failures = check.failures;

let base;
const h = createHarness({
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "en" },
  path: ({ session = true }) => (session ? `/?session=${SESSION}` : "/"),
  ready: "E2E pf cards host session",
  async setup(page) {
    page.__cardRequests = []; page.__cardResponses = []; page.__statusRequests = []; page.__barriers = 0;
    await page.route((url) => url.pathname === BARRIER, (route) => route.fulfill({ status: 204 }));
    page.on("request", (request) => {
      const pathname = new URL(request.url()).pathname;
      if (pathname === CARDS) page.__cardRequests.push({ at: Date.now(), url: request.url() });
      else if (pathname === STATUS) page.__statusRequests.push({ at: Date.now() });
      else if (pathname === BARRIER) page.__barriers += 1;
    });
    page.on("response", (response) => {
      if (new URL(response.url()).pathname === CARDS) response.text().then((text) => page.__cardResponses.push({ at: Date.now(), status: response.status(), text })).catch(() => {});
    });
  },
});
const { errors, withPage } = h;
const card = (page, wi) => dock(page).locator(`[data-pf-card-wi="${wi}"]`);
// the visible status element is the leaf carrying the full
// status as its title — the running card's text is its duration alone, so the
// title is `<status word> <n>m` and ends with the visible text.
const cardStatus = (page, wi) => card(page, wi).evaluate((el) => {
  const lf = [...el.querySelectorAll("[title]")].map((node) => ({ text: (node.textContent ?? "").trim(), title: node.getAttribute("title") ?? "" }))
    .find((entry) => entry.text && (entry.title === entry.text || entry.title.endsWith(` ${entry.text}`)));
  return { text: lf?.text ?? "", title: lf?.title ?? "", aria: el.getAttribute("aria-label") ?? "" };
});
const cardOrder = (page) => dock(page).locator("[data-pf-card-wi]").evaluateAll((nodes) => nodes.map((node) => node.getAttribute("data-pf-card-wi")));
const waitFor = createWaitFor();
/**
 * instead of a wall-clock grace window, wait `ms` of PAGE time
 * (a page timer queued after every live poll interval's next due time, so a
 * still-running 1 s poll would have fired first), then fetch a stubbed
 * barrier URL and wait (bounded) until its request is logged. Requests are
 * logged in issue order, so the request log is complete up to the barrier.
 */
async function afterPageTime(page, ms) {
  const seen = page.__barriers;
  await page.evaluate(async ({ wait, url }) => {
    await new Promise((resolve) => setTimeout(resolve, wait));
    await fetch(url, { cache: "no-store" });
  }, { wait: ms, url: BARRIER });
  await waitFor(() => page.__barriers > seen, "the barrier request is logged", 5_000);
}
/**
 * the server reuses one `pf ops status` read for 1 s, so a
 * fixture change is served up to 1 s later. A scenario that changes the job
 * set waits (bounded) until the card route serves exactly that set before it
 * opens the dock, so its first read is never the previous fixture's snapshot.
 */
async function setOpsSettled(listed) {
  setOps(listed);
  const wanted = listed.map((j) => j.work_item_id).sort().join(",");
  await waitFor(async () => {
    const response = await fetch(`${base}${CARDS}`, { signal: AbortSignal.timeout(30_000) });
    const body = await response.json();
    return Array.isArray(body.cards) && body.cards.map((c) => c.job?.wi).sort().join(",") === wanted;
  }, "the card route serves the fixture's job set", 10_000);
}
const openSettings = async (page) => {
  const show = page.getByRole("button", { name: "Show sidebar", exact: true });
  if (await show.isVisible().catch(() => false)) await show.click();
  await page.getByRole("button", { name: "Settings", exact: true }).first().click();
  return page.getByRole("dialog", { name: "Settings", exact: true });
};
const numberInputs = (dialog) => dialog.locator('input[type="number"]:visible').evaluateAll((nodes) => nodes.map((node) => ({
  value: node.value, min: node.getAttribute("min"), max: node.getAttribute("max"),
})));

// ---- the stacked Work layout ----
// The three Work widths — the 460px and 340px desktop docks (the ~72px text
// rail) and the 375px sheet (the 40px icon rail).
const WORK_WIDTHS = [
  [{ storage: { "pi-web:polyforge-dock-width": "460" } }, "460px dock", 72],
  [{ storage: { "pi-web:polyforge-dock-width": "340" } }, "340px dock", 72],
  [{ viewport: { width: 375, height: 800 } }, "375px sheet", 40],
];
// One read of the Work layout: each named element's box (null when absent),
// the panel's inner box (inside its border), and every element on the path
// from each `paths` element up to the panel that overflows horizontally (the
// card row scrolls sideways by design and is on no such path).
const workBoxes = (page, selectors, paths = []) => dock(page).evaluate((panel, { selectors, paths }) => {
  const rect = (el) => { if (!el) return null; const r = el.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height }; };
  const outer = panel.getBoundingClientRect();
  const out = { panel: { x: outer.x + panel.clientLeft, y: outer.y + panel.clientTop, width: panel.clientWidth, height: panel.clientHeight }, overflow: [] };
  for (const [name, selector] of Object.entries(selectors)) out[name] = rect(panel.querySelector(selector));
  const seen = new Set();
  for (const name of paths) {
    for (let el = panel.querySelector(selectors[name]); el && !seen.has(el); el = el === panel ? null : el.parentElement) {
      seen.add(el);
      if (el.scrollWidth > el.clientWidth + 1) out.overflow.push(`${name}: <${el.tagName.toLowerCase()}> ${el.scrollWidth}/${el.clientWidth}`);
    }
  }
  return out;
}, { selectors, paths });
// A layout read only once it has settled: two reads a double rAF apart agree
// (a ResizeObserver refit lands a frame later —). Bounded by
// waitFor's deadline; never a fixed delay.
async function settledLayout(page, measure, what) {
  return waitFor(async () => {
    const first = JSON.stringify(await measure());
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const second = await measure();
    if (JSON.stringify(second) !== first) throw new Error(`still changing: ${JSON.stringify(second)}`);
    return second;
  }, `${what} settles`, 20_000);
}
const RAIL = '[data-pf-tabstrip="work"]';
// S419-2's Tasks and 工单列表 reads, served in the browser in the routes'
// documented wire shapes (the stub pf lists no task and no work item).
const TASKS_419 = ["1", "2", "3"].map((n) => ({ id: `task_00000000000419${n}0`, project: "workbench", state: "completed",
  prompt_summary: `E2E-419 task row ${n}`, ended_at: EPOCH, wall_consumed_ns: 60e9, report_tail: "", reporter: "alice" }));
const TASK_DETAIL_419 = { ...TASKS_419[1], candidates: ["pi:e2e/model"], max_duration_ns: 3_600e9, max_tokens: 100_000,
  wall_consumed_ns: 60e9, tokens_consumed: 1_000, created_at: EPOCH, started_at: EPOCH, ended_at: EPOCH,
  prompt: "E2E-419 TASK DETAIL", report: "E2E-419 task report", report_len: 19, truncated: false };
const ITEMS_419 = ["1", "2", "3"].map((n) => ({ id: `wi_E2E419item${n}`, slug: `${n}`, project: "workbench", status: "queued",
  priority: "normal", bundle_name: "develop", goal: `E2E-419 item row ${n}`, created_at: at(Number(n)) }));
const ITEM_DETAIL_419 = { ...ITEMS_419[1], goal: "E2E-419 ITEM DETAIL", labels: [], milestone: null, reporter: null, assignee: null,
  updated_at: EPOCH, closed_at: null, waiting_for: null, waiting_on: null, content: "E2E-419 item content", content_len: 20 };
async function mockLists419(page) {
  const json = (route, body) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  await page.route((url) => url.pathname === "/api/polyforge/tasks", (route) => json(route, { pfPresent: true, capable: true, tasks: TASKS_419 }));
  await page.route((url) => url.pathname.startsWith("/api/polyforge/tasks/"), (route) => json(route, { task: TASK_DETAIL_419 }));
  await page.route((url) => url.pathname === "/api/polyforge/work-items", (route) => json(route, { pfPresent: true, capable: true, items: ITEMS_419, total: 3 }));
  await page.route((url) => url.pathname.startsWith("/api/polyforge/work-items/"), (route) => json(route, { pfPresent: true, capable: true, item: ITEM_DETAIL_419 }));
}

try {
  await h.launchBrowser(() => chromium.launch());
  ({ base } = await h.startServer({ name: "polyforge-cards", logFile: join(work, "server.log"), distDir: suiteDist("polyforge-cards"), env: {
    PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: home, POLYFORGE_DATA_DIR: dataRoot,
    PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: "" } }));

  await check("S1", () => withPage({}, async (page) => {
    // the server may already hold the startup fixture's
    // [T, C, B, A] read for OPS_STATUS_REUSE_MS; under `next start` the dock
    // opens well inside that second, so wait until the route serves D too.
    writeTranscripts(); await setOpsSettled([T, C, B, A, D]);
    const panel = await openDock(page);
    await card(page, A.work_item_id).waitFor({ timeout: 30_000 });
    assert.equal(await panel.locator("[data-pf-card]").count(), 5, "one card per ops-status job");
    const expected = [[B.work_item_id, /needs input/i], [T.work_item_id, /paused/i], [C.work_item_id, /queued/i]];
    for (const [wi, label] of expected) assert.match((await cardStatus(page, wi)).text, label, `${wi} status label`);
    // owner-intended change: a running card's visible status is the
    // duration alone; the status word stays in its aria-label/title.
    const runningStatus = await cardStatus(page, A.work_item_id);
    assert.match(runningStatus.text, /^\d+m$/, `the running card shows the duration alone (${runningStatus.text})`);
    assert.match(runningStatus.aria, new RegExp(`^${A.slug} · Running \\d+m$`), `the running aria-label keeps the full status (${runningStatus.aria})`);
  }));

  await check("S2", () => withPage({}, async (page) => {
    setOps([T, C, B, A]); writeTranscripts();
    await openDock(page); await card(page, A.work_item_id).waitFor({ timeout: 30_000 });
    // (owner-approved intentional change ③): the model, spawn
    // count and step/idle age left line 1 for the job detail.
    const text = await card(page, A.work_item_id).innerText();
    assert.doesNotMatch(text, /#3/, "line 1 no longer carries the spawn count");
    assert.doesNotMatch(text, /pi:e2e\/card-model/, "line 1 no longer carries the candidate");
    assert.doesNotMatch(text, /\bidle\b/i, "line 1 no longer carries the idle age");
    await card(page, A.work_item_id).click();
    await dock(page).getByRole("button", { name: /back to job list|deselect/i }).waitFor({ timeout: 10_000 });
    const detail = await dock(page).innerText();
    assert.match(detail, /#3/, "the detail keeps the spawn count"); assert.match(detail, /pi:e2e\/card-model/, "the detail keeps the candidate");
    assert.match(detail, /step/i); assert.match(detail, /idle/i);
    // the card no longer lists step ids, so this now waits
    // (bounded) for the detail's own watch read instead of matching card text.
    await waitFor(async () => /step started|step-code/i.test(await dock(page).innerText()), "the existing detail and event tail open");
    await card(page, A.work_item_id).click();
    await waitFor(async () => (await dock(page).getByRole("button", { name: /back to job list|deselect/i }).count()) === 0, "second click deselects");
  }));

  await check("S3", () => withPage({}, async (page) => {
    setOps([T, C, B, A]); writeTranscripts();
    await openDock(page); const node = card(page, A.work_item_id); await node.waitFor({ timeout: 30_000 });
    const text = await node.innerText();
    assert.ok(text.includes("E2E-GOAL-START"), "the goal head renders on line 1");
    assert.ok(Array.from(GOAL).length > 60, `the fixture is longer than the default 60 code points (${Array.from(GOAL).length})`);
    assert.ok(!text.includes("GOALTAIL"), `the default 60-code-point truncation hides the tail: ${text}`);
    assert.match(text, /…/, "a truncated goal ends with an ellipsis");
    const titles = [await node.getAttribute("title"), ...await node.locator("[title]").evaluateAll((nodes) => nodes.map((n) => n.getAttribute("title")))];
    assert.ok(titles.includes(GOAL), "the full goal is available on hover (title attribute)");
  }));

  await check("S4", () => withPage({}, async (page) => {
    writeTranscripts(); await setOpsSettled([T, C, B, A, D]);
    await openDock(page); await card(page, A.work_item_id).waitFor({ timeout: 30_000 });
    await waitFor(async () => (await cardOrder(page)).length === 5, "five cards");
    assert.deepEqual(await cardOrder(page), [A.work_item_id, B.work_item_id, T.work_item_id, C.work_item_id, D.work_item_id],
      "ascending earliest job_started, ties by wi id, no-start last");
  }));

  await check("S5", () => withPage({}, async (page) => {
    writeTranscripts(); await setOpsSettled([T, C, B, A]);
    await openDock(page); await card(page, A.work_item_id).waitFor({ timeout: 30_000 });
    const before = await cardOrder(page);
    assert.deepEqual(before, [A.work_item_id, B.work_item_id, T.work_item_id, C.work_item_id]);
    setOps([T, C, B, A, E]);
    await waitFor(async () => (await cardOrder(page)).length === 5, "the new job's card appears");
    assert.deepEqual(await cardOrder(page), [...before, E.work_item_id], "the new card appends at the bottom");
  }));

  await check("S6", () => withPage({}, async (page) => {
    writeTranscripts(); await setOpsSettled([T, C, B, A]);
    await openDock(page); await card(page, A.work_item_id).waitFor({ timeout: 30_000 });
    const before = await cardOrder(page); const ticks = page.__cardRequests.length;
    await waitFor(() => page.__cardRequests.length >= ticks + 2, "two poll ticks");
    assert.deepEqual(await cardOrder(page), before, "a poll never reorders the cards");
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.getByText("E2E pf cards host session").first().waitFor({ state: "attached", timeout: NAV_MS });
    await openDock(page); await card(page, A.work_item_id).waitFor({ timeout: 30_000 });
    assert.deepEqual(await cardOrder(page), before, "a full reload shows the same order with no duplicate");
  }));

  await check("S7", () => withPage({}, async (page) => {
    setOps([T, C, B, A]); writeTranscripts();
    await openDock(page); const node = card(page, A.work_item_id); await node.waitFor({ timeout: 30_000 });
    // (owner-ruled compact card): the card carries no step
    // information at all — every step fact below is asserted in the detail.
    const text = await node.innerText();
    for (const id of A_STEP_IDS) assert.ok(!text.includes(id), `the card carries no step id (${id})`);
    assert.equal(await node.locator("[data-pf-step-phase]").count(), 0, "no step segment on the card");
    assert.doesNotMatch(text, /×\d/, "no rerun marker on the card");
    await node.click();
    await dock(page).getByRole("button", { name: /back to job list|deselect/i }).waitFor({ timeout: 10_000 });
    const items = dock(page).getByRole("listitem");
    await waitFor(async () => (await items.count()) >= A_STEP_IDS.length, "the detail's step graph renders every declared step");
    // owner-intended change（重立自 #209）: the node shows only the step
    // name and its dot; the status moved from visible text to title/aria-label,
    // so the status assertions below read the accessible name (same patterns).
    const nodes = (await items.evaluateAll((els) => els.map((el) => el.getAttribute("aria-label") ?? ""))).map((value) => value.replace(/\s+/g, " ").trim());
    const titles = await items.evaluateAll((els) => els.map((el) => el.getAttribute("title") ?? ""));
    // owner-intended change: the node title also carries the step's start time when it has one.
    titles.forEach((value, i) => {
      const title = value.replace(/\s+/g, " ").trim();
      assert.ok(title === nodes[i] || (title.startsWith(`${nodes[i]} · `) && /\d{2}:\d{2}/.test(title.slice(nodes[i].length))), `each node's title carries the same name + status as its aria-label, then at most the start time (${title})`);
    });
    const visible = (await items.allInnerTexts()).map((value) => value.replace(/\s+/g, " ").trim());
    let last = -1;
    for (const id of A_STEP_IDS) { const i = nodes.findIndex((value) => value.startsWith(id)); assert.ok(i > last, `the detail lists ${id} in workflow order`); last = i; }
    for (const id of A_STEP_IDS) assert.ok(visible.includes(id), `the ${id} node shows its name and no visible status text (got ${JSON.stringify(visible)})`);
    const nodeText = (id) => nodes.find((value) => value.startsWith(id)) ?? "";
    // + #173 (+ #213: via title/aria-label): a skipped step names skipped, never unknown/unstarted.
    assert.match(nodeText("step-skip"), /skipped|已跳过/i, `a skipped step names the skipped label (got ${JSON.stringify(nodeText("step-skip"))})`);
    // owner-intended change（重立自 #209）: skipped is a hollow gray ring, pending a filled gray dot.
    const dotOf = (id) => items.filter({ hasText: id }).first().locator('span[aria-hidden="true"]').first().evaluate((el) => {
      const style = getComputedStyle(el);
      return { background: style.backgroundColor, border: parseFloat(style.borderTopWidth) || 0 };
    });
    const clear = (color) => color === "transparent" || /rgba\(\s*\d+\s*,\s*\d+\s*,\s*\d+\s*,\s*0\s*\)/.test(color);
    const skipDot = await dotOf("step-skip"); const pendingDot = await dotOf("step-review");
    assert.ok(clear(skipDot.background) && skipDot.border >= 1, `the skipped dot is a hollow ring (${JSON.stringify(skipDot)})`);
    assert.ok(!clear(pendingDot.background), `the pending dot stays filled (${JSON.stringify(pendingDot)})`);
    assert.doesNotMatch(nodeText("step-skip"), /未知|unknown|—|pending/i, "a skipped step is neither unknown nor unstarted");
    assert.match(nodeText("step-review"), /pending/i, "a not-started step reads as such");
    assert.match(nodeText("step-code"), /in progress/i, "the current step is recognizable");
    // owner-intended change: the timing row moved from the detail rows to the line under its DAG node
    // ([data-pf-step-timing]); the same guarantees are read there.
    const timingLine = async (id) => {
      const line = dock(page).locator(`[data-pf-step-timing="${id}"]`).first();
      return (await line.count()) ? (await line.innerText()).trim() : "";
    };
    await waitFor(async () => /\+\d/.test(await timingLine("step-code")) && await timingLine("step-code"), "the running step's timing line");
    assert.match(await timingLine("step-plan"), /×2/, "a rerun step carries the rerun marker on its timing line");
    assert.match(await timingLine("step-grill"), /\+\d/, "a done step carries its duration");
    assert.doesNotMatch(await timingLine("step-grill"), /×/, "a single-run step carries no rerun marker");
    assert.doesNotMatch(await timingLine("step-nostart"), /\+\d/, "a step with no readable start shows no fabricated duration");
    assert.doesNotMatch(await dock(page).innerText(), /NaN/, "no NaN duration");
    const first = await timingLine("step-code");
    await waitFor(async () => { const line = await timingLine("step-code"); return line && line !== first; },
      "the running step's duration grows with the detail's refresh", 12_000);
  }));

  await check("S8", () => withPage({}, async (page) => {
    setOps([T, C, B, A]); writeTranscripts();
    await openDock(page); const aCard = card(page, A.work_item_id); const bCard = card(page, B.work_item_id);
    await aCard.waitFor({ timeout: 30_000 }); await bCard.waitFor({ timeout: 30_000 });
    // owner-intended change (reverses the 32px narration):
    // the card renders no narration any more, so the route's newest-page,
    // increment and whitelist semantics are asserted on its responses, and
    // the card DOM asserts that no narration reaches it.
    await waitFor(() => page.__cardResponses.find((response) => response.text.includes("A-PROGRESS-240")), "pi progress in the route response");
    await waitFor(() => page.__cardResponses.find((response) => response.text.includes("CLAUDE-VISIBLE-ASSISTANT")), "claude progress in the route response");
    appendA([piText("A-PROGRESS-BETA")]);
    const beta = await waitFor(() => page.__cardResponses.find((response) => response.text.includes("A-PROGRESS-BETA")), "the increment response");
    assert.ok(!beta.text.includes("A-PROGRESS-240"), "the increment response does not re-send earlier entries");
    const dom = `${await aCard.innerText()}\n${await bCard.innerText()}`;
    assert.doesNotMatch(dom, /A-PROGRESS(?:-\d{3}|-BETA)|CLAUDE-VISIBLE-ASSISTANT/, "the card shows no narration");
  }));

  await check("S9", () => withPage({}, async (page) => {
    setOps([T, C, B, A]); writeTranscripts();
    await openDock(page); const bCard = card(page, B.work_item_id); await bCard.waitFor({ timeout: 30_000 });
    // (owner-approved intentional change ②): the card read face
    // carries assistant narration only — neither a tool name nor a
    // command/argument reaches it — and masking still holds.
    // owner-intended change (reverses the 32px narration):
    // those semantics now read on the route response; the card DOM shows none.
    appendA([piText("A-NARRATION token=PI-SECRET-TOKEN masked")]);
    const narration = await waitFor(() => page.__cardResponses.find((response) => response.text.includes("A-NARRATION")), "the credential-bearing narration in the route response");
    assert.match(narration.text, /token=\*\*\*/, "the credential in narration is masked");
    assert.doesNotMatch(narration.text, /bash|printf|"kind":"tool"/, "the response carries no tool entry, name or command");
    for (const secret of ["CLAUDE-SECRET-TOOL-RESULT", "CLAUDE-SECRET-THINKING", "CLAUDE-SECRET-PROMPT", "CLAUDE-SECRET-TOKEN",
      "PI-SECRET-TOOL-RESULT", "PI-SECRET-THINKING", "PI-SECRET-USER-PROMPT", "PI-SECRET-TOKEN"]) assert.ok(!narration.text.includes(secret), `the response leaks ${secret}`);
    // The whitelisted assistant text arrives on the card's first page, which is
    // what carries the Claude fixture; the increment above carries A only.
    const whitelisted = await waitFor(() => page.__cardResponses.find((response) => response.text.includes("CLAUDE-VISIBLE-ASSISTANT")), "the whitelisted assistant text");
    assert.ok(whitelisted.text.includes("CLAUDE-SECOND-VISIBLE"), "both visible assistant texts flow");
    const dom = `${await bCard.innerText()}\n${await card(page, A.work_item_id).innerText()}`;
    assert.doesNotMatch(dom, /A-PROGRESS|A-NARRATION|CLAUDE-VISIBLE-ASSISTANT|token=|bash|printf/, "no narration or tool text reaches the card");
    for (const secret of ["CLAUDE-SECRET-TOOL-RESULT", "CLAUDE-SECRET-THINKING", "CLAUDE-SECRET-PROMPT", "CLAUDE-SECRET-TOKEN",
      "PI-SECRET-TOOL-RESULT", "PI-SECRET-THINKING", "PI-SECRET-USER-PROMPT", "PI-SECRET-TOKEN"]) assert.ok(!dom.includes(secret), `the DOM leaks ${secret}`);
  }));

  await check("S10", () => withPage({}, async (page) => {
    setOps([T, C, B, A]); writeTranscripts();
    await openDock(page); const node = card(page, C.work_item_id); await node.waitFor({ timeout: 30_000 });
    // owner-intended change (reverses the 32px narration):
    // an unreadable transcript now degrades only the route's progress payload;
    // the card keeps slug + status and shows no progress state.
    await waitFor(async () => {
      const response = await fetch(`${base}${CARDS}`, { signal: AbortSignal.timeout(30_000) });
      const body = await response.json();
      return body.cards?.find((item) => item.job?.wi === C.work_item_id)?.progress?.available === false;
    }, "the degraded route payload (progress unavailable)");
    const text = await node.innerText();
    assert.ok(text.includes(C.slug), "the rest of the card still renders");
    assert.doesNotMatch(text, /unavailable|不可用|A-PROGRESS/, "the card shows no progress state");
    const ticks = page.__cardRequests.length;
    await waitFor(() => page.__cardRequests.length >= ticks + 2, "two degraded polls");
    assert.deepEqual(errors.splice(0), [], "no console error on the degraded path");
    const response = await fetch(`${base}${CARDS}`, { signal: AbortSignal.timeout(30_000) });
    assert.equal(response.status, 200, "the degraded read answers 200");
  }));

  await check("S11", async () => {
    setOps([T, C, B, A]); writeTranscripts();
    const get = async (path) => { const response = await fetch(`${base}${path}`, { signal: AbortSignal.timeout(30_000) }); return { status: response.status, text: await response.text() }; };
    const ok = await get(CARDS);
    assert.equal(ok.status, 200, "the card route answers 200"); assert.ok(JSON.parse(ok.text), "the body is decodable JSON");
    for (const query of ["?wi=../../etc/passwd", "?path=../../etc/passwd", "?cursors=not-json", `?cursors=${encodeURIComponent(JSON.stringify({ "../x": "../y" }))}`]) {
      const response = await get(`${CARDS}${query}`);
      assert.equal(response.status, 200, `malformed input answers 200 (${query})`);
      assert.ok(!response.text.includes("root:"), `no client-supplied path is read (${query})`);
    }
    for (const method of ["POST", "PUT"]) {
      const response = await fetch(`${base}${CARDS}`, { method, headers: { "content-type": "application/json" }, body: "{}" });
      assert.equal(response.status, 405, `${method} is refused with 405, got ${response.status}`);
    }
  });

  await check("S12", () => withPage({ storage: { [POLL_KEY]: "1" } }, async (page) => {
    setOps([T, C, B, A]); writeTranscripts();
    const panel = await openDock(page); await card(page, A.work_item_id).waitFor({ timeout: 30_000 });
    const startedAt = Date.now();
    await waitFor(() => page.__cardRequests.filter((r) => r.at >= startedAt).length >= 3, "three one-second ticks", 8_000);
    const early = page.__cardRequests.filter((r) => r.at >= startedAt);
    assert.ok(early.every((r) => new URL(r.url).pathname === CARDS), "one card route serves every card");
    const groups = []; for (const request of early) { if (!groups.length || request.at - groups.at(-1).at(-1).at > 300) groups.push([request]); else groups.at(-1).push(request); }
    assert.ok(groups.every((group) => group.length === 1), `one request per tick, not per card: ${groups.map((g) => g.length)}`);
    // Jobs and Tasks are Work's sub-tabs.
    const tabs = panel.locator('[data-pf-tabstrip="work"] [role="tab"]');
    await tabs.filter({ hasText: /tasks/i }).first().click();
    await afterPageTime(page, 0);
    const offJobs = page.__cardRequests.length; await afterPageTime(page, 1_500); assert.equal(page.__cardRequests.length, offJobs, "no card requests off the Jobs sub-tab");
    await tabs.filter({ hasText: /jobs/i }).first().click(); await waitFor(() => page.__cardRequests.length > offJobs, "returning to the Jobs sub-tab takes a read", 2_000);
    await panel.getByRole("button", { name: /close/i }).first().click(); await dock(page).waitFor({ state: "hidden", timeout: 20_000 });
    await afterPageTime(page, 0);
    const closed = page.__cardRequests.length; await afterPageTime(page, 1_500); assert.equal(page.__cardRequests.length, closed, "no card requests while the dock is closed");
    await pfButton(page).click(); await dock(page).waitFor({ timeout: 20_000 });
    await waitFor(() => page.__cardRequests.length > closed, "reopening takes an immediate read", 2_000);
  }));

  await check("S13", () => withPage({ storage: { [POLL_KEY]: "1" } }, async (page) => {
    setOps([T, C, B, A]); writeTranscripts();
    await openDock(page); const node = card(page, A.work_item_id); await node.waitFor({ timeout: 30_000 });
    // owner-intended change (reverses the 32px narration):
    // the card has no narration area, so the 32px self-scrolling region pin is
    // delete. The newest-page and increment semantics move to the route
    // responses, and the client's 200-entry merge cap stays covered by
    // lib/polyforge-cards-pure.test.mjs ("progress merge: ... the newest 200
    // are kept").
    const cardsOf = (response) => JSON.parse(response.text).cards ?? [];
    const progressOf = (response, wi) => cardsOf(response).find((item) => item.job?.wi === wi)?.progress ?? { entries: [] };
    const first = await waitFor(() => page.__cardResponses.find((response) => progressOf(response, A.work_item_id).entries.some((entry) => entry.text === "A-PROGRESS-240")), "the newest page in the route response");
    const firstEntries = progressOf(first, A.work_item_id).entries;
    assert.ok(firstEntries.length > 0 && firstEntries.length <= 20, `the first load carries the newest ~20 entries (${firstEntries.length})`);
    assert.ok(!firstEntries.some((entry) => entry.text === "A-PROGRESS-220"), "the first load starts at the newest ~20 entries");
    const region = await node.evaluate((el) => {
      for (const child of [el, ...el.querySelectorAll("*")]) {
        const style = getComputedStyle(child);
        if (/(auto|scroll)/.test(style.overflowY) && child.scrollHeight > child.clientHeight + 8) return { height: child.clientHeight, scroll: child.scrollHeight };
      }
      return null;
    });
    assert.equal(region, null, "the card has no narration scroll region any more");
    appendA(Array.from({ length: 220 }, (_, i) => piText(`A-PROGRESS-${String(241 + i).padStart(3, "0")}`)));
    const increments = await waitFor(() => {
      const found = page.__cardResponses.filter((response) => progressOf(response, A.work_item_id).entries.some((entry) => entry.text === "A-PROGRESS-460"));
      return found.length ? found : undefined;
    }, "the newest increment in a route response", 30_000);
    const increment = progressOf(increments.at(-1), A.work_item_id).entries;
    assert.ok(increment.length > 0 && increment.length <= 20, `a poll carries at most the 20 new entries (${increment.length})`);
    assert.ok(!increment.some((entry) => entry.text === "A-PROGRESS-240"), "an increment does not re-send the previous page");
    for (const response of page.__cardResponses) {
      const all = cardsOf(response).flatMap((item) => item.progress?.entries ?? []);
      assert.ok(all.length <= 200, `a response carries at most 200 entries (${all.length})`);
    }
  }));

  await check("S14", () => withPage({ storage: { [POLL_KEY]: "1" } }, async (page) => {
    setOps([T, C, B, A]); writeTranscripts();
    await openDock(page); const node = card(page, A.work_item_id); await node.waitFor({ timeout: 30_000 });
    await node.evaluate((el) => { el.__e2eStamp = "same-node"; });
    const ticks = page.__cardRequests.length;
    await waitFor(() => page.__cardRequests.length >= ticks + 2, "two polls before the click");
    assert.equal(await node.evaluate((el) => el.__e2eStamp), "same-node", "the poll kept the card's DOM node");
    await node.click();
    await dock(page).getByRole("button", { name: /back to job list|deselect/i }).waitFor({ timeout: 10_000 });
  }));

  await check("S15", () => withPage({ storage: { [TITLE_KEY]: "bogus", [POLL_KEY]: "-5" } }, async (page) => {
    let dialog = await openSettings(page);
    await dialog.getByRole("button", { name: "Polyforge", exact: true }).click();
    await waitFor(async () => (await numberInputs(dialog)).length >= 2, "the two numeric controls");
    const controls = await numberInputs(dialog);
    assert.equal(controls.length, 2, "exactly the two new numeric controls");
    const title = controls.find((c) => c.min === "10" && c.max === "200");
    const poll = controls.find((c) => c.min === "1" && c.max === "60");
    assert.ok(title && poll, `the bounds 10–200 and 1–60 are declared: ${JSON.stringify(controls)}`);
    // the title-length default is 60 (was 30).
    assert.deepEqual([title.value, poll.value], ["60", "3"], "corrupt stored values fall back to the defaults");
    await page.evaluate(({ t, p }) => { localStorage.setItem(t, "999"); localStorage.setItem(p, "999"); }, { t: TITLE_KEY, p: POLL_KEY });
    await page.keyboard.press("Escape"); dialog = await openSettings(page);
    await dialog.getByRole("button", { name: "Polyforge", exact: true }).click();
    await waitFor(async () => (await numberInputs(dialog)).length >= 2, "the controls after reload");
    const clamped = await numberInputs(dialog);
    assert.deepEqual([clamped.find((c) => c.min === "10").value, clamped.find((c) => c.min === "1").value], ["200", "60"],
      "out-of-range stored values clamp to their bounds");
    assert.equal(await page.evaluate(() => localStorage.getItem("pi-web:polyforge-poll-interval")), "999", "the setting is browser-stored only");
  }));

  await check("S16", () => withPage({}, async (page) => {
    setOps([T, C, B, A]); writeTranscripts();
    await openDock(page); const node = card(page, A.work_item_id); await node.waitFor({ timeout: 30_000 });
    assert.ok(!(await node.innerText()).includes("GOALTAIL"), "default truncation hides the tail");
    const dialog = await openSettings(page);
    await dialog.getByRole("button", { name: "Polyforge", exact: true }).click();
    await waitFor(async () => (await numberInputs(dialog)).length >= 2, "the title-length control");
    const title = dialog.locator('input[type="number"][min="10"]').first();
    await title.fill("90");
    await title.locator("xpath=following::button[1]").click();
    await waitFor(async () => (await page.evaluate((key) => localStorage.getItem(key), TITLE_KEY)) === "90", "the title length is saved");
    await dialog.getByRole("button", { name: /^close$/i }).first().click();
    if (!(await dock(page).isVisible().catch(() => false))) await openDock(page);
    await waitFor(async () => (await card(page, A.work_item_id).innerText()).includes("GOALTAIL"), "the new length applies without a page reload", 8_000);
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.getByText("E2E pf cards host session").first().waitFor({ state: "attached", timeout: NAV_MS });
    await openDock(page); await card(page, A.work_item_id).waitFor({ timeout: 30_000 });
    assert.ok((await card(page, A.work_item_id).innerText()).includes("GOALTAIL"), "the stored value is used after a reload");
  }));

  // S17: at the default 3 s cadence one tick — the queue
  // line's status poll plus the card poll — spawns exactly one
  // `pf ops status`: the server shares one read between the two routes.
  await check("S17", () => withPage({}, async (page) => {
    setOps([T, C, B, A]); writeTranscripts();
    await openDock(page); await card(page, A.work_item_id).waitFor({ timeout: 30_000 });
    const since = Date.now();
    const ticksAfter = () => {
      const requests = [...page.__statusRequests.map((r) => ({ at: r.at, kind: "status" })), ...page.__cardRequests.map((r) => ({ at: r.at, kind: "cards" }))]
        .filter((r) => r.at >= since).sort((a, b) => a.at - b.at);
      const groups = [];
      for (const request of requests) { if (!groups.length || request.at - groups.at(-1).at(-1).at > 1_000) groups.push([request]); else groups.at(-1).push(request); }
      // A tick is complete when both polls fired and the spawn window after it has passed.
      return groups.filter((group) => group.some((r) => r.kind === "status") && group.some((r) => r.kind === "cards") && Date.now() - group[0].at > 1_500);
    };
    await waitFor(() => ticksAfter().length >= 3, "three complete 3 s ticks", 20_000);
    const ticks = ticksAfter();
    const spawns = opsSpawns();
    for (const tick of ticks) {
      assert.equal(tick.length, 2, `one status and one card request per tick: ${tick.map((r) => r.kind)}`);
      const inTick = spawns.filter((at) => at >= tick[0].at - 500 && at <= tick[0].at + 1_500);
      assert.equal(inTick.length, 1, `one pf ops status spawn per tick, got ${inTick.length}`);
    }
  }));

  // S18 ( review s14#3): the card title is legible, not merely
  // in the DOM (innerText also counts text an overflow:hidden box clips). At
  // the 460px dock, the 375px sheet and the 340px dock the slug element is
  // not clipped (scrollWidth <= clientWidth + 1, and no ancestor up to the
  // card cuts its box) and the goal span (its `title` is the full goal) shows
  // a visible box inside the card. owner-intended change（重立自 #213）:
  // the Jobs stream is ONE row of fixed 150px cards that scrolls horizontally
  // (the grid `repeat(auto-fill, minmax(150px, 1fr))` is gone), so every width
  // shows the same one-row / 150px-card / scrollable shape; the card geometry
  // stays pinned (no narration, 43px + the measured slug row),
  // with the card-A height bound stated against the measured slug row because
  // a 150px card wraps a 14-character slug.
  for (const [opts, label, measureHeight] of [[{ storage: { "pi-web:polyforge-dock-width": "460" } }, "460px dock", true],
    [{ viewport: { width: 375, height: 800 } }, "375px sheet", false], [{ storage: { "pi-web:polyforge-dock-width": "340" } }, "340px dock", true]]) {
    await check(`S18 ${label}`, () => withPage(opts, async (page) => {
      writeTranscripts(); await setOpsSettled([T, C, B, A]);
      await openDock(page); const node = card(page, A.work_item_id); await node.waitFor({ timeout: 30_000 });
      await waitFor(async () => (await node.innerText()).includes("E2E-GOAL-START"), "the card read's goal renders", 30_000);
      const geometry = await node.evaluate((el, { slug, goal }) => {
        /** The part of `target`'s box no overflow-clipping ancestor up to the card cuts. */
        const visible = (target) => {
          const box = target.getBoundingClientRect();
          let left = box.left, right = box.right, top = box.top, bottom = box.bottom;
          for (let node = target.parentElement; node; node = node === el ? null : node.parentElement) {
            const style = getComputedStyle(node);
            if (style.overflowX === "visible" && style.overflowY === "visible") continue;
            const clip = node.getBoundingClientRect();
            left = Math.max(left, clip.left); right = Math.min(right, clip.right);
            top = Math.max(top, clip.top); bottom = Math.min(bottom, clip.bottom);
          }
          return { full: { width: box.width, height: box.height }, width: Math.max(0, right - left), height: Math.max(0, bottom - top) };
        };
        const slugNode = [...el.querySelectorAll("*")].find((child) => child.children.length === 0 && (child.textContent ?? "").trim() === slug);
        const goalNode = [...el.querySelectorAll("[title]")].find((child) => child.getAttribute("title") === goal);
        return {
          slug: slugNode ? { scrollWidth: slugNode.scrollWidth, clientWidth: slugNode.clientWidth, ...visible(slugNode) } : null,
          slugRow: slugNode?.parentElement ? slugNode.parentElement.getBoundingClientRect().height : null,
          goal: goalNode ? visible(goalNode) : null,
        };
      }, { slug: A.slug, goal: GOAL });
      assert.ok(geometry.slug, `${label}: the slug has an element of its own`);
      assert.ok(geometry.slug.scrollWidth <= geometry.slug.clientWidth + 1, `${label}: the slug element does not clip its text (${JSON.stringify(geometry.slug)})`);
      assert.ok(geometry.slug.width >= geometry.slug.full.width - 1 && geometry.slug.height >= geometry.slug.full.height - 1,
        `${label}: no ancestor cuts the slug (${JSON.stringify(geometry.slug)})`);
      assert.ok(geometry.goal, `${label}: the goal span renders`);
      assert.ok(geometry.goal.width > 0 && geometry.goal.height > 0, `${label}: the goal shows a visible box inside the card (${JSON.stringify(geometry.goal)})`);
      // the denser card, falsifiable on the old geometry (measured
      // at a63cedea: card A 112px, goal span 36px, needs_input badge 20.5px).
      // The goal span keeps its two 16px lines (+1px tolerance) — 32px now.
      assert.ok(geometry.goal.height <= 33, `${label}: the goal span is ${geometry.goal.height}px, over the 2×16px clamp (+1px) (${JSON.stringify(geometry.goal)})`);
      // A needs_input card's badge no longer inflates its slug row: 14px line
      // + 1px border = 16px (old badge: 11px × 1.5 + 2px padding + 2px border ≈ 20.5px).
      // The slug row itself is max(badge, the slug's own 16px lines); a fixed
      // card leaves the slug a narrow column beside the status label, so B's
      // 14-character slug may wrap to two 16px lines — inside the ≤33px
      // allowance for the card (one-column 340 dock included). owner 有意変更
      // the card is 180px now, so the slug has more room; the
      // bound follows the measured row either way.
      const needsB = await card(page, B.work_item_id).evaluate((el, slug) => {
        const box = (target) => (target ? target.getBoundingClientRect().height : null);
        const slugNode = [...el.querySelectorAll("*")].find((child) => child.children.length === 0 && (child.textContent ?? "").trim() === slug);
        return { row: box(slugNode?.parentElement ?? null), badge: box(el.querySelector("[data-pf-needs-input]")) };
      }, B.slug);
      assert.ok(needsB.row !== null, `${label}: the needs_input card's slug row renders`);
      assert.ok(needsB.badge !== null && needsB.badge <= 17, `${label}: the needs_input badge is ${needsB.badge}px, over the 17px bound`);
      // Measured at 150px cards: badge 16px, slug row 32px. a
      // running label with a 4-digit minute age narrows the slug beside it, so
      // card A's slug may wrap to a 3rd 16px line; the row stays a whole number
      // of 16px lines and the height bound below follows the measured row.
      assert.ok(needsB.row <= 33, `${label}: the needs_input slug row is ${needsB.row}px, over 33px (16px badge; one wrapped 2-line slug at a 150px card)`);
      assert.ok(geometry.slugRow >= 16 && geometry.slugRow % 16 === 0,
        `${label}: card A's slug row is a whole number of 16px lines (${geometry.slugRow})`);
      // a running card's age is whole minutes, computed from the
      // card's earliest job_started (A has a job_started at at(0)); ±1 absorbs a
      // minute rollover during the run. owner-intended change: the
      // visible status text is the duration alone (`23m`, matching ^\d+m$ and
      // no status word); the full `<status word> <n>m` stays in the element's
      // title and the card's aria-label. A non-running card (B, needs_input)
      // keeps its status text and has no duration.
      const expectedMinutes = Math.floor((Date.now() - Date.parse(at(0))) / 60_000);
      const runningStatus = await cardStatus(page, A.work_item_id);
      assert.match(runningStatus.text, /^\d+m$/, `${label}: the running card's visible status is the duration alone (got ${JSON.stringify(runningStatus.text)})`);
      assert.doesNotMatch(runningStatus.text, /running|运行中/i, `${label}: no status word in the running card's visible status (got ${JSON.stringify(runningStatus.text)})`);
      const minutes = Number(runningStatus.text.slice(0, -1));
      assert.ok(Math.abs(minutes - expectedMinutes) <= 1,
        `${label}: the running label shows ${expectedMinutes}m from the fixture's job_started (got ${JSON.stringify(runningStatus.text)})`);
      assert.match(runningStatus.title, /^Running \d+m$/, `${label}: the status element's title carries the full status (${runningStatus.title})`);
      assert.match(runningStatus.aria, new RegExp(`^${A.slug} · Running \\d+m$`), `${label}: the card aria-label carries the full status (${runningStatus.aria})`);
      const nonRunningStatus = await cardStatus(page, B.work_item_id);
      assert.doesNotMatch(nonRunningStatus.text, /^\d+m$/, `${label}: a non-running card shows no duration (${JSON.stringify(nonRunningStatus.text)})`);
      if (measureHeight) {
        // The docks (460px and 340px) measure card A's whole height. owner-intended change
        // （fix228-card-height， reverses the 32px
        // narration）: no narration area, card-only padding 4px and no slug/goal
        // gap, so — 2 border + 8 padding + slug row + 32 goal = 42 + slug row
        // (58px for a one-line slug, 74px for the wrapped two-line row) — the
        // bound is 43 + the measured slug row. The old 100px/117px card fails
        // it, and so does one that only removed the narration (64px/80px).
        const cardHeight = await node.evaluate((el) => el.offsetHeight);
        const bound = 43 + geometry.slugRow;
        assert.ok(cardHeight <= bound, `${label}: card A is ${cardHeight}px, over the ${bound}px card geometry (43px + its ${geometry.slugRow}px slug row)`);
      }
      // one row of fixed-width cards that scrolls left and right
      // (old: N tracks of `repeat(auto-fill, minmax(150px, 1fr))` per width).
      // owner-intended change: that fixed width is 180px, decoupled from
      // the Tasks grid's own 150px column minimum.
      const row = await node.evaluate((el) => {
        const container = el.parentElement;
        const box = el.getBoundingClientRect();
        return { width: box.width, basis: getComputedStyle(el).flexBasis, gap: getComputedStyle(container).gap,
          clientWidth: container.clientWidth, scrollWidth: container.scrollWidth,
          overflowX: getComputedStyle(container).overflowX, scrollbarWidth: getComputedStyle(container).scrollbarWidth };
      });
      assert.ok(Math.abs(row.width - 180) <= 0.5, `${label}: a job card is a fixed 180px wide (${row.width}) (owner-intended change)`);
      assert.equal(row.basis, "180px", `${label}: the card width is the 180px flex basis, not a stretched track (${row.basis}) (owner-intended change)`);
      assert.equal(row.gap, "8px", `${label}: the card gap is the unchanged 8px (${row.gap})`);
      assert.equal(row.overflowX, "auto", `${label}: the card row scrolls horizontally (${row.overflowX})`);
      assert.equal(row.scrollbarWidth, "thin", `${label}: the row's scrollbar stays visible but thin (${row.scrollbarWidth})`);
      assert.ok(row.scrollWidth > row.clientWidth, `${label}: the row overflows and can be scrolled (${row.scrollWidth}/${row.clientWidth})`);
      const shape = await dock(page).locator("[data-pf-card-wi]").evaluateAll((nodes) => ({
        rows: new Set(nodes.map((el) => Math.round(el.getBoundingClientRect().y))).size,
        widths: nodes.map((el) => el.getBoundingClientRect().width),
        order: nodes.map((el) => el.getAttribute("data-pf-card-wi")),
      }));
      assert.equal(shape.rows, 1, `${label}: every job card sits on one row (${JSON.stringify(shape)})`);
      assert.ok(shape.widths.every((width) => Math.abs(width - 180) <= 0.5), `${label}: every card is 180px wide (${JSON.stringify(shape.widths)}) (owner-intended change)`);
      assert.equal(new Set(shape.order).size, shape.order.length, `${label}: the cards keep one DOM node each (${JSON.stringify(shape.order)})`);
      // Scrolled to the row's end the last card is fully visible (its box inside
      // the row's viewport, nothing clipped) and the row has really moved.
      const end = await node.evaluate((el) => {
        const container = el.parentElement;
        container.scrollLeft = container.scrollWidth;
        const cards = [...container.querySelectorAll("[data-pf-card-wi]")];
        const last = cards.at(-1).getBoundingClientRect(); const clip = container.getBoundingClientRect();
        return { scrollLeft: container.scrollLeft, lastLeft: last.left, lastRight: last.right, clipLeft: clip.left, clipRight: clip.right };
      });
      assert.ok(end.scrollLeft > 0, `${label}: the card row scrolls to the end (${end.scrollLeft})`);
      assert.ok(end.lastRight <= end.clipRight + 1 && end.lastLeft >= end.clipLeft - 1,
        `${label}: scrolled to the end the last card is fully visible (${JSON.stringify(end)})`);
      // the 3 s card poll never resets the row's scroll position
      // (the cards keep their keys, so nothing re-mounts).
      const polls = page.__cardRequests.length;
      await waitFor(() => page.__cardRequests.length >= polls + 2, "two card polls after the scroll", 20_000);
      const after = await node.evaluate((el) => el.parentElement.scrollLeft);
      assert.ok(Math.abs(after - end.scrollLeft) <= 1, `${label}: a card poll keeps the scroll position (${after}/${end.scrollLeft})`);
    }));
  }

  // S419-1 (owner's intentional change 2026-11-09): Work is two
  // parts stacked. The top keeps the row — the card row left, the sub-tab rail
  // right — and the selected job's detail below spans the whole panel width
  // (no longer beside the rail), with the job's input box (PolyforgeJobComposer)
  // at the bottom of that full-width part; no horizontal overflow at any of the
  // three widths. Red on the old tree: the detail sat left of the rail, the
  // panel width minus the rail.
  await check("S419-1", async () => {
    writeTranscripts(); await setOpsSettled([T, C, B, A]);
    for (const [opts, label, railWidth] of WORK_WIDTHS) await withPage(opts, async (page) => {
      await openDock(page); const node = card(page, A.work_item_id); await node.waitFor({ timeout: 30_000 });
      await node.click();
      await dock(page).locator("[data-pf-detail-scroll]").waitFor({ timeout: 15_000 });
      const box = await settledLayout(page, () => workBoxes(page, {
        rail: RAIL, row: "div:has(> [data-pf-card-wi])", scroll: "[data-pf-detail-scroll]",
      }, ["rail", "scroll"]), `${label}: the Jobs layout`);
      assert.ok(box.rail && box.row && box.scroll, `${label}: the rail, the card row and the detail measure (${JSON.stringify(box)})`);
      // The top keeps the row: the cards left, the rail right at the panel's edge.
      assert.ok(box.row.x + box.row.width <= box.rail.x + 1, `${label}: the card row ends left of the rail (${box.row.x + box.row.width} > ${box.rail.x})`);
      assert.ok(Math.abs(box.rail.x + box.rail.width - (box.panel.x + box.panel.width)) <= 1, `${label}: the rail sits at the panel's right edge (${JSON.stringify(box.rail)})`);
      assert.ok(Math.abs(box.rail.width - railWidth) <= 4, `${label}: the rail is about ${railWidth}px wide (${box.rail.width})`);
      // The detail spans the whole panel width, below the top part.
      assert.ok(Math.abs(box.scroll.width - box.panel.width) <= 1 && Math.abs(box.scroll.x - box.panel.x) <= 1,
        `${label}: the detail spans the panel width (detail ${box.scroll.x}+${box.scroll.width}, panel ${box.panel.x}+${box.panel.width})`);
      assert.ok(box.scroll.y >= box.rail.y + box.rail.height - 1 && box.scroll.y >= box.row.y + box.row.height - 1,
        `${label}: the detail starts below the rail and the card row (detail top ${box.scroll.y}, rail ${JSON.stringify(box.rail)})`);
      assert.deepEqual(box.overflow, [], `${label}: no horizontal overflow`);
      // The input box at the bottom of the full-width part.
      const chat = await settledLayout(page, async () => {
        const at = await dock(page).locator("[data-pf-chat]").first().boundingBox();
        if (!at) throw new Error("the input box is not rendered");
        return at;
      }, `${label}: the input box`);
      assert.ok(Math.abs(chat.x - box.panel.x) <= 1 && Math.abs(chat.width - box.panel.width) <= 1,
        `${label}: the input box spans the panel width (${chat.x}+${chat.width})`);
      assert.ok(chat.y >= box.scroll.y + box.scroll.height - 1, `${label}: the input box sits under the detail's scroll region`);
      assert.ok(Math.abs(chat.y + chat.height - (box.panel.y + box.panel.height)) <= 1,
        `${label}: the input box is at the panel's bottom (${chat.y + chat.height}/${box.panel.y + box.panel.height})`);
    });
  });

  // S419-2 (owner-confirmed 2026-11-09): Tasks and 工单列表 follow
  // the same two parts. The top part is the row — the list with its filters
  // and search on the left, the rail on the right — and a selected task's
  // report or work item's detail shows below across the whole panel width;
  // with nothing selected the lower part is not shown. Switching sub-tabs
  // never re-mounts the main area, the rail or their row. Red on the old tree:
  // a selection replaced the list (no list above the detail).
  await check("S419-2", async () => {
    for (const [opts, label] of WORK_WIDTHS) await withPage(opts, async (page) => {
      await mockLists419(page);
      await openDock(page); await card(page, A.work_item_id).waitFor({ timeout: 30_000 });
      const rail = dock(page).locator(RAIL);
      // The nodes that must survive every switch: the main area, the rail, their row.
      await rail.evaluate((el) => { for (const node of [el, el.previousElementSibling, el.parentElement]) node.__pf419 = "kept"; });
      const panelBottom = async () => { const box = await workBoxes(page, {}); return box.panel.y + box.panel.height; };
      for (const [tab, rowSel, text, back] of [["Tasks", "button[data-pf-task]", "E2E-419 TASK DETAIL", "Back"],
        ["Work items", "button[data-pf-work-item]", "E2E-419 ITEM DETAIL", "Back"]]) {
        const what = `${label} ${tab}`;
        // The desktop tab's name also carries its running badge: match the label's start.
        await rail.getByRole("tab", { name: new RegExp(`^${tab}`) }).click();
        await waitFor(async () => (await dock(page).locator(rowSel).count()) === 3, `${what}: three rows render`, 30_000);
        // Nothing selected: no lower part — the top part (the row) fills the Work area.
        const closed = await settledLayout(page, () => workBoxes(page, { top: `${RAIL}`, row: `div:has(> ${RAIL})` }), `${what}: the list without a selection`);
        assert.ok(Math.abs(closed.row.y + closed.row.height - (closed.panel.y + closed.panel.height)) <= 1,
          `${what}: with nothing selected the top part fills the Work area (row ${JSON.stringify(closed.row)}, panel bottom ${closed.panel.y + closed.panel.height})`);
        await dock(page).locator(rowSel).nth(1).click();
        await dock(page).getByText(text, { exact: true }).waitFor({ timeout: 15_000 });
        // The list, its search and its filters stay on top beside the rail.
        await waitFor(async () => (await dock(page).locator(rowSel).count()) === 3 && await dock(page).locator(rowSel).first().isVisible()
          && await dock(page).locator('input[type="search"]').first().isVisible() && await dock(page).locator("select").first().isVisible(),
        `${what}: the list, its search and its filters stay visible above the detail`, 5_000);
        const open = await settledLayout(page, () => workBoxes(page, {
          rail: RAIL, row: `div:has(> ${RAIL})`, layout: `div:has(> div > ${RAIL})`, detail: "[data-pf-work-detail]",
        }, ["rail", "detail"]), `${what}: the list with a detail`);
        assert.ok(open.detail, `${what}: the full-width lower part renders`);
        assert.ok(await dock(page).locator("[data-pf-work-detail]").getByText(text, { exact: true }).isVisible(), `${what}: the detail is in the lower part`);
        assert.ok(Math.abs(open.detail.width - open.panel.width) <= 1 && Math.abs(open.detail.x - open.panel.x) <= 1,
          `${what}: the detail spans the panel width (detail ${open.detail.x}+${open.detail.width}, panel ${open.panel.x}+${open.panel.width})`);
        assert.ok(open.detail.y >= open.row.y + open.row.height - 1 && open.detail.y >= open.rail.y + open.rail.height - 1,
          `${what}: the detail starts below the list and the rail (detail top ${open.detail.y}, row ${JSON.stringify(open.row)})`);
        assert.ok(Math.abs(open.rail.x + open.rail.width - (open.panel.x + open.panel.width)) <= 1, `${what}: the rail sits at the panel's right edge`);
        assert.ok(open.row.height <= open.layout.height * 0.45 + 1, `${what}: the top part takes at most 45% (${open.row.height}/${open.layout.height})`);
        assert.ok(Math.abs(open.detail.y + open.detail.height - (await panelBottom())) <= 1, `${what}: the detail reaches the panel's bottom`);
        assert.deepEqual(open.overflow, [], `${what}: no horizontal overflow`);
        // Back closes the lower part; the list fills the Work area again.
        await dock(page).locator("[data-pf-work-detail]").getByRole("button", { name: back }).click();
        await waitFor(async () => !(await dock(page).locator("[data-pf-work-detail]").isVisible()), `${what}: back hides the lower part`, 5_000);
      }
      await rail.getByRole("tab", { name: /^Jobs/ }).click();
      await card(page, A.work_item_id).waitFor({ timeout: 15_000 });
      const kept = await rail.evaluate((el) => [el, el.previousElementSibling, el.parentElement].map((node) => node?.isConnected && node.__pf419 === "kept"));
      assert.deepEqual(kept, [true, true, true], `${label}: the rail, the main area and their row are the same nodes after the switches`);
    });
  });
} catch (error) {
  failures.push("suite");
  console.log(`FAIL suite: ${String(error?.message ?? error).split("\n")[0]}`);
} finally {
  await h.close();
  if (failures.length) console.log(`\nFAILED ${failures.join(", ")} — ${failures.length} scenario(s) red`);
  else console.log("\nPASS all authored card-stream scenarios");
  console.log(`server log: ${join(work, "server.log")}`);
}
if (failures.length) process.exitCode = 1;
