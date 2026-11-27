// carries: components/SettingsDrafts*, components/NativeModelTiers*, components/ConnectionOverview*, lib/model-reference-impact*
// #285: production Settings components, isolated HTTP fixtures; no OAuth/model requests.
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { createHarness, writeHostSession } from "./harness.mjs";
import { verifySettingsVisuals } from "./settings-refactor-285-visual.mjs";

const artifacts = process.env.SETTINGS_285_ARTIFACTS || join(process.cwd(), "test-results/settings-285");
mkdirSync(artifacts, { recursive: true });
const work = mkdtempSync(join(tmpdir(), "settings-285-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
mkdirSync(project, { recursive: true });
writeHostSession(agentDir, { id: "settings-285", cwd: project, text: "Settings 285 fixture", timestamp: "2026-11-26T00:00:00Z" });
const h = createHarness();
try {
  await h.startServer({ name: "settings-285", distDir: ".next/e2e/settings-285", logFile: join(artifacts, "browser-server.log"), env: { HOME: work, PI_CODING_AGENT_DIR: agentDir, PI_OFFLINE: "1" } });
  await h.launchBrowser(() => chromium.launch({ headless: true, ...(process.env.SETTINGS_BROWSER ? { executablePath: process.env.SETTINGS_BROWSER } : {}) }));
  if (process.env.SETTINGS_VISUAL_ONLY === "1") {
    await verifySettingsVisuals(h, { artifacts, project, work });
  } else {
  const context = await h.browser.newContext({ viewport: { width: 1440, height: 1000 }, reducedMotion: "reduce" });
  await context.addInitScript(() => { localStorage.setItem("pi-locale", "en"); });
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  let config = { providers: Object.fromEntries(Array.from({ length: 11 }, (_, index) => [`gateway${index}`, { api: "openai-completions", baseUrl: "https://fixture.invalid/v1", apiKey: "fixture-only", models: [{ id: "m1", name: `Gateway ${index} One` }, { id: "m2", name: `Gateway ${index} Two` }] }])) };
  let nativeDefault = null;
  let host = { pfPresent: true, pfVersion: "0.2.4", capable: true, stepTiersReason: null, file: { sha256: "hash-1" }, items: [{ key: "step_tiers.fast", value: ["pi:gateway0/m1:high", "pi:missing/model:low"] }] };
  let failEffort = true;
  let failResync = true;
  let conflictTier = true;
  let staleProfile = false;
  const writes = [];
  const enabled = { allEnabled: true, patterns: [], stalePatterns: [], enabledTotal: 22, availableTotal: 22, scope: "global", settingsPath: "fixture/settings.json", editable: true,
    providers: Object.keys(config.providers).map((id) => ({ id, name: id, kind: "custom", enabledCount: 2, models: ["m1", "m2"].map((model) => ({ id: model, name: model, ref: `${id}/${model}`, enabled: true, contextWindow: 2048 })) })) };
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url()); const path = url.pathname; const method = route.request().method();
    const body = method === "GET" ? null : route.request().postDataJSON();
    const send = (json, status = 200) => route.fulfill({ json, status });
    if (path.startsWith("/api/sessions") || path === "/api/agent/running") return route.continue();
    if (path === "/api/models-config") {
      if (method === "PUT") { writes.push({ path, body }); config = body; return send({ success: true }); }
      return send(config);
    }
    if (path === "/api/models/enabled") {
      if (method === "PUT") { writes.push({ path, body }); if (body.op === "resync" && failResync) return send({ error: "fixture resync failed" }, 409); }
      return send(enabled);
    }
    if (path === "/api/models") return send({ modelList: Object.entries(config.providers).flatMap(([provider, entry]) => entry.models.map((model) => ({ ...model, provider }))), defaultModel: { provider: "gateway0", modelId: "m1" }, thinkingLevels: { "gateway0:m1": ["low", "high"] }, models: {} });
    if (path === "/api/models/default") {
      if (method === "PUT") { writes.push({ path, body }); nativeDefault = body.modelId; }
      return send({ defaultHarness: "pi", claudeDefaultModel: "default", claudeDefaultEffort: "default", ok: true });
    }
    if (path === "/api/harness/pi/model") {
      if (method === "PUT") { writes.push({ path, body }); if (failEffort) return send({ error: "fixture effort failed" }, 409); }
      return send({ data: { defaultProvider: nativeDefault ? "gateway0" : null, defaultModel: nativeDefault, reasoningEffort: null, perModelReasoningEffort: {}, providers: [], customModels: [] } });
    }
    if (path === "/api/harness/claude/model") return send({ data: { defaultModel: null, perModelReasoningEffort: {}, userCustomModels: [] } });
    if (path === "/api/claude") return send({ models: [], available: false });
    if (path === "/api/harness") return send({ harnesses: [] });
    if (path === "/api/auth/providers") return send({ oauthProviders: [], apiKeyProviders: [] });
    if (path === "/api/settings/account") {
      if (method === "POST") { writes.push({ path, body }); return send({ results: body.targets.map((target) => ({ target, status: target === "beta" ? "refused" : "written", reason: "fixture" })) }); }
      return send({ claude: { available: true, loggedIn: false, configDirectory: "fixture/claude", envCredentials: {} }, cells: [{ profile: "default", harnesses: {} }, { profile: "beta", harnesses: {} }], doctorError: null });
    }
    if (path === "/api/polyforge/config") {
      if (method === "POST") { writes.push({ path, body }); if (conflictTier) return send({ error: "CAS fixture conflict" }, 409); host = { ...host, file: { sha256: "hash-2" }, items: [{ key: body.key, value: body.value }] }; }
      return send(host);
    }
    if (path === "/api/polyforge/workflows") return send({ capable: true, workflows: null, workspace: null, workspaces: [] });
    if (path === "/api/subagents/profiles") return send({ profiles: staleProfile ? [{ name: "stale", scope: "global", model: "gateway10/retired" }] : [] });
    if (path === "/api/onboarding") return send({ imageManaged: false });
    if (path === "/api/project-trust") return send({ trusted: true, status: "trusted", servers: [] });
    if (path === "/api/polyforge/status") return send({ pfPresent: false, status: null });
    if (path === "/api/home") return send({ home: work });
    return send({});
  });
  await page.goto(`${h.base}/?session=settings-285`, { waitUntil: "domcontentloaded" });
  const show = page.getByRole("button", { name: "Show sidebar", exact: true });
  if (await show.isVisible()) await show.click();
  await page.getByRole("button", { name: "Settings", exact: true }).first().click();
  const dialog = page.getByRole("dialog", { name: "Settings", exact: true });
  const tab = (name) => dialog.locator(".settings-section-tab").filter({ hasText: new RegExp(`^${name}$`) });
  assert.equal(await dialog.locator(".settings-section-tab").count(), 8);
  await tab("Models").click();
  const nav = (id) => dialog.locator(`[data-model-subpage="${id}"]`);
  await dialog.locator('[data-connection-id="provider:gateway10"]').waitFor();
  assert.equal(await dialog.locator("[data-connection-id]").count(), 11);
  assert.equal(await dialog.locator("[data-connection-detail]:visible").count(), 0);
  assert.equal(await dialog.locator("[data-account-providers]:visible").count(), 0);
  await page.screenshot({ path: join(artifacts, "stress-1440-11-connections.png") });
  await nav("default").click();
  const choose = dialog.locator(".settings-model-default-select");
  await choose.waitFor();
  assert.equal(await choose.inputValue(), "", "effective fallback must not become a saved default");
  await choose.selectOption(JSON.stringify({ harness: "pi", provider: "gateway0", id: "m2" }));
  await nav("list").click();
  const effort = dialog.getByRole("combobox", { name: /Reasoning.*gateway0\/m1/ });
  await effort.selectOption("high");
  await tab("General").click();
  assert.equal(writes.length, 0, "navigation must not save");
  const drafts = dialog.locator("[data-settings-drafts]");
  await drafts.getByRole("button", { name: "Save", exact: true }).click();
  await dialog.getByText("fixture effort failed", { exact: true }).waitFor();
  assert.equal(writes.filter((item) => item.path === "/api/models/default").length, 1);
  await tab("General").click(); await tab("Models").click(); await nav("list").click();
  assert.equal(await effort.inputValue(), "high", "failed draft survives navigation");
  failEffort = false;
  await drafts.getByRole("button", { name: "Save", exact: true }).click();
  await drafts.waitFor({ state: "hidden" });
  assert.equal(writes.filter((item) => item.path === "/api/models/default").length, 1, "retry only the failed resource");
  console.log("PASS: real model default/effort wiring, navigation and partial retry");

  await nav("account-claude").click();
  const form = dialog.locator('[data-account-card="credential-form"]');
  await form.locator('[data-account-field="value"]').fill("fixture-secret");
  const credentialSave = form.locator('[data-account-action="save"]');
  assert.equal(await credentialSave.isDisabled(), true);
  await form.locator('[data-account-target="default"]').check();
  await form.locator('[data-account-target="default"]').uncheck();
  assert.equal(await credentialSave.isDisabled(), true, "unchecking the last target must not select all");
  await form.locator('[data-account-target="default"]').check();
  await form.locator('[data-account-target="beta"]').check();
  await credentialSave.click();
  await dialog.locator('[data-write-target="beta"][data-write-status="refused"]').waitFor();
  assert.deepEqual(writes.filter((item) => item.path === "/api/settings/account").at(-1).body.targets, ["default", "beta"]);
  assert.equal(await form.locator('[data-account-target="default"]').isChecked(), false);
  assert.equal(await form.locator('[data-account-target="beta"]').isChecked(), true);
  await credentialSave.click();
  await page.waitForFunction(() => !document.querySelector('[data-account-card="credential-form"] fieldset').disabled);
  assert.deepEqual(writes.filter((item) => item.path === "/api/settings/account").at(-1).body.targets, ["beta"]);
  await drafts.getByRole("button", { name: "Discard", exact: true }).click();
  assert.equal(await form.locator('[data-account-field="value"]').inputValue(), "");
  console.log("PASS: explicit credential targets, empty selection and partial-target retry");

  await nav("tiers").click();
  await dialog.locator(".settings-tier-editor > summary").first().click();
  const chain = dialog.getByRole("textbox", { name: "fast Candidates in order · one per line", exact: true });
  await chain.waitFor();
  assert.equal(await chain.inputValue(), "pi:gateway0/m1:high\npi:missing/model:low");
  await chain.fill("pi:missing/model:low\npi:gateway0/m1:high");
  await nav("default").click(); await nav("tiers").click();
  await drafts.getByRole("button", { name: "Save", exact: true }).click();
  await dialog.getByText("CAS fixture conflict", { exact: true }).waitFor();
  assert.equal(await chain.inputValue(), "pi:missing/model:low\npi:gateway0/m1:high");
  assert.deepEqual(writes.filter((item) => item.path === "/api/polyforge/config").at(-1).body.expect_state, { file_sha256: "hash-1" });
  conflictTier = false;
  await drafts.getByRole("button", { name: "Save", exact: true }).click();
  await drafts.waitFor({ state: "hidden" });
  console.log("PASS: native tier order/effort/invalid references and CAS failure preservation");

  await dialog.locator('[data-connection-id="provider:gateway10"]').click();
  const provider = dialog.locator('[data-provider-page="flat"]');
  // Provider fields use labels from the native editor; edit its exact URL field.
  const urlField = provider.getByPlaceholder("https://api.example.com/v1");
  await urlField.fill("https://fixture.invalid/v2");
  await nav("connections").click(); await nav("providers").click();
  await drafts.getByRole("button", { name: "Save", exact: true }).click();
  await provider.getByText(/Provider definitions saved/).waitFor();
  const configWrites = writes.filter((item) => item.path === "/api/models-config").length;
  failResync = false;
  await drafts.getByRole("button", { name: "Save", exact: true }).click();
  await drafts.waitFor({ state: "hidden" });
  assert.equal(writes.filter((item) => item.path === "/api/models-config").length, configWrites);
  console.log("PASS: native provider draft and dependent resync partial failure");
  await urlField.fill("https://fixture.invalid/draft");
  config = { ...config, providers: { ...config.providers, gateway10: { ...config.providers.gateway10, baseUrl: "https://fixture.invalid/external" } } };
  await drafts.getByRole("button", { name: "Save", exact: true }).click();
  await provider.getByText(/models.json changed elsewhere/).waitFor();
  assert.equal(writes.filter((item) => item.path === "/api/models-config").length, configWrites, "a stale provider draft must not overwrite another editor");
  await drafts.getByRole("button", { name: "Discard", exact: true }).click();
  await drafts.waitFor({ state: "hidden" });
  staleProfile = true;
  await provider.getByRole("button", { name: "More actions", exact: true }).click();
  await provider.getByRole("button", { name: "Delete", exact: true }).click();
  await provider.getByRole("button", { name: "Delete", exact: true }).click();
  await provider.getByText(/subagent · global\/stale · gateway10\/retired/).waitFor();
  assert.equal(writes.filter((item) => item.path === "/api/models-config").length, configWrites);
  assert.ok(config.providers.gateway10, "a referenced provider outside the catalog remains configured");
  staleProfile = false;
  console.log("PASS: retired-model provider references prevent deletion before mutation");

  await nav("list").click();
  const defaultRow = dialog.locator("tr").filter({ hasText: "gateway0/m2" });
  const beforeDisable = writes.filter((item) => item.path === "/api/models/enabled").length;
  await defaultRow.getByRole("checkbox").click();
  await dialog.locator(".settings-models > .config-error-notice").getByText(/This change affects saved references:/).waitFor();
  assert.equal(writes.filter((item) => item.path === "/api/models/enabled").length, beforeDisable);
  assert.equal(await defaultRow.getByRole("checkbox").isChecked(), true);
  console.log("PASS: stale provider refusal and saved-default reference protection");
  await context.addInitScript(() => localStorage.setItem("pi-theme", "dark"));
  await page.evaluate(() => document.documentElement.classList.add("dark"));
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.screenshot({ path: join(artifacts, "stress-1280-dark-models.png") });
  await page.setViewportSize({ width: 390, height: 844 });
  // Resize preserves the section level; select a row to enter the phone detail.
  await nav("list").click();
  const overflow = await dialog.evaluate((element) => ({ scroll: element.scrollWidth, width: element.clientWidth }));
  assert.ok(overflow.scroll <= overflow.width + 1, JSON.stringify(overflow));
  await page.screenshot({ path: join(artifacts, "stress-390-models.png") });
  await page.keyboard.press("Tab");
  assert.ok(await page.evaluate(() => document.activeElement?.closest('[role="dialog"]') !== null));
  await dialog.locator(".settings-phone-back").click();
  const lastConnection = dialog.locator('[data-connection-id="provider:gateway10"]');
  await lastConnection.scrollIntoViewIfNeeded();
  await lastConnection.click();
  await page.waitForFunction(() => document.querySelector('[data-provider-page="flat"] .models-provider-picker-field select')?.value === "provider:gateway10");
  assert.ok(await provider.isVisible(), "the eleventh connection remains reachable on a phone");
  await page.screenshot({ path: join(artifacts, "stress-390-eleventh-connection.png") });
  assert.deepEqual(errors, []);
  console.log("PASS: desktop, dark theme, 390px, eleventh connection and keyboard; zero page errors");
  await context.close();
  await verifySettingsVisuals(h, { artifacts, project, work });
  }
} finally { await h.close(); }
