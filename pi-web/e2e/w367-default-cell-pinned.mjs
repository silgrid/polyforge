// carries: lib/polyforge-cells.ts, lib/polyforge-cells-pure.ts
// carries: app/api/polyforge/config/*, app/api/polyforge/workflows/*, app/api/polyforge/cells/*
// — Settings › Polyforge's default Cell pane reads and writes
// the default Cell, whatever Cell the server process itself sits in.
//
// pf resolves an unscoped Cell command from its own process: `--profile`,
// then PF_PROFILE, then the cwd's mapped Cell, then `default`. Here the Pi
// Web server runs with PF_PROFILE=alpha and its cwd (this package's root)
// inside the named Cell alpha's mapped workspace, so every unscoped pf call
// would land on alpha. The `pf` stub first on PATH answers the real pf wire
// and resolves exactly that way (`--host` is the Host scope; `pf ops status`
// is the Host aggregate unless `--profile` targets a Cell). The suite drives
// the default Cell's pane — its read, a key write, the write-only api_key,
// Reload, a rollback from the history and the workflow card's reads — and
// asserts the exact argv the server hands to pf, that every write landed in
// the default Cell's file and none in alpha's, and that the api key rides
// stdin only.
//
//   node e2e/w367-default-cell-pinned.mjs
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { NAV_MS } from "./timeouts.mjs";
import { createCheck, createHarness, joinedLines, writeHostSession } from "./harness.mjs";

const root = realpathSync(dirname(dirname(fileURLToPath(import.meta.url))));
assert.ok(!existsSync(join(root, process.env.PI_WEB_DIST_DIR || ".next", "dev/lock")), "Run in a checkout without an active dev server");
const work = realpathSync(mkdtempSync(join(tmpdir(), "pi-web-pf-367-")));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-367-default-cell";
const SESSION_TEXT = "E2E workbench 367 default Cell pinned session";
for (const dir of [agentDir, project, join(home, ".config", "polyforge")]) mkdirSync(dir, { recursive: true });
writeHostSession(agentDir, { id: SESSION, cwd: project, text: SESSION_TEXT, timestamp: "2026-11-07T00:00:00.000Z" });

// ---- stub pf: two Cells; alpha maps the server's own cwd ----
const stubDir = join(work, "stub");
mkdirSync(stubDir);
const argvLog = join(stubDir, "argv.log");
const stateFile = join(stubDir, "state.json");
const state = {
  cells: [
    { profile: "default", state: "serving", valid: true, server: "https://hub-default.example", workspaces: [project], running: 0, pending: 0, max_concurrent: 6 },
    { profile: "alpha", state: "serving", valid: true, server: "https://hub-alpha.example", workspaces: [root], running: 0, pending: 0, max_concurrent: 6, cell_max_concurrent_jobs: 2 },
  ],
  files: {
    default: { sha: 1, server: "https://hub-default.example", user: "usr_default", claude: "/usr/bin/claude-default", step_timeout: "30m0s", stall_window: "10m0s", ceiling: 4, mapping: { [project]: "acme" } },
    alpha: { sha: 1, server: "https://hub-alpha.example", user: "usr_alpha", claude: "/usr/bin/claude-alpha", step_timeout: "45m0s", stall_window: "20m0s", ceiling: 2, mapping: { [root]: "beta" } },
  },
  host: [
    { key: "daemon_socket", value: "/run/pf.sock", source: "file", class: "restart-only", schema: { kind: "path" } },
    { key: "max_concurrent_jobs", value: 6, source: "file", class: "live", schema: { kind: "int", min: 1 } },
    { key: "harness_root", value: "/opt/pf", source: "file", class: "restart-only", schema: { kind: "path" } },
    { key: "operator_agent_dir", value: "/home/op/.pi", source: "file", class: "restart-only", schema: { kind: "path" } },
  ],
  writes: [],
  reloads: [],
};
writeFileSync(stateFile, JSON.stringify(state));
writeFileSync(argvLog, "");
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
// A whole-file replace: a sibling spawn reads this file at its start (see the test side).
const saveFile = (p, data) => { fs.writeFileSync(p + "." + process.pid + ".tmp", data); fs.renameSync(p + "." + process.pid + ".tmp", p); };
const dir = path.dirname(fs.realpathSync(__filename));
const stateFile = path.join(dir, "state.json");
const s = JSON.parse(fs.readFileSync(stateFile, "utf8"));
const args = process.argv.slice(2);
// Only a key write carries stdin; any other call must not wait on an open pipe.
const keyWrite = args.includes("set") && args.includes("api_key");
let stdin = ""; if (keyWrite) { try { stdin = fs.readFileSync(0, "utf8"); } catch {} }
const cwd = process.cwd();
const record = { args, stdin, cwd, env: process.env.PF_PROFILE ?? "" };
const done = () => fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify(record) + "\\n");
const out = (doc) => { done(); process.stdout.write(typeof doc === "string" ? doc + "\\n" : JSON.stringify(doc) + "\\n"); process.exit(0); };
if (args.includes("--version")) out("v2.20.21");
// pf's global flags come before the verb
let i = 0; let flagProfile = "";
for (; i < args.length; i += 1) {
  if (args[i] === "--json") continue;
  if (args[i] === "--profile") { flagProfile = args[i + 1]; i += 1; continue; }
  break;
}
const host = args.includes("--host");
const verb = args.slice(i).filter((a) => a !== "--host");
const opt = (name) => { const at = verb.indexOf(name); return at >= 0 ? verb[at + 1] : ""; };
const within = (base, p) => p === base || p.startsWith(base + "/");
// pf's resolver: --profile, PF_PROFILE, the cwd's mapped Cell, default
const resolved = flagProfile || process.env.PF_PROFILE || Object.keys(s.files).find((p) => Object.keys(s.files[p].mapping).some((ws) => within(ws, cwd))) || "default";
// the Cell a config verb resolved (a Host command has none)
if (verb[0] === "config") record.cell = host ? "host" : resolved;
const file = s.files[resolved];
const save = () => saveFile(stateFile, JSON.stringify(s));
const items = (name) => {
  const f = s.files[name];
  const keys = [
    { key: "server", value: f.server, source: "file", class: "connection-change", schema: { kind: "string" } },
    { key: "api_key", set: true, source: "file", class: "connection-change", schema: { kind: "secret" } },
    { key: "user_id", value: f.user, source: "file", class: "cli-only", schema: { kind: "string" } },
    { key: "claude_bin", value: f.claude, source: "file", class: "live", schema: { kind: "string" } },
    { key: "step_timeout", value: f.step_timeout, source: "file", class: "live", schema: { kind: "duration", min_exclusive: 0 } },
    { key: "stall_window", value: f.stall_window, source: "file", class: "live", schema: { kind: "duration", nonzero: true } },
    { key: "cell_max_concurrent_jobs", value: f.ceiling, source: "file", class: "live", schema: { kind: "int", min: 1 } },
  ];
  // the default Cell's file is cli.toml: pf lists the Host keys there too
  return [...keys, ...(name === "default" ? s.host : []),
    ...Object.entries(f.mapping).map(([ws, projects]) => ({ key: '[workspace."' + ws + '"]', value: projects, source: "file", section: true, class: "next-claim" }))];
};
if (verb[0] === "version") out({ version: "v2.20.21", capabilities: ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1", "cell-migration-v1"] });
if (verb[0] === "cell" && verb[1] === "list") out({ dir: path.join(dir, "cfg"), profiles: s.cells });
if (verb[0] === "config" && verb[1] === "list") {
  if (host) out({ file: { sha256: "sha-host", exists: true }, items: s.host });
  out({ file: { sha256: "sha-" + resolved + "-" + file.sha, exists: true }, items: items(resolved) });
}
if (verb[0] === "config" && verb[1] === "workspace") {
  const ws = Object.keys(file.mapping).find((w) => within(w, opt("--cwd")));
  if (!ws) out({ mapped: false, workspace: null, yaml: null, steps: null, steps_complete: false });
  out({ mapped: true, workspace: ws, yaml: { path: ws + "/.polyforge.yaml", sha256: "yaml-" + resolved, exists: true }, steps: {}, steps_complete: true });
}
if (verb[0] === "config" && (verb[1] === "set" || verb[1] === "unset")) {
  const key = verb[2];
  const value = verb[1] === "set" && key !== "api_key" ? verb[3] : null;
  s.writes.push({ cell: record.cell, action: verb[1], key, value });
  if (!host && key === "step_timeout") { file.step_timeout = value ?? "30m0s"; file.sha += 1; }
  if (!host && key === "api_key") file.sha += 1;
  save();
  out({ ok: true, key, class: "live", outcome: "applied" });
}
if (verb[0] === "config" && verb[1] === "reload") { s.reloads.push(record.cell); save(); out({ reloaded: [], pending: [] }); }
// pf ops status: the Host aggregate unless --profile targets a Cell
if (verb[0] === "ops" && verb[1] === "status") {
  record.cell = flagProfile || "host";
  const source = s.files[flagProfile || "default"];
  out({ queue: { pending: 0, running: 0, max_concurrent: 6, max_configured: 6 }, supervision: { step_timeout: source.step_timeout, stall_window: source.stall_window }, reload: { pending: [], overridden: [] }, jobs: [] });
}
// pf workflows reads cli.toml's mapping (the default Cell's)
if (verb[0] === "workflows") {
  const ws = Object.keys(s.files.default.mapping).find((w) => within(w, opt("--cwd"))) ?? null;
  out({ workflows_dir: "/e2e/workflows", daemon_reachable: true, reason: "", workspace: ws, projects: ws ? [s.files.default.mapping[ws]] : [], write_capable: true, write_reason: "", bundles: [], bundle_errors: [] });
}
done(); process.stderr.write("pf: unknown command " + verb.join(" ") + "\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);

const calls = () => (existsSync(argvLog) ? readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)) : []);
const stubState = () => JSON.parse(readFileSync(stateFile, "utf8"));
const waitForCall = async (predicate, ms = 30_000) => {
  for (const deadline = Date.now() + ms; Date.now() < deadline;) {
    const hit = calls().find(predicate);
    if (hit) return hit;
    await delay(100);
  }
  return null;
};
const waitForValue = async (locator, want, ms = 30_000) => {
  for (const deadline = Date.now() + ms; Date.now() < deadline;) {
    if ((await locator.inputValue().catch(() => null)) === want) return true;
    await delay(100);
  }
  return false;
};
const isConfig = (call) => call.args.includes("config");
const withoutState = (args) => args.slice(0, args.includes("--expect-state") ? args.indexOf("--expect-state") : args.length);
const expectState = (args) => JSON.parse(args[args.indexOf("--expect-state") + 1]);
// Every config verb the server spawns names its scope: --profile NAME or --host.
const unscoped = () => calls().filter((call) => (isConfig(call) || call.args.includes("workflows"))
  && !(call.args[0] === "--profile" && call.args[1]) && !call.args.includes("--host"));

const failures = [];
const check = createCheck({ failures, message: joinedLines(4), record: ([label], text) => `${label}: ${text}` });

const KEY = "E2E-W367-KEY-SENTINEL-SENTINEL";
const serverEnv = {
  ...process.env, PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: home,
  PI_CODING_AGENT_DIR: agentDir, PI_WEB_PASSWORD: "", NEXT_TELEMETRY_DISABLED: "1",
  // the server itself sits in the named Cell alpha: by PF_PROFILE and by its cwd
  PF_PROFILE: "alpha",
};
delete serverEnv.PF_BIN;
const h = createHarness();
try {
  await check("D0 the stub resolves an unscoped call to alpha in the server's environment", async () => {
    const run = (args, env) => JSON.parse(spawnSync(join(stubDir, "pf"), args, { cwd: root, env, encoding: "utf8" }).stdout);
    assert.equal(run(["--json", "config", "list"], serverEnv).file.sha256, "sha-alpha-1", "PF_PROFILE resolves alpha");
    assert.equal(run(["--json", "config", "list"], { ...serverEnv, PF_PROFILE: "" }).file.sha256, "sha-alpha-1", "the cwd alone resolves alpha");
    assert.equal(run(["--profile", "default", "--json", "config", "list"], serverEnv).file.sha256, "sha-default-1", "the flag wins");
    writeFileSync(argvLog, "");
  });

  await h.startServer({ name: "w367-default-cell-pinned", logFile: join(work, "server.log"), from: serverEnv });
  await h.launchBrowser(() => chromium.launch());
  const context = await h.browser.newContext({ viewport: { width: 1280, height: 1000 }, locale: "en-US" });
  await context.addInitScript(() => {
    try {
      window.localStorage.setItem("pi-locale", "en");
      window.localStorage.setItem("pi-web:session-age-filter-days", "0");
    } catch { /* storage */ }
  });
  const page = await context.newPage();
  page.setDefaultTimeout(30_000);
  await page.route("**/api/plugins?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ packages: [] }) }));
  // The dock's Messages poll is not this page's: by its own design (#348) it
  // follows the CLI-resolved Cell and names it. Answered here so the argv
  // log holds the settings page's pf calls only.
  await page.route("**/api/polyforge/messages**", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ conversations: [], contacts: [], groups: [] }) }));
  await page.goto(`${h.base}/?session=${SESSION}`, { waitUntil: "domcontentloaded", timeout: NAV_MS });
  await page.getByText(SESSION_TEXT).first().waitFor({ state: "attached", timeout: NAV_MS });
  const show = page.locator('button[aria-label="Show sidebar"]').first();
  if (await show.isVisible().catch(() => false)) await show.click();
  await page.locator('button[aria-label="Settings"]').first().click();
  const dialog = page.locator('[role="dialog"][aria-label="Settings"]').first();
  await dialog.waitFor({ state: "visible", timeout: NAV_MS });
  await dialog.getByRole("button", { name: "Polyforge", exact: true }).first().click();
  await dialog.locator('[data-pf-cell-entry="alpha"]').first().waitFor({ state: "visible", timeout: 60_000 });
  await dialog.locator('[data-pf-cell-entry="default"]').first().click();
  const def = dialog.locator('[data-pf-pane="cell:default"]').first();
  await def.waitFor({ state: "visible", timeout: 30_000 });
  await def.locator(".polyforge-config-cli").first().waitFor({ state: "visible", timeout: 60_000 });
  const stepTimeout = def.locator('input[aria-label="step_timeout"]').first();

  await check("D1 the default pane reads the default Cell's file: its values, its mapping, every config verb pinned", async () => {
    assert.ok(await waitForValue(stepTimeout, "30m0s"), `the default pane shows ${await stepTimeout.inputValue()} (alpha's is 45m0s)`);
    assert.ok(await waitForValue(def.locator('input[aria-label="claude_bin"]').first(), "/usr/bin/claude-default"), "claude_bin is not the default Cell's");
    const list = await waitForCall((call) => call.args.join(" ") === "--profile default --json config list");
    assert.ok(list, `the default pane's read is not \`--profile default --json config list\`: ${JSON.stringify(calls().filter(isConfig).map((call) => call.args))}`);
    assert.equal(list.cell, "default");
    assert.equal(list.env, "alpha", "the server did not run with PF_PROFILE=alpha");
    assert.equal(list.cwd, root, "the server's cwd is not alpha's mapped workspace");
    const ws = await waitForCall((call) => call.args.includes("workspace") && call.args.includes("--cwd"));
    assert.deepEqual(ws.args, ["--profile", "default", "--json", "config", "workspace", "--cwd", project]);
    const mapping = def.getByText("acme", { exact: true });
    assert.ok(await mapping.count() > 0, "the default Cell's workspace mapping (acme) is not shown");
    assert.equal(await def.getByText("beta", { exact: true }).count(), 0, "alpha's mapping renders in the default Cell's pane");
    for (const call of calls().filter((entry) => entry.args.includes("ops"))) {
      assert.deepEqual(call.args, ["--json", "ops", "status"], "ops status is a Host read: no profile");
    }
    assert.deepEqual(unscoped().map((call) => call.args), [], "an unscoped config verb reached pf");
  });

  await check("D2 the workflow card reads the default Cell's workspaces", async () => {
    const workflows = await waitForCall((call) => call.args.includes("workflows"));
    assert.ok(workflows, "the workflow card never read pf workflows");
    assert.deepEqual(workflows.args, ["--profile", "default", "--json", "workflows", "--cwd", project]);
    for (const call of calls().filter((entry) => entry.args.includes("workflows"))) {
      assert.deepEqual(call.args.slice(0, 2), ["--profile", "default"], `an unpinned workflow read: ${call.args.join(" ")}`);
      assert.ok(!call.args.includes(root), "the workflow card read alpha's workspace");
    }
    assert.deepEqual(unscoped().map((call) => call.args), []);
  });

  await check("D3 a key saved in the default pane lands in the default Cell's file, with its CAS hash", async () => {
    await stepTimeout.fill("31m");
    await def.locator(".polyforge-config-row", { has: page.locator('input[aria-label="step_timeout"]') }).getByRole("button", { name: "Save", exact: true }).click();
    const set = await waitForCall((call) => call.args.includes("set") && call.args.includes("step_timeout"));
    assert.ok(set, "the save never reached pf config set");
    assert.deepEqual(withoutState(set.args), ["--profile", "default", "--json", "config", "set", "step_timeout", "31m"]);
    assert.equal(expectState(set.args).file_sha256, "sha-default-1", "the CAS hash is not the default Cell's file");
    assert.deepEqual(stubState().writes, [{ cell: "default", action: "set", key: "step_timeout", value: "31m" }]);
    assert.equal(stubState().files.alpha.step_timeout, "45m0s", "alpha's file changed");
    assert.ok(await waitForValue(stepTimeout, "31m"), "the re-read does not show the saved value");
  });

  await check("D4 the default pane's api_key goes to the default Cell on stdin only", async () => {
    const keyInput = def.locator('input[aria-label="api_key"]').first();
    assert.equal(await keyInput.getAttribute("type"), "password", "the api_key input is not write-only");
    await keyInput.fill(KEY);
    await def.locator(".polyforge-config-row", { has: page.locator('input[aria-label="api_key"]') }).getByRole("button", { name: "Save", exact: true }).click();
    await def.getByRole("button", { name: "Confirm save", exact: true }).first().click();
    const set = await waitForCall((call) => call.args.includes("set") && call.args.includes("api_key"));
    assert.ok(set, "the api_key save never reached pf config set");
    assert.deepEqual(withoutState(set.args), ["--profile", "default", "--json", "config", "set", "api_key"]);
    assert.equal(set.stdin, `${KEY}\n`, "the key did not reach pf on stdin");
    assert.ok(!calls().some((call) => call.args.some((token) => token.includes(KEY))), "the api key appeared in argv");
    assert.deepEqual(stubState().writes.at(-1), { cell: "default", action: "set", key: "api_key", value: null });
    assert.ok(!(await page.locator("body").innerText()).includes(KEY), "the page echoed the api key");
    assert.ok(!readFileSync(join(work, "server.log"), "utf8").includes(KEY), "the server log carries the api key");
    assert.ok(!readFileSync(join(agentDir, "polyforge-config-history.json"), "utf8").includes(KEY), "the panel history carries the api key");
  });

  await check("D5 Reload reloads the default Cell, then reads the Host aggregate", async () => {
    await def.getByRole("button", { name: "Apply hand edits (reload)", exact: true }).click();
    const reload = await waitForCall((call) => call.args.includes("reload"));
    assert.ok(reload, "Reload never reached pf config reload");
    assert.deepEqual(reload.args, ["--profile", "default", "--json", "config", "reload"]);
    assert.deepEqual(stubState().reloads, ["default"]);
    const followUp = () => {
      const log = calls();
      return log.slice(log.findIndex((call) => call.args.includes("reload")) + 1).find((call) => call.args.includes("ops")) ?? null;
    };
    let status = followUp();
    for (const deadline = Date.now() + 20_000; !status && Date.now() < deadline; status = followUp()) await delay(100);
    assert.ok(status, "no daemon read followed the reload");
    assert.deepEqual(status.args, ["--json", "ops", "status"], "the follow-up status is the Host aggregate");
    assert.equal(status.cell, "host");
  });

  await check("D6 a rollback from the history writes the default Cell's file back", async () => {
    const history = await (await page.request.get(`${h.base}/api/polyforge/config/history`)).json();
    const entry = history.entries.find((item) => item.key === "step_timeout");
    assert.ok(entry, "the step_timeout write is not in the history");
    assert.equal(entry.profile, "default", "the history entry does not name the default Cell");
    assert.equal(entry.old, "30m0s", "the history's old value is not the default Cell's");
    await dialog.locator("[data-pf-host-entry]").first().click();
    const host = dialog.locator('[data-pf-pane="host"]').first();
    await host.waitFor({ state: "visible", timeout: 30_000 });
    const section = host.locator('section[aria-label="Recent panel writes"]').first();
    await section.waitFor({ state: "visible", timeout: 30_000 });
    const row = section.locator(".polyforge-config-history-row", { hasText: "step_timeout" }).first();
    if (!(await row.isVisible().catch(() => false))) await section.locator(".polyforge-config-card-title button").first().click();
    await row.getByRole("button", { name: "Roll back", exact: true }).click();
    const set = await waitForCall((call) => call.args.includes("set") && call.args.includes("step_timeout") && call.args.includes("30m0s"));
    assert.ok(set, "the rollback never reached pf config set");
    assert.deepEqual(withoutState(set.args), ["--profile", "default", "--json", "config", "set", "step_timeout", "30m0s"]);
    assert.equal(expectState(set.args).file_sha256, "sha-default-3", "the rollback's CAS read is not the default Cell's current file");
    assert.equal(stubState().files.default.step_timeout, "30m0s");
  });

  await check("D7 nothing reached alpha's file and no config verb was left to the resolver", async () => {
    const s = stubState();
    assert.deepEqual(s.writes.map((write) => write.cell), ["default", "default", "default"], `writes: ${JSON.stringify(s.writes)}`);
    assert.equal(s.files.alpha.step_timeout, "45m0s");
    assert.equal(s.files.alpha.sha, 1, "alpha's file was written");
    assert.ok(!calls().some((call) => isConfig(call) && call.cell === "alpha"), `a config verb resolved to alpha: ${JSON.stringify(calls().filter((call) => call.cell === "alpha").map((call) => call.args))}`);
    assert.deepEqual(unscoped().map((call) => call.args), [], "an unscoped config verb reached pf");
    for (const call of calls().filter((entry) => entry.args.includes("ops"))) {
      assert.equal(call.cell, "host", `a Host read was moved onto a Cell: ${call.args.join(" ")}`);
    }
    for (const call of calls().filter((entry) => entry.args.includes("cell"))) {
      assert.ok(!call.args.includes("--profile"), `pf cell is a Host command: ${call.args.join(" ")}`);
    }
  });
  await context.close();
} catch (error) {
  failures.push(`suite: ${String(error?.message ?? error).split("\n").slice(0, 4).join(" | ")}`);
  console.log(`FAIL suite: ${String(error?.message ?? error).split("\n").slice(0, 4).join(" | ")}`);
} finally {
  await h.close();
}
console.log(`${failures.length ? "FAIL" : "PASS"} w367-default-cell-pinned: ${check.count - failures.length}/${check.count} scenario(s) passed`);
for (const failure of failures) console.log(`  ${failure}`);
process.exit(failures.length ? 1 : 0);
