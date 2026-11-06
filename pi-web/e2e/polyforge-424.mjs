// carries: components/PolyforgeProgressSubagent*, components/PolyforgeProgressToolCard*, app/api/polyforge/progress/tool/*
// carries: lib/polyforge-subagents-424*
// — a step's subagents in the job detail's execution progress.
//
// AC3: a step's session view (its spawn block in the job detail) lists the
// subagents the step ran and opens each one's conversation. AC5: each
// subagent is one expandable entry under its parent tool call — task
// description, running / done (stopped once its block ended unfinished),
// duration — whose expansion shows its tool calls; a running one refreshes
// with the 2 s progress poll; claude and pi alike.
//
// The route scenarios run the real progress handlers against a throwaway
// daemon data root holding two real capsules: a claude step (the recorded
// claude 2.1.312 subagent run, lib/__fixtures__/claude-2.1.312/subagent/,
// its sidecar copied beside the capsule transcript as the daemon's sync
// does) and a pi step (the driver recorder's output for a recorded subagent
// call, lib/__fixtures__/subagents-424/). The rendering scenarios drive the
// documented wire shape through a stubbed progress read, as
// e2e/polyforge-271.mjs does. Each scenario prints `PASS S424-<n>`.
import assert from "node:assert/strict";
import { cpSync, existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { openPolyforgeDock as openDock, polyforgeDock as dock } from "./polyforge-dock.mjs";
import { COLD_MS } from "./timeouts.mjs";
import { createCheck, createHarness, createWaitFor, mockPolyforge } from "./harness.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
process.env.PI_WEB_DIST_DIR ||= ".next/e2e/polyforge-424";
if (process.env.PI_WEB_DIST_DIR.startsWith(".next/e2e/")) rmSync(join(root, process.env.PI_WEB_DIST_DIR), { recursive: true, force: true });
assert.ok(!existsSync(join(root, process.env.PI_WEB_DIST_DIR, "dev/lock")), "Run in a checkout without an active dev server");

const work = mkdtempSync(join(tmpdir(), "pi-web-pf-424-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const dataRoot = join(work, "data");
const CHAT_SESSION = "e2e-pf-424-session";
const CLAUDE_WI = "wi_S424Claude1";
const PI_WI = "wi_S424Pi00001";
const UI = "wi_S424Ui0001";
const ROUTE = "/api/polyforge/progress";
const DETAIL = "/api/polyforge/progress/tool";
const SESSION = "44444444-4444-4444-8444-444444444444";
const PI_CALL = "call_bcb397f329d148e18d52f97f";
const UI_WAIT = 8_000;
const fixture = (path) => fileURLToPath(new URL(`../lib/__fixtures__/${path}`, import.meta.url));
mkdirSync(join(agentDir, "sessions", "e2e"), { recursive: true });
mkdirSync(project);
mkdirSync(join(dataRoot, "events"), { recursive: true });
const jsonl = (records) => records.map((record) => JSON.stringify(record)).join("\n") + "\n";

writeFileSync(join(agentDir, "sessions", "e2e", `2026-11-01T00-00-00-000Z_${CHAT_SESSION}.jsonl`), jsonl([
  { type: "session", version: 3, id: CHAT_SESSION, timestamp: "2026-11-01T00:00:00.000Z", cwd: project },
  { type: "message", id: "u1", parentId: null, timestamp: "2026-11-01T00:00:00.000Z", message: { role: "user", content: "E2E pf 424 host session" } },
]));

// ── route fixture: two real capsules in a throwaway daemon data root ───────
const capsule = (wi) => {
  const dir = join(work, "capsules", wi);
  mkdirSync(join(dir, "runtime-state"), { recursive: true });
  mkdirSync(join(dir, "session"), { recursive: true });
  writeFileSync(join(dir, "runtime-state", "work-item.json"), JSON.stringify({ id: wi, goal: "g" }));
  return dir;
};
const claudeDir = capsule(CLAUDE_WI);
const claudeTranscript = join(claudeDir, "session", "session-tok.jsonl");
cpSync(fixture("claude-2.1.312/subagent/transcript.jsonl"), claudeTranscript);
cpSync(fixture("claude-2.1.312/subagent/subagents"), join(claudeDir, "session", "session-tok", "subagents"), { recursive: true });
writeFileSync(join(claudeDir, "runtime-state", "spawn-journal.jsonl"), jsonl([
  { work_item: CLAUDE_WI, step_id: "build", spawn_n: 1, started_at: "2026-11-02T02:01:02.000Z", harness: "claude", harness_session: SESSION, transcript: claudeTranscript },
]));
writeFileSync(join(dataRoot, "events", `wi-${CLAUDE_WI}.jsonl`), jsonl([
  { at: "2026-11-02T02:01:02.000Z", kind: "step_started", work_item: CLAUDE_WI, body: { step: "build", spawn_n: 1, model: "claude:e2e" } },
]));
const piDir = capsule(PI_WI);
const piTranscript = join(piDir, "session", "session-tok.jsonl");
writeFileSync(piTranscript, jsonl([
  { type: "session", id: "s", timestamp: "2026-11-02T13:24:40.000Z" },
  { type: "message", id: "m1", timestamp: "2026-11-02T13:24:44.000Z", message: { role: "assistant", content: [{ type: "toolCall", id: PI_CALL, name: "subagent", arguments: { agent: "merge-conflict-reviewer", task: "E2E-S424-PI-TASK" } }] } },
  { type: "message", id: "m2", timestamp: "2026-11-02T13:25:51.818Z", message: { role: "toolResult", toolCallId: PI_CALL, toolName: "subagent", isError: false, content: [{ type: "text", text: "done" }] } },
]));
cpSync(fixture("subagents-424/pi/session/session-tok"), join(piDir, "session", "session-tok"), { recursive: true });
writeFileSync(join(piDir, "runtime-state", "spawn-journal.jsonl"), jsonl([
  { work_item: PI_WI, step_id: "review", spawn_n: 1, started_at: "2026-11-02T13:24:40.000Z", harness: "pi", transcript: piTranscript },
]));
writeFileSync(join(dataRoot, "events", `wi-${PI_WI}.jsonl`), jsonl([
  { at: "2026-11-02T13:24:40.000Z", kind: "step_started", work_item: PI_WI, body: { step: "review", spawn_n: 1, model: "pi:e2e" } },
]));
writeFileSync(join(dataRoot, "runtime-state.json"), JSON.stringify({ version: 1, jobs: [
  { work_item_id: CLAUDE_WI, capsule_dir: claudeDir },
  { work_item_id: PI_WI, capsule_dir: piDir },
] }));

// ── UI fixtures: the documented wire shapes ─────────────────────────────────
const job = { wi: UI, slug: "task#424", status: "running", stepAgeSec: 5, idleAgeSec: 1, candidate: "pi:e2e/model", spawnN: 1 };
const status = { pfPresent: true, status: { queue: { running: 1, max: 4 }, jobs: [job], tasks: { running: 0, completed: 0 } } };
const cards = [{ job, goal: " subagents", startedAt: new Date().toISOString(), steps: [{ id: "build", state: "running", runs: 1 }], currentStep: "build", progress: { available: false, entries: [] } }];
const watch = { status: { wi: UI, slug: job.slug, workflowStatus: "running", currentStep: "build", updatedAt: new Date().toISOString(),
  steps: [{ name: "build", status: "in_progress", startedAt: new Date().toISOString() }], declared_steps: [{ id: "build" }], events: [] } };
const now = Date.now();
const started = (id, step, at) => ({ id, kind: "event", text: `00:00:00 ▶ ${step} #1 · pi:e2e`, event: "step_started", tone: "accent", at });
const done = (id, step, at) => ({ id, kind: "event", text: `00:00:09 ■ ${step} +9s`, event: "step_done", tone: "dim", at });
const tool = (id, name, argument, status, extra = {}) => ({ id, kind: "tool", text: `${name}: ${argument}`, tool: { name, argument, status }, ...extra });
const subagent = (over = {}) => ({
  id: "0123456789abcdef", description: "E2E-S424-TASK read the notes", agent: "general-purpose", status: "running", startAt: now - 4_000,
  lines: [tool("0123456789abcdef.10.0", "Read", "E2E-S424-SUB-READ", "ok"), { id: "0123456789abcdef.20.0", kind: "narration", text: "E2E-S424-SUB-NARRATION" }],
  ...over,
});
const runningSnapshot = (run) => ({ available: true, omitted: 0, lines: [
  started("e.1", "build", now - 10_000),
  tool("aaaaaaaaaaaaaaaa.5.0", "Agent", "E2E-S424-AGENT-CALL", "running", { subagents: [run] }),
] });

// ── scenario bookkeeping and browser helpers ────────────────────────────────
const failures = [];
const check = createCheck({ failures });
let base;
const progressBlock = (page) => dock(page).locator("[data-pf-progress]").first();
const entries = (page) => progressBlock(page).locator('[data-pf-progress-kind="subagent"]');
const h = createHarness({
  viewport: { width: 1280, height: 900 },
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "en" },
  session: CHAT_SESSION,
  gotoTimeout: COLD_MS,
  ready: "E2E pf 424 host session",
  async setup(page) {
    page.__progress = { available: true, omitted: 0, lines: [] };
    page.__detail = {};
    page.__progressReads = 0;
    await mockPolyforge(page, {
      status,
      cards: () => ({ pfPresent: true, cards }),
      watch: () => watch,
      progress: () => { page.__progressReads += 1; return page.__progress; },
      "progress/tool": (url) => page.__detail[url.searchParams.get("id")] ?? { available: false, error: "unknown reference" },
    });
  },
});
const { errors, withPage } = h;
const waitFor = createWaitFor({ every: 100 });
const text = async (locator) => (await locator.innerText()).replace(/\s+/g, " ").trim();
async function openDetail(page) {
  await openDock(page);
  const card = dock(page).locator(`[data-pf-card-wi="${UI}"]`);
  await card.waitFor({ timeout: 30_000 });
  await card.click();
  await dock(page).getByRole("button", { name: /back to job list|deselect/i }).waitFor({ timeout: 10_000 });
  await progressBlock(page).waitFor({ timeout: UI_WAIT });
}
const json = async (path) => {
  const response = await fetch(`${base}${path}`, { signal: AbortSignal.timeout(30_000) });
  return { status: response.status, body: await response.json() };
};
const callWithSubagents = (body, name) => (body.lines ?? []).find((line) => line.kind === "tool" && line.tool?.name === name && line.subagents?.length);

try {
  ({ base } = await h.startServer({ name: "polyforge-424", logFile: join(work, "server.log"), env: { PI_WEB_DIST_DIR: process.env.PI_WEB_DIST_DIR,
    PI_CODING_AGENT_DIR: agentDir, POLYFORGE_DATA_DIR: dataRoot, PI_WEB_ALLOWED_ROOT_PREFIXES: work } }));
  await h.launchBrowser(() => chromium.launch());

  // AC3/AC5 (claude, read face): the real route attaches the recorded
  // subagent to its Agent call, and its own tool call opens by id.
  await check("S424-01", async () => {
    const { status: code, body } = await json(`${ROUTE}?wi=${CLAUDE_WI}`);
    assert.equal(code, 200);
    const call = callWithSubagents(body, "Agent");
    assert.ok(call, "the Agent call carries its subagent");
    const [run] = call.subagents;
    assert.equal(run.description, "Read note.txt first line");
    assert.equal(run.status, "done");
    assert.ok(run.durationMs > 0, "a finished subagent has its duration");
    const read = run.lines.find((line) => line.kind === "tool" && line.tool.name === "Read");
    assert.ok(read, "its Read call is a line");
    const detail = await json(`${DETAIL}?wi=${CLAUDE_WI}&id=${encodeURIComponent(read.id)}`);
    assert.equal(detail.body.available, true, "the subagent's call opens through the detail read");
    assert.ok(detail.body.tool.output.includes("SUBAGENT_PROBE_TOKEN_253"));
    assert.ok(!JSON.stringify(body).includes(work), "no answer names a path");
  });

  // AC2/AC3/AC5 (pi, read face): the recorded child conversation is the
  // subagent's lines — its bash calls paired with their results.
  await check("S424-02", async () => {
    const { status: code, body } = await json(`${ROUTE}?wi=${PI_WI}`);
    assert.equal(code, 200);
    const call = callWithSubagents(body, "subagent");
    assert.ok(call, "the pi subagent call carries the recorded child");
    const [run] = call.subagents;
    assert.equal(run.agent, "merge-conflict-reviewer");
    assert.equal(run.status, "done");
    const tools = run.lines.filter((line) => line.kind === "tool");
    assert.ok(tools.length >= 2 && tools.every((line) => line.tool.status === "ok"), "every child tool call is paired");
  });

  // AC3/AC5 (UI): the entry under its call — description, running, age,
  // tool count; collapsed; expanding shows its tool calls and narration,
  // and its tool card opens its detail.
  await check("S424-03", () => withPage(async (page) => {
    page.__progress = runningSnapshot(subagent());
    page.__detail["0123456789abcdef.10.0"] = { available: true, hasResult: true, tool: { name: "Read", argument: "E2E-S424-SUB-READ", status: "ok", input: "E2E-S424-SUB-INPUT", output: "E2E-S424-SUB-OUTPUT", isError: false } };
    await openDetail(page);
    const entry = await waitFor(async () => (await entries(page).count()) === 1 && entries(page).first(), "one subagent entry renders");
    const header = entry.locator("[data-pf-progress-subagent-toggle]");
    assert.equal(await header.getAttribute("aria-expanded"), "false", "collapsed by default");
    const collapsed = await text(header);
    for (const part of ["general-purpose", "E2E-S424-TASK read the notes", "running", "1 tool calls"]) assert.ok(collapsed.includes(part), `the header shows ${part}: ${collapsed}`);
    assert.match(collapsed, /\d+\.\ds/, `a running subagent shows its live age: ${collapsed}`);
    assert.ok(!(await text(entry)).includes("E2E-S424-SUB-NARRATION"), "its conversation is folded");
    await header.click();
    await waitFor(async () => (await text(entry)).includes("E2E-S424-SUB-NARRATION"), "the expansion shows its narration");
    const subTool = entry.locator('[data-pf-progress-kind="tool"]').first();
    assert.ok((await text(subTool)).includes("E2E-S424-SUB-READ"), "the expansion shows its tool call");
    await subTool.locator("button[aria-expanded]").first().click();
    await waitFor(async () => (await text(subTool)).includes("E2E-S424-SUB-OUTPUT"), "the subagent's tool card opens its detail");
  }));

  // AC5 (live refresh): the next poll grows and finishes the expanded
  // subagent in place — it stays open, gains its new call and reads done.
  await check("S424-04", () => withPage(async (page) => {
    page.__progress = runningSnapshot(subagent());
    await openDetail(page);
    const entry = await waitFor(async () => (await entries(page).count()) === 1 && entries(page).first(), "the entry renders");
    await entry.locator("[data-pf-progress-subagent-toggle]").click();
    const reads = page.__progressReads;
    page.__progress = runningSnapshot(subagent({
      status: "done", durationMs: 7_300,
      lines: [...subagent().lines, tool("0123456789abcdef.30.0", "Grep", "E2E-S424-SUB-GREP", "ok")],
    }));
    await waitFor(async () => page.__progressReads > reads && (await text(entry)).includes("E2E-S424-SUB-GREP"), "the poll adds the new call to the open entry", 10_000);
    const header = entry.locator("[data-pf-progress-subagent-toggle]");
    assert.equal(await header.getAttribute("aria-expanded"), "true", "the refresh keeps it open");
    assert.equal(await entry.getAttribute("data-pf-progress-subagent-status"), "done");
    const finished = await text(header);
    assert.ok(finished.includes("done") && finished.includes("7.3s") && finished.includes("2 tool calls"), finished);
  }));

  // AC5: a subagent still recorded as running in a block that ended (its
  // step died mid-call) reads stopped, without an invented age.
  await check("S424-05", () => withPage(async (page) => {
    page.__progress = { available: true, omitted: 0, lines: [
      started("e.1", "build", now - 20_000),
      tool("aaaaaaaaaaaaaaaa.5.0", "Agent", "E2E-S424-AGENT-CALL", "running", { subagents: [subagent()] }),
      done("e.2", "build", now - 11_000),
      started("e.3", "review", now - 10_000),
    ] };
    await openDetail(page);
    await dock(page).locator('[data-pf-progress-block-step="build"]').click();
    const entry = await waitFor(async () => (await entries(page).count()) === 1 && entries(page).first(), "the ended block's entry renders");
    assert.equal(await entry.getAttribute("data-pf-progress-subagent-status"), "stopped");
    const header = await text(entry.locator("[data-pf-progress-subagent-toggle]"));
    assert.ok(header.includes("stopped") && !/\d+\.\ds/.test(header), header);
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
if (errors.length) console.log(`browser errors: ${errors.slice(0, 3).join(" | ")}`);
if (failures.length) process.exitCode = 1;
