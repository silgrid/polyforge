// carries: lib/polyforge-cells-pure.ts, app/settings.css
// — Settings › Polyforge as a master-detail page.
//
// The list holds the Host entry, then every Cell (the default Cell and a
// named `public` Cell) with its state, then Add Cell; the detail shows the
// selected entry only. The Host pane is the one home of the Host-level
// settings, a Cell's pane holds that Cell's own settings, and a narrow
// viewport keeps the list above the detail on one screen. Driven as a
// browser page against the app's real routes with a `pf` stub first on PATH
// answering the real pf wire (the w348 suite's stub: `--json version`
// capabilities, `--json cell list`, the scoped `--json config list`, the
// lifecycle and config writes, `--json ops status`).
//
// restated a named Cell's pane: its keys are its file card (the
// default Cell's same component, inputs labelled by key), its mapping the
// editable 工作区 card; every check below still runs on those surfaces.
//
// Screenshots (desktop and a 420px viewport) are written to
// $PI_WEB_E2E_SCREENS_DIR when set, else to the run's temp dir.
//
//   node e2e/w364-cell-master-detail.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, joinedLines, writeFileAtomic, writeHostSession } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-364-"));
const screens = process.env.PI_WEB_E2E_SCREENS_DIR || join(work, "screens");
mkdirSync(screens, { recursive: true });
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(work, "project");
const publicWs = join(work, "ws-public");
const SESSION = "e2e-pf-364-master-detail";
const SESSION_TEXT = "E2E workbench 364 master-detail host session";
for (const dir of [agentDir, project, publicWs, join(home, ".config", "polyforge")]) mkdirSync(dir, { recursive: true });
// The default Cell's file, as the stub's `main` list reports it (server and a
// set api_key): Pi Web reads this file itself only for install.sh's
// has_config rule, so without it the install card would rightly ask a
// machine whose key the stub says is set for the host and the key.
writeFileSync(join(home, ".config", "polyforge", "cli.toml"), 'server = "https://hub-main.example"\napi_key = "e2e-current-key"\n');
writeHostSession(agentDir, { id: SESSION, cwd: project, text: SESSION_TEXT, timestamp: "2026-11-07T00:00:00.000Z" });

// ---- stub pf: two Cells, the Host and Cell files, lifecycle ----
const stubDir = join(work, "stub");
mkdirSync(stubDir);
const argvLog = join(stubDir, "argv.log");
const EXEC_DISABLED = "no model-credential source declared — set [model] source in this Cell's file and restart the Host";
const PUBLIC_CELL = {
  profile: "public", state: "serving", valid: true, server: "https://hub-public.example", workspaces: [publicWs],
  running: 0, pending: 1, max_concurrent: 6, cell_max_concurrent_jobs: 2, execution_disabled: EXEC_DISABLED,
};
const state = {
  cells: [
    { profile: "default", state: "serving", valid: true, server: "https://hub-main.example", workspaces: [project], running: 1, pending: 0, max_concurrent: 6 },
    PUBLIC_CELL,
  ],
  items: {
    host: [
      { key: "daemon_socket", value: "/run/pf.sock", source: "file", class: "restart-only", schema: { kind: "path" } },
      { key: "max_concurrent_jobs", value: 6, source: "file", class: "live", schema: { kind: "int", min: 1 } },
      { key: "harness_root", value: "/opt/pf", source: "file", class: "restart-only", schema: { kind: "path" } },
      { key: "operator_agent_dir", value: "/home/op/.pi", source: "file", class: "restart-only", schema: { kind: "path" } },
    ],
    public: [
      { key: "server", value: "https://hub-public.example", source: "file", class: "restart-only", schema: { kind: "string" } },
      { key: "api_key", set: true, source: "file", class: "restart-only", schema: { kind: "secret" } },
      { key: "user_id", value: "usr_public", source: "file", class: "cli-only", schema: { kind: "string" } },
      { key: "claude_bin", value: "/usr/local/bin/claude", source: "file", class: "live", schema: { kind: "string" } },
      { key: "step_timeout", value: "45m0s", source: "file", class: "live", schema: { kind: "duration", min_exclusive: 0 } },
      { key: "stall_window", value: "10m0s", source: "file", class: "live", schema: { kind: "duration", nonzero: true } },
      { key: "cell_max_concurrent_jobs", value: 2, source: "file", class: "live", schema: { kind: "int", min: 1 } },
      { key: "model.source", value: null, source: "default", class: "restart-only", schema: { kind: "model-source" } },
      // pf lists a named Cell's own [workspace] mapping in its file's list (its pane's 工作区 card)
      { key: `[workspace."${publicWs}"]`, value: "pub", source: "file", section: true, class: "next-claim" },
    ],
    // the default Cell's unscoped read: pf lists the Host keys there too
    main: [
      { key: "server", value: "https://hub-main.example", source: "file", class: "restart-only", schema: { kind: "string" } },
      { key: "api_key", set: true, source: "file", class: "restart-only", schema: { kind: "secret" } },
      { key: "daemon_socket", value: "/run/pf.sock", source: "file", class: "restart-only", schema: { kind: "path" } },
      { key: "user_id", value: "usr_main", source: "file", class: "cli-only", schema: { kind: "string" } },
      { key: "claude_bin", value: "/usr/local/bin/claude", source: "file", class: "live", schema: { kind: "string" } },
      { key: "step_timeout", value: "30m0s", source: "file", class: "live", schema: { kind: "duration", min_exclusive: 0 } },
      { key: "stall_window", value: "10m0s", source: "file", class: "live", schema: { kind: "duration", nonzero: true } },
      { key: "harness_root", value: "/opt/pf", source: "file", class: "restart-only", schema: { kind: "path" } },
      { key: "operator_agent_dir", value: "/home/op/.pi", source: "file", class: "restart-only", schema: { kind: "path" } },
      { key: "max_concurrent_jobs", value: 6, source: "file", class: "live", schema: { kind: "int", min: 1 } },
      { key: "cell_max_concurrent_jobs", value: 4, source: "file", class: "live", schema: { kind: "int", min: 1 } },
      { key: `[workspace."${project}"]`, value: "acme", source: "file", section: true, class: "next-claim" },
    ],
  },
};
const stateFile = join(stubDir, "state.json");
const writeState = () => writeFileAtomic(stateFile, JSON.stringify(state));
writeState();
writeFileSync(argvLog, "");
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename));
const state = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const args = process.argv.slice(2);
// Only a key write carries stdin; any other call must not wait on an open pipe.
const keyWrite = (args.includes("set") && args.includes("api_key")) || (args.includes("cell") && args.includes("add"));
let stdin = ""; if (keyWrite) { try { stdin = fs.readFileSync(0, "utf8"); } catch {} }
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify({ args, stdin }) + "\\n");
const out = (doc) => { process.stdout.write(JSON.stringify(doc) + "\\n"); process.exit(0); };
if (args.includes("--version")) { process.stdout.write("v2.20.21\\n"); process.exit(0); }
const rest = args.filter((a) => a !== "--json");
const flag = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] ?? "" : ""; };
const profile = flag("--profile");
const hostScope = rest.includes("--host");
const bare = [];
for (let i = 0; i < rest.length; i += 1) {
  if (rest[i] === "--profile" || rest[i] === "--server" || rest[i] === "--model-source" || rest[i] === "--expect-state" || rest[i] === "--cwd") { i += 1; continue; }
  if (rest[i] === "--host") continue;
  bare.push(rest[i]);
}
if (bare[0] === "version") out({ capabilities: ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"] });
if (bare[0] === "cell" && bare[1] === "list") out({ dir: path.join(dir, "cfg"), profiles: state.cells });
if (bare[0] === "cell" && ["add", "repair", "remove", "update"].includes(bare[1])) out({ ok: true, profile: bare[2] });
if (bare[0] === "config" && bare[1] === "list") {
  const items = hostScope ? state.items.host : profile && profile !== "default" ? state.items[profile] || [] : state.items.main;
  out({ file: { sha256: "e2e-sha-" + (hostScope ? "host" : profile || "main"), exists: true }, items });
}
if (bare[0] === "config" && bare[1] === "workspace") out({ mapped: false, workspace: null, yaml: null, steps: null });
if (bare[0] === "config" && (bare[1] === "set" || bare[1] === "unset")) out({ ok: true, key: bare[2], class: "live", outcome: "applied" });
if (bare[0] === "config" && bare[1] === "reload") out({ reloaded: [], pending: [] });
if (bare[0] === "ops" && bare[1] === "status") out({ queue: { pending: 1, running: 1, max_concurrent: 6, max_configured: 6 }, supervision: { step_timeout: "30m0s", stall_window: "10m0s" }, reload: { pending: [], overridden: [] } });
if (bare[0] === "workflows") out({ workflows_dir: "/e2e/workflows", daemon_reachable: true, reason: "", workspace: "", projects: [], write_capable: false, write_reason: "", bundles: [], bundle_errors: [] });
process.stderr.write("pf: unknown command " + bare.join(" ") + "\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
const calls = () => (existsSync(argvLog) ? readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)) : []);
const waitForCall = async (predicate, ms = 20_000) => {
  for (const deadline = Date.now() + ms; Date.now() < deadline;) {
    const hit = calls().find(predicate);
    if (hit) return hit;
    await delay(100);
  }
  return null;
};
const waitForValue = async (locator, want, ms = 20_000) => {
  for (const deadline = Date.now() + ms; Date.now() < deadline;) {
    if ((await locator.inputValue().catch(() => null)) === want) return true;
    await delay(100);
  }
  return false;
};

const failures = [];
const check = createCheck({ failures, message: joinedLines(4), record: ([label], text) => `${label}: ${text}` });

// The one history entry the Host pane's read-out shows (pi-web's own panel
// history; a page route, so the read-out has a row to place).
const HISTORY = [{ id: "e2e-364-h1", ts: "2026-11-07T00:00:00.000Z", key: "step_timeout", old: "20m0s", new: "30m0s", rollbackable: true, file: "cli.toml" }];

const h = createHarness({
  storage: { "pi-locale": "en", "pi-web:session-age-filter-days": "0" },
  shape: (o) => ({ ...o, viewport: { width: o.width, height: o.height } }),
  timeout: 30_000,
  errors: [],
  session: SESSION,
  ready: SESSION_TEXT,
  async setup(page) {
    await page.route("**/api/plugins?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ packages: [] }) }));
    await page.route("**/api/polyforge/config/history", (route) => (route.request().method() === "GET"
      ? route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ entries: HISTORY }) })
      : route.continue()));
  },
  async after(page) {
    const show = page.locator('button[aria-label="Show sidebar"]').first();
    if (await show.isVisible().catch(() => false)) await show.click();
    await page.locator('button[aria-label="Settings"]').first().click();
    const dialog = page.locator('[role="dialog"][aria-label="Settings"]').first();
    await dialog.waitFor({ state: "visible", timeout: NAV_MS });
    await dialog.getByRole("button", { name: "Polyforge", exact: true }).first().click();
    await dialog.locator('[data-pf-pane="host"]').first().waitFor({ state: "visible", timeout: 60_000 });
    // the registry read lands: both Cells listed
    await dialog.locator('[data-pf-cell-entry="public"]').first().waitFor({ state: "visible", timeout: 60_000 });
    return { context: page.context(), page, dialog };
  },
});
try {
  await h.startServer({
    name: "w364-cell-master-detail",
    logFile: join(work, "server.log"),
    env: {
      PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: home,
      PI_CODING_AGENT_DIR: agentDir },
  });
  await h.launchBrowser(() => chromium.launch());

  const { openPage } = h;

  const entry = (dialog, key) => dialog.locator(key === "host" ? "[data-pf-host-entry]" : key === "add" ? "[data-pf-add-entry]" : `[data-pf-cell-entry="${key}"]`).first();
  const pane = (dialog, key) => dialog.locator(`[data-pf-pane="${key === "host" || key === "add" ? key : `cell:${key}`}"]`).first();
  const select = async (dialog, key) => {
    await entry(dialog, key).click();
    await pane(dialog, key).waitFor({ state: "visible", timeout: 30_000 });
  };
  const shot = async (page, name) => {
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await page.screenshot({ path: join(screens, name) });
  };
  const visibleInputLabels = (scope) => scope.locator("input:visible").evaluateAll((nodes) => nodes.map((node) => node.getAttribute("aria-label") ?? ""));

  const desktop = await openPage({ width: 1280, height: 900 });
  const { page, dialog } = desktop;

  await check("M1 the list holds the Host entry, then both Cells with their states, then Add Cell", async () => {
    const tops = [];
    for (const key of ["host", "default", "public", "add"]) {
      const box = await entry(dialog, key).boundingBox();
      assert.ok(box, `the ${key} list entry has no box`);
      tops.push([key, box.y]);
    }
    for (let index = 1; index < tops.length; index += 1) {
      assert.ok(tops[index][1] > tops[index - 1][1], `${tops[index][0]} is not listed below ${tops[index - 1][0]}`);
    }
    assert.equal(await dialog.locator("[data-pf-cell-entry]").count(), 2, "the list does not hold exactly the two Cells");
    assert.equal(await entry(dialog, "default").getAttribute("data-pf-cell-state"), "serving");
    assert.match(await entry(dialog, "default").innerText(), /Serving/, "the default Cell's entry does not name its state");
    assert.equal(await entry(dialog, "public").getAttribute("data-pf-cell-state"), "execution-disabled");
    assert.match(await entry(dialog, "public").innerText(), /Execution disabled/, "the public Cell's entry does not name its state");
    assert.equal(await entry(dialog, "host").getAttribute("aria-current"), "page", "the page does not open on the Host entry");
    assert.match((await dialog.locator("[data-pf-cells]").first().innerText()).replace(/\s+/g, " "), /Cells 2/i, "the Cells group does not count its two Cells");
  });

  await check("M2 the Host pane is the one home of the Host-level settings and holds no Cell's", async () => {
    const host = pane(dialog, "host");
    // the overall status lands once the machine detection answers
    await host.locator('section[aria-label="Machine status"]').first().waitFor({ state: "visible", timeout: 60_000 });
    const ceiling = host.locator('input[aria-label="max_concurrent_jobs (host)"]').first();
    assert.ok(await waitForValue(ceiling, "6"), "the Host concurrency is not in the Host pane");
    assert.ok(await host.locator('input[aria-label="daemon_socket (host)"]').first().isVisible(), "a Host-only key is missing from the Host pane");
    for (const label of ["Polyforge package (global)", "Jobs card stream", "Recent panel writes", "Install / upgrade command"]) {
      assert.ok(await host.locator(`section[aria-label="${label}"]`).first().isVisible(), `${label} is not in the Host pane`);
    }
    // none of a Cell's settings in the Host pane
    assert.equal(await host.locator(".polyforge-config-cli, [data-pf-cell-region], [data-pf-cell-workspaces], section[aria-label=\"Workflow\"]").count(), 0, "a Cell's settings render in the Host pane");
    const inputs = await visibleInputLabels(dialog.locator(".config-detail"));
    assert.ok(inputs.length > 0, "the Host pane shows no input");
    for (const label of inputs) {
      assert.ok(/\(host\)$/.test(label) || /^(Title length|Poll interval)/i.test(label) || label === "Polyforge package (global)",
        `a non-Host input shows on the Host pane: ${label}`);
    }
    await shot(page, "desktop-host.png");
  });

  await check("M3 selecting a Cell switches the detail to that Cell's own settings only", async () => {
    await select(dialog, "public");
    assert.equal(await entry(dialog, "public").getAttribute("aria-current"), "page", "the selected entry is not marked current");
    assert.equal(await entry(dialog, "host").getAttribute("aria-current"), null, "the Host entry stays current");
    assert.equal(await pane(dialog, "host").isVisible(), false, "the Host pane stays shown beside the Cell's");
    const cell = pane(dialog, "public");
    const head = cell.locator('[data-pf-cell="public"]').first();
    const headText = (await head.innerText()).replace(/\s+/g, " ");
    assert.match(headText, /public/);
    assert.match(headText, /Execution disabled/, "the Cell's head does not name its state");
    assert.match(headText, /hub-public\.example/, "the Cell's head does not name its AIHub");
    assert.match(headText, /0 running · 1 pending · max 6/, "the Cell's head lost its counts");
    assert.match(headText, /Cell ceiling 2/, "the Cell's head lost its Cell ceiling");
    assert.match(headText, /no model-credential source declared/, "the Cell's head does not say why execution is disabled");
    assert.ok(await head.getByRole("button", { name: "Repair", exact: true }).isVisible(), "the Cell's pane offers no repair");
    assert.ok(await head.getByRole("button", { name: "Remove", exact: true }).isVisible(), "the Cell's pane offers no remove");
    assert.equal(await dialog.getByRole("button", { name: /cleanup/i }).count(), 0, "the page offers a permanent-cleanup control");
    // connection and identity, the write-only key, Cell concurrency, model source
    const region = cell.locator(".polyforge-config-cli").first();
    await region.waitFor({ state: "visible", timeout: 60_000 });
    assert.ok(await waitForValue(region.locator('input[aria-label="cell_max_concurrent_jobs"]').first(), "2"), "the Cell concurrency is not the Cell's");
    assert.ok(await waitForValue(region.locator('input[aria-label="step_timeout"]').first(), "45m0s"), "the Cell's own step_timeout is not shown");
    assert.ok(await waitForValue(region.locator('input[aria-label="user_id"]').first(), "usr_public"), "the Cell's identity user is not shown");
    assert.ok(await region.locator('input[aria-label="model.source"]').first().isVisible(), "the Cell's model source is not editable");
    const serverRow = region.locator(".polyforge-config-row").filter({ has: page.locator('[data-pf-identity-hint="server"]') }).first();
    assert.match(await serverRow.textContent(), /hub-public\.example/, "the Cell's AIHub server is not shown read-only");
    assert.equal(await region.locator('input[aria-label="server"]').count(), 0, "the Cell's AIHub server is editable in place");
    assert.equal(await region.locator("[data-pf-api-key-state]").first().getAttribute("data-pf-api-key-state"), "set", "the key's set/unset state is not shown");
    const key = region.locator('input[aria-label="api_key"]').first();
    assert.equal(await key.getAttribute("type"), "password", "the Cell's api_key input is not write-only");
    assert.equal(await key.inputValue(), "", "a key value reached the page");
    // workspace mapping
    const mapping = cell.locator('[data-pf-cell-workspaces="public"]').first();
    assert.match(await mapping.textContent(), /ws-public/, "the Cell's workspace mapping is not shown");
    // every visible input is this Cell's own: inside its pane, none a Host key's
    const inputs = await visibleInputLabels(dialog.locator(".config-detail"));
    assert.ok(inputs.length >= 6, `the Cell's pane shows too few inputs: ${inputs.join(", ")}`);
    const outside = await dialog.locator(".config-detail input:visible").evaluateAll((nodes) => nodes.filter((node) => !node.closest('[data-pf-pane="cell:public"]')).map((node) => node.getAttribute("aria-label") ?? ""));
    assert.deepEqual(outside, [], "an input that is not public's shows in its pane");
    for (const label of inputs) {
      assert.ok(!/\(host\)$/.test(label) && !["max_concurrent_jobs", "daemon_socket", "harness_root", "operator_agent_dir"].includes(label), `a Host input shows in a Cell's pane: ${label}`);
    }
    assert.equal(await cell.locator('input[aria-label="max_concurrent_jobs"]').count(), 0, "the Host ceiling rendered in a Cell's pane");
    await shot(page, "desktop-cell-public.png");

    // the default Cell: its settings are the cli.toml card, its own ceiling a visible row, no Host key
    await select(dialog, "default");
    const def = pane(dialog, "default");
    await def.locator(".polyforge-config-cli").first().waitFor({ state: "visible", timeout: 30_000 });
    assert.equal(await pane(dialog, "public").isVisible(), false, "the public pane stays shown beside the default Cell's");
    assert.ok(await waitForValue(def.locator('input[aria-label="cell_max_concurrent_jobs"]').first(), "4"), "the default Cell's own ceiling is not a visible row");
    assert.ok(await def.locator('input[aria-label="step_timeout"]').first().isVisible(), "the default Cell's step_timeout is not shown");
    for (const hostKey of ["max_concurrent_jobs", "daemon_socket", "harness_root", "operator_agent_dir"]) {
      assert.equal(await def.getByText(hostKey, { exact: true }).count(), 0, `the Host key ${hostKey} renders in the default Cell's pane`);
    }
    assert.match(await def.locator('[data-pf-model-source="default"]').first().innerText(), /operator_agent_dir/, "the default Cell does not say where its model credentials come from");
    assert.equal(await def.locator("[data-pf-settings-region]").count(), 0, "the default Cell opened a second region");
    await shot(page, "desktop-cell-default.png");

    // a pane keeps its draft across a switch (it stays mounted, hidden)
    await select(dialog, "public");
    const stepTimeout = pane(dialog, "public").locator('input[aria-label="step_timeout"]').first();
    await stepTimeout.fill("50m");
    await select(dialog, "host");
    await select(dialog, "public");
    assert.equal(await stepTimeout.inputValue(), "50m", "switching entries dropped the Cell's draft");
    await stepTimeout.fill("45m0s");
  });

  await check("M4 a Cell's list state follows pf's registry, worst first", async () => {
    const reread = async () => {
      await dialog.getByRole("button", { name: "Re-detect", exact: true }).first().click();
      await delay(300);
    };
    const stateOf = () => entry(dialog, "public").getAttribute("data-pf-cell-state");
    const waitState = async (want) => {
      for (const deadline = Date.now() + 20_000; Date.now() < deadline;) {
        if ((await stateOf()) === want) return true;
        await delay(150);
      }
      return false;
    };
    Object.assign(PUBLIC_CELL, { draining: true, pending_change: "api_key rotation (same identity) waits for the Cell to go idle" });
    writeState();
    await reread();
    assert.ok(await waitState("pending-restart"), `a draining Cell is not shown pending (${await stateOf()})`);
    assert.match(await entry(dialog, "public").innerText(), /Pending restart/);
    assert.match(await pane(dialog, "public").locator('[data-pf-cell="public"]').first().innerText(), /waits for the Cell to go idle/, "the pane does not say what the pending change waits for");
    Object.assign(PUBLIC_CELL, { state: "degraded", reason: "AIHub unreachable or refusing: e2e" });
    writeState();
    await reread();
    assert.ok(await waitState("degraded"), `a degraded Cell is not shown degraded (${await stateOf()})`);
    assert.match(await entry(dialog, "public").innerText(), /Degraded/);
    Object.assign(PUBLIC_CELL, { state: "serving", reason: undefined, draining: false, pending_change: undefined, execution_disabled: undefined });
    writeState();
    await reread();
    assert.ok(await waitState("serving"), `a serving Cell is not shown serving (${await stateOf()})`);
    Object.assign(PUBLIC_CELL, { execution_disabled: EXEC_DISABLED });
    writeState();
    await reread();
    assert.ok(await waitState("execution-disabled"), "the fixture's state did not come back");
  });

  await check("M5 a Cell's lifecycle and key writes stay on the existing routes and pf's writer", async () => {
    await select(dialog, "public");
    const region = pane(dialog, "public").locator(".polyforge-config-cli").first();
    const ceiling = region.locator('input[aria-label="cell_max_concurrent_jobs"]').first();
    await ceiling.fill("3");
    await region.locator(".polyforge-config-row", { has: page.locator('input[aria-label="cell_max_concurrent_jobs"]') }).getByRole("button", { name: "Save", exact: true }).click();
    const set = await waitForCall((call) => call.args.includes("--profile") && call.args.includes("public") && call.args.includes("set") && call.args.includes("cell_max_concurrent_jobs"));
    assert.ok(set, "the Cell ceiling write did not reach pf config set for its Cell");
    assert.ok(set.args.includes("--expect-state"), "the Cell write lost the CAS expectation");
    const KEY = "E2E-W364-PUBLIC-KEY-SENTINEL";
    await region.locator('input[aria-label="api_key"]').first().fill(KEY);
    await region.locator(".polyforge-config-row", { has: page.locator('input[aria-label="api_key"]') }).getByRole("button", { name: "Save", exact: true }).click();
    const confirm = pane(dialog, "public").locator('[data-pf-confirm="api_key"]');
    await confirm.waitFor({ state: "visible", timeout: 10_000 });
    await confirm.getByRole("button", { name: "Confirm save", exact: true }).click();
    const keyCall = await waitForCall((call) => call.args.includes("public") && call.args.includes("set") && call.args.includes("api_key"));
    assert.ok(keyCall, "the Cell api_key set never reached pf config set");
    assert.ok(keyCall.stdin.includes(KEY) && !keyCall.args.some((token) => token.includes(KEY)), "the key did not travel on stdin only");
    assert.ok(!(await page.locator("body").innerText()).includes(KEY), "the page echoed the key");
    assert.ok(!readFileSync(join(work, "server.log"), "utf8").includes(KEY), "the server log carries the key");
    await pane(dialog, "public").locator('[data-pf-cell="public"]').getByRole("button", { name: "Repair", exact: true }).click();
    assert.ok(await waitForCall((call) => call.args.includes("cell") && call.args.includes("repair") && call.args.includes("public")), "repair never reached pf cell repair");
    await select(dialog, "add");
    const add = pane(dialog, "add").locator("[data-pf-cell-add]").first();
    assert.ok(await add.locator('input[aria-label="API key"]').first().isVisible(), "the Add Cell pane has no form");
    assert.equal(await add.locator('input[aria-label="API key"]').first().getAttribute("type"), "password", "the new Cell's key input is not write-only");
    await shot(page, "desktop-add-cell.png");
  });

  await desktop.context.close();

  await check("M6 a 420px viewport puts the list above the detail on one screen", async () => {
    const narrow = await openPage({ width: 420, height: 900 });
    try {
      const sidebar = await narrow.dialog.locator(".config-sidebar").first().boundingBox();
      const detail = await narrow.dialog.locator(".config-detail").first().boundingBox();
      assert.ok(sidebar && detail, "the list or the detail has no box");
      assert.ok(sidebar.y + sidebar.height <= detail.y + 1, `the list does not sit above the detail (list bottom ${sidebar.y + sidebar.height}, detail top ${detail.y})`);
      assert.ok(Math.abs(sidebar.width - detail.width) <= 2 && sidebar.width >= 380, `the list and the detail do not both span the narrow page (${sidebar.width} / ${detail.width})`);
      assert.ok(detail.height >= 200, `the detail is squeezed under the list (${detail.height}px)`);
      for (const key of ["host", "default", "public", "add"]) {
        assert.ok(await entry(narrow.dialog, key).isVisible(), `the ${key} entry is not visible on a narrow viewport`);
      }
      assert.ok(await pane(narrow.dialog, "host").isVisible(), "the Host pane is not shown under the list");
      const overflow = await narrow.dialog.evaluate((node) => ({ scrollWidth: node.scrollWidth, clientWidth: node.clientWidth }));
      assert.ok(overflow.scrollWidth <= overflow.clientWidth + 1, `the narrow page overflows horizontally (${overflow.scrollWidth} > ${overflow.clientWidth})`);
      await pane(narrow.dialog, "host").locator('section[aria-label="Machine status"]').first().waitFor({ state: "visible", timeout: 60_000 });
      assert.ok(await waitForValue(pane(narrow.dialog, "host").locator('input[aria-label="max_concurrent_jobs (host)"]').first(), "6"), "the narrow Host pane did not load its Host keys");
      await shot(narrow.page, "narrow-420-host.png");
      // selecting a Cell keeps the list on screen: no one-pane drill-in
      await select(narrow.dialog, "public");
      assert.ok(await narrow.dialog.locator(".config-sidebar").first().isVisible(), "selecting a Cell hid the list");
      await pane(narrow.dialog, "public").locator(".polyforge-config-cli").first().waitFor({ state: "visible", timeout: 60_000 });
      assert.ok(await pane(narrow.dialog, "public").locator(".polyforge-config-cli").first().isVisible(), "the Cell's settings are not shown under the list");
      assert.ok(await waitForValue(pane(narrow.dialog, "public").locator('input[aria-label="step_timeout"]').first(), "45m0s"), "the narrow Cell pane did not load its keys");
      const cellOverflow = await narrow.dialog.locator(".config-detail").first().evaluate((node) => ({ scrollWidth: node.scrollWidth, clientWidth: node.clientWidth }));
      assert.ok(cellOverflow.scrollWidth <= cellOverflow.clientWidth + 1, `the narrow Cell pane overflows horizontally (${cellOverflow.scrollWidth} > ${cellOverflow.clientWidth})`);
      await shot(narrow.page, "narrow-420-cell-public.png");
    } finally {
      await narrow.context.close();
    }
  });
} catch (error) {
  failures.push(`suite: ${String(error?.message ?? error).split("\n").slice(0, 4).join(" | ")}`);
  console.log(`FAIL suite: ${String(error?.message ?? error).split("\n").slice(0, 4).join(" | ")}`);
} finally {
  await h.close();
}
console.log(`screenshots: ${screens}`);
console.log(`${failures.length ? "FAIL" : "PASS"} w364-cell-master-detail: ${check.count - failures.length}/${check.count} scenario(s) passed`);
for (const failure of failures) console.log(`  ${failure}`);
process.exit(failures.length ? 1 : 0);
