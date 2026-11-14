// carries: lib/polyforge-workflows*, app/api/polyforge/workflows/route.ts
// — Settings › Polyforge configuration panel, iteration 3, end-to-end.
//
// Authored pre-code against artifacts/spec.md (the capsule's
// artifacts/e2e-scenarios.md mirrors these scenario blocks 1:1, S1..S13).
// The page is driven as a browser page against the app's real routes; `pf` is
// a stub first on PATH answering the real pf wire (`--json config list`, the
// mapping `[workspace."<path>"]` list items, `--json config workspace --cwd`,
// `--json workflows --cwd`, `--json ops status`), so the suite can assert the
// argv and stdin the server hands to pf as well as what the panel renders.
// The three mapped workspaces are this machine's real mapping paths, so the
// union scenario pins the six projects the spec names (criterion: the union of
// the three mapped workspaces); the real-pf half of that criterion cannot run
// inside a workflow step (pf refuses `config`/`workflows` there) and is
// recorded in artifacts/e2e-scenarios.md instead.
//
// summarize is cli.toml's [summarize] table — the stub's
// `config list` carries the three summarize.* items, its `config workspace`
// has no summarize, the workspace yamls carry steps only, and S12 pins a
// flat cli.toml summarize write (no --cwd) from the cli.toml card.
//
//   node e2e/polyforge-config-255.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, joinedLines, writeFileAtomic, writeHostSession } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-255-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const SESSION = "e2e-pf-255";
const CWD = "/workspace";
for (const dir of [agentDir, join(home, ".config", "polyforge")]) mkdirSync(dir, { recursive: true });
writeHostSession(agentDir, { id: SESSION, cwd: CWD, text: "E2E polyforge 255 host session", timestamp: "2026-11-02T00:00:00.000Z" });

// ---- the three mapped workspaces (this machine's real mapping) ----
const WS = [
  { path: "/workspace/ws-osp", projects: ["polyforge", "pi", "tsshd"] },
  { path: CWD, projects: ["orbit", "workbench"] },
  { path: "/workspace/ws-yf", projects: ["demo"] },
];
const sha = (text) => createHash("sha256").update(text).digest("hex");
const yamlOf = (ws) => `steps:\n${ws.projects.map((p) => `  ${p}:\n    fix:\n      models: ["pi:e2e"]\n`).join("")}`;
const YAML_SHA = Object.fromEntries(WS.map((ws) => [ws.path, sha(yamlOf(ws))]));
const cliToml = join(home, ".config", "polyforge", "cli.toml");
const cliText = `server = "http://127.0.0.1:1"\napi_key = "e2e-current-key"\ndaemon_socket = "/run/pf.sock"\nuser_id = "u_e2e"\n\n[daemon]\nmax_concurrent_jobs = 6\n\n${WS.map((ws) => `[workspace."${ws.path}"]\nprojects = [${ws.projects.map((p) => `"${p}"`).join(", ")}]\n`).join("\n")}`;
writeFileSync(cliToml, cliText);
const CLI_SHA = sha(cliText);

// ---- stub pf: list / per-workspace / workflows documents + state + argv log ----
const stubDir = join(work, "stub");
mkdirSync(stubDir);
const listDoc = {
  file: { path: cliToml, sha256: CLI_SHA, exists: true },
  items: [
    { key: "server", value: "http://127.0.0.1:1", source: "file", class: "restart-only" },
    { key: "api_key", set: true, source: "file", class: "restart-only" },
    { key: "daemon_socket", value: "/run/pf.sock", source: "file", class: "restart-only" },
    { key: "user_id", value: "u_e2e", source: "file", class: "cli-only" },
    { key: "claude_bin", value: "/usr/local/bin/claude", source: "file", class: "live" },
    { key: "step_timeout", value: "30m0s", source: "file", class: "live", schema: { kind: "duration" } },
    { key: "stall_window", value: "10m0s", source: "file", class: "live", schema: { kind: "duration" } },
    { key: "harness_root", value: "/opt/pf", source: "file", class: "restart-only" },
    { key: "operator_agent_dir", value: "/home/op/.pi", source: "file", class: "restart-only" },
    { key: "max_concurrent_jobs", value: 6, source: "file", class: "live", schema: { kind: "int", min: 1, max: 32 } },
    ...WS.map((ws) => ({ key: `[workspace."${ws.path}"]`, value: ws.projects.join(","), source: "file", class: "restart-only", section: true })),
    { key: "summarize.models", value: ["pi:example-gateway/deepseek-v4.1-flash-1017", "claude:claude-haiku-5-5"], source: "file", class: "live", schema: { kind: "model-list" } },
    { key: "summarize.language", value: "Chinese", source: "file", class: "live", schema: { kind: "string" } },
    { key: "summarize.interval_sec", value: 120, source: "file", class: "live", schema: { kind: "int", min: 10, max: 3600 } },
  ],
};
const workspaceDoc = (ws) => ({
  mapped: true, workspace: ws.path, yaml: { path: `${ws.path}/.polyforge.yaml`, sha256: YAML_SHA[ws.path], exists: true },
  steps: Object.fromEntries(ws.projects.map((p) => [p, { fix: { declared: true, models: ["pi:e2e"], source: "file", class: "live" }, default: { declared: false, models: null, source: "default", class: "live" } }])),
  steps_complete: true,
});
const chain = (models, from) => ({ models, from });
const workflowsDoc = (ws) => ({
  workflows_dir: "/e2e/workflows", daemon_reachable: true, reason: "", workspace: ws.path,
  projects: ws.projects, write_capable: true, write_reason: "",
  bundles: [{
    name: "feature-full",
    defaults: { model_candidates: ["pi:ff-default"], effective: Object.fromEntries(ws.projects.map((p) => [p, chain(["pi:ff-default"], "bundle feature-full defaults.model_candidates")])) },
    steps: [
      { id: "fix", model_candidates: ["pi:ff-fix"], effective: Object.fromEntries(ws.projects.map((p) => [p, chain(["pi:ff-fix"], "bundle feature-full step fix model_candidates")])) },
      { id: "code", model_candidates: ["pi:ff-code"], effective: Object.fromEntries(ws.projects.map((p) => [p, chain(["pi:ff-code"], "bundle feature-full step code model_candidates")])) },
    ],
  }],
  bundle_errors: [],
});
const writeList = () => writeFileAtomic(join(stubDir, "list.json"), JSON.stringify(listDoc));
writeList();
writeFileSync(join(stubDir, "workspaces.json"), JSON.stringify(Object.fromEntries(WS.map((ws) => [ws.path, workspaceDoc(ws)]))));
writeFileSync(join(stubDir, "workflows.json"), JSON.stringify(Object.fromEntries(WS.map((ws) => [ws.path, workflowsDoc(ws)]))));
const argvLog = join(stubDir, "argv.log");
const setStub = (state = {}) => { writeList(); writeFileAtomic(join(stubDir, "state.json"), JSON.stringify({ fail: "", refuse: "", ...state })); };
setStub();
writeFileSync(argvLog, "");
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
// A whole-file replace: a sibling spawn reads this file at its start (see the test side).
const saveFile = (p, data) => { fs.writeFileSync(p + "." + process.pid + ".tmp", data); fs.renameSync(p + "." + process.pid + ".tmp", p); };
const dir = path.dirname(fs.realpathSync(__filename));
const read = (name) => JSON.parse(fs.readFileSync(path.join(dir, name), "utf8"));
const state = read("state.json");
const args = process.argv.slice(2);
let stdin = ""; try { stdin = fs.readFileSync(0, "utf8"); } catch {}
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify({ args, stdin }) + "\\n");
const rest = args.filter((a) => a !== "--json");
const flag = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] ?? "" : ""; };
const out = (doc) => { process.stdout.write(JSON.stringify(doc) + "\\n"); process.exit(0); };
if (args.includes("--version")) { process.stdout.write("v2.20.21\\n"); process.exit(0); }
if (rest[0] === "config" && rest[1] === "list") out(read("list.json"));
if (rest[0] === "config" && rest[1] === "workspace") out(read("workspaces.json")[flag("--cwd")] ?? { mapped: false, workspace: null, yaml: null, steps: null });
if (rest[0] === "workflows") {
  const cwd = flag("--cwd");
  if (state.fail === cwd) { process.stderr.write("pf: cannot read " + cwd + "/.polyforge.yaml: permission denied\\n"); process.exit(1); }
  const doc = read("workflows.json")[cwd];
  if (!doc) { process.stderr.write("pf: unknown workspace " + cwd + "\\n"); process.exit(1); }
  out(doc);
}
if (rest[0] === "ops" && rest[1] === "status") out({ queue: { pending: 0, running: 0, max_concurrent: 6, max_configured: 6 }, supervision: { step_timeout: "30m0s", stall_window: "10m0s" }, reload: { pending: [], overridden: [] } });
if (rest[0] === "config" && rest[1] === "reload") out({ reloaded: [], pending: [] });
if (rest[0] === "config" && (rest[1] === "set" || rest[1] === "unset")) {
  const key = rest[2];
  if (state.refuse === key) {
    process.stderr.write(JSON.stringify({ error: { code: "CORE-400", detail: key + " must be a URL", extra: { diff: [{ key, current: "<redacted>" }] } } }) + "\\n");
    process.exit(4);
  }
  // A write advances the fixture's own list, so the next fresh read (the
  // history's old value) sees what the previous write left behind.
  if (rest[1] === "set" && !key.startsWith("steps.") && key !== "api_key") {
    const list = read("list.json");
    const item = list.items.find((entry) => entry.key === key);
    if (item) { item.value = key === "max_concurrent_jobs" ? Number(rest[3]) : rest[3]; saveFile(path.join(dir, "list.json"), JSON.stringify(list)); }
  }
  out({ key, class: "live", outcome: "applied" });
}
process.stderr.write("pf: unknown command " + rest.join(" ") + "\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
const calls = () => (existsSync(argvLog) ? readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)) : []);
const resetCalls = () => writeFileSync(argvLog, "");
const waitForCall = async (predicate, ms = 20_000) => {
  for (const deadline = Date.now() + ms; Date.now() < deadline;) {
    const hit = calls().find(predicate);
    if (hit) return hit;
    await delay(100);
  }
  return null;
};
const SAW_KEY = (key) => (call) => call.args.includes("config") && call.args.includes("set") && call.args.includes(key);

// ---- scenario bookkeeping ----
const failures = [];
const check = createCheck({ failures, message: joinedLines(4), record: ([label], text) => `${label}: ${text}` });

const h = createHarness();
let base;
let page;
try {
  ({ base } = await h.startServer({
    name: "polyforge-config-255",
    logFile: join(work, "server.log"),
    env: {
      PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: home,
      PI_CODING_AGENT_DIR: agentDir },
  }));
  await h.launchBrowser(() => chromium.launch());
  const context = await h.browser.newContext({ viewport: { width: 1280, height: 1000 }, locale: "en-US" });
  await context.addInitScript(() => { try { window.localStorage.setItem("pi-locale", "en"); } catch { /* storage */ } });
  page = await context.newPage();
  page.setDefaultTimeout(30_000);
  const requests = [];
  page.on("request", (request) => requests.push(new URL(request.url()).pathname));
  // Not the surface under test: the package switch reads the pi package list.
  await page.route("**/api/plugins?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ packages: [] }) }));

  const workflowCard = () => page.locator('section[aria-label="Workflow"]').first();
  const rowOf = (key) => page.locator(`input[aria-label="${key}"]`).first()
    .locator("xpath=ancestor::div[contains(concat(' ',normalize-space(@class),' '),' polyforge-config-row ')][1]");
  const confirmBox = () => page.locator('[role="alertdialog"]').first();
  const setFolds = async (open) => {
    for (const toggle of await page.locator(".polyforge-config-card button.polyforge-disclosure-toggle").all()) {
      if (!(await toggle.isVisible().catch(() => false))) continue;
      if (((await toggle.getAttribute("aria-expanded")) === "true") !== open) await toggle.click().catch(() => {});
    }
  };
  const expand = () => setFolds(true);
  const answerConfirm = async (answer) => {
    const box = confirmBox();
    await box.waitFor({ state: "visible", timeout: 15_000 });
    const buttons = box.getByRole("button");
    for (let i = 0; i < await buttons.count(); i += 1) {
      const button = buttons.nth(i);
      const name = (await button.innerText()).trim();
      const isCancel = /cancel|close|never|back/i.test(name);
      if ((answer === "cancel") === isCancel) { await button.click(); return; }
    }
    throw new Error(`the confirmation has no ${answer} button: ${(await box.innerText()).slice(0, 120)}`);
  };
  // Row Save, then the confirmation when the spec requires one there.
  const save = async (key, value, { confirm = true, pending = false } = {}) => {
    const input = page.locator(`input[aria-label="${key}"]`).first();
    await input.waitFor({ state: "visible", timeout: 20_000 });
    await input.fill(String(value));
    await rowOf(key).getByRole("button", { name: "Save", exact: true }).first().click();
    if (pending) return;
    if (confirm) {
      const appeared = await confirmBox().waitFor({ state: "visible", timeout: 4_000 }).then(() => true).catch(() => false);
      if (appeared) await answerConfirm("confirm");
    } else {
      await delay(400);
      if (await confirmBox().isVisible().catch(() => false)) throw new Error(`a confirmation appeared for ${key}, which needs none`);
    }
    await delay(200);
  };
  // the page is master-detail — the Host pane holds the
  // Host-level settings (the history among them), the default Cell's pane
  // the cli.toml card; a list entry selects its pane.
  const selectPane = async (pane) => {
    const entry = pane === "host" ? "[data-pf-host-entry]" : `[data-pf-cell-entry="${pane.slice("cell:".length)}"]`;
    await page.locator(entry).first().click();
    await page.locator(`[data-pf-pane="${pane}"]`).first().waitFor({ state: "visible", timeout: 30_000 });
  };
  const openSettings = async () => {
    await page.goto(`${base}/?session=${SESSION}`, { waitUntil: "domcontentloaded", timeout: NAV_MS });
    await page.getByText("E2E polyforge 255 host session").first().waitFor({ state: "attached", timeout: NAV_MS });
    const show = page.locator('button[aria-label="Show sidebar"]').first();
    if (await show.isVisible().catch(() => false)) await show.click();
    await page.locator('button[aria-label="Settings"]').first().click();
    const dialog = page.locator('[role="dialog"][aria-label="Settings"]').first();
    await dialog.getByRole("button", { name: "Polyforge", exact: true }).click();
    await page.locator(".settings-polyforge").first().waitFor({ state: "attached", timeout: 60_000 });
    // the cli.toml card is the default Cell's own pane.
    await selectPane("cell:default");
    await page.locator('input[aria-label="step_timeout"]').first().waitFor({ state: "attached", timeout: 60_000 });
  };
  await openSettings();
  // (review s17#2): each key has one layered surface — the
  // Host-only keys live in the Host and Cells card's Host region (behind the
  // multi-Cell capability), never in the cli.toml card, so these scenarios
  // drive the cli.toml card's own keys (user_id carries the same-identity
  // connection confirmation in place of daemon_socket).
  const HOST_ONLY = ["max_concurrent_jobs", "daemon_socket", "harness_root", "operator_agent_dir"];

  await check("S1", async () => {
    // (restated): server is an identity key — rendered
    // read-only with the new-profile guidance, never an input or a Save
    const advanced = ["user_id"];
    const common = ["step_timeout", "stall_window", "claude_bin", "api_key"];
    for (const key of common) {
      assert.equal(await page.locator(`input[aria-label="${key}"]`).first().isVisible(), true, `${key} has no visible value input`);
      assert.ok(await rowOf(key).getByRole("button", { name: "Save", exact: true }).count() > 0, `${key} has no Save control`);
      assert.ok((await rowOf(key).locator(".polyforge-config-badge").innerText()).trim().length > 0, `${key} lost pf's reload-class badge`);
    }
    await expand();
    for (const key of advanced) {
      assert.equal(await page.locator(`input[aria-label="${key}"]`).first().isVisible(), true, `${key} has no value input`);
      assert.ok(await rowOf(key).getByRole("button", { name: "Save", exact: true }).count() > 0, `${key} has no Save control`);
      assert.ok((await rowOf(key).locator(".polyforge-config-badge").innerText()).trim().length > 0, `${key} lost pf's reload-class badge`);
    }
    for (const key of HOST_ONLY) {
      assert.equal(await page.locator(`.polyforge-config-cli input[aria-label="${key}"]`).count(), 0, `${key} is Host-only: the cli.toml card must not edit it`);
    }
    assert.equal(await page.locator('input[aria-label="server"]').count(), 0, "server must not be editable in place");
    const serverRow = page.locator(".polyforge-config-row").filter({ has: page.locator('[data-pf-identity-hint="server"]') }).first();
    assert.equal(await serverRow.getByRole("button", { name: "Save", exact: true }).count(), 0, "server offers a Save control");
    assert.match((await serverRow.innerText()).replace(/\s+/g, " "), /new profile/i, "the server row lacks the new-profile guidance");
    const apiKey = page.locator('input[aria-label="api_key"]').first();
    assert.equal(await apiKey.getAttribute("type"), "password", "api_key must stay write-only (a password input)");
    assert.equal(await apiKey.inputValue(), "", "the api_key value must never reach the browser");
    const mappingRows = page.locator(".polyforge-config-row").filter({ has: page.locator(".polyforge-config-workspace-path") });
    assert.ok(await mappingRows.count() >= 3, "the three mapped workspaces must render as mapping rows");
    // (owner ruling 2026-11-08, overthe design record's default no. 6 and #255's read-only
    // mapping): a mapping row edits only its projects through the mapping writer — one projects
    // input, Save and Remove — and never offers its raw section key as a generic key input.
    for (const mappingRow of await mappingRows.all()) {
      const inputs = await mappingRow.locator("input").evaluateAll((nodes) => nodes.map((node) => node.getAttribute("aria-label") ?? ""));
      assert.equal(inputs.length, 1, `a [workspace."<path>"] mapping row renders exactly its projects input: ${JSON.stringify(inputs)}`);
      assert.match(inputs[0], /^Projects · \//, "the mapping row's input is its projects, labelled by its path");
      assert.ok(!inputs.some((label) => label.startsWith("[workspace")), "a mapping row offers its section key as a key input");
      assert.equal(await mappingRow.getByRole("button", { name: "Save", exact: true }).count(), 1, "a mapping row has one Save");
      assert.equal(await mappingRow.getByRole("button", { name: "Remove", exact: true }).count(), 1, "a mapping row has one Remove");
    }
  });

  // the confirmation is narrowed to the same-identity keys
  const CONNECTION = new Set(["api_key", "daemon_socket", "user_id"]);
  const NINE = [
    ["user_id", "u_e2e_255"],
    ["claude_bin", "/usr/local/bin/claude-e2e"], ["step_timeout", "45m0s"], ["stall_window", "12m0s"],
  ];
  await check("S2", async () => {
    await expand();
    resetCalls();
    for (const [key, value] of NINE) {
      await save(key, value, { confirm: CONNECTION.has(key) });
      const call = await waitForCall(SAW_KEY(key));
      assert.ok(call, `no pf config set argv for ${key}`);
      assert.ok(call.args.includes(String(value)), `${key}'s submitted value is not in the pf argv: ${JSON.stringify(call.args)}`);
      const feedback = (await rowOf(key).locator(".polyforge-config-row-feedback").innerText().catch(() => "")).trim();
      assert.ok(feedback.length > 0 && /applied|saved|next|pf/i.test(feedback), `${key}'s row does not show pf's own result (got "${feedback}")`);
    }
    const SENTINEL = "E2E-W255-API-KEY-SENTINEL";
    await save("api_key", SENTINEL);
    const keyCall = await waitForCall(SAW_KEY("api_key"));
    assert.ok(keyCall, "no pf config set api_key spawn");
    assert.ok(!keyCall.args.some((token) => token.includes(SENTINEL)), "the api_key value appeared as an argv token");
    assert.ok(keyCall.stdin.includes(SENTINEL), "the api_key value did not reach pf on stdin");
    const log = readFileSync(join(work, "server.log"), "utf8");
    assert.ok(!log.includes(SENTINEL), "the server log carries the api_key value");
  });

  await check("S3", async () => {
    await expand();
    resetCalls();
    await save("user_id", "u_e2e_9922", { pending: true });
    const box = confirmBox();
    await box.waitFor({ state: "visible", timeout: 15_000 });
    const consequence = (await box.innerText()).replace(/\s+/g, " ");
    assert.match(consequence, /restart/i, `the confirmation does not state the daemon restart consequence: "${consequence.slice(0, 160)}"`);
    assert.match(consequence, /next|since|until|still/i, `the confirmation does not state the split-brain consequence: "${consequence.slice(0, 160)}"`);
    await answerConfirm("cancel");
    await delay(500);
    assert.equal(calls().filter(SAW_KEY("user_id")).length, 0, "cancelling the confirmation still wrote user_id");
    assert.equal(await page.locator('input[aria-label="user_id"]').first().inputValue(), "u_e2e_9922", "cancelling cleared the draft");
    await save("claude_bin", "/usr/local/bin/claude-fast", { confirm: false });
    assert.ok(await waitForCall(SAW_KEY("claude_bin")), "a non-connection key's own Save did not reach pf");
  });

  await check("S4", async () => {
    await expand();
    const before = requests.length;
    resetCalls();
    await save("user_id", "u_e2e_9933");
    await save("step_timeout", "40m0s");
    await delay(500);
    const reloadish = calls().filter((call) => /reload|shutdown|ensure/.test(call.args.join(" ")));
    assert.deepEqual(reloadish.map((call) => call.args.join(" ")), [], "a save spawned a daemon reload/shutdown/ensure");
    assert.deepEqual(requests.slice(before).filter((pathname) => /\/api\/polyforge\/config\/reload/.test(pathname)), [], "a save called the reload route");
    assert.ok(await page.getByRole("button", { name: /reload/i }).count() >= 1, "the explicit Reload control is gone");
  });

  await check("S5", async () => {
    await expand();
    const old = await page.locator('input[aria-label="user_id"]').first().inputValue();
    resetCalls();
    await save("user_id", "u_e2e_9944");
    const saved = rowOf("user_id");
    const notice = (await saved.innerText()).replace(/\s+/g, " ");
    assert.match(notice, /saved|applies|restart|notice/i, `the saved connection row shows no inline notice: "${notice.slice(0, 160)}"`);
    const rollback = saved.getByRole("button", { name: /roll\s?back|restore/i }).first();
    assert.ok(await rollback.count() > 0, "the saved connection row offers no one-click rollback");
    resetCalls();
    await rollback.click();
    const restored = await waitForCall((call) => SAW_KEY("user_id")(call) && call.args.includes(old));
    assert.ok(restored, `the rollback did not write the previous value (${old}) back through pf`);
    assert.ok(restored.args.some((token) => /^--expect-(state|hash)$/.test(token)), "the rollback did not use the CAS expectation");
    // The stub logs its argv when the process starts; the route's answer and the
    // row's re-render follow. Poll that real condition (bounded) rather than a
    // fixed 500 ms sleep, which a loaded host outruns.
    const serverInput = page.locator('input[aria-label="user_id"]').first();
    for (const deadline = Date.now() + 20_000; Date.now() < deadline && (await serverInput.inputValue()) !== old;) await delay(100);
    assert.equal(await serverInput.inputValue(), old, "the row does not show the restored value");
    await save("api_key", "E2E-W255-ROLLBACK-API-KEY");
    assert.equal(await rowOf("api_key").getByRole("button", { name: /roll\s?back|restore/i }).count(), 0, "an api_key save offers a rollback");
    await selectPane("host");
    const historyToggle = page.locator("section.polyforge-config-card").filter({ hasText: /Recent panel writes/ }).first().locator("button.polyforge-disclosure-toggle").first();
    if ((await historyToggle.getAttribute("aria-expanded")) !== "true") await historyToggle.click();
    const apiKeyHistory = page.locator(".polyforge-config-history-row").filter({ hasText: /api_key/ }).first();
    assert.equal(await apiKeyHistory.getByText(/E2E-W255-ROLLBACK-API-KEY/).count(), 0, "the history carries an api_key value");
    assert.equal(await apiKeyHistory.getByRole("button", { name: /roll\s?back|restore/i }).count(), 0, "the api_key history entry offers a rollback");
  });

  const zoneToggleOf = (key) => rowOf(key)
    .locator("xpath=preceding::button[contains(concat(' ',normalize-space(@class),' '),' polyforge-disclosure-toggle ')][1]");
  await check("S6", async () => {
    await selectPane("cell:default");
    await setFolds(false);
    for (const key of ["step_timeout", "stall_window", "claude_bin", "api_key"]) {
      assert.equal(await page.locator(`input[aria-label="${key}"]`).first().isVisible(), true, `${key} is not visible without expanding anything`);
    }
    for (const key of ["server", "user_id"]) {
      assert.notEqual(await page.locator(`input[aria-label="${key}"]`).first().isVisible().catch(() => false), true, `${key} is not folded by default`);
    }
    await expand();
    // server is shown read-only with its guidance once expanded
    assert.equal(await page.locator('[data-pf-identity-hint="server"]').first().isVisible(), true, "the server row stays hidden after expanding its zone");
    for (const key of ["user_id"]) {
      assert.equal(await page.locator(`input[aria-label="${key}"]`).first().isVisible(), true, `${key} stays hidden after expanding its zone`);
      assert.ok(await rowOf(key).getByRole("button", { name: "Save", exact: true }).count() > 0, `${key} has no Save after expanding`);
    }
    // Each file zone folds its own advanced rows behind one counted control
    // (the Host-only rows left the card, so the top-level zone
    // folds server and user_id, and the [daemon] zone has no advanced row).
    const topCount = (await zoneToggleOf("user_id").innerText()).trim();
    assert.match(topCount, /2/, `the top-level zone's counted control does not count 2: "${topCount}"`);
    const daemonZone = page.locator(".polyforge-config-zone.is-daemon").first();
    assert.equal(await daemonZone.locator("button.polyforge-disclosure-toggle").count(), 0, "the [daemon] zone folds a row the cli.toml card no longer holds");
    // A draft keeps its zone open and says so; the confirmation is never hidden.
    await page.locator('input[aria-label="user_id"]').first().fill("u_e2e_9955");
    await zoneToggleOf("user_id").click();
    assert.equal(await zoneToggleOf("user_id").getAttribute("aria-expanded"), "true", "a zone with an unsaved draft collapsed");
    assert.ok(await page.locator(".polyforge-disclosure-marker").count() > 0, "a locked zone does not say why it stays open");
    await rowOf("user_id").getByRole("button", { name: "Save", exact: true }).first().click();
    const box = confirmBox();
    await box.waitFor({ state: "visible", timeout: 15_000 });
    assert.equal(await box.evaluate((node) => Boolean(node.closest("[hidden]"))), false, "the connection-key confirmation sits inside a hidden folded region");
    await answerConfirm("cancel");
    // The stored fold state survives a reload (no draft is written to storage).
    await openSettings();
    assert.equal(await page.locator('input[aria-label="user_id"]').first().isVisible(), true, "the fold state did not survive a reload");
  });

  await check("S7", async () => {
    const headers = await page.evaluate(() => {
      const leaf = (want) => [...document.querySelectorAll("section.polyforge-config-card *")]
        .filter((node) => node.children.length === 0 && (node.textContent ?? "").trim() === want);
      const flat = leaf("cli.toml");
      const daemon = leaf("cli.toml [daemon]");
      const sectionOf = (node) => node?.closest("section.polyforge-config-card") ?? null;
      return { flat: flat.length, daemon: daemon.length, same: flat.length > 0 && daemon.length > 0 && sectionOf(flat[0]) === sectionOf(daemon[0]) };
    });
    assert.ok(headers.flat >= 1, 'no zone header names the literal section "cli.toml"');
    assert.ok(headers.daemon >= 1, 'no zone header names the literal section "cli.toml [daemon]"');
    assert.equal(headers.same, true, "the two zone headers do not live in one cli.toml card");
    const mappingInCli = await page.evaluate(() => {
      const daemon = [...document.querySelectorAll("section.polyforge-config-card *")].filter((n) => n.children.length === 0 && (n.textContent ?? "").trim() === "cli.toml [daemon]")[0];
      const cliCard = daemon?.closest("section.polyforge-config-card");
      return cliCard ? [...cliCard.querySelectorAll(".polyforge-config-workspace-path")].length : -1;
    });
    assert.equal(mappingInCli, 0, "a [workspace.\"<path>\"] mapping row moved into the cli.toml card");
    assert.ok(await page.locator(".polyforge-config-workspace-path").count() >= 3, "the mapping rows left the workspace card");
    await expand();
    const serverDaemonCell = await rowOf("server").locator(".polyforge-config-daemon").count();
    assert.equal(serverDaemonCell, 0, "the top-level zone renders a daemon-value cell");
    assert.ok(await rowOf("step_timeout").locator(".polyforge-config-daemon").count() >= 1, "the [daemon] zone lost its daemon-value column");
    assert.ok(await page.getByText("Daemon value", { exact: true }).count() >= 1, "the [daemon] zone lost its daemon-value column header");
  });

  await check("S8", async () => {
    setStub();
    const cardWorkflow = workflowCard();
    await cardWorkflow.locator("select").first().waitFor({ state: "visible", timeout: 60_000 });
    const options = (await cardWorkflow.locator("select option").allInnerTexts()).map((text) => text.trim()).sort();
    assert.deepEqual(options, WS.flatMap((ws) => ws.projects).sort(), `the workflow card's projects are not the union of the three mapped workspaces: ${options}`);
    const text = await cardWorkflow.innerText();
    for (const ws of WS) {
      assert.ok(text.includes(ws.path.split("/").pop()) || text.includes(ws.path), `no project is labelled with the source yaml path of ${ws.path}`);
    }
    assert.ok((text.match(/\.polyforge\.yaml/g) ?? []).length >= WS.length, "the projects are not labelled with their workspace yaml path");
  });

  await check("S9", async () => {
    setStub({ fail: "/workspace/ws-yf" });
    const cardWorkflow = workflowCard();
    await cardWorkflow.getByRole("button", { name: "Refresh", exact: true }).first().click();
    const reason = (() => { const deadline = Date.now() + 60_000; return deadline; })();
    for (;;) {
      const text = await cardWorkflow.innerText();
      if (/permission denied/.test(text)) break;
      assert.ok(Date.now() < reason, "the failed workspace never appeared as a degradation line");
      await delay(250);
    }
    const text = (await cardWorkflow.innerText()).replace(/\s+/g, " ");
    assert.equal((text.match(/permission denied/g) ?? []).length, 1, `the failed workspace does not degrade to exactly one line: ${text.slice(0, 300)}`);
    assert.ok(/ws-yf/.test(text), "the degradation line does not name the failed workspace");
    const options = (await cardWorkflow.locator("select option").allInnerTexts()).map((entry) => entry.trim());
    assert.ok(!options.includes("demo"), "the unreadable workspace still offers its project");
    for (const project of ["polyforge", "pi", "tsshd", "orbit", "workbench"]) assert.ok(options.includes(project), `${project} disappeared with the failed workspace`);
    await cardWorkflow.locator("select").first().selectOption("polyforge");
    const toggle = cardWorkflow.locator("button.polyforge-disclosure-toggle").first();
    if ((await toggle.getAttribute("aria-expanded")) !== "true") await toggle.click();
    const addInput = cardWorkflow.locator('input[aria-label="Add candidate · default"]').first();
    assert.equal(await addInput.isEnabled(), true, "the other workspaces' projects lost their edit control");
  });

  await check("S10", async () => {
    setStub();
    const cardWorkflow = workflowCard();
    await cardWorkflow.getByRole("button", { name: "Refresh", exact: true }).first().click();
    const deadline = Date.now() + 60_000;
    for (;;) {
      if ((await cardWorkflow.locator("select option").allInnerTexts()).some((text) => text.trim() === "demo")) break;
      assert.ok(Date.now() < deadline, "the union never offered demo after the failed workspace recovered");
      await delay(250);
    }
    await cardWorkflow.locator("select").first().selectOption("demo");
    const toggle = cardWorkflow.locator("button.polyforge-disclosure-toggle").first();
    if ((await toggle.getAttribute("aria-expanded")) !== "true") await toggle.click();
    resetCalls();
    const addInput = cardWorkflow.locator('input[aria-label="Add candidate · default"]').first();
    await addInput.fill("pi:union-probe");
    const row = page.locator(".polyforge-workflow-row").filter({ has: page.locator(".polyforge-workflow-id", { hasText: /^default$/ }) }).first();
    await row.getByRole("button", { name: "Save", exact: true }).first().click();
    const write = await waitForCall((call) => call.args.includes("set") && call.args.some((token) => token.startsWith("steps.demo.")));
    assert.ok(write, "the step write never reached pf");
    const cwdAt = write.args.indexOf("--cwd");
    assert.equal(write.args[cwdAt + 1], "/workspace/ws-yf", `the step write did not carry the owning workspace's --cwd: ${JSON.stringify(write.args)}`);
    assert.ok(write.args.some((token) => token.includes(YAML_SHA["/workspace/ws-yf"])), "the step write did not carry the owning workspace's yaml CAS");
    setStub({ fail: "/workspace/ws-yf" });
    await cardWorkflow.getByRole("button", { name: "Refresh", exact: true }).first().click();
    // Bounded poll for the refreshed union (as S9 does), not a fixed delay: under a loaded full bar the
    // refresh can land after 1.5s, as a verification run showed.
    const dropDeadline = Date.now() + 60_000;
    while ((await cardWorkflow.locator("select option").allInnerTexts()).some((text) => text.trim() === "demo")) {
      assert.ok(Date.now() < dropDeadline, "the bounded poll timed out: demo still in the union 60 s after the failing refresh");
      await delay(250);
    }
    resetCalls();
    assert.equal((await cardWorkflow.locator("select option").allInnerTexts()).some((text) => text.trim() === "demo"), false, "the unreadable workspace still offers a project to write");
    assert.equal(calls().filter((call) => call.args.some((token) => token.startsWith("steps.demo."))).length, 0, "a write was issued for the unreadable workspace's project");
  });

  await check("S11", async () => {
    setStub();
    const cardWorkflow = workflowCard();
    await cardWorkflow.getByRole("button", { name: "Refresh", exact: true }).first().click();
    await delay(2000);
    resetCalls();
    for (let refresh = 0; refresh < 2; refresh += 1) {
      await cardWorkflow.getByRole("button", { name: "Refresh", exact: true }).first().click();
      const deadline = Date.now() + 60_000;
      for (;;) {
        const wanted = (refresh + 1) * WS.length;
        if (calls().filter((call) => call.args.includes("workflows")).length >= wanted) break;
        assert.ok(Date.now() < deadline, "a refresh never read every mapped workspace");
        await delay(250);
      }
    }
    const read = calls();
    const workflowsCalls = read.filter((call) => call.args.includes("workflows"));
    const workspaceCalls = read.filter((call) => call.args.includes("config") && call.args.includes("workspace"));
    const listCalls = read.filter((call) => call.args.includes("config") && call.args.includes("list"));
    for (const ws of WS) {
      assert.equal(workflowsCalls.filter((call) => call.args.includes(ws.path)).length, 2, `the two refreshes did not read ${ws.path} fresh twice`);
      assert.equal(workspaceCalls.filter((call) => call.args.includes(ws.path)).length, 2, `the two refreshes did not read ${ws.path}'s yaml twice`);
    }
    assert.equal(workflowsCalls.length, 2 * WS.length, `per-project or missing workflow spawns: ${JSON.stringify(workflowsCalls.map((call) => call.args))}`);
    assert.equal(workspaceCalls.length, 2 * WS.length, "the workspace reads are not one per mapped workspace per refresh");
    assert.equal(listCalls.length, 2, "the mapping was not read exactly once per refresh");
  });

  await check("S12", async () => {
    const language = page.locator('input[aria-label="summarize.language"]').first();
    assert.equal(await language.inputValue(), "Chinese", "summarize.language does not show config list's value");
    const card = await language.evaluate((node) => node.closest("section.polyforge-config-card")?.getAttribute("aria-label") ?? null);
    assert.equal(card, "cli.toml", "the summarize row does not live in the cli.toml card");
    resetCalls();
    await save("summarize.language", "Japanese");
    const write = await waitForCall(SAW_KEY("summarize.language"));
    assert.ok(write, "the summarize write never reached pf");
    assert.ok(!write.args.includes("--cwd") && !write.args.includes("--workspace"), `the summarize write carried a path flag: ${JSON.stringify(write.args)}`);
    assert.ok(write.args.some((token) => token.includes(CLI_SHA)), "the summarize write did not carry the cli.toml CAS");
  });

  await check("S13", async () => {
    await expand();
    resetCalls();
    await page.locator('input[aria-label="claude_bin"]').first().fill("");
    await rowOf("claude_bin").getByRole("button", { name: "Save", exact: true }).first().click();
    const feedback = (await rowOf("claude_bin").locator(".polyforge-config-row-feedback").innerText().catch(() => "")).trim();
    assert.match(feedback, /required|empty|must/i, `an empty path key was not refused with visible row text (got "${feedback}")`);
    assert.equal(calls().filter(SAW_KEY("claude_bin")).length, 0, "an empty value reached pf");
    setStub({ refuse: "claude_bin" });
    await save("claude_bin", "/usr/local/bin/claude-refused", { confirm: false });
    // the refusal reaches the row only after the route's pf spawns
    // answer, which a fixed 500 ms dwell outran on GitHub's 2-core runner; wait
    // for pf's text (bounded), then assert on the row as before.
    const rowText = async () => (await rowOf("claude_bin").innerText()).replace(/\s+/g, " ");
    for (const deadline = Date.now() + 20_000; Date.now() < deadline && !/must be a URL/.test(await rowText());) await delay(100);
    const refused = await rowText();
    assert.match(refused, /must be a URL/, `pf's own refusal is not on the row: ${refused.slice(0, 200)}`);
    assert.match(refused, /claude_bin/, "the row does not show pf's key-level diff");
    assert.equal(sha(readFileSync(cliToml, "utf8")), CLI_SHA, "pi-web wrote cli.toml itself");
  });
} catch (error) {
  console.error(`ABORT ${error?.stack ?? error}`);
  process.exitCode = 1;
} finally {
  await h.close();
}

console.log("");
for (const failure of failures) console.log(`FAIL ${failure}`);
console.log(failures.length ? `${failures.length}/${check.count} scenario(s) failed` : `all ${check.count} scenarios passed`);
if (failures.length) process.exitCode = 1;