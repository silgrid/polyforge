// carries: components/ToolCallCard*, components/SafeMarkdownBody*, components/PolyforgeProgressToolCard*, app/api/polyforge/progress/tool/*
// carries: app/api/polyforge/polyforge-progress-tool-routes*
// — job-detail execution progress as structured rendering.
//
// Authored PRE-CODE against artifacts/spec.md (Acceptance criteria + `####
// Scenario` blocks); it never reads `code`'s implementation. It drives only
// the interfaces the spec and `artifacts/plan.md` pin: the existing Jobs tab
// (`[data-pf-card-wi]`, the deselect control), the read faces
// `GET /api/polyforge/progress?wi=` and the new
// `GET /api/polyforge/progress/tool?wi=&id=`, and the progress DOM the earlier
// suites already use (`[data-pf-progress]`,
// `[data-pf-progress-kind]`, `[data-pf-progress-block]`,
// `[data-pf-progress-block-toggle]`, `[data-pf-detail-scroll]`). Rendering
// assertions stay semantic (rendered markdown DOM, a bordered card shell with
// an `aria-expanded` header, an ellipsised single header line with the full
// text on `title`, an inline expansion with no inner scroller) so the suite
// does not invent the implementation's class names.
//
// The poll/detail route scenarios run the real handlers against a throwaway
// daemon data root; the rendering scenarios drive the documented wire shape
// through a stubbed progress read, as e2e/polyforge-238.mjs does. At the
// pre-implementation tree every kept scenario is assertion-level RED (flat
// text rows, whitespace-collapsed narration, no tool-detail endpoint, no
// light `tool` fields, no group-head duration/absorption). Each scenario prints
// `PASS S271-<n>` on success (its binding marker).
//
// R8.1's first-screen budget (S271-17) is asserted in the renderer's own
// scheduling unit, not a wall clock: the first paint is at most
// one PROGRESS_RENDER_CHUNK (200 lines, components/PolyforgePanel.tsx) and lands
// within 2 animation frames of the progress answer, no frame adds more than one
// chunk, and every one of the 2000 entries renders. Host load stretches the
// frames, never the count; the measured ms appear in the messages only. The
// suite stays in the isolated lane because it is proven load-sensitive on a
// shared host (S271-17's former 1 s figure read 1288 ms at loadavg 67):
// e2e/all: isolated
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { openPolyforgeDock as openDock, polyforgeDock as dock } from "./polyforge-dock.mjs";
import { COLD_MS } from "./timeouts.mjs";
import { createCheck, createHarness, createWaitFor, mockPolyforge } from "./harness.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
process.env.PI_WEB_DIST_DIR ||= ".next/e2e/polyforge-271";
if (process.env.PI_WEB_DIST_DIR.startsWith(".next/e2e/")) rmSync(join(root, process.env.PI_WEB_DIST_DIR), { recursive: true, force: true });
assert.ok(!existsSync(join(root, process.env.PI_WEB_DIST_DIR, "dev/lock")), "Run in a checkout without an active dev server");

const work = mkdtempSync(join(tmpdir(), "pi-web-pf-271-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const dataRoot = join(work, "data");
const CHAT_SESSION = "e2e-pf-271-session";
const R271 = "wi_S271Route01";
const UI = "wi_S271Ui0001";
const ROUTE = "/api/polyforge/progress";
const DETAIL = "/api/polyforge/progress/tool";
const UI_WAIT = 8_000;
const TOKEN = "SECRET271TOKEN";
mkdirSync(join(agentDir, "sessions", "e2e"), { recursive: true });
mkdirSync(project);
mkdirSync(join(dataRoot, "events"), { recursive: true });
const jsonl = (records) => records.map((record) => JSON.stringify(record)).join("\n") + "\n";

// ── chat fixture: the session transcript's own tool card (R2.3) ────────────
writeFileSync(join(agentDir, "sessions", "e2e", `2026-11-01T00-00-00-000Z_${CHAT_SESSION}.jsonl`), jsonl([
  { type: "session", version: 3, id: CHAT_SESSION, timestamp: "2026-11-01T00:00:00.000Z", cwd: project },
  { type: "message", id: "u1", parentId: null, timestamp: "2026-11-01T00:00:00.000Z", message: { role: "user", content: "E2E pf 271 host session" } },
  { type: "message", id: "call", parentId: "u1", timestamp: "2026-11-01T00:00:01.000Z", message: { role: "assistant", content: [
    { type: "text", text: "E2E-S271-CHAT-NARRATION" },
    { type: "toolCall", id: "chat-t1", name: "bash", arguments: { command: "echo E2E-S271-CHAT-TOOL" } },
  ] } },
  { type: "message", id: "result", parentId: "call", timestamp: "2026-11-01T00:00:02.000Z", message: { role: "toolResult", toolCallId: "chat-t1", toolName: "bash", isError: false, content: [{ type: "text", text: "E2E-S271-CHAT-RESULT" }] } },
]));

// ── UI fixtures: documented wire shapes of the existing reads ──────────────
const job = { wi: UI, slug: "task#271", status: "running", stepAgeSec: 5, idleAgeSec: 1, candidate: "pi:e2e/model", spawnN: 1 };
const status = { pfPresent: true, status: { queue: { running: 1, max: 4 }, jobs: [job], tasks: { running: 0, completed: 0 } } };
const cards = [{ job, goal: " progress rendering", startedAt: new Date().toISOString(), steps: [{ id: "code", state: "running", runs: 1 }], currentStep: "code", progress: { available: false, entries: [] } }];
const watch = { status: { wi: UI, slug: job.slug, workflowStatus: "running", currentStep: "code", updatedAt: new Date().toISOString(),
  steps: [{ name: "code", status: "in_progress", startedAt: new Date().toISOString() }], declared_steps: [{ id: "code" }], events: [] } };

const line = (id, kind, text, extra = {}) => ({ id, kind, text, ...extra });
const eventLine = (id, text, event, tone, at) => line(id, "event", text, { event, ...(tone ? { tone } : {}), ...(at !== undefined ? { at } : {}) });
const toolLine = (id, name, argument, tool) => line(id, "tool", argument ? `${name}: ${argument}` : name, { tool });
const snapshotBody = (lines, omitted = 0) => ({ available: true, lines, omitted });

// ── route fixture: a throwaway daemon data root with one real capsule ──────
const eventBase = Date.parse("2026-11-01T10:00:00Z");
const eventAt = (sec) => new Date(eventBase + sec * 1_000).toISOString();
const capsule = (wi) => join(work, "capsules", wi);
const ensureCapsule = (wi) => { const dir = capsule(wi); mkdirSync(join(dir, "runtime-state"), { recursive: true }); mkdirSync(join(dir, "session"), { recursive: true }); return dir; };
const MULTILINE = [
  "E2E-S271-MD-P1 with a paragraph.",
  "",
  "- E2E-S271-MD-ITEM-1",
  "- E2E-S271-MD-ITEM-2",
  "",
  "```js",
  "const e2e271 = 1;",
  "```",
  "",
  `  indented E2E-S271-MD-P2 token=${TOKEN}`,
  `E2E-S271-LONG-TAIL ${"L".repeat(1200)}`,
].join("\n");
const MASKED_MULTILINE = MULTILINE.replace(`token=${TOKEN}`, "token=***");
const LONG_ARG = `curl --token=${TOKEN} https://e2e.invalid/${"x".repeat(200)} E2E-S271-ARG-TAIL`;
const ARG_MASKED = `curl --token=*** https://e2e.invalid/${"x".repeat(200)} E2E-S271-ARG-TAIL`;
const spawn = (wi, atSec, stepId, spawnN, transcript, harness) => ({ at: eventAt(atSec), work_item: wi, step_id: stepId, spawn_n: spawnN, started_at: eventAt(atSec), transcript, harness });
const event = (wi, sec, kind, body = {}) => ({ at: eventAt(sec), kind, work_item: wi, body, seq: sec + 1 });

const routeDir = ensureCapsule(R271);
const spawnPi = join(routeDir, "session", "pi.jsonl");
const spawnClaude = join(routeDir, "session", "claude.jsonl");
writeFileSync(spawnPi, jsonl([
  { type: "session", version: 3, id: "s271", timestamp: eventAt(0), cwd: project },
  { type: "message", id: "n1", timestamp: eventAt(1), message: { role: "assistant", content: [{ type: "text", text: MULTILINE }] } },
  { type: "message", id: "th1", timestamp: eventAt(2), message: { role: "assistant", content: [{ type: "thinking", thinking: "E2E-S271-THINK-P1\n\nE2E-S271-THINK-P2" }] } },
  { type: "message", id: "tc1", timestamp: eventAt(3), message: { role: "assistant", content: [{ type: "toolCall", id: "call1", name: "bash", arguments: { command: LONG_ARG, extra: "E2E-S271-FULL-INPUT" } }] } },
  { type: "message", id: "tr1", timestamp: eventAt(4), message: { role: "toolResult", toolCallId: "call1", toolName: "bash", isError: false, content: [{ type: "text", text: `E2E-S271-FULL-RESULT ${"r".repeat(900)}` }] } },
  { type: "message", id: "tc2", timestamp: eventAt(5), message: { role: "assistant", content: [{ type: "toolCall", id: "call2", name: "read", arguments: { path: "E2E-S271-NO-RESULT" } }] } },
  { type: "message", id: "tc3", timestamp: eventAt(6), message: { role: "assistant", content: [{ type: "toolCall", id: "call3", name: "grep", arguments: { pattern: "E2E-S271-ERROR" } }] } },
  { type: "message", id: "tr3", timestamp: eventAt(8), message: { role: "toolResult", toolCallId: "call3", toolName: "grep", isError: true, content: [{ type: "text", text: "E2E-S271-ERROR-RESULT" }] } },
]));
writeFileSync(spawnClaude, jsonl([
  { type: "user", timestamp: eventAt(30), message: { role: "user", content: "E2E-S271-CLAUDE-USER" } },
  { type: "assistant", timestamp: eventAt(31), message: { role: "assistant", content: [
    { type: "text", text: "E2E-S271-CLAUDE-NARRATION" },
    { type: "tool_use", id: "tu1", name: "bash", input: { command: "echo E2E-S271-CLAUDE-TOOL", extra: "E2E-S271-CLAUDE-EXTRA" } },
  ] } },
  { type: "user", timestamp: eventAt(33), message: { role: "user", content: [{ type: "tool_result", tool_use_id: "tu1", is_error: false, content: [{ type: "text", text: "E2E-S271-CLAUDE-RESULT" }] }] } },
]));
writeFileSync(join(routeDir, "runtime-state", "spawn-journal.jsonl"), jsonl([
  spawn(R271, 0, "grill", 1, spawnPi, "pi"),
  spawn(R271, 30, "code", 1, spawnClaude, "claude"),
]));
writeFileSync(join(dataRoot, "events", `wi-${R271}.jsonl`), jsonl([
  event(R271, 0, "step_started", { step: "grill", spawn_n: 1, model: "pi:e2e/model" }),
  event(R271, 15, "step_done", { step: "grill", spawn_n: 1 }),
  event(R271, 30, "step_started", { step: "code", spawn_n: 1, model: "claude:e2e-model" }),
  event(R271, 45, "needs_input", { step: "code", spawn_n: 1 }),
  event(R271, 46, "rewind", { gate: "code-review", target: "code", spawn_n: 1 }),
  event(R271, 47, "gate_verdict", { step: "code", spawn_n: 1 }),
]));
const runtimeState = join(dataRoot, "runtime-state.json");
writeFileSync(runtimeState, JSON.stringify({ version: 1, jobs: [{ work_item_id: R271, capsule_dir: routeDir }] }));

// ── scenario bookkeeping and browser helpers ───────────────────────────────
const failures = [];
const check = createCheck({ failures });
let base;
const progressBlock = (page) => dock(page).locator("[data-pf-progress]").first();
const kindRows = (page, kind) => progressBlock(page).locator(`[data-pf-progress-kind="${kind}"]`);
const detailAnswer = (page, id, body, statusCode = 200) => { page.__detail.answers[id] = { status: statusCode, body }; };
const h = createHarness({
  viewport: { width: 1280, height: 900 },
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "en" },
  session: CHAT_SESSION,
  gotoTimeout: COLD_MS,
  ready: "E2E pf 271 host session",
  async setup(page) {
    page.__progress = snapshotBody([]);
    page.__detail = { answers: {} };
    page.__detailRequests = [];
    await mockPolyforge(page, {
      status,
      cards: () => ({ pfPresent: true, cards }),
      watch: () => watch,
      progress: () => page.__progress,
      "progress/tool": (url, route) => {
        const id = url.searchParams.get("id");
        page.__detailRequests.push(id);
        const answer = page.__detail.answers[id] ?? { status: 200, body: { available: false, error: "unknown reference" } };
        return route.fulfill({ status: answer.status, json: answer.body });
      },
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
async function rowByText(page, kind, needle) {
  return waitFor(async () => {
    const rows = kindRows(page, kind);
    for (let i = 0; i < await rows.count(); i += 1) { const row = rows.nth(i); if ((await row.innerText()).includes(needle)) return row; }
    return null;
  }, `a ${kind} row containing ${needle} renders`);
}
const toggle = (row) => row.locator("button[aria-expanded]").first();
async function expandRow(row, needle) {
  assert.equal(await toggle(row).count(), 1, "the tool card exposes one expandable header control");
  await toggle(row).click();
  await waitFor(async () => (await text(row)).includes(needle), `the expanded card shows ${needle}`);
}
// The card shell: the nearest ancestor of the entry (or the entry itself) that
// draws a bordered, rounded card. It returns null while the entry is a flat row.
const cardShell = (row, key) => row.evaluate((el, marker) => {
  let node = el;
  while (node && node !== document.body) {
    const style = getComputedStyle(node);
    if (parseFloat(style.borderTopWidth) > 0 && style.borderTopStyle !== "none" && parseFloat(style.borderTopLeftRadius) > 0) {
      node.setAttribute("data-e2e-card", marker);
      return { radius: style.borderTopLeftRadius, border: style.borderTopColor, background: style.backgroundColor, expandedHeader: node.querySelector("button[aria-expanded]") !== null };
    }
    node = node.parentElement;
  }
  return null;
}, key);
const markdownShape = (row) => row.evaluate((el) => ({
  paragraphs: el.querySelectorAll("p").length,
  listItems: el.querySelectorAll("li").length,
  codeBlock: Boolean(el.querySelector("pre code, pre")),
  italic: [el, ...el.querySelectorAll("*")].some((node) => getComputedStyle(node).fontStyle === "italic"),
  text: el.innerText,
  height: el.getBoundingClientRect().height,
}));
const titleOf = (locator) => locator.evaluate((el) => {
  let node = el;
  while (node && node !== document.body) { const title = node.getAttribute("title"); if (title) return title; node = node.parentElement; }
  return null;
});
const json = async (path, headers = {}) => {
  const response = await fetch(`${base}${path}`, { headers, signal: AbortSignal.timeout(30_000) });
  return { status: response.status, text: await response.text() };
};
const snapshot = async (wi) => {
  const response = await json(`${ROUTE}?wi=${encodeURIComponent(wi)}`);
  assert.equal(response.status, 200, `the progress read answers 200 for ${wi}`);
  return JSON.parse(response.text);
};

try {
  const logFile = join(work, "server.log");
  ({ base } = await h.startServer({ name: "polyforge-271", logFile, env: { PI_WEB_DIST_DIR: process.env.PI_WEB_DIST_DIR,
    PI_CODING_AGENT_DIR: agentDir, POLYFORGE_DATA_DIR: dataRoot, PI_WEB_ALLOWED_ROOT_PREFIXES: work } }));
  await h.launchBrowser(() => chromium.launch());

  await check("S271-01", () => withPage(async (page) => {
    page.__progress = snapshotBody([line("md1", "narration", MULTILINE)]);
    await openDetail(page);
    const row = await rowByText(page, "narration", "E2E-S271-MD-P1");
    const shape = await markdownShape(row);
    assert.ok(shape.paragraphs >= 2, `the entry renders as markdown paragraphs (${shape.paragraphs})`);
    assert.ok(shape.listItems >= 2, `the markdown list renders its items (${shape.listItems})`);
    assert.ok(shape.codeBlock, "the fenced code block renders as a code block");
    for (const marker of ["E2E-S271-MD-P1", "E2E-S271-MD-ITEM-2", "const e2e271 = 1;", "E2E-S271-MD-P2", "E2E-S271-LONG-TAIL"]) {
      assert.ok(shape.text.includes(marker), `the narration keeps ${marker}`);
    }
    assert.notEqual(await row.evaluate((el) => getComputedStyle(el).whiteSpace), "nowrap", "the narration line is not a single nowrap line");
  }));

  await check("S271-02", () => withPage(async (page) => {
    page.__progress = snapshotBody([line("th1", "narration", "E2E-S271-THINK-P1\n\nE2E-S271-THINK-P2", { variant: "thinking" }), line("md2", "narration", "E2E-S271-PLAIN")]);
    await openDetail(page);
    const row = await rowByText(page, "narration", "E2E-S271-THINK-P1");
    const shape = await markdownShape(row);
    assert.ok(shape.paragraphs >= 2, `thinking renders as markdown paragraphs (${shape.paragraphs})`);
    assert.ok(shape.italic, "the thinking treatment is italic");
    assert.ok(shape.text.includes("E2E-S271-THINK-P2"), "the whole thinking text is present");
    assert.ok(shape.height > 0, "the thinking entry is visible without a click");
    assert.equal(await row.locator('[aria-expanded="false"]').count(), 0, "thinking is not folded behind a click");
  }));

  await check("S271-03", async () => {
    const body = await snapshot(R271);
    const narration = (body.lines ?? []).find((entry) => entry.kind === "narration" && entry.text.includes("E2E-S271-MD-P1"));
    assert.ok(narration, "the narration entry is in the progress answer");
    assert.equal(narration.text, MASKED_MULTILINE, "the route keeps the narration's original whitespace and masks the credential");
    assert.ok(narration.text.includes("\n\n"), "blank lines survive the projection");
    assert.ok(narration.text.includes("  indented"), "the narration indentation survives");
    assert.ok(narration.text.length > 1200, `no per-line length cap is applied (${narration.text.length} chars)`);
    assert.ok(!JSON.stringify(body).includes(TOKEN), "the raw credential never leaves the read face");
  });

  await check("S271-04", () => withPage(async (page) => {
    page.__progress = snapshotBody([toolLine("card-h", "bash", ARG_MASKED, { name: "bash", argument: ARG_MASKED, status: "ok", durationMs: 1500 })]);
    await openDetail(page);
    const row = await rowByText(page, "tool", "curl --token=***");
    const collapsed = await text(row);
    assert.ok(collapsed.includes("bash"), `the card header names the tool: ${collapsed}`);
    assert.ok(collapsed.includes("curl --token=***"), `the header carries the masked primary argument: ${collapsed}`);
    assert.ok(!collapsed.includes("E2E-S271-ARG-TAIL"), `the collapsed header is ellipsised before the long tail: ${collapsed}`);
    assert.equal(await toggle(row).count(), 1, "the tool entry exposes one expandable header control");
    const full = await titleOf(toggle(row));
    assert.ok(full && full.includes("E2E-S271-ARG-TAIL"), `the full header text is available on title (${full})`);
    const header = await row.evaluate((el) => {
      for (const node of [...el.querySelectorAll("*"), el]) {
        const style = getComputedStyle(node);
        if ((node.textContent ?? "").includes("curl --token=***") && style.whiteSpace === "nowrap" && style.textOverflow === "ellipsis") return { text: node.textContent };
      }
      return null;
    });
    assert.ok(header, "the header preview is a single ellipsised line");
    assert.ok(header.text.length <= 120, `the visible header is capped at 120 chars (${header.text.length})`);
  }));

  await check("S271-05", () => withPage(async (page) => {
    page.__progress = snapshotBody([
      toolLine("st-ok", "bash", "echo E2E-S271-A1", { name: "bash", argument: "echo E2E-S271-A1", status: "ok", durationMs: 1500 }),
      toolLine("st-bad", "bash", "echo E2E-S271-A2", { name: "bash", argument: "echo E2E-S271-A2", status: "error", durationMs: 200 }),
      toolLine("st-run", "bash", "echo E2E-S271-A3", { name: "bash", argument: "echo E2E-S271-A3", status: "running" }),
    ]);
    await openDetail(page);
    const okRow = await rowByText(page, "tool", "E2E-S271-A1");
    const badRow = await rowByText(page, "tool", "E2E-S271-A2");
    const runRow = await rowByText(page, "tool", "E2E-S271-A3");
    for (const row of [okRow, badRow, runRow]) assert.equal(await toggle(row).count(), 1, "each tool entry exposes one card header control");
    const okText = await text(toggle(okRow));
    assert.match(okText, /\bok\b/, `a succeeded call shows ok: ${okText}`);
    assert.match(okText, /1\.5s/, `a succeeded call shows its duration: ${okText}`);
    const badText = await text(toggle(badRow));
    assert.match(badText, /\berror\b/, `a failed call shows error: ${badText}`);
    assert.match(badText, /0\.2s/, `the failed call shows its duration: ${badText}`);
    const runText = await text(toggle(runRow));
    assert.match(runText, /\brunning\b/, `a call without a result shows running: ${runText}`);
    assert.doesNotMatch(runText, /\d+(\.\d+)?(ms|s)\b/, `a call without timestamps shows no invented duration: ${runText}`);
  }));

  await check("S271-06", () => withPage(async (page) => {
    page.__progress = snapshotBody([
      toolLine("same-pi", "bash", "echo E2E-S271-PITOOL", { name: "bash", argument: "echo E2E-S271-PITOOL", status: "ok" }),
      toolLine("same-claude", "bash", "echo E2E-S271-CLAUDETOOL", { name: "bash", argument: "echo E2E-S271-CLAUDETOOL", status: "ok" }),
    ]);
    await openDetail(page);
    const pi = await cardShell(await rowByText(page, "tool", "E2E-S271-PITOOL"), "pi");
    const claude = await cardShell(await rowByText(page, "tool", "E2E-S271-CLAUDETOOL"), "claude");
    assert.ok(pi, "the pi tool entry renders as a bordered card");
    assert.ok(claude, "the claude tool entry renders as a bordered card");
    assert.deepEqual({ radius: claude.radius, border: claude.border, background: claude.background }, { radius: pi.radius, border: pi.border, background: pi.background }, "both harnesses use one card language");
    const chat = page.locator('[data-message-role="assistant"]').filter({ hasText: "E2E-S271-CHAT-TOOL" }).first();
    await chat.waitFor({ timeout: UI_WAIT });
    const transcript = await chat.evaluate((scope) => {
      for (const node of scope.querySelectorAll("*")) {
        if (!(node.textContent ?? "").includes("E2E-S271-CHAT-TOOL")) continue;
        let at = node;
        while (at && at !== document.body) {
          const style = getComputedStyle(at);
          if (parseFloat(style.borderTopWidth) > 0 && style.borderTopStyle !== "none" && parseFloat(style.borderTopLeftRadius) > 0) {
            return { radius: style.borderTopLeftRadius, border: style.borderTopColor, background: style.backgroundColor };
          }
          at = at.parentElement;
        }
      }
      return null;
    });
    assert.ok(transcript, "the session transcript's tool block renders a bordered card");
    assert.deepEqual({ radius: pi.radius, border: pi.border, background: pi.background }, transcript, "the progress card is the transcript's card shell (same border, tone and radius)");
  }));

  await check("S271-07", () => withPage(async (page) => {
    page.__progress = snapshotBody([toolLine("card-a", "bash", "echo E2E-S271-A", { name: "bash", argument: "echo E2E-S271-A", status: "ok", durationMs: 1000 })]);
    detailAnswer(page, "card-a", { available: true, hasResult: true, tool: { name: "bash", argument: "echo E2E-S271-A", status: "ok", durationMs: 1000, isError: false,
      input: `E2E-S271-FULL-ARG ${"i".repeat(3000)}`, output: `E2E-S271-FULL-OUT ${"o".repeat(6000)}` } });
    await openDetail(page);
    const row = await rowByText(page, "tool", "E2E-S271-A");
    await expandRow(row, "E2E-S271-FULL-OUT");
    const expanded = await text(row);
    assert.ok(expanded.includes("E2E-S271-FULL-ARG"), "the complete arguments are shown inline");
    assert.ok(expanded.includes("E2E-S271-FULL-OUT"), "the complete result is shown inline");
    assert.ok(expanded.length > 6000, `the expanded detail is uncapped (${expanded.length} chars)`);
    const scrollers = await row.evaluate((el) => [el, ...el.querySelectorAll("*")].filter((node) => /(auto|scroll)/.test(getComputedStyle(node).overflowY) && node.scrollHeight > node.clientHeight + 1).length);
    assert.equal(scrollers, 0, "the expansion adds no card-internal vertical scroller");
    assert.equal(await dock(page).locator("[data-pf-detail-scroll]").count(), 1, "the detail keeps its one scroll region");
  }));

  await check("S271-08", () => withPage(async (page) => {
    page.__progress = snapshotBody([toolLine("card-nr", "read", "E2E-S271-NORESULT", { name: "read", argument: "E2E-S271-NORESULT", status: "running" })]);
    detailAnswer(page, "card-nr", { available: true, hasResult: false, tool: { name: "read", argument: "E2E-S271-NORESULT", status: "running", isError: false, input: "E2E-S271-NORESULT-INPUT", output: null } });
    await openDetail(page);
    const row = await rowByText(page, "tool", "E2E-S271-NORESULT");
    await expandRow(row, "E2E-S271-NORESULT-INPUT");
    const expanded = await text(row);
    assert.match(expanded, /no result|not yet|pending|no output/i, `the result area states that there is no result yet: ${expanded}`);
    assert.ok(!expanded.includes("E2E-S271-FULL-OUT"), "no output text is fabricated");
  }));

  await check("S271-09", () => withPage(async (page) => {
    page.__progress = snapshotBody([
      line("keep", "narration", "E2E-S271-STREAM-KEPT"),
      toolLine("card-bad", "bash", "echo E2E-S271-BAD", { name: "bash", argument: "echo E2E-S271-BAD", status: "ok" }),
      toolLine("card-good", "bash", "echo E2E-S271-GOOD", { name: "bash", argument: "echo E2E-S271-GOOD", status: "ok" }),
    ]);
    detailAnswer(page, "card-bad", { available: false, error: "E2E-S271-UNAVAILABLE" });
    detailAnswer(page, "card-good", { available: true, hasResult: true, tool: { name: "bash", argument: "echo E2E-S271-GOOD", status: "ok", isError: false, input: "E2E-S271-GOOD-INPUT", output: "E2E-S271-GOOD-OUT" } });
    await openDetail(page);
    const bad = await rowByText(page, "tool", "E2E-S271-BAD");
    assert.equal(await toggle(bad).count(), 1, "the tool card exposes one expandable header control");
    await toggle(bad).click();
    await waitFor(async () => /unavailable|not available|no longer|failed/i.test(await text(bad)), "the failed detail read leaves an unavailable card");
    assert.ok(!(await text(bad)).includes("E2E-S271-GOOD-OUT"), "the unavailable card holds no other call's content");
    assert.ok((await text(await rowByText(page, "narration", "E2E-S271-STREAM-KEPT"))).includes("E2E-S271-STREAM-KEPT"), "the narration stays rendered");
    const good = await rowByText(page, "tool", "E2E-S271-GOOD");
    await expandRow(good, "E2E-S271-GOOD-OUT");
  }));

  await check("S271-10", async () => {
    const body = await snapshot(R271);
    const toolLineEntry = (body.lines ?? []).find((entry) => entry.kind === "tool" && entry.text.includes("E2E-S271-ARG-TAIL"));
    assert.ok(toolLineEntry, "the tool entry is in the poll answer");
    assert.ok(toolLineEntry.tool && typeof toolLineEntry.tool === "object", "the tool entry carries its light tool fields");
    assert.deepEqual(Object.keys(toolLineEntry.tool).filter((key) => !["name", "argument", "status", "durationMs"].includes(key)), [], `only the light tool fields ride the poll (${Object.keys(toolLineEntry.tool)})`);
    assert.equal(toolLineEntry.tool.name, "bash", "the poll names the tool");
    assert.equal(toolLineEntry.tool.argument, ARG_MASKED, "the poll carries the masked primary argument");
    assert.equal(toolLineEntry.tool.status, "ok", "the poll carries the status");
    assert.equal(toolLineEntry.tool.durationMs, 1000, "the poll carries the duration");
    const serialized = JSON.stringify(body);
    assert.ok(serialized.includes("E2E-S271-ARG-TAIL"), "the primary argument is the light field");
    assert.ok(!serialized.includes("E2E-S271-FULL-INPUT"), "the call's full input never rides the poll");
    assert.ok(!serialized.includes("E2E-S271-FULL-RESULT"), "the call's full output never rides the poll");
    const running = (body.lines ?? []).find((entry) => entry.kind === "tool" && entry.text.includes("E2E-S271-NO-RESULT"));
    assert.equal(running.tool.status, "running", "a call with no result is running in the poll");
    assert.equal(running.tool.durationMs, undefined, "a call without a result timestamp carries no duration");
    const failed = (body.lines ?? []).find((entry) => entry.kind === "tool" && entry.text.includes("E2E-S271-ERROR"));
    assert.equal(failed.tool.status, "error", "an error result is error in the poll");
    assert.equal(failed.tool.durationMs, 2000, "the error call's duration comes from its timestamps");
  });

  await check("S271-11", () => withPage(async (page) => {
    page.__progress = snapshotBody([
      toolLine("only-a", "bash", "echo E2E-S271-ONLY-A", { name: "bash", argument: "echo E2E-S271-ONLY-A", status: "ok" }),
      toolLine("only-b", "bash", "echo E2E-S271-ONLY-B", { name: "bash", argument: "echo E2E-S271-ONLY-B", status: "ok" }),
    ]);
    detailAnswer(page, "only-a", { available: true, hasResult: true, tool: { name: "bash", argument: "echo E2E-S271-ONLY-A", status: "ok", isError: false, input: "E2E-S271-A-IN", output: "E2E-S271-A-OUT" } });
    detailAnswer(page, "only-b", { available: true, hasResult: true, tool: { name: "bash", argument: "echo E2E-S271-ONLY-B", status: "ok", isError: false, input: "E2E-S271-B-IN", output: "E2E-S271-B-OUT" } });
    await openDetail(page);
    await rowByText(page, "tool", "E2E-S271-ONLY-B");
    assert.deepEqual(page.__detailRequests, [], "no tool detail is requested while the cards stay collapsed");
    const a = await rowByText(page, "tool", "E2E-S271-ONLY-A");
    await expandRow(a, "E2E-S271-A-OUT");
    assert.deepEqual(page.__detailRequests, ["only-a"], "expanding one card fetches exactly that call");
    const b = await rowByText(page, "tool", "E2E-S271-ONLY-B");
    await expandRow(b, "E2E-S271-B-OUT");
    assert.deepEqual(page.__detailRequests, ["only-a", "only-b"], "expanding the second card fetches exactly that call");
  }));

  await check("S271-12", async () => {
    const body = await snapshot(R271);
    const call = (body.lines ?? []).find((entry) => entry.kind === "tool" && entry.text.includes("E2E-S271-ARG-TAIL"));
    const id = encodeURIComponent(call.id);
    const query = `${DETAIL}?wi=${R271}&id=${id}`;
    const trusted = await json(query);
    assert.equal(trusted.status, 200, "the authenticated surface reaches the detail endpoint");
    const detail = JSON.parse(trusted.text);
    assert.equal(detail.available, true, "the reachable endpoint answers the call's detail");
    assert.ok(detail.tool && typeof detail.tool === "object", "the detail carries the tool payload");
    assert.ok(JSON.stringify(detail.tool.input ?? null).includes("E2E-S271-FULL-INPUT"), "the detail carries the call's complete input");
    assert.ok(JSON.stringify(detail.tool.output ?? null).includes("E2E-S271-FULL-RESULT"), "the detail carries the call's complete output");
    assert.ok(JSON.stringify(detail.tool.input ?? "").includes("token=***") || String(detail.tool.argument ?? "").includes("token=***"), "the detail is credential-masked");
    assert.ok(!trusted.text.includes(TOKEN), "the raw credential never leaves the detail endpoint");
    const crossSite = { origin: "http://evil.invalid", "sec-fetch-site": "cross-site" };
    const refused = await json(query, crossSite);
    const refusedProgress = await json(`${ROUTE}?wi=${R271}`, crossSite);
    assert.equal(refused.status, 403, "a request without pi-web's authentication is refused");
    assert.equal(refusedProgress.status, refused.status, "the new endpoint shares the gate that protects the existing progress route");
    assert.equal(refused.text, refusedProgress.text, "the refusal is the same gate answer, not a missing route");
    assert.ok(!refused.text.includes("E2E-S271-FULL-RESULT") && !refused.text.includes("E2E-S271-FULL-INPUT"), "a refused request returns no tool input or output");
  });

  await check("S271-13", async () => {
    const body = await snapshot(R271);
    const call = (body.lines ?? []).find((entry) => entry.kind === "tool" && entry.text.includes("E2E-S271-ARG-TAIL"));
    const malformedWi = await json(`${DETAIL}?wi=${encodeURIComponent("../../etc/passwd")}&id=${encodeURIComponent(call.id)}`);
    assert.equal(malformedWi.status, 400, "a malformed work-item reference is refused before any read");
    const malformedId = await json(`${DETAIL}?wi=${R271}&id=${encodeURIComponent("not-a-tool-id")}`);
    assert.equal(malformedId.status, 400, "a malformed tool reference is refused before any read");
    const unknown = await json(`${DETAIL}?wi=wi_S271Missing01&id=${encodeURIComponent(call.id)}`);
    assert.equal(unknown.status, 400, "a reference with neither index record nor event log is refused");
    renameSync(spawnPi, `${spawnPi}.gone`);
    try {
      const gone = await json(`${DETAIL}?wi=${R271}&id=${encodeURIComponent(call.id)}`);
      assert.equal(gone.status, 200, "an unreadable underlying read degrades instead of a 5xx");
      const degraded = JSON.parse(gone.text);
      assert.equal(degraded.available, false, "the degraded detail is explicitly unavailable");
      assert.ok(typeof degraded.error === "string" && degraded.error.length > 0, "the degraded detail names an error");
      assert.doesNotMatch(gone.text, new RegExp(work.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")), "no answer discloses the server's data root path");
      assert.doesNotMatch(gone.text, /\/capsules\/|\/session\//, "no answer discloses a server filesystem path");
    } finally {
      renameSync(`${spawnPi}.gone`, spawnPi);
    }
    for (const answer of [malformedWi, malformedId, unknown]) {
      assert.ok(answer.status < 500, `no reference failure answers 5xx (${answer.status})`);
      assert.doesNotMatch(answer.text, /\/capsules\/|\/session\//, "a refused reference names no path");
    }
  });

  await check("S271-14", () => withPage(async (page) => {
    page.__progress = snapshotBody([
      eventLine("lone-start", "00:00:01 ▶ grill #1 · pi:e2e/model", "step_started", "accent", eventBase),
      line("lone-body", "narration", "E2E-S271-LONE-BODY"),
      eventLine("lone-done", "00:00:16 ■ grill +15s", "step_done", "dim", eventBase + 15_000),
    ]);
    await openDetail(page);
    const blocks = progressBlock(page).locator("[data-pf-progress-block]");
    await waitFor(async () => (await blocks.count()) === 1, "a lone step/spawn block still renders as a block");
    const head = blocks.first().locator("[data-pf-progress-block-toggle]").first();
    const headText = await text(head);
    assert.ok(headText.includes("grill"), `the lone block's head names the step: ${headText}`);
    assert.match(headText, /[✓✕?▶⏸]/, `the head carries a status mark: ${headText}`);
    assert.match(headText, /(^|\D)15(\.0)?s(\D|$)/, `the head shows the duration derived from the lifecycle timestamps: ${headText}`);
    assert.equal(await head.getAttribute("aria-expanded"), "true", "the newest (lone) block starts expanded");
    await head.click();
    await waitFor(async () => (await head.getAttribute("aria-expanded")) === "false", "the lone block folds like any other block");
    assert.ok(!(await text(progressBlock(page))).includes("E2E-S271-LONE-BODY"), "a folded lone block hides its body");
  }));

  await check("S271-16", () => withPage(async (page) => {
    const at = eventBase;
    page.__progress = snapshotBody([
      eventLine("h1", "00:00:01 ▶ grill #1 · pi:e2e/model", "step_started", "accent", at),
      line("h1-body", "narration", "E2E-S271-HEAD-BODY"),
      eventLine("h1-done", "00:00:16 ■ grill +15s", "step_done", "dim", at + 15_000),
      eventLine("h2", "00:00:30 ▶ spec #1 · pi:e2e/model", "step_started", "accent"),
      line("h2-body", "narration", "E2E-S271-NO-TIMES"),
    ]);
    await openDetail(page);
    const blocks = progressBlock(page).locator("[data-pf-progress-block]");
    await waitFor(async () => (await blocks.count()) === 2, "two blocks render");
    const timedHead = blocks.nth(0).locator("[data-pf-progress-block-toggle]").first();
    await timedHead.click();
    await waitFor(async () => (await timedHead.getAttribute("aria-expanded")) === "true", "the older block expands");
    const absorbed = await titleOf(timedHead);
    assert.ok(absorbed && absorbed.includes("00:00:01 ▶ grill #1") && absorbed.includes("step_started"), `the head's title carries the absorbed start text and raw kind (${absorbed})`);
    assert.ok(absorbed.includes("00:00:16 ■ grill +15s") && absorbed.includes("step_done"), `the head's title carries the absorbed terminal text and raw kind (${absorbed})`);
    assert.equal(await blocks.nth(0).locator('[data-pf-progress-kind="event"]').count(), 0, "the absorbed lifecycle lines are not duplicated inside the block body");
    assert.equal(await blocks.nth(0).locator('[data-pf-progress-kind="narration"]').count(), 1, "the block body keeps its narration");
    const untimedText = await text(blocks.nth(1).locator("[data-pf-progress-block-toggle]").first());
    assert.doesNotMatch(untimedText, /\d+(\.\d+)?s\b/, `a head without timestamps shows no invented duration: ${untimedText}`);
    assert.equal(await blocks.nth(1).locator('[data-pf-progress-kind="event"]').count(), 0, "the second block's start line is absorbed too");
  }));

  await check("S271-17", () => withPage(async (page) => {
    const mixed = [];
    for (let i = 0; i < 700; i += 1) mixed.push(line(`p-n-${i}`, "narration", `E2E-S271-PERF-N-${String(i).padStart(4, "0")}\n\nsecond paragraph`));
    for (let i = 0; i < 1100; i += 1) mixed.push(toolLine(`p-t-${i}`, "bash", `echo E2E-S271-PERF-T-${String(i).padStart(4, "0")}`, { name: "bash", argument: `echo E2E-S271-PERF-T-${String(i).padStart(4, "0")}`, status: "ok", durationMs: 1000 }));
    for (let i = 0; i < 200; i += 1) mixed.push(eventLine(`p-e-${i}`, `00:00:0${i % 10} ? code #1 E2E-S271-PERF-E-${String(i).padStart(3, "0")}`, "needs_input", "amber"));
    assert.equal(mixed.length, 2000, "the fixture holds at least 2000 mixed entries");
    page.__progress = snapshotBody(mixed);
    await openDock(page);
    const card = dock(page).locator(`[data-pf-card-wi="${UI}"]`);
    await card.waitFor({ timeout: 30_000 });
    // In-page probe, armed before the click: an animation-frame
    // counter that samples the rendered entry count once per frame, a mutation
    // observer that stamps the frame of the first rendered entry, and a stamp of
    // the frame in which the (route-mocked) progress answer is parsed. It only
    // observes; the answer itself is unchanged.
    await page.evaluate((route) => {
      const probe = { frame: 0, answerFrame: null, firstFrame: null, firstCount: null, samples: [] };
      window.__pf271 = probe;
      const rendered = () => document.querySelectorAll("[data-pf-progress] [data-pf-progress-kind]").length;
      const tick = () => {
        probe.frame += 1;
        const count = rendered();
        const last = probe.samples.at(-1);
        if (count !== (last ? last.count : 0)) probe.samples.push({ frame: probe.frame, count });
        if (count < 2000) requestAnimationFrame(tick);
      };
      requestAnimationFrame(tick);
      const observer = new MutationObserver(() => {
        const count = rendered();
        if (count === 0) return;
        probe.firstFrame = probe.frame; probe.firstCount = count;
        observer.disconnect();
      });
      observer.observe(document.body, { childList: true, subtree: true });
      const fetchOriginal = window.fetch;
      window.fetch = async (...args) => {
        const response = await fetchOriginal(...args);
        const url = String(args[0] instanceof Request ? args[0].url : args[0]);
        if (new URL(url, location.href).pathname !== route) return response;
        const parse = response.json.bind(response);
        response.json = async () => {
          const body = await parse();
          if (probe.answerFrame === null && body?.lines?.length > 0) probe.answerFrame = probe.frame;
          return body;
        };
        return response;
      };
    }, ROUTE);
    const started = Date.now();
    await card.click();
    const firstMs = await waitFor(async () => {
      const seen = (await kindRows(page, "narration").count()) + (await kindRows(page, "tool").count()) + (await kindRows(page, "event").count());
      return seen > 0 ? Date.now() - started : null;
    }, "the first content is visible", 20_000);
    const total = await waitFor(async () => {
      const seen = await progressBlock(page).locator("[data-pf-progress-kind]").count();
      return seen === 2000 ? Date.now() - started : null;
    }, "every entry is rendered", 20_000);
    const probe = await waitFor(() => page.evaluate(() => {
      const seen = window.__pf271;
      return seen.samples.at(-1)?.count === 2000 && seen.firstFrame !== null && seen.answerFrame !== null ? seen : null;
    }), "the in-page probe saw the answer, the first paint and every entry", 12_000);
    const cadence = `answer frame ${probe.answerFrame}, first paint frame ${probe.firstFrame} with ${probe.firstCount}, per-frame counts ${probe.samples.map((sample) => `${sample.frame}:${sample.count}`).join(" ")}; first ${firstMs}ms, all ${total}ms`;
    // The first-screen budget in the renderer's own unit (PROGRESS_RENDER_CHUNK = 200).
    assert.ok(probe.firstCount > 0 && probe.firstCount <= 200, `the first paint shows at most one 200-line chunk, not the whole stream (${cadence})`);
    assert.ok(probe.firstFrame - probe.answerFrame <= 2, `the first content paints within 2 animation frames of the progress answer (${cadence})`);
    let previous = 0;
    for (const sample of probe.samples) {
      assert.ok(sample.count - previous <= 200, `no animation frame renders more than one 200-line chunk (${cadence})`);
      previous = sample.count;
    }
    assert.equal(await progressBlock(page).locator("[data-pf-progress-kind]").count(), 2000, "no entry is dropped to meet the budget");
    const first = await rowByText(page, "narration", "E2E-S271-PERF-N-0000");
    assert.ok((await markdownShape(first)).paragraphs >= 2, `the fixture's narration renders as markdown (first ${firstMs}ms, all ${total}ms)`);
    const tool = await rowByText(page, "tool", "E2E-S271-PERF-T-0000");
    assert.ok(await cardShell(tool, "perf"), `the fixture's tool entries render as cards (first ${firstMs}ms, all ${total}ms)`);
    assert.ok((await text(progressBlock(page))).includes("E2E-S271-PERF-T-1099"), "the newest tool entry is among the rendered rows");
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