// carries: lib/polyforge-unclassified*, app/api/polyforge/unclassified/*
// — polyforge#51, end-to-end: the Host region's additional
// failure-classification pattern lists, the two slow-model latency bounds
// and the unclassified-error list (the last 7 days' fallback texts), driven
// as a browser page against the app's real routes with `pf` stubbed first
// on PATH (the Host-scoped `--json config list --host`, `--json ops
// unclassified`, `config set … --host`), so the suite asserts both what
// the page renders and the argv the server hands to pf.
//
// components/PolyforgeConfig*, lib/polyforge-config-schema* and
// lib/polyforge-config-write* are deliberately NOT carried: three suites
// already declare each of them (the e2e-carries cap is three per tracked
// path); this suite's own surface — the unclassified read and its panel —
// is what it uniquely pins.
//
//   node e2e/w51-error-classes.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, joinedLines, writeHostSession } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-51-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const SESSION = "e2e-pf-51";
for (const dir of [agentDir, join(home, ".config", "polyforge")]) mkdirSync(dir, { recursive: true });
writeHostSession(agentDir, { id: SESSION, cwd: "/workspace", text: "E2E polyforge 51 host session", timestamp: "2026-11-16T00:00:00.000Z" });

// ---- the stub: the Host-scoped config list, the unclassified report, writes ----
const stubDir = join(work, "stub");
mkdirSync(stubDir);
const argvLog = join(stubDir, "argv.log");
const HOST_ITEMS = [
  { key: "max_concurrent_jobs", value: 6, source: "file", class: "live", schema: { kind: "int", min: 1 } },
  { key: "daemon_socket", value: "/run/pf.sock", source: "file", class: "restart-only" },
  { key: "harness_root", value: "/opt/pf", source: "file", class: "restart-only" },
  { key: "operator_agent_dir", value: "/home/op/.pi", source: "file", class: "restart-only" },
  { key: "slow_model_median_latency", value: "45s", source: "file", class: "live", schema: { kind: "duration", min_exclusive: 0 } },
  { key: "slow_model_p90_latency", value: "3m0s", source: "file", class: "live", schema: { kind: "duration", min_exclusive: 0 } },
  { key: "transient_error_patterns", value: ["\\bupstream\\b hiccup", "payment retry"], source: "file", class: "live", schema: { kind: "regex-list" } },
  { key: "model_error_patterns", value: ["weird model failure"], source: "file", class: "live", schema: { kind: "regex-list" } },
];
const CELL_ITEMS = [
  { key: "step_timeout", value: "30m0s", source: "file", class: "live", schema: { kind: "duration" } },
  { key: "max_concurrent_jobs", value: 6, source: "file", class: "live", schema: { kind: "int", min: 1 } },
];
const UNCLASSIFIED = {
  window_days: 7,
  items: [
    { text: "gateway tantrum A", count: 3, last_at: "2026-11-15T10:00:00Z" },
    { text: "upstream wailed mysteriously", count: 1, last_at: "2026-11-14T09:00:00Z" },
  ],
};
const stateFile = join(stubDir, "state.json");
const hostItemsFile = join(stubDir, "host-items.json");
writeFileSync(hostItemsFile, JSON.stringify(HOST_ITEMS));
const writeFileAtomic = (path, data) => writeFileSync(path, data);
const saveState = (state) => writeFileAtomic(stateFile, JSON.stringify(state));
saveState({ refuse: "" });
writeFileSync(argvLog, "");
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename));
const state = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const args = process.argv.slice(2);
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify({ args }) + "\\n");
// the verb after the global flags: --json, and a Cell pane's --profile <cell>
const rest = args.filter((a, i) => a !== "--json" && a !== "--profile" && args[i - 1] !== "--profile");
const flag = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] ?? "" : ""; };
const out = (doc) => { process.stdout.write(JSON.stringify(doc) + "\\n"); process.exit(0); };
const hostItems = () => JSON.parse(fs.readFileSync(path.join(dir, "host-items.json"), "utf8"));
// the default Cell's file is cli.toml: pf lists the Host keys there too, so
// the Cell card's Host-only filter is what keeps the four keys out of it
const cellItems = ${JSON.stringify(CELL_ITEMS)};
if (args.includes("--version")) out({ version: "v9.9.9", capabilities: ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"] });
if (rest[0] === "cell" && rest[1] === "list") out({ dir: path.join(dir, "cfg"), profiles: [{ profile: "default", file: "cli.toml", state: "serving", valid: true, workspaces: [] }] });
if (rest[0] === "config" && rest[1] === "list") out({ file: { sha256: "e2e-sha", exists: true }, items: rest.includes("--host") ? hostItems() : [...cellItems, ...hostItems().filter((item) => !cellItems.some((own) => own.key === item.key))] });
if (rest[0] === "config" && rest[1] === "workspace") out({ mapped: false, workspace: null, yaml: null, steps: null });
if (rest[0] === "ops" && rest[1] === "status") out({ queue: { pending: 0, running: 0, max_concurrent: 6, max_configured: 6 }, supervision: { step_timeout: "30m0s", stall_window: "10m0s" }, reload: { pending: [], overridden: [] } });
if (rest[0] === "ops" && rest[1] === "unclassified") out(${JSON.stringify(UNCLASSIFIED)});
if (rest[0] === "config" && (rest[1] === "set" || rest[1] === "unset")) {
  const key = rest[2];
  if (state.refuse === key) { process.stderr.write(JSON.stringify({ error: { code: "CORE-400", detail: key + " pattern ( is not a compilable RE2 regex" } }) + "\\n"); process.exit(4); }
  if (rest.includes("--host") && rest[1] === "set") {
    const itemsFile = path.join(dir, "host-items.json");
    const items = JSON.parse(fs.readFileSync(itemsFile, "utf8"));
    const item = items.find((entry) => entry.key === key);
    const value = rest.find((arg, i) => i > rest.indexOf(key) && (arg.startsWith("[") || !arg.startsWith("-")));
    if (item && value) { item.value = item.schema && item.schema.kind === "regex-list" ? JSON.parse(value) : value; }
    fs.writeFileSync(itemsFile, JSON.stringify(items));
  }
  out({ ok: true, key, class: "live", outcome: "applied" });
}
if (rest[0] === "config" && rest[1] === "reload") out({ reloaded: [], pending: [] });
if (rest[0] === "workflows") out({ workflows_dir: "/e2e/workflows", daemon_reachable: true, reason: "", workspace: "", projects: [], write_capable: false, write_reason: "", bundles: [], bundle_errors: [] });
process.stderr.write("pf: unknown command " + rest.join(" ") + "\\n"); process.exit(2);
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

const failures = [];
const check = createCheck({ failures, message: joinedLines(4), record: ([label], text) => `${label}: ${text}` });

const h = createHarness();
let base;
let page;
try {
  ({ base } = await h.startServer({
    name: "polyforge-51",
    logFile: join(work, "server.log"),
    env: { PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: home, PI_CODING_AGENT_DIR: agentDir },
  }));
  await h.launchBrowser(() => chromium.launch());
  const context = await h.browser.newContext({ viewport: { width: 1280, height: 1000 }, locale: "en-US" });
  await context.addInitScript(() => { try { window.localStorage.setItem("pi-locale", "en"); } catch { /* storage */ } });
  page = await context.newPage();
  page.setDefaultTimeout(30_000);
  await page.route("**/api/plugins?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ packages: [] }) }));

  const openHostPane = async () => {
    await page.goto(`${base}/?session=${SESSION}`, { waitUntil: "domcontentloaded", timeout: NAV_MS });
    await page.getByText("E2E polyforge 51 host session").first().waitFor({ state: "attached", timeout: NAV_MS });
    const show = page.locator('button[aria-label="Show sidebar"]').first();
    if (await show.isVisible().catch(() => false)) await show.click();
    await page.locator('button[aria-label="Settings"]').first().click();
    const dialog = page.locator('[role="dialog"][aria-label="Settings"]').first();
    await dialog.getByRole("button", { name: "Polyforge", exact: true }).click();
    await page.locator(".settings-polyforge").first().waitFor({ state: "attached", timeout: 60_000 });
    await page.locator("[data-pf-host-entry]").first().click();
    const host = page.locator('[data-pf-settings-region="host"]').first();
    await host.waitFor({ state: "attached", timeout: 60_000 });
    return host;
  };
  const host = await openHostPane();

  await check("W1", async () => {
    // the two slow-model bounds are one-line rows; the two pattern lists are
    // one-regex-per-line editors; none of the four leaks into a Cell's card.
    const median = host.locator('input[aria-label="slow_model_median_latency (host)"]').first();
    await median.waitFor({ state: "visible", timeout: 20_000 });
    const waitForValue = async (locator, want, ms = 20_000) => {
      for (const deadline = Date.now() + ms; Date.now() < deadline;) {
        if ((await locator.inputValue()) === want) return true;
        await delay(100);
      }
      return false;
    };
    assert.ok(await waitForValue(median, "45s"), "the median bound shows its file value");
    assert.equal(await host.locator('input[aria-label="slow_model_p90_latency (host)"]').first().inputValue(), "3m0s", "the p90 bound shows its file value");
    const transient = host.locator('textarea[aria-label="transient_error_patterns (host)"]').first();
    await transient.waitFor({ state: "visible", timeout: 20_000 });
    assert.equal(await transient.inputValue(), "\\bupstream\\b hiccup\npayment retry", "the transient list renders one regex per line");
    assert.equal(await host.locator('textarea[aria-label="model_error_patterns (host)"]').first().inputValue(), "weird model failure", "the model list renders its one pattern");
    await page.locator("[data-pf-cell-entry=\"default\"]").first().click();
    const cell = page.locator('[data-pf-pane="cell:default"]').first();
    await cell.waitFor({ state: "visible", timeout: 30_000 });
    // the Cell's cli.toml card has settled before its absences are read: a
    // known Cell key row shows its file value (an unrendered card would
    // count 0 for every key and pass vacuously)
    const cellStepTimeout = cell.locator('input[aria-label="step_timeout"]').first();
    await cellStepTimeout.waitFor({ state: "visible", timeout: 30_000 });
    assert.ok(await waitForValue(cellStepTimeout, "30m0s"), "the Cell's cli.toml card loaded its step_timeout row");
    for (const key of ["slow_model_median_latency", "slow_model_p90_latency", "transient_error_patterns", "model_error_patterns"]) {
      assert.equal(await cell.locator(`[aria-label="${key}"], [aria-label="${key} (host)"]`).count(), 0, `${key} is Host-only: the cli.toml card must not edit it`);
    }
    await page.locator("[data-pf-host-entry]").first().click();
    await host.waitFor({ state: "attached", timeout: 30_000 });
  });

  await check("W2", async () => {
    // editing the transient list and saving reaches pf as one JSON array on
    // the Host scope (blank lines trimmed, --host pinned, no --profile).
    const transient = host.locator('textarea[aria-label="transient_error_patterns (host)"]').first();
    await transient.waitFor({ state: "visible", timeout: 20_000 });
    await transient.fill("\\b402\\b\npayment retry\n");
    await host.locator('[data-pf-pattern-list="transient_error_patterns"]').getByRole("button", { name: "Save", exact: true }).first().click();
    const hit = await waitForCall((call) => call.args.includes("config") && call.args.includes("set") && call.args.includes("transient_error_patterns"));
    assert.ok(hit, "the list save reached pf");
    const keyIdx = hit.args.indexOf("transient_error_patterns");
    const value = hit.args.find((arg, i) => i > keyIdx && arg.startsWith("["));
    assert.equal(value, JSON.stringify(["\\b402\\b", "payment retry"]), "the value is one JSON array, blank lines trimmed");
    assert.ok(hit.args.includes("--host"), "the write is pinned to the Host scope");
    assert.ok(!hit.args.includes("--profile"), "a Host key write carries no --profile");
    // the save cleared the draft and the region re-read: the editor shows
    // the applied file value (the stub applies the write to its fixture)
    for (const deadline = Date.now() + 20_000; Date.now() < deadline;) {
      const value = await transient.inputValue();
      if (value === "\\b402\\b\npayment retry") break;
      if (Date.now() > deadline - 100) {
        const status = await host.locator("[role=status], [role=alert]").allInnerTexts().catch(() => []);
        throw new Error(`the re-read did not settle: value ${JSON.stringify(value)}, status ${JSON.stringify(status)}`);
      }
      await delay(100);
    }
    assert.equal(await transient.inputValue(), "\\b402\\b\npayment retry", "the re-read shows the applied list");
  });

  await check("W3", async () => {
    // the unclassified panel lists the stubbed fallback texts with counts,
    // from `pf --json ops unclassified` (Host-wide, no --profile).
    const panel = host.locator('[data-pf-unclassified="host"]').first();
    await panel.waitFor({ state: "attached", timeout: 20_000 });
    await panel.getByText("gateway tantrum A").first().waitFor({ state: "attached", timeout: 20_000 });
    assert.match(await panel.innerText(), /3×/, "the row carries its count");
    assert.ok((await panel.innerText()).includes("upstream wailed mysteriously"), "the second text renders");
    const read = await waitForCall((call) => call.args.includes("ops") && call.args.includes("unclassified"));
    assert.ok(read, "the panel read reached pf");
    assert.ok(!read.args.includes("--profile"), "the unclassified read is Host-level");
  });

  await check("W4", async () => {
    // pf's refusal (an uncompilable regex) surfaces in the region and
    // nothing crashes: the editor keeps the draft.
    saveState({ refuse: "transient_error_patterns" });
    const transient = host.locator('textarea[aria-label="transient_error_patterns (host)"]').first();
    await transient.waitFor({ state: "visible", timeout: 20_000 });
    await transient.fill("not a regex (");
    await host.locator('[data-pf-pattern-list="transient_error_patterns"]').getByRole("button", { name: "Save", exact: true }).first().click();
    const error = host.locator('[role="status"], [role="alert"]').filter({ hasText: /compilable RE2/ }).first();
    await error.waitFor({ state: "attached", timeout: 20_000 });
    assert.equal(await transient.inputValue(), "not a regex (", "a refused write keeps the draft");
  });
} catch (error) {
  failures.push(`crash: ${error instanceof Error ? error.stack : String(error)}`);
  console.log(`FAIL crash: ${error instanceof Error ? error.message : String(error)}`);
} finally {
  await h.close();
  rmSync(work, { recursive: true, force: true });
}

if (failures.length > 0) {
  console.log(joinedLines(4));
  console.log(`FAIL e2e/w51-error-classes: ${failures.length} scenario(s) red`);
  process.exitCode = 1;
} else {
  console.log(`PASS e2e/w51-error-classes: all ${check.count} scenario(s) green`);
}
