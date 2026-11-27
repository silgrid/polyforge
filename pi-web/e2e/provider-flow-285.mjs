// carries: components/ModelsConfig*, components/PiAccountLogin*, components/AccountSettings*, components/HarnessModelSettings*, components/SettingsDrafts*, components/ClaudeCustomModels*, components/EnabledModelsSection*, components/SettingsPanel*, components/PolyforgeConfig*, components/NativeModelTiers*, app/settings.css, lib/i18n/messages/*
// Real components, isolated HTTP fixtures. The temporary standalone route is removed in finally.
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { createHarness, writeHostSession, root } from "./harness.mjs";
const artifacts = process.env.PROVIDER_FLOW_ARTIFACTS || join(root, "test-results/provider-flow-285");
mkdirSync(artifacts, { recursive: true });
const work = mkdtempSync(join(tmpdir(), "provider-flow-285-"));
const project = join(work, "project"); mkdirSync(project, { recursive: true });
const agentDir = join(work, "agent");
writeHostSession(agentDir, { id: "provider-flow", cwd: project, text: "Provider flow fixture", timestamp: "2026-11-27T00:00:00Z" });
const fixtureDir = join(root, "app/e2e-provider-flow-fixture");
assert.equal(existsSync(fixtureDir), false, "never overwrite a route");
mkdirSync(fixtureDir);
writeFileSync(join(fixtureDir, "page.tsx"), `"use client";
import { useState } from "react";
import { ApiKeyDetail } from "@/components/PiAccountLogin";
import { ModelsConfig } from "@/components/ModelsConfig";
import { useEnabledModels } from "@/components/EnabledModelsSection";
import { I18nProvider } from "@/hooks/useI18n";
function Fixture() { const enabled = useEnabledModels(null); const [models, setModels] = useState(false); return models ? <ModelsConfig enabledModels={enabled} onClose={() => setModels(false)} /> : <><ApiKeyDetail provider={{id:"fixtureOpenAI",displayName:"Fixture OpenAI",configured:false,modelCount:1}} onRefresh={() => {}} /><button onClick={() => setModels(true)}>Open standalone models</button></>; }
export default function Page() { return <I18nProvider><Fixture /></I18nProvider>; }
`);
const h = createHarness();
const writes = []; const errors = []; const reads = [];
let config = { providers: { acceptance: { name: "团队网关", baseUrl: "https://fixture.invalid/v1", api: "openai-completions", apiKey: "!printf fixture-only", customMetadata: { retained: true }, models: [{ id: "m1", name: "主力模型", contextWindow: 4096, extra: "keep" }] }, other: { name: "备用连接", baseUrl: "https://other.invalid", models: [{ id: "other" }] } } };
let configured = false; let keyFail = true; let releaseKey = null; let holdKey = false; let resyncFail = false;
let holdReferences = false; let releaseReferences = null;
let taskFixtures = false; let chainConflict = true;
let hostHash = "fixture-hash"; let hostCandidates = ["pi:acceptance/m1:high"];
const chainSources = {
  default: [{ workspace: "/fixture/default", project: "product", hash: "default-yaml", models: ["pi:acceptance/m1:high"] }],
  beta: [{ workspace: "/fixture/beta", project: "product", hash: "beta-yaml", models: ["pi:acceptance/m1:high"] }, { workspace: "/fixture/docs", project: "product", hash: "docs-yaml", models: ["pi:acceptance/m1:low"] }],
};
const workflows = (profile) => ({ capable: true, workspaces: (chainSources[profile] ?? []).map((entry) => ({
  workspace: entry.workspace,
  declared: { workspace: entry.workspace, yaml: { path: `${entry.workspace}/.polyforge.yaml`, sha256: entry.hash }, steps: { [entry.project]: { task: { declared: true, models: entry.models } } } },
  workflows: { workspace: entry.workspace, projects: [entry.project], daemon_reachable: true, write_capable: true, bundles: [], bundle_errors: [], task: { [entry.project]: { models: entry.models, from: "workspace.task" } } },
})) });
const initial = structuredClone(config);
const nativeDefault = { defaultProvider: "acceptance", defaultModel: "m1", perModelReasoningEffort: {} };
const enabled = () => ({ allEnabled: true, patterns: [], stalePatterns: [], scope: "global", settingsPath: "fixture/settings.json", editable: true, enabledTotal: 2, availableTotal: 2,
  providers: [...Object.entries(config.providers).map(([id, entry]) => ({ id, name: entry.name ?? id, kind: "custom", enabledCount: entry.models?.length ?? 0, models: (entry.models ?? []).map((model) => ({ ...model, name: model.name ?? model.id, ref: `${id}/${model.id}`, enabled: true })) })), { id: "fixtureOpenAI", name: "Ant Ling", kind: "apikey", enabledCount: 1, models: [{ id: "native-model", name: "原生模型", ref: "fixtureOpenAI/native-model", enabled: true, contextWindow: 8192 }] }] });
try {
  await h.startServer({ name: "provider-flow-285", distDir: ".next/e2e/provider-flow-285", logFile: join(artifacts, "browser-server.log"), from: { PATH: process.env.PATH }, env: { HOME: work, PI_CODING_AGENT_DIR: agentDir, PI_OFFLINE: "1", PI_WEB_DISABLE_MCP: "1" } });
  await h.launchBrowser(() => chromium.launch({ headless: true, ...(process.env.SETTINGS_BROWSER ? { executablePath: process.env.SETTINGS_BROWSER } : {}) }));
  const context = await h.browser.newContext({ viewport: { width: 1440, height: 1000 }, reducedMotion: "reduce", colorScheme: "light" });
  await context.addInitScript(() => { localStorage.setItem("pi-locale", "zh-CN"); localStorage.removeItem("pi-theme"); });
  const page = await context.newPage(); page.setDefaultTimeout(15000);
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("**/api/**", async (route) => {
    const request = route.request(); const url = new URL(request.url()); const path = url.pathname; const method = request.method();
    const body = method === "GET" ? null : request.postDataJSON();
    const send = (json, status = 200) => route.fulfill({ json, status });
    if (path.startsWith("/api/sessions") || path === "/api/agent/running") return route.continue();
    if (path.endsWith("/lease")) return send({});
    if (method !== "GET") writes.push({ path, method, body: path.includes("api-key") ? { apiKey: "[fixture redacted]" } : body });
    else reads.push(path);
    if (path === "/api/models-config") { if (method === "PUT") config = structuredClone(body); return send(method === "PUT" ? { success: true } : config); }
    if (path === "/api/models/enabled") return method === "PUT" && resyncFail ? send({ error: "fixture resync failed" }, 409) : send(enabled());
    if (path === "/api/models-config/discover") return send({ models: [{ id: "discovered", name: "发现模型" }], endpoint: "https://fixture.invalid/models" });
    if (path === "/api/models-config/test") return send({ success: true, latencyMs: 5 });
    if (path === "/api/models") return send({ models: {}, modelList: Object.entries(config.providers).flatMap(([provider, entry]) => (entry.models ?? []).map((model) => ({ ...model, provider }))) });
    if (path === "/api/models/default") return send({ defaultHarness: "pi" });
    if (path === "/api/harness/pi/model") return send({ data: nativeDefault });
    if (path === "/api/harness/claude/model") return send({ data: { userCustomModels: [], perModelReasoningEffort: {} } });
    if (path === "/api/polyforge/config") {
      if (method === "POST") {
        if (body.host) { assert.equal(body.key, "step_tiers.fast"); assert.equal(body.expect_state.file_sha256, hostHash); assert.equal(body.profile, undefined); assert.equal(body.cwd, undefined); hostCandidates = body.value; hostHash = "host-after"; }
        else {
          assert.equal(body.profile, "beta"); assert.equal(body.key, "steps.product.task.models");
          const target = chainSources.beta.find((entry) => entry.workspace === body.cwd);
          assert.ok(target, "writes name a chosen workspace in this Cell"); assert.equal(body.expect_state.file_sha256, target.hash);
          if (chainConflict) return send({ error: "fixture CAS conflict", code: "config-cas-409" }, 409);
          target.models = body.value; target.hash = `${target.hash}-after`;
        }
        return send({ ok: true });
      }
      return send({ pfPresent: true, capable: true, pfVersion: "0.2.4", file: { sha256: hostHash }, items: url.searchParams.has("profile") ? [] : [{ key: "step_tiers.fast", value: hostCandidates }] });
    }
    if (path === "/api/polyforge/workflows") return send(taskFixtures ? workflows(url.searchParams.get("profile") ?? "default") : { capable: true, workspaces: [] });
    if (path === "/api/polyforge/cells") return send({ supported: true, cells: taskFixtures ? ["default", "beta", "empty"].map((profile) => ({ profile, state: "serving", valid: true, workspaces: [], running: 0, pending: 0 })) : [] });
    if (path === "/api/polyforge/detect") return send({ pfPresent: true, pfVersion: "0.2.4", line: "public", cellsSupported: true, daemon: { reachable: true } });
    if (path === "/api/polyforge/step-tiers") return send({ capable: true, tiers: [{ tier: "fast", candidates: hostCandidates }], defined: { pi: ["acceptance/m1", "other/other"], claude: [] } });
    if (path === "/api/subagents/profiles") {
      if (holdReferences) await new Promise((resolve) => { releaseReferences = resolve; });
      return send({ profiles: [] });
    }
    if (path === "/api/settings/account") return send({ claude: { available: true, loggedIn: true, envCredentials: {} }, cells: [], doctorError: null });
    if (path === "/api/auth/providers") return send({ oauthProviders: [{ id: "fixture-oauth", name: "Fixture OAuth", loggedIn: false, usesCallbackServer: false }], apiKeyProviders: [{ id: "fixtureOpenAI", displayName: "Ant Ling", configured, modelCount: 1 }] });
    if (path === "/api/auth/api-key/fixtureOpenAI") {
      assert.equal(method, "POST"); assert.ok(["fixture-secret", "standalone-secret"].includes(body.apiKey));
      if (holdKey) await new Promise((resolve) => { releaseKey = resolve; });
      if (keyFail) return send({ error: "fixture credential refused" }, 503);
      configured = true; return send({ success: true });
    }
    if (path === "/api/claude") return send({ available: true, models: [] });
    if (path === "/api/harness") return send({ harnesses: [] });
    if (path === "/api/onboarding") return send({ imageManaged: false });
    if (path === "/api/project-trust") return send({ trusted: true, servers: [] });
    if (path === "/api/polyforge/status") return send({ pfPresent: false, status: null });
    if (path === "/api/home") return send({ home: work });
    return send({});
  });
  await page.goto(`${h.base}/?session=provider-flow`, { waitUntil: "domcontentloaded", timeout: 120000 });
  await page.getByRole("button", { name: "设置", exact: true }).first().click();
  await page.locator(".settings-section-tab").filter({ hasText: /^模型$/ }).click();
  const dialog = page.getByRole("dialog", { name: "设置", exact: true });
  const provider = dialog.locator('[data-provider-page="flat"]');
  const detail = provider.locator('[data-provider-detail][data-active="true"]');
  const field = (label) => detail.locator(".config-field").filter({ has: page.locator(".config-field-label", { hasText: new RegExp(`^${label}$`) }) }).locator("input").first();
  const drafts = dialog.locator("[data-settings-drafts]");
  const save = drafts.getByRole("button", { name: "保存", exact: true });
  const discard = drafts.getByRole("button", { name: "丢弃", exact: true });
  const oneSave = async () => { await save.waitFor(); assert.equal(await dialog.getByRole("button", { name: "保存", exact: true }).count(), 1); assert.equal(await dialog.getByRole("button", { name: "丢弃", exact: true }).count(), 1); };
  const assertNoOverflow = async () => {
    assert.equal(await dialog.evaluate((node) => node.scrollWidth <= node.clientWidth + 1), true, "dialog has no horizontal overflow");
    const overflow = await dialog.locator('.config-detail:visible, .config-panel-surface:visible, .polyforge-workflow-card:visible').evaluateAll((nodes) => nodes.filter((node) => node.scrollWidth > node.clientWidth + 1).map((node) => node.className));
    assert.deepEqual(overflow, [], "visible editor containers have no horizontal overflow");
  };
  const shots = async (name) => {
    for (const [width, height, theme] of [[1440, 1000, "light"], [1280, 900, "dark"], [390, 844, "light"]]) {
      await page.setViewportSize({ width, height }); await page.emulateMedia({ colorScheme: theme });
      await page.waitForFunction((dark) => document.documentElement.classList.contains("dark") === dark, theme === "dark");
      await oneSave();
      if (width === 390) await drafts.locator(".settings-draft-link").click();
      await dialog.locator(name === "native-api" ? '[data-api-key-detail] input' : '[data-provider-detail][data-active="true"] input').first().waitFor({ state: "visible" });
      const box = await save.boundingBox(); assert.ok(box && box.x >= 0 && box.y >= 0 && box.x + box.width <= width && box.y + box.height <= height, "save remains reachable");
      await assertNoOverflow();
      if (width === 390 && name === "native-api") assert.equal(await dialog.locator('.settings-dialog-title').innerText(), "Ant Ling");
      await page.screenshot({ path: join(artifacts, `${width}-${theme}-${name}.png`), animations: "disabled" });
    }
    await page.setViewportSize({ width: 1440, height: 1000 }); await page.emulateMedia({ colorScheme: "light" });
  };
  await dialog.locator('[data-connection-id="provider:acceptance"]').click();
  await field("显示名称").fill("团队网关更新"); await field("Base URL").fill("https://fixture.invalid/v2");
  assert.equal(await provider.locator(".models-provider-picker").count(), 0);
  assert.equal(await provider.getByRole("button", { name: "添加 Provider", exact: true }).count(), 0);
  await oneSave(); await shots("custom");
  await provider.locator('[data-manage-custom-models]:visible').click();
  await provider.locator('[data-connection-model="m1"]').click();
  await field("Name").fill("待保存模型名"); await shots("model");
  await provider.locator('[data-provider-back="model"]').click();
  assert.equal(await provider.getAttribute("data-current-provider"), "acceptance");
  assert.equal(await field("显示名称").inputValue(), "团队网关更新");
  await provider.locator('[data-connection-model="m1"]').click(); assert.equal(await field("Name").inputValue(), "待保存模型名");
  await field("Name").fill("主力模型"); await provider.locator('[data-provider-back="model"]').click();
  assert.equal(writes.length, 0, "editing and local navigation never writes");
  await save.click(); await drafts.waitFor({ state: "hidden" });
  assert.deepEqual(writes.map(({ path }) => path), ["/api/models-config"]);
  assert.deepEqual(config.providers.acceptance, { ...initial.providers.acceptance, name: "团队网关更新", baseUrl: "https://fixture.invalid/v2" });
  assert.equal(reads.includes("/api/subagents/profiles"), false, "metadata changes do not invoke reference guards");
  console.log("PASS: one current connection, subordinate model round-trip, one save, only models-config PUT and stable references");

  await provider.getByRole("button", { name: "添加模型", exact: true }).click();
  await field("ID \\*").fill("new-model"); resyncFail = true;
  await save.click(); await provider.getByText(/已保存.*同步|重试.*同步/).first().waitFor();
  const putCount = writes.filter(({ path }) => path === "/api/models-config").length;
  assert.equal(await discard.isDisabled(), true, "a landed write's pending synchronization cannot be discarded");
  await provider.locator('[data-provider-back="model"]').click();
  await field("显示名称").fill("同步期间的新草稿");
  await discard.click();
  await drafts.getByRole("button", { name: "重试", exact: true }).waitFor();
  await page.waitForFunction(() => [...document.querySelectorAll('[data-settings-drafts] button')].some((button) => button.textContent === "丢弃" && button.disabled));
  assert.equal(await discard.isDisabled(), true, "discarding new edits retains the failed sync and its mappings");
  resyncFail = false; await drafts.getByRole("button", { name: "重试", exact: true }).click(); await drafts.waitFor({ state: "hidden" });
  assert.equal(writes.filter(({ path }) => path === "/api/models-config").length, putCount, "resync retry never repeats config PUT");
  const beforeDiscover = writes.length;
  await provider.getByRole("button", { name: "导入模型…", exact: true }).click();
  await provider.getByText("发现模型", { exact: true }).waitFor();
  assert.deepEqual(writes.slice(beforeDiscover).map(({ path }) => path), ["/api/models-config/discover"], "discovery does not save");
  console.log("PASS: model add retains partial-resync retry and discovery is an immediate read action without implicit Save");
  await dialog.locator('[data-connection-id="provider:other"]').click();
  await field("显示名称").fill("备用草稿");
  holdReferences = true;
  await detail.getByRole("button", { name: "更多操作", exact: true }).click();
  await detail.getByRole("button", { name: "删除", exact: true }).click();
  await detail.getByRole("button", { name: "删除", exact: true }).click();
  await page.waitForFunction(() => document.querySelector('[data-provider-page="flat"]')?.inert);
  await dialog.locator('[data-connection-id="provider:acceptance"]').click();
  assert.equal(await drafts.locator('.config-button-primary').isDisabled(), true, "reference-check busy blocks the shared save");
  await assert.rejects(field("显示名称").fill("must-not-land", { timeout: 200 }), /Timeout/);
  assert.ok(releaseReferences); releaseReferences(); holdReferences = false;
  await page.waitForFunction(() => !document.querySelector('[data-provider-page="flat"]')?.inert);
  await discard.click(); await drafts.waitFor({ state: "hidden" });
  await dialog.locator('[data-connection-id="provider:acceptance"]').click();
  assert.equal(await field("显示名称").inputValue(), "团队网关更新");
  console.log("PASS: delayed provider removal locks edits and shared save; discard restores only the uncommitted removal");

  await provider.locator('[data-provider-back="connection"]:visible').click();
  await dialog.getByRole("button", { name: "添加连接", exact: true }).click();
  assert.equal(await page.getByRole("button", { name: /Fixture OAuth/ }).count(), 1, "OAuth entry remains available");
  await page.getByRole("button", { name: /Ant Ling/ }).click();
  const native = dialog.locator('[data-api-key-detail="fixtureOpenAI"]');
  const key = native.getByLabel("API Key", { exact: true });
  await key.waitFor(); assert.equal(await key.inputValue(), "");
  assert.equal(await dialog.getByText("Ant Ling", { exact: true }).filter({ visible: true }).count(), 1);
  assert.equal(await dialog.locator('[data-account-back]:visible').count(), 1);
  assert.equal(await native.locator(".config-detail-header").count(), 0);
  await key.fill("fixture-secret"); await shots("native-api");
  const beforeKey = writes.length; await key.press("Enter"); assert.equal(writes.length, beforeKey, "managed Enter does not bypass the sole visible Save");
  await save.click(); await native.getByRole("alert").getByText("fixture credential refused").waitFor();
  assert.equal(await key.inputValue(), "fixture-secret"); await oneSave();
  keyFail = false; holdKey = true; await save.click();
  await page.waitForFunction(() => document.querySelector('[data-api-key-detail] input')?.disabled);
  await key.dispatchEvent("keydown", { key: "Enter" });
  assert.equal(writes.length, beforeKey + 2, "busy Enter cannot duplicate requests");
  releaseKey(); holdKey = false;
  await native.getByRole("status").getByText("已保存", { exact: true }).waitFor();
  await drafts.waitFor({ state: "hidden" }); assert.equal(await key.inputValue(), "");
  await native.getByText("凭据已配置", { exact: true }).waitFor();
  await key.fill("unsaved-only"); await dialog.locator('[data-connection-id="provider:acceptance"]').click();
  await save.waitFor(); await drafts.locator(".settings-draft-link").click(); await key.waitFor({ state: "visible" });
  assert.equal(await key.inputValue(), "unsaved-only"); await discard.click(); assert.equal(await key.inputValue(), "");
  assert.deepEqual(nativeDefault, { defaultProvider: "acceptance", defaultModel: "m1", perModelReasoningEffort: {} });
  assert.equal(writes.some(({ path }) => ["/api/models/default", "/api/polyforge/config", "/api/polyforge/workflows"].includes(path)), false);
  console.log("PASS: Add connection/native API has one vendor header/back/save, real failed and successful POSTs, Enter guard, hidden draft reveal and OAuth entry");

  // Removed main entries retain real, object-specific model editors.
  const modelNav = dialog.locator('.settings-models > .config-split-view > .config-sidebar');
  for (const old of ["providers", "claude", "chains"]) assert.equal(await modelNav.locator(`[data-model-subpage="${old}"]`).count(), 0);
  for (const entry of ["connections", "default", "list", "tiers"]) assert.equal(await modelNav.locator(`[data-model-subpage="${entry}"]`).count(), 1);
  await dialog.locator('[data-manage-connection-models]:visible').click();
  await provider.getByText("原生模型", { exact: true }).waitFor();
  const contextRow = provider.locator('.enabled-models-row').filter({ hasText: "原生模型" });
  assert.equal(await contextRow.locator('input').isVisible(), false, "optional context fields start folded");
  await contextRow.locator('summary').click();
  await contextRow.locator('input').fill("16000");
  await oneSave();
  await dialog.locator('[data-model-subpage="connections"]').click();
  await drafts.locator('.settings-draft-link').click();
  await provider.getByText("Ant Ling · 模型", { exact: true }).waitFor();
  await provider.locator('[data-provider-back="managed"]').click();
  await key.waitFor();
  await dialog.locator('[data-manage-connection-models]:visible').click();
  assert.equal(await contextRow.locator('input').inputValue(), "16000");
  await discard.click(); await drafts.waitFor({ state: "hidden" });
  const capture = async (name, navigate, primary) => {
    for (const [width, height, theme] of [[1440, 1000, "light"], [1280, 900, "dark"], [390, 844, "light"]]) {
      await page.setViewportSize({ width, height }); await page.emulateMedia({ colorScheme: theme });
      await page.waitForFunction((dark) => document.documentElement.classList.contains("dark") === dark, theme === "dark");
      await navigate(width);
      await assertNoOverflow();
      if (primary) { const bounds = await primary().boundingBox(); assert.ok(bounds && bounds.x >= 0 && bounds.y >= 0 && bounds.x + bounds.width <= width && bounds.y + bounds.height <= height, `${name}: primary action in viewport`); }
      await page.screenshot({ path: join(artifacts, `${width}-${theme}-${name}.png`), animations: "disabled" });
    }
    await page.setViewportSize({ width: 1440, height: 1000 }); await page.emulateMedia({ colorScheme: "light" });
  };
  await capture("native-models", async (width) => {
    if (width === 390) { await dialog.locator('[data-connection-id="apikey:fixtureOpenAI"]').click(); await dialog.locator('[data-manage-connection-models]:visible').click(); }
    await provider.getByText("原生模型", { exact: true }).waitFor();
  }, () => provider.locator('[data-provider-back="managed"]'));
  await dialog.locator('[data-model-subpage="account-claude"]').click();
  await capture("claude", async (width) => { if (width === 390) await dialog.locator('[data-model-subpage="account-claude"]').click(); await dialog.locator('[data-claude-connection-home]').waitFor(); }, () => dialog.locator('[data-configure-claude-api]:visible'));
  await dialog.locator('[data-manage-connection-models]:visible').click();
  await capture("claude-models", async (width) => {
    if (width === 390) { await dialog.locator('[data-model-subpage="account-claude"]').click(); await dialog.locator('[data-manage-connection-models]:visible').click(); }
    await dialog.locator('[data-claude-custom-models]').waitFor();
  }, () => dialog.locator('[data-claude-models-back]'));
  await dialog.locator('[data-claude-models-back]').click();
  await dialog.locator('[data-model-subpage="connections"]').click();
  await capture("main-sidebar", async () => {}, null);
  assert.equal(writes.some(({ path }) => path === "/api/claude-models-migration"), false);
  console.log("PASS: removed main entries have native/custom/Claude replacements, model return preserves context drafts, advanced optional fields and Chinese responsive shells");

  // The advanced entry asks for a real Cell/project; every write retains its own CAS scope.
  taskFixtures = true;
  await modelNav.locator('.settings-model-advanced > summary').click();
  await modelNav.locator('[data-model-task-entry]').click();
  await dialog.locator('[data-task-chain-entry]').waitFor();
  assert.equal(await dialog.locator('[data-pf-pane="host"]').isVisible(), false);
  assert.equal(await dialog.locator('.polyforge-workflow-card:visible').count(), 0, "no guessed default target");
  await capture("advanced-task-entry", async () => {}, () => dialog.locator('[data-task-cell-picker]'));
  await dialog.locator('[data-task-cell-picker]').selectOption("empty");
  await dialog.locator('[data-pf-pane="cell:empty"] [data-task-no-workspace]').waitFor();
  assert.equal(await dialog.locator('[data-pf-pane="cell:empty"] .polyforge-workflow-row').count(), 0);
  await dialog.locator('[data-task-cell-picker]').selectOption("beta");
  const workflow = dialog.locator('[data-pf-pane="cell:beta"] .polyforge-workflow-card');
  const projectSelect = workflow.getByLabel("工作区项目", { exact: true });
  await projectSelect.waitFor(); assert.equal(await projectSelect.inputValue(), "");
  assert.equal(await workflow.locator('.polyforge-workflow-row').count(), 0);
  assert.deepEqual(await projectSelect.locator('option:not([disabled])').allTextContents(), ["product · /fixture/beta", "product · /fixture/docs"], "same-name projects remain separate workspace targets");
  await projectSelect.selectOption(JSON.stringify(["/fixture/beta", "product"]));
  const task = workflow.locator('.polyforge-workflow-row').filter({ has: page.locator('.polyforge-workflow-id', { hasText: /^task$/ }) });
  const candidate = task.locator('input');
  await candidate.fill("pi:other/other:low");
  await oneSave();
  await projectSelect.selectOption(JSON.stringify(["/fixture/docs", "product"]));
  assert.equal(await candidate.inputValue(), "", "project navigation retains a separate chain draft");
  await candidate.fill("pi:other/other:high");
  await drafts.locator('.settings-draft-item').filter({ hasText: "/fixture/docs" }).waitFor();
  assert.equal(await drafts.locator('.settings-draft-item').count(), 2, "same-name project drafts have distinct workspace labels");
  await drafts.locator('.settings-draft-item').filter({ hasText: "/fixture/docs" }).getByRole('button', { name: "丢弃", exact: true }).click();
  await drafts.locator('.settings-draft-item').filter({ hasText: "/fixture/docs" }).waitFor({ state: "hidden" });
  assert.equal(await drafts.locator('.settings-draft-item').count(), 1);
  assert.ok((await drafts.innerText()).includes("/fixture/beta"), "discard settles only its workspace draft");
  await capture("advanced-task-chain", async () => { await drafts.locator('.settings-draft-link').click(); await candidate.waitFor(); await candidate.scrollIntoViewIfNeeded(); }, () => save);
  assert.equal(await projectSelect.inputValue(), JSON.stringify(["/fixture/beta", "product"]));
  await dialog.locator('[data-task-cell-picker]').selectOption("default");
  await save.click(); // footer reveals and submits the beta draft, even from another Cell
  await workflow.getByRole('alert').waitFor();
  assert.equal(await candidate.inputValue(), "pi:other/other:low", "CAS refusal retains candidate draft");
  const refused = writes.filter(({ path }) => path === "/api/polyforge/config").at(-1).body;
  assert.deepEqual(refused, { action: "set", key: "steps.product.task.models", value: ["pi:acceptance/m1:high", "pi:other/other:low"], cwd: "/fixture/beta", profile: "beta", expect_state: { file_sha256: "beta-yaml", values: {} } });
  chainConflict = false; await save.click(); await drafts.waitFor({ state: "hidden" });
  assert.equal(chainSources.default[0].hash, "default-yaml"); assert.equal(chainSources.beta[1].hash, "docs-yaml");
  await projectSelect.selectOption(JSON.stringify(["/fixture/docs", "product"]));
  await candidate.fill("pi:other/other:high"); await save.click(); await drafts.waitFor({ state: "hidden" });
  assert.deepEqual(writes.filter(({ path }) => path === "/api/polyforge/config").at(-1).body, { action: "set", key: "steps.product.task.models", value: ["pi:acceptance/m1:low", "pi:other/other:high"], cwd: "/fixture/docs", profile: "beta", expect_state: { file_sha256: "docs-yaml", values: {} } });
  assert.deepEqual(chainSources.beta[0].models, ["pi:acceptance/m1:high", "pi:other/other:low"], "saving the same-name project in docs cannot change beta");
  await page.locator('.settings-section-tab').filter({ hasText: /^模型$/ }).click();
  await dialog.locator('[data-model-subpage="tiers"]').click();
  const tiers = dialog.locator('[data-native-tiers]'); await tiers.locator('summary').filter({ hasText: "编辑候选顺序" }).click();
  await tiers.locator('textarea').fill("pi:acceptance/m1:high\npi:other/other:low");
  await save.click(); await drafts.waitFor({ state: "hidden" });
  assert.deepEqual(writes.filter(({ path }) => path === "/api/polyforge/config").at(-1).body, { host: true, key: "step_tiers.fast", action: "set", value: ["pi:acceptance/m1:high", "pi:other/other:low"], expect_state: { file_sha256: "fixture-hash" } });
  assert.equal(writes.some(({ path }) => path === "/api/models/default"), false);
  const beforeContextLink = writes.length;
  await dialog.locator('[data-model-subpage="default"]').click();
  await dialog.locator('[data-model-page-pane="default"] .settings-model-advanced > summary').click();
  await dialog.getByRole('button', { name: "选择任务环境与步骤链", exact: true }).click();
  await dialog.locator('[data-task-cell-picker]').waitFor();
  assert.equal(await dialog.locator('[data-task-cell-picker]').inputValue(), "", "default-model context link opens the explicit picker afresh");
  assert.equal(await dialog.locator('.polyforge-workflow-card:visible').count(), 0);
  assert.equal(writes.length, beforeContextLink);
  console.log("PASS: advanced target picker and real chain editor, cross-project/Cell draft retention, refused CAS retry, exact workspace/profile/hash and separate Host tier scope");

  const standalone = await context.newPage(); standalone.on("pageerror", (error) => errors.push(error.message));
  let releaseStandalone = null;
  // Reuse the exact same HTTP fixture interception for the standalone document.
  await standalone.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/auth/api-key/fixtureOpenAI") {
      assert.deepEqual(route.request().postDataJSON(), { apiKey: "standalone-secret" });
      writes.push({ path, method: "POST", body: { apiKey: "[standalone fixture redacted]" } });
      return route.fulfill({ json: { success: true } });
    }
    if (path === "/api/models-config") {
      if (route.request().method() === "PUT") { await new Promise((resolve) => { releaseStandalone = resolve; }); config = route.request().postDataJSON(); writes.push({ path, method: "PUT", body: config }); }
      return route.fulfill({ json: route.request().method() === "PUT" ? { success: true } : config });
    }
    return route.fulfill({ json: path === "/api/models/enabled" ? enabled() : { oauthProviders: [], apiKeyProviders: [] } });
  });
  await standalone.goto(`${h.base}/e2e-provider-flow-fixture`, { waitUntil: "domcontentloaded", timeout: 120000 });
  await standalone.waitForFunction(() => document.documentElement.lang === "zh-CN");
  const standaloneKey = standalone.locator("[data-api-key-detail]");
  await standaloneKey.getByLabel("API Key", { exact: true }).fill("standalone-secret");
  assert.equal(await standaloneKey.getByRole("button", { name: "保存", exact: true }).count(), 1);
  const beforeStandalone = writes.length;
  await standaloneKey.getByLabel("API Key", { exact: true }).evaluate((input) => {
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
  });
  await standaloneKey.getByRole("status").waitFor(); assert.equal(writes.length, beforeStandalone + 1);
  await standalone.getByRole("button", { name: "Open standalone models" }).click();
  const standaloneModels = standalone.getByRole("dialog");
  await standaloneModels.getByRole("button", { name: "团队网关更新", exact: true }).click();
  const standaloneName = standaloneModels.locator(".config-field").filter({ has: standalone.locator(".config-field-label", { hasText: /^显示名称$/ }) }).locator("input");
  await standaloneName.fill("独立编辑器");
  await standaloneModels.getByRole("button", { name: "保存", exact: true }).click();
  await standalone.waitForFunction(() => document.querySelector('.settings-provider-lock')?.disabled);
  await assert.rejects(standaloneName.fill("must-not-overwrite", { timeout: 200 }), /Timeout/);
  assert.ok(releaseStandalone); releaseStandalone();
  await standaloneModels.getByRole("button", { name: "已保存", exact: true }).waitFor();
  assert.equal(config.providers.acceptance.name, "独立编辑器");
  console.log("PASS: standalone API-key Enter and models.json fallback Save persist without DraftContext");
  // A first connection must not need a pre-existing custom provider to open its models.
  config = { providers: {} }; configured = false;
  await page.reload({ waitUntil: "domcontentloaded" });
  await page.getByRole("button", { name: "设置", exact: true }).first().click();
  await page.locator('.settings-section-tab').filter({ hasText: /^模型$/ }).click();
  await dialog.locator('[data-model-subpage="connections"]').click();
  await dialog.getByRole('button', { name: "添加连接", exact: true }).click();
  await page.getByRole('button', { name: /Ant Ling/ }).click();
  await dialog.locator('[data-manage-connection-models]:visible').click();
  await provider.getByText("原生模型", { exact: true }).waitFor();
  assert.equal(await provider.getByRole('button', { name: "添加 Provider", exact: true }).count(), 0);
  await provider.locator('[data-provider-back="managed"]').click(); await key.waitFor();
  assert.deepEqual(errors, []);
  writeFileSync(join(artifacts, "requests.json"), JSON.stringify({ writes, errors }, null, 2));
  console.log("PASS: first native connection with empty models.json retains its own model editor and credential return; zero browser errors");
  await context.close();
} finally { await h.close(); rmSync(fixtureDir, { recursive: true, force: true }); }
