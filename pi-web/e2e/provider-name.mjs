// carries: components/ModelsConfig*, components/ConnectionOverview*, components/HarnessModelSettings*, lib/model-reference-impact*, lib/i18n/messages/*
// Real Settings components with isolated HTTP fixtures; no real credentials or model calls.
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { createJiti } from "jiti";
import { createHarness, writeHostSession } from "./harness.mjs";

const jiti = createJiti(import.meta.url);
const { validateModelsConfigPayload, readModelsConfig, writeModelsConfig } = await jiti.import("../lib/models-config-store.ts");
const artifacts = process.env.PROVIDER_NAME_ARTIFACTS || join(process.cwd(), "test-results/provider-name");
mkdirSync(artifacts, { recursive: true });
const work = mkdtempSync(join(tmpdir(), "provider-name-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
mkdirSync(project, { recursive: true });
writeHostSession(agentDir, { id: "provider-name", cwd: project, text: "Provider display name fixture", timestamp: "2026-11-26T00:00:00Z" });
const original = {
  baseUrl: "https://fixture.invalid/v1", api: "openai-completions", apiKey: "!printf fixture-only",
  headers: { "X-Fixture": "FIXTURE_HEADER" }, customMetadata: { retained: true },
  models: [{ id: "m1", name: "Fixture model", contextWindow: 4096, extra: "keep" }],
};
let config = { providers: { acceptance: structuredClone(original), occupied: { baseUrl: "https://other.invalid/v1", models: [{ id: "m2" }] } } };
const modelsPath = join(agentDir, "models.json");
writeModelsConfig(config, modelsPath);
const writes = [];
const sessionLeases = [];
const referenceReads = [];
const errors = [];
const nativeDefault = { defaultProvider: "acceptance", defaultModel: "m1", perModelReasoningEffort: {} };
const host = { pfPresent: true, capable: true, pfVersion: "0.2.4", file: { sha256: "fixture-hash" }, items: [{ key: "step_tiers.fast", value: ["pi:acceptance/m1:high"] }] };
const workflows = { capable: true, workspaces: [{ workspace: project,
  declared: { yaml: { path: `${project}/.polyforge.yaml`, sha256: "workspace-hash" }, steps: { product: { task: { declared: true, models: ["pi:acceptance/m1:high"] } } } },
  workflows: { workspace: project, projects: ["product"], bundles: [], task: {} },
}] };
const enabled = () => ({ allEnabled: true, patterns: [], stalePatterns: [], enabledTotal: 2, availableTotal: 2, scope: "global", settingsPath: "fixture/settings.json", editable: true,
  providers: Object.entries(config.providers).map(([id, entry]) => ({ id, name: entry.name ?? id, kind: "custom", enabledCount: entry.models?.length ?? 0,
    models: (entry.models ?? []).map((model) => ({ ...model, name: model.name ?? model.id, ref: `${id}/${model.id}`, enabled: true })) })) });
const h = createHarness();
try {
  await h.startServer({ name: "provider-name", distDir: ".next/e2e/provider-name", logFile: join(artifacts, "browser-server.log"),
    from: { PATH: process.env.PATH },
    env: { HOME: work, PI_CODING_AGENT_DIR: agentDir, PI_OFFLINE: "1", PI_WEB_DISABLE_MCP: "1" } });
  await h.launchBrowser(() => chromium.launch({ headless: true, ...(process.env.SETTINGS_BROWSER ? { executablePath: process.env.SETTINGS_BROWSER } : {}) }));
  const context = await h.browser.newContext({ viewport: { width: 1440, height: 1000 }, reducedMotion: "reduce" });
  await context.addInitScript(() => localStorage.setItem("pi-locale", "zh-CN"));
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const method = request.method();
    const body = method === "GET" ? null : request.postDataJSON();
    const send = (json, status = 200) => route.fulfill({ json, status });
    // The fixture session keeps its page lease alive; it is unrelated to configuration saves.
    if (path === "/api/agent/provider-name/lease") { sessionLeases.push({ path, method, body }); return send({}); }
    if (method !== "GET") writes.push({ path, method, body });
    if (path.startsWith("/api/sessions") || path === "/api/agent/running") return route.continue();
    if (path === "/api/models-config") {
      if (method === "PUT") {
        const checked = validateModelsConfigPayload(body);
        assert.equal(checked.ok, true, JSON.stringify(checked));
        writeModelsConfig(checked.data, modelsPath);
        config = readModelsConfig(modelsPath);
        return send({ success: true });
      }
      return send(readModelsConfig(modelsPath));
    }
    if (path === "/api/models/enabled") return send(enabled());
    if (path === "/api/models") return send({ models: {}, modelList: Object.entries(config.providers).flatMap(([provider, entry]) => (entry.models ?? []).map((model) => ({ ...model, provider }))), defaultModel: { provider: "acceptance", modelId: "m1" } });
    if (path === "/api/models/default") return send({ defaultHarness: "pi", claudeDefaultModel: "default", claudeDefaultEffort: "default" });
    if (path === "/api/harness/pi/model") return send({ data: nativeDefault });
    if (path === "/api/harness/claude/model") return send({ data: { defaultModel: null, perModelReasoningEffort: {}, userCustomModels: [] } });
    if (path === "/api/polyforge/config") return send(host);
    if (path === "/api/polyforge/workflows") { referenceReads.push(path); return send(workflows); }
    if (path === "/api/subagents/profiles") { referenceReads.push(path); return send({ profiles: [] }); }
    if (path === "/api/settings/account") return send({ claude: { available: false, loggedIn: false, envCredentials: {} }, cells: [{ profile: "workstation", harnesses: {} }], doctorError: null });
    if (path === "/api/auth/providers") return send({ oauthProviders: [], apiKeyProviders: [] });
    if (path === "/api/claude") return send({ models: [], available: false });
    if (path === "/api/harness") return send({ harnesses: [] });
    if (path === "/api/onboarding") return send({ imageManaged: false });
    if (path === "/api/project-trust") return send({ trusted: true, status: "trusted", servers: [] });
    if (path === "/api/polyforge/status") return send({ pfPresent: false, status: null });
    if (path === "/api/home") return send({ home: work });
    return send({});
  });
  const openSettings = async () => {
    await page.getByRole("button", { name: "设置", exact: true }).first().click();
    await page.locator('.settings-section-tab').filter({ hasText: /^模型$/ }).click();
  };
  await page.goto(`${h.base}/?session=provider-name`, { waitUntil: "domcontentloaded", timeout: 120000 });
  const show = page.getByRole("button", { name: "显示侧栏", exact: true });
  if (await show.isVisible()) await show.click();
  await openSettings();
  const dialog = page.getByRole("dialog", { name: "设置", exact: true });
  const side = dialog.locator('[data-connection-id="provider:acceptance"]');
  await side.click();
  const provider = dialog.locator('[data-provider-page="flat"]');
  const detail = provider.locator('[data-provider-detail][data-active="true"]');
  const field = (label) => detail.locator(".config-field").filter({ has: page.locator(".config-field-label", { hasText: new RegExp(`^${label}$`) }) }).locator("input").first();
  const name = field("显示名称");
  const id = field("Provider 标识（高级）");
  const selectedId = () => provider.getAttribute("data-current-provider");
  assert.equal(await provider.locator(".models-provider-picker").count(), 0, "the selected connection has no global navigation");
  const drafts = dialog.locator("[data-settings-drafts]");
  const save = async () => {
    await drafts.getByRole("button", { name: "保存", exact: true }).waitFor();
    assert.equal(await dialog.getByRole("button", { name: "保存", exact: true }).count(), 1, "one visible submission surface for models.json");
    await drafts.getByRole("button", { name: "保存", exact: true }).click();
    try { await drafts.waitFor({ state: "hidden" }); }
    catch (error) {
      console.error(await provider.locator('[data-provider-footer]').innerText());
      throw error;
    }
  };
  assert.equal(await id.isVisible(), false, "internal ID is collapsed under advanced settings");
  await name.fill("  团队网关  ");
  await save();
  assert.deepEqual(config.providers.acceptance, { ...original, name: "团队网关" });
  assert.equal(await name.inputValue(), "团队网关");
  assert.equal(await selectedId(), "acceptance");
  assert.match(await side.innerText(), /团队网关/);
  await provider.getByText("团队网关", { exact: true }).waitFor();
  assert.equal(referenceReads.length, 0, "display name save never checks removal references");
  assert.deepEqual(writes.map(({ path }) => path), ["/api/models-config"], "display name writes only models.json");
  await page.screenshot({ path: join(artifacts, "01-display-name-saved.png") });
  await dialog.locator('[data-model-subpage="connections"]').click();
  await dialog.locator('[data-connections-overview]').getByText("团队网关", { exact: true }).waitFor();
  await page.screenshot({ path: join(artifacts, "02-overview-and-sidebar.png") });
  console.log("PASS: referenced acceptance saves its display name; overview, sidebar and picker update without changing identity");

  await page.reload({ waitUntil: "domcontentloaded" });
  await openSettings();
  await side.click();
  assert.equal(await name.inputValue(), "团队网关", "refresh reads the saved native name");
  await detail.getByText("高级设置", { exact: true }).click();
  assert.equal(await id.inputValue(), "acceptance");
  await id.fill("accidental-rename");
  await name.fill("团队网关更新");
  await save();
  assert.equal(config.providers.acceptance.name, "团队网关更新");
  assert.equal(config.providers["accidental-rename"], undefined, "Save never applies an unconfirmed ID proposal");
  assert.equal(referenceReads.length, 0);
  const beforeRename = writes.length;
  await provider.getByRole("button", { name: "应用标识修改", exact: true }).click();
  const refusal = provider.locator('[data-provider-footer]');
  await refusal.getByText(/default · pi:acceptance\/m1/).waitFor();
  assert.match(await refusal.innerText(), /Host · fast · pi:acceptance\/m1:high/);
  assert.match(await refusal.innerText(), /Cell workstation · product\/task · pi:acceptance\/m1:high/);
  assert.equal(writes.length, beforeRename);
  assert.equal(await selectedId(), "acceptance");
  await page.screenshot({ path: join(artifacts, "03-internal-id-blocked.png") });
  await refusal.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(artifacts, "04-reference-details.png") });
  await id.fill("occupied");
  await provider.getByRole("button", { name: "应用标识修改", exact: true }).click();
  await refusal.getByText(/Provider 标识“occupied”已存在/).waitFor();
  assert.equal(writes.length, beforeRename);
  await id.fill("acceptance");
  console.log("PASS: reload, unconfirmed ID isolation, explicit default/Host/workstation task protection and ID collision");

  await name.fill("   ");
  await save();
  assert.equal(Object.hasOwn(config.providers.acceptance, "name"), false, "blank optional name must be omitted");
  assert.equal(await name.inputValue(), "");
  assert.match(await side.innerText(), /acceptance/);
  assert.equal(await selectedId(), "acceptance");
  await dialog.locator('[data-model-subpage="connections"]').click();
  await dialog.locator('[data-connections-overview]').getByText("acceptance", { exact: true }).waitFor();
  await side.click();
  const readCount = referenceReads.length;
  await field("Base URL").fill("https://fixture.invalid/v2");
  await field("API Key").fill("!printf fixture-updated");
  await save();
  assert.deepEqual(config.providers.acceptance, { ...original, baseUrl: "https://fixture.invalid/v2", apiKey: "!printf fixture-updated" });
  assert.equal(referenceReads.length, readCount, "URL/key edits do not invoke removal guards");
  const htmlName = '<img src=x onerror="window.providerNameInjected=true">';
  await name.fill(htmlName);
  await save();
  assert.equal(config.providers.acceptance.name, htmlName);
  assert.ok((await side.textContent()).includes(htmlName));
  assert.equal(await side.locator("img").count(), 0);
  await dialog.locator('[data-model-subpage="connections"]').click();
  await dialog.locator('[data-connections-overview]').getByText(htmlName, { exact: true }).waitFor();
  assert.equal(await dialog.locator('[data-connections-overview] img[src="x"]').count(), 0);
  assert.equal(await page.evaluate(() => window.providerNameInjected), undefined);
  await side.click();
  await provider.getByRole("button", { name: "更多操作", exact: true }).click();
  await provider.getByRole("button", { name: "删除", exact: true }).click();
  await provider.getByRole("button", { name: "删除", exact: true }).click();
  await refusal.getByText(/Cell workstation · product\/task/).waitFor();
  assert.ok(config.providers.acceptance);
  console.log("PASS: blank fallback, URL/key preservation, escaped display text and existing deletion protection");

  // New IDs remain editable and have no persisted references to migrate.
  await dialog.locator('[data-model-subpage="connections"]').click();
  await dialog.getByRole("button", { name: "添加连接", exact: true }).click();
  await page.getByRole("button", { name: /第三方 API/ }).click();
  await page.waitForFunction(() => document.querySelector('[data-current-provider="custom"]'));
  await detail.getByText("高级设置", { exact: true }).click();
  await id.fill("new-gateway");
  await provider.getByRole("button", { name: "应用标识修改", exact: true }).click();
  await field("Base URL").fill("https://new.invalid/v1");
  await name.fill("新网关");
  await save();
  assert.equal(config.providers["new-gateway"].name, "新网关");
  assert.equal(config.providers.custom, undefined);
  const beforeConflict = writes.length;
  const external = structuredClone(config);
  external.providers.acceptance.baseUrl = "https://external.invalid/v1";
  writeModelsConfig(external, modelsPath);
  await name.fill("未保存草稿");
  await drafts.getByRole("button", { name: "保存", exact: true }).click();
  await refusal.getByText(/models.json 已被其他操作修改/).waitFor();
  assert.equal(writes.length, beforeConflict, "real external edits still block PUT");
  assert.equal(await name.inputValue(), "未保存草稿");
  assert.equal(readModelsConfig(modelsPath).providers.acceptance.baseUrl, "https://external.invalid/v1");
  assert.deepEqual(nativeDefault, { defaultProvider: "acceptance", defaultModel: "m1", perModelReasoningEffort: {} });
  assert.deepEqual(host.items[0].value, ["pi:acceptance/m1:high"]);
  for (const write of writes) {
    assert.ok(["/api/models-config", "/api/models/enabled"].includes(write.path), `Unexpected write: ${write.path}`);
    if (write.path === "/api/models-config") assert.ok(write.body.providers.acceptance, "all PUTs retain acceptance");
    else { assert.equal(write.body.op, "resync"); assert.deepEqual(write.body.renames, []); assert.deepEqual(write.body.modelRenames, []); }
  }
  assert.deepEqual(errors, []);
  writeFileSync(join(artifacts, "requests.json"), JSON.stringify({ writes, sessionLeases, referenceReads, errors }, null, 2));
  console.log("PASS: new ID editing and real external-conflict protection; no default, Host or workspace writes; zero browser errors");
  await context.close();
} finally { await h.close(); }
