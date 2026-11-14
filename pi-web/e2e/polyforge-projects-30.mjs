// carries: components/PolyforgeProjects*, lib/polyforge-projects*, app/api/polyforge/projects/*
// — the Work panel's fifth sub-tab 项目 (polyforge#30): the mapped projects
// with their global states. (The panel rail, the hook, the doc and the
// locale tables stay with the suites and tests that already declare them
// — polyforge.mjs and polyforge-cards.mjs for the panel and its doc, the
// component test for the locale keys — so the light tier's selection
// keeps its declaration caps.)
//
// Authored pre-code against polyforge#30's acceptance (AC7): the project
// list hides the deactivated projects by default, a「显示已停用」entry
// reveals them with their marker and an in-place restore in the same row,
// and restoring moves the project back into the default view. The suite
// starts its own dev server on a stub `pf` whose `config list`,
// `projects get` and `projects restore` answers copy the real field
// shapes; every pf argv is appended to a ledger, so the one-read and the
// restore-write disciplines are asserted, not assumed.
//
// Baseline posture: the pre-change tree has no 项目 sub-tab and no projects
// route, so every scenario is assertion-level RED. A passing scenario prints
// its binding marker `PASS S30-<n>`.
//
//   node e2e/polyforge-projects-30.mjs
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { chromium } from "playwright";
import { openPolyforgeDock, polyforgeDock as dock, waitForPolyforgeToggle } from "./polyforge-dock.mjs";
import { NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, root, suiteDist, writeFileAtomic, writeHostSession } from "./harness.mjs";

const distDir = suiteDist("polyforge-projects-30");
assertNoDevServer(distDir);
rmSync(join(root, distDir), { force: true, recursive: true });

const work = mkdtempSync(join(tmpdir(), "pi-web-pf-projects-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-projects-session";
mkdirSync(project);
writeHostSession(agentDir, { id: SESSION, cwd: project, text: "E2E pf projects host session", timestamp: "2026-11-14T00:00:00.000Z" });

// ---- stub pf ----
const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const argvLog = join(stubDir, "argv.log");
const seed = () => {
  writeFileAtomic(stateFile, JSON.stringify({
    version: "v2.20.13", ws: project,
    projects: { workbench: "active", dormant: "deactivated" },
  }));
  writeFileSync(argvLog, "");
};
seed();
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
if (rest[0] === "ops" && rest[1] === "status") out({ machine_id: "m-e2e", workspaces: [s.ws], jobs: [], queue: { running: 0, max_concurrent: 6 }, tasks: { running: 0, completed: 0 } });
if (rest[0] === "config" && rest[1] === "workspace") out(flag("--cwd") === s.ws ? { mapped: true, workspace: s.ws } : { mapped: false, workspace: null });
if (rest[0] === "config" && rest[1] === "list") out({ items: [{ key: "user_id", value: "u_e2e", source: "file" },
  { key: '[workspace."' + s.ws + '"]', section: true, value: "workbench, dormant" }] });
if (rest[0] === "msg" && rest[1] === "list") out({ conversations: [] });
if (rest[0] === "msg" && rest[1] === "contacts") out({ contacts: [] });
if (rest[0] === "recall") out({ items: [], total: 0 });
if (rest[0] === "task" && rest[1] === "list") out({ tasks: [], now: new Date().toISOString() });
if (rest[0] === "queue") out({ items: [], total: 0 });
if (rest[0] === "projects" && rest[1] === "get") {
  const name = rest[2];
  out({ id: "prj_" + name, name, status: s.projects[name] ?? "active" });
}
if (rest[0] === "projects" && rest[1] === "restore") {
  const name = rest[2];
  s.projects[name] = "active";
  fs.writeFileSync(path.join(dir, "state.json"), JSON.stringify(s));
  out({ id: "prj_" + name, name, status: "active" });
}
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
const calls = () => readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line).filter((a) => a !== "--json"));
const projectGets = () => calls().filter((a) => a[0] === "projects" && a[1] === "get");
const projectRestores = () => calls().filter((a) => a[0] === "projects" && a[1] === "restore");

// ---- scenario bookkeeping and page helpers ----
const failures = [];
const check = createCheck({ failures });
const RE_WORK = /工单|工單|work/i, RE_PROJECTS = /项目|專案|projects/i;
const BARRIER = "/__e2e_request_barrier";
const topTab = (page, re) => dock(page).locator('[data-pf-tabstrip="top"] [role="tab"]').filter({ hasText: re }).first();
const segments = (page) => dock(page).locator('[data-pf-tabstrip="work"] [role="tab"]');
const segment = (page, re) => segments(page).filter({ hasText: re }).first();
const h = createHarness({
  locale: "zh-CN",
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN" },
  errors: ["pageerror", "console", "5xx"],
  session: SESSION,
  ready: "E2E pf projects host session",
  async setup(page) {
    page.__pf = [];
    await page.route((url) => url.pathname === BARRIER, (route) => route.fulfill({ status: 204 }));
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.pathname.startsWith("/api/polyforge/")) page.__pf.push(url.pathname);
    });
  },
  server: (name) => ({ name, logFile: join(work, `server-${name}.log`), distDir,
    env: { PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: work, PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: "" } }),
});
const { errors, startServer, withPage } = h;
const waitFor = createWaitFor({ ms: 20_000 });
const listRequests = (page) => page.__pf.filter((pathname) => pathname === "/api/polyforge/projects").length;
const requestBarrier = (page) => page.evaluate(async (url) => { await fetch(url, { cache: "no-store" }); }, BARRIER);

async function openProjects(page) {
  if ((await dock(page).count()) === 0 || !(await dock(page).isVisible().catch(() => false))) {
    await openPolyforgeDock(page);
  }
  await waitFor(async () => (await topTab(page, RE_WORK).count()) > 0, "a Work top-level tab exists", NAV_MS);
  await topTab(page, RE_WORK).click();
  const before = listRequests(page);
  const tab = segment(page, RE_PROJECTS)
    .or(dock(page).locator('[data-pf-tabstrip="work"]').getByRole("tab", { name: RE_PROJECTS }))
    .first();
  await tab.waitFor({ timeout: NAV_MS });
  await tab.click();
  await waitFor(() => listRequests(page) > before, "the projects sub-tab issues its list read", NAV_MS);
  await requestBarrier(page);
  return dock(page);
}

try {
  await h.launchBrowser(() => chromium.launch());
  await startServer("main");
  await withPage({ gotoTimeout: 60_000, readyTimeout: 60_000 }, (page) => waitForPolyforgeToggle(page, 60_000));

  await check("S30-1", async () => {
    seed();
    await withPage({}, async (page) => {
      const panel = await openProjects(page);
      // The Work rail carries the fifth sub-tab after 全部.
      assert.deepEqual((await segments(page).allInnerTexts()).map((t) => t.trim().split(/\s/)[0]),
        ["作业", "任务", "全部", "项目", "待集成"], "the Work strip lists 作业, 任务, 全部, 项目, 待集成 in order");
      // The default view lists the active project and hides the deactivated one.
      const view = panel.getByTestId("pf-projects-list");
      await view.waitFor({ timeout: NAV_MS });
      await waitFor(async () => (await view.textContent()).includes("workbench"), "the active project renders", NAV_MS);
      assert(!(await view.textContent()).includes("dormant"), "the default view lists the deactivated project");
      // The「显示已停用」entry exists beside the list.
      assert((await panel.getByTestId("pf-projects-toggle").count()) === 1, "no 显示已停用 entry beside the list");
      // The read discipline: one route read per opening, and behind it the
      // mapping read plus one projects get per mapped project (the panel's
      // other surfaces share the pf, so the mapping read's count is not
      // pinned here — the projects route's own one-read is).
      assert.equal(listRequests(page), 1, "more than one projects read fired");
      assert.ok(calls().filter((a) => a[0] === "config" && a[1] === "list").length >= 1, "the mapping read never fired");
      assert.deepEqual(projectGets().map((a) => a[2]).sort(), ["dormant", "workbench"], "one projects get per mapped project");
      assert.equal(projectRestores().length, 0, "no restore fired on a read");
    });
  });

  await check("S30-2", async () => {
    seed();
    await withPage({}, async (page) => {
      const panel = await openProjects(page);
      const view = panel.getByTestId("pf-projects-list");
      await waitFor(async () => (await view.textContent()).includes("workbench"), "the active project renders", NAV_MS);
      // The toggle reveals the deactivated project, marked, with the
      // restore entry in the same row.
      await panel.getByTestId("pf-projects-toggle").click();
      const row = panel.locator('[data-pf-project-row]').filter({ hasText: "dormant" });
      await row.waitFor({ timeout: NAV_MS });
      assert((await row.locator('[data-pf-project-mark]').count()) === 1, "the revealed row carries no deactivated marker");
      assert((await row.getByTestId("pf-projects-restore").count()) === 1, "the revealed row carries no in-place restore entry");
    });
  });

  await check("S30-3", async () => {
    seed();
    await withPage({}, async (page) => {
      const panel = await openProjects(page);
      const view = panel.getByTestId("pf-projects-list");
      await waitFor(async () => (await view.textContent()).includes("workbench"), "the active project renders", NAV_MS);
      await panel.getByTestId("pf-projects-toggle").click();
      const row = panel.locator('[data-pf-project-row]').filter({ hasText: "dormant" });
      await row.waitFor({ timeout: NAV_MS });
      // Restore: the write goes through the route's action door, the stub
      // flips the project, the one re-read settles the row — unmarked.
      await row.getByTestId("pf-projects-restore").click();
      await row.locator('[data-pf-project-mark]').waitFor({ state: "detached", timeout: NAV_MS });
      assert(!(await row.textContent()).includes("deactivated"), "the restored row still carries the marker");
      assert.equal(projectRestores().length, 1, "the restore did not fire exactly once");
      assert.deepEqual(projectRestores()[0].slice(0, 2), ["projects", "restore"], "the restore argv");
      // The restored project is back in the default view.
      await panel.getByTestId("pf-projects-toggle").click();
      await waitFor(async () => {
        const text = await panel.getByTestId("pf-projects-list").textContent();
        return text.includes("dormant") && !text.includes("deactivated");
      }, "the default view never listed the restored project unmarked", NAV_MS);
    });
  });

  if (errors.length) {
    failures.push("errors");
    console.log(`FAIL errors: ${JSON.stringify(errors.slice(0, 10))}`);
  }
} catch (error) {
  failures.push("suite");
  console.log(`FAIL suite: ${String(error?.message ?? error).split("\n")[0]}`);
} finally {
  await h.close();
  if (failures.length) console.log(`\nFAILED ${failures.join(", ")} — ${failures.length} scenario(s) red`);
  else console.log("\nPASS all authored projects scenarios");
  console.log(`server log: ${join(work, "server-main.log")}`);
}
if (failures.length) process.exitCode = 1;
