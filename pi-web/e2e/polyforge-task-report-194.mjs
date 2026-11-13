// carries: components/PolyforgeTasks*, lib/polyforge-tasks*, app/api/polyforge/tasks/*
// — Polyforge Tasks: the card's report tail line and the reporter filter.
//
// Authored pre-code against artifacts/spec.md (Requirement "The authored e2e
// suite MUST prove tail rendering, both placeholders and reporter
// discrimination on a multi-reporter stub"). The suite starts its own
// `next dev` on a stub `pf` whose task list holds two human reporters (alice,
// bob) plus `machine` rows and a finished/empty/running mix; every pf argv is
// appended to a ledger, so the read discipline (filtering spawns no
// `pf task list`) is asserted, not assumed.
//
// Baseline posture: the pre-change tree forwards only the seven old list keys
// and has neither the tail element nor the reporter select, so every kept
// scenario is assertion-level RED. Each passing scenario prints
// `PASS S194-<n>` (the binding marker).
//
//   node e2e/polyforge-task-report-194.mjs
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { chromium } from "playwright";
import { openPolyforgeDock as openDock, polyforgeDock as dock } from "./polyforge-dock.mjs";
import { NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createWaitFor, root, suiteDist, writeFileAtomic, writeHostSession, createHarness } from "./harness.mjs";

const distDir = suiteDist("polyforge-task-report-194");
assertNoDevServer(distDir);
rmSync(join(root, distDir), { force: true, recursive: true });

const work = mkdtempSync(join(tmpdir(), "pi-web-pf-report-e2e-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(work, "project");
const dataRoot = join(home, ".local", "share", "polyforge");
const SESSION = "e2e-pf-report-session";
mkdirSync(project);
mkdirSync(dataRoot, { recursive: true });
writeHostSession(agentDir, { id: SESSION, cwd: project, text: "E2E pf report host session", timestamp: "2026-10-30T02:00:00.000Z" });

// ---- task fixture: two human reporters + machine, every tail state ----
const ids = {
  R1: "task_00000000000000a1", R2: "task_00000000000000a2", R3: "task_00000000000000a3",
  F2: "task_00000000000000b1", F3: "task_00000000000000b2", F7: "task_00000000000000b3",
  F4: "task_00000000000000b4", F5: "task_00000000000000b5", F6: "task_00000000000000b6",
  F8: "task_00000000000000b7", M1: "task_00000000000000c1", M2: "task_00000000000000c2",
  D1: "task_00000000000000d1", D2: "task_00000000000000d2",
};
const EPOCH = "2026-10-30T02:00:00.000Z";
const ENDED = new Date(Date.now() - 120_000).toISOString();
// pf bounds the tail itself (≤200 code points, over-long → … + last 199); the
// client only clamps visually. The markdown characters prove literal rendering.
const LONG_TAIL = `Report: **BOLD** and \`code\` and [link](x) — ${"a".repeat(420)}`;
const CAPPED_TAIL = "…CAPPED-TAIL-END-0123456789";
const STALE_TAIL = "STALE-RUNNING-TAIL-SENTINEL";
const runningAt = () => new Date(Date.now() - 5_000).toISOString();
const task = (id, projectName, state, summary, extra = {}) => ({
  id, spec: { project: projectName, max_duration_ns: 1_200_000_000_000, max_tokens: 1_000_000, prompt: `PROMPT-${id}` },
  state, prompt_summary: summary, candidate: "pi:e2e/task-model", created_at: EPOCH, tokens_consumed: 12_345, delivered: false,
  started_at: extra.started_at, ended_at: extra.ended_at, wall_consumed_ns: extra.wall_consumed_ns ?? 0,
  report: extra.report, error: extra.error, origin_chat: extra.origin_chat,
  report_tail: extra.report_tail, reporter: extra.reporter,
});
const taskRows = () => [
  task(ids.R1, "workbench", "running", "TASK-ALICE-RUN running alpha", { started_at: runningAt(), reporter: "alice", report_tail: "" }),
  task(ids.R2, "polyforge", "running", "TASK-MACHINE-RUN running bravo", { started_at: runningAt(), reporter: "machine", report_tail: "" }),
  task(ids.R3, "workbench", "running", "TASK-BOB-RUN running charlie", { started_at: runningAt(), reporter: "bob", report_tail: STALE_TAIL }),
  task(ids.F2, "workbench", "completed", "TASK-ALICE-TAIL completed report", { started_at: EPOCH, ended_at: ENDED, wall_consumed_ns: 42_000_000_000,
    reporter: "alice", report_tail: LONG_TAIL, report: "FULL-REPORT-SENTINEL", error: "ERROR-SENTINEL", origin_chat: "ORIGIN-CHAT-SENTINEL" }),
  task(ids.F3, "workbench", "completed", "TASK-MACHINE-EMPTY-TAIL completed report", { started_at: EPOCH, ended_at: ENDED, reporter: "machine", report_tail: "" }),
  task(ids.F7, "workbench", "completed", "TASK-BOB-TAIL completed report", { started_at: EPOCH, ended_at: ENDED, reporter: "bob", report_tail: CAPPED_TAIL }),
  task(ids.F4, "workbench", "capped", "TASK-BOB-WHITESPACE capped report", { started_at: EPOCH, ended_at: ENDED, reporter: "bob", report_tail: "   " }),
  task(ids.F5, "polyforge", "interrupted", "TASK-NOREPORTER interrupted report", { started_at: EPOCH, ended_at: ENDED }),
  task(ids.F6, "polyforge", "failed", "TASK-MACHINE-FAILED failed report", { started_at: EPOCH, ended_at: ENDED, reporter: "machine", report_tail: "FAILED-TAIL-REPORT" }),
  task(ids.F8, "polyforge", "completed", "TASK-NUMERIC-REPORTER completed report", { started_at: EPOCH, ended_at: ENDED, reporter: 42, report_tail: 424242 }),
];
const machineOnlyRows = () => [
  task(ids.M1, "workbench", "running", "TASK-ONLY-MACHINE-RUN", { started_at: runningAt(), reporter: "machine", report_tail: "" }),
  task(ids.M2, "workbench", "completed", "TASK-ONLY-MACHINE-DONE", { started_at: EPOCH, ended_at: ENDED, reporter: "machine", report_tail: "MACHINE-ONLY-TAIL" }),
];
const fieldlessRows = () => [
  task(ids.D1, "workbench", "running", "TASK-FIELDLESS-RUN", { started_at: runningAt() }),
  task(ids.D2, "workbench", "completed", "TASK-FIELDLESS-DONE", { started_at: EPOCH, ended_at: ENDED }),
];
const TASK_DETAIL = {
  ...task(ids.F2, "workbench", "completed", "TASK-ALICE-TAIL completed report", { started_at: EPOCH, ended_at: ENDED, wall_consumed_ns: 42_000_000_000,
    reporter: "alice", report_tail: LONG_TAIL }),
  spec: { project: "workbench", prompt: "TASK-DETAIL-PROMPT", candidates: ["pi:e2e/task-model"], max_duration_ns: 1_200_000_000_000, max_tokens: 1_000_000 },
  report: "FULL-REPORT-SENTINEL", report_len: 20, truncated: false,
};

// ---- stub pf ----
const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const argvLog = join(stubDir, "argv.log");
const fixture = (patch = {}) => ({
  version: "v2.20.13", ws: project, jobs: [], queue: { running: 0, max_concurrent: 6 },
  tasks: { running: 3, completed: 4, capped: 1, failed: 1, interrupted: 1 },
  taskRows: taskRows(), taskDetail: TASK_DETAIL, ...patch,
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
if (rest[0] === "ops" && rest[1] === "status") out({ machine_id: "m-e2e", workspaces: [s.ws], jobs: s.jobs, queue: s.queue, tasks: s.tasks });
if (rest[0] === "config" && rest[1] === "workspace") out(flag("--cwd") === s.ws ? { mapped: true, workspace: s.ws } : { mapped: false, workspace: null });
if (rest[0] === "config" && rest[1] === "list") out({ items: [{ key: "user_id", value: "u_e2e", source: "file" }] });
if (rest[0] === "msg" && rest[1] === "list") out({ conversations: [] });
if (rest[0] === "msg" && rest[1] === "contacts") out({ contacts: [] });
if (rest[0] === "recall") out({ items: [], total: 0 });
if (rest[0] === "task" && rest[1] === "list") out({ tasks: s.taskRows, now: new Date().toISOString() });
if (rest[0] === "task" && rest[1] === "show") out(s.taskDetail);
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
const calls = () => readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line).filter((a) => a !== "--json"));
const taskLists = () => calls().filter((a) => a[0] === "task" && a[1] === "list");

// ---- scenario bookkeeping and page helpers ----
const failures = [];
const check = createCheck({ failures });
const RE_WORK = /工单|work/i, RE_TASKS = /任务|tasks/i;
const BARRIER = "/__e2e_request_barrier";
let base;
const topTab = (page, re) => dock(page).locator('[role="tablist"]').first().getByRole("tab").filter({ hasText: re }).first();
const subTab = (page, re) => dock(page).getByRole("tab", { name: re }).or(dock(page).getByRole("button", { name: re })).last();
const taskBlock = (page, id) => dock(page).locator(`[data-pf-task="${id}"]`).first();
const h = createHarness({
  locale: "zh-CN",
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN" },
  timeout: 8_000,
  errors: [],
  session: SESSION,
  ready: "E2E pf report host session",
  async setup(page) {
    page.__pf = []; page.__taskUrls = [];
    await page.route((url) => url.pathname === BARRIER, (route) => route.fulfill({ status: 204 }));
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.pathname.startsWith("/api/polyforge/")) page.__pf.push(url.pathname);
      if (url.pathname === "/api/polyforge/tasks") page.__taskUrls.push(request.url());
    });
  },
});
const { withPage } = h;
const waitFor = createWaitFor({ ms: 15_000 });
async function openWork(page) {
  await openDock(page);
  await waitFor(async () => (await topTab(page, RE_WORK).count()) > 0, "a 工单 (Work) top-level tab exists", 20_000);
  await topTab(page, RE_WORK).click();
  await waitFor(async () => (await subTab(page, RE_TASKS).count()) > 0, "the 任务 sub-tab control exists", 15_000);
  await subTab(page, RE_TASKS).click();
  return dock(page);
}
const requestBarrier = (page) => page.evaluate(async (url) => { await fetch(url, { cache: "no-store" }); }, BARRIER);
const statusPolls = (page) => page.__pf.filter((pathname) => pathname === "/api/polyforge/status").length;
const taskListRequests = (page) => page.__pf.filter((pathname) => pathname === "/api/polyforge/tasks").length;
async function afterStatusPolls(page, n, label) {
  const seen = statusPolls(page);
  await waitFor(() => statusPolls(page) >= seen + n, `${label}: ${n} more status poll(s)`, 15_000);
  await requestBarrier(page);
}
const visibleIds = async (page) => (await dock(page).locator("[data-pf-task]").evaluateAll((nodes) => nodes.map((node) => node.getAttribute("data-pf-task")))).sort();
const optionTexts = (select) => select.locator("option").allInnerTexts().then((rows) => rows.map((row) => row.trim()));
// The reporter select is the one whose first option is 全部发起人 (project/state
// identify themselves by 全部项目 and 全部); finding it by label keeps the
// existing S14-style scan independent of select count or DOM order.
async function reporterSelect(page) {
  const selects = dock(page).getByRole("combobox");
  const count = await selects.count();
  for (let index = 0; index < count; index += 1) {
    const labels = await optionTexts(selects.nth(index));
    if (labels[0] === "全部发起人") return selects.nth(index);
  }
  return null;
}
async function findSelect(page, firstOption) {
  const selects = dock(page).getByRole("combobox");
  for (let index = 0; index < await selects.count(); index += 1) {
    if ((await optionTexts(selects.nth(index)))[0] === firstOption) return selects.nth(index);
  }
  return null;
}
const selectedLabel = (select) => select.evaluate((el) => el.options[el.selectedIndex]?.textContent.trim() ?? "");
// One leaf element (a text-only node) of a task block: the tail line, a
// placeholder or the prompt summary. No data-* hook: the spec's surface is text.
const leafByText = (block, text) => block.evaluate((el, wanted) => {
  const node = [...el.querySelectorAll("*")].find((child) => child.children.length === 0 && child.textContent === wanted);
  if (!node) return null;
  const style = getComputedStyle(node); const rect = node.getBoundingClientRect();
  const lineHeight = parseFloat(style.lineHeight) || 16;
  return { text: node.textContent, x: rect.x, y: rect.y, width: rect.width, height: rect.height, lineHeight,
    lines: Math.round(rect.height / lineHeight), color: style.color, overflow: style.overflow, overflowY: style.overflowY,
    clamp: style.webkitLineClamp, scrollHeight: node.scrollHeight, clientHeight: node.clientHeight, children: node.children.length };
}, text);
const headingBox = (page, text) => dock(page).evaluate((el, wanted) => {
  const node = [...el.querySelectorAll("div,span")].find((child) => child.children.length === 0 && child.textContent.trim() === wanted);
  if (!node) return null;
  const rect = node.getBoundingClientRect(); return { y: rect.y, height: rect.height };
}, text);
const dimColor = (page) => page.evaluate(() => {
  const probe = document.createElement("span"); probe.style.color = "var(--text-dim)"; probe.style.position = "absolute";
  document.body.appendChild(probe); const color = getComputedStyle(probe).color; probe.remove(); return color;
});

try {
  await h.launchBrowser(() => chromium.launch());
  ({ base } = await h.startServer({ name: "polyforge-task-report-194", logFile: join(work, "server.log"), env: {
    PI_WEB_DIST_DIR: distDir, PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: home, POLYFORGE_DATA_DIR: dataRoot,
    PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: "" } }));

  await check("S194-1", async () => {
    seed();
    const response = await fetch(`${base}/api/polyforge/tasks`, { signal: AbortSignal.timeout(30_000) });
    assert.equal(response.status, 200);
    const body = await response.text(); const rows = JSON.parse(body).tasks;
    const allowed = new Set(["id", "project", "state", "prompt_summary", "started_at", "ended_at", "wall_consumed_ns", "report_tail", "reporter"]);
    for (const row of rows) for (const key of Object.keys(row)) assert.ok(allowed.has(key), `raw field ${key} stays unforwarded`);
    const by = (id) => rows.find((row) => row.id === id);
    assert.equal(by(ids.R1).reporter, "alice", "reporter is forwarded");
    assert.equal(by(ids.R1).report_tail, "", "a running row's empty tail is a supplied value, kept as \"\"");
    assert.equal(by(ids.F2).report_tail, LONG_TAIL, "the server-bounded tail is forwarded verbatim, never re-cut");
    assert.equal(by(ids.F2).reporter, "alice");
    assert.equal(by(ids.F3).report_tail, "", "an empty tail is forwarded, not dropped");
    assert.ok("report_tail" in by(ids.F3) && "reporter" in by(ids.F3), "the empty values are present keys, not absent ones");
    assert.ok(!("report_tail" in by(ids.F8)) && !("reporter" in by(ids.F8)), "a non-string field is not forwarded");
    assert.deepEqual(Object.keys(by(ids.F5)).sort(),
      ["ended_at", "id", "project", "prompt_summary", "started_at", "state", "wall_consumed_ns"],
      "an old-pf row keeps the seven keys and fabricates neither new field");
    for (const sentinel of ["FULL-REPORT-SENTINEL", "ERROR-SENTINEL", "ORIGIN-CHAT-SENTINEL"]) {
      assert.ok(!body.includes(sentinel), `the ${sentinel} raw field never leaves the server`);
    }
    const detail = await (await fetch(`${base}/api/polyforge/tasks/${ids.F2}`, { signal: AbortSignal.timeout(30_000) })).json();
    assert.ok(detail.task, "the detail read answers");
    assert.ok(!("report_tail" in detail.task) && !("reporter" in detail.task), "the detail passport gains neither field");
  });

  await check("S194-2", () => withPage({ storage: { "pi-web:polyforge-dock-width": "460" } }, async (page) => {
    seed(); await openWork(page);
    const block = taskBlock(page, ids.F2); await block.waitFor({ timeout: 30_000 });
    const tail = await leafByText(block, LONG_TAIL);
    assert.ok(tail, `the finished block renders its report tail verbatim (block text: ${JSON.stringify((await block.innerText()).slice(0, 160))})`);
    assert.equal(tail.text, LONG_TAIL, "no client code-point cut and no appended ellipsis: the leaf holds the whole server-bounded string");
    assert.equal(tail.children, 0, "the tail is plain text: Markdown syntax appears literally");
    assert.ok(tail.text.includes("**BOLD**") && tail.text.includes("`code`"), "Markdown is not interpreted");
    const summary = await leafByText(block, "TASK-ALICE-TAIL completed report");
    assert.ok(summary, "the prompt summary renders");
    assert.ok(tail.y >= summary.y + summary.height - 2, `the tail sits below the prompt summary (${tail.y} vs ${summary.y}+${summary.height})`);
    assert.ok(tail.lines <= 2 && tail.height <= tail.lineHeight * 2 + 2,
      `at most two rendered lines (${tail.lines} line(s), ${tail.height}px at ${tail.lineHeight}px)`);
    assert.ok(tail.overflowY === "hidden" || tail.overflow === "hidden", `the rest is visually clipped (overflow ${tail.overflow})`);
    const cappedBlock = taskBlock(page, ids.F7); await cappedBlock.waitFor({ timeout: 20_000 });
    const capped = await leafByText(cappedBlock, CAPPED_TAIL);
    assert.ok(capped, "a capped tail renders verbatim");
    assert.ok(capped.text.startsWith("…") && capped.text.length > 3, "pf's leading … is preserved");
    assert.equal((capped.text.match(/…/g) ?? []).length, 1, "no extra truncation marker is added");
  }));

  await check("S194-3", () => withPage({}, async (page) => {
    seed(); await openWork(page);
    const dim = await dimColor(page);
    const running = taskBlock(page, ids.R1); await running.waitFor({ timeout: 30_000 });
    const placeholder = await leafByText(running, "生成中…");
    assert.ok(placeholder, "a running block shows 生成中… (keyed on state, not on an empty tail)");
    assert.equal(placeholder.color, dim, `the placeholder uses the dim treatment (${placeholder.color})`);
    const stale = taskBlock(page, ids.R3); await stale.waitFor({ timeout: 20_000 });
    const stalePlaceholder = await leafByText(stale, "生成中…");
    assert.ok(stalePlaceholder, "a running row with a stale non-empty tail still shows 生成中…");
    assert.equal(await leafByText(stale, STALE_TAIL), null, "the stale tail is not rendered");
    assert.ok(!(await stale.innerText()).includes(STALE_TAIL), "no raw stale tail text on the running block");
  }));

  await check("S194-4", () => withPage({}, async (page) => {
    seed(); await openWork(page);
    const dim = await dimColor(page);
    const variants = [[ids.F3, "empty"], [ids.F4, "whitespace-only"], [ids.F5, "missing"]];
    for (const [id, label] of variants) {
      const block = taskBlock(page, id); await block.waitFor({ timeout: 30_000 });
      const placeholder = await leafByText(block, "暂无报告");
      assert.ok(placeholder, `the ${label}-tail finished block shows 暂无报告 (block: ${JSON.stringify((await block.innerText()).slice(0, 140))})`);
      assert.equal(placeholder.color, dim, `the ${label} placeholder uses the dim treatment`);
    }
  }));

  await check("S194-5", () => withPage({ storage: { "pi-web:polyforge-dock-width": "460" } }, async (page) => {
    seed(); await openWork(page);
    const block = taskBlock(page, ids.R1); await block.waitFor({ timeout: 30_000 });
    const select = await reporterSelect(page);
    assert.ok(select, "the filter bar offers a reporter select");
    assert.deepEqual(await optionTexts(select), ["全部发起人", "alice", "bob", "机器发起"],
      "全部发起人 first, human names ascending, 机器发起 last");
    const aria = (await select.getAttribute("aria-label")) ?? (await select.getAttribute("title"));
    assert.ok(aria && aria.trim().length > 0, "the select carries a localized aria-label/title like the project/state selects");
    assert.notEqual(aria.trim(), "全部发起人", "the aria-label names the control, not its all-value");
    const project = await findSelect(page, "全部项目"); const state = await findSelect(page, "全部");
    assert.ok(project && state, "the project and state selects are still found by their option labels");
    await project.selectOption({ label: "workbench" });
    await state.selectOption({ label: "已完成" });
    await select.selectOption({ label: "alice" });
    assert.deepEqual(await optionTexts(select), ["全部发起人", "alice", "bob", "机器发起"],
      "the options come from the whole loaded list and do not cascade");
  }));

  await check("S194-6", () => withPage({}, async (page) => {
    seed({ taskRows: machineOnlyRows(), tasks: { running: 1, completed: 1, capped: 0, failed: 0, interrupted: 0 } });
    await openWork(page);
    await taskBlock(page, ids.M1).waitFor({ timeout: 30_000 });
    const select = await reporterSelect(page);
    assert.ok(select, "the reporter select renders for a machine-only list");
    assert.deepEqual(await optionTexts(select), ["全部发起人", "机器发起"], "a machine-only list offers 全部发起人 and 机器发起 only");
    await select.selectOption({ label: "机器发起" });
    assert.deepEqual(await visibleIds(page), [ids.M1, ids.M2].sort(), "机器发起 shows the machine rows");
  }));

  await check("S194-7", () => withPage({ storage: { "pi-web:polyforge-dock-width": "460" } }, async (page) => {
    seed();
    // Fixture readiness (s194-7-fixture-and-stale-lock): as in S194-10,
    // let the shared status snapshot reflect this seed before opening Tasks, so the
    // #163 count-change re-read cannot land during the exactly-one-read check.
    await openDock(page);
    await waitFor(async () => (await topTab(page, RE_WORK).count()) > 0, "a 工单 (Work) top-level tab exists", 20_000);
    await topTab(page, RE_WORK).click();
    await afterStatusPolls(page, 2, "before opening Tasks");
    await waitFor(async () => (await subTab(page, RE_TASKS).count()) > 0, "the 任务 sub-tab control exists", 15_000);
    await subTab(page, RE_TASKS).click();
    await taskBlock(page, ids.R1).waitFor({ timeout: 30_000 });
    const select = await reporterSelect(page);
    assert.ok(select, "the reporter select renders on the multi-reporter list");
    const reads = taskLists().length;
    await select.selectOption({ label: "alice" });
    assert.deepEqual(await visibleIds(page), [ids.R1, ids.F2].sort(), "alice's reporter narrows the list to exactly her rows");
    const runningHeading = await headingBox(page, "正在运行"); const allHeading = await headingBox(page, "全部");
    const r1 = await taskBlock(page, ids.R1).boundingBox(); const f2 = await taskBlock(page, ids.F2).boundingBox();
    assert.ok(runningHeading && allHeading && r1 && f2, "the section headings and both blocks measure");
    assert.ok(r1.y > runningHeading.y && r1.y < allHeading.y, "alice's running row stays in 正在运行 (both sections are filtered)");
    assert.ok(f2.y > allHeading.y, "alice's finished row stays in 全部");
    await select.selectOption({ label: "机器发起" });
    assert.deepEqual(await visibleIds(page), [ids.F3, ids.F6, ids.R2].sort(), "机器发起 shows exactly the machine rows in both sections");
    await select.selectOption({ label: "bob" });
    assert.deepEqual(await visibleIds(page), [ids.F4, ids.F7, ids.R3].sort(), "bob's reporter shows exactly his rows");
    await select.selectOption({ label: "全部发起人" });
    assert.equal((await visibleIds(page)).length, 10, "全部发起人 restores every row the other filters allow");
    await requestBarrier(page);
    assert.equal(taskLists().length, reads, "cycling the reporter selection spawns no pf task list read");
    assert.equal(taskListRequests(page), 1, "and issues no additional list request");
  }));

  await check("S194-8", () => withPage({ storage: { "pi-web:polyforge-dock-width": "460" } }, async (page) => {
    seed(); await openWork(page);
    await taskBlock(page, ids.R1).waitFor({ timeout: 30_000 });
    const select = await reporterSelect(page); const project = await findSelect(page, "全部项目"); const state = await findSelect(page, "全部");
    assert.ok(select && project && state, "the reporter, project and state filters all render");
    const search = dock(page).getByRole("searchbox").or(dock(page).getByPlaceholder(/搜索|search/i)).first();
    await project.selectOption({ label: "workbench" });
    await state.selectOption({ label: "已完成" });
    await search.fill("TAIL");
    await select.selectOption({ label: "alice" });
    assert.deepEqual(await visibleIds(page), [ids.F2], "reporter + project + state + search compose as an intersection");
    await select.selectOption({ label: "全部发起人" });
    assert.deepEqual(await visibleIds(page), [ids.F2, ids.F3, ids.F7].sort(),
      "全部发起人 restores the rows the other three filters allow");
  }));

  await check("S194-9", () => withPage({}, async (page) => {
    seed(); await openWork(page);
    await taskBlock(page, ids.R1).waitFor({ timeout: 30_000 });
    const select = await reporterSelect(page); assert.ok(select, "the reporter select renders");
    await select.selectOption({ label: "alice" });
    assert.deepEqual(await visibleIds(page), [ids.R1, ids.F2].sort(), "the selection narrows the list");
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.getByText("E2E pf report host session").first().waitFor({ state: "attached", timeout: NAV_MS });
    await openWork(page);
    await taskBlock(page, ids.R1).waitFor({ timeout: 30_000 });
    const after = await reporterSelect(page); assert.ok(after, "the reporter select renders after the reload");
    assert.equal(await selectedLabel(after), "全部发起人", "a reload resets the reporter selection to 全部发起人");
    assert.equal((await visibleIds(page)).length, 10, "and the list shows every reporter again");
    const storage = await page.evaluate(() => ({ local: { ...localStorage }, session: { ...sessionStorage } }));
    for (const [key, value] of [...Object.entries(storage.local), ...Object.entries(storage.session)]) {
      assert.ok(!/reporter/i.test(key) && !/reporter/i.test(value), `no storage key/value holds a reporter selection (${key})`);
      assert.ok(!["alice", "bob", "machine"].includes(String(value).trim()), `no storage value holds a reporter (${key})`);
    }
    for (const url of page.__taskUrls) assert.ok(!/reporter/i.test(url), `no list request carries a reporter (${url})`);
    assert.ok(!/reporter/i.test(page.url()), "the URL carries no reporter selection");
  }));

  await check("S194-10", () => withPage({}, async (page) => {
    seed({ taskRows: fieldlessRows(), tasks: { running: 1, completed: 1, capped: 0, failed: 0, interrupted: 0 } });
    // Fixture readiness (s194-10-fixture-race): the server's shared
    // ops-status reader may still answer the previous scenario's snapshot, whose
    // task counts differ; a count change while Tasks is mounted re-reads by design.
    // Let the status snapshot reflect this seed before opening Tasks.
    await openDock(page);
    await waitFor(async () => (await topTab(page, RE_WORK).count()) > 0, "a 工单 (Work) top-level tab exists", 20_000);
    await topTab(page, RE_WORK).click();
    await afterStatusPolls(page, 2, "before opening Tasks");
    await waitFor(async () => (await subTab(page, RE_TASKS).count()) > 0, "the 任务 sub-tab control exists", 15_000);
    await subTab(page, RE_TASKS).click();
    const done = taskBlock(page, ids.D2); await done.waitFor({ timeout: 30_000 });
    const text = await dock(page).innerText();
    assert.doesNotMatch(text, /需要升级|upgrade/i, "a field-less list at the floor raises no upgrade notice");
    assert.ok(await leafByText(done, "暂无报告"), "a field-less finished block shows 暂无报告");
    const running = taskBlock(page, ids.D1); await running.waitFor({ timeout: 20_000 });
    assert.ok(await leafByText(running, "生成中…"), "a field-less running block shows 生成中…");
    const select = await reporterSelect(page); assert.ok(select, "the reporter select still renders");
    assert.deepEqual(await optionTexts(select), ["全部发起人"], "a field-less list offers only 全部发起人");
    await afterStatusPolls(page, 2, "after two status polls");
    assert.equal(taskLists().length, 1, "the field-less tab spawns no extra pf task list read");
    const verbs = new Set(calls().map((argv) => argv[0]));
    // (owner-intended, plan step 18): the list read also names
    // the Cell it reads through one local `pf --json profile` (no daemon, no Hub).
    // (intentional): every status read also reads the decision
    // feed (`pf --json watch decisions`), whatever tab is shown.
    for (const verb of verbs) assert.ok(["--version", "ops", "config", "msg", "recall", "memory", "task", "profile", "watch"].includes(verb), `no extra pf verb (${verb})`);
  }));

} catch (error) {
  failures.push("suite");
  console.log(`FAIL suite: ${String(error?.message ?? error).split("\n")[0]}`);
} finally {
  await h.close();
  if (failures.length) console.log(`\nFAILED ${failures.join(", ")} — ${failures.length} scenario(s) red`);
  else console.log("\nPASS all authored report-tail/reporter scenarios");
  console.log(`server log: ${join(work, "server.log")}`);
}
if (failures.length) process.exitCode = 1;
