// carries: components/HarnessGeneralSettings*, components/GeneralSettings*, components/HarnessEnvironmentSettings*, components/HarnessEffectiveSettings*, components/UnifiedSetting*, components/HarnessApplyTo*
// carries: components/HarnessPermissionSettings*, components/ClaudePermissionSettings*, lib/harness-permissions-view*, lib/harness-settings-view*, lib/harness/diagnostics*, app/api/harness/*
// — polyforge#149 (#85 85a1b, the settings skeleton) and polyforge#150
// (#85 85a4, the permission page): the 10-tab rail (九个新 tab 加标「旧版」的
// 原「模型」tab；原「Claude」tab 已删), General's left column (界面与对话 /
// 环境 / 生效值与来源), the account / models placeholders, the permission
// page (审批模式 / 沙箱 / 项目信任 / 规则: the unified blocks' one-write-many-reads,
// pi's 不做审批 / 无沙箱 notes, claude's own sections, the Pi Web approval
// channel and the rules editor) and the migrated old navigation values
// (claude → 权限, models → 新「模型」页). The harness adapter APIs are mocked
// at the browser (context.route): this suite pins the UI contract and never
// runs a real native diagnostic command or write; the adapter, the write
// route and the migration are pinned by lib/harness/*.test.mjs,
// app/api/harness/route.test.mjs, lib/claude-migration.test.mjs and
// e2e/claude-permission-442.mjs (the real routes).
//
// Not declared, though exercised: components/SettingsPanel.tsx and
// lib/settings-navigation.ts (already declared by 3 suites) —
// lib/e2e-carries.test.mjs.
//
// Each scenario prints one `PASS S85-nn` marker; the suite ends with
// `PASS harness-settings-85` only when every scenario passed. Every wait
// polls its condition to a bounded deadline; no fixed sleep.
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createHarness, createWaitUntil, seedStorage } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-harness-settings-85-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
mkdirSync(agentDir, { recursive: true });

const h = createHarness({
  timeout: 30_000,
  coldFirst: true,
  errors: ["pageerror", "console", "5xx"],
  server(name) {
    return { name, logFile: join(work, `server-${name}.log`), env: { HOME: home, PI_CODING_AGENT_DIR: agentDir } };
  },
});

// ---- the mocked adapter contract (the shapes 85a1's routes answer) ----
const HARNESS_LIST = {
  harnesses: [
    { id: "pi", version: "1.5.0", versionKnown: true, knownRange: null, versionReason: null, writable: true, capabilities: { id: "pi" } },
    { id: "claude", version: "2.1.322", versionKnown: true, knownRange: null, versionReason: null, writable: true, capabilities: { id: "claude" } },
  ],
};
const ENV_PI = { harness: "pi", concept: "env", data: { target: "pi", setKeys: ["ANTHROPIC_API_KEY", "GEMINI_API_KEY"] } };
const ENV_CLAUDE = { harness: "claude", concept: "env", data: { target: "claude", setKeys: ["ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_EXTRA_BODY"] } };
const EFFECTIVE_PI = {
  harness: "pi", concept: "effective-config",
  data: { fields: {
    model: { value: "route-model-85", sourceScope: "user", sourcePath: join(agentDir, "settings.json"), inferred: true },
    "reasoning-effort": { value: "low", sourceScope: "project", sourcePath: join(work, "project", ".pi", "settings.json"), inferred: true },
  } },
};
const EFFECTIVE_CLAUDE = {
  harness: "claude", concept: "effective-config",
  data: { fields: {
    model: { value: "claude-sonnet-5-5", sourceScope: "project", sourcePath: join(work, "project", ".claude", "settings.json"), inferred: true },
    "pi-web:launch-permission-mode": { value: "acceptEdits", sourceScope: "pi-web", sourcePath: join(agentDir, "pi-web-claude-settings.json"), inferred: true },
  } },
};
const DIAG_CLAUDE = {
  harness: "claude",
  diagnostics: { available: true, command: ["claude", "doctor"], lines: ["claude doctor: installation healthy"], truncated: false, timedOut: false },
};
const DIAG_PI = {
  harness: "pi",
  diagnostics: { available: false, reason: "pi has no fixed safe diagnostic command yet" },
};
const CLAUDE_REPORT = { available: true, models: [{ value: "default", displayName: "Default", source: "alias" }], version: "2.1.322", versionRecorded: true, recordedVersions: ["2.1.322"], capabilities: ["hooks", "ui_surface_v1"], features: [
  { id: "approvals", available: true, basis: "host-prompts" },
  { id: "backgroundTasks", available: true, basis: "recorded" },
  { id: "subagentText", available: true, basis: "recorded" },
] };

// The permission page's reads (85a4): the compare's four fields and the two
// harnesses' permission payloads. The trust field answers as a project would
// (pi trusted, claude not) so the unify flow is pinnable without a project.
const NULL_SOURCES = { defaultMode: null, sandbox: null, rules: null, denyInsteadOfPrompting: null };
const COMPARE = {
  results: [
    { field: "reasoning-effort", status: "consistent", values: { pi: { supported: true, neutral: "low", raw: "low" }, claude: { supported: true, neutral: "low", raw: "low" } } },
    { field: "approval-mode", status: "unsupported", values: { pi: { supported: false, neutral: null, raw: null }, claude: { supported: true, neutral: "ask", raw: "default" } } },
    { field: "sandbox", status: "unsupported", values: { pi: { supported: false, neutral: null, raw: null }, claude: { supported: true, neutral: "on", raw: "on" } } },
    { field: "project-trust", status: "inconsistent", values: { pi: { supported: true, neutral: "trusted", raw: "trusted" }, claude: { supported: true, neutral: "untrusted", raw: "untrusted" } } },
  ],
};
const PERMISSION_PI = {
  harness: "pi", concept: "permission",
  data: { approvalMode: { supported: false, neutral: null, raw: null }, projectTrust: { trusted: true }, sandbox: { supported: false, enabled: null, autoAllowBashIfSandboxed: null, otherKeys: [] }, rules: null, denyInsteadOfPrompting: null, sources: NULL_SOURCES },
};
const PERMISSION_CLAUDE = {
  harness: "claude", concept: "permission",
  data: {
    approvalMode: { supported: true, neutral: "ask", raw: "default" },
    projectTrust: { trusted: false },
    sandbox: { supported: true, enabled: true, autoAllowBashIfSandboxed: false, otherKeys: ["failIfUnavailable"] },
    rules: { allow: ["WebSearch"], ask: [], deny: ["Bash(rm:*)"] },
    denyInsteadOfPrompting: false,
    sources: { defaultMode: { scope: "user", path: join(agentDir, "claude-user-settings.json") }, sandbox: { scope: "user", path: join(agentDir, "claude-user-settings.json") }, rules: null, denyInsteadOfPrompting: null },
  },
};
/** The permission PUTs the browser sent (each scenario clears and reads it). */
const permissionPuts = [];

/** Answers the harness/claude adapter routes the settings skeleton reads; everything else hits the server. */
async function mockAdapterApis(context) {
  // The app's fresh-page flow would otherwise select a default project
  // asynchronously (POST /api/default-cwd → POST /api/cwd/validate): a cwd
  // landing mid-scenario re-fires the panes' reads with ?cwd=, racing every
  // no-project assertion (v1 round 1: S85-09's alert was cleared by exactly
  // that). The suite is a no-project suite; the flow answers no cwd.
  await context.route("**/api/default-cwd", (route) => route.fulfill({ status: 200, json: {} }));
  await context.route(/\/api\/harness(?:\/|\?|$)/, async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "PUT") {
      // The permission page's one write path: recorded, answered as written.
      if (path === "/api/harness/pi/permission" || path === "/api/harness/claude/permission") {
        permissionPuts.push({ path, body: route.request().postDataJSON() });
        await route.fulfill({ status: 200, json: { harness: path.split("/")[3], concept: "permission", result: { status: "written", summary: "mocked" } } });
        return;
      }
      await route.fulfill({ status: 400, json: { error: "write-refused", message: "not a permission write path" } });
      return;
    }
    const body = path === "/api/harness" ? HARNESS_LIST
      : path === "/api/harness/compare" ? COMPARE
      : path === "/api/harness/pi/permission" ? PERMISSION_PI
      : path === "/api/harness/claude/permission" ? PERMISSION_CLAUDE
      : path === "/api/harness/pi/env" ? ENV_PI
      : path === "/api/harness/claude/env" ? ENV_CLAUDE
      : path === "/api/harness/pi/effective-config" ? EFFECTIVE_PI
      : path === "/api/harness/claude/effective-config" ? EFFECTIVE_CLAUDE
      : path === "/api/harness/pi/diagnose" ? DIAG_PI
      : path === "/api/harness/claude/diagnose" ? DIAG_CLAUDE
      : null;
    if (body === null) await route.fulfill({ status: 404, json: { error: "unknown-harness-path" } });
    else await route.fulfill({ status: 200, json: body });
  });
  await context.route("**/api/models-config", (route) => route.fulfill({ status: 200, json: { providers: {} } }));
  await context.route("**/api/claude", (route) => route.fulfill({ status: 200, json: CLAUDE_REPORT }));
  await context.route("**/api/claude-permissions", (route) => route.fulfill({ status: 200, json: { mode: "ask", push: true, includeHookEvents: false, session: null } }));
}

const TABS = [
  "General", "Account", "Models", "Permissions", "Skills", "MCP",
  "Sub-agents", "Plugins", "Polyforge", "Models (legacy)",
];

/** A label as an anchored regex, its punctuation escaped (the legacy labels carry parens). */
const labelRe = (label) => new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);

const T = {
  S1: "S85-01: Settings shows 9 new tabs plus the legacy-marked Models one, in the settled order",
  S2: "S85-02: General is a left-column layout whose three items switch the detail pane",
  S3: "S85-03: a phone walks General as list → detail → back",
  S4: "S85-04: the account / models placeholders open; the permission page is real",
  S5: "S85-05: the legacy Models page stays reachable and working",
  S6: "S85-06: the environment pane lists only variable names, grouped per harness",
  S7: "S85-07: effective values carry the Inferred mark and their source; native diagnostics are a collapsed block per harness",
  S8: "S85-08: stored old navigation values land on the new pages",
  S9: "S85-09: a failed harness-list read is a visible reason in both panes, never a contradictory empty registry",
  S10: "S85-10: the permission page's left column is 审批模式 / 沙箱 / 项目信任 / 规则, its approval pane unified and claude-sectioned",
  S11: "S85-11: the unified approval write reaches claude's native mode (Q1 A), the extra values its own radio",
  S12: "S85-12: the sandbox pane writes claude's sandbox keys, pi answering 无沙箱",
  S13: "S85-13: the trust pane shows each harness's own decision and 统一成当前值 writes the differing one",
  S14: "S85-14: the rules pane edits claude's allow / ask / deny lists with no unified block",
  S15: "S85-15: General has no claude permission controls; its hook switch stays",
};
const failures = new Map(Object.values(T).map((title) => [title, []]));
const ran = new Set();

async function part(id, label, fn) {
  const title = T[id];
  ran.add(title);
  try {
    await fn();
    console.log(`PASS ${id}`);
  } catch (error) {
    failures.get(title).push(`${label}: ${String(error?.message ?? error).split("\n")[0]}`);
    console.log(`FAIL ${id} ${label}: ${error?.message ?? error}`);
  }
}

const waitFor = createWaitUntil({ every: 250 });
const dialog = (page) => page.getByRole("dialog", { name: "Settings", exact: true });
const sectionTabs = (page) => dialog(page).locator("nav.settings-section-tabs button.settings-section-tab");
const sectionHost = (page, id) => dialog(page).locator(`.settings-section-host[data-settings-section="${id}"]:not([hidden])`);
const tab = (page, label) => sectionTabs(page).filter({ hasText: typeof label === "string" ? labelRe(label) : label });

let coldSettings = true;

async function newPage(viewport = { width: 1280, height: 900 }, storage = null) {
  const context = await h.browser.newContext({ viewport, locale: "en-US" });
  await seedStorage(context, storage ?? {});
  await mockAdapterApis(context);
  const page = await context.newPage();
  page.setDefaultTimeout(30_000);
  return page;
}

async function goto(page, path = "/") {
  await page.goto(`${h.base}${path}`, { waitUntil: "domcontentloaded", timeout: coldSettings ? COLD_MS : NAV_MS });
  coldSettings = false;
}

/** The Settings button is server-rendered; a click before hydration is dropped, so wait for React's own props first. */
async function clickSettingsButton(page) {
  const button = page.getByRole("button", { name: "Settings", exact: true }).first();
  await button.waitFor({ state: "visible", timeout: COLD_MS });
  await page.waitForFunction(() => {
    const candidate = [...document.querySelectorAll("button")]
      .find((entry) => entry.getAttribute("aria-label") === "Settings" || entry.textContent.trim() === "Settings");
    return !!candidate && Object.keys(candidate).some((key) => key.startsWith("__reactFiber$") || key.startsWith("__reactProps$"));
  }, null, { timeout: COLD_MS });
  await button.click({ timeout: ACTION_MS });
}

async function openSettings(page) {
  await goto(page);
  await clickSettingsButton(page);
  await dialog(page).waitFor({ state: "visible", timeout: COLD_MS });
  return dialog(page);
}

const generalSidebarItems = (page) => sectionHost(page, "general").locator(".config-sidebar-item");
const generalPane = (page, pane) => sectionHost(page, "general").locator(`[data-general-pane="${pane}"]`);

h.trapInterrupt();

try {
  await h.startServer("settings-85");
  h.browser = await h.launchBrowser(() => chromium.launch());

  // S85-01 — AC1: 9 new tabs + 2 legacy-marked, order and no-project behaviour.
  await part("S1", "tabs", async () => {
    const page = await newPage();
    await openSettings(page);
    const labels = (await sectionTabs(page).allInnerTexts()).map((text) => text.trim());
    assert.deepEqual(labels, TABS, `tabs (got: ${labels.join(", ")})`);
    // Skills/Agents/Plugins need a project; the rest open without one.
    for (const name of ["Skills", "Sub-agents", "Plugins"]) {
      assert.ok(await tab(page, name).isDisabled(), `${name} should need a project`);
    }
    for (const name of ["General", "Account", "Models", "Permissions", "MCP", "Polyforge", "Models (legacy)"]) {
      assert.ok(!(await tab(page, name).isDisabled()), `${name} should open without a project`);
    }
    // The one legacy tab is marked as such in its own label; the old Claude
    // tab is gone (85a4), its values living on the permission page.
    assert.match(labels[9], /\(legacy\)/, "the old Models tab is not marked legacy");
    assert.ok(!labels.some((label) => /Claude \(legacy\)/.test(label)), "the old Claude tab is still in the rail");
    await page.context().close();
  });

  // S85-02 — AC1: General's left column and its three switchable items.
  await part("S2", "general left column", async () => {
    const page = await newPage();
    const opened = await openSettings(page);
    const items = generalSidebarItems(page);
    await items.first().waitFor({ state: "visible", timeout: ACTION_MS });
    const labels = (await items.allInnerTexts()).map((text) => text.trim());
    assert.deepEqual(labels, ["Interface & conversation", "Environment", "Effective values & sources"], `General's left column (got: ${labels.join(", ")})`);
    assert.equal(await items.first().getAttribute("aria-current"), "page", "the first item is not the default pane");
    // The default detail is the old General content.
    await generalPane(page, "conversation").getByRole("heading", { name: "Appearance", exact: true }).waitFor({ state: "visible", timeout: ACTION_MS });
    // Switching panes swaps the detail.
    await items.filter({ hasText: "Environment" }).click({ timeout: ACTION_MS });
    await generalPane(page, "environment").waitFor({ state: "visible", timeout: ACTION_MS });
    await generalPane(page, "conversation").waitFor({ state: "hidden", timeout: ACTION_MS });
    await items.filter({ hasText: "Effective values & sources" }).click({ timeout: ACTION_MS });
    await generalPane(page, "effective").waitFor({ state: "visible", timeout: ACTION_MS });
    await items.filter({ hasText: "Interface & conversation" }).click({ timeout: ACTION_MS });
    await generalPane(page, "conversation").getByRole("heading", { name: "Appearance", exact: true }).waitFor({ state: "visible", timeout: ACTION_MS });
    assert.equal(await opened.count(), 1);
    await page.context().close();
  });

  // S85-03 — AC1: the phone walks General as list → detail → back (the shared split view).
  await part("S3", "phone levels", async () => {
    const page = await newPage({ width: 320, height: 568 });
    await goto(page);
    // The phone's sidebar starts folded; its toggle and the Settings button
    // both need hydration first (a pre-hydration click is swallowed).
    const sidebar = page.getByRole("button", { name: "Show sidebar", exact: true });
    await page.waitForFunction(() => [...document.querySelectorAll("button")]
      .some((entry) => (entry.getAttribute("aria-label") ?? entry.textContent.trim()) === "Show sidebar"
        && Object.keys(entry).some((key) => key.startsWith("__reactFiber$") || key.startsWith("__reactProps$"))), null, { timeout: COLD_MS });
    if (await sidebar.count()) await sidebar.click({ timeout: ACTION_MS });
    await clickSettingsButton(page);
    const home = page.locator(".settings-phone-home");
    await home.waitFor({ state: "visible", timeout: COLD_MS });
    await home.getByRole("button", { name: "General", exact: true }).click({ timeout: ACTION_MS });
    // The section level shows the three-item list, not the detail.
    const items = generalSidebarItems(page);
    await items.first().waitFor({ state: "visible", timeout: ACTION_MS });
    assert.ok(!(await generalPane(page, "conversation").isVisible()), "the phone's section level shows the detail pane");
    await items.filter({ hasText: "Interface & conversation" }).click({ timeout: ACTION_MS });
    await generalPane(page, "conversation").getByRole("heading", { name: "Appearance", exact: true }).waitFor({ state: "visible", timeout: ACTION_MS });
    const back = page.locator(".settings-phone-back");
    await back.waitFor({ state: "visible", timeout: ACTION_MS });
    await back.click({ timeout: ACTION_MS });
    await items.first().waitFor({ state: "visible", timeout: ACTION_MS });
    await generalPane(page, "conversation").waitFor({ state: "hidden", timeout: ACTION_MS });
    await page.context().close();
  });

  // S85-04 — AC1: the account / models placeholders open; the permission page is real (85a4).
  await part("S4", "placeholders", async () => {
    const page = await newPage();
    await openSettings(page);
    for (const [name, id] of [["Account", "account"], ["Models", "models"]]) {
      await tab(page, new RegExp(`^${name}$`)).click({ timeout: ACTION_MS });
      const host = sectionHost(page, id);
      await host.waitFor({ state: "visible", timeout: ACTION_MS });
      assert.match(await host.innerText(), /Placeholder/i, `the ${name} page shows no placeholder text`);
    }
    // The permission page is 85a4's: its left column is the settled four.
    await tab(page, /^Permissions$/).click({ timeout: ACTION_MS });
    const permission = sectionHost(page, "permission");
    await permission.locator(".config-sidebar-item").first().waitFor({ state: "visible", timeout: ACTION_MS });
    assert.deepEqual((await permission.locator(".config-sidebar-item").allInnerTexts()).map((text) => text.trim()),
      ["Approval mode", "Sandbox", "Project trust", "Rules"], "the permission page's left column");
    await page.context().close();
  });

  // S85-05 — AC1: the legacy Models page stays reachable and working (the
  // legacy Claude page is deleted by 85a4; S1 pins its absence from the rail).
  await part("S5", "legacy pages", async () => {
    const page = await newPage();
    await openSettings(page);
    await tab(page, /^Models \(legacy\)$/).click({ timeout: ACTION_MS });
    const models = sectionHost(page, "legacy-models");
    await models.getByRole("button", { name: "Add provider", exact: true }).waitFor({ state: "visible", timeout: COLD_MS });
    await page.context().close();
  });

  // S85-06 — AC2: the environment pane lists only variable names, per harness.
  await part("S6", "environment names only", async () => {
    const page = await newPage();
    await openSettings(page);
    await generalSidebarItems(page).filter({ hasText: "Environment" }).click({ timeout: ACTION_MS });
    const pane = generalPane(page, "environment");
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    const text = await waitFor(async () => {
      const body = await pane.innerText();
      return body.includes("ANTHROPIC_API_KEY") && body.includes("CLAUDE_CODE_EXTRA_BODY") ? body : false;
    }, ACTION_MS, "the environment pane never listed the mocked names");
    for (const name of ["ANTHROPIC_API_KEY", "GEMINI_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_EXTRA_BODY"]) {
      assert.ok(text.includes(name), `the environment pane does not list ${name}`);
    }
    for (const harness of ["pi", "claude"]) {
      await pane.locator(`[data-harness="${harness}"]`).waitFor({ state: "visible", timeout: ACTION_MS });
    }
    assert.match(text, /Only variable names are shown/, "the names-only note is missing");
    await page.context().close();
  });

  // S85-07 — AC2/AC6: Inferred marks and sources; native diagnostics as a collapsed block.
  await part("S7", "effective values and diagnostics", async () => {
    const page = await newPage();
    await openSettings(page);
    await generalSidebarItems(page).filter({ hasText: "Effective values & sources" }).click({ timeout: ACTION_MS });
    const pane = generalPane(page, "effective");
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    const text = await waitFor(async () => {
      const body = await pane.innerText();
      return body.includes("route-model-85") ? body : false;
    }, ACTION_MS, "the effective pane never showed a mocked value");
    assert.match(text, /Inferred/, "the Inferred mark is missing");
    assert.ok(text.includes(join(agentDir, "settings.json")), "the value's source path is missing");
    // Diagnostics: one collapsed block per harness; expanding loads the output once.
    const claudeBlock = pane.locator("details.settings-harness-diagnostics[data-harness=\"claude\"]");
    await claudeBlock.waitFor({ state: "visible", timeout: ACTION_MS });
    assert.equal(await claudeBlock.getAttribute("open"), null, "the diagnostics block starts open");
    await claudeBlock.locator("summary").click({ timeout: ACTION_MS });
    await claudeBlock.getByText("claude doctor: installation healthy").waitFor({ state: "visible", timeout: ACTION_MS });
    const piBlock = pane.locator("details.settings-harness-diagnostics[data-harness=\"pi\"]");
    await piBlock.locator("summary").click({ timeout: ACTION_MS });
    await piBlock.getByText(/no fixed safe diagnostic command/).waitFor({ state: "visible", timeout: ACTION_MS });
    await page.context().close();
  });

  // S85-09 — AC2/Q4A (r2#3): a failed harness-list read is a visible reason in both
  // panes — never silence, and never the contradictory「No harnesses registered.」.
  await part("S9", "a failed harness list says why", async () => {
    const page = await newPage();
    // The later registration wins over the shared adapter mocks: the list itself 500s.
    await page.context().route(/\/api\/harness(?:\/|\?|$)/, (route) => route.fulfill({ status: 500, json: { error: "internal" } }));
    await openSettings(page);
    await generalSidebarItems(page).filter({ hasText: "Effective values & sources" }).click({ timeout: ACTION_MS });
    const effectivePane = generalPane(page, "effective");
    await effectivePane.waitFor({ state: "visible", timeout: ACTION_MS });
    const effectiveText = await waitFor(async () => {
      const body = await effectivePane.innerText();
      return body.includes("The harness list could not be read: HTTP 500") ? body : false;
    }, ACTION_MS, "the effective pane never showed the list read's failure");
    assert.ok(!effectiveText.includes("No harnesses registered."), `the failed read masquerades as an empty registry: ${effectiveText.slice(0, 200)}`);
    await generalSidebarItems(page).filter({ hasText: "Environment" }).click({ timeout: ACTION_MS });
    const envPane = generalPane(page, "environment");
    await envPane.waitFor({ state: "visible", timeout: ACTION_MS });
    await waitFor(async () => (await envPane.innerText()).includes("The harness list could not be read: HTTP 500"), ACTION_MS, "the environment pane never showed the list read's failure");
    await page.context().close();
  });

  // S85-08 — AC4: stored old values land on the new pages.
  await part("S8", "navigation migration", async () => {
    for (const [stored, expectedTab, expectedHost] of [
      ["claude", /^Permissions$/, "permission"],
      ["models", /^Models$/, "models"],
    ]) {
      const page = await newPage(undefined, { "pi-web:settings-navigation": JSON.stringify({ section: stored }) });
      await openSettings(page);
      const current = sectionTabs(page).filter({ hasText: expectedTab }).first();
      await current.waitFor({ state: "visible", timeout: COLD_MS });
      assert.equal(await current.getAttribute("aria-current"), "page", `stored ${stored} did not land on ${expectedHost}`);
      await sectionHost(page, expectedHost).waitFor({ state: "visible", timeout: ACTION_MS });
      await page.context().close();
    }
  });

  // S85-10 — 85a4 AC1/AC4: the approval pane's unified block and claude's own sections.
  await part("S10", "approval pane", async () => {
    const page = await newPage();
    permissionPuts.length = 0;
    await openSettings(page);
    await tab(page, /^Permissions$/).click({ timeout: ACTION_MS });
    const permission = sectionHost(page, "permission");
    await permission.locator(".config-sidebar-item").first().waitFor({ state: "visible", timeout: ACTION_MS });
    const pane = permission.locator('[data-permission-pane="approval"]');
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    // The unified block: the current value, pi's own 不做审批 note, claude's value.
    const unified = pane.locator(".unified-setting").first();
    await unified.waitFor({ state: "visible", timeout: ACTION_MS });
    const text = await waitFor(async () => {
      const body = await unified.innerText();
      return body.includes("pi does no approval prompting") ? body : false;
    }, ACTION_MS, "the unified block never showed pi's own note");
    assert.ok(text.includes("default"), `the unified block does not show the current mode: ${text}`);
    // claude's own sections: the extra values' radio, denyInsteadOfPrompting,
    // and the Pi Web approval channel (the moved claude tool permissions).
    await pane.getByRole("radio", { name: /bypassPermissions/ }).waitFor({ state: "visible", timeout: ACTION_MS });
    await pane.getByRole("switch", { name: "denyInsteadOfPrompting" }).waitFor({ state: "visible", timeout: ACTION_MS });
    await pane.getByRole("radiogroup", { name: "Permission mode" }).waitFor({ state: "visible", timeout: ACTION_MS });
    await pane.getByRole("switch", { name: "Push a reminder when a permission waits" }).waitFor({ state: "visible", timeout: ACTION_MS });
    // The retired page's Claude Code report block renders again (owner
    // ruling: taken over): version, claude's own capabilities, the features.
    const reportText = await waitFor(async () => {
      const body = await pane.innerText();
      return body.includes("2.1.322") && body.includes("Subagent text forwarding") ? body : false;
    }, ACTION_MS, "the Claude Code report block never showed its mocked answer");
    assert.ok(reportText.includes("ui_surface_v1"), "claude's own capabilities are not listed");
    assert.ok(reportText.includes("Background tasks"), "the feature table is missing");
    assert.equal(permissionPuts.length, 0, "opening the page wrote something");
    await page.context().close();
  });

  // S85-11 — 85a4 AC1/AC2 (Q1 A): the unified three write claude's native mode;
  // the extra values are claude's own radio.
  await part("S11", "unified approval write", async () => {
    const page = await newPage();
    permissionPuts.length = 0;
    await openSettings(page);
    await tab(page, /^Permissions$/).click({ timeout: ACTION_MS });
    const pane = sectionHost(page, "permission").locator('[data-permission-pane="approval"]');
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    // 不询问即拒绝 writes claude's native dontAsk (Q1 A).
    await pane.getByRole("radio", { name: "Deny without asking" }).click({ timeout: ACTION_MS });
    await waitFor(() => permissionPuts.some((put) => put.path === "/api/harness/claude/permission" && put.body?.approvalMode === "dontAsk"),
      ACTION_MS, `the unified deny write never reached claude: ${JSON.stringify(permissionPuts)}`);
    // An extra value is claude's own, written from its radio.
    await pane.getByRole("radio", { name: /acceptEdits/ }).click({ timeout: ACTION_MS });
    await waitFor(() => permissionPuts.some((put) => put.body?.approvalMode === "acceptEdits"),
      ACTION_MS, `the extra value write never reached claude: ${JSON.stringify(permissionPuts)}`);
    await page.context().close();
  });

  // S85-12 — 85a4 AC1/AC2: the sandbox pane writes claude's sandbox keys; pi answers 无沙箱.
  await part("S12", "sandbox pane", async () => {
    const page = await newPage();
    permissionPuts.length = 0;
    await openSettings(page);
    await tab(page, /^Permissions$/).click({ timeout: ACTION_MS });
    const permission = sectionHost(page, "permission");
    await permission.locator(".config-sidebar-item").filter({ hasText: "Sandbox" }).click({ timeout: ACTION_MS });
    const pane = permission.locator('[data-permission-pane="sandbox"]');
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    await waitFor(async () => (await pane.locator(".unified-setting").first().innerText()).includes("pi has no sandbox"),
      ACTION_MS, "the sandbox block never showed pi's own note");
    const enabled = pane.getByRole("switch", { name: "Bash sandbox" });
    await enabled.waitFor({ state: "visible", timeout: ACTION_MS });
    assert.equal(await enabled.getAttribute("aria-checked"), "true", "the mocked enabled switch is off");
    await enabled.click({ timeout: ACTION_MS });
    await waitFor(() => permissionPuts.some((put) => put.body?.sandboxEnabled === false),
      ACTION_MS, `the sandbox write never reached claude: ${JSON.stringify(permissionPuts)}`);
    const autoAllow = pane.getByRole("switch", { name: "Auto-allow Bash if sandboxed" });
    await autoAllow.click({ timeout: ACTION_MS });
    await waitFor(() => permissionPuts.some((put) => put.body?.autoAllowBashIfSandboxed === true),
      ACTION_MS, `the auto-allow write never reached claude: ${JSON.stringify(permissionPuts)}`);
    // claude's other sandbox keys show by name only.
    assert.match(await pane.innerText(), /failIfUnavailable/, "the other sandbox keys are missing");
    await page.context().close();
  });

  // S85-13 — 85a4 AC1: the trust pane shows each harness's own decision, and
  // 统一成当前值 writes the differing harness with the current value.
  await part("S13", "trust unify", async () => {
    const page = await newPage();
    permissionPuts.length = 0;
    await openSettings(page);
    await tab(page, /^Permissions$/).click({ timeout: ACTION_MS });
    const permission = sectionHost(page, "permission");
    await permission.locator(".config-sidebar-item").filter({ hasText: "Project trust" }).click({ timeout: ACTION_MS });
    const pane = permission.locator('[data-permission-pane="trust"]');
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    const unified = pane.locator(".unified-setting").first();
    await waitFor(async () => {
      const body = await unified.innerText();
      return body.includes("trusted") && body.includes("untrusted") ? body : false;
    }, ACTION_MS, "the trust block never showed both harnesses' decisions");
    const unify = pane.getByRole("button", { name: "Unify to the current value" });
    await unify.waitFor({ state: "visible", timeout: ACTION_MS });
    await unify.click({ timeout: ACTION_MS });
    // pi is the current value (trusted); claude differs, so claude is written.
    await waitFor(() => permissionPuts.some((put) => put.path === "/api/harness/claude/permission" && put.body?.projectTrust?.trusted === true),
      ACTION_MS, `the trust unify never reached claude: ${JSON.stringify(permissionPuts)}`);
    assert.ok(!permissionPuts.some((put) => put.path === "/api/harness/pi/permission"), "the unify rewrote the harness that already agreed");
    await page.context().close();
  });

  // S85-14 — 85a4 AC2: the rules pane edits claude's allow / ask / deny lists,
  // with no unified block (规则没有统一块).
  await part("S14", "rules editor", async () => {
    const page = await newPage();
    permissionPuts.length = 0;
    await openSettings(page);
    await tab(page, /^Permissions$/).click({ timeout: ACTION_MS });
    const permission = sectionHost(page, "permission");
    await permission.locator(".config-sidebar-item").filter({ hasText: "Rules" }).click({ timeout: ACTION_MS });
    const pane = permission.locator('[data-permission-pane="rules"]');
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    // No unified block on the rules pane.
    assert.equal(await pane.locator(".unified-setting").count(), 0, "the rules pane shows a unified block");
    const allow = pane.locator("#permission-rules-allow");
    await allow.waitFor({ state: "visible", timeout: ACTION_MS });
    assert.equal(await allow.inputValue(), "WebSearch", "the mocked allow rules are missing");
    assert.equal(await pane.locator("#permission-rules-deny").inputValue(), "Bash(rm:*)", "the mocked deny rules are missing");
    await allow.fill("WebSearch\nWebFetch(domain:example.com)");
    await pane.getByRole("button", { name: "Save", exact: true }).click({ timeout: ACTION_MS });
    await waitFor(() => permissionPuts.some((put) => put.body?.rules?.allow?.length === 2 && put.body.rules.allow.includes("WebFetch(domain:example.com)")
      && put.body.rules.deny?.length === 1),
      ACTION_MS, `the rules write never reached claude: ${JSON.stringify(permissionPuts)}`);
    await page.context().close();
  });

  // S85-15 — 85a4 AC4: General has no claude permission controls; the hook
  // switch (a Pi Web preference now) stays.
  await part("S15", "general cleanup", async () => {
    const page = await newPage();
    await openSettings(page);
    const conversation = generalPane(page, "conversation");
    await conversation.waitFor({ state: "visible", timeout: ACTION_MS });
    await conversation.getByRole("switch", { name: "Show hook activity" }).waitFor({ state: "visible", timeout: ACTION_MS });
    const text = await conversation.innerText();
    assert.ok(!text.includes("claude tool permissions"), `General still shows the claude permission block: ${text.slice(0, 200)}`);
    assert.ok(!text.includes("Ask in a dialog"), `General still shows the approval channel radios: ${text.slice(0, 200)}`);
    await page.context().close();
  });
} finally {
  await h.close();
}

const failed = [...failures].filter(([, list]) => list.length > 0);
if (failed.length > 0 || ran.size < Object.keys(T).length) {
  for (const [title, list] of failed) {
    console.log(`FAIL harness-settings-85: ${title}`);
    for (const entry of list) console.log(`  ${entry}`);
  }
  for (const title of Object.values(T)) if (!ran.has(title)) console.log(`FAIL harness-settings-85: never ran: ${title}`);
  process.exitCode = 1;
} else {
  console.log("PASS harness-settings-85");
}
