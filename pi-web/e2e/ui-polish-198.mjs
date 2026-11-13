// carries: components/MachineLoadSection*, components/DebugPanel*, app/globals.css
// — pi-web UI polish batch acceptance pass (five items).
//
// Authored PRE-CODE from artifacts/spec.md (Requirements + `#### Scenario`)
// and never from `code`'s implementation. Every locator drives behaviour
// through surfaces that already exist on base 4b000b22 (session fixtures,
// the Session-info popover, the Debug panel, the chat's floating button, the
// Polyforge config page and Jobs dock) and matches new behaviour only through
// user-visible text or the spec's geometry predicates. On 4b000b22 each
// scenario is assertion-level red: the popover still mounts machine load,
// the Debug section sits above `.debug-cols`, the toolbar still owns ↻, the
// floating fetch form does not exist, the config badge is on its own row, the
// Jobs track is 112px, the card status is its own row and progress is 48px.
//
// Each passing scenario prints its own `PASS S<n>` line (the verification
// marker); on the baseline it prints `FAIL S<n>` and no marker matches.
//
//   node e2e/ui-polish-198.mjs
//   npm run test:e2e:all -- ui-polish-198.mjs
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { createCheck, createHarness, createWaitFor } from "./harness.mjs";
import { openPolyforgeDock, settlePolyforgeToggle } from "./polyforge-dock.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
process.env.PI_WEB_DIST_DIR ||= ".next/e2e/ui-polish-198";
if (process.env.PI_WEB_DIST_DIR.startsWith(".next/e2e/")) {
  rmSync(join(root, process.env.PI_WEB_DIST_DIR), { recursive: true, force: true });
}
assert.ok(!existsSync(join(root, process.env.PI_WEB_DIST_DIR || ".next", "dev/lock")), "Run in a checkout without an active dev server");

const work = mkdtempSync(join(tmpdir(), "pi-web-ui-polish-198-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
mkdirSync(join(agentDir, "sessions", "e2e"), { recursive: true });
mkdirSync(project, { recursive: true });
const TS = "2026-10-31T00:00:00.000Z";
const MAIN = "e2e-polish-main";
const SCROLL = "e2e-polish-scroll";
const writeSession = (id, messages) => writeFileSync(join(agentDir, "sessions", "e2e", `${id}.jsonl`), [
  { type: "session", version: 3, id, timestamp: TS, cwd: project },
  ...messages.map((message, index) => ({ type: "message", id: `m${index}`, parentId: index ? `m${index - 1}` : null, timestamp: TS, message })),
].map((entry) => JSON.stringify(entry)).join("\n") + "\n");
writeSession(MAIN, [
  { role: "user", content: "E2E ui polish host session" },
  { role: "assistant", content: "E2E ui polish reply" },
]);
writeSession(SCROLL, Array.from({ length: 160 }, (_, index) => ({
  role: index % 2 ? "assistant" : "user",
  content: `SCROLL message ${String(index).padStart(3, "0")}`,
})));

// S12 (owner answer s198-code-split-pane-s12): a second long saved session for the right split pane.
const RIGHT = "e2e-polish-right";
writeSession(RIGHT, Array.from({ length: 160 }, (_, index) => ({
  role: index % 2 ? "assistant" : "user",
  content: `RIGHT message ${String(index).padStart(3, "0")}`,
})));

const BARRIER = "/__e2e_request_barrier";
const ML = { cpu: "12.3%", cores: "8", loadavg: "0.42 / 0.51 / 0.63", memory: "5.2 / 16.0 GiB" };
const ML_VALUES = Object.values(ML);
const mlPayload = () => ({ metrics: { cpuPercent: 12.3, coreCount: 8, loadavg: [0.42, 0.51, 0.63], memoryUsedBytes: 5583457485, memoryTotalBytes: 17179869184 } });
/** 150.5px measured on 4b000b22 with this suite's card fixture and 460px dock. */
const CARD_HEIGHT_BASE_460 = 150.5;

const failures = [];
const check = createCheck({ failures, fail: ([id, title], text, error) => {
  console.log(`FAIL ${id}: ${text}`);
  console.error(`  ${title}: ${String(error?.message ?? error)}`);
} });

const openContexts = new Set();
const visible = (locator) => locator.first().isVisible().catch(() => false);
const waitFor = createWaitFor({ ms: 15_000, every: 100 });

const h = createHarness({
  viewport: { width: 1440, height: 900 },
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "en", "pi-web:session-age-filter-days": "0" },
  timeout: 20_000,
  errors: [],
  session: MAIN,
  marker: "E2E ui polish reply",
  async setup(page, o) {
    const context = page.context();
    openContexts.add(context);
    const state = o.state = { context, page, requests: [], mlHits: 0, mlMode: "ok" };
    page.on("request", (request) => {
      const url = new URL(request.url());
      state.requests.push(url.pathname + url.search);
      if (/\/api\/machine-load$/.test(url.pathname)) state.mlHits += 1;
    });
    page.on("pageerror", (error) => { state.pageError = error.message; });
    page.on("console", (event) => { if (event.type() === "error") state.consoleError = event.text(); });
    await page.route((url) => url.pathname === BARRIER, (route) => route.fulfill({ status: 204 }));
    page.route(/\/api\/machine-load(\?|$)/, (route) => state.mlMode === "null-metrics"
      ? route.fulfill({ json: { metrics: null } })
      : route.fulfill({ json: mlPayload() }));
    for (const [pattern, handler] of o.routes ?? []) await page.route(pattern, handler);
  },
  ready: (page, { marker }, timeout) => page.getByText(marker, { exact: true }).first().waitFor({ state: "attached", timeout }),
  async after(page, { clock, state }) {
    if (clock) {
      await page.clock.install({ time: new Date() });
      await page.clock.pauseAt(new Date(Date.now() + 2000));
    }
    return state;
  },
});
const { openPage } = h;
async function closePage(state) {
  openContexts.delete(state.context);
  await state.context.close().catch(() => {});
}
const openDebug = async (state) => {
  const button = state.page.locator('button[aria-label="Debug"]:visible').first();
  await button.click();
  await state.page.locator(".debug-panel").first().waitFor({ state: "visible", timeout: 20_000 });
};
const closeDebug = async (state) => {
  await state.page.locator('button[aria-label="Debug"]:visible').first().click();
  await state.page.locator(".debug-panel").first().waitFor({ state: "hidden", timeout: 20_000 });
};
const machineSection = (state) => state.page.evaluate((values) => {
  const holds = (node) => (node.innerText ?? "").includes("Machine load") && values.every((value) => (node.innerText ?? "").includes(value));
  const holders = [...document.querySelectorAll("*")].filter(holds);
  if (holders.length === 0) return null;
  const depth = (node) => { let level = 0; for (let current = node; current; current = current.parentElement) level += 1; return level; };
  let scope = holders.reduce((best, node) => (depth(node) > depth(best) ? node : best));
  const grid = document.querySelector(".debug-cols");
  // Every ancestor up to <html> holds the text: stop below the column grid (owner answer s198-code-authored-s1-s5).
  while (scope.parentElement && scope.parentElement !== grid && holds(scope.parentElement)) scope = scope.parentElement;
  const box = (node) => { const r = node?.getBoundingClientRect(); return r ? { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width } : null; };
  const scopeBox = box(scope);
  const gridBox = grid?.getBoundingClientRect();
  const style = grid ? getComputedStyle(grid) : null;
  const innerLeft = gridBox && style ? gridBox.left + parseFloat(style.paddingLeft) + parseFloat(style.borderLeftWidth) : null;
  const innerRight = gridBox && style ? gridBox.right - parseFloat(style.paddingRight) - parseFloat(style.borderRightWidth) : null;
  const load = document.querySelector(".debug-load");
  const section = document.querySelector(".machine-load-section");
  const scroll = document.querySelector(".debug-panel-scroll");
  const columns = [...document.querySelectorAll(".debug-col")];
  return {
    found: true,
    inGrid: Boolean(grid?.contains(scope)),
    text: scope.innerText ?? "",
    left: scopeBox.left, right: scopeBox.right, top: scopeBox.top,
    gridLeft: innerLeft, gridRight: innerRight,
    // The computed track areas, an oracle this suite never restates as a stylesheet regex.
    areas: style?.gridTemplateAreas ?? null,
    panelWidth: document.querySelector(".debug-panel")?.getBoundingClientRect().width ?? null,
    columnTops: columns.map((column) => column.getBoundingClientRect().top),
    columnBottoms: columns.map((column) => column.getBoundingClientRect().bottom),
    load: box(load),
    section: box(section),
    sessions: box(document.querySelector(".debug-col-sessions")),
    loadLeftRule: load ? parseFloat(getComputedStyle(load).borderLeftWidth) : null,
    sectionTopRule: section ? parseFloat(getComputedStyle(section).borderTopWidth) : null,
    scrollOverflow: scroll ? scroll.scrollWidth - scroll.clientWidth : null,
  };
}, ML_VALUES);

async function scrollList(page, where) {
  await page.evaluate((target) => {
    const list = document.querySelector('[data-chat-focused="true"]') ?? document.querySelector(".scrollbar-subtle.overflow-y-auto");
    if (!list) throw new Error("no chat scroll list");
    if (target === "top") list.scrollTop = 0;
    else if (target === "bottom") list.scrollTop = list.scrollHeight;
    else list.scrollTop = Math.max(0, Math.floor((list.scrollHeight - list.clientHeight) / 2));
  }, where);
}
async function listAtBottom(page) {
  return page.evaluate(() => {
    const list = document.querySelector('[data-chat-focused="true"]') ?? document.querySelector(".scrollbar-subtle.overflow-y-auto");
    return Boolean(list) && list.scrollTop + list.clientHeight >= list.scrollHeight - 6;
  });
}
const floating = (page) => page.locator(".chat-scroll-to-bottom").first();
const forceReads = (state, since = 0) => state.requests.filter((request, index) => index >= since && request.startsWith(`/api/sessions/${SCROLL}?`) && request.includes("force=1")).length;
const forceIndex = (state) => state.requests.length;

// Polyforge config-page fixture (same shape as e2e/polyforge-config-layout.mjs).
const WS = `[workspace."${project}"]`;
const CONFIG = {
  pfPresent: true, pfVersion: "v2.20.11", capable: true, file: { sha256: "e2e-cli", exists: true }, mapped: true,
  daemonReachable: true, reload: { pending: [], overridden: [] }, workspaceFile: { sha256: "e2e-yaml", exists: true },
  items: [
    { key: "server", value: "http://127.0.0.1:1", source: "file", class: "restart-only" },
    { key: WS, value: "e2e-project", source: "file", class: "restart-only", section: true },
    { key: "step_timeout", value: "30m0s", source: "file", class: "live", schema: { kind: "duration" }, daemon: { value: "30m0s" } },
    { key: "stall_window", value: "10m0s", source: "file", class: "live", schema: { kind: "duration" }, daemon: { value: "10m0s" } },
  ],
  steps: [], steps_complete: true,
};
const json = (body) => ({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
const configRoutes = [
  ["**/api/polyforge/config/history", (route) => route.fulfill(json({ entries: [] }))],
  ["**/api/polyforge/config?*", (route) => route.fulfill(json(CONFIG))],
  ["**/api/polyforge/detect*", (route) => route.fulfill(json({ pfPresent: true, pfVersion: "v2.20.11", pfVersionBelowMinimum: false, line: "internal", piSkillInstalled: true, piSkillEnabled: true, claudeSkillsSymlink: true, claudeSessionStartHook: true, daemon: { reachable: true } }))],
  ["**/api/plugins?*", (route) => route.fulfill(json({ packages: [{ scope: "global", packageName: "polyforge", source: "/opt/polyforge" }] }))],
];

// Polyforge Jobs fixture (drive the one card route the tab reads).
const GOAL = `E2E-GOAL-START-${"G".repeat(40)}-GOALTAIL`;
const JOBS = [
  { wi: "wi_E2E198A", slug: "task#9181", status: "running", stepAgeSec: 5, idleAgeSec: 1, candidate: "pi:e2e/model", spawnN: 1 },
  { wi: "wi_E2E198B", slug: "task#9182", status: "needs_input", stepAgeSec: 6, idleAgeSec: 2, candidate: "claude:e2e/model", spawnN: 1 },
];
// one card per status, for the frame-colour and status-text
// oracle; the four loud states must be pairwise distinct and distinct from the
// one shared quiet gray (queued/paused/cancelled).
const STATUS_JOBS = [
  JOBS[0], JOBS[1],
  { wi: "wi_E2E198C", slug: "task#9183", status: "completed", stepAgeSec: 7, idleAgeSec: 3, candidate: "pi:e2e/model", spawnN: 1 },
  { wi: "wi_E2E198D", slug: "task#9184", status: "failed", stepAgeSec: 8, idleAgeSec: 4, candidate: "pi:e2e/model", spawnN: 1 },
  { wi: "wi_E2E198E", slug: "task#9185", status: "queued", stepAgeSec: 9, idleAgeSec: 5, candidate: "pi:e2e/model", spawnN: 1 },
  { wi: "wi_E2E198F", slug: "task#9186", status: "paused", stepAgeSec: 10, idleAgeSec: 6, candidate: "pi:e2e/model", spawnN: 1 },
  { wi: "wi_E2E198G", slug: "task#9187", status: "cancelled", stepAgeSec: 11, idleAgeSec: 7, candidate: "pi:e2e/model", spawnN: 1 },
];
const CARD = (job) => ({
  job, goal: job.status === "running" ? GOAL : "E2E-B-GOAL", startedAt: TS,
  steps: [], progress: { available: true, entries: Array.from({ length: 12 }, (_, index) => ({ id: `${job.wi}-${index}`, text: `NARRATION-${job.wi}-${index}` })) },
});
const OPS = { queue: { running: 2, max: 4 }, jobs: JOBS, tasks: { running: 0, completed: 0 } };
// the Jobs row also shows a feed-only card for every work item
// the decision feed names without a live job, so the fixture answers the feed
// read (empty) — the host's own pf feed never adds cards to the measured row.
const EMPTY_FEED = ["**/api/polyforge/decisions", (route) => route.fulfill(json({ pfPresent: true, feed: { decisions: [], attention: 0 } }))];
const jobsRoutes = [
  EMPTY_FEED,
  ["**/api/polyforge/status", (route) => route.fulfill(json({ pfPresent: true, status: OPS }))],
  ["**/api/polyforge/cards*", (route) => route.fulfill(json({ pfPresent: true, cards: JOBS.map(CARD) }))],
  ["**/api/polyforge/messages", (route) => route.fulfill(json({ pfPresent: true, capable: true, conversations: [], contacts: [] }))],
];
const statusRoutes = [
  EMPTY_FEED,
  ["**/api/polyforge/status", (route) => route.fulfill(json({ pfPresent: true, status: { ...OPS, jobs: STATUS_JOBS } }))],
  ["**/api/polyforge/cards*", (route) => route.fulfill(json({ pfPresent: true, cards: STATUS_JOBS.map(CARD) }))],
  ["**/api/polyforge/messages", (route) => route.fulfill(json({ pfPresent: true, capable: true, conversations: [], contacts: [] }))],
];
async function openJobsDock(page) {
  const dock = await openPolyforgeDock(page);
  await page.locator("[data-pf-card-wi]").first().waitFor({ timeout: 30_000 });
  return dock;
}
const cardNode = (page, wi) => page.locator(`[data-pf-card-wi="${wi}"]`).first();
async function cardGeometry(page, wi, slug, statusText) {
  return cardNode(page, wi).evaluate((el, { slugText, status }) => {
    const leaf = (text) => [...el.querySelectorAll("*")].find((node) => node.children.length === 0 && (node.textContent ?? "").trim() === text) ?? null;
    const box = (node) => { const r = node?.getBoundingClientRect(); return r ? { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height } : null; };
    const style = getComputedStyle(el);
    const contentRight = el.getBoundingClientRect().right - parseFloat(style.paddingRight);
    const slugNode = leaf(slugText);
    // the status element carries its full status text as its
    // title (a running card's visible text is the duration alone), so match by
    // the title's prefix instead of the visible text.
    const statusNode = [...el.querySelectorAll("[title]")].find((node) => (node.getAttribute("title") ?? "").startsWith(status)) ?? null;
    const goalNode = [...el.querySelectorAll("[title]")].find((node) => (node.getAttribute("title") ?? "").startsWith("E2E-")) ?? null;
    let progress = null;
    for (const child of [el, ...el.querySelectorAll("*")]) {
      const s = getComputedStyle(child);
      if (/(auto|scroll)/.test(s.overflowY) && child.scrollHeight > child.clientHeight + 8) {
        progress = { height: child.clientHeight, scrollHeight: child.scrollHeight };
        break;
      }
    }
    return { card: box(el), contentRight, slug: box(slugNode), status: box(statusNode), goal: box(goalNode), progress,
      statusText: (statusNode?.textContent ?? "").trim(), statusTitle: statusNode?.getAttribute("title") ?? "", aria: el.getAttribute("aria-label") ?? "" };
  }, { slugText: slug, status: statusText });
}

try {
  await h.startServer({ name: "ui-polish-198", logFile: join(work, "server.log"),
    env: { HOME: work, PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, PI_WEB_DIST_DIR: process.env.PI_WEB_DIST_DIR } });
  await h.launchBrowser(() => chromium.launch());

  // Warm the fixture page once so the per-scenario waits do not pay a cold compile.
  {
    const warm = await openPage();
    await closePage(warm);
  }

  // S1 — spec Req 1 / AC 1: the Session-info popover no longer carries machine load.
  await check("S1", "Session-info popover renders no machine load and issues no request", async () => {
    const state = await openPage({ session: MAIN, marker: "E2E ui polish reply" });
    try {
      const before = state.mlHits;
      await state.page.getByRole("button", { name: /^(Session info|会话信息)$/ }).first().click();
      await state.page.locator(".session-info-popover").first().waitFor({ state: "visible", timeout: 20_000 });
      await state.page.evaluate(async (url) => { await fetch(url, { cache: "no-store" }); }, BARRIER);
      assert.equal(state.mlHits, before, `opening the popover issued /api/machine-load (${state.mlHits - before})`);
      const popover = await state.page.locator(".session-info-popover").first().innerText();
      assert.ok(!popover.includes("Machine load"), "the popover still has a Machine load heading");
      // The bare cores value "8" is skipped: the popover's session path (/tmp/pi-web-ui-polish-198-…) holds an 8.
      for (const value of ML_VALUES.filter((item) => item !== ML.cores)) assert.ok(!popover.includes(value), `the popover still renders ${value}`);
    } finally { await closePage(state); }
  });

  // S2 — spec Req 2 / AC 2, owner-intended change (the load is no longer a full-width last
  // row): on a wide panel it is the grid's narrow rightmost column, behind the same 20px gutter + 20px
  // padding + left rule the server and sessions columns use, its top on their tops and its right edge
  // on the grid's, with no top rule of its own; between the four-column width (a measured 1412px panel)
  // and 760px it is the right column under the sessions, that top rule back as the separator; on a
  // phone it stays the last block of the stack, full width. Nothing scrolls horizontally, and the
  // block stays narrow instead of stretching with the panel.
  await check("S2", "Debug machine load is the narrow rightmost column at wide, under the sessions in the middle and last on a phone", async () => {
    const state = await openPage({ session: MAIN, marker: "E2E ui polish reply" });
    try {
      await openDebug(state);
      await waitFor(() => state.mlHits >= 1, "the Debug panel's first machine-load request");
      await waitFor(async () => (await machineSection(state))?.text.includes(ML.memory), "the section's values");
      const measured = [];
      // 1800px: with the 260px sidebar the panel is ~1540px, above the 1412px four-column width;
      // 1440px leaves a ~1180px (middle) panel; 700px is the phone stack.
      for (const width of [1800, 1440, 700]) {
        await state.page.setViewportSize({ width, height: 900 });
        await state.page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
        const s = await machineSection(state);
        assert.ok(s, `no Machine load section at ${width}px`);
        assert.ok(s.inGrid, `at ${width}px the section is outside .debug-cols`);
        assert.ok(s.scrollOverflow <= 1, `at ${width}px .debug-panel-scroll scrolls horizontally by ${s.scrollOverflow}px`);
        assert.ok(Math.abs(s.right - s.gridRight) <= 1, `at ${width}px the load block's right edge ${s.right} is not on the grid edge ${s.gridRight}`);
        const panel = s.panelWidth;
        if (panel >= 1412) {
          assert.equal(s.areas, `"page server sessions load"`, `the wide layout's tracks: ${s.areas}`);
          assert.ok(Math.abs(s.load.top - Math.min(...s.columnTops)) <= 1, `the load's top ${s.load.top} is not on the columns' tops ${Math.min(...s.columnTops)}`);
          assert.ok(s.load.left >= s.sessions.right + 19, `the load column starts ${s.load.left - s.sessions.right}px after the sessions' right edge, not behind the 20px gutter`);
          assert.ok(Math.abs(s.load.left - s.sessions.right - 20) <= 1, `the load's gutter measures ${s.load.left - s.sessions.right}px`);
          assert.ok(Math.abs(s.section.left - s.sessions.right - 41) <= 1, `the load's rule and padding measure ${s.section.left - s.sessions.right}px, not 41`);
          assert.equal(s.loadLeftRule, 1, "the load column carries the left rule the server and sessions columns use");
          assert.equal(s.sectionTopRule, 0, "the load keeps a top rule on a wide panel, where the left rule separates it");
          assert.ok(s.load.width <= 281, `the load column stretches to ${s.load.width}px instead of staying narrow (<=280px)`);
          assert.ok(s.section.width >= 190 && s.section.width <= 241, `the load block measures ${s.section.width}px, not the narrow 200-240px`);
        } else if (panel > 760) {
          assert.equal(s.areas, `"page server" "page sessions" "page load"`, `the middle layout's tracks: ${s.areas}`);
          assert.ok(Math.abs(s.load.left - s.sessions.left) <= 1, `the load's left edge ${s.load.left} is not on the sessions' ${s.sessions.left}`);
          assert.ok(s.load.top >= s.sessions.bottom - 1, `the load's top ${s.load.top} is above the sessions' bottom ${s.sessions.bottom}`);
          assert.ok(Math.abs(s.load.right - s.sessions.right) <= 1, `the load's right edge ${s.load.right} is not on the sessions' ${s.sessions.right}`);
          assert.equal(s.sectionTopRule, 1, "the load under the sessions has no horizontal separator");
        } else {
          assert.equal(s.areas, `"page" "server" "sessions" "load"`, `the phone layout's tracks: ${s.areas}`);
          assert.ok(s.load.top >= Math.max(...s.columnBottoms) - 1, `the load's top ${s.load.top} is above a column bottom ${Math.max(...s.columnBottoms)}`);
          assert.ok(Math.abs(s.load.left - s.gridLeft) <= 1, `the load's left edge ${s.load.left} is not on the grid edge ${s.gridLeft}`);
          assert.equal(s.sectionTopRule, 1, "the phone stack's last block lost its top rule");
          assert.equal(s.loadLeftRule, 0, "the phone stack's last block keeps a left rule");
        }
        measured.push({ width, panel: Math.round(panel) });
      }
      assert.ok(measured[0].panel >= 1412, `the wide case measured ${measured[0].panel}px, not above the four-column width: ${JSON.stringify(measured)}`);
      assert.ok(measured[1].panel > 760 && measured[1].panel < 1412, `the middle case measured ${measured[1].panel}px, not 761..1411: ${JSON.stringify(measured)}`);
      assert.ok(measured[2].panel <= 760, `the narrow case measured ${measured[2].panel}px, not <=760: ${JSON.stringify(measured)}`);
    } finally { await closePage(state); }
  });

  // S3 — spec Req 3 / AC 3: the moved Debug mount keeps one fetch on open, a 2 s
  // tick, zero closed requests, last-good on a failed tick and one refetch on reopen.
  await check("S3", "Machine-load polling survives the move: open, tick, closed, failed tick, reopen", async () => {
    const state = await openPage({ session: MAIN, marker: "E2E ui polish reply", clock: true });
    try {
      await openDebug(state);
      await waitFor(() => state.mlHits === 1, "exactly one request on open");
      await waitFor(async () => (await machineSection(state))?.inGrid, "the moved section inside .debug-cols");
      await waitFor(async () => (await machineSection(state))?.text.includes(ML.memory), "the first values");
      await state.page.clock.runFor(2000);
      await waitFor(() => state.mlHits === 2, "one request per 2 s tick");
      const good = (await machineSection(state)).text;
      state.mlMode = "null-metrics";
      await state.page.clock.runFor(2000);
      await waitFor(() => state.mlHits === 3, "the failed tick's request");
      assert.equal((await machineSection(state)).text, good, "a failed tick flashed a placeholder instead of keeping the last good values");
      await closeDebug(state);
      const closed = state.mlHits;
      await state.page.clock.runFor(6000);
      assert.equal(state.mlHits, closed, `a closed panel issued ${state.mlHits - closed} request(s) over three ticks`);
      state.mlMode = "ok";
      await openDebug(state);
      await waitFor(() => state.mlHits === closed + 1, "exactly one immediate request on reopen");
    } finally { await closePage(state); }
  });

  // S4 — spec Req 4 / AC 4: no ↻ in the desktop top bar, phone toolbar or More overlay.
  await check("S4", "No manual-fetch control remains in the toolbar or the phone More overlay", async () => {
    const toolbarRefresh = (page) => page.evaluate(() => {
      const isVisible = (el) => Boolean(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
      const text = (el) => `${el.innerText ?? ""} ${el.getAttribute("aria-label") ?? ""} ${el.getAttribute("title") ?? ""}`;
      const stats = document.querySelector('button[data-mobile-toolbar-stats="true"]')
        ?? [...document.querySelectorAll("button")].find((b) => /Session info|会话信息/.test(b.getAttribute("aria-label") ?? ""));
      let scope = stats;
      for (let depth = 0; scope && depth < 4; depth += 1) {
        // The chat's floating button (the spec's ↻ at the tail) is not a toolbar control.
        const found = [...scope.querySelectorAll("button")].filter((b) => b !== stats && isVisible(b) && !b.classList.contains("chat-scroll-to-bottom")).find((b) => /↻|⟳|Fetch latest|刷新最新|刷新|拉取/.test(text(b)));
        if (found) return text(found).trim();
        scope = scope.parentElement;
      }
      return null;
    });
    const desktop = await openPage({ session: MAIN });
    try {
      assert.equal(await toolbarRefresh(desktop.page), null, "the desktop top bar still offers a manual-fetch control");
    } finally { await closePage(desktop); }
    const phone = await openPage({ session: MAIN, viewport: { width: 390, height: 844 } });
    try {
      assert.equal(await toolbarRefresh(phone.page), null, "the phone toolbar still offers a manual-fetch control");
      // The resident Polyforge toggle settles (bounded) before ⋯ opens.
      await settlePolyforgeToggle(phone.page);
      const more = phone.page.locator('[data-mobile-toolbar-more="true"]');
      if (await more.count()) {
        if ((await more.getAttribute("aria-expanded")) !== "true") await more.click();
        // The same exclusion as the toolbar search: the chat's floating ↻ is not an overlay control.
        const overlayText = await phone.page.evaluate(() => {
          const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
          const texts = [];
          for (let node = walker.nextNode(); node; node = walker.nextNode()) {
            if (!node.parentElement?.closest(".chat-scroll-to-bottom")) texts.push(node.textContent ?? "");
          }
          return texts.join(" ");
        });
        assert.ok(!/↻|⟳/.test(overlayText), "the phone More overlay still renders a manual-fetch glyph");
      }
    } finally { await closePage(phone); }
  });

  // S5 — spec Req 5 / AC 5: one floating button, two forms by scroll position.
  await check("S5", "Floating button is refresh at the tail and scroll-to-bottom when scrolled up", async () => {
    const state = await openPage({ session: SCROLL, marker: "SCROLL message 159", viewport: { width: 1280, height: 800 } });
    try {
      await scrollList(state.page, "bottom");
      const button = floating(state.page);
      await button.waitFor({ state: "visible", timeout: 20_000 });
      assert.equal(await button.getAttribute("aria-label"), "Fetch latest", "at the tail the floating button is not the refresh form");
      const first = forceIndex(state);
      await button.click();
      await waitFor(() => forceReads(state, first) === 1, "one forced fresh read from the floating refresh form");
      await waitFor(() => listAtBottom(state.page), "the list stayed at the bottom after the fetch");
      // The page's own post-load forced read (refreshFromDisk) is not the ↓ form's: wait until forced reads are stable for 2 s.
      let stableCount = forceReads(state);
      let stableSince = Date.now();
      await waitFor(() => {
        const current = forceReads(state);
        if (current !== stableCount) { stableCount = current; stableSince = Date.now(); }
        return Date.now() - stableSince >= 2000;
      }, "forced reads settled for 2 s");
      await scrollList(state.page, "middle");
      await waitFor(async () => (await button.getAttribute("aria-label")) === "Scroll to latest", "the scrolled-up form is scroll-to-latest");
      const beforeScroll = forceIndex(state);
      await button.click();
      await waitFor(() => listAtBottom(state.page), "the scroll form reached the bottom");
      await waitFor(async () => (await button.getAttribute("aria-label")) === "Fetch latest", "the button did not become the refresh form at the bottom");
      assert.equal(forceReads(state, beforeScroll), 0, "the scroll-to-bottom form performed a fetch");
    } finally { await closePage(state); }
  });

  // S6 — spec Req 6 / AC 6: the refresh form spins busy, joins an in-flight
  // fetch on a re-click and stays at the bottom.
  await check("S6", "Floating refresh shows busy, joins a re-click into one fetch and one notice", async () => {
    const state = await openPage({ session: SCROLL, marker: "SCROLL message 159", viewport: { width: 1280, height: 800 } });
    try {
      await state.page.route(/\/api\/sessions\/[^/]+\?.*force=1/, async (route) => { await delay(1200); await route.continue().catch(() => {}); });
      await scrollList(state.page, "bottom");
      const button = floating(state.page);
      await button.waitFor({ state: "visible", timeout: 20_000 });
      assert.equal(await button.getAttribute("aria-label"), "Fetch latest", "the refresh form is missing at the tail");
      const first = forceIndex(state);
      await button.click();
      await waitFor(async () => (await button.getAttribute("aria-busy")) === "true", "the busy state (aria-busy)");
      const busyText = `${await button.innerText()} ${await button.getAttribute("aria-label")}`;
      assert.match(busyText, /Fetching latest|正在拉取|正在擷取/, "the busy label is missing");
      await button.click();
      await waitFor(() => forceReads(state, first) === 1, "a re-click issued a second forced read");
      await waitFor(async () => (await button.getAttribute("aria-busy")) !== "true", "the busy state did not clear");
      const notices = await state.page.locator(".notice-shelf-item").evaluateAll((nodes) => nodes.map((node) => node.innerText.trim()));
      assert.equal(notices.filter((notice) => /Already up to date|Updated to|Fetch failed/.test(notice)).length, 1, `expected exactly one fetch notice, got ${JSON.stringify(notices)}`);
      assert.ok(await listAtBottom(state.page), "the fetch left the reader away from the newest message");
    } finally { await closePage(state); }
  });

  // S7 — spec Req 7 / AC 7: at <=640px key and class badge share one row, values below.
  await check("S7", "At <=640px the config class badge shares the key's row and values span below", async () => {
    const state = await openPage({ session: MAIN, marker: "E2E ui polish reply", viewport: { width: 1100, height: 980 }, routes: configRoutes });
    try {
      const { page } = state;
      const show = page.getByRole("button", { name: "Show sidebar", exact: true });
      if (await visible(show)) await show.click();
      await page.getByRole("button", { name: "Settings", exact: true }).first().click();
      const dialog = page.getByRole("dialog", { name: "Settings", exact: true });
      await dialog.getByRole("button", { name: "Polyforge", exact: true }).click();
      // the cli.toml card is the default Cell's own pane of the master-detail page.
      await dialog.locator('[data-pf-cell-entry="default"]').first().click();
      await dialog.locator('[data-pf-pane="cell:default"]').first().waitFor({ state: "visible", timeout: 30_000 });
      // [daemon] is a zone of the one cli.toml card, no card of its own.
      await dialog.locator('section[aria-label="cli.toml"] .polyforge-config-zone.is-daemon').first().waitFor({ state: "attached", timeout: 30_000 });
      await page.setViewportSize({ width: 390, height: 844 });
      await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      const geometry = await page.evaluate(() => {
        const rows = [...document.querySelectorAll(".polyforge-config-row")];
        const row = rows.find((node) => (node.querySelector(".polyforge-config-key")?.textContent ?? "").trim() === "step_timeout");
        if (!row) return null;
        const rect = (node) => { const r = node.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width }; };
        const style = getComputedStyle(row);
        const contentLeft = row.getBoundingClientRect().left + parseFloat(style.paddingLeft);
        const contentRight = row.getBoundingClientRect().right - parseFloat(style.paddingRight);
        return {
          key: rect(row.querySelector(".polyforge-config-key")),
          badge: rect(row.querySelector(".polyforge-config-class .polyforge-config-badge")),
          file: rect(row.querySelector(".polyforge-config-file")),
          daemon: rect(row.querySelector(".polyforge-config-daemon")),
          contentLeft, contentRight,
        };
      });
      assert.ok(geometry, "the badged daemon row did not render");
      const { key, badge, file, daemon, contentLeft, contentRight } = geometry;
      assert.ok(Math.abs(badge.top - key.top) <= 2, `the badge is not on the key's row (key ${key.top}, badge ${badge.top})`);
      assert.ok(Math.abs(badge.right - contentRight) <= 1, `the badge is not right-aligned (badge ${badge.right}, row ${contentRight})`);
      assert.ok(key.left < badge.left, "the key is not left of the badge");
      assert.ok(file.top >= Math.max(key.bottom, badge.bottom) - 1, "the file value is not below the key/badge row");
      assert.ok(Math.abs(file.width - (contentRight - contentLeft)) <= 1, `the file value does not span the row (${file.width} vs ${contentRight - contentLeft})`);
      assert.ok(daemon.top >= Math.max(key.bottom, badge.bottom) - 1, "the daemon value is not below the key/badge row");
      assert.ok(Math.abs(daemon.width - (contentRight - contentLeft)) <= 1, `the daemon value does not span the row (${daemon.width} vs ${contentRight - contentLeft})`);
    } finally { await closePage(state); }
  });

  // S8 — spec Req 8 / AC 8: the Jobs cards are one row of fixed-width cards.
  // owner-intended change（重立自 #209）: the shared Jobs/Tasks minimum is
  // 150px (was 160px). owner-intended change: the Jobs stream is one
  // horizontally scrolling row, so the cards share one row at every width and
  // the row scrolls once the cards no longer fit. owner-intended change:
  // that fixed width is 180px (decoupled from the Tasks grid's 150px), checked
  // at the 390/768/1280 viewports (#311's method) — the 390 sheet scrolls two
  // 180px cards, the 768 and 1280 docks (460px) still fit them with free space.
  await check("S8", "Jobs cards are one row of fixed 180px cards at 390/768/1280", async () => {
    const cases = [
      [{ width: 390 }, "390 viewport (sheet)", true],
      [{ width: 768 }, "768 viewport", false],
      [{ width: 1280 }, "1280 viewport", false],
    ];
    for (const [opts, label, overflows] of cases) {
      const state = await openPage({ session: MAIN, marker: "E2E ui polish reply", viewport: { width: opts.width, height: 800 }, storage: opts.storage, routes: jobsRoutes });
      try {
        await openJobsDock(state.page);
        const measured = await state.page.evaluate(() => {
          const card = document.querySelector("[data-pf-card-wi]");
          const row = card?.parentElement;
          if (!row) return null;
          const style = getComputedStyle(row);
          const cards = [...row.querySelectorAll("[data-pf-card-wi]")].map((node) => { const r = node.getBoundingClientRect(); return { top: Math.round(r.top), left: Math.round(r.left), width: r.width }; });
          return { cards, width: row.getBoundingClientRect().width, clientWidth: row.clientWidth, scrollWidth: row.scrollWidth,
            overflowX: style.overflowX, scrollbarWidth: style.scrollbarWidth };
        });
        assert.ok(measured, `${label}: the card row did not render`);
        assert.equal(measured.cards.length, 2, `${label}: expected two cards`);
        const rows = new Set(measured.cards.map((card) => card.top));
        assert.equal(rows.size, 1, `${label}: the cards share one row (${JSON.stringify(measured.cards)})`);
        assert.ok(measured.cards.every((card) => Math.abs(card.width - 180) <= 0.5),
          `${label}: each card is a fixed 180px wide (${JSON.stringify(measured.cards.map((card) => card.width))}) (owner-intended change)`);
        assert.ok(measured.cards[1].left - (measured.cards[0].left + measured.cards[0].width) >= 6,
          `${label}: the two cards keep the 8px gap (${JSON.stringify(measured.cards)})`);
        assert.equal(measured.overflowX, "auto", `${label}: the card row scrolls horizontally (${measured.overflowX})`);
        assert.equal(measured.scrollbarWidth, "thin", `${label}: the row's scrollbar stays visible but thin (${measured.scrollbarWidth})`);
        if (overflows) assert.ok(measured.scrollWidth > measured.clientWidth,
          `${label}: two 180px cards exceed the ${measured.clientWidth}px row and it scrolls (${measured.scrollWidth}) (owner-intended change)`);
        else assert.ok(measured.scrollWidth <= measured.clientWidth,
          `${label}: two 180px cards fit the ${measured.clientWidth}px row with free space at the right (${measured.scrollWidth})`);
      } finally { await closePage(state); }
    }
  });

  // S9 — spec Req 9 / AC 10: status on the title row's right end. owner-intended change
  // (reverses the 32px narration): the card has no
  // progress region any more. owner-intended change: a running card's
  // visible status is the duration alone (`23m`), the full status text lives in
  // the element's title and the card's aria-label, and the card frame's colour
  // is the job's status.
  await check("S9", "Jobs card status: duration-only running text, frame colour by status, full status in title/aria", async () => {
    const state = await openPage({ session: MAIN, marker: "E2E ui polish reply", viewport: { width: 460, height: 800 }, storage: { "pi-web:polyforge-dock-width": "460" }, routes: statusRoutes });
    try {
      await openJobsDock(state.page);
      const running = await cardGeometry(state.page, "wi_E2E198A", "task#9181", "Running");
      assert.ok(running.slug && running.status && running.goal, "the running card's slug, status and goal did not render");
      assert.ok(Math.abs(running.status.top - running.slug.top) <= 2, `status is not on the slug's row (slug ${running.slug.top}, status ${running.status.top})`);
      assert.ok(Math.abs(running.status.right - running.contentRight) <= 2, `status is not at the card's right edge (${running.status.right} vs ${running.contentRight})`);
      assert.ok(running.status.left > running.slug.left, "status is not right of the slug");
      assert.ok(running.goal.top >= Math.max(running.slug.bottom, running.status.bottom) - 1, "the goal is not below slug and status");
      // owner-intended change (reverses the 32px narration):
      // nothing in the card scrolls on its own — the narration area is gone.
      assert.equal(running.progress, null, "the running card has no progress region");
      // owner-intended change: duration-only visible text; the status word
      // and age stay in the element's title and the card's aria-label.
      assert.match(running.statusText, /^\d+m$/, `the running card's visible status is the duration alone (${JSON.stringify(running.statusText)})`);
      assert.doesNotMatch(running.statusText, /running|运行中/i, "no status word in the running visible text");
      assert.match(running.statusTitle, /^Running \d+m$/, `the running status element carries the full status as its title (${JSON.stringify(running.statusTitle)})`);
      assert.equal(running.aria, `task#9181 · ${running.statusTitle}`, `the running card's aria-label keeps the slug and the full status (${JSON.stringify(running.aria)})`);
      const needs = await cardGeometry(state.page, "wi_E2E198B", "task#9182", "Needs input");
      assert.ok(needs.slug && needs.status, "the needs_input card's slug and badge did not render");
      assert.ok(Math.abs(needs.status.top - needs.slug.top) <= 2, `the needs_input badge is not on the slug's row (slug ${needs.slug.top}, badge ${needs.status.top})`);
      assert.ok(await state.page.locator('[data-pf-card-wi="wi_E2E198B"] [data-pf-needs-input]').count() === 1, "the needs_input badge lost its emphasized form");
      assert.equal(needs.statusText, "Needs input", "a non-running card keeps its visible status text");
      assert.equal(needs.statusTitle, "Needs input", "the needs_input badge carries the full status as its title");
      assert.equal(needs.aria, "task#9182 · Needs input", "the non-running aria-label keeps the slug and the full status");
      // Oracle (b): the computed border colour per status equals the memo table;
      // the three quiet states share one gray and differ from the four coloured
      // ones, and every frame stays 1px.
      const frames = await state.page.evaluate(() => {
        const probe = document.createElement("span");
        document.body.appendChild(probe);
        const resolve = (value) => { probe.style.color = value; return getComputedStyle(probe).color; };
        const expected = {
          running: resolve("var(--accent)"),
          needs_input: resolve("#d97706"),
          completed: resolve("#16a34a"),
          failed: resolve("#dc2626"),
          queued: resolve("var(--text-dim)"),
          paused: resolve("var(--text-dim)"),
          cancelled: resolve("var(--text-dim)"),
          unknown: resolve("var(--border)"),
        };
        const cards = Object.fromEntries([...document.querySelectorAll("[data-pf-card-wi]")].map((el) => {
          const style = getComputedStyle(el);
          return [el.getAttribute("data-pf-card-wi"), { border: style.borderTopColor, width: parseFloat(style.borderTopWidth) || 0 }];
        }));
        probe.remove();
        return { expected, cards };
      });
      const statuses = { wi_E2E198A: "running", wi_E2E198B: "needs_input", wi_E2E198C: "completed", wi_E2E198D: "failed",
        wi_E2E198E: "queued", wi_E2E198F: "paused", wi_E2E198G: "cancelled" };
      for (const [wi, status] of Object.entries(statuses)) {
        assert.ok(frames.cards[wi], `${wi} card renders`);
        assert.equal(frames.cards[wi].border, frames.expected[status], `${wi} (${status}) frame colour is the status colour (${frames.cards[wi].border})`);
        assert.equal(frames.cards[wi].width, 1, `${wi} keeps the 1px frame width (${frames.cards[wi].width})`);
      }
      const quiet = frames.expected.queued;
      assert.equal(frames.expected.paused, quiet, "paused shares the quiet gray");
      assert.equal(frames.expected.cancelled, quiet, "cancelled shares the quiet gray");
      const loud = ["running", "needs_input", "completed", "failed"].map((status) => frames.expected[status]);
      assert.equal(new Set(loud).size, 4, "the four coloured states are pairwise distinct");
      assert.ok(!loud.includes(quiet) && quiet !== frames.expected.unknown, "the quiet gray differs from the four coloured and from the block default");
    } finally { await closePage(state); }
  });

  // S10 — spec Req 10 / AC 14: the card is strictly shorter than the 4b000b22
  // measurement, taken with this fixture and viewport (see the constant above).
  await check("S10", "Jobs card is shorter than the 4b000b22 baseline measured with the same fixture", async () => {
    const state = await openPage({ session: MAIN, marker: "E2E ui polish reply", viewport: { width: 460, height: 800 }, storage: { "pi-web:polyforge-dock-width": "460" }, routes: jobsRoutes });
    try {
      await openJobsDock(state.page);
      const height = await cardNode(state.page, "wi_E2E198A").evaluate((node) => node.getBoundingClientRect().height);
      assert.ok(CARD_HEIGHT_BASE_460 > 0, "the 4b000b22 card-height baseline constant is not set");
      assert.ok(height <= CARD_HEIGHT_BASE_460 - 4, `the card is ${height.toFixed(1)}px, not strictly shorter than the ${CARD_HEIGHT_BASE_460}px 4b000b22 baseline`);
    } finally { await closePage(state); }
  });

  // S12 — spec scenario "Each split pane acts on its own session" (added per
  // owner answer s198-code-split-pane-s12): with two panes on two different
  // saved sessions, the right pane's floating refresh reads only the right
  // session (one forced read) and nothing happens in the left pane.
  await check("S12", "Each split pane's floating refresh acts only on its own session", async () => {
    const state = await openPage({ session: SCROLL, marker: "SCROLL message 159", viewport: { width: 2560, height: 1200 }, storage: { "pi-web:split-view-enabled": "true", "pi-chat-content-width": "820" } });
    try {
      const { page } = state;
      const paneArea = page.locator("[data-split-pane-area]");
      const panes = paneArea.locator("> div");
      await page.locator('[title="RIGHT message 000"]').first().click();
      await waitFor(async () => (await panes.count()) === 2, "a second split pane opened for the right session");
      const left = panes.nth(0);
      const right = panes.nth(1);
      await left.getByText("SCROLL message 159", { exact: true }).first().waitFor({ state: "attached" });
      await right.getByText("RIGHT message 159", { exact: true }).first().waitFor({ state: "attached" });
      for (const pane of [left, right]) {
        await pane.evaluate((node) => {
          const list = node.querySelector(".scrollbar-subtle.overflow-y-auto");
          if (!list) throw new Error("no chat scroll list in the pane");
          list.scrollTop = list.scrollHeight;
        });
      }
      const rightButton = right.locator(".chat-scroll-to-bottom").first();
      await rightButton.waitFor({ state: "visible", timeout: 20_000 });
      assert.equal(await rightButton.getAttribute("aria-label"), "Fetch latest", "the right pane's floating button is not the refresh form at the tail");
      // The page's own post-load forced reads are not the button's: wait until they are stable for 2 s.
      const allForced = () => state.requests.filter((request) => request.startsWith("/api/sessions/") && request.includes("force=1")).length;
      let stableCount = allForced();
      let stableSince = Date.now();
      await waitFor(() => {
        const current = allForced();
        if (current !== stableCount) { stableCount = current; stableSince = Date.now(); }
        return Date.now() - stableSince >= 2000;
      }, "forced reads settled for 2 s");
      const fetchNotices = (pane) => pane.locator(".notice-shelf-item").evaluateAll((nodes) => nodes.map((node) => node.innerText.trim()).filter((text) => /Already up to date|Updated to|Fetch failed/.test(text)));
      const leftNoticesBefore = (await fetchNotices(left)).length;
      const since = forceIndex(state);
      const forcedFor = (id) => state.requests.filter((request, index) => index >= since && request.startsWith(`/api/sessions/${id}?`) && request.includes("force=1")).length;
      await rightButton.click();
      await waitFor(async () => (await fetchNotices(right)).length === 1, "the right pane shows its fetch notice");
      await delay(1500);
      assert.equal(forcedFor(RIGHT), 1, "the right pane's refresh did not issue exactly one forced read of the right session");
      assert.equal(forcedFor(SCROLL), 0, "the right pane's refresh read the left pane's session");
      assert.equal(allForced() - stableCount, 1, "the right pane's refresh issued forced reads beyond its own session");
      assert.equal((await fetchNotices(left)).length, leftNoticesBefore, "a fetch notice appeared in the left pane");
    } finally { await closePage(state); }
  });
} catch (error) {
  failures.push("suite");
  console.error(`FAIL suite: ${String(error?.stack ?? error)}`);
} finally {
  for (const context of [...openContexts]) await context.close().catch(() => {});
  await h.close();
  console.log(`\n${failures.length ? `FAILED ${failures.join(", ")}` : "PASS all authored UI-polish scenarios"}`);
  console.log(`server log: ${join(work, "server.log")}`);
}
if (failures.length) process.exitCode = 1;
