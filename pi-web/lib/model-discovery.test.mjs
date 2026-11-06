import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

async function loadSubject(path) {
  try {
    const { createJiti } = await import("jiti");
    return createJiti(import.meta.url).import(path);
  } catch {
    return import(path);
  }
}

const { buildModelsListUrl, parseDiscoveredModels } = await loadSubject("./model-discovery.ts");
const { resolveModelDiscoveryAuth } = await loadSubject("./model-discovery-auth.ts");

test("builds protocol-appropriate model list URLs", () => {
  assert.equal(buildModelsListUrl("https://api.example.com/v1/", "openai-completions").toString(), "https://api.example.com/v1/models");
  assert.equal(buildModelsListUrl("https://api.anthropic.com", "anthropic-messages").toString(), "https://api.anthropic.com/v1/models?limit=1000");
  assert.equal(buildModelsListUrl("https://generativelanguage.googleapis.com", "google-generative-ai").toString(), "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1000");
  assert.equal(buildModelsListUrl("https://api.example.com/custom/models", "openai-responses").toString(), "https://api.example.com/custom/models");
});

test("parses OpenAI, Anthropic, Google, and string model lists", () => {
  assert.deepEqual(parseDiscoveredModels({ data: [{ id: "gpt-5" }, { id: "claude", display_name: "Claude" }] }), [
    { id: "claude", name: "Claude" },
    { id: "gpt-5" },
  ]);
  assert.deepEqual(parseDiscoveredModels({ models: [{ name: "models/gemini-2.5-pro", displayName: "Gemini 2.5 Pro" }] }), [
    { id: "gemini-2.5-pro", name: "Gemini 2.5 Pro" },
  ]);
  assert.deepEqual(parseDiscoveredModels(["zeta", "alpha", "alpha"]), [
    { id: "alpha" },
    { id: "zeta" },
  ]);
});

test("keeps capability metadata a gateway publishes (#856)", () => {
  assert.deepEqual(parseDiscoveredModels({
    data: [{
      id: "glm-5.3",
      object: "model",
      context_length: 1000000,
      max_output_tokens: 64000,
      supports_reasoning: true,
      default_effort: "high",
      can_disable_thinking: true,
      supported_efforts: ["low", "high", "max"],
    }],
  }), [{
    id: "glm-5.3",
    reasoning: true,
    // Unadvertised levels are unsupported; max is offered only when mapped.
    thinkingLevelMap: { minimal: null, medium: null, xhigh: null, max: "max" },
    contextWindow: 1000000,
    maxTokens: 64000,
  }]);
  assert.deepEqual(parseDiscoveredModels({
    models: [{
      name: "models/gemini-2.5-pro",
      displayName: "Gemini 2.5 Pro",
      inputTokenLimit: 1048576,
      outputTokenLimit: "65536",
      thinking: true,
    }],
  }), [{ id: "gemini-2.5-pro", name: "Gemini 2.5 Pro", reasoning: true, contextWindow: 1048576, maxTokens: 65536 }]);
});

test("ignores junk and ambiguous capability values (#856)", () => {
  assert.deepEqual(parseDiscoveredModels({
    data: [
      { id: "junk", context_length: 0, max_output_tokens: "abc", supports_reasoning: "yes", supported_efforts: ["high"] },
      // max_tokens means either limit depending on the gateway.
      { id: "ambiguous", max_tokens: 8192, contextWindow: 12.5 },
      { id: "efforts-only", supports_reasoning: true, supported_efforts: ["none", 3] },
      { id: "empty-efforts", reasoning: true, supported_efforts: [] },
    ],
  }), [
    { id: "ambiguous" },
    { id: "efforts-only", reasoning: true },
    { id: "empty-efforts", reasoning: true },
    { id: "junk" },
  ]);
});

test("resolves environment-backed headers without an API key", async () => {
  process.env.PI_WEB_DISCOVERY_TEST_TOKEN = "resolved-token";
  try {
    const auth = await resolveModelDiscoveryAuth("pi-web-header-only-test", {
      baseUrl: "https://example.invalid/v1",
      api: "openai-completions",
      headers: { "X-Discovery-Token": "$PI_WEB_DISCOVERY_TEST_TOKEN" },
    });
    assert.equal(auth.apiKey, undefined);
    assert.deepEqual(auth.headers, { "X-Discovery-Token": "resolved-token" });
  } finally {
    delete process.env.PI_WEB_DISCOVERY_TEST_TOKEN;
  }
});

test("resolves the effective base URL and API for the requested provider", async () => {
  const configured = await resolveModelDiscoveryAuth("pi-web-baseurl-test", {
    baseUrl: "https://example.invalid/v1/",
    api: "anthropic-messages",
  });
  assert.equal(configured.baseUrl, "https://example.invalid/v1/");
  assert.equal(configured.api, "anthropic-messages");

  // A models-only entry keeps the endpoint and protocol pi ships for that
  // provider, so discovery does not depend on models.json repeating the base URL.
  const builtin = await resolveModelDiscoveryAuth("deepseek", {
    models: [{ id: "deepseek-flash" }],
  });
  assert.equal(builtin.baseUrl, "https://api.deepseek.com");
  assert.equal(builtin.api, "openai-completions");
});

async function loadDiscoverRoute() {
  const { createJiti } = await import("jiti");
  return createJiti(import.meta.url, { tsconfigPaths: true }).import("../app/api/models-config/discover/route.ts");
}

function discoverRequest(body) {
  // The route's isApiRequestAllowed guard refuses a request carrying neither
  // a Host header the server trusts (loopback/IP/configured) nor a same-site
  // Origin; a bare `new Request(url)` has neither; node's fetch Request never
  // synthesizes one (unlike a real browser/XHR request), so every discover
  // route test must set Host explicitly, same as app/api/models-config/test/route.test.mjs.
  return new Request("http://localhost/api/models-config/discover", {
    method: "POST",
    headers: { "Content-Type": "application/json", Host: "localhost" },
    body: JSON.stringify(body),
  });
}

test("asks for a Base URL when a custom provider has none and pi ships no endpoint", async () => {
  const { POST } = await loadDiscoverRoute();
  const response = await POST(discoverRequest({ providerName: "pi-web-no-baseurl-test", provider: {} }));
  assert.equal(response.status, 400);
  assert.deepEqual(await response.json(), { error: "Base URL is required" });
});

test("a non-OpenAI catalog provider keeps its native protocol for the model list URL (review blocker 3)", async () => {
  const { POST } = await loadDiscoverRoute();
  // "anthropic" ships in pi's catalog with a native non-OpenAI protocol; the
  // request supplies neither base URL nor protocol, so the route must build
  // the Anthropic model list URL from the catalog's api — the guard's former
  // openai-completions default made that fallback unreachable.
  const auth = await resolveModelDiscoveryAuth("anthropic", {});
  assert.notEqual(auth.api, "openai-completions");

  const originalFetch = globalThis.fetch;
  let calledUrl;
  globalThis.fetch = async (input) => {
    calledUrl = typeof input === "string" ? input : input.toString();
    return new Response(JSON.stringify({ data: [{ id: "claude-model" }] }), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  };
  try {
    const response = await POST(discoverRequest({ providerName: "anthropic", provider: {} }));
    assert.equal(response.status, 200);
    assert.equal(calledUrl, buildModelsListUrl(auth.baseUrl, auth.api).toString());
    // And the OpenAI shape must NOT have been built from the same base URL.
    assert.notEqual(calledUrl, buildModelsListUrl(auth.baseUrl, "openai-completions").toString());
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("falls back to pi's built-in provider catalog when the request names no base URL (upstream #1006)", async () => {
  const { POST } = await loadDiscoverRoute();
  const originalFetch = globalThis.fetch;
  let calledUrl;
  globalThis.fetch = async (input) => {
    calledUrl = typeof input === "string" ? input : input.toString();
    return new Response(JSON.stringify({ data: [{ id: "deepseek-chat" }, { id: "deepseek-flash" }] }), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  };
  try {
    // "deepseek" ships in pi's built-in provider catalog with a known base URL
    // and protocol; the request supplies neither, so the route must resolve
    // both from lib/model-discovery-auth.ts's fallback instead of refusing
    // with "Base URL is required".
    const response = await POST(discoverRequest({ providerName: "deepseek", provider: {} }));
    assert.equal(response.status, 200);
    const body = await response.json();
    assert.deepEqual(body.models, [{ id: "deepseek-chat" }, { id: "deepseek-flash" }]);
    assert.equal(calledUrl, buildModelsListUrl("https://api.deepseek.com", "openai-completions").toString());
    assert.equal(body.endpoint, calledUrl);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("a request-supplied base URL for an unknown provider is used as-is, with no catalog involved", async () => {
  const { POST } = await loadDiscoverRoute();
  const originalFetch = globalThis.fetch;
  let calledUrl;
  globalThis.fetch = async (input) => {
    calledUrl = typeof input === "string" ? input : input.toString();
    return new Response(JSON.stringify({ data: [{ id: "custom-model" }] }), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  };
  try {
    const response = await POST(discoverRequest({
      providerName: "pi-web-custom-baseurl-test",
      provider: { baseUrl: "https://acme.example/v1", apiKey: "client-own-key" },
    }));
    assert.equal(response.status, 200);
    assert.deepEqual((await response.json()).models, [{ id: "custom-model" }]);
    assert.equal(calledUrl, "https://acme.example/v1/models");
  } finally {
    globalThis.fetch = originalFetch;
  }
});
