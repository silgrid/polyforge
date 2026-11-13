// carries: lib/polyforge-cells*, app/api/polyforge/cells/*
// carries: app/api/polyforge/polyforge-cells-routes*
// — Settings › Polyforge Host and Cells page acceptance.
//
// Authored against artifacts/spec.md (AC8) and reproduced in the capsule's
// artifacts/e2e-scenarios.md as P1/P2. The page is driven as a browser page
// against the app's real routes; `pf` is a stub first on PATH answering the
// real pf wire (`--json version` capabilities, `--json cell list`, the scoped
// `--json config list`, `pf cell add|repair|remove`, the scoped config set and
// `--json ops status`). The suite asserts what the page renders, the exact
// argv/stdin the server hands to pf, and that an api key never appears in the
// argv, the page or the server log.
//
// Baseline posture: the Host/Cells card and its routes do not exist yet, so
// both scenarios are assertion-level red (a missing card/region, a write that
// never reaches pf) — never a build or fixture error.
//
// restated the surfaces, not the assertions: the page is
// master-detail, so the Host region is the Host pane's, a Cell's row is its
// list entry plus its pane head (lifecycle controls there), its region is its
// pane, and Add Cell is its own pane. Every check below still runs.
// restated them again: a named Cell's keys are its pane's file
// card (the default Cell's same component), so alpha's region checks run on
// that card — its inputs labelled by key, its api_key row's Save and the
// card's confirmation. Every check below still runs.
//
//   node e2e/w348-cell-page.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, joinedLines, writeHostSession } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-348-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-348-cells";
for (const dir of [agentDir, project, join(home, ".config", "polyforge")]) mkdirSync(dir, { recursive: true });
writeHostSession(agentDir, { id: SESSION, cwd: project, text: "E2E workbench 348 Cell page host session", timestamp: "2026-11-05T00:00:00.000Z" });

// ---- stub pf: the Cell registry, the scoped config reads/writes, lifecycle ----
const stubDir = join(work, "stub");
mkdirSync(stubDir);
const argvLog = join(stubDir, "argv.log");
const state = {
  cells: [
    { profile: "default", state: "serving", valid: true, server: "https://hub-a.example", workspaces: [project], running: 0, pending: 0, max_concurrent: 6 },
    { profile: "alpha", state: "serving", valid: true, server: "https://hub-b.example", workspaces: [join(work, "ws-alpha")], running: 1, pending: 2, max_concurrent: 6, cell_max_concurrent_jobs: 3 },
  ],
  items: {
    host: [
      { key: "max_concurrent_jobs", value: 6, source: "file", class: "live", schema: { kind: "int", min: 1 } },
      { key: "daemon_socket", value: "/run/pf.sock", source: "file", class: "restart-only" },
      { key: "harness_root", value: "/opt/pf", source: "file", class: "restart-only" },
      { key: "operator_agent_dir", value: "/home/op/.pi", source: "file", class: "restart-only" },
    ],
    alpha: [
      { key: "server", value: "https://hub-b.example", source: "file", class: "restart-only" },
      { key: "api_key", set: true, source: "file", class: "restart-only" },
      { key: "user_id", value: "usr_alpha", source: "file", class: "cli-only" },
      { key: "step_timeout", value: "30m0s", source: "file", class: "live", schema: { kind: "duration" } },
      { key: "stall_window", value: "10m0s", source: "file", class: "live", schema: { kind: "duration" } },
      { key: "claude_bin", value: "/usr/local/bin/claude", source: "file", class: "live" },
      { key: "cell_max_concurrent_jobs", value: 3, source: "file", class: "live", schema: { kind: "int", min: 1 } },
      { key: "model.source", value: "shared-default", source: "file", class: "restart-only" },
    ],
    main: [
      { key: "server", value: "https://hub-a.example", source: "file", class: "restart-only" },
      { key: "api_key", set: true, source: "file", class: "restart-only" },
      { key: "user_id", value: "usr_root", source: "file", class: "cli-only" },
      { key: "step_timeout", value: "30m0s", source: "file", class: "live", schema: { kind: "duration" } },
      { key: "stall_window", value: "10m0s", source: "file", class: "live", schema: { kind: "duration" } },
      { key: "max_concurrent_jobs", value: 6, source: "file", class: "live", schema: { kind: "int", min: 1 } },
    ],
  },
};
const stateFile = join(stubDir, "state.json");
writeFileSync(stateFile, JSON.stringify(state));
writeFileSync(argvLog, "");
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename));
const state = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const args = process.argv.slice(2);
let stdin = ""; try { stdin = fs.readFileSync(0, "utf8"); } catch {}
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify({ args, stdin }) + "\\n");
if (stdin) fs.appendFileSync(path.join(dir, "stdin.txt"), stdin + "\\n");
const out = (doc) => { process.stdout.write(JSON.stringify(doc) + "\\n"); process.exit(0); };
if (args.includes("--version")) { process.stdout.write("v2.20.21\\n"); process.exit(0); }
const rest = args.filter((a) => a !== "--json");
const at = (name) => rest.indexOf(name);
const flag = (name) => { const i = at(name); return i >= 0 ? rest[i + 1] ?? "" : ""; };
let profile = flag("--profile");
const hostScope = rest.includes("--host");
const bare = [];
for (let i = 0; i < rest.length; i += 1) {
  if (rest[i] === "--profile" || rest[i] === "--host" || rest[i] === "--server" || rest[i] === "--model-source" || rest[i] === "--expect-state" || rest[i] === "--cwd") { i += 1; continue; }
  bare.push(rest[i]);
}
if (bare[0] === "version") out({ capabilities: ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"] });
if (bare[0] === "cell" && bare[1] === "list") out({ dir: path.join(dir, "cfg"), profiles: state.cells });
if (bare[0] === "cell" && bare[1] === "add") out({ ok: true, profile: bare[2] });
if (bare[0] === "cell" && (bare[1] === "repair" || bare[1] === "remove" || bare[1] === "update")) out({ ok: true, profile: bare[2] });
if (bare[0] === "config" && bare[1] === "list") {
  // the default Cell's pane names its Cell (--profile default)
  const items = hostScope ? state.items.host : profile && profile !== "default" ? state.items[profile] || [] : state.items.main;
  out({ file: { sha256: "e2e-sha-" + (hostScope ? "host" : profile || "main"), exists: true }, items });
}
if (bare[0] === "config" && bare[1] === "workspace") out({ mapped: false, workspace: null, yaml: null, steps: null });
if (bare[0] === "config" && (bare[1] === "set" || bare[1] === "unset")) out({ ok: true, key: bare[2], class: "live", outcome: "applied" });
if (bare[0] === "config" && bare[1] === "reload") out({ reloaded: [], pending: [] });
if (bare[0] === "ops" && bare[1] === "status") out({ queue: { pending: 0, running: 0, max_concurrent: 6, max_configured: 6 }, supervision: { step_timeout: "30m0s", stall_window: "10m0s" }, reload: { pending: [], overridden: [] } });
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

const h = createHarness();
let base;
try {
  ({ base } = await h.startServer({
    name: "w348-cell-page",
    logFile: join(work, "server.log"),
    env: {
      PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: home,
      PI_CODING_AGENT_DIR: agentDir },
  }));
  await h.launchBrowser(() => chromium.launch());
  const context = await h.browser.newContext({ viewport: { width: 1280, height: 1000 }, locale: "en-US" });
  await context.addInitScript(() => { try { window.localStorage.setItem("pi-locale", "en"); } catch { /* storage */ } });
  const page = await context.newPage();
  page.setDefaultTimeout(30_000);
  await page.route("**/api/plugins?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ packages: [] }) }));

  const openSettings = async () => {
    await page.goto(`${base}/?session=${SESSION}`, { waitUntil: "domcontentloaded", timeout: NAV_MS });
    await page.getByText("E2E workbench 348 Cell page host session").first().waitFor({ state: "attached", timeout: NAV_MS });
    const show = page.locator('button[aria-label="Show sidebar"]').first();
    if (await show.isVisible().catch(() => false)) await show.click();
    await page.locator('button[aria-label="Settings"]').first().click();
    const dialog = page.locator('[role="dialog"][aria-label="Settings"]').first();
    await dialog.getByRole("button", { name: "Polyforge", exact: true }).click();
    await page.locator(".settings-polyforge").first().waitFor({ state: "attached", timeout: 60_000 });
    await page.locator("[data-pf-cells]").first().waitFor({ state: "attached", timeout: 60_000 });
  };
  let ready = true;
  try {
    await openSettings();
  } catch (error) {
    // The card/region is the surface under test: its absence is each
    // scenario's own assertion-level red, not a suite crash.
    ready = false;
    const message = String(error?.message ?? error).split("\n").slice(0, 4).join(" | ");
    for (const label of ["P1", "P2"]) {
      check.count += 1;
      failures.push(`${label}: ${message}`);
      console.log(`FAIL ${label}: ${message}`);
    }
  }
  // a Cell's row is its list entry; selecting it shows its pane.
  const selectEntry = async (entry, pane) => {
    await page.locator(entry).first().click();
    await page.locator(`[data-pf-pane="${pane}"]`).first().waitFor({ state: "visible", timeout: 30_000 });
  };
  const selectAlpha = () => selectEntry('[data-pf-cell-entry="alpha"]', "cell:alpha");
  const alphaRow = () => page.locator('[data-pf-cell="alpha"]').first();
  // alpha's own keys are its pane's file card
  const alphaRegion = () => page.locator('[data-pf-pane="cell:alpha"] .polyforge-config-cli').first();
  const alphaPane = () => page.locator('[data-pf-pane="cell:alpha"]').first();

  if (ready) await check("P1", async () => {
    // Host and Cells are layered: the Host region edits the Host-only keys
    // (including the Host ceiling), one named Cell's region edits that Cell's
    // own keys (including its optional Cell ceiling); neither edits the other.
    await selectEntry("[data-pf-host-entry]", "host");
    const host = page.locator('[data-pf-settings-region="host"]').first();
    await host.waitFor({ state: "attached", timeout: 60_000 });
    const hostCeiling = host.locator('input[aria-label="max_concurrent_jobs (host)"]').first();
    await hostCeiling.waitFor({ state: "visible", timeout: 20_000 });
    assert.ok(await waitForValue(hostCeiling, "6"), "the Host region does not carry the Host ceiling");
    assert.ok(await host.locator('input[aria-label="daemon_socket (host)"]').count() > 0, "the Host region lost a Host-only key");
    assert.equal(await host.locator('input[aria-label="step_timeout (host)"]').count(), 0, "a Cell key rendered in the Host region");
    await selectEntry('[data-pf-cell-entry="default"]', "cell:default");
    const defaultPane = page.locator('[data-pf-pane="cell:default"]').first();
    await defaultPane.locator(".polyforge-config-cli").first().waitFor({ state: "visible", timeout: 30_000 });
    assert.equal(await page.locator('[data-pf-settings-region="default"]').count(), 0, "the default Cell opened a second region instead of the cli.toml card");
    await selectAlpha();
    assert.match((await alphaRow().innerText()).replace(/\s+/g, " "), /Cell ceiling 3/, "the alpha row does not show its Cell ceiling");
    const region = alphaRegion();
    await region.waitFor({ state: "attached", timeout: 30_000 });
    const cellCeiling = region.locator('input[aria-label="cell_max_concurrent_jobs"]').first();
    await cellCeiling.waitFor({ state: "visible", timeout: 20_000 });
    assert.ok(await waitForValue(cellCeiling, "3"), "the alpha region does not carry its Cell ceiling");
    assert.ok(await region.locator('input[aria-label="step_timeout"]').count() > 0, "the alpha region lost a Cell key");
    assert.equal(await alphaPane().locator('input[aria-label="max_concurrent_jobs"]').count(), 0, "the Host ceiling rendered in the Cell region");
    assert.ok(await region.getByText(/A Cell's AIHub server and user never change in place/).count() > 0, "the Cell region lost the new-profile identity guidance");
    const dialog = page.locator('[role="dialog"][aria-label="Settings"]').first();
    assert.equal(await dialog.getByRole("button", { name: /cleanup/i }).count(), 0, "the page offers a permanent-cleanup control");
  });

  if (ready) await check("P2", async () => {
    // Make this scenario independent of P1's scroll/click state.
    await selectAlpha();
    await alphaRegion().waitFor({ state: "attached", timeout: 30_000 });
    await alphaRegion().locator('input[aria-label="cell_max_concurrent_jobs"]').first()
      .waitFor({ state: "visible", timeout: 20_000 });
    await selectEntry("[data-pf-add-entry]", "add");
    const add = page.locator("[data-pf-cell-add]").first();
    await add.waitFor({ state: "attached", timeout: 30_000 });
    const ADD_KEY = "E2E-W348-ADD-KEY-SENTINEL";
    await add.locator('input[aria-label="Profile"]').fill("beta");
    await add.locator('input[aria-label="AIHub server"]').fill("https://hub-b.example");
    await add.locator('input[aria-label="API key"]').fill(ADD_KEY);
    await add.locator('input[aria-label="Model source"]').fill("shared-default");
    await add.getByRole("button", { name: "Add Cell", exact: true }).click();
    const addCall = await waitForCall((call) => call.args.includes("cell") && call.args.includes("add") && call.args.includes("beta"));
    assert.ok(addCall, "Cell add never reached pf cell add");
    assert.ok(addCall.args.includes("--server") && addCall.args.includes("https://hub-b.example"), `Cell add lost its server argv: ${JSON.stringify(addCall.args)}`);
    assert.ok(addCall.args.includes("--model-source") && addCall.args.includes("shared-default"), `Cell add lost its model-source argv: ${JSON.stringify(addCall.args)}`);
    assert.ok(addCall.stdin.includes(ADD_KEY), "the api key did not reach pf on stdin");
    assert.ok(!addCall.args.some((token) => token.includes(ADD_KEY)), "the api key appeared as an argv token");
    assert.ok(await waitForValue(add.locator('input[aria-label="API key"]'), ""), "the add form kept the api key after submit");

    await selectAlpha();
    await alphaRow().getByRole("button", { name: "Repair", exact: true }).click();
    assert.ok(await waitForCall((call) => call.args.includes("cell") && call.args.includes("repair") && call.args.includes("alpha")), "Cell repair never reached pf cell repair");
    await alphaRow().getByRole("button", { name: "Remove", exact: true }).click();
    await alphaRow().getByRole("button", { name: /Confirm remove/ }).click();
    assert.ok(await waitForCall((call) => call.args.includes("cell") && call.args.includes("remove") && call.args.includes("alpha")), "Cell remove never reached pf cell remove");

    // The Cell region's api_key is write-only: a password input, set/unset
    // only, cleared after every submit, and never echoed by argv, page or log.
    // (The stub keeps listing alpha after its removal, so its pane stays.)
    assert.ok(await page.locator('[data-pf-pane="cell:alpha"]').first().isVisible(), "a Cell pf still lists lost its pane after a removal");
    const region = alphaRegion();
    await region.waitFor({ state: "attached", timeout: 30_000 });
    const keyInput = region.locator('input[aria-label="api_key"]').first();
    await keyInput.waitFor({ state: "visible", timeout: 20_000 });
    assert.equal(await keyInput.getAttribute("type"), "password", "the Cell api_key input is not write-only");
    assert.equal(await keyInput.inputValue(), "", "the Cell api_key value reached the browser");
    const CELL_KEY = "E2E-W348-CELL-KEY-SENTINEL";
    await keyInput.fill(CELL_KEY);
    await region.locator(".polyforge-config-row", { has: page.locator('input[aria-label="api_key"]') }).getByRole("button", { name: "Save", exact: true }).click();
    const confirm = alphaPane().locator('[data-pf-confirm="api_key"]');
    await confirm.waitFor({ state: "visible", timeout: 10_000 });
    await confirm.getByRole("button", { name: "Confirm save", exact: true }).click();
    const keyCall = await waitForCall((call) => call.args.includes("--profile") && call.args.includes("alpha") && call.args.includes("config") && call.args.includes("set") && call.args.includes("api_key"));
    assert.ok(keyCall, "the Cell api_key set never reached pf config set");
    assert.ok(keyCall.stdin.includes(CELL_KEY), "the Cell api key did not reach pf on stdin");
    assert.ok(!keyCall.args.some((token) => token.includes(CELL_KEY)), "the Cell api key appeared as an argv token");
    assert.ok(await waitForValue(keyInput, ""), "the Cell api_key input kept its value after submit");
    const bodyText = await page.locator("body").innerText();
    assert.ok(!bodyText.includes(CELL_KEY), "the page echoed the Cell api key");
    assert.ok(!bodyText.includes(ADD_KEY), "the page echoed the added Cell's api key");
    const log = readFileSync(join(work, "server.log"), "utf8");
    assert.ok(!log.includes(CELL_KEY) && !log.includes(ADD_KEY), "the server log carries an api key value");
  });
} catch (error) {
  failures.push(`suite: ${String(error?.message ?? error).split("\n").slice(0, 4).join(" | ")}`);
  console.log(`FAIL suite: ${String(error?.message ?? error).split("\n").slice(0, 4).join(" | ")}`);
} finally {
  await h.close();
}
console.log(`${failures.length ? "FAIL" : "PASS"} w348-cell-page: ${check.count - failures.length}/${check.count} scenario(s) passed`);
for (const failure of failures) console.log(`  ${failure}`);
process.exit(failures.length ? 1 : 0);
