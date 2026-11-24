// carries: components/OnboardingWizard*, components/AppShell.tsx, components/GeneralSettings.tsx
// carries: app/api/onboarding/*, lib/onboarding-state.ts, lib/onboarding-flow.ts
// — polyforge#167 (91-onboarding) AC2, authored before the implementation from
// the ticket alone: image mode only, four steps (AIHub → 账号 → 工作区 → 第一张票),
// each skippable, the team mode's `pf cell add` key on stdin, the account deep
// link + login read, the develop-lane first ticket, reopen from Settings › 常规,
// and no wizard outside the image. The stub `pf`/`claude` stand in for the
// external CLIs only; the onboarding API, routes and pages are the real ones.
//   E2E_SERVER_MODE=start npm run test:e2e:all -- e2e/onboarding-91.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { createCheck, createHarness, createWaitUntil, errorText, setupFailed } from "./harness.mjs";

const KEY_SENTINEL = "E2E-ONBOARDING-KEY-SENTINEL-167";
const TEAM_SERVER = "https://aihub.team.example.test:8420";

const scratch = mkdtempSync(join(tmpdir(), "pi-web-onboarding-91-"));
const home = join(scratch, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(home, "project");
const stubBin = join(scratch, "bin");
const stubLogDir = join(scratch, "stub");
const dataDir = join(scratch, "data");
const seedRoot = join(scratch, "seed");
const hostSeedRoot = join(scratch, "host-mode-seed");
const claudeConfig = join(scratch, "claude-config");
const distDir = process.env.PI_WEB_DIST_DIR || ".next/e2e/onboarding-91";
for (const dir of [agentDir, project, stubBin, stubLogDir, dataDir, claudeConfig]) mkdirSync(dir, { recursive: true });

// The image's read-only seed manifest — the one image-mode fact. NODE_OPTIONS
// preloads the test-only seam into the Next server process (production reads
// the constant when the global is unset).
mkdirSync(join(seedRoot, "releases", "v0.2.4"), { recursive: true });
writeFileSync(join(seedRoot, "releases", "v0.2.4", "manifest.json"), "{}\n");
const preload = join(scratch, "seed-preload.cjs");
writeFileSync(preload, `if (process.env.PI_WEB_E2E_SEED_ROOT) globalThis.__piWebTestSeedRoot = process.env.PI_WEB_E2E_SEED_ROOT;\n`);

// #162's published first-boot record: external (team) mode, done, ready.
mkdirSync(join(dataDir, "workstation"), { recursive: true });
writeFileSync(join(dataDir, "workstation", "first-boot.json"), JSON.stringify({
  phase: "done",
  mode: "external",
  defaultCell: "default",
  server: TEAM_SERVER,
  project: "polyforge",
  userReady: true,
  keyReady: true,
  imageManaged: true,
  updatedAt: "2026-11-23T00:00:00Z",
}, null, 2) + "\n");

const pfLog = join(stubLogDir, "pf-calls.jsonl");
const claudeLog = join(stubLogDir, "claude-calls.jsonl");
writeFileSync(pfLog, "");
writeFileSync(claudeLog, "");
const pfStub = join(stubBin, "pf");
writeFileSync(pfStub, `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
let stdin = "";
let done = false;
const finish = () => {
  if (done) return;
  done = true;
  fs.appendFileSync(${JSON.stringify(pfLog)}, JSON.stringify({ argv: args, stdin }) + "\\n");
  const rest = args.filter((a) => a !== "--json");
  const out = (value) => { process.stdout.write(JSON.stringify(value)); process.exit(0); };
  const identity = ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"];
  if (args.includes("--version")) {
    if (args.includes("--json")) out({ version: "v2.20.12", capabilities: identity });
    process.stdout.write("v2.20.12\\n");
    process.exit(0);
  }
  if (rest[0] === "profile") out({ profile: "default" });
  if (rest.includes("config") && rest.includes("list")) out({ items: [{ key: '[workspace."' + (process.env.PF_STUB_PROJECT || "/ws") + '"]', value: "polyforge", section: true }] });
  if (rest.includes("projects") && rest.includes("get")) out({ project: rest[rest.indexOf("get") + 1], status: "active", members: [] });
  if (rest.includes("doctor")) out({ cells: null });
  if (rest.includes("ops") && rest.includes("status")) out({ queue: { running: 0, pending: 0 }, daemon: { running: true } });
  if (rest.includes("cell") && rest.includes("add")) out({ ok: true, profile: rest[rest.indexOf("add") + 1] });
  if (rest.includes("file") && rest.includes("create")) out({ id: "wi_e2e_onboarding", slug: "polyforge#91" });
  process.exit(2);
};
process.stdin.on("data", (chunk) => { stdin += chunk; });
process.stdin.on("end", finish);
setTimeout(finish, 200);
`);
chmodSync(pfStub, 0o755);

const claudeStub = join(stubBin, "claude");
writeFileSync(claudeStub, `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(claudeLog)}, JSON.stringify(args) + "\\n");
if (args.includes("auth") && args.includes("status")) {
  process.stdout.write(JSON.stringify({ loggedIn: true, authMethod: "claude.ai", configDirectory: process.env.CLAUDE_CONFIG_DIR || "" }));
  process.exit(0);
}
if (args.includes("--version")) { process.stdout.write("2.1.322\\n"); process.exit(0); }
process.stdout.write("{}\\n");
`);
chmodSync(claudeStub, 0o755);

const calls = () => readFileSync(pfLog, "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
const readText = (file) => (existsSync(file) ? readFileSync(file, "utf8") : "");
const stateFile = join(agentDir, "pi-web-onboarding.json");
const serverFile = join(scratch, "server.log");

const h = createHarness();
let browser;
let base;
const contexts = [];
const failures = [];
const scenario = createCheck({ failures, message: errorText, record: (names, text) => `${names.join(" ")}: ${text}` });
const waitFor = createWaitUntil({ every: 200, withLast: true });

function serverEnv(seed) {
  return {
    HOME: home,
    PI_CODING_AGENT_DIR: agentDir,
    POLYFORGE_DATA_DIR: dataDir,
    PF_BIN: pfStub,
    PF_STUB_PROJECT: project,
    PATH: `${stubBin}:${process.env.PATH ?? ""}`,
    CLAUDE_CONFIG_DIR: claudeConfig,
    PI_WEB_ALLOWED_ROOT_PREFIXES: scratch,
    PI_WEB_DIST_DIR: distDir,
    PI_WEB_IDLE_TIMEOUT_MS: "5000",
    NODE_OPTIONS: `--require ${preload}`,
    PI_WEB_E2E_SEED_ROOT: seed,
  };
}

async function newPage() {
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, locale: "en-US" });
  await context.addInitScript(() => {
    try {
      localStorage.setItem("pi-locale", "en");
      localStorage.setItem("pi-web:split-view-enabled", "false");
    } catch { /* storage unavailable */ }
  });
  const page = await context.newPage();
  page.setDefaultTimeout(NAV_MS);
  return { context, page };
}

const stepStatuses = (page) => page.locator("[data-onboarding-step]").evaluateAll((rows) => rows.map((row) => [row.getAttribute("data-onboarding-step"), row.getAttribute("data-onboarding-status")]));

async function openSettings(page) {
  const button = page.getByRole("button", { name: /^Settings$/ }).first();
  await button.waitFor({ state: "visible", timeout: NAV_MS });
  const dialog = page.locator('[role="dialog"][aria-label="Settings"]').first();
  for (let attempt = 0; attempt < 6; attempt += 1) {
    await button.click({ timeout: ACTION_MS }).catch(() => {});
    try {
      await dialog.waitFor({ state: "visible", timeout: 30_000 });
      return dialog;
    } catch { /* not hydrated yet */ }
  }
  await dialog.waitFor({ state: "visible", timeout: NAV_MS });
  return dialog;
}

try {
  ({ base } = await h.startServer({ name: "onboarding-91", logFile: serverFile, env: serverEnv(seedRoot) }));
  browser = await h.launchBrowser(() => chromium.launch());
  const { context, page } = await newPage();
  contexts.push(context);
  const wizard = page.locator("[data-onboarding-wizard]");
  const problems = [];
  page.on("pageerror", (error) => problems.push(error.message));

  await scenario("OB1", "the first open in the image shows the four-step wizard in order, on the AIHub step", async () => {
    await page.goto(`${base}/`, { waitUntil: "domcontentloaded", timeout: COLD_MS });
    await wizard.waitFor({ state: "visible", timeout: COLD_MS });
    assert.deepEqual(await stepStatuses(page), [
      ["aihub", "incomplete"],
      ["account", "incomplete"],
      ["workspace", "incomplete"],
      ["ticket", "incomplete"],
    ], "the four steps are not in the ticket's order or not all 「未完成」");
    assert.equal(await page.locator('[data-onboarding-step-panel="aihub"]').count(), 1, "the wizard does not start on AIHub");
    assert.equal(await page.locator("[data-onboarding-connection]").getAttribute("data-onboarding-connection"), "external", "the team mode is not read from the first-boot record");
    assert.match(await page.locator('[data-fact="server"]').innerText(), /aihub\.team\.example\.test:8420/, "the connection result does not show the team server");
    assert.match(await page.locator('[data-fact="userReady"]').innerText(), /ready/, "the first-boot user fact is not shown");
    assert.match(await page.locator('[data-fact="keyReady"]').innerText(), /ready/, "the first-boot key fact is not shown");
  });

  await scenario("OB9", "the first-open popup survives the entry fallback opening its draft session (the null-session branch is stable)", async () => {
    // Split view is ON in this context, so AppShell's entry fallback opens the
    // new-session draft tab — the transient that used to stand in for the
    // null-session branch. The wizard must still be there after it fired.
    const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, locale: "en-US" });
    contexts.push(context);
    const page = await context.newPage();
    page.setDefaultTimeout(NAV_MS);
    const entryFallback = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/cwd/validate"
      && response.request().method() === "POST", { timeout: COLD_MS }).catch(() => null);
    await page.goto(`${base}/`, { waitUntil: "domcontentloaded", timeout: COLD_MS });
    const fallback = await entryFallback;
    assert.ok(fallback, "the entry fallback never ran: split view was off and the transient was not exercised");
    // The fallback's answer only starts the draft's open: its `.then` calls
    // handleNewSession, whose setState (and React's commit) come after the
    // response. The applied fact is the draft tab itself, so wait for that
    // tab before reading the wizard — a regression that let the draft close
    // the popup has its effect in the same commit the tab first shows.
    await page.locator("#pane-tab-__new-session__").waitFor({ state: "attached", timeout: COLD_MS });
    await page.locator("[data-onboarding-wizard]").waitFor({ state: "visible", timeout: COLD_MS });
    assert.equal(await page.locator('[data-onboarding-step-panel="aihub"]').count(), 1, "the draft-session fallback closed the first-open wizard");
  });

  await scenario("OB2", "the team mode adds a Cell through the existing route: the key rides stdin only", async () => {
    await page.locator("[data-onboarding-cell-profile]").fill("team");
    await page.locator("[data-onboarding-cell-server]").fill(TEAM_SERVER);
    await page.locator("[data-onboarding-cell-key]").fill(KEY_SENTINEL);
    await page.locator("[data-onboarding-cell-add]").click({ timeout: ACTION_MS });
    await page.locator('[data-onboarding-step-panel="account"]').waitFor({ state: "visible", timeout: COLD_MS });
    await waitFor(async () => (await stepStatuses(page)).find(([step]) => step === "aihub")[1] === "complete", COLD_MS, "the AIHub step did not complete after the Cell add");
    const add = calls().find((call) => call.argv.includes("cell") && call.argv.includes("add"));
    assert.ok(add, "no `pf cell add` was spawned");
    assert.ok(add.argv.includes("team") && add.argv.includes("--server"), "the Cell add did not carry the profile and server");
    assert.ok(!add.argv.includes(KEY_SENTINEL), "the API key rode the command line");
    assert.ok(add.stdin.includes(KEY_SENTINEL), "the API key did not ride stdin");
    assert.equal((await page.content()).includes(KEY_SENTINEL), false, "the API key came back to the page");
  });

  await scenario("OB3", "skipping the account step leaves it 「未完成」 and moves the wizard on", async () => {
    // The panel's own skip control (the wizard's per-step skip). The write's
    // own answer is read first: the panel advance is optimistic, and a read
    // before the write lands could not tell a recorded completion from it.
    const skipWritten = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/onboarding"
      && response.request().method() === "POST", { timeout: COLD_MS });
    await page.locator('[data-onboarding-step-panel="account"]').getByRole("button", { name: /^Skip for now$/ }).click({ timeout: ACTION_MS });
    const answered = await (await skipWritten).json();
    assert.equal(answered.steps.account, "incomplete", "a skipped step was recorded complete (the write answered complete)");
    await page.locator('[data-onboarding-step-panel="workspace"]').waitFor({ state: "visible", timeout: COLD_MS });
    await waitFor(async () => (await stepStatuses(page)).find(([step]) => step === "account")[1] === "incomplete", COLD_MS, "the applied skip read complete");
  });

  await scenario("OB4", "the workspace goes through the existing selection, then the first ticket is the develop lane", async () => {
    await page.locator("[data-onboarding-workspace-path]").fill(project);
    await page.locator("[data-onboarding-workspace-use]").click({ timeout: ACTION_MS });
    await page.locator('[data-onboarding-step-panel="ticket"]').waitFor({ state: "visible", timeout: COLD_MS });
    await waitFor(async () => (await stepStatuses(page)).find(([step]) => step === "workspace")[1] === "complete", COLD_MS, "the workspace step did not complete");

    await page.locator("[data-onboarding-ticket-open]").click({ timeout: ACTION_MS });
    const dialog = page.locator('[role="dialog"][data-lane="develop"]').first();
    await dialog.waitFor({ state: "visible", timeout: COLD_MS });
    await dialog.locator('option[value="polyforge"]').waitFor({ state: "attached", timeout: COLD_MS });
    await dialog.locator("input").first().fill("First ticket from onboarding");
    await dialog.locator("textarea").fill("Created by the first-open onboarding e2e.");
    await dialog.getByRole("button", { name: "Confirm and file" }).click({ timeout: ACTION_MS });
    await wizard.waitFor({ state: "detached", timeout: COLD_MS });
    const create = calls().find((call) => call.argv.includes("file") && call.argv.includes("create"));
    assert.ok(create, "no `pf file create` was spawned");
    assert.ok(create.argv.includes("--stdin"), "the wizard's create did not go through stdin");
    const body = JSON.parse(create.stdin);
    assert.equal(body.bundle_name, "develop", "the first ticket did not carry the develop lane");
    assert.equal(body.goal, "First ticket from onboarding");
    assert.ok(!create.argv.includes("develop"), "the lane rode the command line");
    for (const call of calls()) assert.equal(call.argv.some((arg) => arg.includes(KEY_SENTINEL)), false, "the key appeared in a pf argv");
  });

  await scenario("OB5", "after completion a reload shows no popup; Settings › 常规 reopens at the first unfinished step", async () => {
    await page.reload({ waitUntil: "domcontentloaded", timeout: COLD_MS });
    // A positive read flag: the shell applied the state API's dismissed answer.
    // Only then is the absence of the wizard a settled fact, not a race with
    // the first read.
    await page.locator('[data-onboarding-read="dismissed"]').waitFor({ state: "attached", timeout: COLD_MS });
    await page.getByRole("button", { name: /^Settings$/ }).first().waitFor({ state: "visible", timeout: COLD_MS });
    assert.equal(await wizard.count(), 0, "the wizard popped up again after the completion/skip");
    assert.equal(await page.locator("[data-onboarding-reopen]").count(), 0, "the reopen entry is in the visible page before Settings opens");

    const settings = await openSettings(page);
    const reopen = settings.locator("[data-onboarding-reopen]");
    await reopen.waitFor({ state: "visible", timeout: NAV_MS });
    await reopen.getByRole("button", { name: /^Reopen$/ }).click({ timeout: ACTION_MS });
    await wizard.waitFor({ state: "visible", timeout: 30_000 });
    assert.equal(await wizard.getAttribute("data-onboarding-current"), "account", "the reopen did not start at the first unfinished step");
    assert.deepEqual(await stepStatuses(page), [
      ["aihub", "complete"],
      ["account", "incomplete"],
      ["workspace", "complete"],
      ["ticket", "complete"],
    ], "the reopened wizard lost a completed step or wrote the skipped one as complete");
    assert.match(await page.locator('[data-onboarding-step="account"]').innerText(), /Not done/, "the skipped step is not labelled 「未完成」");
  });

  await scenario("OB6", "the account step deep-links to Settings › 账号 and completes only on the read-back login", async () => {
    await page.locator("[data-onboarding-account-link]").click({ timeout: ACTION_MS });
    const settings = page.locator('[role="dialog"][aria-label="Settings"]').first();
    await settings.waitFor({ state: "visible", timeout: COLD_MS });
    await settings.locator('[data-settings-section="account"]').first().waitFor({ state: "visible", timeout: COLD_MS });
    await page.keyboard.press("Escape");
    await settings.waitFor({ state: "hidden", timeout: NAV_MS });

    await page.locator("[data-onboarding-account-check]").click({ timeout: ACTION_MS });
    await waitFor(async () => (await stepStatuses(page)).find(([step]) => step === "account")[1] === "complete", COLD_MS, "the account step did not complete on the read-back login");
    assert.equal((await stepStatuses(page)).find(([step]) => step === "workspace")[1], "complete", "the workspace record was lost");
    await page.locator("[data-onboarding-close]").click({ timeout: ACTION_MS });
    await wizard.waitFor({ state: "detached", timeout: COLD_MS });
    await page.reload({ waitUntil: "domcontentloaded", timeout: COLD_MS });
    await page.locator('[data-onboarding-read="dismissed"]').waitFor({ state: "attached", timeout: COLD_MS });
    await page.getByRole("button", { name: /^Settings$/ }).first().waitFor({ state: "visible", timeout: COLD_MS });
    assert.equal(await wizard.count(), 0, "the wizard reopened after being dismissed");
  });

  await scenario("OB7", "the state API, the state file, the page and the server log carry no key value", async () => {
    const response = await fetch(`${base}/api/onboarding`);
    const body = await response.text();
    assert.equal(response.status, 200);
    assert.equal(body.includes(KEY_SENTINEL), false, "the state API leaked the key");
    assert.equal(readText(stateFile).includes(KEY_SENTINEL), false, "the onboarding state file leaked the key");
    assert.equal(readText(serverFile).includes(KEY_SENTINEL), false, "the server log leaked the key");
    assert.equal(readText(pfLog).split("\n").filter(Boolean).some((line) => {
      const call = JSON.parse(line);
      return call.argv.some((arg) => arg.includes(KEY_SENTINEL));
    }), false, "a pf command line leaked the key");
    assert.deepEqual(problems, [], "the page raised an uncaught error");
  });

  await scenario("OB8", "outside the image no entry and no wizard appear, and the API answers one boolean", async () => {
    await h.stop();
    ({ base } = await h.startServer({ name: "onboarding-91-host", logFile: join(scratch, "server-host.log"), env: serverEnv(hostSeedRoot) }));
    const hostPage = await newPage();
    await hostPage.page.goto(`${base}/`, { waitUntil: "domcontentloaded", timeout: COLD_MS });
    await hostPage.page.locator('[data-onboarding-read="host"]').waitFor({ state: "attached", timeout: COLD_MS });
    await hostPage.page.getByRole("button", { name: /^Settings$/ }).first().waitFor({ state: "visible", timeout: COLD_MS });
    assert.equal(await hostPage.page.locator("[data-onboarding-wizard]").count(), 0, "a host-mode Pi Web showed the wizard");
    const response = await fetch(`${base}/api/onboarding`);
    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), { imageManaged: false }, "host mode exposed onboarding state facts");
    await hostPage.context.close();
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  for (const context of contexts) await context.close().catch(() => {});
  await browser?.close().catch(() => {});
  await h.stop().catch(() => {});
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) console.log(`FAIL onboarding-91: ${failures.length} scenario(s) failed`);
process.exit(failures.length ? 1 : 0);