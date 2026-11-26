// carries: components/OnboardingWizard*, components/AppShell.tsx, components/GeneralSettings.tsx
// carries: app/api/onboarding/*, lib/onboarding-state.ts, lib/onboarding-flow.ts
// carries: app/onboarding.css, components/PiAccountLogin.tsx, components/SettingsPanel.tsx, components/HarnessModelSettings.tsx, components/ModelsConfig.tsx
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
    await page.locator(".onboarding-details summary").click();
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
    const dialog = page.locator('[data-onboarding-ticket-form][data-lane="develop"]').first();
    await dialog.waitFor({ state: "visible", timeout: COLD_MS });
    await dialog.locator('option[value="polyforge"]').waitFor({ state: "attached", timeout: COLD_MS });
    await dialog.locator("input").first().fill("First ticket from onboarding");
    await dialog.locator("textarea").fill("Created by the first-open onboarding e2e.");
    let releaseCreate;
    const createGate = new Promise((resolve) => { releaseCreate = resolve; });
    await page.route("**/api/polyforge/messages/file-ticket", async (route) => { await createGate; await route.continue(); });
    await dialog.getByRole("button", { name: "Confirm and file" }).click({ timeout: ACTION_MS });
    try {
      assert.equal(await page.locator("[data-onboarding-close]").isDisabled(), true);
      assert.equal(await page.locator('[data-onboarding-step="workspace"] button').isDisabled(), true);
      await page.keyboard.press("Escape");
      assert.equal(await dialog.isVisible(), true, "Escape cannot discard an in-flight ticket");
    } finally { releaseCreate(); }
    await wizard.waitFor({ state: "detached", timeout: COLD_MS });
    await page.unroute("**/api/polyforge/messages/file-ticket");
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

  await scenario("OB10", "setup layouts, local failure facts, keyboard focus and screenshots", async () => {
    const screenshotDir = process.env.ONBOARDING_SCREENSHOT_DIR;
    if (screenshotDir) mkdirSync(screenshotDir, { recursive: true });
    const localConnection = { phase: "done", mode: "local", defaultCell: "default", server: "http://aihub:8420", project: "workstation", userReady: true, keyReady: true };
    for (const width of [900, 1280, 390]) for (const theme of ["light", "dark"]) {
      const { context: visualContext, page: visual } = await newPage();
      contexts.push(visualContext);
      await visual.setViewportSize({ width, height: 800 });
      await visualContext.addInitScript(({ theme }) => { localStorage.setItem("pi-theme", theme); localStorage.setItem("pi-locale", "zh-CN"); }, { theme });
      let connection = localConnection;
      await visual.route("**/api/onboarding", async (route) => {
        if (route.request().method() !== "GET") return route.fulfill({ json: { imageManaged: true, version: 1, steps: { aihub: "incomplete", account: "incomplete", workspace: "incomplete", ticket: "incomplete" }, nextStep: "aihub", dismissed: false, connection } });
        await route.fulfill({ json: { imageManaged: true, version: 1, steps: { aihub: "incomplete", account: "incomplete", workspace: "incomplete", ticket: "incomplete" }, nextStep: "aihub", dismissed: false, connection } });
      });
      await visual.goto(base, { waitUntil: "domcontentloaded", timeout: COLD_MS });
      const surface = visual.locator("[data-onboarding-wizard]");
      await surface.waitFor({ state: "visible" });
      for (const step of ["aihub", "account", "workspace", "ticket"]) {
        await visual.locator(`[data-onboarding-step-panel="${step}"]`).waitFor({ state: "visible" });
        assert.match(await visual.locator(".onboarding-step-count").innerText(), new RegExp(String(["aihub", "account", "workspace", "ticket"].indexOf(step) + 1)), "progress must follow the displayed skipped-to step");
        const bounds = await surface.evaluate((element) => {
          const footer = element.querySelector(".onboarding-footer").getBoundingClientRect();
          return { overflow: element.scrollWidth > element.clientWidth, pageOverflow: document.documentElement.scrollWidth > innerWidth, footerBottom: footer.bottom, height: innerHeight };
        });
        assert.equal(bounds.overflow || bounds.pageOverflow, false, `${width}/${theme}/${step} overflows`);
        assert.ok(bounds.footerBottom <= bounds.height, "actions stay inside the viewport");
        if (screenshotDir) await visual.screenshot({ path: join(screenshotDir, `${width}-${theme}-${step}.png`) });
        if (step === "ticket") {
          await visual.locator("[data-onboarding-ticket-open]").click();
          await visual.locator("[data-onboarding-ticket-form]").waitFor();
          await visual.locator('[data-onboarding-ticket-form] option[value="polyforge"]').waitFor({ state: "attached" });
          assert.equal(await surface.locator(".onboarding-footer").count(), 1, "embedded ticket has only one footer");
          assert.equal(await surface.evaluate((el) => el.scrollWidth > el.clientWidth), false);
          if (screenshotDir) await visual.screenshot({ path: join(screenshotDir, `${width}-${theme}-ticket-form.png`) });
        } else await visual.locator(".onboarding-skip").click();
      }
      await visualContext.close();
    }
    const { context: statusContext, page: statusPage } = await newPage();
    contexts.push(statusContext);
    let connection = localConnection;
    await statusPage.route("**/api/onboarding", (route) => route.fulfill({ json: { imageManaged: true, version: 1, steps: { aihub: "incomplete", account: "incomplete", workspace: "incomplete", ticket: "incomplete" }, nextStep: "aihub", dismissed: false, connection } }));
    for (const changes of [{ phase: "config/failed" }, { phase: "identity" }, { keyReady: false }, { mode: null }, null]) {
      connection = changes === null ? null : { ...localConnection, ...changes };
      await statusPage.goto(base, { waitUntil: "domcontentloaded" });
      const primary = statusPage.locator('[data-onboarding-primary="aihub"]');
      await primary.waitFor();
      assert.equal(await primary.isDisabled(), true, "incomplete or absent setup cannot continue");
      assert.equal(await statusPage.locator('[data-connection-status="ready"]').count(), 0);
    }
    const close = statusPage.locator("[data-onboarding-close]");
    await close.focus();
    await statusPage.keyboard.press("Shift+Tab");
    assert.equal(await statusPage.locator(".onboarding-skip").evaluate((el) => el === document.activeElement), true, "Tab stays inside setup");
    await statusContext.close();
  });

  await scenario("OB11", "custom API card is first in API Key, searchable, keyboard reachable and opens the existing draft", async () => {
    const { context: customContext, page: customPage } = await newPage();
    contexts.push(customContext);
    await customPage.route("**/api/onboarding", (route) => route.fulfill({ json: { imageManaged: false } }));
    await customPage.route("**/api/auth/providers", (route) => route.fulfill({ json: {
      oauthProviders: [{ id: "openai-codex", name: "ChatGPT Plus/Pro", loggedIn: false }],
      apiKeyProviders: [{ id: "openai", displayName: "OpenAI", configured: false, modelCount: 2 }, { id: "google", displayName: "Google", configured: true, modelCount: 1 }],
    } }));
    await customPage.route("**/api/models-config", (route) => route.fulfill({ json: { providers: {} } }));
    await customPage.route("**/api/models/enabled**", (route) => route.fulfill({ json: {
      allEnabled: true, patterns: null, stalePatterns: [], enabledTotal: 1, availableTotal: 1,
      scope: "global", settingsPath: "~/.pi/agent/settings.json", editable: true,
      providers: [{ id: "google", name: "Google", kind: "builtin", enabledCount: 1, models: [{ id: "test-model", name: "Test model", ref: "google/test-model", enabled: true, contextWindow: 32000 }] }],
    } }));
    await customPage.goto(base, { waitUntil: "domcontentloaded" });
    const settings = await openSettings(customPage);
    // A newly drafted native model override is not an unfinished custom provider.
    await settings.locator(".settings-section-tabs").getByRole("button", { name: "Models", exact: true }).click();
    await settings.getByRole("button", { name: "pi Providers", exact: true }).click();
    const providerPane = settings.locator('[data-model-page-pane="providers"]');
    await providerPane.locator('option[value="apikey:google"]').waitFor({ state: "attached" });
    await providerPane.getByRole("combobox", { name: "Provider", exact: true }).selectOption("apikey:google");
    await providerPane.locator(".model-context-input").fill("42000");
    await settings.locator(".settings-section-tabs").getByRole("button", { name: "Account", exact: true }).click();
    await settings.getByRole("button", { name: "pi", exact: true }).click();
    await settings.locator('[data-account-action="add-provider"]').click();
    const picker = customPage.locator("[data-add-provider-picker]");
    await picker.waitFor();
    assert.deepEqual(await picker.locator("[data-provider-kind]").evaluateAll((els) => els.map((el) => el.dataset.providerKind)), ["oauth", "custom", "apikey"]);
    for (const query of ["第三方", "自定义", "OpenAI兼容", "Anthropic兼容", "custom", "no-such-native-provider"]) {
      await picker.getByRole("textbox").fill(query);
      assert.equal(await picker.locator("[data-provider-custom]").isVisible(), true, `custom entry missing for ${query}`);
    }
    await picker.getByRole("textbox").fill("");
    if (process.env.ONBOARDING_SCREENSHOT_DIR) await customPage.screenshot({ path: join(process.env.ONBOARDING_SCREENSHOT_DIR, "1280-light-provider-picker.png") });
    await picker.locator("[data-provider-custom]").focus();
    await customPage.keyboard.press("Enter");
    await picker.waitFor({ state: "detached" });
    await providerPane.waitFor({ state: "visible" });
    const baseUrl = providerPane.getByPlaceholder("https://api.example.com/v1");
    await baseUrl.waitFor();
    await waitFor(() => providerPane.evaluate((element) => element.contains(document.activeElement) && document.activeElement?.tagName === "INPUT"), NAV_MS, "keyboard navigation must focus the custom editor");
    await baseUrl.fill("https://draft.example.test/v1");
    await settings.locator(".settings-section-tabs").getByRole("button", { name: "Account", exact: true }).click();
    await settings.locator('[data-account-action="add-provider"]').click();
    await picker.locator("[data-provider-custom]").click();
    await providerPane.waitFor({ state: "visible" });
    assert.equal(await baseUrl.inputValue(), "https://draft.example.test/v1", "the custom navigation overwrote the unfinished draft");
    assert.equal(await providerPane.locator('option[value="provider:custom-1"]').count(), 0, "returning created another blank provider");
    await providerPane.getByRole("combobox", { name: "Provider", exact: true }).selectOption("apikey:google");
    assert.equal(await providerPane.locator(".model-context-input").inputValue(), "42000", "custom navigation must preserve managed overrides too");
    await customContext.close();
  });

  await scenario("OB12", "account completion uses saved available pi models, never navigation or an empty/failed read", async () => {
    const { context: accountContext, page: accountPage } = await newPage();
    contexts.push(accountContext);
    const completions = [];
    await accountPage.route("**/api/onboarding", (route) => {
      if (route.request().method() === "POST") completions.push(route.request().postDataJSON());
      return route.fulfill({ json: { imageManaged: true, version: 1, steps: { aihub: "complete", account: "incomplete", workspace: "incomplete", ticket: "incomplete" }, nextStep: "account", dismissed: false, connection: null } });
    });
    await accountPage.route("**/api/settings/account", (route) => route.fulfill({ json: { claude: { loggedIn: false }, cells: [] } }));
    let models = { providers: [] };
    await accountPage.route("**/api/models/enabled", (route) => route.fulfill({ json: models }));
    await accountPage.route("**/api/models-config", (route) => route.fulfill({ json: { providers: {} } }));
    await accountPage.goto(base, { waitUntil: "domcontentloaded" });
    await accountPage.locator("[data-onboarding-models-link]").click();
    const providers = accountPage.locator('[data-model-page-pane="providers"]');
    await providers.waitFor({ state: "visible" });
    await providers.getByPlaceholder("https://api.example.com/v1").waitFor();
    assert.equal(completions.length, 0, "opening custom setup is not completion");
    await accountPage.keyboard.press("Escape");
    await accountPage.locator('[role="dialog"][aria-label="Settings"]').waitFor({ state: "detached" });
    assert.equal(await accountPage.locator("[data-onboarding-models-link]").evaluate((el) => el === document.activeElement), true);
    for (const result of [{ providers: [] }, { error: "unavailable" }, { modelError: "invalid configuration", providers: [{ models: [{ ref: "custom/model" }] }] }]) {
      models = result;
      await accountPage.locator("[data-onboarding-account-check]").click();
      await accountPage.locator("[data-onboarding-account-status]").waitFor();
      assert.equal(completions.length, 0, "an empty/failed model read must not complete");
    }
    models = { providers: [{ id: "custom", kind: "custom", models: [{ ref: "custom/model" }] }] };
    await accountPage.locator("[data-onboarding-account-check]").click();
    await accountPage.locator('[data-onboarding-step-panel="workspace"]').waitFor();
    assert.deepEqual(completions, [{ action: "complete", step: "account" }]);
    await accountContext.close();
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