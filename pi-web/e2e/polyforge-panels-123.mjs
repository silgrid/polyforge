// carries: components/PolyforgeTasks*, components/PolyforgeSearchBar*, lib/polyforge-tasks*, app/api/polyforge/tasks/*, app/api/polyforge/polyforge-tasks-routes*, components/PolyforgeMemory*, lib/polyforge-memory*, app/api/polyforge/memory/*
// — pi-web dock panels: browse-by-default Memory, read-only
// Tasks tab, tab reorder, persisted desktop pin, and the mermaid contract.
//
// Authored pre-code by e2e-author against artifacts/spec.md (the capsule's
// artifacts/e2e-scenarios.md carries the GIVEN/WHEN/THEN blocks and the
// baseline evidence). The suite starts its own `next dev` on a stub `pf`
// first on PATH whose recall/task/memory answers copy the real `pf --json`
// shapes and which records every argv in a ledger.
//
// Baseline posture: browse mode, the Tasks tab, the new tab order and the
// pin control do not exist yet, so every kept scenario is assertion-level
// RED (the missing surface never renders what the scenario waits for).
// The mermaid fixtures were exercised against the already-shipped strict
// pipeline while authoring; those scenarios passed at baseline, were
// deleted, and are recorded as already_satisfied in the scenario document.
//
//   node e2e/polyforge-panels-123.mjs
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { polyforgeDock as dock, polyforgeToggle as pfButton } from "./polyforge-dock.mjs";
import { COLD_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, rawMessage, suiteDist, writeFileAtomic, writeHostSession } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-panels-e2e-"));
const agentDir = join(work, "agent");
const ws1 = join(work, "ws1");
mkdirSync(ws1);
const ts = "2026-10-27T00:00:00.000Z";
const SESSION = "e2e-pf-panels-session";
writeHostSession(agentDir, { id: SESSION, cwd: ws1, text: "E2E pf panels host session", timestamp: ts });

// ---- fixtures ----
const PROJECT_ID = "prj_e2e_panels_123";
const USER_SENTINEL = "u_e2e_sentinel_owner_123";
const bytes = (s) => Buffer.byteLength(s, "utf8");
const pad = (n, w) => String(n).padStart(w, "0");
const MEM = (id, title, updated, content = `${title} body`) => ({ id, project_id: PROJECT_ID, owner_user_id: USER_SENTINEL,
  work_item_id: null, type: "doc.note", title, visibility: "project", tags: [], related: [], status: "active",
  created_at: "2026-10-20T00:00:00Z", updated_at: updated, content_len: bytes(content), content });
const EPOCH = Date.parse("2026-10-27T00:00:00Z");
const iso = (deltaMinutes) => new Date(EPOCH + deltaMinutes * 60_000).toISOString();

// Four mermaid bodies (M0–M3) are the newest entries; the 25 BRW rows follow
// so browse page 1 is 4 mermaid + BRW-001..016 and page 2 is BRW-017..025.
const MERMAID = {
  "mem_e2eMrma00001": MEM("mem_e2eMrma00001", "MERMAID-DIAGRAM", iso(40), "```mermaid\ngraph TD\n  A[Start] --> B[End]\n```\n\nMERMAID-DIAGRAM-END"),
  "mem_e2eMrma00002": MEM("mem_e2eMrma00002", "MERMAID-LOOSE", iso(39),
    "```mermaid\n%%{init: {\"securityLevel\": \"loose\"}}%%\ngraph TD\n  A[Loose] --> B[Still]\n```"),
  "mem_e2eMrma00003": MEM("mem_e2eMrma00003", "MERMAID-CLICK", iso(38),
    "```mermaid\ngraph TD\n  A[Click] --> B[Done]\n  click A call window.__pwned=3()\n  click B \"javascript:window.__pwned=4()\"\n```"),
  "mem_e2eMrma00004": MEM("mem_e2eMrma00004", "MERMAID-HTML", iso(37),
    "```mermaid\ngraph TD\n  A[\"<img src=x onerror=window.__pwned=5>\"] --> B[\"<script>window.__pwned=6</script>\"]\n```"),
};
const BRW = Array.from({ length: 25 }, (_, i) => MEM(`mem_e2eBrw${pad(i + 1, 5)}`, `BRW-ROW-${pad(i + 1, 3)}`, iso(-(i + 1))));
const MEMORIES = Object.fromEntries([...Object.values(MERMAID), ...BRW].map((m) => [m.id, m]));
const BROWSE = [...Object.keys(MERMAID), ...BRW.map((m) => m.id)];
const hit = (m) => { const { content, ...rest } = m; return { ...rest, content_excerpt: content.slice(0, 80) }; };
// Relevance order C, A, B; updated order is C (10-03), B (10-02), A (10-01).
const KW = [MEM("mem_e2eKw00000003", "KW-HIT-C", "2026-10-03T00:00:00Z"), MEM("mem_e2eKw00000001", "KW-HIT-A", "2026-10-01T00:00:00Z"),
  MEM("mem_e2eKw00000002", "KW-HIT-B", "2026-10-02T00:00:00Z")];
const RECALL = { "keyword probe": KW, mermaid: Object.values(MERMAID) };

const T1 = "task_1111111111111111", T2 = "task_2222222222222222", T3 = "task_3333333333333333";
// pf's real shape: the spawn facts sit under `spec` (internal/polyforge/task/task.go).
const TASK = (id, project, state, summary, extra = {}, spec = {}) => ({ id, spec: { project, max_duration_ns: 1_200_000_000_000, max_tokens: 300000, ...spec },
  state, prompt_summary: summary, candidate: "pi:e2e/task-model",
  created_at: "2026-10-27T01:00:00Z", started_at: "2026-10-27T01:00:01Z", ended_at: "2026-10-27T01:10:00Z",
  wall_consumed_ns: 42_000_000_000, tokens_consumed: 12345, ...extra });
const TASKS = [TASK(T1, "workbench", "running", "TASK-ONE-ALPHA run the long job", { ended_at: undefined }),
  TASK(T2, "polyforge", "completed", "TASK-TWO-BRAVO finished report"),
  TASK(T3, "workbench", "failed", "TASK-THREE-CHARLIE broke")];
const REPORT = "**REPORT-BOLD**\n\nTASK-REPORT-MARK\n\n" + "r".repeat(270000) + "\nTASK-REPORT-LAST-LINE";
const TASK_DETAIL = { [T2]: TASK(T2, "polyforge", "completed", "TASK-TWO-BRAVO finished report", { report: REPORT },
  { prompt: "TASK-PROMPT-MARK the full prompt body", candidates: ["pi:e2e/task-model"] }) };

// ---- stub pf ----
const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const argvLog = join(stubDir, "argv.log");
const seed = (patch = {}) => {
  writeFileAtomic(stateFile, JSON.stringify({ version: "v2.20.12", mode: "ok", ws1,
    memories: MEMORIES, browse: BROWSE, recall: Object.fromEntries(Object.entries(RECALL).map(([k, v]) => [k, v.map(hit)])),
    tasks: TASKS, taskDetails: TASK_DETAIL, ...patch }));
  writeFileSync(argvLog, "");
};
seed();
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename));
const args = process.argv.slice(2);
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify(args) + "\\n");
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
// A synchronous full write: process.exit right after process.stdout.write cuts a
// pipe at 64 KiB, and the T2 report is larger than the 262144-byte cap.
const out = (v) => { const b = Buffer.from(JSON.stringify(v) + "\\n"); let o = 0;
  while (o < b.length) { try { o += fs.writeSync(1, b, o); } catch (e) { if (e.code !== "EAGAIN") throw e; } } process.exit(0); };
if (args.includes("--version")) { process.stdout.write(s.version + "\\n"); process.exit(0); }
const rest = args.filter((a) => a !== "--json");
const flag = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] : undefined; };
if (rest[0] === "ops" && rest[1] === "status") out({ machine_id: "m-e2e", workspaces: [s.ws1], env_names: ["E2E_ENV"],
  jobs: [{ work_item_id: "wi_e2eaaaa0001", slug: "task-123", status: "running", spawn_n: 1, step_age_sec: 30, last_work_ago_sec: 3, candidate: "pi:e2e" }],
  queue: { pending: 0, running: 1, max_concurrent: 4 } });
if (rest[0] === "config" && rest[1] === "workspace") out(flag("--cwd") === s.ws1 ? { mapped: true, workspace: s.ws1 } : { mapped: false, workspace: null });
if (rest[0] === "config" && rest[1] === "list") out({ items: [
  { key: "server", value: "http://127.0.0.1:8420", source: "file" },
  { key: "api_key", set: true, source: "file" },
  { key: "user_id", value: "${USER_SENTINEL}", source: "file" },
  { key: '[workspace."' + s.ws1 + '"]', section: true, value: "workbench, polyforge" }] });
if (rest[0] === "watch") out({ work_item: rest[1], slug: "task-123", known: true, job: { status: "running" },
  execution: { workflow_status: "running", current_step: "WATCH-123", steps: {} }, events: [], last_seq: 0 });
if (rest[0] === "msg") out({ conversations: [] });
if (rest[0] === "recall") {
  if (s.mode === "down") { process.stderr.write("pf: daemon not reachable (connection refused)\\n"); process.exit(1); }
  const words = [];
  for (let i = 1; i < rest.length; i++) {
    if (["--project", "--limit", "--tags", "--type", "--sort", "--after", "--q"].includes(rest[i])) i++;
    else if (!rest[i].startsWith("--")) words.push(rest[i]);
  }
  const q = flag("--q") || words.join(" ");
  if (!q) {
    const lim = Number(flag("--limit") || 20);
    const after = flag("--after") || "";
    const start = after ? Number(String(after).replace(/\\D/g, "")) : 0;
    const ids = s.browse.slice(start, start + lim);
    const next = start + ids.length < s.browse.length ? "cur" + (start + ids.length) : "";
    const page = { items: ids.map((id) => { const { content, ...r } = s.memories[id]; return { ...r, content_excerpt: content.slice(0, 80) }; }), total: s.browse.length };
    if (next) page.next_cursor = next;
    out(page);
  }
  const hits = s.recall[q] || [];
  out({ items: hits, total: hits.length });
}
if (rest[0] === "memory" && rest[1] === "show") {
  const m = s.memories[rest[2]];
  if (!m) { process.stderr.write(JSON.stringify({ code: "MEM-404", detail: "memory not found" }) + "\\n"); process.exit(4); }
  out(m);
}
if (rest[0] === "task" && rest[1] === "list") {
  if (s.mode === "down") { process.stderr.write("pf: daemon not reachable (connection refused)\\n"); process.exit(1); }
  out({ tasks: s.tasks, now: new Date().toISOString() });
}
if (rest[0] === "task" && rest[1] === "show") {
  const t = s.taskDetails[rest[2]];
  if (!t) { process.stderr.write(JSON.stringify({ code: "DAEMON-404", detail: "no such task" }) + "\\n"); process.exit(4); }
  out(t);
}
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
const calls = () => readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line).filter((a) => a !== "--json"));
const recalls = () => calls().filter((a) => a[0] === "recall");
const shows = () => calls().filter((a) => a[0] === "task" && a[1] === "show");
const taskLists = () => calls().filter((a) => a[0] === "task" && a[1] === "list");
const argAfter = (argv, name) => { const i = argv.indexOf(name); return i >= 0 ? argv[i + 1] : undefined; };
const recallQuery = (argv) => argv.slice(1).filter((a, i, all) => !a.startsWith("--") && !["--project", "--limit", "--tags", "--type", "--sort", "--after", "--q"].includes(all[i - 1])).join(" ");

// ---- scenario bookkeeping ----
const T = {
  W1: "W1: opening the Memory tab with no keyword browses the selected project's first updated-desc page and shows the total",
  W2: "W2: load more appends the next keyset page with no duplicate id and no scroll jump to the top",
  W3: "W3: a non-empty keyword switches the Memory tab to one 200-row relevance read rendered newest-first",
  W4: "W4: type and tag filters narrow browse and keyword modes and clearing the keyword returns to browse",
  W5: "W5: a pf below 2.20.12 shows the upgrade state on Memory and Tasks and spawns no memory or task verb",
  W6: "W6: a pf at 2.20.12 enables browse on Memory and the task list on Tasks with no warning",
  W7: "W7: the Tasks tab splits running from all tasks, shows project and summary, and agrees with pf task list",
  W8: "W8: Tasks search matches id, project, state and prompt summary on the client and clearing it needs no new read",
  W9: "W9: a task detail shows status, candidates, budget, times, prompt and the markdown report with the collapse rule and cap",
  W10: "W10: the Tasks read surface has no spawn or cancel control and every pi-web task route is GET-only",
  W11: "W11: the dock's top tab strip orders Work, Memory, Messages with Jobs/Tasks as Work's sub-tabs, and each tab shows its own surface",
  W12: "W12: a pinned desktop dock survives an outside press, unpin restores dismissal, and the mobile sheet has no pin",
  W13: "W13: closing a pinned dock keeps the pin, and reloading while pinned reopens the dock as an overlay",
};
const failures = new Map(Object.values(T).map((title) => [title, []]));
const check = createCheck({ failures: ([id], text) => failures.get(T[id]).push(text), log: rawMessage });

let base;
const h = createHarness({
  // The fixture session is pinned to 2026-10-27, so the sidebar's default
  // 15-day age filter (7 earlier) would hide its row once the wall clock passes it:
  // W12's outside press would then land on the right-aligned
  // chat message, which the dock overlay covers. Disable the filter for
  // determinism, the seed e2e/run.mjs and e2e/claude-session-219.mjs already use.
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "en", "pi-web:session-age-filter-days": "0" },
  session: SESSION,
  ready: "E2E pf panels host session",
  // Each server gets its own dist dir, nested so e2e/all.mjs clears it with the suite dir.
  server: (name) => ({ name, logFile: join(work, `server-${name}.log`), distDir: join(suiteDist("polyforge-panels-123"), name), env: {
    PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: work, PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: "" } }),
  async started(started) {
    base = started.base;
    if (h.browser) await h.withPage({ gotoTimeout: COLD_MS, readyTimeout: COLD_MS }, async () => {});
  },
});
const { startServer, withPage } = h;
// the top strip is Work | Memory | Messages; Jobs and Tasks
// are Work's sub-tabs in their own named tablist.
const topTabs = (page) => dock(page).locator('[data-pf-tabstrip="top"] [role="tab"]');
const workTabs = (page) => dock(page).locator('[data-pf-tabstrip="work"] [role="tab"]');
async function openWorkTasks(page) {
  const work = topTabs(page).filter({ hasText: /work|工单/i }).first();
  await work.waitFor({ timeout: 15_000 }); await work.click();
  const tasks = workTabs(page).filter({ hasText: /tasks|任务/i }).first();
  await tasks.waitFor({ timeout: 15_000 }); await tasks.click();
}
const keyword = (page) => dock(page).getByRole("searchbox").or(dock(page).getByPlaceholder(/keyword|search/i)).first();
async function openDock(page) { await pfButton(page).click(); await dock(page).waitFor({ timeout: 20_000 }); return dock(page); }
async function openTab(page, re) {
  const panel = await openDock(page);
  const tab = topTabs(page).filter({ hasText: re }).first();
  await tab.waitFor({ timeout: 15_000 }); await tab.click(); return panel;
}
const memoryTab = (page) => openTab(page, /memory|记忆/i);
const tasksTab = async (page) => { const panel = await openDock(page); await openWorkTasks(page); return panel; };
const topOf = (panel, needle) => panel.getByText(needle, { exact: false }).first().boundingBox().then((b) => b?.y ?? -1);
const waitFor = createWaitFor({ every: 250 });
const scrollInfo = (panel) => panel.evaluate((root) => {
  for (const el of [root, ...root.querySelectorAll("*")]) {
    const style = getComputedStyle(el);
    if (el.scrollHeight > el.clientHeight + 40 && /(auto|scroll)/.test(style.overflowY)) return { top: el.scrollTop, height: el.scrollHeight };
  }
  return null;
});
const scrollToBottom = (panel) => panel.evaluate((root) => {
  for (const el of [root, ...root.querySelectorAll("*")]) {
    const style = getComputedStyle(el);
    if (el.scrollHeight > el.clientHeight + 40 && /(auto|scroll)/.test(style.overflowY)) { el.scrollTop = el.scrollHeight; return el.scrollTop; }
  }
  return 0;
});

try {
  await h.launchBrowser(() => chromium.launch());
  await startServer("main");

  await check("W1", () => withPage({}, async (page) => {
    seed(); const panel = await memoryTab(page);
    await panel.getByText("BRW-ROW-001").waitFor({ timeout: 30_000 });
    const first = recalls();
    assert.ok(first.length >= 1, "opening the tab must request a browse page");
    const argv = first[0];
    assert.equal(argAfter(argv, "--project"), "workbench", `browse argv ${JSON.stringify(argv)}`);
    assert.equal(argAfter(argv, "--limit"), "20", `browse argv ${JSON.stringify(argv)}`);
    assert.equal(argAfter(argv, "--sort"), "updated", `browse argv ${JSON.stringify(argv)}`);
    assert.equal(recallQuery(argv), "", `the browse request carries no keyword: ${JSON.stringify(argv)}`);
    const text = await panel.innerText();
    assert.match(text, /\b29\b/, "the project total (29) must be visible");
    assert.ok(await panel.getByText(/load more|加载更多|更多/i).count() > 0, "a next cursor must offer load more");
    const tops = {};
    for (const id of ["BRW-ROW-001", "BRW-ROW-008", "BRW-ROW-016"]) tops[id] = await topOf(panel, id);
    assert.ok(tops["BRW-ROW-001"] < tops["BRW-ROW-008"] && tops["BRW-ROW-008"] < tops["BRW-ROW-016"], `the first page is updated-descending: ${JSON.stringify(tops)}`);
    assert.equal(await panel.getByText(/BRW-ROW-017/).count(), 0, "page 2 rows are not loaded yet");
  }));

  await check("W2", () => withPage({}, async (page) => {
    seed(); const panel = await memoryTab(page);
    await panel.getByText("BRW-ROW-016").waitFor({ timeout: 30_000 });
    await scrollToBottom(panel);
    const before = await scrollInfo(panel);
    const spawned = recalls().length;
    const loadMore = panel.getByText(/load more|加载更多|更多/i).first();
    await loadMore.click();
    await panel.getByText("BRW-ROW-025").waitFor({ timeout: 20_000 });
    assert.equal(recalls().length, spawned + 1, "load more must run exactly one next page read");
    const second = recalls().at(-1);
    assert.equal(argAfter(second, "--after"), "cur20", `the second page carries the cursor: ${JSON.stringify(second)}`);
    assert.equal(argAfter(second, "--sort"), "updated", "load more keeps the browse order");
    assert.equal(recallQuery(second), "", "load more carries no keyword");
    assert.equal(await panel.getByText(/BRW-ROW-\d{3}/).count(), 25, "all 25 BRW rows appear exactly once");
    assert.ok(await topOf(panel, "BRW-ROW-016") < await topOf(panel, "BRW-ROW-017"), "the next page appends after the current rows");
    const after = await scrollInfo(panel);
    assert.ok(!before || !after || after.top > 0, `the list must not jump back to the top (before ${JSON.stringify(before)}, after ${JSON.stringify(after)})`);
  }));

  await check("W3", () => withPage({}, async (page) => {
    seed(); const panel = await memoryTab(page);
    await panel.getByText("BRW-ROW-001").waitFor({ timeout: 30_000 });
    const spawned = recalls().length;
    await keyword(page).fill("keyword probe"); await keyword(page).press("Enter");
    await panel.getByText("KW-HIT-C").waitFor({ timeout: 20_000 });
    await waitFor(() => recalls().length === spawned + 1, "a keyword switches to exactly one relevance read");
    const argv = recalls().at(-1);
    assert.equal(recallQuery(argv), "keyword probe", `keyword argv ${JSON.stringify(argv)}`);
    assert.equal(argAfter(argv, "--limit"), "200", "the keyword read asks for the one 200-row page");
    assert.ok(!argv.includes("--after") && !argv.includes("--sort"), `the keyword read carries no browse paging: ${JSON.stringify(argv)}`);
    const c = await topOf(panel, "KW-HIT-C"), b = await topOf(panel, "KW-HIT-B"), a = await topOf(panel, "KW-HIT-A");
    assert.ok(c < b && b < a, `the relevance answer renders newest-first C, B, A (tops ${c}, ${b}, ${a})`);
  }));

  await check("W4", () => withPage({}, async (page) => {
    seed(); const panel = await memoryTab(page);
    await panel.getByText("BRW-ROW-001").waitFor({ timeout: 30_000 });
    await panel.getByRole("combobox", { name: /type/i }).selectOption("doc.*");
    await waitFor(() => recalls().some((a) => recallQuery(a) === "" && argAfter(a, "--type") === "doc.*"), "a type change re-issues the browse read");
    await panel.getByLabel(/tags/i).or(panel.getByPlaceholder(/tag/i)).first().fill("audit");
    const filtered = await waitFor(() => recalls().find((a) => recallQuery(a) === "" && argAfter(a, "--tags") === "audit"), "a tag change re-issues the browse read");
    assert.equal(argAfter(filtered, "--type"), "doc.*", `the browse read keeps both filters: ${JSON.stringify(filtered)}`);
    await keyword(page).fill("keyword probe"); await keyword(page).press("Enter");
    await panel.getByText("KW-HIT-C").waitFor({ timeout: 20_000 });
    const kw = recalls().at(-1);
    assert.equal(argAfter(kw, "--limit"), "200", "the keyword mode keeps its 200-row read");
    assert.equal(argAfter(kw, "--type"), "doc.*", "the type filter applies in keyword mode");
    assert.equal(argAfter(kw, "--tags"), "audit", "the tag filter applies in keyword mode");
    assert.equal(recallQuery(kw), "keyword probe");
    await keyword(page).fill(""); await keyword(page).press("Enter");
    await waitFor(() => recalls().at(-1) !== kw && recallQuery(recalls().at(-1)) === "" && argAfter(recalls().at(-1), "--tags") === "audit",
      "clearing the keyword returns to the browse read with the same filters");
    assert.equal(argAfter(recalls().at(-1), "--type"), "doc.*", "clearing keeps the type filter");
  }));

  await check("W6", () => withPage({}, async (page) => {
    seed(); const panel = await memoryTab(page);
    await panel.getByText("BRW-ROW-001").waitFor({ timeout: 30_000 });
    assert.equal(await panel.getByText(/upgrade required|upgrade|需升级/i).count(), 0, "at the floor the Memory tab shows no upgrade warning");
    await openWorkTasks(page);
    await dock(page).getByText(T1).waitFor({ timeout: 20_000 });
    assert.equal(/upgrade required|需升级/i.test(await dock(page).innerText()), false, "at the floor the Tasks tab shows no upgrade warning");
  }));

  await check("W7", () => withPage({ storage: { "pi-locale": "zh-CN" } }, async (page) => {
    seed(); const panel = await tasksTab(page);
    await panel.getByText(T1).waitFor({ timeout: 30_000 });
    // the state filter offers a 全部 option and the project
    // filter 全部项目, both above the sections; the 全部 heading is the last
    // exact 全部 on the surface.
    const running = await topOf(panel, "正在运行");
    const all = await panel.getByText("全部", { exact: true }).last().boundingBox().then((b) => b?.y ?? -1);
    const t1 = await topOf(panel, T1), t2 = await topOf(panel, T2), t3 = await topOf(panel, T3);
    assert.ok(running >= 0 && all > running, `the running and all sections render in order (running ${running}, all ${all})`);
    assert.ok(t1 > running && t1 < all, `正在运行 holds the running task ${T1} (t1 ${t1})`);
    assert.ok(t2 > all && t3 > all, `全部 holds the terminal tasks (t2 ${t2}, t3 ${t3}, all ${all})`);
    const text = await panel.innerText();
    for (const want of ["workbench", "polyforge", "TASK-ONE-ALPHA", "TASK-TWO-BRAVO", "TASK-THREE-CHARLIE"]) {
      assert.ok(text.includes(want), `a task row must show ${want}`);
    }
    assert.equal(taskLists().length, 1, `the tab reads pf task list exactly once (ledger ${JSON.stringify(calls())})`);
  }));

  await check("W8", () => withPage({ storage: { "pi-locale": "zh-CN" } }, async (page) => {
    seed(); const panel = await tasksTab(page);
    await panel.getByText(T1).waitFor({ timeout: 30_000 });
    const reads = taskLists().length;
    const search = panel.getByRole("searchbox").or(panel.getByPlaceholder(/search|搜索/i)).first();
    for (const [needle, keep, drop] of [["BRAVO", T2, T1], ["polyforge", T2, T3], ["task_3333", T3, T1], ["failed", T3, T1]]) {
      await search.fill(needle);
      await panel.getByText(keep).waitFor({ timeout: 10_000 });
      assert.equal(await panel.getByText(drop).count(), 0, `searching ${needle} hides ${drop}`);
    }
    assert.equal(taskLists().length, reads, "client-side search must not read pf again");
    await search.fill("");
    await panel.getByText(T1).waitFor({ timeout: 10_000 });
    assert.ok(await panel.getByText(T2).count() > 0 && await panel.getByText(T3).count() > 0, "clearing the search restores every row");
    assert.equal(taskLists().length, reads, "clearing the search must not read pf again");
  }));

  await check("W9", () => withPage({ storage: { "pi-locale": "zh-CN" } }, async (page) => {
    seed(); const panel = await tasksTab(page);
    await panel.getByText(T2).waitFor({ timeout: 30_000 });
    await panel.getByText(T2).first().click();
    await panel.getByText("TASK-PROMPT-MARK").waitFor({ timeout: 20_000 });
    assert.deepEqual(shows().map((a) => a[2]), [T2], `selecting a row must read exactly that task via pf task show (ledger ${JSON.stringify(shows())})`);
    const text = await panel.innerText();
    assert.ok(text.includes("pi:e2e/task-model"), "the detail shows the candidate models");
    assert.match(text, /\b300[.,]?000\b/, "the detail shows the token budget limit");
    assert.match(text, /\b12[.,]?345\b/, "the detail shows the tokens consumed");
    assert.match(text, /2026/, "the detail shows its times");
    assert.ok(await panel.locator("strong", { hasText: "REPORT-BOLD" }).count() > 0, "the report renders as markdown");
    assert.ok(await panel.getByRole("button", { name: /展开|expand|show more/i }).count() > 0, "a >2000-byte report starts collapsed behind an expand control");
    assert.match(text, /截断|truncat/i, "a capped report carries the truncation notice");
  }));

  await check("W10", () => withPage({ storage: { "pi-locale": "zh-CN" } }, async (page) => {
    seed(); const panel = await tasksTab(page);
    await panel.getByText(T1).waitFor({ timeout: 30_000 });
    const buttons = (await panel.getByRole("button").allInnerTexts()).map((t) => t.trim()).filter(Boolean);
    assert.equal(buttons.filter((t) => /^(spawn|派生|cancel task|取消任务)/i.test(t)).length, 0, `no spawn/cancel control may exist: ${JSON.stringify(buttons)}`);
    for (const path of ["/api/polyforge/tasks", `/api/polyforge/tasks/${T1}`]) {
      const r = await fetch(`${base}${path}`, { method: "POST", headers: { "content-type": "application/json" }, body: "{}" });
      assert.equal(r.status, 405, `POST ${path} must be refused as method-not-allowed, got ${r.status}`);
    }
  }));

  await check("W11", () => withPage({}, async (page) => {
    seed(); await openDock(page);
    // (owner-approved intentional change ①): three top-level
    // tabs; Jobs and Tasks are no longer top-level. (owner
    // 2026-10-30, intentional change): Work | Memory | Messages.
    await topTabs(page).first().waitFor({ timeout: 15_000 });
    const labels = (await topTabs(page).allInnerTexts()).map((t) => t.trim().split(/\s/)[0].toLowerCase());
    assert.deepEqual(labels, ["work", "memory", "messages"], `the top tab strip DOM order is Work, Memory, Messages: ${JSON.stringify(labels)}`);
    assert.equal(await topTabs(page).filter({ hasText: /jobs|tasks/i }).count(), 0, "no Jobs/Tasks top-level tab remains");
    await openWorkTasks(page);
    await dock(page).getByText(T1).waitFor({ timeout: 20_000 });
    await topTabs(page).filter({ hasText: /memory/i }).click();
    await dock(page).getByText("BRW-ROW-001").waitFor({ timeout: 20_000 });
    await topTabs(page).filter({ hasText: /messages/i }).click();
    await dock(page).getByText(/select a conversation|选择一个会话|新消息|new message/i).first().waitFor({ timeout: 20_000 });
  }));

  await check("W12", () => withPage({}, async (page) => {
    seed(); const panel = await openDock(page);
    const pin = panel.locator("button[aria-pressed]").first();
    await pin.waitFor({ timeout: 15_000 });
    await pin.click();
    assert.equal(await pin.getAttribute("aria-pressed"), "true", "the pin control must report the pinned state");
    assert.equal(await page.evaluate(() => localStorage.getItem("pi-web:polyforge-dock-pinned")), "1", "the pin persists in global localStorage");
    await page.evaluate(() => { window.__e2ePress = []; document.addEventListener("pointerdown", (e) => window.__e2ePress.push(e.target?.id || e.target?.tagName || ""), true); });
    await page.getByText("E2E pf panels host session").first().click();
    await delay(300);
    assert.ok(await panel.isVisible(), "a pinned dock survives an outside press");
    assert.ok((await page.evaluate(() => window.__e2ePress.length)) > 0, "the outside press still reaches its target");
    await pin.click();
    assert.equal(await pin.getAttribute("aria-pressed"), "false", "a second press unpins");
    await page.getByText("E2E pf panels host session").first().click();
    await waitFor(() => panel.isVisible().then((v) => !v), "an outside press closes the unpinned dock");
    await withPage({ viewport: { width: 600, height: 900 } }, async (sheetPage) => {
      seed(); const sheet = await openDock(sheetPage);
      assert.equal(await sheet.locator("button[aria-pressed]").count(), 0, "the mobile sheet must show no pin control");
      await sheetPage.mouse.click(10, 200);
      await waitFor(() => sheet.isVisible().then((v) => !v), "the backdrop tap still closes the mobile sheet");
    });
  }));

  await check("W13", () => withPage({}, async (page) => {
    seed(); const panel = await openDock(page);
    const pin = panel.locator("button[aria-pressed]").first();
    await pin.waitFor({ timeout: 15_000 }); await pin.click();
    assert.equal(await page.evaluate(() => localStorage.getItem("pi-web:polyforge-dock-pinned")), "1");
    await panel.getByRole("button", { name: /close|关闭/i }).first().click();
    await waitFor(() => panel.isVisible().then((v) => !v), "the close control closes a pinned dock");
    assert.equal(await page.evaluate(() => localStorage.getItem("pi-web:polyforge-dock-pinned")), "1", "closing must not clear the pin");
    await page.reload({ waitUntil: "domcontentloaded" });
    await dock(page).waitFor({ timeout: 30_000 });
    assert.ok(await dock(page).isVisible(), "a reload while pinned reopens the dock");
    const position = await dock(page).evaluate((el) => getComputedStyle(el).position);
    assert.equal(position, "absolute", `the dock stays an overlay (position ${position})`);
  }));

  // W5 runs last: it needs a fresh server whose pf version probe answers
  // 2.20.10 (below the 2.20.12 floor; the probe is cached per process).
  await check("W5", async () => {
    await h.stop(); seed({ version: "v2.20.10" }); await startServer("floor");
    await withPage({}, async (page) => {
      const panel = await memoryTab(page);
      await panel.getByText(/upgrade required|upgrade|需升级/i).first().waitFor({ timeout: 20_000 });
      await openWorkTasks(page);
      await dock(page).getByText(/upgrade required|upgrade|需升级/i).first().waitFor({ timeout: 20_000 });
      await delay(1500);
      assert.equal(recalls().length + taskLists().length + shows().length, 0,
        `below the floor no memory or task verb may be spawned (ledger ${JSON.stringify(calls())})`);
    });
  });
} catch (error) {
  console.error(error);
  for (const [title, list] of failures) if (!list.length) failures.set(title, [`suite: ${error?.message ?? error}`]);
} finally {
  await h.close().catch(() => {});
}

let failed = 0;
console.log("\n=== scenarios ===");
for (const [title, list] of failures) {
  if (list.length) { failed++; console.log(`FAIL ${title}`); for (const line of list) console.log(`     ${line}`); }
  else console.log(`PASS ${title}`);
}
rmSync(work, { force: true, recursive: true });
process.exit(failed ? 1 : 0);
