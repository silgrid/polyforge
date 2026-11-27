// Imported by settings-refactor-285.mjs: Chinese visual/interaction coverage of real Settings components.
import assert from "node:assert/strict";
import { join } from "node:path";

export async function verifySettingsVisuals(h, { artifacts, work }) {
  const context = await h.browser.newContext({ viewport: { width: 1440, height: 1000 }, locale: "zh-CN", colorScheme: "light", reducedMotion: "reduce" });
  await context.addInitScript(() => { localStorage.setItem("pi-locale", "zh-CN"); localStorage.removeItem("pi-theme"); });
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => { if (message.type() === "error" && !message.text().includes("status of 409") && !message.text().includes("status of 503")) errors.push(message.text()); });
  const writes = [];
  let empty = false;
  let zeroClaudeModels = false;
  let detailAuthReadFails = false;
  const modelList = [
    { provider: "openai-codex", id: "gpt-5-codex", name: "GPT-5 Codex" },
    { provider: "openai-codex", id: "gpt-5-mini", name: "GPT-5 Mini" },
    { provider: "anthropic", id: "claude-sonnet", name: "Claude Sonnet" },
    { provider: "anthropic", id: "claude-opus", name: "Claude Opus" },
    { provider: "团队网关", id: "claude-sonnet", name: "Claude Sonnet · 团队" },
    { provider: "团队网关", id: "claude-opus", name: "Claude Opus · 团队" },
  ];
  const definitions = { providers: { 团队网关: { api: "anthropic-messages", baseUrl: "https://gateway.example.invalid/v1", apiKey: "fixture-only", models: modelList.filter((model) => model.provider === "团队网关").map(({ id, name }) => ({ id, name, reasoning: true, contextWindow: 200000 })) } } };
  const auth = {
    oauthProviders: [{ id: "openai-codex", name: "ChatGPT Plus / Pro", loggedIn: true, usesCallbackServer: true }, { id: "google-gemini-cli", name: "Google Gemini", loggedIn: false, usesCallbackServer: true }],
    apiKeyProviders: [{ id: "anthropic", displayName: "Anthropic API", configured: true, source: "auth", modelCount: 2 }, { id: "openai", displayName: "OpenAI", configured: false, modelCount: 2 }],
  };
  const permission = (claude) => ({
    approvalMode: { supported: claude, neutral: claude ? "ask" : null, raw: claude ? "default" : null },
    projectTrust: { trusted: true }, sandbox: { supported: claude, enabled: false, autoAllowBashIfSandboxed: false, otherKeys: [] },
    rules: claude ? { allow: ["Read"], ask: [], deny: [] } : null, denyInsteadOfPrompting: false,
    sources: { defaultMode: null, sandbox: null, rules: null, denyInsteadOfPrompting: null },
  });
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url()); const path = url.pathname; const method = route.request().method();
    const send = (json, status = 200) => route.fulfill({ json, status });
    if (route.request().headers().accept?.includes("text/event-stream")) return route.fulfill({ status: 200, contentType: "text/event-stream", body: ": isolated fixture\n\n" });
    if (path.startsWith("/api/sessions") || path === "/api/agent/running") return route.continue();
    if (path.endsWith("/lease")) return send({ running: false, subagentsActive: false, awaitingInput: false });
    if (path === "/api/provider-usage/query") return send({ providers: [] });
    if (method !== "GET") { writes.push({ path, method }); return send({ error: "visual fixture refuses mutations" }, 409); }
    if (path === "/api/auth/providers") return detailAuthReadFails ? send({ error: "isolated catalog failure" }, 503) : send(empty ? { oauthProviders: [], apiKeyProviders: [] } : auth);
    if (path === "/api/models-config") return send(empty ? { providers: {} } : definitions);
    if (path === "/api/models") return send({ modelList: empty ? [] : modelList, models: {}, defaultModel: { provider: "openai-codex", modelId: "gpt-5-codex" }, thinkingLevels: Object.fromEntries(modelList.map((model) => [`${model.provider}:${model.id}`, ["low", "medium", "high"]])) });
    if (path === "/api/models/default") return send({ defaultHarness: "pi", claudeDefaultModel: "sonnet", claudeDefaultEffort: "high" });
    if (path === "/api/models/enabled") return send({ allEnabled: true, patterns: [], stalePatterns: [], enabledTotal: 6, availableTotal: 6, scope: "global", settingsPath: "fixture/settings.json", editable: true,
      providers: ["openai-codex", "anthropic", "团队网关"].map((id) => ({ id, name: id, kind: id === "团队网关" ? "custom" : "builtin", enabledCount: 2, models: modelList.filter((model) => model.provider === id).map((model) => ({ ...model, ref: `${id}/${model.id}`, enabled: true, contextWindow: 200000 })) })) });
    if (path === "/api/harness/pi/model" || path === "/api/harness/claude/model") return send({ data: { defaultProvider: "openai-codex", defaultModel: path.includes("/pi/") ? "gpt-5-codex" : "sonnet", reasoningEffort: { neutral: "high", raw: "high" }, perModelReasoningEffort: {}, providers: [], customModels: [], userCustomModels: [] } });
    if (path === "/api/claude") return send({ available: !empty, models: empty || zeroClaudeModels ? [] : [{ value: "sonnet", displayName: "Claude Sonnet" }, { value: "opus", displayName: "Claude Opus" }], version: "2.1.329", versionRecorded: true, features: [], capabilities: [] });
    if (path === "/api/harness") return send({ harnesses: [{ id: "pi", capabilities: { loginCommands: [] } }, { id: "claude", capabilities: { loginCommands: [{ id: "auth-login", cell: false }, { id: "setup-token", cell: false }], credentialsFollowConfigRoot: true } }] });
    if (path === "/api/settings/account") return send({ claude: empty ? { available: true, loggedIn: false, envCredentials: { ANTHROPIC_BASE_URL: true } } : { available: true, loggedIn: true, authMethod: "oauth_token", configDirectory: "/fixture/claude", envCredentials: { ANTHROPIC_API_KEY: false } },
      cells: empty ? [] : [{ profile: "default", harnesses: { claude: { credential: "set", status: "ready", configRoot: "/fixture/cells/default/claude-config", source: "/fixture/operator", sourceKind: "operator", usable: true, reason: null, credentialsFollow: true, keychainHint: false } } }], doctorError: null });
    if (path === "/api/polyforge/config") return send({ pfPresent: true, pfVersion: "0.2.4", capable: true, stepTiersReason: null, file: { sha256: "visual-fixture" }, items: [{ key: "step_tiers.standard", value: ["pi:openai-codex/gpt-5-codex:high", "claude:sonnet:high"] }, { key: "step_tiers.fast", value: ["pi:openai-codex/gpt-5-mini:medium", "pi:团队网关/claude-sonnet:low"] }] });
    if (path === "/api/harness/compare") return send({ results: ["approval-mode", "sandbox", "project-trust"].map((field) => ({ field, consistent: true, values: { pi: { supported: field === "project-trust", neutral: field === "project-trust" ? "trusted" : null, raw: null }, claude: { supported: true, neutral: field === "approval-mode" ? "ask" : field === "sandbox" ? "false" : "trusted", raw: "default" } } })) });
    if (path.endsWith("/permission")) return send({ data: permission(path.includes("/claude/")) });
    if (path === "/api/claude-permissions") return send({ mode: "ask", push: false, includeHookEvents: false, session: null });
    if (path === "/api/agent-language") return send({ language: "zh-CN", polyforge: null });
    if (path === "/api/cache-miss-notices") return send({ showCacheMissNotices: true });
    if (path === "/api/tools/settings") return send({ isWindows: false, powerShellEnabled: false, codemode: "automatic", codemodeMode: "auto", codemodeInlineBudget: "auto" });
    if (path === "/api/onboarding") return send({ imageManaged: false });
    if (path === "/api/project-trust") return send({ trusted: true, status: "trusted", servers: [] });
    if (path === "/api/polyforge/status") return send({ pfPresent: false, status: null });
    if (path === "/api/home") return send({ home: work });
    return send({});
  });
  const open = async () => {
    await page.goto(`${h.base}/?session=settings-285`, { waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => document.documentElement.lang === "zh-CN");
    const show = page.getByRole("button", { name: "显示侧边栏", exact: true });
    if (await show.isVisible()) await show.click();
    await page.getByRole("button", { name: "设置", exact: true }).first().click();
  };
  await open();
  const dialog = page.getByRole("dialog", { name: "设置", exact: true });
  const tab = (text) => dialog.locator(".settings-section-tab").filter({ hasText: new RegExp(`^${text}$`) });
  const modelNav = async (id) => {
    await tab("模型").click();
    const item = dialog.locator(`[data-model-subpage="${id}"]`);
    if (!await item.isVisible()) await dialog.locator(".settings-phone-back").click();
    await item.click();
  };
  const shot = async (name) => {
    await page.evaluate(() => document.fonts.ready);
    const geometries = await dialog.evaluate((element) => [...element.querySelectorAll('.settings-dialog-surface, .settings-section-host:not([hidden]) > div, .settings-connections-overview, .settings-data-panel')].filter((node) => node.getClientRects().length).map((node) => ({ className: node.className, width: node.clientWidth, scroll: node.scrollWidth })));
    for (const geometry of geometries) assert.ok(geometry.scroll <= geometry.width + 1, `${name}: ${JSON.stringify(geometry)}`);
    assert.deepEqual(errors, [], `${name}: runtime errors`);
    await page.screenshot({ path: join(artifacts, `${name}.png`), animations: "disabled" });
  };
  await modelNav("connections");
  const overview = dialog.locator("[data-connections-overview]");
  await overview.locator('[data-connection-row="provider:团队网关"]').waitFor();
  assert.equal(await overview.locator("tbody tr").count(), 4);
  assert.equal(await dialog.locator("[data-connection-detail]:visible").count(), 0);
  assert.equal(await dialog.locator("[data-account-providers]:visible").count(), 0);
  assert.ok(!(await overview.innerText()).includes("没有已连接"));
  assert.equal(await overview.getByRole("button", { name: "添加连接", exact: true }).count(), 1);
  assert.match(await overview.locator('[data-connection-row="provider:团队网关"] .settings-state-badge').innerText(), /定义已保存/);
  assert.equal(await overview.locator('[data-connection-row="provider:团队网关"]').getByText("2 个模型", { exact: true }).count(), 1);

  detailAuthReadFails = true;
  await overview.getByRole("button", { name: "配置 ChatGPT Plus / Pro", exact: true }).click();
  await dialog.locator('[data-pi-account-login] [role="alert"]').waitFor();
  detailAuthReadFails = false;
  await dialog.locator('[data-pi-account-login]').getByRole("button", { name: "重试", exact: true }).click();
  await dialog.locator('[data-pi-account-login]').getByRole("button", { name: "断开连接", exact: true }).waitFor();
  assert.equal(await overview.count(), 0);
  await modelNav("connections");

  // The same primary action offers native API, OAuth, and the first API Key custom card.
  await overview.getByRole("button", { name: "添加连接", exact: true }).click();
  const picker = page.locator("[data-add-provider-picker]");
  await picker.waitFor();
  await page.screenshot({ path: join(artifacts, "1440-light-add-connection.png"), animations: "disabled" });
  assert.equal(await picker.locator('[data-provider-kind="custom"], [data-provider-kind="apikey"]').first().getAttribute("data-provider-kind"), "custom");
  assert.equal(await picker.locator('[data-provider-kind="oauth"]').count(), 1);
  await picker.locator('[data-provider-kind="apikey"][data-provider-available="openai"]').click();
  await dialog.locator("[data-connection-detail]:visible").waitFor();
  assert.equal(await overview.count(), 0);
  await dialog.locator('[data-pi-account-login] input[type="password"]:visible').waitFor();
  assert.equal(await dialog.locator('[data-pi-account-login] input[type="password"]:visible').count(), 1);
  await modelNav("connections");
  await overview.getByRole("button", { name: "添加连接", exact: true }).click();
  await picker.locator("[data-provider-custom]").click();
  const custom = dialog.locator('[data-provider-page="flat"]');
  await page.waitForFunction(() => document.querySelector('[data-provider-page="flat"] .models-provider-picker-field select')?.value === "provider:custom");
  await custom.getByPlaceholder("https://api.example.com/v1").fill("https://draft.example.invalid/v1");
  await modelNav("connections"); await modelNav("providers");
  assert.equal(await custom.getByPlaceholder("https://api.example.com/v1").inputValue(), "https://draft.example.invalid/v1");
  await dialog.locator("[data-settings-drafts]").getByRole("button", { name: "丢弃", exact: true }).click();
  await dialog.locator("[data-settings-drafts]").waitFor({ state: "hidden" });
  await modelNav("connections");
  await overview.getByRole("button", { name: "配置 Claude Code", exact: true }).click();
  await dialog.locator('[data-account-pane="claude"]:visible').waitFor();
  assert.equal(await overview.count(), 0);
  const cells = dialog.locator('[data-account-pane="claude"] [data-account-card="cells"]');
  assert.equal(await cells.getAttribute("open"), null);
  await cells.locator("summary").first().click();
  await cells.locator('[data-account-cell="default"]').waitFor({ state: "visible" });
  await cells.locator("summary").first().click();
  assert.equal(await dialog.locator('[data-account-pane="claude"] [data-login-command="auth-login"]').isVisible(), true);
  assert.equal(await dialog.locator('[data-account-pane="claude"] [data-account-action="save"]').isDisabled(), true);
  await shot("1440-light-claude-detail");
  assert.deepEqual(writes, [], "overview, picker and unsaved navigation must not write native settings");
  console.log("PASS: unified mixed overview, truthful status/count, one add action, native/custom navigation and selected Claude details");

  for (const { width, height, theme, prefix } of [
    { width: 1440, height: 1000, theme: "light", prefix: "1440-light" },
    { width: 1280, height: 900, theme: "dark", prefix: "1280-dark" },
    { width: 390, height: 844, theme: "light", prefix: "390-light" },
  ]) {
    await page.setViewportSize({ width, height });
    await page.emulateMedia({ colorScheme: theme });
    await page.waitForFunction((dark) => document.documentElement.classList.contains("dark") === dark, theme === "dark");
    for (const [id, label] of [["connections", "connections"], ["default", "default"], ["list", "models"], ["tiers", "tiers"]]) {
      await modelNav(id);
      if (id === "tiers") await dialog.locator("[data-native-tiers] .settings-tier-card").first().waitFor();
      await shot(`${prefix}-${label}`);
    }
    await tab("常规").click();
    if (width <= 640 && !await dialog.locator('[data-general-pane="conversation"]').isVisible()) await dialog.getByRole("button", { name: "界面与对话", exact: true }).click();
    await dialog.locator('[data-general-pane="conversation"] .settings-general-section').first().waitFor();
    await shot(`${prefix}-general`);
    await tab("权限").click();
    if (width <= 640 && !await dialog.locator('[data-permission-pane="approval"]').isVisible()) await dialog.getByRole("button", { name: "审批模式", exact: true }).click();
    await dialog.locator('[data-permission-zone="unified-approval"]').waitFor();
    await shot(`${prefix}-permissions`);
  }
  await modelNav("connections");
  await overview.getByRole("button", { name: "添加连接", exact: true }).click();
  await picker.waitFor();
  await picker.getByRole("textbox").focus();
  await page.screenshot({ path: join(artifacts, "390-light-add-connection.png"), animations: "disabled" });
  await page.keyboard.press("Escape");
  await picker.waitFor({ state: "detached" });
  assert.equal(await dialog.isVisible(), true, "Escape closes only the add picker");
  await modelNav("default");
  await dialog.locator(".settings-model-default-select").selectOption(JSON.stringify({ harness: "pi", provider: "anthropic", id: "claude-sonnet" }));
  await modelNav("connections");
  const summary = dialog.locator("[data-settings-drafts]");
  await summary.waitFor();
  const summarySave = await summary.getByRole("button", { name: "保存", exact: true }).boundingBox();
  assert.ok(summarySave && summarySave.y + summarySave.height <= 844, "the unified save action fits the phone viewport");
  await shot("390-light-draft-summary");
  await summary.getByRole("button", { name: "丢弃", exact: true }).click();
  await overview.getByRole("button", { name: "配置 Claude Code", exact: true }).click();
  const credentialForm = dialog.locator('[data-account-pane="claude"] [data-account-card="credential-form"]');
  await credentialForm.locator('[data-account-field="value"]').fill("fixture-only-unsaved");
  await credentialForm.locator('[data-account-target="interactive"]').check();
  const scopedSave = credentialForm.locator('[data-account-action="save"]');
  await scopedSave.scrollIntoViewIfNeeded();
  const scopedBox = await scopedSave.boundingBox();
  assert.ok(scopedBox && scopedBox.y >= 0 && scopedBox.y + scopedBox.height <= 844, "the scoped credential action is not clipped");
  await shot("390-light-claude-detail-actions");
  await summary.getByRole("button", { name: "丢弃", exact: true }).click();
  await modelNav("connections");
  await page.emulateMedia({ colorScheme: "dark" });
  await page.waitForFunction(() => document.documentElement.classList.contains("dark"));
  await shot("390-dark-connections");
  assert.deepEqual(writes, [], "the visual checks do not save credentials or model settings");
  await page.keyboard.press("Tab");
  assert.ok(await page.evaluate(() => document.activeElement?.closest('[role="dialog"]') !== null));
  assert.deepEqual(errors, []);
  console.log("PASS: Chinese 1440 light / 1280 dark / 390 screenshots for overview, default, inventory, tiers, General and Permissions without horizontal overflow");

  zeroClaudeModels = true;
  await page.setViewportSize({ width: 1440, height: 1000 });
  await open(); await modelNav("connections");
  await overview.locator('[data-connection-row="harness:claude"]').getByText("0 个模型", { exact: true }).waitFor();
  console.log("PASS: a successfully read zero-model Claude catalog is distinct from unknown");

  // BASE_URL alone is not a credential: a successful empty read has one empty state.
  empty = true;
  await page.setViewportSize({ width: 1440, height: 1000 });
  await open(); await modelNav("connections");
  await overview.getByText(/还没有连接/).waitFor();
  assert.equal(await dialog.locator('[data-account-providers]:visible').count(), 0);
  assert.equal(await overview.locator(".config-empty-state").count(), 1);
  assert.equal(await overview.locator('[data-connection-row="harness:claude"]').count(), 0, "BASE_URL alone must not be shown as a configured credential");
  assert.equal(await dialog.locator('[data-model-subpage="account-claude"]').isVisible(), true);
  console.log("PASS: BASE_URL-only Claude is not authenticated; one genuine empty state and reachable Claude configuration");
  await context.close();
}
