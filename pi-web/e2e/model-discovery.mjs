import assert from "node:assert/strict";

// The UI must preserve omitted protocols so discovery can resolve them through
// pi's catalogue. These routes isolate the panel from credentials and upstreams.
export async function checkModelDiscovery(page) {
  const waitForModelId = async (locator, expected) => {
    const deadline = Date.now() + 30_000;
    while (Date.now() < deadline) {
      if ((await locator.inputValue().catch(() => "")) === expected) return;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    throw new Error(`the edited model id never read back as ${expected}`);
  };
  const config = { providers: {
    anthropic: { models: [{ id: "claude-existing" }] },
    relay: { baseUrl: "https://example.invalid/v1", api: "openai-responses", models: [{ id: "relay-existing", name: "Relay Existing" }] },
  } };
  const discoveries = [];
  let saved;
  const configRoute = "**/api/models-config";
  const discoveryRoute = "**/api/models-config/discover";
  await page.route(configRoute, (route) => {
    if (route.request().method() === "PUT") {
      saved = route.request().postDataJSON();
      return route.fulfill({ json: { success: true } });
    }
    return route.fulfill({ json: config });
  });
  await page.route(discoveryRoute, (route) => {
    discoveries.push(route.request().postDataJSON());
    return route.fulfill({ json: {
      models: [{ id: "discovered-fixture" }], endpoint: "https://example.invalid/v1/models",
    } });
  });
  try {
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    const settings = page.getByRole("dialog", { name: "Settings", exact: true });
    // The models.json editor is embedded in the unified Models page.
    await settings.getByRole("button", { name: "Models", exact: true }).click();
    const host = settings.locator('.settings-section-host[data-settings-section="models"]:not([hidden])');
    // The models.json editor is the flat pi Providers subpage (T3): a page
    // provider picker + model picker instead of the nested editor's sidebar.
    await host.locator('[data-model-subpage="providers"]').click();
    const providerSelect = host.getByLabel("Provider", { exact: true });
    const modelSelect = host.getByLabel("Model", { exact: true });
    const api = settings.locator(".config-field-label").filter({ hasText: /^API$/ }).locator("..").locator("select");
    await providerSelect.selectOption("provider:anthropic");
    await modelSelect.locator("option").first().waitFor({ state: "attached" });
    assert.equal(await modelSelect.locator("option").filter({ hasText: /^claude-existing$/ }).count(), 1, "A model without a name is listed by its id");
    // A real Provider -> model -> Provider round trip: enter the model form,
    // edit its id draft, then return through the picker's Provider settings.
    await modelSelect.selectOption("0");
    const modelId = settings.getByPlaceholder("model-id", { exact: true });
    await modelId.waitFor({ state: "visible" });
    await modelId.fill("claude-edited");
    await modelSelect.selectOption("__provider__");
    await api.waitFor({ state: "visible" });
    await modelSelect.selectOption("0");
    await waitForModelId(modelId, "claude-edited");
    await modelSelect.selectOption("__provider__");
    await providerSelect.selectOption("provider:relay");
    await modelSelect.locator("option").filter({ hasText: /^Relay Existing$/ }).first().waitFor({ state: "attached" });
    assert.equal(await modelSelect.locator("option").filter({ hasText: /^Relay Existing$/ }).count(), 1, "A model is listed by its models.json name");
    // provider -> model -> provider: the model picker's Provider settings entry
    // returns to the parent form even when the Provider select already names it.
    await modelSelect.selectOption("__provider__");
    await api.waitFor({ state: "visible" });
    await providerSelect.selectOption("provider:anthropic");
    assert.equal(await api.inputValue(), "", "A missing protocol stays unspecified in the editor");
    const discover = async () => {
      const response = page.waitForResponse((reply) => reply.url().endsWith("/api/models-config/discover"));
      await settings.getByRole("button", { name: "Import models…", exact: true }).click();
      await response;
    };
    await discover();
    assert.equal(discoveries[0].providerName, "anthropic");
    assert.equal(discoveries[0].provider.api, undefined, "Discovery must reach the catalog protocol fallback");
    assert.equal(discoveries[0].provider.baseUrl, undefined);

    await providerSelect.selectOption("provider:relay");
    assert.equal(await api.inputValue(), "openai-responses");
    await discover();
    assert.equal(discoveries[1].provider.api, "openai-responses", "Explicit protocols must stay authoritative");
    assert.equal(discoveries[1].provider.baseUrl, "https://example.invalid/v1");

    const saveResponse = page.waitForResponse((reply) => reply.url().endsWith("/api/models-config") && reply.request().method() === "PUT");
    await settings.getByRole("button", { name: "Save", exact: true }).click();
    await saveResponse;
    assert.equal(saved.providers.anthropic.api, undefined, "Visiting a provider must not persist a guessed protocol");
    assert.equal(saved.providers.relay.api, "openai-responses");
    await settings.getByRole("button", { name: "Close", exact: true }).click();
    console.log("PASS: discovery preserves catalog protocols and explicit overrides without changing them on Save");
  } finally {
    await page.unroute(configRoute);
    await page.unroute(discoveryRoute);
  }
}
