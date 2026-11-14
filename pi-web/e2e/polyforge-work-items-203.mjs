// carries: components/PolyforgeWorkItems*, components/PolyforgeSearchBar*, lib/polyforge-work-items*, app/api/polyforge/work-items/*, app/api/polyforge/polyforge-work-items-routes*
// — the Work panel's third, read-only 工单列表 sub-tab.
//
// Authored pre-code against artifacts/spec.md (Requirements 1–11) and
// artifacts/plan.md; Requirement 12 (real-data reconciliation) is carried by
// the capsule-only replay documented in artifacts/e2e-scenarios.md because it
// needs the owner-captured snapshots, which never enter this repository.
//
// The suite starts its own `next dev` on a stub `pf` whose merged `queue` and
// `file show` answers copy the real field shapes (content, attrs, project_id,
// parent_id, seq, session_* included). Every pf argv is appended to a ledger,
// so the one-read and one-detail-read disciplines are asserted, not assumed.
//
// Baseline posture: the pre-change tree has no third sub-tab and no work-items
// routes, so every kept scenario is assertion-level RED. A passing scenario
// prints its binding marker `PASS S203-<n>`.
//
//   node e2e/polyforge-work-items-203.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { request } from "node:http";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { openPolyforgeDock, polyforgeDock as dock, waitForPolyforgeToggle } from "./polyforge-dock.mjs";
import { COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, root, suiteDist, writeFileAtomic, writeHostSession } from "./harness.mjs";


const distDir = suiteDist("polyforge-work-items-203");
assertNoDevServer(distDir);
rmSync(join(root, distDir), { force: true, recursive: true });

const work = mkdtempSync(join(tmpdir(), "pi-web-pf-items-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-items-session";
mkdirSync(project);
writeHostSession(agentDir, { id: SESSION, cwd: project, text: "E2E pf items host session", timestamp: "2026-10-31T00:00:00.000Z" });

// ---- fixtures: real pf queue/file-show field shapes plus dropped sentinels ----
const CONTENT_SENTINEL = "CONTENT-SENTINEL-203";
const ATTRS_SENTINEL = "ATTRS-SENTINEL-203";
const PROJECT_ID = "prj_SENTINEL_203";
const PARENT_ID = "wi_PARENT_SENTINEL_203";
const SESSION_SENTINEL = "SESSION-SENTINEL-203";
const REPO_SENTINEL = "REPO-SENTINEL-203";
const LABEL_SENTINEL = "LABEL-SENTINEL-203";
const at = (minute) => new Date(Date.UTC(2026, 9, 31, 0, minute)).toISOString();
const item = (n, projectName, status, minute, goal, extra = {}) => ({
  id: `wi_203E2E${String(n).padStart(4, "0")}`, slug: `${projectName}-p${String(n).padStart(4, "0")}`,
  project: projectName, status, priority: n % 2 ? "high" : "normal", bundle_name: n % 3 ? null : `feature-${n}`,
  goal, created_at: at(minute), content: `${CONTENT_SENTINEL} body ${n}`, attrs: { sentinel: ATTRS_SENTINEL },
  project_id: PROJECT_ID, parent_id: PARENT_ID, seq: n, clean_pause: false, repo_inventory: [REPO_SENTINEL],
  waiting_on: null, waiting_for: n === 2 ? [{ slug: "ieops#1", status: "queued" }] : undefined,
  session_fingerprint: SESSION_SENTINEL, session_machine: "MACHINE-SENTINEL-203", session_started_at: at(minute),
  labels: [LABEL_SENTINEL], milestone: "MILESTONE-203", reporter: "alice", assignee: "bob",
  updated_at: at(minute), closed_at: null, ...extra,
});
const baseItems = () => [
  item(1, "workbench", "wrapped", 1, "GOAL-A wraps an old ticket"),
  item(2, "polyforge", "running", 2, "GOAL-B is running now"),
  item(3, "workbench", "queued", 3, "GOAL-C waits in the queue"),
  item(4, "ieops", "failed", 4, "GOAL-D failed yesterday"),
  item(5, "workbench", "canceled", 5, "GOAL-E was canceled"),
  item(6, "polyforge", "paused", 6, "GOAL-F is paused"),
  item(7, "ieops", "blocked", 7, "GOAL-G is blocked"),
];
const manyItems = () => Array.from({ length: 1162 }, (_, i) => {
  const projectName = i % 2 ? "ieops" : "workbench";
  return item(1000 + i, projectName, i % 5 ? "wrapped" : "running", 0, `PERF-GOAL-${String(i + 1).padStart(4, "0")}`,
    { id: `wi_PERF${String(i + 1).padStart(4, "0")}`, slug: `${projectName}-perf-${String(i + 1).padStart(4, "0")}` });
});
const capItems = () => [
  ...Array.from({ length: 500 }, (_, i) => item(2000 + i, "ieops", "wrapped", 0, `CAP-GOAL-${String(i + 1).padStart(3, "0")}`,
    { id: `wi_CAP${String(i + 1).padStart(4, "0")}`, slug: `demo-cap-${String(i + 1).padStart(4, "0")}` })),
  item(3001, "workbench", "running", 1, "CAP-WORKBENCH-ONE", { id: "wi_CAP9001", slug: "workbench-cap-1" }),
  item(3002, "polyforge", "queued", 2, "CAP-POLYFORGE-ONE", { id: "wi_CAP9002", slug: "polyforge-cap-1" }),
];
const longContent = "x".repeat(300_000) + "\nE2E-TAIL-AFTER-CAP";
const detail = (content = "Detail body of") => ({
  ...item(2, "polyforge", "running", 2, "GOAL-B is running now"),
  content, content_len: Buffer.byteLength(content, "utf8"),
  waiting_for: [{ slug: "ieops#1", status: "queued" }], waiting_on: null,
  labels: [LABEL_SENTINEL], milestone: "MILESTONE-203", reporter: "alice", assignee: "bob", updated_at: at(2), closed_at: null,
});
const passportItems = () => [
  item(11, "workbench", "wrapped", 11, "PASSPORT-KEEP", { id: "wi_PASS0001", slug: "workbench-pass-1" }),
  item(12, "polyforge", "running", 12, "PASSPORT-DEGRADE", { id: "wi_PASS0002", slug: "polyforge-pass-2",
    goal: 42, created_at: 1234, status: null, bundle_name: { bad: true } }),
  item(13, "workbench", "queued", 13, "PASSPORT-BAD-SLUG", { id: "wi_PASS0003", slug: 13 }),
  item(14, "ieops", "failed", 14, "PASSPORT-BAD-ID", { id: "not-a-work-item", slug: "demo-pass-4" }),
];

// ---- stub pf ----
const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const argvLog = join(stubDir, "argv.log");
const fixture = (patch = {}) => ({
  version: "v2.20.13", mode: "ok", detailMode: "ok", ws: project,
  queue: { running: 2, max_concurrent: 6 }, jobs: [],
  items: baseItems(), total: 7, detail: detail(), ...patch,
});
const seed = (patch = {}) => { writeFileAtomic(stateFile, JSON.stringify(fixture(patch))); writeFileSync(argvLog, ""); };
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename)); const args = process.argv.slice(2);
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify(args) + "\\n");
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const out = (v) => { const b = Buffer.from(JSON.stringify(v) + "\\n"); let o = 0;
  while (o < b.length) { try { o += fs.writeSync(1, b, o); } catch (e) { if (e.code !== "EAGAIN") throw e; } } process.exit(0); };
if (args.includes("--version")) { process.stdout.write(s.version + "\\n"); process.exit(0); }
const rest = args.filter((a) => a !== "--json");
const flag = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] : undefined; };
if (rest[0] === "ops" && rest[1] === "status") out({ machine_id: "m-e2e", workspaces: [s.ws], jobs: s.jobs, queue: s.queue, tasks: { running: 0, completed: 0 } });
if (rest[0] === "config" && rest[1] === "workspace") out(flag("--cwd") === s.ws ? { mapped: true, workspace: s.ws } : { mapped: false, workspace: null });
if (rest[0] === "config" && rest[1] === "list") out({ items: [{ key: "user_id", value: "u_e2e", source: "file" },
  { key: '[workspace."' + s.ws + '"]', section: true, value: "workbench, polyforge" }] });
if (rest[0] === "msg" && rest[1] === "list") out({ conversations: [] });
if (rest[0] === "msg" && rest[1] === "contacts") out({ contacts: [] });
if (rest[0] === "recall") out({ items: [], total: 0 });
if (rest[0] === "task" && rest[1] === "list") out({ tasks: [], now: new Date().toISOString() });
if (rest[0] === "queue") {
  if (s.mode === "fail") { process.stderr.write("queue: project ieops: do-not-show-half-the-table\\n"); process.exit(1); }
  if (s.mode === "nomap") { process.stderr.write("pf: no [workspace] mapping for this directory\\n"); process.exit(1); }
  if (s.mode === "malformed") { process.stdout.write("not a decodable queue document\\n"); process.exit(0); }
  out(s.total === null ? { items: s.items } : { items: s.items, total: s.total });
}
if (rest[0] === "file" && rest[1] === "show") {
  if (s.detailMode === "fail") { process.stderr.write("file show: daemon refused\\n"); process.exit(1); }
  out(s.detail);
}
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
const calls = () => readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line).filter((a) => a !== "--json"));
const queues = () => calls().filter((a) => a[0] === "queue");
const fileShows = () => calls().filter((a) => a[0] === "file" && a[1] === "show");
const itemVerbs = () => queues().length + fileShows().length;
const absentPath = (process.env.PATH || "").split(delimiter).map((dir, n) => {
  if (!existsSync(join(dir, "pf"))) return dir;
  const farm = join(work, `nopf-${n}`); mkdirSync(farm);
  for (const name of readdirSync(dir)) if (name !== "pf") symlinkSync(join(dir, name), join(farm, name));
  return farm;
}).join(delimiter);
const json = (text) => { try { return JSON.parse(text); } catch { return undefined; } };
// ---- scenario bookkeeping and page helpers ----
const failures = [];
const check = createCheck({ failures });
const RE_WORK = /工单|工單|work/i, RE_TASKS = /任务|任務|tasks/i, RE_ITEMS = /工单列表|工單列表|work items/i, RE_MEMORY = /记忆|記憶|memory/i;
const BARRIER = "/__e2e_request_barrier";
let base;
const topTab = (page, re) => dock(page).locator('[data-pf-tabstrip="top"] [role="tab"]').filter({ hasText: re }).first();
const segments = (page) => dock(page).locator('[data-pf-tabstrip="work"] [role="tab"]');
const segment = (page, re) => segments(page).filter({ hasText: re }).first();
const optionTexts = (select) => select.locator("option").allInnerTexts().then((rows) => rows.map((row) => row.trim()));
const optionValues = (select) => select.locator("option").evaluateAll((rows) => rows.map((row) => row.value));
const selectedLabel = (select) => select.evaluate((el) => el.options[el.selectedIndex]?.textContent.trim() ?? "");
const row = (panel, text) => panel.getByText(text, { exact: true }).first();
const rowButton = (panel, text) => panel.getByRole("button").filter({ hasText: text }).first();
async function findSelect(panel, firstOption) {
  const selects = panel.getByRole("combobox");
  for (let index = 0; index < await selects.count(); index += 1) {
    if ((await optionTexts(selects.nth(index)))[0] === firstOption) return selects.nth(index);
  }
  return null;
}
const h = createHarness({
  locale: "zh-CN",
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN" },
  errors: ["pageerror", "console", "5xx"],
  session: SESSION,
  ready: "E2E pf items host session",
  async setup(page) {
    page.__pf = [];
    await page.route((url) => url.pathname === BARRIER, (route) => route.fulfill({ status: 204 }));
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.pathname.startsWith("/api/polyforge/")) page.__pf.push(url.pathname);
    });
  },
  // Each server gets its own dist dir, nested so e2e/all.mjs clears it with the suite dir.
  server: (name, path = `${stubDir}${delimiter}${process.env.PATH}`) => ({ name, logFile: join(work, `server-${name}.log`), distDir: join(distDir, name),
    env: { PATH: path, HOME: work, PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: "" } }),
  started: (started) => { base = started.base; },
});
const { errors, startServer, withPage } = h;
const waitFor = createWaitFor({ ms: 20_000 });
async function openDock(page) {
  if ((await dock(page).count()) > 0 && await dock(page).isVisible().catch(() => false)) return dock(page);
  return openPolyforgeDock(page);
}
async function openWork(page) {
  await openDock(page);
  await waitFor(async () => (await topTab(page, RE_WORK).count()) > 0, "a Work top-level tab exists", NAV_MS);
  await topTab(page, RE_WORK).click();
  await waitFor(async () => (await segments(page).count()) >= 2, "the Work sub-tab segments exist", NAV_MS);
  return dock(page);
}
const requestBarrier = (page) => page.evaluate(async (url) => { await fetch(url, { cache: "no-store" }); }, BARRIER);
const statusPolls = (page) => page.__pf.filter((pathname) => pathname === "/api/polyforge/status").length;
const listRequests = (page) => page.__pf.filter((pathname) => pathname === "/api/polyforge/work-items").length;
async function afterStatusPolls(page, n, label) {
  const seen = statusPolls(page);
  await waitFor(() => statusPolls(page) >= seen + n, `${label}: ${n} more status poll(s)`, 20_000);
  await requestBarrier(page);
}
async function openItems(page) {
  await openWork(page);
  const before = listRequests(page);
  // At ≤640px the Work rail is icon-only; its tab is named by aria-label, so
  // fall back to the accessible name when the visible label is gone.
  const tab = segment(page, RE_ITEMS)
    .or(dock(page).locator('[data-pf-tabstrip="work"]').getByRole("tab", { name: RE_ITEMS }))
    .first();
  await tab.waitFor({ timeout: NAV_MS });
  await tab.click();
  await waitFor(() => listRequests(page) > before, "the items sub-tab issues its list read", NAV_MS);
  await requestBarrier(page);
  return dock(page);
}
async function openMemory(page) {
  await openDock(page);
  const tab = topTab(page, RE_MEMORY);
  await tab.waitFor({ timeout: 15_000 });
  await tab.click();
  const bar = page.locator('[data-pf-searchbar]').first();
  await bar.waitFor({ timeout: 20_000 });
  return bar;
}
// The shared search-bar's measurable shape (row 6): the input,
// each select and the square action are compared control type by control
// type; the container and filter rows are measured for the ≤640px layout.
const barShape = (bar) => bar.evaluate((root) => {
  const metric = (el) => {
    const style = getComputedStyle(el); const rect = el.getBoundingClientRect();
    return { height: Math.round(rect.height * 100) / 100, borderRadius: style.borderRadius,
      borderWidth: style.borderTopWidth, fontSize: style.fontSize, top: Math.round(rect.top * 100) / 100 };
  };
  const row = root.querySelector('[data-pf-searchbar-row]');
  const filters = root.querySelector('[data-pf-searchbar-filters]');
  const input = row.querySelector('input[type="search"]');
  const action = row.querySelector('[data-pf-searchbar-action]');
  const children = [...filters.children].map((el) => {
    const rect = el.getBoundingClientRect(); return { top: Math.round(rect.top), height: Math.round(rect.height * 100) / 100 };
  });
  const rows = {};
  for (const child of children) (rows[child.top] ??= []).push(child.height);
  const controls = [...filters.querySelectorAll('input:not([type="checkbox"]), select')].map((el) => metric(el).height);
  return {
    input: metric(input), action: metric(action),
    selects: [...filters.querySelectorAll('select')].map((el) => metric(el)),
    filterRowCounts: Object.values(rows).map((heights) => heights.length),
    filterHeights: controls,
    scrollWidth: root.scrollWidth, clientWidth: root.clientWidth,
  };
});
const goalCount = (panel, re) => panel.getByText(re).count();
const rawGet = (path, host) => new Promise((resolve, reject) => {
  const req = request({ host: "127.0.0.1", port: Number(new URL(base).port), path, headers: { Host: host }, setHost: false }, (res) => {
    const chunks = []; res.on("data", (c) => chunks.push(c));
    res.on("end", () => resolve({ status: res.statusCode ?? 0, text: Buffer.concat(chunks).toString("utf8") }));
  });
  req.on("error", reject); req.end();
});
const get = async (path) => {
  const response = await fetch(`${base}${path}`, { signal: AbortSignal.timeout(60_000) });
  return { status: response.status, text: await response.text() };
};

try {
  await h.launchBrowser(() => chromium.launch());
  await startServer("main");
  // The warm-up also waits, at the cold ceiling, for the toggle: its status probe's route compiles
  // cold on first use, and under a loaded full bar that outlasted S203-1's NAV_MS toggle wait
  // (verification s17).
  seed();
  await withPage({ gotoTimeout: COLD_MS, readyTimeout: COLD_MS }, (page) => waitForPolyforgeToggle(page, COLD_MS));

  await check("S203-1", async () => {
    seed();
    await withPage({}, async (page) => {
      const panel = await openItems(page);
      // intentional fourth sub-tab: 待集成 joins the Work rail after 工单列表.
      assert.deepEqual((await segments(page).allInnerTexts()).map((t) => t.trim().split(/\s/)[0]), ["作业", "任务", "工单列表", "待集成"], "the Work strip lists 作业, 任务, 工单列表, 待集成 in order");
      // The work-items route answers its first read cold in this page.
      await row(panel, "GOAL-B is running now").waitFor({ timeout: COLD_MS });
      assert.equal(await segment(page, RE_TASKS).getAttribute("aria-selected"), "false", "selecting 工单列表 deselects 任务");
    });
    for (const [locale, label] of [["en", "Work items"], ["zh-TW", "工單列表"]]) {
      await withPage({ locale, storage: { "pi-locale": locale } }, async (page) => {
        await openWork(page);
        // The label line is read once it has rendered (bounded); the assertion below is unchanged.
        await waitFor(async () => (await segment(page, new RegExp(label, "i")).innerText()).trim().split("\n")[0].trim() !== "", `${locale}: the third sub-tab's label line renders`, NAV_MS);
        // Owner ruling 2026-10-31 (authored-check-defects-203): compare the whole label line (the badge sits on its own line), not the first whitespace token.
        assert.equal((await segment(page, new RegExp(label, "i")).innerText()).trim().split("\n")[0].trim(), label, `${locale} labels the third sub-tab ${label}`);
      });
    }
    // owner-intended change（重立自 #209）: at ≤640px the three sub-tabs
    // are a 40px vertical icon rail on the right (was a horizontal bar above
    // the main area), each button named by its tab copy (aria-label/title).
    await withPage({ viewport: { width: 600, height: 900 } }, async (page) => {
      const panel = await openWork(page);
      // intentional fourth sub-tab: 待集成 joins the Work rail after 工单列表.
      assert.equal(await segments(page).count(), 4, "at ≤640px the Work rail carries four buttons");
      const strip = dock(page).locator('[data-pf-tabstrip="work"]');
      assert.equal(await strip.getAttribute("aria-orientation"), "vertical", "the mobile rail is vertical, not a horizontal bar");
      const names = await segments(page).evaluateAll((nodes) => nodes.map((node) => [node.getAttribute("aria-label"), node.getAttribute("title")]));
      assert.deepEqual(names, [["作业", "作业"], ["任务", "任务"], ["工单列表", "工单列表"], ["待集成", "待集成"]], "the four rail buttons are named 作业, 任务, 工单列表, 待集成 in order");
      // Geometry is read once the rail is laid out (a bounded condition wait):
      // a read racing layout under host load sees 0-size boxes.
      await waitFor(async () => (await strip.getAttribute("aria-orientation")) === "vertical"
        && (await segments(page).evaluateAll((nodes) => nodes.every((node) => { const rect = node.getBoundingClientRect(); return rect.width > 0 && rect.height > 0; }))),
      "the mobile rail is laid out", NAV_MS);
      const boxes = await segments(page).evaluateAll((nodes) => nodes.map((node) => {
        const rect = node.getBoundingClientRect(); return { x: rect.x, y: rect.y, width: rect.width, height: rect.height, right: rect.right };
      }));
      assert.ok(boxes.every((box) => Math.abs(box.x - boxes[0].x) < 2), `the rail buttons form one vertical rail (${JSON.stringify(boxes)})`);
      assert.ok(boxes[0].y < boxes[1].y && boxes[1].y < boxes[2].y && boxes[2].y < boxes[3].y, `the rail stacks 作业, 任务, 工单列表, 待集成 top to bottom (${JSON.stringify(boxes)})`);
      assert.ok(boxes.every((box) => box.width > 0 && box.height > 0), "all four buttons are rendered controls");
      const before = listRequests(page);
      const items = strip.getByRole("tab", { name: RE_ITEMS }).first();
      await items.click();
      await waitFor(() => listRequests(page) > before, "the mobile third rail button is selectable", NAV_MS);
      assert.equal(await items.getAttribute("aria-selected"), "true", "the mobile 工单列表 button is selected");
      await row(panel, "GOAL-B is running now").waitFor({ timeout: NAV_MS });
      const goal = await row(panel, "GOAL-B is running now").boundingBox();
      assert.ok(goal && goal.x + goal.width <= boxes[0].x + 2, `the rail sits right of the main work area (goal end ${goal ? goal.x + goal.width : null}, rail x ${boxes[0].x})`);
      const sheet = await dock(page).boundingBox();
      assert.ok(sheet && boxes[0].right >= sheet.x + sheet.width - 2, `the rail is on the sheet's right edge (rail end ${boxes[0].right}, sheet end ${sheet ? sheet.x + sheet.width : null})`);
    });
  });

  await check("S203-2", async () => {
    seed();
    await withPage({}, async (page) => {
      const panel = await openItems(page);
      await row(panel, "GOAL-B is running now").waitFor({ timeout: 20_000 });
      assert.equal(queues().length, 1, `the first open spawns exactly one queue read (ledger ${JSON.stringify(queues())})`);
      assert.deepEqual(queues()[0], ["queue"], "the read carries no --project, --status or --limit");
      await segment(page, RE_TASKS).click();
      await delay(1000);
      await segment(page, /作业|jobs/i).click();
      await requestBarrier(page);
      assert.equal(queues().length, 1, "showing Jobs or Tasks makes no work-item read");
      await segment(page, RE_ITEMS).click();
      await waitFor(() => queues().length === 2, "re-opening the sub-tab reads exactly once", 20_000);
      await requestBarrier(page);
      assert.equal(queues().length, 2, "the re-open makes exactly one more queue read");
    });
  });

  await check("S203-3", async () => {
    seed({ items: passportItems(), total: 99 });
    const response = await get("/api/polyforge/work-items");
    assert.equal(response.status, 200, `the list route answered ${response.status}`);
    const body = json(response.text); assert.ok(body, "the list route answers JSON");
    assert.equal(body.total, 99, "total is pf's total");
    assert.equal(body.items.length, 2, "a row without a usable id or slug is omitted");
    const allowed = ["bundle_name", "created_at", "goal", "id", "priority", "project", "slug", "status"];
    for (const kept of body.items) assert.deepEqual(Object.keys(kept).sort(), allowed, `a list row carries exactly the eight whitelisted keys, got ${Object.keys(kept)}`);
    const bySlug = Object.fromEntries(body.items.map((r) => [r.slug, r]));
    assert.equal(bySlug["workbench-pass-1"].goal, "PASSPORT-KEEP", "the usable row survives");
    assert.notEqual(bySlug["polyforge-pass-2"].goal, 42, "a wrong-typed goal is dropped or emptied, never coerced");
    assert.ok([null, "", undefined].includes(bySlug["polyforge-pass-2"].created_at), "a wrong-typed created_at is not coerced");
    for (const dropped of ["content", "attrs", "project_id", "session_fingerprint", "parent_id", "seq", "clean_pause", "repo_inventory", "waiting_for", "waiting_on"]) {
      for (const kept of body.items) assert.ok(!(dropped in kept), `the list passport drops ${dropped}`);
    }
    for (const sentinel of [CONTENT_SENTINEL, ATTRS_SENTINEL, PROJECT_ID, PARENT_ID, SESSION_SENTINEL, REPO_SENTINEL, LABEL_SENTINEL]) {
      assert.ok(!response.text.includes(sentinel), `the list response never carries ${sentinel}`);
    }
    seed({ items: passportItems(), total: null });
    const fallback = json((await get("/api/polyforge/work-items")).text);
    assert.equal(fallback.total, fallback.items.length, "an absent pf total falls back to the sanitized row count");
    assert.equal(fallback.items.length, 2, "wrong-typed fields do not resurrect omitted rows");
  });

  await check("S203-4", async () => {
    for (const [mode, reason] of [["fail", /queue: project ieops/], ["nomap", /workspace|mapping/i], ["malformed", /decod|parse|unavailable|queue/i]]) {
      seed({ mode });
      const response = await get("/api/polyforge/work-items"); const body = json(response.text);
      assert.equal(response.status, 200, `${mode}: the list route answers 200, got ${response.status}`);
      assert.ok(body, `${mode}: the list route answers JSON`);
      assert.deepEqual(body.items, [], `${mode}: the whole table is empty, never a partial one`);
      assert.equal(body.total, 0, `${mode}: the total is zero`);
      assert.match(String(body.reason ?? body.error ?? ""), reason, `${mode}: carries pf's own bounded reason (got ${JSON.stringify(body.reason ?? body.error)})`);
      assert.ok(!response.text.includes("GOAL-B"), `${mode}: no successful project's rows leak through`);
    }
  });

  await check("S203-5", async () => {
    seed({ items: capItems(), total: 502 });
    await withPage({}, async (page) => {
      const panel = await openItems(page);
      await row(panel, "CAP-WORKBENCH-ONE").waitFor({ timeout: 30_000 });
      const text = await panel.innerText();
      assert.match(text, /\b502\b/, "the header shows the total");
      const newest = await row(panel, "CAP-POLYFORGE-ONE").boundingBox();
      const older = await row(panel, "CAP-WORKBENCH-ONE").boundingBox();
      assert.ok(newest && older && newest.y < older.y, "rows are globally created_at descending (newest first)");
      assert.ok(text.includes("仅显示最近 500 条"), "a project with exactly 500 rows triggers the cap hint");
      const option = await findSelect(panel, "全部项目");
      assert.ok(option, "the project filter renders");
      assert.ok((await optionTexts(option)).some((label) => label.includes("仅显示最近 500 条")), "the capped project's option carries the same hint");
    });
    seed({ items: capItems().filter((r) => r.project !== "ieops").concat(
      Array.from({ length: 499 }, (_, i) => item(4000 + i, "ieops", "wrapped", 0, `NOCAP-GOAL-${i}`,
        { id: `wi_NOCAP${i}`, slug: `demo-nocap-${i}` }))), total: 501 });
    await withPage({}, async (page) => {
      const panel = await openItems(page);
      await row(panel, "CAP-WORKBENCH-ONE").waitFor({ timeout: 30_000 });
      assert.ok(!(await panel.innerText()).includes("仅显示最近 500 条"), "499 rows do not trigger the cap hint");
    });
  });

  await check("S203-6", async () => {
    seed({ items: manyItems(), total: 1162 });
    await withPage({}, async (page) => {
      const panel = await openItems(page);
      await waitFor(async () => (await goalCount(panel, /^PERF-GOAL-\d{4}$/)) === 1162, "all 1162 rows render", 60_000);
      assert.equal(await goalCount(panel, /^PERF-GOAL-\d{4}$/), 1162, "the one scrolling list holds every row, no virtualisation");
      assert.equal(await panel.getByRole("button", { name: /next|load more|下一页|加载更多/i }).count(), 0, "no paging or load-more control exists");
      const read = queues().length;
      const project = await findSelect(panel, "全部项目");
      assert.ok(project, "the project filter renders at scale");
      await project.selectOption({ label: "ieops" });
      assert.equal(await goalCount(panel, /^PERF-GOAL-\d{4}$/), 581, "filtering narrows the same rendered list");
      const search = panel.getByRole("searchbox").or(panel.getByPlaceholder(/搜索|search/i)).first();
      await search.fill("PERF-GOAL-1162");
      assert.equal(await goalCount(panel, /^PERF-GOAL-\d{4}$/), 1, "search narrows the large list");
      await requestBarrier(page);
      assert.equal(queues().length, read, "filtering and searching issue no pf read");
    });
  });

  await check("S203-7", async () => {
    seed();
    await withPage({}, async (page) => {
      const panel = await openItems(page);
      await row(panel, "GOAL-B is running now").waitFor({ timeout: 20_000 });
      const status = await findSelect(panel, "全部状态"); const project = await findSelect(panel, "全部项目");
      assert.ok(status && project, "status and project filters render");
      assert.equal((await optionTexts(status)).length, 8, "全部状态 plus the seven statuses");
      assert.deepEqual((await optionValues(status)).slice(1), ["queued", "running", "paused", "blocked", "wrapped", "failed", "canceled"], "the seven real statuses are offered");
      const projects = await optionTexts(project);
      assert.equal(projects[0], "全部项目", "project defaults to 全部项目");
      assert.deepEqual(projects.slice(1).sort(), ["ieops", "polyforge", "workbench"], "the distinct projects of the loaded list");
      const read = queues().length;
      const search = panel.getByRole("searchbox").or(panel.getByPlaceholder(/搜索|search/i)).first();
      await search.fill(CONTENT_SENTINEL);
      assert.equal(await goalCount(panel, /^GOAL-/), 0, "a term occurring only in content matches no row");
      await search.fill("goal-c");
      assert.equal(await goalCount(panel, /^GOAL-/), 1, "slug/goal/project search is case-insensitive");
      await search.fill("workbench");
      assert.equal(await goalCount(panel, /^GOAL-/), 3, "a project name matches its rows");
      await project.selectOption({ label: "workbench" });
      await status.selectOption("canceled");
      assert.equal(await goalCount(panel, /^GOAL-/), 1, "status and project compose into one predicate");
      await requestBarrier(page);
      assert.equal(queues().length, read, "filtering and searching issue no request");
    });
  });

  await check("S203-8", async () => {
    seed({ queue: { running: 3, max_concurrent: 6 } });
    await withPage({}, async (page) => {
      await openWork(page);
      await afterStatusPolls(page, 2, "before opening the items tab");
      const badges = () => segments(page).evaluateAll((nodes) => nodes.flatMap((n) => [n, ...n.querySelectorAll("[aria-label]")])
        .map((node) => node.getAttribute("aria-label") ?? "").filter((name) => name.includes("运行中的工单")));
      assert.deepEqual(await badges(), [], "before the first list read the items badge is absent");
      await segment(page, RE_ITEMS).click();
      await waitFor(() => listRequests(page) > 0, "the first list read", 20_000);
      // Owner ruling 2026-10-31 (authored-check-defects-203): the badge is the count of status === "running" rows of the read (baseItems holds exactly one), not queue.running (3).
      await waitFor(async () => (await badges()).some((name) => name === "1 个运行中的工单"), "the badge shows the first read's running-row count (1)", 20_000);
      const reads = queues().length;
      // Owner ruling 2026-10-31 (authored-check-ledger-reset-203): change only the state; seed() would also empty the argv ledger.
      writeFileAtomic(stateFile, JSON.stringify(fixture({ queue: { running: 4, max_concurrent: 6 } })));
      await waitFor(() => queues().length === reads + 1, "a count-key change re-reads exactly once", 25_000);
      await requestBarrier(page);
      assert.equal(queues().length, reads + 1, "the count-key change does not cause a second read");
      await afterStatusPolls(page, 2, "after the count-key re-read");
      assert.equal(queues().length, reads + 1, "no polling read follows while nothing changes");
    });
  });

  await check("S203-9", async () => {
    seed();
    await withPage({}, async (page) => {
      const panel = await openItems(page);
      await row(panel, "GOAL-B is running now").waitFor({ timeout: 20_000 });
      const project = await findSelect(panel, "全部项目"); const search = panel.getByRole("searchbox").or(panel.getByPlaceholder(/搜索|search/i)).first();
      await project.selectOption({ label: "polyforge" }); await search.fill("GOAL-B");
      await rowButton(panel, "GOAL-B is running now").click();
      await waitFor(async () => (await panel.innerText()).includes("Detail body of"), "the detail read lands", 20_000);
      assert.deepEqual(fileShows().map((a) => a[2]), ["wi_203E2E0002"], "the selection spawns exactly one file show for that id");
      const text = await panel.innerText();
      for (const want of ["GOAL-B is running now", LABEL_SENTINEL, "MILESTONE-203", "alice", "bob", "ieops#1"]) assert.ok(text.includes(want), `the detail shows ${want}`);
      const reads = queues().length;
      await panel.getByRole("button", { name: /返回|back|←/i }).first().click();
      await row(panel, "GOAL-B is running now").waitFor({ timeout: 20_000 });
      assert.equal(await selectedLabel(project), "polyforge", "back keeps the project filter");
      assert.equal(await search.inputValue(), "GOAL-B", "back keeps the search term");
      assert.equal(queues().length, reads, "back makes no new list read");
    });
  });

  // AC4: terminal work items bound to a bundle the release no
  // longer installs (the four removed lanes, or a name no release shipped) list
  // and open like any other — the panel shows pf's record verbatim and never
  // resolves the installed bundles, so nothing here names a lane specially.
  await check("S426-1", async () => {
    const removed = ["feature-full", "chore", "bug-fix", "autopilot", "never-installed-lane"];
    const rows = removed.map((bundle, i) => item(4260 + i, "workbench", i % 2 ? "canceled" : "wrapped", 40 + i, `REMOVED-LANE-${bundle}`,
      { id: `wi_W426${String(i).padStart(4, "0")}`, slug: `workbench-w426-${i}`, bundle_name: bundle }));
    const body = "Detail body of a removed-lane work item";
    seed({ items: [...baseItems(), ...rows], total: 7 + rows.length,
      detail: { ...rows[0], content: body, content_len: Buffer.byteLength(body, "utf8") } });
    const response = await get("/api/polyforge/work-items");
    assert.equal(response.status, 200, `the list route answered ${response.status}`);
    const list = json(response.text);
    assert.ok(list && !list.error && !list.reason, `the list route answers without a failure: ${response.text.slice(0, 200)}`);
    for (const r of rows) {
      const got = list.items.find((x) => x.id === r.id);
      assert.ok(got, `the list keeps the terminal row on ${r.bundle_name}`);
      assert.equal(got.bundle_name, r.bundle_name, `the row carries its bundle ${r.bundle_name} verbatim`);
      assert.equal(got.status, r.status, `the row on ${r.bundle_name} keeps its ${r.status} status`);
    }
    const seen = errors.length;
    await withPage({}, async (page) => {
      const panel = await openItems(page);
      for (const r of rows) await row(panel, r.goal).waitFor({ timeout: 20_000 });
      const table = await panel.innerText();
      for (const bundle of removed) assert.ok(table.includes(bundle), `the table shows the bundle ${bundle}`);
      await rowButton(panel, rows[0].goal).click();
      await waitFor(async () => (await panel.innerText()).includes(body), "the removed-lane detail read lands", 20_000);
      assert.deepEqual(fileShows().map((a) => a[2]), [rows[0].id], "the detail is one file show for that id");
      assert.ok((await panel.innerText()).includes("feature-full"), "the detail shows its removed bundle");
    });
    assert.deepEqual(errors.slice(seen), [], "no page error, console error or 5xx while listing and opening removed-lane work items");
  });

  await check("S203-10", async () => {
    seed({ detail: detail(longContent) });
    const response = await get("/api/polyforge/work-items/wi_203E2E0002");
    assert.equal(response.status, 200, `the detail route answered ${response.status}`);
    const body = json(response.text); assert.ok(body?.item, "the detail route answers its item");
    assert.ok(Buffer.byteLength(body.item.content, "utf8") <= 262144, "content is capped at 262144 UTF-8 bytes");
    assert.equal(body.item.content_len, Buffer.byteLength(longContent, "utf8"), "content_len is the uncut byte length");
    assert.ok(!body.item.content.includes("E2E-TAIL-AFTER-CAP"), "content is cut on a code-point boundary, never the tail");
    const allowed = ["assignee", "bundle_name", "closed_at", "content", "content_len", "created_at", "goal", "id", "labels", "milestone",
      "priority", "project", "reporter", "slug", "status", "updated_at", "waiting_for", "waiting_on"];
    assert.deepEqual(Object.keys(body.item).sort(), allowed, "the detail carries only the eighteen whitelisted keys");
    for (const dropped of ["attrs", "project_id", "parent_id", "seq", "clean_pause", "repo_inventory", "session_fingerprint"]) assert.ok(!(dropped in body.item), `drops ${dropped}`);
    for (const sentinel of [ATTRS_SENTINEL, PROJECT_ID, PARENT_ID, REPO_SENTINEL, SESSION_SENTINEL]) assert.ok(!response.text.includes(sentinel), `the detail response never carries ${sentinel}`);
    const before = itemVerbs();
    const malformed = await get("/api/polyforge/work-items/not-a-wi-id");
    assert.equal(malformed.status, 400, `a malformed id answers 400, got ${malformed.status}`);
    assert.equal(itemVerbs(), before, "a malformed id spawns no pf");
  });

  await check("S203-11", async () => {
    seed();
    const before = itemVerbs();
    for (const path of ["/api/polyforge/work-items", "/api/polyforge/work-items/wi_203E2E0002"]) {
      const post = await fetch(`${base}${path}`, { method: "POST", signal: AbortSignal.timeout(30_000) });
      assert.equal(post.status, 405, `POST ${path} must be refused as method-not-allowed, got ${post.status}`);
    }
    const put = await fetch(`${base}/api/polyforge/work-items`, { method: "PUT", signal: AbortSignal.timeout(30_000) });
    assert.equal(put.status, 405, `PUT must be refused as method-not-allowed, got ${put.status}`);
    for (const path of ["/api/polyforge/work-items", "/api/polyforge/work-items/wi_203E2E0002"]) {
      const refused = await rawGet(path, "e2e-evil.example.com");
      assert.equal(refused.status, 403, `a cross-host ${path} must answer 403, got ${refused.status}`);
      assert.ok(json(refused.text), "the 403 is JSON");
    }
    assert.equal(itemVerbs(), before, "a refused request spawns no pf");
    assert.ok(!errors.some((line) => /^5\d\d /.test(line)), `no route answers 5xx: ${errors.join(" | ")}`);
  });


  await check("S203-13", async () => {
    seed();
    // the 工单列表 and Memory search bars are one shared shape.
    // The numeric thresholds are the memo's row-6 oracle; on the pre-#258 tree
    // there is no data-pf-searchbar marker and these checks are red.
    for (const viewport of [{ width: 1280, height: 800 }, { width: 375, height: 800 }, { width: 600, height: 900 }]) {
      await withPage({ viewport }, async (page) => {
        const panel = await openItems(page);
        await row(panel, "GOAL-B is running now").waitFor({ timeout: 20_000 });
        const itemsBars = page.locator('[data-pf-searchbar]');
        assert.equal(await itemsBars.count(), 1, `工单列表 carries exactly one data-pf-searchbar at ${viewport.width}px`);
        const items = await barShape(itemsBars.first());
        const memoryBar = await openMemory(page);
        assert.equal(await page.locator('[data-pf-searchbar]').count(), 1, `Memory carries exactly one data-pf-searchbar at ${viewport.width}px`);
        const memory = await barShape(memoryBar);
        for (const key of ["input", "action"]) {
          assert.deepEqual(
            { height: memory[key].height, borderRadius: memory[key].borderRadius, borderWidth: memory[key].borderWidth, fontSize: memory[key].fontSize },
            { height: items[key].height, borderRadius: items[key].borderRadius, borderWidth: items[key].borderWidth, fontSize: items[key].fontSize },
            `${key} geometry matches between 工单列表 and Memory at ${viewport.width}px`,
          );
        }
        assert.equal(memory.selects.length, items.selects.length, "both search bars render the same number of selects");
        for (let index = 0; index < items.selects.length; index += 1) {
          const pick = (control) => ({ height: control.height, borderRadius: control.borderRadius, borderWidth: control.borderWidth, fontSize: control.fontSize });
          assert.deepEqual(pick(memory.selects[index]), pick(items.selects[index]), `select ${index} geometry matches at ${viewport.width}px`);
        }
        if (viewport.width <= 640) {
          for (const [name, shape] of [["工单列表", items], ["Memory", memory]]) {
            assert.ok(shape.scrollWidth <= shape.clientWidth, `${name} search bar does not overflow at ${viewport.width}px (${shape.scrollWidth}/${shape.clientWidth})`);
            assert.ok(Math.abs(shape.input.top - shape.action.top) <= 2, `${name} input and action share row 1 at ${viewport.width}px (${shape.input.top}/${shape.action.top})`);
            assert.ok(shape.action.height >= 40, `${name} action is ≥40px high at ${viewport.width}px (${shape.action.height})`);
            for (const height of [shape.input.height, ...shape.selects.map((select) => select.height), ...shape.filterHeights]) {
              assert.ok(height >= 40, `${name} control is ≥40px high at ${viewport.width}px (${height})`);
            }
            assert.ok(shape.filterRowCounts.every((count) => count <= 2), `${name} filter rows hold at most two controls at ${viewport.width}px (${JSON.stringify(shape.filterRowCounts)})`);
          }
        } else {
          for (const [name, shape] of [["工单列表", items], ["Memory", memory]]) {
            assert.equal(shape.input.height, 26, `${name} desktop input is 26px high (${shape.input.height})`);
            assert.equal(shape.action.height, 26, `${name} desktop action is 26px high (${shape.action.height})`);
            assert.equal(shape.input.borderWidth, "1px", `${name} desktop border is 1px`);
            assert.equal(shape.input.borderRadius, "4px", `${name} desktop radius is 4px`);
            assert.equal(shape.input.fontSize, "12px", `${name} desktop font is 12px`);
            for (const select of shape.selects) assert.equal(select.height, 26, `${name} desktop select is 26px high (${select.height})`);
          }
        }
      });
    }
  });

  await check("S203-14", async () => {
    seed();
    await withPage({}, async (page) => {
      const panel = await openItems(page);
      await row(panel, "GOAL-B is running now").waitFor({ timeout: 20_000 });
      const refresh = page.locator('[data-pf-searchbar] [data-pf-searchbar-action]').first();
      assert.equal(await refresh.count(), 1, "the 工单列表 search bar renders the shared ↻ action");
      const reads = listRequests(page); const spawns = queues().length;
      // Hold the answer so the in-flight busy state is observable, then let
      // the one read it represents land.
      const workItemsPath = (url) => url.pathname === "/api/polyforge/work-items";
      const holdWorkItems = async (route) => { await delay(800); await route.continue(); };
      await page.route(workItemsPath, holdWorkItems);
      await refresh.click();
      await waitFor(() => refresh.isDisabled(), "the ↻ button is disabled while the newest read is in flight", 5_000);
      assert.equal(await refresh.getAttribute("aria-busy"), "true", "the ↻ button reports aria-busy while in flight");
      await waitFor(() => listRequests(page) === reads + 1, "one click issues exactly one list read", 20_000);
      await waitFor(async () => !(await refresh.isDisabled()), "the ↻ button re-enables when the newest read lands", 20_000);
      await page.unroute(workItemsPath, holdWorkItems);
      await requestBarrier(page);
      assert.equal(listRequests(page), reads + 1, "one click issues no second list request");
      assert.equal(queues().length, spawns + 1, "one click spawns exactly one queue read");
    });
  });

  await check("S203-12", async () => {
    await h.stop();
    seed({ version: "v2.18.9" });
    await startServer("floor");
    const list = await get("/api/polyforge/work-items"); const item = await get("/api/polyforge/work-items/wi_203E2E0002");
    for (const [label, response] of [["list", list], ["detail", item]]) {
      assert.equal(response.status, 200, `below the floor the ${label} route answers 200, got ${response.status}`);
      const body = json(response.text); assert.ok(body, `below the floor the ${label} route answers JSON`);
      assert.equal(body.capable, false, `below the floor ${label} answers capable:false`);
      assert.match(String(body.reason ?? ""), /2\.19\.0/, `below the floor ${label} names the 2.19.0 reason`);
    }
    assert.equal(itemVerbs(), 0, "below the floor no queue or file verb is spawned");
    await h.stop();
    await startServer("absent", absentPath);
    const absent = await get("/api/polyforge/work-items");
    assert.equal(absent.status, 200, `without pf the list route answers 200, got ${absent.status}`);
    const body = json(absent.text); assert.ok(body, "without pf the list route answers JSON");
    assert.equal(body.pfPresent, false, "without pf the body says pfPresent:false");
    assert.ok(Array.isArray(body.items) && body.items.length === 0, "without pf the item list is empty");
    assert.equal(itemVerbs(), 0, "without pf no queue or file verb is spawned");
  });
} catch (error) {
  failures.push("suite");
  console.log(`FAIL suite: ${String(error?.message ?? error).split("\n")[0]}`);
} finally {
  await h.close();
  if (failures.length) console.log(`\nFAILED ${failures.join(", ")} — ${failures.length} scenario(s) red`);
  else console.log("\nPASS all authored work-item scenarios");
  console.log(`server log: ${join(work, "server-main.log")}`);
}
if (failures.length) process.exitCode = 1;
