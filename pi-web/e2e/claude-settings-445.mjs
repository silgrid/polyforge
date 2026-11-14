// carries: components/ClaudeSettings*, app/api/claude-settings/*, lib/claude-settings*, lib/claude-probe*, lib/__fixtures__/claude-2.1.322/initialize/*, lib/__fixtures__/claude-2.1.322/hooks/*, lib/__fixtures__/claude-2.1.322/permission-mode/*, docs/agents/settings-ui.md
// — Settings › Claude: browser e2e of the page's basic operations, that they
// persist and reach new claude sessions, and the user-visible parts of AC2–AC5.
//
// Not declared, though exercised: components/SettingsPanel.tsx and lib/settings-navigation.ts
// (already declared by 3 suites), the locale tables (already 2 declarers), and
// components/ChatInput.tsx, e2e/claude-stub/claude.mjs and docs/agents/claude-sessions.md
// (full, or in the light fixture, which a ninth browser suite would push past
// 8) — lib/e2e-carries.test.mjs.
//
// The child is the PATH-injected e2e/claude-stub/claude.mjs: it answers Pi Web's probe with
// claude 2.1.322's recorded `initialize` answer (its aliases, version and capabilities;
// `PI_E2E_CLAUDE_STUB_VERSION` reports another version), opens each turn of a
// `--include-hook-events` child with the recorded `hooks/` frames, and answers
// `set_permission_mode` as claude 2.1.322 did. Plan follows the claude tool
// permissions (r3): offered on Ask in a dialog (the default), disabled with its
// reason on Deny silently, set through PUT /api/claude-permissions. Two servers run in turn on one scratch tree:
// the second reports claude 9.9.9, and its first message to an existing session is that
// session's `--resume` launch.
//
// Each scenario prints one `PASS S445-nn` marker (after the run, in order; a scenario that
// spans both servers passes only when both parts did); the suite ends with
// `PASS claude-settings-445` only when every scenario passed. Every wait polls its
// condition to a bounded deadline.
import assert from "node:assert/strict";
import { existsSync, readFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, claudeEnv, claudeScratch, createApi, createWaitUntil, createHarness, errorText, pidAlive, readJsonLines, root, seedStorage } from "./harness.mjs";

assertNoDevServer();
const kit = claudeScratch("pi-web-claude-settings-445-");
const { artifacts, scratch, agentDir, project, stubLog } = kit;

const FIXTURES = join(root, "lib/__fixtures__/claude-2.1.322");
const settingsFile = join(agentDir, "pi-web-claude-settings.json");
/** The recorded initialize answer: what the page's read-only block shows of claude. */
const recordedInitialize = readFileSync(join(FIXTURES, "initialize/stream.jsonl"), "utf8").split("\n").filter(Boolean)
  .map((line) => JSON.parse(line)).find((frame) => frame.type === "control_response").response.response;
const ALIASES = ["default", "opus", "fable", "sonnet", "haiku"];
const CUSTOM_MODEL = "claude-custom-e2e-1";
const BAD_MODEL = "-bad name";
const DEFAULT_LABEL = "Default (follows Claude Code's own settings)";
const PLAN_REASON = "Not available: plan mode needs claude tool permissions set to Ask in a dialog (Settings › General) — on Deny silently claude would refuse to leave plan mode";
const VERSION_NOTICE = /is not covered by Pi Web's fixtures/;

const h = createHarness();
let page;
let base;
let firstLoad = true;
const waitFor = createWaitUntil({ every: 200, withLast: true });
const { apiFetch } = createApi(() => base);
const stubRecords = () => readJsonLines(stubLog);
const stubLaunches = () => stubRecords().filter((record) => record.kind === "launch");
const readSettingsFile = () => (existsSync(settingsFile) ? readFileSync(settingsFile, "utf8") : null);
/** the global claude tool permissions (ask / deny), through its own route. */
async function setGlobalPermissionMode(mode) {
  const response = await apiFetch("/api/claude-permissions", { method: "PUT", body: JSON.stringify({ mode }) });
  assert.equal(response.status, 200, `PUT /api/claude-permissions ${mode} -> ${response.status} ${(await response.text()).slice(0, 200)}`);
}

// Scenario parts: a part's failure fails its scenario; the markers print after the run.
const SCENARIOS = ["S445-01", "S445-02", "S445-03", "S445-04", "S445-05", "S445-06", "S445-07"];
const ran = new Set();
const failed = new Map();
async function part(id, label, fn) {
  ran.add(id);
  try {
    await fn();
  } catch (error) {
    const text = errorText(error).split("\n")[0];
    failed.set(id, [...(failed.get(id) ?? []), `${label}: ${text}`]);
    console.log(`FAIL ${id} ${label}: ${text}`);
  }
}

const servers = {
  first: { name: "claude-settings-445", env: {} },
  // The second server's claude reports a version no fixture was recorded with.
  second: { name: "claude-settings-445-b", env: { PI_E2E_CLAUDE_STUB_VERSION: "9.9.9" } },
};
async function startServer(which) {
  const spec = servers[which];
  ({ base } = await h.startServer({
    name: spec.name,
    logFile: join(artifacts, `${spec.name}-server.log`),
    env: claudeEnv(kit, {
      PI_E2E_CLAUDE_INITIALIZE_FIXTURE: join(FIXTURES, "initialize"),
      PI_E2E_CLAUDE_HOOKS_FIXTURE: join(FIXTURES, "hooks"),
      // The product default: only the suite ends a child.
      PI_WEB_IDLE_TIMEOUT_MS: "600000",
      // The host's own value must not reach claude's environment: background tasks are judged on the version alone.
      CLAUDE_CODE_DISABLE_BACKGROUND_TASKS: "",
      ...spec.env,
    }),
  }));
  firstLoad = true;
}

async function newPage() {
  const context = await h.browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "en-US" });
  await seedStorage(context);
  // A synthetic pi catalog: the fresh composer's pi side needs options (as claude-250.mjs).
  const models = [{ provider: "e2e", id: "model-0", name: "Model 0" }];
  await context.route(/\/api\/models(?:\?|$)/, (route) => route.fulfill({
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
  }));
  const opened = await context.newPage();
  opened.setDefaultTimeout(ACTION_MS);
  return opened;
}

async function goto(path) {
  await page.goto(`${base}${path}`, { waitUntil: "domcontentloaded", timeout: firstLoad ? COLD_MS : NAV_MS });
  firstLoad = false;
}

// Settings

const dialog = () => page.locator('[role="dialog"][aria-label="Settings"]');
const sectionTabs = () => dialog().locator("nav.settings-section-tabs button");
/** The section Settings shows: the one host not hidden. */
const shownSection = () => dialog().locator(".settings-section-host:not([hidden])");
const claudeHeading = () => shownSection().getByRole("heading", { name: "Claude", exact: true });
const modeGroup = () => shownSection().getByRole("radiogroup", { name: "Default permission mode" });
const modeRadio = (text) => modeGroup().getByRole("radio").filter({ hasText: text });
const hookSwitch = () => shownSection().getByRole("switch", { name: "Show hook activity" });
const claudeCodeBlock = () => shownSection().locator("section").filter({ has: page.getByRole("heading", { name: "Claude Code", exact: true }) });
const featureRow = (name) => shownSection().getByRole("table", { name: "Pi Web's claude features" }).getByRole("row").filter({ hasText: name });

async function openSettings() {
  const button = page.getByRole("button", { name: "Settings", exact: true }).first();
  await button.waitFor({ state: "visible", timeout: NAV_MS });
  // A click before the button's handler hydrates is swallowed: click again until the dialog shows.
  await waitFor(async () => {
    if (await dialog().isVisible()) return true;
    await button.click({ timeout: ACTION_MS }).catch(() => {});
    return dialog().waitFor({ state: "visible", timeout: 3_000 }).then(() => true, () => false);
  }, NAV_MS, "Settings never opened");
}

async function openClaudeSection() {
  await openSettings();
  const tab = sectionTabs().filter({ hasText: /^Claude$/ });
  await waitFor(async () => (await tab.count()) === 1, 30_000, async () => `Settings has no Claude section tab (tabs: ${(await sectionTabs().allInnerTexts()).join(", ")})`);
  if ((await tab.getAttribute("aria-current")) !== "page") await tab.click({ timeout: ACTION_MS });
  await claudeHeading().waitFor({ state: "visible", timeout: NAV_MS });
}

/** The page once its GETs settled: the mode radios and the Claude Code block's version. */
async function waitForClaudePage(version) {
  await waitFor(async () => (await modeGroup().getByRole("radio").count()) === 3, NAV_MS, "the permission mode control never showed its three choices");
  await waitFor(async () => (await claudeCodeBlock().innerText()).includes(version), NAV_MS, async () => `the Claude Code block never showed version ${version} (it reads: ${(await claudeCodeBlock().innerText().catch(() => "")).slice(0, 300)})`);
}

const checked = async (locator) => (await locator.getAttribute("aria-checked")) === "true";

/** Clicks a control that saves, and waits for that PUT /api/claude-settings's answer. */
async function saveBy(click) {
  const put = page.waitForResponse((response) => response.url().endsWith("/api/claude-settings") && response.request().method() === "PUT", { timeout: COLD_MS });
  await click();
  const response = await put;
  assert.equal(response.status(), 200, `PUT /api/claude-settings -> ${response.status()} ${(await response.text()).slice(0, 200)}`);
}

// Composer

const textarea = () => page.locator(".chat-input-textarea").first();
async function openFreshComposer() {
  await goto(`/?cwd=${encodeURIComponent(project)}`);
  await textarea().waitFor({ state: "visible", timeout: NAV_MS });
}
async function chooseClaude() {
  await page.locator(".chat-input-controls .harness-selector > button").click({ timeout: ACTION_MS });
  const listbox = page.locator('[role="listbox"]').last();
  await listbox.waitFor({ state: "visible", timeout: 10_000 });
  await listbox.getByRole("option").filter({ hasText: /^claude$/i }).click({ timeout: ACTION_MS });
  await listbox.waitFor({ state: "detached", timeout: 10_000 });
}
async function claudeModelMenu() {
  await page.locator(".chat-input-controls .model-selector > button").click({ timeout: ACTION_MS });
  const listbox = page.locator('[role="listbox"]').last();
  await listbox.waitFor({ state: "visible", timeout: 10_000 });
  const texts = (await listbox.getByRole("option").allInnerTexts()).map((text) => text.trim()).filter(Boolean);
  await page.keyboard.press("Escape");
  await listbox.waitFor({ state: "detached", timeout: 10_000 }).catch(() => {});
  return texts;
}
async function send(text) {
  await textarea().fill(text);
  await page.getByRole("button", { name: "Send", exact: true }).first().click({ timeout: ACTION_MS });
}
const replyShown = async (text) => (await page.locator(".markdown-body").allInnerTexts()).some((shown) => shown.includes(`E2E final reply to ${text}`));

/** Creates a claude session from the fresh composer and answers its first launch, once its first turn shows. */
async function createFromComposer(text) {
  const before = stubLaunches().length;
  await openFreshComposer();
  await chooseClaude();
  await send(text);
  const launch = await waitFor(() => stubLaunches().slice(before).find((record) => !record.resumed) ?? false, 60_000, `sending "${text}" from the fresh claude composer launched no claude child`);
  await waitFor(() => replyShown(text), 60_000, `the first turn's reply to "${text}" never showed`);
  return launch;
}

const at = (argv, flag) => argv.indexOf(flag);
const valueAfter = (argv, flag) => (at(argv, flag) >= 0 ? argv[at(argv, flag) + 1] : undefined);

/** The session's own page; its composer once it shows. */
async function openSession(id) {
  await goto(`/?session=${encodeURIComponent(id)}`);
  await textarea().waitFor({ state: "visible", timeout: NAV_MS });
}

const permissionButton = () => page.locator(".chat-input-controls").getByRole("button", { name: "Permission mode", exact: true });

/** Text nodes outside the composer's status shelf that read like the hook status line. */
const hookTextOutsideShelf = () => page.evaluate(() => {
  const found = [];
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    if (/hook: (SessionStart|PreToolUse|PostToolUse)/.test(node.textContent ?? "") && !node.parentElement?.closest(".extension-status-shelf")) found.push(node.textContent);
  }
  return found;
});

h.trapInterrupt();

let hookSession = null;
let hookLaunch = null;

try {
  await startServer("first");
  h.browser = await h.launchBrowser(() => chromium.launch());
  page = await newPage();

  // S445-01 — AC1: Settings has its own Claude section, apart from pi's pages, open without a project.
  await part("S445-01", "Settings has a Claude section, separate from pi's, without a project", async () => {
    await goto("/");
    await openSettings();
    const tabs = (await sectionTabs().allInnerTexts()).map((text) => text.trim());
    assert.ok(tabs.includes("Claude"), `Settings has no Claude section (tabs: ${tabs.join(", ")})`);
    assert.equal(tabs.indexOf("Claude"), tabs.indexOf("Polyforge") - 1, `the Claude section is not right before Polyforge (tabs: ${tabs.join(", ")})`);
    const skills = sectionTabs().filter({ hasText: /^Skills$/ });
    assert.ok(await skills.isDisabled(), "the page has a project open: Skills is enabled, so this is not the no-project case");
    const tab = sectionTabs().filter({ hasText: /^Claude$/ });
    assert.ok(!(await tab.isDisabled()), "the Claude section needs a project");
    await tab.click({ timeout: ACTION_MS });
    await claudeHeading().waitFor({ state: "visible", timeout: NAV_MS });
    assert.equal(await tab.getAttribute("aria-current"), "page", "the Claude tab is not the current section");
    assert.equal(await shownSection().count(), 1, "more than one section shows beside Claude");
    const text = await shownSection().innerText();
    for (const piText of ["Appearance", "Agent language"]) assert.ok(!text.includes(piText), `the Claude section shows General's "${piText}"`);
    assert.ok(!(await shownSection().locator(".settings-general-section").filter({ hasText: "Appearance" }).count()), "General's content shows in the Claude section");
  });

  // S445-02 (first server) — AC3: claude's version and its capabilities verbatim, and Pi Web's three features with their basis;
  // approvals follow the claude tool permissions (r3): available on Ask in a dialog, not on Deny silently.
  await part("S445-02", "the version, claude's capabilities and Pi Web's features for claude 2.1.322", async () => {
    await goto("/");
    await openClaudeSection();
    await waitForClaudePage("2.1.322");
    const block = claudeCodeBlock();
    const capabilities = (await block.getByRole("list", { name: "Capabilities claude reports" }).getByRole("listitem").allInnerTexts()).map((text) => text.trim());
    assert.deepEqual(capabilities, recordedInitialize.capabilities, "the capabilities shown are not claude's own, verbatim");
    assert.equal(await shownSection().getByText(VERSION_NOTICE).count(), 0, "a recorded version (2.1.322) shows the version notice");
    const cells = async (name) => (await featureRow(name).getByRole("cell").allInnerTexts()).map((text) => text.trim());
    const approvals = await cells("Approvals");
    assert.equal(approvals[1], "Available", `approvals under the default Ask in a dialog: ${approvals.join(" | ")}`);
    assert.ok(approvals[2]?.includes("Ask in a dialog"), `approvals' basis does not name Ask in a dialog: ${approvals.join(" | ")}`);
    for (const name of ["Background tasks", "Subagent text"]) {
      const row = await cells(name);
      assert.equal(row[1], "Available", `${name}: ${row.join(" | ")}`);
      assert.ok(row[2]?.includes("2.1.322"), `${name}'s basis does not name the recorded 2.1.322: ${row.join(" | ")}`);
    }
    await setGlobalPermissionMode("deny");
    try {
      await page.reload({ waitUntil: "domcontentloaded", timeout: NAV_MS });
      await openClaudeSection();
      await waitForClaudePage("2.1.322");
      const denied = await waitFor(async () => {
        const row = await cells("Approvals");
        return row[1] === "Not available" ? row : false;
      }, 10_000, async () => `approvals are not "Not available" under Deny silently: ${(await cells("Approvals")).join(" | ")}`);
      assert.ok(denied[2]?.includes("Deny silently"), `approvals' basis does not name Deny silently: ${denied.join(" | ")}`);
    } finally {
      await setGlobalPermissionMode("ask");
    }
  });

  // S445-03 — AC2/AC6: default reads as Claude Code's own setting; plan is offered under
  // Ask in a dialog (the default) and survives a reload; on Deny silently it is
  // disabled with its reason visible and the stored plan shows as default; back on ask,
  // acceptEdits is saved, survives a reload and is the file's value.
  await part("S445-03", "the permission mode: default's label, plan under ask and disabled with its reason under deny, acceptEdits saved", async () => {
    const reopen = async () => {
      await page.reload({ waitUntil: "domcontentloaded", timeout: NAV_MS });
      await openClaudeSection();
      await waitForClaudePage("2.1.322");
    };
    await goto("/");
    await openClaudeSection();
    await waitForClaudePage("2.1.322");
    assert.equal(await modeRadio(DEFAULT_LABEL).count(), 1, `no "${DEFAULT_LABEL}" choice`);
    assert.ok(await checked(modeRadio(DEFAULT_LABEL)), "default is not the stored mode before any save");
    const plan = modeRadio("plan");
    assert.ok(!(await plan.isDisabled()), "plan is disabled under's default, Ask in a dialog");
    assert.equal(await shownSection().getByText(PLAN_REASON).count(), 0, "plan's unavailable reason shows under ask");
    await saveBy(() => plan.click({ timeout: ACTION_MS }));
    await waitFor(() => checked(plan), 10_000, "plan is not shown as saved");
    assert.equal(JSON.parse(readSettingsFile() ?? "{}").launchPermissionMode, "plan", "pi-web-claude-settings.json does not hold plan");
    await reopen();
    await waitFor(() => checked(plan), 10_000, "plan is not selected after a reload");

    await setGlobalPermissionMode("deny");
    try {
      await reopen();
      await waitFor(() => plan.isDisabled(), 10_000, "plan is offered while claude tool permissions are Deny silently");
      await waitFor(async () => (await shownSection().getByText(PLAN_REASON).count()) === 1 && (await shownSection().getByText(PLAN_REASON).isVisible()), 10_000, "plan's reason is not visible text");
      await waitFor(() => checked(modeRadio(DEFAULT_LABEL)), 10_000, "the stored plan does not show as default under deny");
      assert.ok(!(await checked(plan)), "plan shows as selected under deny");
      assert.equal(JSON.parse(readSettingsFile() ?? "{}").launchPermissionMode, "plan", "deny rewrote the stored plan ( r3 D13: it is only read as default)");
    } finally {
      await setGlobalPermissionMode("ask");
    }
    await reopen();
    await waitFor(() => checked(plan), 10_000, "the stored plan is not shown again back on ask");
    await saveBy(() => modeRadio("acceptEdits").click({ timeout: ACTION_MS }));
    await waitFor(() => checked(modeRadio("acceptEdits")), 10_000, "acceptEdits is not shown as saved");
    assert.equal(JSON.parse(readSettingsFile() ?? "{}").launchPermissionMode, "acceptEdits", "pi-web-claude-settings.json does not hold acceptEdits");
    await reopen();
    await waitFor(() => checked(modeRadio("acceptEdits")), 10_000, "acceptEdits is not selected after a reload");
    assert.ok(!(await checked(modeRadio(DEFAULT_LABEL))), "default is still selected after the reload");
  });

  // S445-04 — AC4/AC6: claude's aliases read-only, a custom name saved and offered by a new
  // composer beside them, an invalid name refused visibly with the file unchanged.
  await part("S445-04", "claude's aliases, a custom model name in the composer, an invalid name refused", async () => {
    await goto("/");
    await openClaudeSection();
    await waitForClaudePage("2.1.322");
    const aliases = (await shownSection().getByRole("list", { name: "claude's model aliases" }).locator("code").allInnerTexts()).map((text) => text.trim());
    assert.deepEqual(aliases, ALIASES, "the alias list is not claude's aliases");
    const input = shownSection().getByRole("textbox", { name: "Custom model name" });
    await input.fill(CUSTOM_MODEL);
    await saveBy(() => shownSection().getByRole("button", { name: "Add", exact: true }).click({ timeout: ACTION_MS }));
    const customList = () => shownSection().getByRole("list", { name: "Custom model names" }).locator("code");
    await waitFor(async () => (await customList().allInnerTexts()).map((text) => text.trim()).includes(CUSTOM_MODEL), 10_000, `${CUSTOM_MODEL} is not listed as a custom name`);
    assert.deepEqual(JSON.parse(readSettingsFile() ?? "{}").customModels, [CUSTOM_MODEL], "the file does not hold the custom name");
    const fileBefore = readSettingsFile();
    let puts = 0;
    const countPut = (request) => { if (request.url().endsWith("/api/claude-settings") && request.method() === "PUT") puts += 1; };
    page.on("request", countPut);
    await input.fill(BAD_MODEL);
    await shownSection().getByRole("button", { name: "Add", exact: true }).click({ timeout: ACTION_MS });
    await waitFor(async () => {
      const alerts = await shownSection().getByRole("alert").allInnerTexts();
      return alerts.some((text) => text.includes("Not a claude model name") && text.includes(BAD_MODEL));
    }, 10_000, `"${BAD_MODEL}" is not refused with a visible message`);
    page.off("request", countPut);
    assert.equal(puts, 0, "an invalid name was sent to the server");
    assert.equal(readSettingsFile(), fileBefore, "an invalid name changed the settings file");
    assert.ok(!(await customList().allInnerTexts()).map((text) => text.trim()).includes(BAD_MODEL), "the invalid name is listed");

    await openFreshComposer();
    await chooseClaude();
    const offered = await waitFor(async () => {
      const texts = await claudeModelMenu();
      return texts.includes(CUSTOM_MODEL) ? texts : false;
    }, 30_000, `the new composer's claude model menu does not offer ${CUSTOM_MODEL}`);
    for (const alias of ALIASES) assert.ok(offered.includes(alias), `the composer's menu lost the alias ${alias}: ${offered.join(", ")}`);
  });

  // S445-05 (first server) — AC2/AC5/AC6: with hook activity on, a session the composer creates
  // launches with the stored mode and the hook flag; once the settings are back, a new one has neither.
  await part("S445-05", "a new session launches with acceptEdits and --include-hook-events", async () => {
    await goto("/");
    await openClaudeSection();
    await waitForClaudePage("2.1.322");
    assert.ok(!(await checked(hookSwitch())), "hook activity is on before any save");
    await saveBy(() => hookSwitch().click({ timeout: ACTION_MS }));
    await waitFor(() => checked(hookSwitch()), 10_000, "the hook switch is not shown on after its save");
    assert.equal(JSON.parse(readSettingsFile() ?? "{}").includeHookEvents, true, "the file does not hold the hook switch");

    hookLaunch = await createFromComposer("E2E 445 hooks one");
    hookSession = hookLaunch.sessionId;
    const argv = hookLaunch.argv;
    assert.equal(valueAfter(argv, "--permission-mode"), "acceptEdits", `no --permission-mode acceptEdits: ${argv.join(" ")}`);
    assert.ok(at(argv, "--include-hook-events") >= 0, `no --include-hook-events: ${argv.join(" ")}`);
    const prompts = at(argv, "--permission-prompts");
    assert.ok(at(argv, "--permission-mode") < prompts && at(argv, "--include-hook-events") < prompts, `the flags are not before --permission-prompts: ${argv.join(" ")}`);
  });

  // S445-06 — AC5: the hook activity is one status line above the composer, never a message.
  await part("S445-06", "hook activity shows as the status line above the composer", async () => {
    assert.ok(hookSession, "no session with hook activity (S445-05)");
    await openSession(hookSession);
    const bubblesBefore = await page.locator(".markdown-body").count();
    await send("E2E 445 hooks two");
    const status = await waitFor(async () => {
      const texts = await page.locator(".extension-status-shelf").allInnerTexts();
      return texts.find((text) => /hook: \S+ · \S+/.test(text)) ?? false;
    }, 30_000, "the composer's status line never showed the hook activity");
    assert.match(status, /hook: (SessionStart|PreToolUse|PostToolUse) · /, `the status line is not a recorded hook: ${status}`);
    await waitFor(() => replyShown("E2E 445 hooks two"), 60_000, "the turn with hook activity never ended");
    assert.deepEqual(await hookTextOutsideShelf(), [], "the hook activity shows outside the status line");
    const bubbles = await page.locator(".markdown-body").allInnerTexts();
    assert.ok(!bubbles.some((text) => /PreToolUse|PostToolUse|SessionStart/.test(text)), "a message shows the hook activity");
    assert.ok(bubbles.length >= bubblesBefore, "messages went missing");
  });

  // S445-05 (continued): the settings back to default and off; a new session has neither flag.
  await part("S445-05", "settings back: a new session has neither flag", async () => {
    await goto("/");
    await openClaudeSection();
    await waitForClaudePage("2.1.322");
    await saveBy(() => modeRadio(DEFAULT_LABEL).click({ timeout: ACTION_MS }));
    await waitFor(() => checked(modeRadio(DEFAULT_LABEL)), 10_000, "default is not shown as saved");
    await saveBy(() => hookSwitch().click({ timeout: ACTION_MS }));
    await waitFor(async () => !(await checked(hookSwitch())), 10_000, "the hook switch is not shown off after its save");
    const stored = JSON.parse(readSettingsFile() ?? "{}");
    assert.equal(stored.launchPermissionMode, "default", "the file does not hold default");
    assert.equal(stored.includeHookEvents, false, "the file still holds the hook switch on");
    const launch = await createFromComposer("E2E 445 plain one");
    assert.equal(at(launch.argv, "--permission-mode"), -1, `a default session carries --permission-mode: ${launch.argv.join(" ")}`);
    assert.equal(at(launch.argv, "--include-hook-events"), -1, `a session made with hook activity off carries --include-hook-events: ${launch.argv.join(" ")}`);
  });

  // The second server: claude reports 9.9.9. Every child of the first is gone with it.
  await page.context().close();
  await h.stop();
  for (const record of stubLaunches()) {
    if (pidAlive(record.pid)) {
      try { process.kill(record.pid, "SIGKILL"); } catch { /* already gone */ }
    }
  }
  await startServer("second");
  page = await newPage();

  // S445-02 (second server): a version outside the recorded ones shows the notice.
  await part("S445-02", "claude 9.9.9 shows the version notice", async () => {
    await goto("/");
    await openClaudeSection();
    await waitForClaudePage("9.9.9");
    const notice = shownSection().getByText(VERSION_NOTICE);
    await waitFor(async () => (await notice.count()) === 1 && (await notice.isVisible()), 10_000, "claude 9.9.9 shows no visible version notice");
    assert.ok((await notice.innerText()).includes("9.9.9"), `the notice does not name the version: ${await notice.innerText()}`);
    const cells = async (name) => (await featureRow(name).getByRole("cell").allInnerTexts()).map((text) => text.trim());
    assert.equal((await cells("Approvals"))[1], "Available", "approvals changed with the version ('s ask is not gated on it)");
    assert.equal((await cells("Background tasks"))[1], "Available", "background tasks are not available on 9.9.9");
    assert.equal((await cells("Subagent text"))[1], "Available", "subagent text is not available on 9.9.9");
  });

  // S445-05 (continued): the hook session resumes with the values it was created with.
  await part("S445-05", "the session made with acceptEdits and hooks resumes with both", async () => {
    assert.ok(hookSession && hookLaunch, "no session with hook activity (S445-05)");
    const before = stubLaunches().length;
    const response = await apiFetch(`/api/agent/${encodeURIComponent(hookSession)}`, { method: "POST", body: JSON.stringify({ type: "prompt", message: "E2E 445 hooks three" }) });
    assert.equal(response.status, 200, `prompting the hook session -> ${response.status} ${(await response.text()).slice(0, 200)}`);
    const resumed = await waitFor(() => stubLaunches().slice(before).find((record) => record.resumed && record.sessionId === hookSession) ?? false, 60_000, "the hook session was not resumed");
    const expected = hookLaunch.argv.map((arg) => (arg === "--session-id" ? "--resume" : arg));
    assert.deepEqual(resumed.argv, expected, "the resume argv is not the session's first argv with --resume");
    await waitFor(async () => {
      const body = await (await apiFetch(`/api/agent/${encodeURIComponent(hookSession)}`)).json();
      return body.running === true && body.state?.isStreaming !== true;
    }, 60_000, "the resumed turn never ended");
  });

  // S445-07 — AC2: the open session's permission control switches claude's mode live; plan
  // is a choice for a session launched with Ask in a dialog.
  await part("S445-07", "switching the open session's permission mode sends set_permission_mode", async () => {
    assert.ok(hookSession, "no session with a live child (S445-05)");
    const child = stubLaunches().filter((record) => record.sessionId === hookSession).at(-1);
    assert.ok(child && pidAlive(child.pid), "the session's child is not alive");
    const switchedTo = (mode) => waitFor(() => stubRecords().find((record) => {
      if (record.kind !== "stdin" || record.pid !== child.pid) return false;
      try {
        const line = JSON.parse(record.line);
        return line.type === "control_request" && line.request?.subtype === "set_permission_mode" && line.request.mode === mode ? line : false;
      } catch {
        return false;
      }
    }) ?? false, 30_000, `the child's stdin got no set_permission_mode ${mode}`);
    const openMenu = async () => {
      await permissionButton().click({ timeout: ACTION_MS });
      const menu = page.getByRole("listbox", { name: "Permission mode" });
      await menu.waitFor({ state: "visible", timeout: 10_000 });
      return menu;
    };
    await openSession(hookSession);
    await waitFor(async () => (await permissionButton().innerText()).trim() === "acceptEdits", 30_000, async () => `the permission control does not show acceptEdits (it shows: ${await permissionButton().innerText().catch(() => "nothing")})`);
    let menu = await openMenu();
    const plan = menu.getByRole("option").filter({ hasText: "plan" });
    assert.ok(!(await plan.isDisabled()), "plan is not offered to a session launched with Ask in a dialog");
    assert.ok(!(await plan.innerText()).includes(PLAN_REASON), `plan's unavailable reason shows under ask: ${await plan.innerText()}`);
    await plan.click({ timeout: ACTION_MS });
    await switchedTo("plan");
    await waitFor(async () => (await permissionButton().innerText()).trim() === "plan", 30_000, "the permission control does not show plan after the switch");
    menu = await openMenu();
    await menu.getByRole("option").filter({ hasText: DEFAULT_LABEL }).click({ timeout: ACTION_MS });
    await switchedTo("default");
    await waitFor(async () => (await permissionButton().innerText()).trim() === DEFAULT_LABEL, 30_000, "the permission control does not show default after the switch");
  });
} catch (error) {
  console.log(`FAIL claude-settings-445 setup: ${errorText(error).split("\n")[0]}`);
  failed.set("setup", [errorText(error)]);
} finally {
  if (page && failed.size) await page.screenshot({ path: join(artifacts, "claude-settings-445-failure.png") }).catch(() => {});
  await h.close();
  for (const record of stubLaunches()) {
    try { process.kill(record.pid, "SIGKILL"); } catch { /* already gone */ }
  }
  rmSync(scratch, { recursive: true, force: true });
}

for (const id of SCENARIOS) {
  if (!ran.has(id)) console.log(`FAIL ${id}: not run`);
  else if (!failed.has(id)) console.log(`PASS ${id}`);
}
if (failed.size || SCENARIOS.some((id) => !ran.has(id))) {
  console.log(`FAIL claude-settings-445: ${[...failed.keys()].join(", ") || "scenarios not run"}`);
  process.exitCode = 1;
} else {
  console.log("PASS claude-settings-445");
}
