// carries: lib/__fixtures__/claude-2.1.312/*, lib/__fixtures__/claude-stub/*
// carries: lib/e2e-claude-idle-timeout.test.mjs
// carries: app/api/claude/*
// carries: components/ModelSelector*
// carries: components/SelectorRow*, components/HarnessSelector*
// carries: docs/agents/tools.md
// — claude composer toolbar (model / effort / compact): e2e acceptance pass.
//
// Authored against artifacts/spec.md's `## Acceptance criteria` and its Requirement
// `#### Scenario` blocks BEFORE the implementation exists, so every assertion states
// the spec's intent and never an imagined implementation. Each scenario drives only
// surface that exists pre-code: `POST /api/agent/new` (harness claude), `GET/POST
// /api/agent/<id>`, `GET /api/sessions`, `GET /api/sessions/<id>`, `POST
// /api/sessions/stats`, `GET /api/claude`, the fresh composer, an open claude
// session, pi's own toolbar DOM (`.chat-input-controls`, `.model-selector`, the
// reasoning button, the compact button), the PATH-injected e2e stub's argv/stdin log,
// claude's transcript and pi-web's index file. Where the spec names a new fact, it is
// matched only by the user-visible or file-visible fact it names (claude's model names —
// since the aliases its initialize answer lists, not four pinned full names —
// claude's five effort levels, `/compact` on stdin, the localized result line,
// `--effort`/`--model` in argv, the index's `effort`), so a pre-code run fails at
// assertion level, never at build level.
//
// Two scenarios hold a turn open with the repository's existing unit stub
// (`lib/__fixtures__/claude-stub/claude.mjs`, `PI_TEST_CLAUDE_STUB_MODE=hang`) so the
// running-turn states are deterministic instead of racing the e2e stub's ~2 s reply;
// this is the stub's documented "hang" mode, used through a scratch shim, and it never
// writes into the repository.
//
// Every kept scenario is individually guarded and prints one `PASS S272-nn` marker.
import assert from "node:assert/strict";
import { chmodSync, existsSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, claudeEnv, claudeScratch, createCheck, createHarness, createWaitUntil, errorText, findTranscript as findTranscriptIn, pidAlive, readJsonLines, root, seedStorage, setupFailed } from "./harness.mjs";

assertNoDevServer();
const kit = claudeScratch("pi-web-claude-272-");
const { artifacts, scratch, agentDir, project, stubDir, stubLog, configDir } = kit;
const holdLog = join(scratch, "claude-hold.jsonl");
const fixtures = join(root, "lib/__fixtures__/claude-2.1.312");

// The contract the spec fixes, authored from the spec (never imported from lib/).
// claude's aliases from its initialize answer (the stub answers with the
// claude 2.1.322 recording); a session's own model is listed beside them when it is not one.
const CLAUDE_MODELS = ["default", "opus", "fable", "sonnet", "haiku"];
const INITIALIZE_FIXTURE = join(root, "lib/__fixtures__/claude-2.1.322/initialize");
const EFFORT_LEVELS = ["low", "medium", "high", "xhigh", "max"];
const HINT_EN = "Uses the system claude; if something goes wrong, adjust claude from the pi-web terminal.";
const COMPACT_RESULT_EN = /^Compacted from [\d.,]+ ?[kKmM]? tokens$/;
const COMPACT_RESULT_ZH_CN = /^已压缩：压缩前 [\d.,]+ ?[kKmM]? tokens$/;
// `fork` is a claude command now (e2e/claude-fork-370.mjs); navigate_tree stays pi's.
const piOnlyCommands = ["set_thinking_level", "set_tools", "navigate_tree", "steer", "follow_up", "reload"];

const h = createHarness();
let browser;
let desktopContext;
let mobileContext;
let page;
let base;
let firstLoad = true;
const failures = [];

const scenario = createCheck({ failures, message: errorText, record: (names, text) => `${names.join(" ")}: ${text}`, after: () => restoreDefaultStub() });

const delayMs = (ms) => delay(ms);

const stubRecords = () => readJsonLines(stubLog);
const launches = () => stubRecords().filter((entry) => entry.kind === "launch");
const launchesFor = (id) => launches().filter((entry) => entry.sessionId === id);
function holdLaunches() {
  if (!existsSync(holdLog)) return [];
  return readFileSync(holdLog, "utf8").split("\n").filter(Boolean).flatMap((line) => {
    try {
      return [JSON.parse(line)];
    } catch {
      return [];
    }
  }).filter((entry) => entry.kind === "launch");
}
function stdinEvents() {
  return stubRecords()
    .filter((entry) => entry.kind === "stdin" && typeof entry.line === "string")
    .flatMap((entry) => {
      try {
        return [JSON.parse(entry.line)];
      } catch {
        return [];
      }
    });
}
const stdinJson = () => JSON.stringify(stdinEvents());
function messageTextOf(event) {
  const content = event?.message?.content;
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content.filter((block) => block?.type === "text").map((block) => String(block.text ?? "")).join("\n");
}
const userLines = () => stdinEvents().filter((event) => event?.type === "user").map(messageTextOf);

const waitFor = createWaitUntil();

function flagValue(argv, flag) {
  const equals = argv.find((arg) => arg.startsWith(`${flag}=`));
  if (equals) return equals.slice(flag.length + 1);
  const at = argv.indexOf(flag);
  return at >= 0 ? argv[at + 1] : undefined;
}

async function apiFetch(pathname, init = {}) {
  return fetch(`${base}${pathname}`, {
    ...init,
    headers: { ...(init.body ? { "Content-Type": "application/json" } : {}), ...(init.headers ?? {}) },
    signal: AbortSignal.timeout(init.timeout ?? 120_000),
  });
}
async function getJson(pathname) {
  const response = await apiFetch(pathname);
  assert.equal(response.status, 200, `GET ${pathname} -> ${response.status}`);
  return response.json();
}
async function postJson(pathname, body) {
  const response = await apiFetch(pathname, { method: "POST", body: JSON.stringify(body) });
  return { status: response.status, body: await response.json().catch(() => ({})) };
}

async function createClaudeSession({ message, model, extra = {} }) {
  const created = await postJson("/api/agent/new", { cwd: project, type: "prompt", message, harness: "claude", model, ...extra });
  assert.equal(created.status, 200, `POST /api/agent/new (harness claude, model ${model}) -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  const id = created.body.sessionId ?? created.body.id;
  assert.ok(typeof id === "string" && id.length > 0, "the create route returned no claude session id");
  return id;
}

const findTranscript = (id) => findTranscriptIn(configDir, id);
function transcriptLines(id) {
  const file = findTranscript(id);
  if (!file) return [];
  return readFileSync(file, "utf8").split("\n").filter(Boolean).flatMap((line) => {
    try {
      return [JSON.parse(line)];
    } catch {
      return [];
    }
  });
}
const assistantModels = (id) => transcriptLines(id).filter((entry) => entry.type === "assistant").map((entry) => entry.message?.model).filter(Boolean);

const indexPath = () => join(agentDir, "claude-sessions.json");
function readIndex() {
  try {
    return JSON.parse(readFileSync(indexPath(), "utf8"));
  } catch {
    return { version: 1, sessions: [] };
  }
}
const indexRecord = (id) => (readIndex().sessions ?? []).find((record) => record.id === id);
function appendIndex(records) {
  const index = readIndex();
  index.sessions = [...(index.sessions ?? []), ...records];
  writeFileSync(indexPath(), `${JSON.stringify(index, null, 2)}\n`);
}
const seedRecord = (id, extra) => ({
  id,
  cwd: project,
  created: new Date(0).toISOString(),
  modified: new Date(0).toISOString(),
  firstMessage: id,
  usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cost: 0 },
  ...extra,
});

const bottomBar = () => page.locator(".chat-input-controls").first();
const composer = () => bottomBar().locator("xpath=..");
const textarea = () => page.locator(".chat-input-textarea").first();
const modelTrigger = () => bottomBar().locator(".model-selector > button").first();
const triggerText = async () => (await modelTrigger().innerText()).trim();
const effortTrigger = () => bottomBar().locator('button[aria-label="Change reasoning level"]').first();
const compactTrigger = () => bottomBar().locator('button[aria-label="Compact context"]').first();
const stopCompactTrigger = () => bottomBar().locator('button[aria-label="Stop compaction"]').first();
const moreTrigger = () => bottomBar().locator('button[aria-label="More controls"]').first();
const collapseTrigger = () => bottomBar().locator('button[aria-label="Collapse controls"]').first();
const effortOptions = () => page.locator('[role="option"]');
const effortOption = (label) => effortOptions().filter({ hasText: new RegExp(`^${label}\\b`) });

async function closeMenu() {
  await page.keyboard.press("Escape").catch(() => {});
  await page.locator('[role="listbox"]').last().waitFor({ state: "detached", timeout: 5_000 }).catch(() => {});
}
async function openModelMenu() {
  await modelTrigger().click({ timeout: ACTION_MS });
  const listbox = page.locator('[role="listbox"]').last();
  await listbox.waitFor({ state: "visible", timeout: 10_000 });
  const texts = (await listbox.locator('[role="option"]').allInnerTexts()).map((text) => text.trim()).filter(Boolean);
  return { listbox, texts };
}
async function chooseModel(name) {
  assert.ok(await modelTrigger().count() > 0, "the composer has no model selector, so no model can be switched");
  const { listbox, texts } = await openModelMenu();
  const index = texts.findIndex((text) => text.toLowerCase() === name.toLowerCase());
  assert.ok(index >= 0, `no "${name}" row in the model selector (rows: ${texts.join(", ")})`);
  await listbox.locator('[role="option"]').nth(index).click({ timeout: ACTION_MS });
  await closeMenu();
}
async function chooseEffort(label) {
  assert.ok(await effortTrigger().count() > 0, "the composer has no effort control");
  await effortTrigger().click({ timeout: ACTION_MS });
  await effortOptions().first().waitFor({ state: "visible", timeout: 10_000 });
  await effortOption(label).first().click({ timeout: ACTION_MS });
}

async function tryHarnessTrigger(trigger) {
  try {
    await trigger.click({ timeout: 5_000 });
    const listbox = page.locator('[role="listbox"]').last();
    await listbox.waitFor({ state: "visible", timeout: 5_000 });
    const texts = (await listbox.locator('[role="option"]').allInnerTexts()).map((text) => text.trim().toLowerCase());
    if (texts.includes("pi") && texts.includes("claude")) return { trigger, listbox };
  } catch {
    // not a selector menu
  }
  await closeMenu();
  return null;
}
async function harnessControl() {
  const candidates = bottomBar().locator('button[aria-haspopup="listbox"]');
  for (let i = 0, n = await candidates.count(); i < n; i += 1) {
    const candidate = candidates.nth(i);
    if (await candidate.evaluate((element) => Boolean(element.closest(".model-selector")))) continue;
    const found = await tryHarnessTrigger(candidate);
    if (found) return found;
  }
  return null;
}
async function chooseHarness(name) {
  const control = await harnessControl();
  assert.ok(control, "the composer's bottom bar offers no harness selector");
  const options = control.listbox.locator('[role="option"]');
  for (let i = 0, n = await options.count(); i < n; i += 1) {
    if ((await options.nth(i).innerText()).trim().toLowerCase() === name.toLowerCase()) {
      await options.nth(i).click({ timeout: ACTION_MS });
      await page.locator('[role="listbox"]').last().waitFor({ state: "detached", timeout: 5_000 }).catch(() => {});
      return;
    }
  }
  assert.ok(false, `no "${name}" row in the harness selector`);
}

async function openFreshComposer(locale = "en") {
  await page.goto(`${base}/?cwd=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded", timeout: firstLoad ? COLD_MS : NAV_MS });
  firstLoad = false;
  await textarea().waitFor({ state: "visible", timeout: NAV_MS });
  if (await page.evaluate(() => localStorage.getItem("pi-locale")) !== locale) {
    await page.evaluate((value) => localStorage.setItem("pi-locale", value), locale);
    await page.reload({ waitUntil: "domcontentloaded", timeout: NAV_MS });
    await textarea().waitFor({ state: "visible", timeout: NAV_MS });
  }
}
async function openSession(id) {
  await page.goto(`${base}/?session=${encodeURIComponent(id)}`, { waitUntil: "domcontentloaded", timeout: firstLoad ? COLD_MS : NAV_MS });
  firstLoad = false;
  await textarea().waitFor({ state: "visible", timeout: NAV_MS });
}
async function sendPrompt(text) {
  await textarea().fill(text);
  await page.getByRole("button", { name: "Send", exact: true }).first().click({ timeout: ACTION_MS });
}
async function setLocale(locale) {
  await page.evaluate((value) => localStorage.setItem("pi-locale", value), locale);
  await page.reload({ waitUntil: "domcontentloaded", timeout: NAV_MS });
  await textarea().waitFor({ state: "visible", timeout: NAV_MS });
}

// The repository's existing unit stub in its documented `hang` mode: a turn that
// stays open until Stop, so the running-turn states do not race a 2 s reply.
const unitStub = join(root, "lib/__fixtures__/claude-stub/claude.mjs");
const defaultStub = join(root, "e2e/claude-stub/claude.mjs");
function installStub(source, envPrefix = "") {
  const shim = `#!/bin/sh\n${envPrefix} exec ${JSON.stringify(process.execPath)} ${JSON.stringify(source)} "$@"\n`;
  writeFileSync(join(stubDir, "claude"), shim);
  chmodSync(join(stubDir, "claude"), 0o755);
}
const installHoldStub = () => installStub(unitStub, `PI_TEST_CLAUDE_STUB_MODE=hang PI_TEST_CLAUDE_STUB_LOG=${JSON.stringify(holdLog)}`);
const restoreDefaultStub = () => installStub(defaultStub);

async function waitChildGone(id) {
  await waitFor(async () => {
    const response = await apiFetch(`/api/agent/${encodeURIComponent(id)}`);
    return response.status === 200 && (await response.json()).running === false;
  }, 60_000, "the claude child did not exit, so the no-live-child state cannot be observed", 400);
}
async function killChild(id) {
  await waitFor(() => launchesFor(id).length > 0, 30_000, "no claude child was launched for the session", 200);
  // The hold stub records no session id, so its newest child counts too; only one
  // hold child is live at a time and every scenario restores the default stub.
  const pids = [...launchesFor(id).map((entry) => entry.pid), ...holdLaunches().map((entry) => entry.pid)].filter(pidAlive);
  assert.ok(pids.length > 0, "no live claude child was found to end");
  for (const pid of pids) {
    try { process.kill(pid, "SIGKILL"); } catch { /* already gone */ }
  }
  await waitChildGone(id);
}
const isRunning = async (id) => (await getJson(`/api/agent/${encodeURIComponent(id)}`)).running === true;

h.trapInterrupt();

try {
  ({ base } = await h.startServer({ name: "claude-composer-272", logFile: join(artifacts, "claude-composer-272-server.log"), env: claudeEnv(kit, {
      // the product default, not 5 s. A page renews its lease only
      // every 30 s, so a 5 s window reaps an open session's child whenever a loaded
      // scenario dwells; the stream then reconnects and resumes the child, and the
      // launch-count scenarios read that resume as their own restart. Keep this
      // above every scenario dwell (page-load ceilings are COLD_MS 480 s / NAV_MS
      // 180 s) so only an explicit kill ends a child mid-scenario.
      PI_WEB_IDLE_TIMEOUT_MS: "600000",
      // The stub answers Pi Web's probe with the recorded 2.1.322 initialize answer.
      PI_E2E_CLAUDE_INITIALIZE_FIXTURE: INITIALIZE_FIXTURE,
  }) }));

  const { chromium } = await import("playwright");
  browser = await h.launchBrowser(() => chromium.launch());
  const viewport = { width: 1280, height: 900 };
  desktopContext = await browser.newContext({ viewport, locale: "en-US", baseURL: base });
  mobileContext = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, locale: "en-US", baseURL: base });
  // A synthetic pi catalog: pi's model selector needs options, none can collide with claude's names.
  const models = [
    { provider: "e2e", id: "model-0", name: "Model 0" },
    { provider: "e2e", id: "model-1", name: "Model 1" },
  ];
  const catalog = (route) => route.fulfill({
    json: {
      models: Object.fromEntries(models.map((model) => [`${model.provider}/${model.id}`, model.name])),
      modelList: models,
      defaultModel: { provider: "e2e", modelId: "model-0" },
      defaultThinkingLevel: "medium",
      savedDefaultThinkingLevel: null,
      thinkingLevels: {},
      thinkingLevelMaps: {},
      thinkingLevelPins: {},
    },
  });
  for (const context of [desktopContext, mobileContext]) {
    await seedStorage(context);
    await context.route(/\/api\/models(?:\?|$)/, catalog);
  }
  const desktopPage = await desktopContext.newPage();
  desktopPage.setDefaultTimeout(ACTION_MS);
  await desktopContext.tracing.start({ screenshots: true, snapshots: true });
  page = desktopPage;

  // S272-01 — criterion: an open claude session shows model, effort and compact in
  // pi's own slots/components/DOM.
  await scenario("S272-01", "An open claude session shows the model, effort and compact controls in pi's slots", async () => {
    const id = await createClaudeSession({ message: "E2E 272 controls", model: "claude-opus-5-5" });
    await openSession(id);
    const bar = bottomBar();
    assert.ok(await bar.locator(".model-selector").count() > 0, "the claude composer has no model selector in pi's slot");
    assert.ok(await effortTrigger().count() > 0, "the claude composer has no reasoning-strength control (pi's reasoning button)");
    assert.ok(await compactTrigger().count() > 0, "the claude composer has no compact control");
    const menu = await openModelMenu();
    assert.deepEqual([...menu.texts].sort(), [...CLAUDE_MODELS, "claude-opus-5-5"].sort(), `the claude model selector is not claude's aliases plus the session's own model: ${menu.texts.join(", ")}`);
    await closeMenu();
    assert.equal(await triggerText(), "claude-opus-5-5", "the selector does not show the session's current model");
    const barBox = await bar.boundingBox();
    for (const [name, control] of [["model", modelTrigger()], ["effort", effortTrigger()], ["compact", compactTrigger()]]) {
      const box = await control.boundingBox();
      assert.ok(barBox && box && box.y + box.height > barBox.y - 2 && box.y < barBox.y + barBox.height + 2, `the ${name} control is not in the composer's toolbar row`);
    }
  });

  // S272-02 — criterion: a fresh claude composer offers model and effort, no
  // compaction, and the claude hint line stays as it is.
  await scenario("S272-02", "A fresh claude composer offers model and effort, not compaction", async () => {
    await openFreshComposer();
    await chooseHarness("claude");
    assert.ok(await bottomBar().locator(".model-selector").count() > 0, "the fresh claude composer offers no model selector");
    assert.ok(await effortTrigger().count() > 0, "the fresh claude composer offers no effort control before the first prompt");
    assert.equal(await compactTrigger().count(), 0, "the fresh claude composer offers a compact control although there is no conversation yet");
    assert.ok((await composer().innerText()).includes(HINT_EN), "the system-claude hint line changed or disappeared");
    await effortTrigger().click({ timeout: ACTION_MS });
    await effortOptions().first().waitFor({ state: "visible", timeout: 10_000 });
  });

  // S272-03 — criterion (owner-intended change 2026-11-04: this
  // deliberately reverses part of #272's phone placement): at <=640 px an
  // open claude session's model selector is visible in the bottom bar's left
  // cell before More controls is opened, and it is not inside the menu;
  // effort and compact are still reached only through the menu, and
  // opening/closing it changes nothing else.
  await scenario("S272-03", "A phone shows the model selector in the bottom bar and keeps effort and compact in More controls", async () => {
    const id = await createClaudeSession({ message: "E2E 272 phone", model: "claude-opus-5-5" });
    page = await mobileContext.newPage();
    page.setDefaultTimeout(ACTION_MS);
    await openSession(id);
    assert.ok(await moreTrigger().count() > 0, "no More controls button at 390 px");
    assert.ok(await modelTrigger().isVisible(), "the model selector is not visible in the bottom bar before opening More controls at 390 px");
    assert.equal(await triggerText(), "claude-opus-5-5", "the bottom bar's model selector does not show the session's current model");
    // The menu is the nearest common ancestor of effort and compact; the model
    // selector must not be inside it (on #272's layout it was).
    const modelInsideMenu = async () => page.evaluate(() => {
      const effort = document.querySelector('.chat-input-controls button[aria-label="Change reasoning level"]');
      const compact = document.querySelector('.chat-input-controls button[aria-label="Compact context"]');
      const model = document.querySelector('.chat-input-controls .model-selector > button');
      if (!effort || !compact || !model) return null;
      let menu = effort.parentElement;
      while (menu && !menu.contains(compact)) menu = menu.parentElement;
      return Boolean(menu && menu.contains(model));
    });
    assert.equal(await modelInsideMenu(), false, "the model selector is inside the More controls menu");
    // the selector now shares pi's left cell at 390 px, so the
    // composer row and the page must still fit without horizontal scrolling.
    const widths = await bottomBar().evaluate((bar) => ({
      row: bar.scrollWidth,
      rowClient: bar.clientWidth,
      page: bar.ownerDocument.documentElement.scrollWidth,
      pageClient: bar.ownerDocument.documentElement.clientWidth,
    }));
    assert.ok(widths.row <= widths.rowClient + 1, `the composer row overflows horizontally at 390 px (${widths.row} > ${widths.rowClient})`);
    assert.ok(widths.page <= widths.pageClient + 1, `the page scrolls horizontally at 390 px (${widths.page} > ${widths.pageClient})`);
    assert.ok(!(await effortTrigger().isVisible()), "the effort control is not collapsed into the More controls menu on a phone");
    assert.ok(!(await compactTrigger().isVisible()), "the compact control is not collapsed into the More controls menu on a phone");
    await moreTrigger().click({ timeout: ACTION_MS });
    for (const [name, control] of [["model", modelTrigger()], ["effort", effortTrigger()], ["compact", compactTrigger()]]) {
      assert.ok(await control.isVisible(), `the ${name} control is not visible with the More controls menu open at 390 px`);
    }
    assert.equal(await modelInsideMenu(), false, "opening the More controls menu moved the model selector inside it");
    const before = { launches: launches().length, model: await triggerText() };
    await collapseTrigger().click({ timeout: ACTION_MS });
    assert.ok(!(await effortTrigger().isVisible()), "closing the More controls menu left the effort control open");
    assert.ok(!(await compactTrigger().isVisible()), "closing the More controls menu left the compact control open");
    assert.ok(await modelTrigger().isVisible(), "closing the More controls menu hid the bottom bar's model selector");
    assert.equal(await triggerText(), before.model, "opening and closing the menu changed the session's model");
    assert.equal(launches().length, before.launches, "opening and closing the menu spawned a child");
    await page.close();
    page = desktopPage;
  });

  // S272-04 — criterion: a running turn disables the three controls and the server
  // refuses a switch that would apply mid-turn.
  await scenario("S272-04", "A running turn disables the three controls and the server refuses a mid-turn switch", async () => {
    const id = await createClaudeSession({ message: "E2E 272 running", model: "claude-opus-5-5" });
    await killChild(id);
    installHoldStub();
    await openSession(id);
    await waitFor(() => isRunning(id), 30_000, "the hold stub child was not started");
    await sendPrompt("E2E 272 hold turn");
    await waitFor(async () => (await textarea().getAttribute("placeholder").catch(() => ""))?.includes("answering"), 30_000, "the turn never started (no claude answering state)");
    for (const [name, control] of [["model", modelTrigger()], ["effort", effortTrigger()], ["compact", compactTrigger()]]) {
      assert.ok(await control.count() > 0, `the ${name} control is not rendered while a turn runs`);
      assert.ok(await control.isDisabled(), `the ${name} control is not disabled while a turn runs`);
    }
    const model = indexRecord(id)?.model;
    const stdinBefore = stdinJson().length;
    for (const command of [{ type: "set_model", model: "claude-haiku-5-5" }, { type: "compact" }]) {
      await postJson(`/api/agent/${id}`, command);
    }
    assert.equal(indexRecord(id)?.model, model, "a mid-turn model switch changed the index");
    assert.equal(stdinJson().length, stdinBefore, "a mid-turn switch reached the child's stdin");
    await bottomBar().locator('button[aria-label="Stop agent"]').first().click({ timeout: ACTION_MS });
    await waitFor(async () => !(await textarea().getAttribute("placeholder").catch(() => ""))?.includes("answering"), 30_000, "Stop did not end the turn");
    await waitFor(async () => await effortTrigger().isEnabled(), 30_000, "the three controls did not come back when the turn ended");
    const launchesAfter = launches().length;
    assert.equal(indexRecord(id)?.model, model, "ending the turn changed the model");
    assert.equal(launches().length, launchesAfter, "ending the turn spawned a child");
    restoreDefaultStub();
  });

  // S272-05 — criterion: a live switch changes the next turn's model, in the same
  // conversation, with every surface agreeing.
  await scenario("S272-05", "A live model switch takes the next turn to the new model without restarting", async () => {
    const id = await createClaudeSession({ message: "E2E 272 switch one", model: "claude-opus-5-5" });
    await openSession(id);
    await waitFor(() => launchesFor(id).length > 0, 30_000, "the claude child was not launched");
    const launchesBefore = launchesFor(id).length;
    await chooseModel("sonnet");
    await waitFor(() => /"sonnet"/.test(stdinJson()) && stdinEvents().some((event) => event.type === "control_request"), 30_000, "no claude control request carrying the new model reached the child's stdin");
    assert.equal(launchesFor(id).length, launchesBefore, "the model switch restarted the conversation (a new child was launched)");
    await waitFor(async () => (await triggerText()) === "sonnet", 30_000, "the composer's selector still shows the old model");
    const state = await getJson(`/api/agent/${id}`);
    assert.equal(state.state?.model?.id, "sonnet", "the live session state still reports the old model");
    const listed = await getJson("/api/sessions?summary=1&force=1");
    assert.equal(listed.sessions.find((session) => session.id === id)?.claude?.model, "sonnet", "the session list row still reports the old model");
    assert.equal((await getJson(`/api/sessions/${id}`)).info?.claude?.model, "sonnet", "the session detail still reports the old model");
    const stats = await postJson("/api/sessions/stats", { ids: [id] });
    assert.equal(stats.status, 200, `POST /api/sessions/stats -> ${stats.status}`);
    assert.equal(JSON.stringify(stats.body).includes("sonnet"), true, "the session stats still report the old model");
    // the turn's assistant text reaches the transcript before its
    // `result`, whose usage the server then writes into the index. Wait for that
    // write so this session's turn has settled before S272-07 snapshots the index.
    const indexedInput = () => indexRecord(id)?.usage?.input ?? 0;
    await waitFor(() => indexedInput() > 0, 30_000, "the first turn's result never reached the index");
    const inputBefore = indexedInput();
    await sendPrompt("E2E 272 switch turn");
    await waitFor(() => assistantModels(id).includes("sonnet"), 60_000, "the next turn's assistant message does not report the new model", 500);
    assert.ok(transcriptLines(id).some((entry) => entry.type === "user" && JSON.stringify(entry.message?.content ?? "").includes("E2E 272 switch one")), "the conversation history lost its earlier user message");
    await waitFor(() => indexedInput() > inputBefore, 30_000, "the switch turn's result never reached the index");
  });

  // S272-06 — criterion: with no live child the switch only updates the index and
  // the next open resumes with the new --model; a reload shows it.
  await scenario("S272-06", "With no live child the switch is recorded and the next resume carries the new --model", async () => {
    const id = await createClaudeSession({ message: "E2E 272 idle switch", model: "claude-opus-5-5" });
    // there is a conversation to resume only once claude has written
    // its transcript. The child records its launch before it reads the prompt, so on
    // a loaded host (GitHub's 2-core runner) an end right after the launch left no
    // transcript and the reopen rightly started a fresh conversation, without --resume.
    await waitFor(() => transcriptLines(id).some((entry) => entry.type === "user"), 30_000, "claude never wrote the conversation's first message to its transcript");
    // the suite's idle window no longer reaps during a scenario, so
    // this scenario ends its own child instead of waiting for an idle reap.
    await killChild(id);
    const launchesBefore = launchesFor(id).length;
    const switched = await postJson(`/api/agent/${id}`, { type: "set_model", provider: "claude", modelId: "claude-haiku-5-5", model: "claude-haiku-5-5" });
    assert.equal(switched.status, 200, `the switch with no live child was refused: ${switched.status} ${JSON.stringify(switched.body).slice(0, 200)}`);
    await waitFor(() => indexRecord(id)?.model === "claude-haiku-5-5", 30_000, "the index did not record the switch made with no live child");
    assert.equal(launchesFor(id).length, launchesBefore, "the switch itself spawned a child (it must only record the index)");
    await openSession(id);
    await waitFor(() => (launchesFor(id).at(-1)?.resumed ? flagValue(launchesFor(id).at(-1).argv, "--model") === "claude-haiku-5-5" : false), 30_000, "the reopened session did not resume with the new --model");
    assert.equal(flagValue(launchesFor(id).at(-1).argv, "--resume"), id, "the reopened session did not resume the same conversation");
    assert.equal(await triggerText(), "claude-haiku-5-5", "the composer's selector does not show the switched model after a reload");
  });

  // S272-07 — criterion: a legacy record (alias, no effort) reads as before and the
  // next write stores the new shape; reading rewrites nothing.
  await scenario("S272-07", "A pre-#272 index record reads as before and rewrites into the new shape", async () => {
    const id = "s272-legacy-sonnet";
    appendIndex([seedRecord(id, { modelAlias: "sonnet", preset: "read-only" })]);
    const before = readFileSync(indexPath(), "utf8");
    const listed = await getJson("/api/sessions?summary=1&force=1");
    const row = listed.sessions.find((session) => session.id === id);
    assert.ok(row, "the legacy index record is no longer listed");
    assert.equal(row.claude?.model, "sonnet", "the legacy sonnet alias does not read as the alias claude takes");
    assert.ok(!EFFORT_LEVELS.includes(row.claude?.effort), `a record without an effort field reads as ${JSON.stringify(row.claude?.effort)} instead of the default entry`);
    assert.equal(readFileSync(indexPath(), "utf8"), before, "reading the index rewrote the file");
    await openSession(id);
    assert.ok(await effortTrigger().count() > 0, "the legacy session offers no effort control");
    const active = page.locator('[role="option"][aria-selected="true"]');
    await effortTrigger().click({ timeout: ACTION_MS });
    await effortOptions().first().waitFor({ state: "visible", timeout: 10_000 });
    const activeLabel = (await active.first().innerText()).trim().split(/\s/)[0];
    assert.ok(!EFFORT_LEVELS.includes(activeLabel), `an older record without effort does not show the default entry as active (active: ${activeLabel})`);
    await effortOption("high").first().click({ timeout: ACTION_MS });
    await waitFor(() => indexRecord(id)?.effort === "high", 30_000, "picking a level did not write the new effort into the record");
    const record = indexRecord(id);
    assert.equal(record.model, "sonnet", "the next index write did not store the model name");
    assert.ok(!("modelAlias" in record), "the next index write kept the legacy modelAlias field");
  });

  // S272-08 — criterion: a switch claude does not accept is visible and leaves the
  // old model everywhere; a later successful switch replaces the line.
  await scenario("S272-08", "A switch claude does not accept is visible and leaves the old model", async () => {
    const id = await createClaudeSession({ message: "E2E 272 refused", model: "claude-opus-5-5" });
    await killChild(id);
    installHoldStub();
    await openSession(id);
    await waitFor(() => isRunning(id), 30_000, "the hold stub child was not started");
    await chooseModel("haiku");
    const errorLine = composer().getByText(/Could not switch model:/);
    await waitFor(async () => (await errorLine.count()) > 0, 60_000, "a switch claude did not accept showed no visible error line", 500);
    assert.ok((await errorLine.innerText()).trim().length > "Could not switch model:".length, "the switch error line carries no claude error text");
    assert.equal(indexRecord(id)?.model, "claude-opus-5-5", "a refused switch changed the index's model");
    assert.equal(await triggerText(), "claude-opus-5-5", "the selector silently claims the refused model");
    await page.goto("about:blank");
    await killChild(id);
    restoreDefaultStub();
    await openSession(id);
    await waitFor(() => isRunning(id), 30_000, "the default stub child was not started after the failed switch");
    await chooseModel("fable");
    await waitFor(() => indexRecord(id)?.model === "fable", 30_000, "the later successful switch did not reach the index");
    await waitFor(async () => (await composer().getByText(/Could not switch model:/).count()) === 0, 30_000, "the later successful switch did not clear the switch error line");
  });

  // S272-09 — criterion: exactly claude's five levels plus the default entry, one
  // active, none of off/minimal/auto, a description per entry.
  await scenario("S272-09", "The effort menu lists exactly claude's five levels plus the default entry", async () => {
    const id = await createClaudeSession({ message: "E2E 272 effort list", model: "claude-opus-5-5" });
    await openSession(id);
    assert.ok(await effortTrigger().count() > 0, "the claude composer offers no effort control");
    await effortTrigger().click({ timeout: ACTION_MS });
    await effortOptions().first().waitFor({ state: "visible", timeout: 10_000 });
    const rows = await effortOptions().evaluateAll((elements) => elements.map((element) => ({
      label: (element.textContent ?? "").trim().split(/\s/)[0],
      active: element.getAttribute("aria-selected") === "true",
      described: (element.querySelectorAll("span").length >= 2) && [...element.querySelectorAll("span")].slice(1).some((span) => (span.textContent ?? "").trim().length > 0),
    })));
    assert.equal(rows.length, 6, `the effort menu has ${rows.length} entries, not claude's five levels plus the default one: ${rows.map((row) => row.label).join(", ")}`);
    for (const level of EFFORT_LEVELS) assert.ok(rows.some((row) => row.label === level), `the effort menu is missing claude's "${level}" level`);
    for (const invented of ["off", "minimal", "auto"]) assert.ok(!rows.some((row) => row.label === invented), `the effort menu offers "${invented}", which claude does not have`);
    assert.equal(rows.filter((row) => row.active).length, 1, "the effort menu does not mark exactly one entry active");
    assert.ok(rows.every((row) => row.described), "an effort entry has no description line");
    await effortTrigger().click({ timeout: ACTION_MS });
  });

  // S272-10 — criterion: the default effort sends no --effort and a picked level
  // reaches every later spawn.
  await scenario("S272-10", "The default effort sends no --effort and a picked level reaches every later spawn", async () => {
    const defaultId = await createClaudeSession({ message: "E2E 272 default effort", model: "claude-opus-5-5" });
    const first = await waitFor(() => launchesFor(defaultId)[0], 60_000, "the default-effort session spawned no child", 500);
    assert.equal(flagValue(first.argv, "--effort"), undefined, `the default effort reached the child's argv: ${first.argv.join(" ")}`);
    await openSession(defaultId);
    await chooseEffort("high");
    await waitFor(() => indexRecord(defaultId)?.effort === "high", 30_000, "picking an effort level did not reach the index");
    await killChild(defaultId);
    // Amendment 4 (owner-approved, code-272-authored-defects): wait for a new launch, not the one before the kill.
    const beforeRespawn = launchesFor(defaultId).length;
    await sendPrompt("E2E 272 effort respawn");
    const resumed = await waitFor(() => launchesFor(defaultId).length > beforeRespawn && launchesFor(defaultId).at(-1), 60_000, "the reaped session did not spawn a resumed child", 500);
    assert.equal(flagValue(resumed.argv, "--effort"), "high", `the resumed child's argv does not carry --effort high: ${resumed.argv.join(" ")}`);
    await killChild(defaultId);
    const beforeAgain = launchesFor(defaultId).length;
    await sendPrompt("E2E 272 effort respawn two");
    const again = await waitFor(() => launchesFor(defaultId).length > beforeAgain && launchesFor(defaultId).at(-1), 60_000, "the second reaped session did not spawn a child", 500);
    assert.equal(flagValue(again.argv, "--effort"), "high", "a later spawn dropped the session's effort level");
  });

  // S272-11 — criterion: effort persists in the index, a fresh composer seeds the
  // session, and an untouched composer stays on the default entry.
  await scenario("S272-11", "Effort persists in the index and a fresh composer seeds it", async () => {
    await openFreshComposer();
    await chooseHarness("claude");
    await chooseEffort("medium");
    await sendPrompt("E2E 272 seeded effort");
    const id = await waitFor(() => readIndex().sessions?.find((record) => record.firstMessage === "E2E 272 seeded effort")?.id, 60_000, "the fresh composer with an effort created no session", 500);
    assert.equal(indexRecord(id)?.effort, "medium", "the new session's index record does not carry the fresh composer's medium effort");
    const first = await waitFor(() => launchesFor(id)[0], 60_000, "the seeded session spawned no child", 500);
    assert.equal(flagValue(first.argv, "--effort"), "medium", `the seeded session's first argv does not carry --effort medium: ${first.argv.join(" ")}`);
    await openSession(id);
    // Amendment 1 (owner-approved, code-272-authored-defects): compare the awaited count, not a Promise.
    await waitFor(async () => (await effortTrigger().count()) > 0, 30_000, "the session offers no effort control to read the persisted level");
    await effortTrigger().click({ timeout: ACTION_MS });
    await effortOptions().first().waitFor({ state: "visible", timeout: 10_000 });
    const active = (await page.locator('[role="option"][aria-selected="true"]').first().innerText()).trim();
    assert.ok(/^medium\b/.test(active), `the effort control does not show the persisted medium level (active: ${active})`);
    await effortTrigger().click({ timeout: ACTION_MS });
    for (const launch of launchesFor(id)) assert.equal(flagValue(launch.argv, "--effort"), "medium", `a spawn dropped the persisted effort: ${launch.argv.join(" ")}`);
    await openFreshComposer();
    await chooseHarness("claude");
    await sendPrompt("E2E 272 untouched effort");
    const untouched = await waitFor(() => readIndex().sessions?.find((record) => record.firstMessage === "E2E 272 untouched effort")?.id, 60_000, "a fresh claude composer left untouched created no session", 500);
    assert.ok(!EFFORT_LEVELS.includes(indexRecord(untouched)?.effort), "an untouched fresh composer created a session at a non-default effort");
    const untouchedLaunch = await waitFor(() => launchesFor(untouched)[0], 60_000, "the untouched session spawned no child", 500);
    assert.equal(flagValue(untouchedLaunch.argv, "--effort"), undefined, "an untouched fresh composer sent a --effort flag");
  });

  // S272-12 — criterion: an effort change while live applies to later turns in the
  // same conversation (runtime channel when proven, resume otherwise).
  await scenario("S272-12", "An effort change while live applies to later turns in the same conversation", async () => {
    await openFreshComposer();
    await chooseHarness("claude");
    await chooseEffort("low");
    await sendPrompt("E2E 272 live effort one");
    const id = await waitFor(() => readIndex().sessions?.find((record) => record.firstMessage === "E2E 272 live effort one")?.id, 60_000, "the fresh composer created no session", 500);
    await openSession(id);
    await waitFor(() => launchesFor(id).length > 0 && pidAlive(launchesFor(id).at(-1).pid), 30_000, "no live claude child was launched");
    const launchesBefore = launchesFor(id).length;
    const assistantsBefore = assistantModels(id).length;
    await chooseEffort("max");
    await waitFor(() => indexRecord(id)?.effort === "max", 30_000, "the live effort change did not reach the index");
    const inPlace = await waitFor(() => stdinEvents().some((event) => event.type === "control_request" && JSON.stringify(event).includes("max")), 15_000, "").catch(() => false);
    if (inPlace) assert.equal(launchesFor(id).length, launchesBefore, "the proven runtime effort channel still restarted the child");
    await sendPrompt("E2E 272 live effort two");
    if (!inPlace) {
      const resumed = await waitFor(() => launchesFor(id).slice(launchesBefore).find((launch) => flagValue(launch.argv, "--effort") === "max"), 60_000, "neither a runtime effort request nor a resume with --effort max applied the change", 500);
      assert.equal(flagValue(resumed.argv, "--resume"), id, "the effort resume started a new conversation instead of resuming");
    }
    await waitFor(() => assistantModels(id).length > assistantsBefore, 60_000, "the later turn produced no assistant message", 500);
    assert.ok(transcriptLines(id).some((entry) => entry.type === "user" && JSON.stringify(entry.message?.content ?? "").includes("E2E 272 live effort one")), "the effort change lost the conversation's earlier messages");
  });

  // S272-13 — criterion: compact sends claude's own /compact on stdin, nothing restarts.
  await scenario("S272-13", "Compact sends claude's own /compact on stdin with no instructions", async () => {
    const id = await createClaudeSession({ message: "E2E 272 compact one", model: "claude-opus-5-5" });
    await openSession(id);
    await waitFor(() => launchesFor(id).length > 0, 30_000, "the claude child was not launched");
    const launchesBefore = launchesFor(id).length;
    assert.ok(await compactTrigger().count() > 0, "the claude composer has no compact control");
    await compactTrigger().click({ timeout: ACTION_MS });
    const line = await waitFor(() => userLines().find((text) => text === "/compact"), 30_000, "no bare /compact user line reached the child's stdin");
    assert.equal(line, "/compact", `the compact command carried more than claude's own /compact: ${JSON.stringify(line)}`);
    assert.equal(launchesFor(id).length, launchesBefore, "compaction restarted the conversation");
  });

  // S272-14 — criterion: a compact_boundary produces one localized result line with
  // only claude's number.
  await scenario("S272-14", "A compact_boundary produces one localized result line with only claude's number", async () => {
    const id = await createClaudeSession({ message: "E2E 272 compact result", model: "claude-opus-5-5" });
    await openSession(id);
    assert.ok(await compactTrigger().count() > 0, "the claude composer has no compact control");
    await compactTrigger().click({ timeout: ACTION_MS });
    const line = await waitFor(async () => {
      const matches = await composer().getByText(COMPACT_RESULT_EN).allInnerTexts().catch(() => []);
      return matches.map((text) => text.trim()).find((text) => COMPACT_RESULT_EN.test(text)) ?? false;
    }, 60_000, "the composer's compact-result slot shows no 'Compacted from N tokens' line", 500);
    assert.ok(!/saved|->|→|\bafter\b/i.test(line), `the claude result line invents a number claude did not report: ${line}`);
    // Amendment 2 (owner-approved, code-272-authored-defects): pi's compact button is localized,
    // so in zh-CN it is found by its zh label, and the locale is restored whatever happens.
    await setLocale("zh-CN");
    try {
      await bottomBar().locator('button[aria-label="压缩上下文"]').first().click({ timeout: ACTION_MS });
      await waitFor(async () => (await composer().getByText(COMPACT_RESULT_ZH_CN).count()) > 0, 60_000, "the zh-CN compact result line is not 「已压缩：压缩前 N tokens」", 500);
      const zh = (await composer().getByText(COMPACT_RESULT_ZH_CN).first().innerText()).trim();
      assert.ok(!/节省|saved|->/.test(zh), `the zh-CN result line invents a number claude did not report: ${zh}`);
    } finally {
      await setLocale("en");
    }
  });

  // S272-15 — criterion: compaction is a guarded turn, Stop works during it, and a
  // reload shows the marker with the pre-compaction messages still visible.
  await scenario("S272-15", "Compaction is a guarded turn, Stop works, and a reload shows the marker", async () => {
    const done = await createClaudeSession({ message: "E2E 272 marker before", model: "claude-opus-5-5" });
    await openSession(done);
    assert.ok(await compactTrigger().count() > 0, "the claude composer has no compact control");
    await compactTrigger().click({ timeout: ACTION_MS });
    await waitFor(() => transcriptLines(done).some((entry) => entry.type === "custom" && entry.customType === "compaction") || transcriptLines(done).some((entry) => entry.subtype === "compact_boundary"), 60_000, "claude's transcript recorded no compaction boundary", 500);
    await openSession(done);
    await waitFor(async () => (await page.getByText("compaction", { exact: true }).count()) > 0, 30_000, "a reload does not show the compaction marker in the history");
    assert.ok((await page.locator(".chat-input-textarea").count()) > 0, "the reloaded session has no composer");
    assert.ok((await page.getByText("E2E 272 marker before", { exact: false }).count()) > 0, "the message before the compaction is no longer visible");
    const stopped = await createClaudeSession({ message: "E2E 272 stop compact", model: "claude-opus-5-5" });
    await killChild(stopped);
    installHoldStub();
    await openSession(stopped);
    await waitFor(() => isRunning(stopped), 30_000, "the hold stub child was not started");
    assert.ok(await compactTrigger().count() > 0, "the claude composer has no compact control");
    await compactTrigger().click({ timeout: ACTION_MS });
    await waitFor(async () => (await stopCompactTrigger().count()) > 0, 30_000, "compaction never became a running turn with a Stop control");
    // Amendment 3 (owner-approved, code-272-authored-defects): Send sits in the textarea row, not the bottom bar.
    assert.ok(await composer().locator('button[aria-label="Send"]').first().isDisabled(), "Send is not disabled while a compaction runs");
    assert.ok((await page.getByText("E2E 272 stop compact", { exact: false }).count()) > 0, "the history is not visible while the compaction runs");
    await stopCompactTrigger().click({ timeout: ACTION_MS });
    await waitFor(async () => (await stopCompactTrigger().count()) === 0, 30_000, "Stop did not end the compaction turn");
    assert.equal(await page.getByText("compaction", { exact: true }).count(), 0, "a stopped compaction invented a summary in the history");
    const before = transcriptLines(stopped).length;
    await page.goto("about:blank");
    await killChild(stopped);
    restoreDefaultStub();
    await openSession(stopped);
    await sendPrompt("E2E 272 after stop");
    await waitFor(() => transcriptLines(stopped).length > before, 60_000, "the composer did not take the next message after a stopped compaction", 500);
  });

  // S272-16 — criterion: a claude id accepts the compact command and still refuses
  // every other pi-only command.
  await scenario("S272-16", "A claude id accepts compact and still refuses every other pi-only command", async () => {
    const id = await createClaudeSession({ message: "E2E 272 commands", model: "claude-opus-5-5" });
    await openSession(id);
    await waitFor(() => launchesFor(id).length > 0, 30_000, "the claude child was not launched");
    // Amendment 5 (owner-approved, code-272-authored-defects): the creation prompt's turn ends first.
    await waitFor(async () => (await getJson(`/api/agent/${encodeURIComponent(id)}`)).state?.isStreaming === false, 60_000, "the creation prompt's turn never ended", 400);
    const compact = await postJson(`/api/agent/${id}`, { type: "compact" });
    assert.equal(compact.status, 200, `a claude id refused the compact command: ${compact.status} ${JSON.stringify(compact.body).slice(0, 200)}`);
    const launchesBefore = launches().length;
    const stdinBefore = stdinJson().length;
    for (const type of piOnlyCommands) {
      const refused = await postJson(`/api/agent/${id}`, { type, message: "x", tools: [] });
      assert.equal(refused.status, 400, `a claude id accepted the pi-only command ${type}: ${refused.status}`);
    }
    assert.equal(launches().length, launchesBefore, "a refused pi-only command spawned a child");
    assert.equal(stdinJson().length, stdinBefore, "a refused pi-only command reached the child's stdin");
  });

  // S272-17 — criterion: the fixed-for-this-session line and its key are gone and no
  // harness selector appears on an open claude session.
  await scenario("S272-17", "The fixed-for-this-session line and its key are gone", async () => {
    const id = await createClaudeSession({ message: "E2E 272 fixed line", model: "claude-sonnet-5-5" });
    await openSession(id);
    const text = await composer().innerText();
    assert.ok(!/fixed for this session/i.test(text), `an open claude session still shows the fixed-for-this-session line: ${text.slice(-300)}`);
    assert.equal(await page.locator(".claude-fixed-line").count(), 0, "the fixed-line element is still rendered");
    assert.equal(await harnessControl(), null, "an open claude session still offers a harness selector");
    for (const locale of ["en", "zh-CN", "zh-TW"]) {
      const source = readFileSync(join(root, "lib/i18n/messages", `${locale}.ts`), "utf8");
      assert.ok(!source.includes("claude.fixedLine"), `the claude.fixedLine key is still in the ${locale} locale`);
    }
  });

  // S272-18 — criterion: the docs answer the "configured" question in plain language
  // and note that claude sessions have no such control.
  await scenario("S272-18", "The docs answer the configured question in plain language", async () => {
    const tools = readFileSync(join(root, "docs/agents/tools.md"), "utf8");
    const opening = tools.slice(0, 2_000);
    assert.ok(/configured/.test(opening), "docs/agents/tools.md does not open with the `configured` answer");
    assert.ok(/defaultTools/.test(opening), "the opening does not name pi's `defaultTools`");
    assert.ok(/settings\.json/.test(opening), "the opening does not name pi's settings.json");
    assert.ok(/no tool override|sends no[^.]{0,40}override|without (a )?tool override/i.test(opening), "the opening does not say that `configured` sends no tool override");
    assert.ok(/built-in default list|built-in default/i.test(opening), "the opening does not say what no written defaultTools means");
    const claude = readFileSync(join(root, "docs/agents/claude-sessions.md"), "utf8");
    assert.ok(/configured/.test(claude), "docs/agents/claude-sessions.md does not mention `configured`");
    assert.ok(/(no|not)[^.]{0,60}tool[^.]{0,20}preset/i.test(claude), "docs/agents/claude-sessions.md does not say claude sessions have no pi-web tool preset");
  });

  // S272-19 — criterion: the three scrubbed real-2.1.312 recordings exist, are
  // scrubbed, and wire the stub's replay at the spec's falsifiable points.
  await scenario("S272-19", "The three real 2.1.312 recordings exist, are scrubbed and wire the stub replay", async () => {
    const read = (path) => readFileSync(join(fixtures, path), "utf8");
    const readme = read("README.md");
    const eventsOf = (name) => read(`${name}/stream.jsonl`).split("\n").filter(Boolean).map((line) => JSON.parse(line));
    for (const name of ["model-switch", "effort", "compact"]) {
      for (const file of ["stdin.jsonl", "stream.jsonl", "transcript.jsonl"]) {
        assert.ok(existsSync(join(fixtures, name, file)), `the real 2.1.312 ${name} recording has no ${file}`);
        assert.ok(!/sk-ant|ANTHROPIC_API_KEY\s*[:=]\s*["']?sk-/.test(read(`${name}/${file}`)), `the ${name}/${file} recording is not scrubbed`);
      }
      assert.ok(new RegExp(name).test(readme), `the README does not enumerate the ${name} recording`);
    }
    const switchEvents = eventsOf("model-switch");
    assert.ok(switchEvents.some((event) => event.type === "control_response" && /success/.test(JSON.stringify(event))), "the model-switch recording holds no accepted control response");
    assert.ok(switchEvents.some((event) => event.type === "control_response" && /check_failed/.test(JSON.stringify(event))), "the model-switch recording holds no claude refusal (check_failed)");
    assert.ok(switchEvents.some((event) => event.type === "assistant" && event.message?.model === "claude-sonnet-5-5"), "the recorded next turn does not report the switched model");
    const compactEvents = eventsOf("compact");
    assert.ok(compactEvents.some((event) => JSON.stringify(event).includes("compact_boundary")), "the compact recording holds no compact_boundary");
    assert.ok(eventsOf("effort").some((event) => event.type === "control_response"), "the effort recording holds no accepted control response");
    for (const flag of ["--effort", "--model"]) assert.ok(readme.includes(flag), `the recordings README does not document the ${flag} argv row`);
    const stubSource = readFileSync(join(root, "e2e/claude-stub/claude.mjs"), "utf8");
    for (const knob of ["PI_E2E_CLAUDE_SWITCH_FIXTURE", "PI_E2E_CLAUDE_EFFORT_FIXTURE", "PI_E2E_CLAUDE_COMPACT_FIXTURE"]) {
      assert.ok(stubSource.includes(knob), `the e2e stub does not wire ${knob}`);
    }
  });

  // S272-20 — regression for a scenario's own dwell must not end
  // the open session's child. With the old 5 s idle window a 13 s dwell was
  // enough to reap the child, reconnect the stream after 1 s and resume it
  // twice, so the live-action scenarios read those resumes as their own
  // restarts ("3 !== 1") whenever a loaded UI step stretched the dwell. The
  // suite's idle window is now the product default (see the server env), and
  // this scenario pins it: the same dwell that failed on the old config passes.
  await scenario("S272-20", "A quiet open session is not relaunched while a scenario dwells", async () => {
    const id = await createClaudeSession({ message: "E2E 272 dwell", model: "claude-opus-5-5" });
    await openSession(id);
    const launch = await waitFor(() => launchesFor(id).find((entry) => pidAlive(entry.pid)), 30_000, "no live claude child was launched", 200);
    const launchesBefore = launchesFor(id).length;
    await delayMs(13_000);
    assert.equal(launchesFor(id).length, launchesBefore, "an open session was relaunched while the scenario did nothing but wait");
    assert.equal(pidAlive(launch.pid), true, "the idle timer reaped the open session's child during the dwell");
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  if (page) await page.screenshot({ path: join(artifacts, "claude-composer-272-failure.png") }).catch(() => {});
  if (desktopContext) await desktopContext.tracing.stop({ path: join(artifacts, "claude-composer-272-trace.zip") }).catch(() => {});
  await h.close();
  for (const launch of launches()) {
    if (pidAlive(launch.pid)) {
      try { process.kill(launch.pid, "SIGKILL"); } catch { /* already gone */ }
    }
  }
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) {
  console.log(`FAIL claude-composer-272: ${failures.length} scenario(s) failed`);
  process.exitCode = 1;
}