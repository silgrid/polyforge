// carries: app/settings.css
// carries: lib/polyforge-cell-parity*
// — Settings › Polyforge: a named Cell's pane looks and works
// like the default Cell's.
//
// The default Cell and a named `public` Cell (file cli-public.toml) are
// driven as a browser page against the app's real routes. `pf` is a stub
// first on PATH answering the real pf wire: `--json version` capabilities,
// `--json cell list`, the scoped `config list` of each Cell's file (the
// default Cell's cli.toml lists the Host keys too), `config workspace`,
// `config set|unset` (a `[workspace."<path>"]` write lands in that Cell's
// file and, as pf does, notifies the daemon — which reloads the Cell, or
// refuses a Cell whose workspace equals or nests another Cell's with
// DAEMON-409 "Cell … was not changed"), `config reload`, `ops status` and
// `workflows`. The suite asserts what both panes render, the exact argv the
// server hands to pf (every per-Cell call carries `--profile <name>`), and
// that an api key never appears in argv, the page or the server log.
//
// Screenshots (desktop and a 420px viewport, both panes) are written to
// $PI_WEB_E2E_SCREENS_DIR when set, else to the run's temp dir.
//
//   node e2e/w385-named-cell-parity.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, eventually, joinedLines, writeHostSession } from "./harness.mjs";

assertNoDevServer();
const work = realpathSync(mkdtempSync(join(tmpdir(), "pi-web-pf-385-")));
const screens = process.env.PI_WEB_E2E_SCREENS_DIR || join(work, "screens");
mkdirSync(screens, { recursive: true });
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(work, "project");
const publicWs = join(work, "ws-public");
const freshWs = join(work, "ws-fresh");
const defaultExtra = join(work, "ws-default-extra");
const SESSION = "e2e-pf-385-named-cell";
const SESSION_TEXT = "E2E workbench 385 named Cell parity session";
for (const dir of [agentDir, project, publicWs, freshWs, defaultExtra, join(home, ".config", "polyforge")]) mkdirSync(dir, { recursive: true });
writeHostSession(agentDir, { id: SESSION, cwd: project, text: SESSION_TEXT, timestamp: "2026-11-08T00:00:00.000Z" });

// ---- stub pf: two Cells with their own files ----
const stubDir = join(work, "stub");
mkdirSync(stubDir);
const argvLog = join(stubDir, "argv.log");
const stateFile = join(stubDir, "state.json");
const HOST_KEYS = ["max_concurrent_jobs", "daemon_socket", "harness_root", "operator_agent_dir"];
writeFileSync(stateFile, JSON.stringify({
  cells: [
    { profile: "default", state: "serving", valid: true, server: "https://hub-main.example", workspaces: [project], running: 0, pending: 0, max_concurrent: 6 },
    { profile: "public", state: "serving", valid: true, server: "https://hub-public.example", workspaces: [publicWs], running: 0, pending: 0, max_concurrent: 6, cell_max_concurrent_jobs: 2 },
  ],
  files: {
    default: { sha: 1, server: "https://hub-main.example", user: "usr_main", step_timeout: "30m0s", ceiling: 4, mapping: { [project]: "acme" } },
    public: { sha: 1, server: "https://hub-public.example", user: "usr_public", step_timeout: "45m0s", ceiling: 2, modelSource: "shared-default", mapping: { [publicWs]: "pub" } },
  },
  writes: [],
  reloads: [],
}));
writeFileSync(argvLog, "");
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename));
const stateFile = path.join(dir, "state.json");
const s = JSON.parse(fs.readFileSync(stateFile, "utf8"));
const args = process.argv.slice(2);
// Only a key write carries stdin; any other call must not wait on an open pipe.
const keyWrite = args.includes("set") && args.includes("api_key");
let stdin = ""; if (keyWrite) { try { stdin = fs.readFileSync(0, "utf8"); } catch {} }
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify({ args, stdin }) + "\\n");
const out = (doc) => { process.stdout.write((typeof doc === "string" ? doc : JSON.stringify(doc)) + "\\n"); process.exit(0); };
const refuse = (code, detail) => { process.stderr.write(JSON.stringify({ error: { code, detail } }) + "\\n"); process.exit(1); };
if (args.includes("--version")) out("v2.20.21");
let i = 0; let profile = "";
for (; i < args.length; i += 1) {
  if (args[i] === "--json") continue;
  if (args[i] === "--profile") { profile = args[i + 1]; i += 1; continue; }
  break;
}
const host = args.includes("--host");
const verb = args.slice(i).filter((a) => a !== "--host");
const opt = (name) => { const at = verb.indexOf(name); return at >= 0 ? verb[at + 1] : ""; };
const cell = profile || "default";
const file = s.files[cell];
// atomic, so the suite never reads a half-written state
const save = () => { fs.writeFileSync(stateFile + "." + process.pid, JSON.stringify(s)); fs.renameSync(stateFile + "." + process.pid, stateFile); };
const nested = (a, b) => a === b || a.startsWith(b + "/") || b.startsWith(a + "/");
const hostItems = [
  { key: "daemon_socket", value: "/run/pf.sock", source: "file", class: "restart-only", schema: { kind: "path" } },
  { key: "max_concurrent_jobs", value: 6, source: "file", class: "live", schema: { kind: "int", min: 1 } },
  { key: "harness_root", value: "/opt/pf", source: "file", class: "restart-only", schema: { kind: "path" } },
  { key: "operator_agent_dir", value: "/home/op/.pi", source: "file", class: "restart-only", schema: { kind: "path" } },
];
const items = (name) => {
  const f = s.files[name];
  return [
    { key: "server", value: f.server, source: "file", class: "restart-only", schema: { kind: "string" } },
    { key: "api_key", set: true, source: "file", class: "restart-only", schema: { kind: "secret" } },
    { key: "user_id", value: f.user, source: "file", class: "cli-only", schema: { kind: "string" } },
    { key: "claude_bin", value: "/usr/local/bin/claude", source: "file", class: "live", schema: { kind: "string" } },
    { key: "step_timeout", value: f.step_timeout, source: "file", class: "live", schema: { kind: "duration", min_exclusive: 0 } },
    { key: "stall_window", value: "10m0s", source: "file", class: "live", schema: { kind: "duration", nonzero: true } },
    { key: "cell_max_concurrent_jobs", value: f.ceiling, source: "file", class: "live", schema: { kind: "int", min: 1 } },
    { key: "summarize.models", value: ["pi:sum"], source: "file", class: "live", schema: { kind: "model-list" } },
    { key: "summarize.language", value: "en", source: "file", class: "live", schema: { kind: "string" } },
    { key: "summarize.interval_sec", value: 60, source: "default", class: "live", schema: { kind: "int", min: 10, max: 3600 } },
    // pf lists [model] source for a named Cell only (the default Cell's credentials are the Host's)
    ...(name === "default" ? hostItems : [{ key: "model.source", value: f.modelSource, source: "file", class: "restart-only", schema: { kind: "model-source" } }]),
    ...Object.entries(f.mapping).map(([ws, projects]) => ({ key: '[workspace."' + ws + '"]', value: projects, source: "file", section: true, class: "next-claim" })),
  ];
};
if (verb[0] === "version") out({ version: "v2.20.21", capabilities: ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"] });
if (verb[0] === "cell" && verb[1] === "list") out({ dir: path.join(dir, "cfg"), profiles: s.cells });
if (verb[0] === "cell") out({ ok: true, profile: verb[2] });
if (verb[0] === "config" && verb[1] === "list") {
  if (host) out({ file: { sha256: "sha-host", exists: true }, items: hostItems });
  out({ file: { sha256: "sha-" + cell + "-" + file.sha, exists: true }, items: items(cell) });
}
if (verb[0] === "config" && verb[1] === "workspace") {
  const ws = Object.keys(file.mapping).find((w) => nested(w, opt("--cwd")) && opt("--cwd").startsWith(w));
  if (!ws) out({ mapped: false, workspace: null, yaml: null, steps: null, steps_complete: false });
  out({ mapped: true, workspace: ws, yaml: { path: ws + "/.polyforge.yaml", sha256: "yaml-" + cell, exists: true }, steps: {}, steps_complete: true });
}
if (verb[0] === "config" && (verb[1] === "set" || verb[1] === "unset")) {
  const key = verb[2];
  const value = verb[1] === "set" && key !== "api_key" ? verb[3] : null;
  s.writes.push({ cell: host ? "host" : cell, action: verb[1], key, value });
  if (!host) file.sha += 1;
  const m = /^\\[workspace\\."(.*)"\\]$/.exec(key);
  if (m && !host) {
    // pf lands the edit in the Cell's file, then notifies the daemon, which
    // reloads that Cell or refuses an overlapping one (#348's rule)
    if (verb[1] === "set") file.mapping[m[1]] = value.replace(/^projects=/, "");
    else delete file.mapping[m[1]];
    save();
    if (verb[1] === "set") {
      for (const other of s.cells) {
        if (other.profile === cell) continue;
        for (const ws of other.workspaces) {
          if (nested(m[1], ws)) refuse("DAEMON-409", 'Cell "' + cell + '" was not changed: conflicts with ' + (other.profile === "default" ? "the default Cell" : "Cell " + other.profile) + ": workspace " + m[1] + " overlaps " + ws);
        }
      }
    }
    s.reloads.push(cell);
    s.cells.find((c) => c.profile === cell).workspaces = Object.keys(file.mapping);
    save();
    out({ outcomes: [{ key, class: "next-claim", status: "next-claim" }] });
  }
  if (!host && key === "step_timeout") file.step_timeout = value ?? "30m0s";
  save();
  out({ ok: true, key, class: "live", outcome: "applied" });
}
if (verb[0] === "config" && verb[1] === "reload") { s.reloads.push(host ? "host" : cell); save(); out({ reloaded: [], pending: [] }); }
if (verb[0] === "ops" && verb[1] === "status") out({ queue: { pending: 0, running: 0, max_concurrent: 6, max_configured: 6 }, supervision: { step_timeout: file ? file.step_timeout : "30m0s", stall_window: "10m0s" }, reload: { pending: [], overridden: [] } });
if (verb[0] === "workflows") out({ workflows_dir: "/e2e/workflows", daemon_reachable: true, reason: "", workspace: opt("--cwd"), projects: Object.values(file.mapping).flatMap((p) => p.split(",")), write_capable: false, write_reason: "", bundles: [], bundle_errors: [] });
process.stderr.write("pf: unknown command " + verb.join(" ") + "\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
const calls = () => (existsSync(argvLog) ? readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)) : []);
const stubState = () => JSON.parse(readFileSync(stateFile, "utf8"));
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
const waitFor = (probe, ms = 20_000) => eventually(() => probe().catch(() => false), ms);
// The stub logs its argv before it saves its state: a check of the state waits
// for the stub to finish (under the parallel aggregate the gap is real).
const settled = async (probe, ms = 20_000) => {
  for (const deadline = Date.now() + ms; Date.now() < deadline;) {
    try { if (probe(stubState())) return true; } catch { /* a state read mid-rename */ }
    await delay(100);
  }
  return false;
};
const withoutState = (args) => args.slice(0, args.includes("--expect-state") ? args.indexOf("--expect-state") : args.length);
const mappingKey = (path) => `[workspace."${path}"]`;

const failures = [];
const check = createCheck({ failures, message: joinedLines(4, 400), record: ([label], text) => `${label}: ${text}` });

const KEY = "E2E-W385-PUBLIC-KEY-SENTINEL";
const h = createHarness({
  storage: { "pi-locale": "en", "pi-web:session-age-filter-days": "0" },
  shape: (o) => ({ ...o, viewport: { width: o.width, height: o.height } }),
  timeout: 30_000,
  errors: [],
  session: SESSION,
  ready: SESSION_TEXT,
  async setup(page) {
    await page.route("**/api/plugins?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ packages: [] }) }));
    // The dock's Messages poll is not this page's (it follows the CLI's Cell): answered here.
    await page.route("**/api/polyforge/messages**", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ conversations: [], contacts: [], groups: [] }) }));
  },
  async after(page) {
    const show = page.locator('button[aria-label="Show sidebar"]').first();
    if (await show.isVisible().catch(() => false)) await show.click();
    await page.locator('button[aria-label="Settings"]').first().click();
    const dialog = page.locator('[role="dialog"][aria-label="Settings"]').first();
    await dialog.waitFor({ state: "visible", timeout: NAV_MS });
    await dialog.getByRole("button", { name: "Polyforge", exact: true }).first().click();
    await dialog.locator('[data-pf-cell-entry="public"]').first().waitFor({ state: "visible", timeout: 60_000 });
    return { context: page.context(), page, dialog };
  },
});
try {
  await h.startServer({
    name: "w385-named-cell-parity",
    logFile: join(work, "server.log"),
    env: {
      PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: home,
      PI_CODING_AGENT_DIR: agentDir },
  });
  await h.launchBrowser(() => chromium.launch());

  const { openPage } = h;

  const pane = (dialog, profile) => dialog.locator(`[data-pf-pane="cell:${profile}"]`).first();
  const select = async (dialog, profile) => {
    await dialog.locator(`[data-pf-cell-entry="${profile}"]`).first().click();
    await pane(dialog, profile).waitFor({ state: "visible", timeout: 30_000 });
    await pane(dialog, profile).locator(".polyforge-config-cli").first().waitFor({ state: "visible", timeout: 60_000 });
  };
  // A pane's screenshot from its top (head and file card), and one with its 工作区 card in view.
  const shot = async (page, name, scope = null) => {
    await page.evaluate(() => { for (const node of document.querySelectorAll(".config-detail")) node.scrollTop = 0; });
    if (scope) await workspaceCard(scope).scrollIntoViewIfNeeded();
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await page.screenshot({ path: join(screens, name) });
  };
  // Open every fold of a pane (advanced keys, the mapping rows) so rows are visible.
  const openFolds = async (scope) => {
    const toggles = scope.locator('.polyforge-config-cli button.polyforge-disclosure-toggle[aria-expanded="false"], section[aria-label="Workspace"] button.polyforge-disclosure-toggle[aria-expanded="false"]');
    for (let n = await toggles.count(); n > 0; n = await toggles.count()) await toggles.first().click();
  };
  // A pane's structure: its cards in order, the file card's title and zone titles.
  const structure = (scope) => scope.evaluate((node) => ({
    cards: [...node.querySelectorAll("section.polyforge-config-card")].map((card) => card.getAttribute("aria-label")),
    fileTitle: node.querySelector(".polyforge-config-cli .polyforge-config-card-title")?.textContent ?? null,
    zones: [...node.querySelectorAll(".polyforge-config-cli .polyforge-config-zone-title")].map((zone) => zone.textContent),
    reload: [...node.querySelectorAll("button")].some((button) => button.textContent === "Apply hand edits (reload)"),
  }));
  const workspaceCard = (scope) => scope.locator('section[aria-label="Workspace"]').first();
  const mappingRow = (scope, path) => scope.locator(`[data-pf-workspace-row="${path}"]`).first();

  const desktop = await openPage({ width: 1280, height: 900 });
  const { page, dialog } = desktop;

  await check("N1 a named Cell's pane renders the default pane's cards, its file card labelled cli-public.toml, no Host key", async () => {
    await select(dialog, "default");
    const def = pane(dialog, "default");
    await select(dialog, "public");
    const pub = pane(dialog, "public");
    await openFolds(pub);
    const publicShape = await structure(pub);
    const defaultShape = await structure(def);
    // the same cards in the same order; only the file card's label is each Cell's own file
    assert.deepEqual(publicShape.cards, ["cli-public.toml", "Workspace", "Workflow"], `the named pane's cards: ${JSON.stringify(publicShape.cards)}`);
    assert.deepEqual(defaultShape.cards, ["cli.toml", "Workspace", "Workflow"], `the default pane's cards: ${JSON.stringify(defaultShape.cards)}`);
    assert.equal(publicShape.fileTitle, "cli-public.toml", "the file card is not labelled with the Cell's own file");
    assert.equal(defaultShape.fileTitle, "cli.toml", "the default file card is not labelled cli.toml");
    assert.deepEqual(publicShape.zones, ["cli-public.toml", "cli-public.toml [daemon]", "cli-public.toml [model]", "cli-public.toml [summarize]"], "the named file card's zones are not its file's literal sections");
    assert.deepEqual(defaultShape.zones, ["cli.toml", "cli.toml [daemon]", "cli.toml [summarize]"], "the default file card's zones changed");
    assert.ok(publicShape.reload && defaultShape.reload, "Reload is missing from a pane");
    // the same row controls: per-row save and unset, values from the Cell's own file
    assert.ok(await waitForValue(pub.locator('input[aria-label="step_timeout"]').first(), "45m0s"), "the named pane does not show its own step_timeout");
    assert.ok(await waitForValue(pub.locator('input[aria-label="cell_max_concurrent_jobs"]').first(), "2"), "the named pane does not show its Cell ceiling");
    const stepRow = pub.locator(".polyforge-config-row", { has: page.locator('input[aria-label="step_timeout"]') }).first();
    assert.ok(await stepRow.getByRole("button", { name: "Save", exact: true }).isVisible(), "a row lost its Save");
    assert.ok(await stepRow.getByRole("button", { name: "Unset", exact: true }).isVisible(), "a row has no Unset");
    // Host-only keys are absent from the named pane (and from the default Cell's)
    for (const scope of [pub, def]) {
      for (const key of HOST_KEYS) {
        assert.equal(await scope.locator(`input[aria-label="${key}"]`).count(), 0, `the Host key ${key} has an input in a Cell's pane`);
        assert.equal(await scope.locator(".polyforge-config-row").filter({ hasText: new RegExp(`^${key}`) }).count(), 0, `the Host key ${key} renders in a Cell's pane`);
      }
    }
    // api_key write-only: a password input, never a value, its set state shown
    const key = pub.locator('input[aria-label="api_key"]').first();
    assert.equal(await key.getAttribute("type"), "password", "the named api_key input is not write-only");
    assert.equal(await key.inputValue(), "", "a key value reached the page");
    assert.equal(await pub.locator("[data-pf-api-key-state]").first().getAttribute("data-pf-api-key-state"), "set", "the key's set state is not shown");
    // identity: server read-only with the new-profile guidance
    assert.equal(await pub.locator('input[aria-label="server"]').count(), 0, "the Cell's server is editable in place");
    const serverRow = pub.locator(".polyforge-config-row").filter({ has: page.locator('[data-pf-identity-hint="server"]') }).first();
    assert.match(await serverRow.innerText(), /hub-public\.example/, "the Cell's AIHub server is not shown");
    assert.match(await serverRow.innerText(), /never change in place/, "the new-profile guidance is missing");
    // model.source: the named Cell's own, editable; the default Cell says where its credentials come from
    assert.ok(await waitForValue(pub.locator('input[aria-label="model.source"]').first(), "shared-default"), "the named Cell's model.source is not an editable row");
    assert.equal(await def.locator('input[aria-label="model.source"]').count(), 0, "the default Cell grew a model.source row");
    assert.match(await def.locator('[data-pf-model-source="default"]').first().innerText(), /operator_agent_dir/);
    // the 工作区 card is the same editable card in both panes (the default's checked once it is shown)
    assert.ok(await workspaceCard(pub).locator("[data-pf-workspace-add]").first().isVisible(), "the named 工作区 card offers no add row");
    assert.ok(await mappingRow(pub, publicWs).isVisible(), "the named Cell's mapping row is not shown");
    assert.ok(await waitForValue(mappingRow(pub, publicWs).locator("input").first(), "pub"), "the named mapping's projects are not editable");
    await shot(page, "desktop-public.png");
    await shot(page, "desktop-public-workspace.png", pub);
    await select(dialog, "default");
    await openFolds(def);
    assert.ok(await workspaceCard(def).locator("[data-pf-workspace-add]").first().isVisible(), "the default 工作区 card offers no add row");
    assert.ok(await mappingRow(def, project).isVisible(), "the default Cell's mapping row is not the same editable row");
    assert.ok(await waitForValue(mappingRow(def, project).locator("input").first(), "acme"), "the default mapping's projects are not editable");
    await shot(page, "desktop-default.png");
    await shot(page, "desktop-default-workspace.png", def);
  });

  // review s2#1: a Cell pane never claims its settings are read-only — the
  // settings cwd lies outside a named Cell's mapping, yet its 工作区 card is editable.
  // (Runs before N3: the refused overlap lands the settings cwd in public's file, as pf does.)
  await check("N7 no Cell pane claims its workspace settings are read-only", async () => {
    for (const profile of ["public", "default"]) {
      await select(dialog, profile);
      const text = await pane(dialog, profile).innerText();
      assert.doesNotMatch(text, /settings are read-only|not mapped to a workspace/i, `the ${profile} pane shows a read-only claim`);
    }
  });

  await check("N2 a named Cell's mapping is added, edited and removed from the page; it lands in cli-public.toml and the Cell reloads", async () => {
    await select(dialog, "public");
    const pub = pane(dialog, "public");
    await openFolds(pub);
    const add = workspaceCard(pub).locator("[data-pf-workspace-add]").first();
    await add.locator('input[aria-label="Workspace path"]').fill(freshWs);
    await add.locator('input[aria-label="Projects"]').fill("pub2, docs");
    const before = calls().length;
    await add.getByRole("button", { name: "Add workspace", exact: true }).click();
    const set = await waitForCall((call) => call.args.includes("set") && call.args.includes(mappingKey(freshWs)));
    assert.ok(set, "the add never reached pf config set");
    assert.deepEqual(withoutState(set.args), ["--profile", "public", "--json", "config", "set", mappingKey(freshWs), "projects=pub2,docs"]);
    assert.ok(set.args.includes("--expect-state"), "the mapping write lost the CAS expectation");
    assert.ok(await settled((st) => st.files.public.mapping[freshWs] === "pub2,docs" && st.reloads.includes("public")), "the mapping did not land in the public Cell's file");
    assert.equal(stubState().files.public.mapping[freshWs], "pub2,docs", "the mapping did not land in the public Cell's file");
    assert.equal(stubState().files.default.mapping[freshWs], undefined, "the mapping landed in the default Cell's file");
    assert.ok(stubState().reloads.includes("public"), "the Cell did not reload");
    assert.ok(await waitFor(() => mappingRow(pub, freshWs).isVisible()), "the added mapping is not shown after the re-read");
    assert.ok(await waitFor(async () => calls().slice(before).some((call) => call.args.includes("cell") && call.args.includes("list"))), "the page did not re-read the Cell registry after the write");
    // edit its projects
    const row = mappingRow(pub, freshWs);
    await row.locator("input").first().fill("pub2");
    await row.getByRole("button", { name: "Save", exact: true }).click();
    const edit = await waitForCall((call) => call.args.includes("set") && call.args.includes(mappingKey(freshWs)) && call.args.includes("projects=pub2"));
    assert.ok(edit, "the edit never reached pf config set");
    assert.deepEqual(withoutState(edit.args), ["--profile", "public", "--json", "config", "set", mappingKey(freshWs), "projects=pub2"]);
    assert.ok(await settled((st) => st.files.public.mapping[freshWs] === "pub2"), "the edit did not land in the public Cell's file");
    assert.equal(stubState().files.public.mapping[freshWs], "pub2");
    // remove it
    await mappingRow(pub, freshWs).getByRole("button", { name: "Remove", exact: true }).click();
    const unset = await waitForCall((call) => call.args.includes("unset") && call.args.includes(mappingKey(freshWs)));
    assert.ok(unset, "the remove never reached pf config unset");
    assert.deepEqual(withoutState(unset.args), ["--profile", "public", "--json", "config", "unset", mappingKey(freshWs)]);
    assert.ok(await settled((st) => st.files.public.mapping[freshWs] === undefined), "the mapping is still in the public Cell's file");
    assert.equal(stubState().files.public.mapping[freshWs], undefined, "the mapping is still in the public Cell's file");
    assert.ok(await waitFor(async () => (await mappingRow(pub, freshWs).count()) === 0), "the removed mapping is still shown");
  });

  await check("N3 an overlapping or nested path is refused with the Host's message on the row", async () => {
    const pub = pane(dialog, "public");
    const add = workspaceCard(pub).locator("[data-pf-workspace-add]").first();
    for (const path of [project, join(project, "nested")]) {
      await add.locator('input[aria-label="Workspace path"]').fill(path);
      await add.locator('input[aria-label="Projects"]').fill("pub");
      await add.getByRole("button", { name: "Add workspace", exact: true }).click();
      const message = `Cell "public" was not changed: conflicts with the default Cell: workspace ${path} overlaps ${project}`;
      const alert = workspaceCard(pub).locator('[role="alert"]', { hasText: message }).first();
      assert.ok(await waitFor(() => alert.isVisible()), `the Host's refusal is not shown for ${path}`);
      assert.ok(calls().some((call) => call.args.includes(mappingKey(path)) && call.args[0] === "--profile" && call.args[1] === "public"), "the refused write did not go to the public Cell");
    }
    assert.deepEqual(stubState().cells.find((cell) => cell.profile === "public").workspaces, [publicWs], "the Host applied an overlapping mapping");
  });

  await check("N4 a named Cell's api_key rides stdin only: never in argv, the page or the log", async () => {
    const pub = pane(dialog, "public");
    const key = pub.locator('input[aria-label="api_key"]').first();
    await key.fill(KEY);
    await pub.locator(".polyforge-config-row", { has: page.locator('input[aria-label="api_key"]') }).getByRole("button", { name: "Save", exact: true }).click();
    await pub.getByRole("button", { name: "Confirm save", exact: true }).first().click();
    const set = await waitForCall((call) => call.args.includes("set") && call.args.includes("api_key"));
    assert.ok(set, "the api_key save never reached pf config set");
    assert.deepEqual(withoutState(set.args), ["--profile", "public", "--json", "config", "set", "api_key"]);
    assert.equal(set.stdin, `${KEY}\n`, "the key did not reach pf on stdin");
    assert.ok(!calls().some((call) => call.args.some((token) => token.includes(KEY))), "the api key appeared in argv");
    assert.ok(await waitForValue(key, ""), "the api_key input kept the key");
    assert.ok(!(await page.locator("body").innerText()).includes(KEY), "the page echoed the api key");
    assert.ok(!readFileSync(join(work, "server.log"), "utf8").includes(KEY), "the server log carries the api key");
  });

  await check("N5 Reload and the workflow card are the named Cell's: every per-Cell pf call carries --profile", async () => {
    const pub = pane(dialog, "public");
    await pub.getByRole("button", { name: "Apply hand edits (reload)", exact: true }).click();
    const reload = await waitForCall((call) => call.args.includes("reload") && call.args.includes("public"));
    assert.ok(reload, "Reload never reached pf config reload for the public Cell");
    assert.deepEqual(reload.args, ["--profile", "public", "--json", "config", "reload"]);
    const workflows = await waitForCall((call) => call.args.includes("workflows") && call.args.includes("public"));
    assert.ok(workflows, "the named workflow card never read its Cell's workflows");
    assert.deepEqual(workflows.args, ["--profile", "public", "--json", "workflows", "--cwd", publicWs]);
    assert.match(await pub.locator('section[aria-label="Workflow"]').first().innerText(), /ws-public/, "the workflow card does not show the Cell's own workspace");
    const perCell = calls().filter((call) => call.args.includes("config") || call.args.includes("workflows"));
    assert.ok(perCell.length > 0);
    for (const call of perCell) {
      if (call.args.includes("--host")) continue;
      assert.equal(call.args[0], "--profile", `a per-Cell pf call without --profile: ${call.args.join(" ")}`);
    }
    for (const write of stubState().writes) assert.notEqual(write.cell, "default", `a named-pane write landed in the default Cell: ${JSON.stringify(write)}`);
  });

  await check("D1 the default pane's 工作区 card is the same editable card, written with --profile default", async () => {
    await select(dialog, "default");
    const def = pane(dialog, "default");
    await openFolds(def);
    const add = workspaceCard(def).locator("[data-pf-workspace-add]").first();
    await add.locator('input[aria-label="Workspace path"]').fill(defaultExtra);
    await add.locator('input[aria-label="Projects"]').fill("extra");
    await add.getByRole("button", { name: "Add workspace", exact: true }).click();
    const set = await waitForCall((call) => call.args.includes("set") && call.args.includes(mappingKey(defaultExtra)));
    assert.ok(set, "the default add never reached pf config set");
    assert.deepEqual(withoutState(set.args), ["--profile", "default", "--json", "config", "set", mappingKey(defaultExtra), "projects=extra"]);
    assert.ok(await waitFor(() => mappingRow(def, defaultExtra).isVisible()), "the default Cell's new mapping is not shown");
    assert.ok(await settled((st) => st.files.default.mapping[defaultExtra] === "extra"), "the default write did not land in the default Cell's file");
    assert.equal(stubState().files.public.mapping[defaultExtra], undefined, "the default write landed in the public Cell");
  });
  // review s2#4: a mapping write re-reads the workflow card too (a Cell's
  // first mapping must not leave the card saying it maps none).
  await check("N8 a mapping write re-reads the Cell's workflow card", async () => {
    await select(dialog, "public");
    const pub = pane(dialog, "public");
    await openFolds(pub);
    const add = workspaceCard(pub).locator("[data-pf-workspace-add]").first();
    await add.locator('input[aria-label="Workspace path"]').fill(freshWs);
    await add.locator('input[aria-label="Projects"]').fill("pub3");
    const before = calls().length;
    await add.getByRole("button", { name: "Add workspace", exact: true }).click();
    assert.ok(await waitForCall((call) => call.args.includes("set") && call.args.includes(mappingKey(freshWs)) && call.args.includes("projects=pub3")), "the add never reached pf");
    assert.ok(await waitFor(async () => calls().slice(before).some((call) => call.args[1] === "public" && call.args.includes("workflows") && call.args.includes(freshWs))), "the workflow card did not re-read the Cell's new workspace");
    assert.ok(await waitFor(async () => /ws-fresh/.test(await pub.locator('section[aria-label="Workflow"]').first().innerText())), "the workflow card does not show the new workspace");
  });
  await desktop.context.close();

  await check("N6 a 420px viewport shows both panes without horizontal overflow", async () => {
    const narrow = await openPage({ width: 420, height: 900 });
    try {
      for (const profile of ["default", "public"]) {
        await select(narrow.dialog, profile);
        const detail = narrow.dialog.locator(".config-detail").first();
        const overflow = await detail.evaluate((node) => ({ scrollWidth: node.scrollWidth, clientWidth: node.clientWidth }));
        assert.ok(overflow.scrollWidth <= overflow.clientWidth + 1, `the narrow ${profile} pane overflows (${overflow.scrollWidth} > ${overflow.clientWidth})`);
        assert.ok(await workspaceCard(pane(narrow.dialog, profile)).locator("[data-pf-workspace-add]").first().isVisible(), `the narrow ${profile} pane has no add row`);
        await shot(narrow.page, `narrow-420-${profile}.png`);
      }
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
console.log(`${failures.length ? "FAIL" : "PASS"} w385-named-cell-parity: ${check.count - failures.length}/${check.count} scenario(s) passed`);
for (const failure of failures) console.log(`  ${failure}`);
process.exit(failures.length ? 1 : 0);
