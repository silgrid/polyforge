// carries: components/HarnessGeneralSettings*, components/GeneralSettings*, components/HarnessEnvironmentSettings*, components/HarnessEffectiveSettings*, components/UnifiedSetting*, components/HarnessApplyTo*
// carries: lib/harness-settings-view*, lib/claude-settings-store*, lib/harness/diagnostics*, hooks/useClaudeSettings.ts, app/api/harness/*
// carries: components/AccountSettings*, components/PiAccountLogin*, components/ModelsConfig*, app/api/settings/account/*, lib/provider-listing*, lib/harness/account*, lib/polyforge-doctor*
// — polyforge#149 (#85 85a1b, the settings skeleton): the 9+2 tab rail
// (九个新 tab 加两个标「旧版」的原「模型」「Claude」tab), General's left
// column (界面与对话 / 环境 / 生效值与来源), the account / models / permission
// placeholders, and the migrated old navigation values (claude → 权限,
// models → 新「模型」页). The harness adapter APIs are mocked at the browser
// (context.route): this suite pins the UI contract and never runs a real
// native diagnostic command; the adapter and route behavior is pinned by
// lib/harness/*.test.mjs and app/api/harness/route.test.mjs.
//
// Not declared, though exercised: components/SettingsPanel.tsx and
// lib/settings-navigation.ts (already declared by 3 suites),
// components/ClaudeSettings.tsx and lib/claude-settings.ts
// (e2e/claude-settings-445.mjs) — lib/e2e-carries.test.mjs.
//
// Each scenario prints one `PASS S85-nn` marker; the suite ends with
// `PASS harness-settings-85` only when every scenario passed. Every wait
// polls its condition to a bounded deadline; no fixed sleep.
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { chromium } from "playwright";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createHarness, createWaitUntil, seedStorage } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-harness-settings-85-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
mkdirSync(agentDir, { recursive: true });

// A stub `claude` for the one-shot login terminal (S85-12): `auth login`
// prints its ready line and waits — a real claude prints the authorization
// URL and waits for the pasted code. Anything else delegates to the
// recorded unit stub (the initialize probe). Only S85-12's terminal runs
// it; every other claude face is mocked at the browser.
const stubBin = join(work, "stub-bin");
mkdirSync(stubBin, { recursive: true });
const stubClaude = join(stubBin, "claude");
writeFileSync(stubClaude, `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "login" ]; then
  printf '%s\r\n' "85 STUB LOGIN READY - open the URL and paste the code"
  sleep 60
  exit 0
fi
exec node ${join(dirname(new URL(import.meta.url).pathname), "..", "lib", "__fixtures__", "claude-stub", "claude.mjs")} "$@"
`);
chmodSync(stubClaude, 0o755);

const h = createHarness({
  timeout: 30_000,
  coldFirst: true,
  errors: ["pageerror", "console", "5xx"],
  server(name) {
    return { name, logFile: join(work, `server-${name}.log`), env: { HOME: home, PI_CODING_AGENT_DIR: agentDir, PATH: `${stubBin}:${process.env.PATH}` } };
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
const CLAUDE_SETTINGS = { launchPermissionMode: "default", customModels: [], includeHookEvents: false, planAvailable: true };
const CLAUDE_REPORT = { available: true, models: [{ value: "default", displayName: "Default", source: "alias" }], version: "2.1.322", versionRecorded: true, recordedVersions: ["2.1.322"], capabilities: ["hooks"], features: [] };

/** Answers the harness/claude adapter routes the settings skeleton reads; everything else hits the server. */
async function mockAdapterApis(context) {
  // The app's fresh-page flow would otherwise select a default project
  // asynchronously (POST /api/default-cwd → POST /api/cwd/validate): a cwd
  // landing mid-scenario re-fires the panes' reads with ?cwd=, racing every
  // no-project assertion (v1 round 1: S85-09's alert was cleared by exactly
  // that). The suite is a no-project suite; the flow answers no cwd.
  await context.route("**/api/default-cwd", (route) => route.fulfill({ status: 200, json: {} }));
  await context.route(/\/api\/harness(?:\/|\?|$)/, (route) => {
    const path = new URL(route.request().url()).pathname;
    const body = path === "/api/harness" ? HARNESS_LIST
      : path === "/api/harness/pi/env" ? ENV_PI
      : path === "/api/harness/claude/env" ? ENV_CLAUDE
      : path === "/api/harness/pi/effective-config" ? EFFECTIVE_PI
      : path === "/api/harness/claude/effective-config" ? EFFECTIVE_CLAUDE
      : path === "/api/harness/pi/diagnose" ? DIAG_PI
      : path === "/api/harness/claude/diagnose" ? DIAG_CLAUDE
      : null;
    if (body === null) route.fulfill({ status: 404, json: { error: "unknown-harness-path" } });
    else route.fulfill({ status: 200, json: body });
  });
  await context.route("**/api/models-config", (route) => route.fulfill({ status: 200, json: { providers: {} } }));
  await context.route("**/api/claude-settings", (route) => route.fulfill({ status: 200, json: CLAUDE_SETTINGS }));
  await context.route("**/api/claude", (route) => route.fulfill({ status: 200, json: CLAUDE_REPORT }));
  await context.route("**/api/claude-permissions", (route) => route.fulfill({ status: 200, json: { mode: "ask", push: true, session: null } }));
}

const TABS = [
  "General", "Account", "Models", "Permissions", "Skills", "MCP",
  "Sub-agents", "Plugins", "Polyforge", "Models (legacy)", "Claude (legacy)",
];

/** A label as an anchored regex, its punctuation escaped (the legacy labels carry parens). */
const labelRe = (label) => new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);

const T = {
  S1: "S85-01: Settings shows 9 new tabs plus the two legacy-marked ones, in the settled order",
  S2: "S85-02: General is a left-column layout whose three items switch the detail pane",
  S3: "S85-03: a phone walks General as list → detail → back",
  S4: "S85-04: the account / models / permission placeholders open, permission naming its Claude part",
  S5: "S85-05: the legacy Models and Claude pages stay reachable and working",
  S6: "S85-06: the environment pane lists only variable names, grouped per harness",
  S7: "S85-07: effective values carry the Inferred mark and their source; native diagnostics are a collapsed block per harness",
  S8: "S85-08: stored old navigation values land on the new pages",
  S9: "S85-09: a failed harness-list read is a visible reason in both panes, never a contradictory empty registry",
  S10: "S85-10: the account tab lists the harnesses, shows the interactive card and the per-Cell rows with every field, and never a credential value",
  S11: "S85-11: pi's sign-in lives only on Account › pi — the legacy Models tab has no login, API-key entry or Add-provider picker, and the never-offered Anthropic subscription appears nowhere",
  S12: "S85-12: the account page's one-shot login terminal attaches and streams the declared command's output — the real login path a human would walk (review r1#1)",
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
    for (const name of ["General", "Account", "Models", "Permissions", "MCP", "Polyforge", "Models (legacy)", "Claude (legacy)"]) {
      assert.ok(!(await tab(page, name).isDisabled()), `${name} should open without a project`);
    }
    // The legacy tabs are marked as such in their own label.
    assert.match(labels[9], /\(legacy\)/, "the old Models tab is not marked legacy");
    assert.match(labels[10], /\(legacy\)/, "the old Claude tab is not marked legacy");
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

  // S85-04 — AC1: the models and permission placeholders open (85a2 filled the
  // account tab in); permission names its Claude part.
  await part("S4", "placeholders", async () => {
    const page = await newPage();
    await openSettings(page);
    for (const [name, id] of [["Models", "models"], ["Permissions", "permission"]]) {
      await tab(page, new RegExp(`^${name}$`)).click({ timeout: ACTION_MS });
      const host = sectionHost(page, id);
      await host.waitFor({ state: "visible", timeout: ACTION_MS });
      assert.match(await host.innerText(), /Placeholder/i, `the ${name} page shows no placeholder text`);
    }
    // The permission page names the Claude part the old `claude` value lands on.
    assert.match(await sectionHost(page, "permission").innerText(), /Claude/, "the permission placeholder does not name its Claude part");
    await page.context().close();
  });

  // S85-10 — polyforge#142 (85a2 AC1/AC7): the account tab — the two harness
  // panes, the interactive claude card, the per-Cell rows with the ticket's
  // field list (来源含 shared default, the keychain hint, 步骤能否使用及原因),
  // the credential form, and no credential value anywhere. The account and
  // harness answers are mocked at the browser; the S85-09 pattern (a later
  // route registration wins) carries the richer capability declaration.
  await part("S10", "the account tab", async () => {
    const page = await newPage();
    const ACCOUNT = {
      claude: { available: true, loggedIn: true, authMethod: "oauth_token", configDirectory: "/home/op/.claude", reason: null,
        envCredentials: { ANTHROPIC_AUTH_TOKEN: true, ANTHROPIC_BASE_URL: true, ANTHROPIC_API_KEY: false, CLAUDE_CODE_OAUTH_TOKEN: false } },
      cells: [
        { profile: "default", executionDisabled: null, probeError: null, harnessOrder: ["pi", "claude"], harnesses: {
          pi: { id: "pi", configRoot: "/h/default/step-agent", credential: "set", status: "ready", authType: "api_key", checkReason: null, source: "/home/op/.pi/agent", sourceKind: "operator", credentialsFollow: true, keychainHint: false, credentialKinds: null, credentialsFile: null, usable: true, reason: null },
          claude: { id: "claude", configRoot: "/h/default/claude-config", credential: "set", status: "ready", authType: null, checkReason: null, source: "/home/op/.pi/agent", sourceKind: "operator", credentialsFollow: true, keychainHint: false, credentialKinds: { oauthToken: false, apiKey: false, gateway: true }, credentialsFile: "absent", usable: true, reason: null } } },
        { profile: "beta", executionDisabled: null, probeError: null, harnessOrder: ["pi", "claude"], harnesses: {
          pi: { id: "pi", configRoot: "/h/beta/step-agent", credential: "unset", status: "not_ready", authType: null, checkReason: "credentials_not_configured", source: "/home/op/.pi/agent", sourceKind: "shared-default", credentialsFollow: true, keychainHint: false, credentialKinds: null, credentialsFile: null, usable: false, reason: "credentials_not_configured" },
          claude: { id: "claude", configRoot: "/h/beta/claude-config", credential: "unset", status: "not_ready", authType: null, checkReason: null, source: "/home/op/.pi/agent", sourceKind: "shared-default", credentialsFollow: false, keychainHint: true, credentialKinds: null, credentialsFile: "present", usable: true, reason: null } } },
      ],
      doctorError: null,
    };
    await page.context().route(/\/api\/settings\/account/, (route) => route.fulfill({ json: ACCOUNT }));
    await page.context().route(/\/api\/harness(?:\/|\?|$)/, (route) => route.fulfill({ json: { harnesses: [
      { id: "pi", version: "1.5.0", versionKnown: true, knownRange: null, versionReason: null, writable: true, capabilities: { id: "pi", loginCommands: [], credentialsFollowConfigRoot: true } },
      { id: "claude", version: "2.1.322", versionKnown: true, knownRange: null, versionReason: null, writable: true, capabilities: { id: "claude", loginCommands: [
        { id: "auth-login", argv: ["claude", "auth", "login"], cell: false },
        { id: "setup-token", argv: ["claude", "setup-token"], cell: false },
      ], credentialsFollowConfigRoot: false } },
    ] } }));
    await openSettings(page);
    await tab(page, /^Account$/).click({ timeout: ACTION_MS });
    const host = sectionHost(page, "account");
    await host.waitFor({ state: "visible", timeout: ACTION_MS });
    // the left column: the two harnesses
    const claudeItem = host.locator(".config-sidebar-item").filter({ hasText: "Claude" });
    await claudeItem.click({ timeout: ACTION_MS });
    const claudeText = await waitFor(async () => {
      const text = await host.innerText();
      return text.includes("Interactive session") && text.includes("Daemon steps") ? text : false;
    }, ACTION_MS, "the claude pane never showed its cards");
    for (const expected of ["oauth_token", "/home/op/.claude", "Log in with Claude (OAuth)", "Mint a setup token",
      "/h/default/claude-config", "shared default", "/home/op/.pi/agent", "Keychain", "Residual OAuth login",
      "Write a credential", "copied server-side"]) {
      assert.ok(claudeText.includes(expected), `the claude pane misses ${expected}`);
    }
    assert.ok(!claudeText.includes("sk-SENTINEL"), "a credential value rendered");
    // the pi pane: its own rows, no claude form
    await host.locator(".config-sidebar-item").filter({ hasText: /^pi$/ }).click({ timeout: ACTION_MS });
    const piText = await waitFor(async () => {
      const text = await host.innerText();
      return text.includes("api_key") && text.includes("/h/beta/step-agent") ? text : false;
    }, ACTION_MS, "the pi pane never showed its rows");
    assert.ok(!piText.includes("Write a credential"), "the pi pane carries the claude form");
    await page.context().close();
  });

  // S85-11 — polyforge#142 (85a2 AC4/AC8): pi's sign-in, API-key entry and
  // Add-provider picker live only on Account › pi; the legacy Models tab
  // shows a moved notice for a managed provider; and the never-offered
  // Anthropic subscription login appears nowhere (the mocked providers answer
  // is the filtered shape the route now returns).
  await part("S11", "pi sign-in only on the account page", async () => {
    const page = await newPage();
    const PROVIDERS = {
      providers: [
        { id: "openai-codex", name: "ChatGPT Plus/Pro", usesCallbackServer: false, loggedIn: false, supportsApiKey: false },
        { id: "google", name: "Google", usesCallbackServer: false, loggedIn: true, supportsApiKey: true },
      ],
      oauthProviders: [
        { id: "openai-codex", name: "ChatGPT Plus/Pro", usesCallbackServer: false, loggedIn: false, supportsApiKey: false },
        { id: "google", name: "Google", usesCallbackServer: false, loggedIn: true, supportsApiKey: true },
      ],
      apiKeyProviders: [
        { id: "google", displayName: "Google", configured: true, modelCount: 8, supportsOAuth: true },
        { id: "anthropic", displayName: "Anthropic", configured: false, modelCount: 12, supportsOAuth: true },
      ],
    };
    await page.context().route(/\/api\/auth\/providers/, (route) => route.fulfill({ json: PROVIDERS }));
    await openSettings(page);
    // the legacy Models tab: the logged-in provider row opens the moved notice
    await tab(page, /^Models \(legacy\)$/).click({ timeout: ACTION_MS });
    const models = sectionHost(page, "legacy-models");
    await models.locator(".config-sidebar-item").filter({ hasText: "Google" }).first().click({ timeout: ACTION_MS });
    // the heading renders in the detail header's uppercase style
    const movedText = await waitFor(async () => {
      const text = await models.innerText();
      return /sign-in has moved/i.test(text) ? text : false;
    }, ACTION_MS, "the legacy tab never showed the moved notice");
    assert.ok(!movedText.includes("Log in"), "the legacy tab still offers a login");
    assert.ok(!movedText.includes("Add provider"), "the legacy tab still offers the picker");
    // Account › pi: the rows, the Add provider button, and no Anthropic
    // subscription anywhere
    await tab(page, /^Account$/).click({ timeout: ACTION_MS });
    const account = sectionHost(page, "account");
    await account.locator(".config-sidebar-item").filter({ hasText: /^pi$/ }).click({ timeout: ACTION_MS });
    const piText = await waitFor(async () => {
      const text = await account.innerText();
      return text.includes("ChatGPT Plus/Pro") ? text : false;
    }, ACTION_MS, "the account pi pane never listed the offered providers");
    assert.ok(piText.includes("Add provider"), "the account pi pane lost the Add provider entry");
    assert.ok(piText.includes("Anthropic"), "the Anthropic API-key entry is gone from the account pane");
    assert.ok(!piText.includes("Anthropic (Claude Pro/Max)"), "the never-offered Anthropic subscription appears");
    await page.context().close();
  });

  // S85-12 — polyforge#142 (85a2 AC3, review r1#1): the account page's
  // 「打开终端」 runs the declared login command in the one-shot terminal, and
  // the embedded panel ATTACHES to it — the terminal's output reaches the
  // page (the stub claude prints its ready line and waits, exactly where a
  // real claude auth login prints its URL and waits for the code). Closing
  // the terminal tears it down. The red of the round-1 defect under review
  // was exactly here: the panel's start request failed, the stream never
  // opened, and this poll never settled.
  await part("S12", "the one-shot login terminal attaches", async () => {
    const page = await newPage();
    const ACCOUNT = {
      claude: { available: true, loggedIn: false, authMethod: "none", configDirectory: join(home, ".claude"), reason: null,
        envCredentials: { ANTHROPIC_AUTH_TOKEN: false, ANTHROPIC_BASE_URL: false, ANTHROPIC_API_KEY: false, CLAUDE_CODE_OAUTH_TOKEN: false }, consoleProfile: "absent" },
      cells: [],
      doctorError: null,
    };
    await page.context().route(/\/api\/settings\/account/, (route) => route.fulfill({ json: ACCOUNT }));
    await page.context().route(/\/api\/harness(?:\/|\?|$)/, (route) => route.fulfill({ json: { harnesses: [
      { id: "pi", version: "1.5.0", versionKnown: true, knownRange: null, versionReason: null, writable: true, capabilities: { id: "pi", loginCommands: [], credentialsFollowConfigRoot: true } },
      { id: "claude", version: "2.1.322", versionKnown: true, knownRange: null, versionReason: null, writable: true, capabilities: { id: "claude", loginCommands: [
        { id: "auth-login", argv: ["claude", "auth", "login"], cell: false },
      ], credentialsFollowConfigRoot: true } },
    ] } }));
    await openSettings(page);
    await tab(page, /^Account$/).click({ timeout: ACTION_MS });
    const account = sectionHost(page, "account");
    await account.locator(".config-sidebar-item").filter({ hasText: "Claude" }).click({ timeout: ACTION_MS });
    const loginButton = account.locator('[data-login-command="auth-login"]');
    await loginButton.waitFor({ state: "visible", timeout: ACTION_MS });
    await loginButton.click({ timeout: ACTION_MS });
    // the panel attaches and the command's output streams: poll the
    // terminal's own text, not a static heading
    const terminal = account.locator("[data-account-login-terminal]");
    await terminal.waitFor({ state: "visible", timeout: ACTION_MS });
    const termText = await waitFor(async () => {
      const text = await terminal.innerText();
      return text.includes("85 STUB LOGIN READY") ? text : false;
    }, ACTION_MS, "the login terminal never showed the command's output");
    assert.ok(!termText.includes("SENTINEL"), "a value leaked into the terminal");
    // closing tears it down: the panel goes and the terminal with it
    await account.getByRole("button", { name: "Close terminal" }).click({ timeout: ACTION_MS });
    await waitFor(async () => !(await terminal.count()), ACTION_MS, "the closed login terminal stayed mounted");
    await page.context().close();
  });

  // S85-05 — AC1: the legacy pages stay reachable and keep working.
  await part("S5", "legacy pages", async () => {
    const page = await newPage();
    await openSettings(page);
    await tab(page, /^Models \(legacy\)$/).click({ timeout: ACTION_MS });
    const models = sectionHost(page, "legacy-models");
    // 85a2 moved the Add-provider picker to Account › pi: the legacy tab
    // renders its provider list without one
    await models.locator(".config-sidebar-list").first().waitFor({ state: "visible", timeout: COLD_MS });
    assert.equal(await models.getByRole("button", { name: "Add provider", exact: true }).count(), 0, "the legacy Models tab still offers Add provider");
    await tab(page, /^Claude \(legacy\)$/).click({ timeout: ACTION_MS });
    const claude = sectionHost(page, "legacy-claude");
    await claude.getByRole("radiogroup", { name: "Default permission mode" }).waitFor({ state: "visible", timeout: COLD_MS });
    // The hook switch stays on the legacy page too (one shared state holder).
    await claude.getByRole("switch", { name: "Show hook activity" }).waitFor({ state: "visible", timeout: ACTION_MS });
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
