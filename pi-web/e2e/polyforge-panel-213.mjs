// carries: components/PolyforgeTasks*
// Polyforge panel UI revision, re-filed from an earlier ticket
//
// Authored pre-code against artifacts/spec.md (the owner-confirmed acceptance
// ①–④). The suite starts its own `next dev` and drives the panel through its
// existing HTTP faces, mocked in the browser with the routes' documented wire
// shapes: /api/polyforge/status, /cards, /watch, /tasks and /messages. No pf
// CLI state, no daemon data root and no model call is needed. Every scenario
// prints `PASS S213-<n>` on success (the binding marker); at the
// pre-implementation tree every one is assertion-level RED:
//   - the DAG node still renders its localized status text and a filled
//     skipped dot;
//   - Jobs uses 160px and Tasks 112px, so the tracks and column counts differ;
//   - the ≤640px Work selector is a horizontal text bar with no icons/names;
//   - mobile Messages is a single-view switch with no side-by-side pane.
//
//   node e2e/polyforge-panel-213.mjs
//
// e2e/all: isolated — a precaution: names this suite, and it
// shares work-tab-163's dock click path (openPolyforgeDock() in
// e2e/polyforge-dock.mjs), whose 8 s click timed out in the parallel bar.
// S213-12's own red runs (#236) were a
// context-read race, fixed by mocking the live status read, not by isolation.
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { openPolyforgeDock as openDock, polyforgeDock as dock, polyforgeToggle as pfButton, waitForPolyforgeToggle } from "./polyforge-dock.mjs";
import { ACTION_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, mockContextUsage, mockPolyforge, suiteDist } from "./harness.mjs";

const distDir = suiteDist("polyforge-panel-213");
assertNoDevServer(distDir);
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-213-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-panel-213-session";
mkdirSync(join(agentDir, "sessions", "e2e"), { recursive: true });
mkdirSync(project);
writeFileSync(join(agentDir, "sessions", "e2e", `${SESSION}.jsonl`), [
  { type: "session", version: 3, id: SESSION, timestamp: "2026-10-31T00:00:00.000Z", cwd: project },
  { type: "message", id: "m1", parentId: null, timestamp: "2026-10-31T00:00:00.000Z", message: { role: "user", content: "E2E pf panel 213 host session" } },
  // usage on disk gives the phone stats strip all four items
  // (input, output, cost, context %) without a live model call (
  // retired the fifth, the session's polyforge chat id).
  { type: "message", id: "m2", parentId: "m1", timestamp: "2026-10-31T00:00:00.000Z", message: {
    role: "assistant", content: [{ type: "text", text: "E2E pf panel 213 assistant" }],
    provider: "e2e", model: "e2e-model", api: "openai-completions", stopReason: "stop",
    usage: { input: 12_345_678, output: 2_345_678, cacheRead: 0, cacheWrite: 0, totalTokens: 14_691_356,
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 12.34 } },
    timestamp: Date.parse("2026-10-31T00:00:00.000Z"),
  } },
].map((entry) => JSON.stringify(entry)).join("\n") + "\n");

// ---- fixture data: the documented wire shapes of the existing routes ----
const EPOCH = "2026-10-31T00:00:00.000Z";
const job = (n) => ({ wi: `wi_E2E213J${n}`, slug: `${n}`.replace("#93", "#932"), status: "running",
  stepAgeSec: 30 + n, idleAgeSec: n, candidate: "pi:e2e/model", spawnN: 1 });
const A = job(1); const B = job(2); const C = job(3);
const cards = [A, B, C].map((entry) => ({
  job: entry, goal: `E2E 213 job ${entry.wi}`, startedAt: EPOCH,
  steps: [
    { id: "step-grill", state: "timed", durationSec: 12, runs: 1 },
    { id: "step-skip", state: "skipped", runs: 0 },
    { id: "step-code", state: "timed", durationSec: 5, runs: 1 },
    { id: "step-review", state: "unstarted", runs: 0 },
  ],
  currentStep: "step-code",
  progress: { available: true, entries: [] },
}));
const status = {
  pfPresent: true,
  status: { queue: { running: 3, max: 6 }, jobs: [A, B, C], tasks: { running: 0, completed: 3 } },
};
const watch = { status: {
  wi: A.wi, slug: A.slug, workflowStatus: "running", currentStep: "step-code", updatedAt: EPOCH,
  steps: [
    { name: "step-grill", status: "done", startedAt: EPOCH },
    { name: "step-skip", status: "skipped" },
    { name: "step-code", status: "in_progress", startedAt: EPOCH },
    { name: "step-review", status: "pending" },
  ],
  declared_steps: [{ id: "step-grill" }, { id: "step-skip" }, { id: "step-code" }, { id: "step-review" }],
  events: [],
} };
const T = ["1", "2", "3"].map((n) => ({ id: `task_000000000000000${n}`, project: "workbench", state: "completed",
  prompt_summary: `E2E TASK ${n}`, started_at: EPOCH, ended_at: EPOCH, wall_consumed_ns: 42_000_000_000,
  report_tail: `E2E report ${n}`, reporter: "machine" }));
const tasks = { pfPresent: true, capable: true, tasks: T };
const conversation = { id: "conv_e2e213", kind: "direct", peer: "u_e2e213", peer_name: "Alice Chen",
  last_seq: 1, unread: 0, created_at: EPOCH };
const messages = { pfPresent: true, pfVersion: "v2.20.13", capable: true, conversations: [conversation], contacts: [] };

// ---- scenario bookkeeping and helpers ----
const failures = [];
const check = createCheck({ failures });
// the session's context usage is one fact, served by two route
// faces: the mount's /api/sessions/<id>/state read and the connect-time
// /api/agent/<id>?systemPrompt=0 status read. Both mocks answer from this
// constant so a lost request-order race cannot make the two faces disagree.
const CONTEXT_USAGE = { percent: 42, contextWindow: 200_000, tokens: 84_000 };
const h = createHarness({
  locale: "zh-CN",
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN" },
  timeout: ACTION_MS,
  session: SESSION,
  ready: "E2E pf panel 213 host session",
  async setup(page) {
    await mockPolyforge(page, {
      status,
      cards: () => ({ pfPresent: true, cards }),
      watch: () => watch,
      tasks: () => tasks,
      messages: () => messages,
      "messages/*": () => ({ capable: true, messages: [], last_seq: conversation.last_seq, has_older: false }),
    });
    // the stats strip's context reading comes from the mount's
    // state read; the page's live status read ( / #236 / #247) is
    // pinned to the same reading.
    await mockContextUsage(page, SESSION, CONTEXT_USAGE);
  },
});
const { errors, withPage } = h;
const topTabs = (page) => dock(page).locator('[role="tablist"]').first().getByRole("tab");
const topTab = (page, re) => topTabs(page).filter({ hasText: re }).first();
const workTablist = (page) => dock(page).locator('[data-pf-tabstrip="work"]');
const workTab = (page, re) => workTablist(page).getByRole("tab", { name: re }).first();
const card = (page, wi) => dock(page).locator(`[data-pf-card-wi="${wi}"]`);
const cardsOn = (page) => dock(page).locator("[data-pf-card-wi]");
const taskBlocks = (page) => dock(page).locator("[data-pf-task]");
async function openWork(page, sub) {
  await openDock(page);
  await topTab(page, /工单|work/i).waitFor({ timeout: 20_000 });
  await topTab(page, /工单|work/i).click();
  if (sub) { await workTab(page, sub).waitFor({ timeout: 15_000 }); await workTab(page, sub).click(); }
  return dock(page);
}
async function openMessages(page) {
  await openDock(page);
  await topTab(page, /消息|messages/i).waitFor({ timeout: 15_000 });
  await topTab(page, /消息|messages/i).click();
  return dock(page);
}
const waitFor = createWaitFor({ ms: 20_000 });
async function require(locator, what) {
  assert.ok((await locator.count()) > 0, `${what} is not rendered`);
  return locator.first();
}
const overlap = (p, q) => p.x < q.x + q.width - 1 && q.x < p.x + p.width - 1 && p.y < q.y + q.height - 1 && q.y < p.y + p.height - 1;
const node = (page, id) => dock(page).getByRole("listitem").filter({ hasText: id }).first();
const dotStyle = (nodeLocator) => nodeLocator.locator('span[aria-hidden="true"]').first().evaluate((el) => {
  const style = getComputedStyle(el);
  return { background: style.backgroundColor, borderWidth: parseFloat(style.borderTopWidth) || 0, borderColor: style.borderTopColor };
});
const transparent = (color) => color === "transparent" || /rgba\(\s*\d+\s*,\s*\d+\s*,\s*\d+\s*,\s*0\s*\)/.test(color);
async function gridInfo(locator) {
  return locator.evaluate((el) => {
    const inline = el.style.gridTemplateColumns;
    const tracks = getComputedStyle(el).gridTemplateColumns.split(/\s+/).filter((part) => /px$/.test(part)).map((part) => parseFloat(part));
    return { inline, tracks };
  });
}
async function columnCount(locator) {
  const xs = await locator.evaluateAll((nodes) => nodes.map((el) => Math.round(el.getBoundingClientRect().x)));
  return new Set(xs).size;
}
// (owner's intentional change, not a regression): the Jobs stream
// is ONE row of fixed 180px cards that scrolls horizontally (owner-intended change;
// it was 150px), so it has no
// column count of its own any more — the Tasks blocks keep their grid. The
// helpers below measure the card row and, on the Tasks side, the grid as before.
async function workLayout(page) {
  await openWork(page);
  await waitFor(async () => (await cardsOn(page).count()) === 3, "three job cards render", 30_000);
  const row = cardsOn(page).first().locator("xpath=..");
  const boxes = await cardsOn(page).evaluateAll((nodes) => nodes.map((el) => {
    const box = el.getBoundingClientRect();
    return { x: box.x, y: box.y, width: box.width };
  }));
  const metrics = await row.evaluate((el) => ({
    clientWidth: el.clientWidth, scrollWidth: el.scrollWidth,
    overflowX: getComputedStyle(el).overflowX, scrollbarWidth: getComputedStyle(el).scrollbarWidth,
  }));
  return { main: await row.boundingBox(), boxes, row: metrics };
}
async function taskLayout(page) {
  await workTab(page, /任务|tasks/i).click();
  await waitFor(async () => (await taskBlocks(page).count()) === 3, "three task blocks render", 30_000);
  const grid = await taskBlocks(page).first().locator("xpath=..");
  return { tasks: await columnCount(taskBlocks(page)), grid };
}

try {
  await h.launchBrowser(() => chromium.launch());
  await h.startServer({ name: "polyforge-panel-213", logFile: join(work, "server.log"), distDir, env: {
    PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: "" } });

  await check("S213-1", () => withPage({}, async (page) => {
    await openWork(page);
    await card(page, A.wi).waitFor({ timeout: 30_000 });
    await card(page, A.wi).click();
    await waitFor(async () => (await dock(page).getByRole("listitem").count()) >= 4, "the DAG nodes render", 15_000);
    const expected = { "step-grill": "已完成", "step-skip": "已跳过", "step-code": "进行中", "step-review": "待开始" };
    for (const [id, label] of Object.entries(expected)) {
      const listitem = node(page, id);
      await listitem.waitFor({ timeout: 15_000 });
      assert.equal(await listitem.getAttribute("aria-label"), `${id} · ${label}`, `${id}: aria-label carries id and status`);
      // owner-intended change: a started step's title also carries its start time; an unstarted one is unchanged.
      const title = await listitem.getAttribute("title");
      if (id === "step-grill" || id === "step-code") assert.match(title, new RegExp(`^${id} · ${label} · \\d{2}:\\d{2}:\\d{2}`), `${id}: title carries id, status and start time`);
      else assert.equal(title, `${id} · ${label}`, `${id}: title carries id and status`);
      const text = (await listitem.innerText()).replace(/\s+/g, " ").trim();
      assert.equal(text, id, `${id}: the node shows the step name and no visible status text (got ${JSON.stringify(text)})`);
      assert.ok((await listitem.locator('span[aria-hidden="true"]').count()) > 0, `${id}: the status dot stays`);
    }
  }));

  await check("S213-2", () => withPage({}, async (page) => {
    await openWork(page);
    await card(page, A.wi).waitFor({ timeout: 30_000 });
    await card(page, A.wi).click();
    await node(page, "step-skip").waitFor({ timeout: 15_000 });
    const skipped = await dotStyle(node(page, "step-skip"));
    const pending = await dotStyle(node(page, "step-review"));
    assert.ok(transparent(skipped.background), `the skipped dot is hollow (background ${skipped.background})`);
    assert.ok(skipped.borderWidth >= 1, `the skipped dot has a gray border (width ${skipped.borderWidth})`);
    assert.ok(!transparent(skipped.borderColor), `the skipped ring border is visible (${skipped.borderColor})`);
    assert.ok(!transparent(pending.background), `the pending dot stays a filled dot (background ${pending.background})`);
    assert.equal(await node(page, "step-skip").getAttribute("aria-label"), "step-skip · 已跳过", "the skipped node still names its status");
    assert.equal(await node(page, "step-review").getAttribute("aria-label"), "step-review · 待开始", "the pending node still names its status");
  }));

  await check("S213-3", () => withPage({ storage: { "pi-web:polyforge-dock-width": "460" } }, async (page) => {
    const jobs = await workLayout(page);
    const task = await taskLayout(page);
    const tasksGrid = await gridInfo(task.grid);
    assert.match(tasksGrid.inline, /minmax\(150px,\s*1fr\)/, `the Tasks grid uses a 150px minimum (${tasksGrid.inline})`);
    // (owner's intentional change, not a regression): the job
    // cards are one row of fixed-width cards. owner-intended change: the
    // job card is now a fixed 180px and the Tasks grid keeps its own 150px
    // column minimum — the two are decoupled, so the old "job card width = the
    // Tasks grid minimum" equal-width pin becomes "job card = 180px, Tasks
    // minimum still 150px". The one-row / horizontally scrollable shape and
    // every Tasks minimum stay pinned, not relaxed.
    const jobWidths = jobs.boxes.map((box) => box.width);
    assert.ok(jobWidths.every((width) => Math.abs(width - 180) <= 0.5), `every job card is 180px wide (${JSON.stringify(jobWidths)}) (owner-intended change)`);
    const taskMin = Number(/minmax\((\d+)px,\s*1fr\)/.exec(tasksGrid.inline)?.[1]);
    assert.equal(taskMin, 150, `the Tasks grid keeps its own 150px column minimum (${taskMin}) (owner-intended change)`);
    assert.ok(tasksGrid.tracks.length > 0 && tasksGrid.tracks.every((track) => track >= taskMin - 0.5),
      `every Tasks track keeps the declared ${taskMin}px minimum (${JSON.stringify(tasksGrid.tracks)})`);
    assert.ok(Math.abs(Math.min(...jobWidths) - 180) <= 0.5 && Math.abs(Math.min(...jobWidths) - taskMin) > 1,
      `the job card width is the decoupled 180px, not the Tasks grid minimum (${Math.min(...jobWidths)}/${taskMin}px)`);
    assert.equal(new Set(jobs.boxes.map((box) => Math.round(box.y))).size, 1,
      `the job cards share one row (${JSON.stringify(jobs.boxes.map((box) => box.y))})`);
    assert.equal(jobs.row.overflowX, "auto", `the card row scrolls horizontally (${jobs.row.overflowX})`);
    assert.equal(jobs.row.scrollbarWidth, "thin", `the row's scrollbar stays visible but thin (${jobs.row.scrollbarWidth})`);
    assert.ok(jobs.row.scrollWidth > jobs.row.clientWidth,
      `the row overflows and can be scrolled (${jobs.row.scrollWidth}/${jobs.row.clientWidth})`);
  }));

  const measureColumns = (opts, label, taskColumns) => withPage(opts, async (page) => {
    const jobs = await workLayout(page);
    // the job cards stay one fixed-width row at every width and
    // the row scrolls instead of wrapping or squeezing (old: "Job cards use N
    // column(s)"). owner-intended change: that fixed width is 180px.
    assert.equal(new Set(jobs.boxes.map((box) => Math.round(box.y))).size, 1, `${label}: the job cards share one row`);
    assert.ok(jobs.boxes.every((box) => Math.abs(box.width - 180) <= 0.5),
      `${label}: every job card is 180px wide (${JSON.stringify(jobs.boxes.map((box) => box.width))}) (owner-intended change)`);
    assert.ok(jobs.row.scrollWidth > jobs.row.clientWidth,
      `${label}: the card row is horizontally scrollable (${jobs.row.scrollWidth}/${jobs.row.clientWidth})`);
    const task = await taskLayout(page);
    assert.equal(task.tasks, taskColumns, `${label}: Task blocks use ${taskColumns} column(s)`);
  });
  await check("S213-4", async () => {
    await measureColumns({ viewport: { width: 375, height: 800 } }, "375px sheet with the 40px rail", 2);
    await measureColumns({ storage: { "pi-web:polyforge-dock-width": "460" } }, "460px dock", 2);
    await measureColumns({ storage: { "pi-web:polyforge-dock-width": "340" } }, "340px dock", 1);
  });

  await check("S213-5", async () => {
    let mobileButtons;
    await withPage({ viewport: { width: 375, height: 800 } }, async (page) => {
      const jobs = await workLayout(page);
      const rail = workTablist(page);
      await rail.waitFor({ timeout: 15_000 });
      const railBox = await rail.boundingBox();
      assert.ok(railBox && jobs.main, "the rail and the main area measure");
      assert.equal(await rail.getAttribute("aria-orientation"), "vertical", "the mobile rail is vertical");
      assert.ok(Math.abs(railBox.width - 40) <= 4, `the rail is about 40px wide (${railBox.width})`);
      assert.ok(railBox.x >= jobs.main.x + jobs.main.width - 2, `the rail sits right of the main area (rail ${railBox.x}, main end ${jobs.main.x + jobs.main.width})`);
      const buttons = rail.getByRole("tab");
      // intentional fourth sub-tab: 待集成 joins the Work rail after 工单列表.
      assert.equal(await buttons.count(), 4, "four sub-tab buttons");
      mobileButtons = [];
      for (let i = 0; i < 4; i++) {
        const button = buttons.nth(i);
        const box = await button.boundingBox();
        assert.ok(box && Math.abs(box.width - 40) <= 6 && Math.abs(box.height - 40) <= 6, `button ${i} is about 40×40 (${box?.width}×${box?.height})`);
        const icon = button.locator("svg").first();
        assert.ok((await icon.count()) > 0, `button ${i} renders an icon`);
        const iconBox = await icon.boundingBox();
        assert.ok(iconBox && Math.abs(iconBox.width - 16) <= 2 && Math.abs(iconBox.height - 16) <= 2,
          `button ${i} uses a 16px icon (${iconBox?.width}×${iconBox?.height})`);
        assert.equal(await icon.getAttribute("viewBox"), "0 0 24 24", `button ${i} uses the panel's 24-viewBox icon style`);
        mobileButtons.push(box);
      }
    });
    await withPage({}, async (page) => {
      await workLayout(page);
      const buttons = workTablist(page).getByRole("tab");
      const desktop = [];
      for (let i = 0; i < 4; i++) desktop.push(await buttons.nth(i).boundingBox());
      assert.ok(desktop.every(Boolean), "the desktop rail buttons measure");
      for (const box of mobileButtons) {
        assert.ok(box.width <= Math.max(...desktop.map((item) => item.width)) + 1, `a mobile button is no wider than a desktop rail button (${box.width})`);
        assert.ok(box.height <= Math.max(...desktop.map((item) => item.height)) + 1, `a mobile button is no taller than a desktop rail button (${box.height})`);
      }
    });
  });

  await check("S213-6", () => withPage({ viewport: { width: 375, height: 800 } }, async (page) => {
    const jobs = await workLayout(page);
    const rail = workTablist(page);
    await rail.waitFor({ timeout: 15_000 });
    const railBox = await rail.boundingBox();
    const dockBox = await dock(page).boundingBox();
    assert.ok(railBox && jobs.main && dockBox, "the rail, main area and sheet measure");
    assert.ok(!overlap(jobs.main, railBox), "the rail does not cover the main area");
    assert.ok(jobs.main.x + jobs.main.width <= railBox.x + 2, `the main area ends before the rail (main end ${jobs.main.x + jobs.main.width}, rail ${railBox.x})`);
    assert.ok(jobs.main.width >= dockBox.width - railBox.width - 12,
      `the main area keeps the remaining width (${jobs.main.width}/${dockBox.width} beside a ${railBox.width}px rail)`);
    assert.ok(jobs.main.width <= dockBox.width - railBox.width + 8,
      `the rail takes its own place instead of the main area (${jobs.main.width}/${dockBox.width})`);
  }));

  await check("S213-7", () => withPage({ viewport: { width: 375, height: 800 } }, async (page) => {
    await workLayout(page);
    const rail = workTablist(page);
    await rail.waitFor({ timeout: 15_000 });
    const buttons = rail.getByRole("tab");
    // intentional fourth sub-tab: 待集成 joins the Work rail after 工单列表.
    assert.equal(await buttons.count(), 4, "four sub-tab buttons");
    const names = ["作业", "任务", "工单列表", "待集成"];
    for (let i = 0; i < 4; i++) {
      const button = buttons.nth(i);
      assert.equal(await button.getAttribute("aria-label"), names[i], `button ${i} has the existing tab copy as aria-label`);
      assert.equal(await button.getAttribute("title"), names[i], `button ${i} has the existing tab copy as title`);
      const text = (await button.innerText()).replace(/\s+/g, " ").trim();
      assert.ok(!text.includes(names[i]), `button ${i} shows no tab text (got ${JSON.stringify(text)})`);
      const icon = button.locator("svg").first();
      assert.ok((await icon.count()) > 0, `button ${i} is icon-only with a visible icon`);
      assert.equal(await icon.getAttribute("viewBox"), "0 0 24 24", `button ${i} icon uses the 24-viewBox stroke style`);
      assert.notEqual(await icon.getAttribute("stroke"), "none", `button ${i} icon is stroked`);
    }
  }));

  await check("S213-8", () => withPage({ viewport: { width: 375, height: 800 } }, async (page) => {
    await workLayout(page);
    const badge = dock(page).locator('[data-pf-work-badge="jobs"]').first();
    await badge.waitFor({ timeout: 15_000 });
    const button = badge.locator("xpath=..");
    const icon = button.locator("svg").first();
    assert.ok((await icon.count()) > 0, "the running badge sits with the sub-tab icon");
    const badgeBox = await badge.boundingBox(); const iconBox = await icon.boundingBox(); const buttonBox = await button.boundingBox();
    assert.ok(badgeBox && iconBox && buttonBox, "the badge, icon and button measure");
    assert.ok(badgeBox.y + badgeBox.height / 2 >= buttonBox.y + buttonBox.height / 2,
      `the badge is under the icon (badge ${badgeBox.y}, button ${buttonBox.y})`);
    assert.ok(iconBox.y + iconBox.height / 2 <= buttonBox.y + buttonBox.height / 2,
      `the icon is above the badge (icon ${iconBox.y})`);
    assert.ok(Math.abs((badgeBox.x + badgeBox.width / 2) - (buttonBox.x + buttonBox.width / 2)) <= 6,
      "the badge stays under the icon horizontally");
    assert.match(await badge.getAttribute("aria-label"), /3/, "the badge keeps its accessible count label");
  }));

  const messagesLayout = (opts, label, expectedColumns) => withPage(opts, async (page) => {
    await openMessages(page);
    await waitFor(async () => (await dock(page).getByRole("button", { name: /Alice Chen/ }).count()) > 0, "the conversation row renders", 20_000);
    const row = dock(page).getByRole("button", { name: /Alice Chen/ }).first();
    const list = row.locator("xpath=..");
    const select = await require(dock(page).getByText(/选择一个会话/), `${label}: the chat pane empty state`);
    const dockBox = await dock(page).boundingBox(); const listBox = await list.boundingBox(); const chatBox = await select.boundingBox();
    assert.ok(dockBox && listBox && chatBox, `${label}: the sheet, list column and chat pane measure`);
    assert.ok(listBox.x >= chatBox.x + chatBox.width - 8, `${label}: the list column is on the right of the chat pane`);
    assert.ok(!overlap(listBox, chatBox), `${label}: the two panes do not overlap`);
    const expected = Math.min(220, Math.max(120, dockBox.width * 0.35));
    assert.ok(Math.abs(listBox.width - expected) <= 14, `${label}: the list width resolves from the clamp (${listBox.width}/${expected})`);
    const divider = await list.evaluate((el) => parseFloat(getComputedStyle(el).borderLeftWidth) || 0);
    assert.ok(divider >= 1, `${label}: the desktop divider is kept (${divider}px)`);
    assert.equal(await dock(page).getByLabel(/会话记录|Conversation history/).count(), 0, `${label}: nothing is auto-opened`);
    const columns = new Set([Math.round(listBox.x), Math.round(chatBox.x)]).size;
    assert.equal(columns, expectedColumns, `${label}: two panes are on screen together`);
  });
  await check("S213-9", () => messagesLayout({ viewport: { width: 375, height: 800 } }, "375px", 2));
  await check("S213-10", () => messagesLayout({ viewport: { width: 320, height: 800 } }, "320px", 2));

  await check("S213-11", () => withPage({ viewport: { width: 375, height: 800 } }, async (page) => {
    await openMessages(page);
    await waitFor(async () => (await dock(page).getByRole("button", { name: /Alice Chen/ }).count()) > 0, "the conversation row renders", 20_000);
    assert.ok((await dock(page).getByRole("button", { name: /Alice Chen/ }).count()) > 0, "the list column is still shown");
    const select = dock(page).getByText(/选择一个会话/);
    assert.ok((await select.count()) > 0, "the chat pane keeps the select-a-conversation empty state");
    assert.equal(await dock(page).getByLabel(/会话记录|Conversation history/).count(), 0, "no conversation is auto-opened");
    assert.equal(await dock(page).getByText("MSG-ONE", { exact: false }).count(), 0, "no message text is shown before a selection");
  }));

  // the phone top bar. At 375/320 the Polyforge button is
  // resident right of ⋯ (no overlay tap needed), the overlay keeps the other
  // actions and loses Polyforge, and the stats strip scrolls with no item
  // permanently clipped.
  // The stat items are found by their own classes/leaf spans, not by the
  // wrapper the scrolling layout adds, so the same probe reads both layouts.
  const statsSnapshot = (stats) => stats.evaluate((el) => {
    const strip = el.getBoundingClientRect();
    const nodes = [...el.querySelectorAll("span")].filter((node) => (
      node.classList.contains("mobile-session-stat-io")
      || node.classList.contains("mobile-session-stat-cost")
      || (node.children.length === 0 && !node.closest("svg"))
    ));
    return {
      scrollWidth: el.scrollWidth,
      clientWidth: el.clientWidth,
      scrollLeft: el.scrollLeft,
      strip: { left: strip.left, right: strip.right },
      items: nodes.map((node) => {
        const box = node.getBoundingClientRect();
        return { className: node.className, text: (node.textContent ?? "").trim(), left: box.left, right: box.right };
      }),
    };
  });
  const insideStrip = (item, strip) => item.left >= strip.left - 1 && item.right <= strip.right + 1;
  const setStatsScroll = async (page, stats, left) => {
    await stats.evaluate((el, value) => { el.scrollLeft = value; }, left);
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  };
  await check("S213-12", async () => {
    for (const width of [375, 320]) {
      await withPage({ viewport: { width, height: 800 } }, async (page) => {
        const more = page.locator('[data-mobile-toolbar-more="true"]');
        const stats = page.locator('button[data-mobile-toolbar-stats="true"]').first();
        await stats.waitFor({ state: "visible", timeout: 20_000 });
        assert.equal(await more.count(), 1, `${width}px: the ⋯ button renders`);
        // (#234 N2): the toggle renders only after the mount-time
        // pfPresent probe, so wait for it (bounded) before counting it.
        await waitForPolyforgeToggle(page);
        const pf = pfButton(page);
        assert.equal(await pf.count(), 1, `${width}px: the Polyforge button renders`);
        assert.ok(await pf.isVisible(), `${width}px: Polyforge is visible without opening ⋯`);
        assert.ok(await pf.isEnabled(), `${width}px: Polyforge is enabled`);
        assert.equal(await pf.getAttribute("aria-pressed"), "false", `${width}px: Polyforge is closed initially`);
        assert.equal(await pf.evaluate((el) => Boolean(el.closest('[data-mobile-toolbar-actions]'))), false,
          `${width}px: Polyforge is not inside the ⋯ overlay`);
        const pfBox = await pf.boundingBox(); const moreBox = await more.boundingBox(); const statsBox = await stats.boundingBox();
        assert.ok(pfBox && moreBox && statsBox, `${width}px: the toolbar buttons measure`);
        assert.ok(pfBox.x >= moreBox.x + moreBox.width - 1, `${width}px: Polyforge is right of ⋯ (${pfBox.x} < ${moreBox.x + moreBox.width})`);
        assert.ok(pfBox.x + pfBox.width <= statsBox.x + 1, `${width}px: Polyforge is left of the stats (${pfBox.x + pfBox.width} > ${statsBox.x})`);

        await pf.click();
        await dock(page).waitFor({ timeout: 20_000 });
        assert.equal(await pf.getAttribute("aria-pressed"), "true", `${width}px: clicking Polyforge opened the dock`);
        await page.getByRole("button", { name: /关闭 Polyforge|Close Polyforge/ }).click();
        await waitFor(async () => (await dock(page).count()) === 0, `${width}px: the dock closes`);

        await more.click();
        const overlay = page.locator('[data-mobile-toolbar-actions="true"]');
        await overlay.waitFor({ state: "visible", timeout: 10_000 });
        assert.ok((await overlay.locator("[data-mobile-toolbar-action]").count()) >= 5,
          `${width}px: the ⋯ overlay still holds the other actions`);
        assert.equal(await overlay.locator("[data-pf-toggle]").count(), 0, `${width}px: the ⋯ overlay holds no Polyforge button`);
        assert.equal(await overlay.evaluate((el) => getComputedStyle(el).overflowX), "auto", `${width}px: the overlay scrolls, never clips`);
        await more.click();
        await overlay.waitFor({ state: "hidden", timeout: 10_000 });

        await waitFor(async () => {
          const text = await stats.innerText();
          const count = await stats.evaluate((el) => [...el.querySelectorAll("span")].filter((node) => (
            node.classList.contains("mobile-session-stat-io")
            || node.classList.contains("mobile-session-stat-cost")
            || (node.children.length === 0 && !node.closest("svg"))
          )).length);
          if (text.includes("12.3M") && text.includes("$12.34") && text.includes("42%") && count === 4) return true;
          throw new Error(`${count} items, ${JSON.stringify(text)}`);
        }, `${width}px: all four stats items render`, 30_000);
        await setStatsScroll(page, stats, 0);
        const start = await statsSnapshot(stats);
        // without the retired chat-id item the strip may fit;
        // either way no item is clipped at both scroll ends (checked below).
        assert.equal(start.items.length, 4, `${width}px: all four stats items render`);
        assert.ok(start.items[0].className.includes("mobile-session-stat-io"), `${width}px: the first stat is ↑input`);
        assert.ok(insideStrip(start.items[0], start.strip), `${width}px: the start position shows ↑input fully`);
        await setStatsScroll(page, stats, start.scrollWidth);
        const end = await statsSnapshot(stats);
        for (const [index, item] of start.items.entries()) {
          assert.ok(insideStrip(item, start.strip) || insideStrip(end.items[index], end.strip),
            `${width}px: ${JSON.stringify(item.text)} is clipped at both ends`);
        }
        await stats.click();
        await page.locator(".session-info-popover").first().waitFor({ state: "visible", timeout: 10_000 });
        assert.equal(await stats.getAttribute("aria-pressed"), "true", `${width}px: a tap on the stats still opens Session info`);
      });
    }
  });

  if (errors.length) console.log(`\nBROWSER ERRORS: ${errors.join(" | ")}`);
  if (failures.length) {
    console.log(`\nFAILED ${failures.join(", ")} — ${failures.length} scenario(s) red`);
    process.exitCode = 1;
  } else {
    console.log("\nPASS all authored panel-213 scenarios");
  }
} finally {
  await h.close();
}