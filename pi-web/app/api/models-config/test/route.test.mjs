import "#test-env";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const originalAgentDir = process.env.PI_CODING_AGENT_DIR;
const originalOffline = process.env.PI_OFFLINE;
const testAgentDir = await mkdtemp(join(tmpdir(), "pi-web-model-test-route-"));
process.env.PI_CODING_AGENT_DIR = testAgentDir;
process.env.PI_OFFLINE = "1";
process.env.ACME_EXPR_KEY = "resolved-expr-key";
await writeFile(join(testAgentDir, "models.json"), JSON.stringify({
  providers: {
    acme: { baseUrl: "https://acme.example/v1", apiKey: "configured-key" },
    openai: { baseUrl: "https://api.openai.com/v1" },
    exprProvider: { baseUrl: "https://expr.example/v1", apiKey: "$ACME_EXPR_KEY" },
  },
}));
// Review r1: an actual ISOLATED auth.json holding the operator's stored
// credential for the colliding built-in id — the SDK's auth resolution puts
// auth.json AHEAD of a temp models.json apiKey, which is exactly the
// precedence the request-key path must never be able to reach.
await writeFile(join(testAgentDir, "auth.json"), JSON.stringify({
  openai: { type: "api_key", key: "operator-stored-secret" },
}));

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { POST } = await jiti.import("./route.ts");
const { redirectRefusingFetch } = await jiti.import("@/lib/model-discovery-guards");

test.after(async () => {
  if (originalAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = originalAgentDir;
  if (originalOffline === undefined) delete process.env.PI_OFFLINE;
  else process.env.PI_OFFLINE = originalOffline;
  delete process.env.ACME_EXPR_KEY;
  await rm(testAgentDir, { recursive: true, force: true });
});

function testModel(body) {
  return new Request("http://localhost/api/models-config/test", {
    method: "POST",
    headers: { "Content-Type": "application/json", Host: "localhost" },
    body: JSON.stringify(body),
  });
}

test("a stored credential is refused for a base URL the operator never configured", async () => {
  const response = await POST(testModel({
    providerName: "openai",
    provider: { baseUrl: "https://evil.example/v1" },
    model: { id: "gpt-x" },
  }));
  assert.equal(response.status, 403);
  assert.deepEqual(await response.json(), { ok: false, error: "storedCredentialBaseUrlMismatch" });
});

test("malformed bodies are refused with the typed code", async () => {
  for (const body of [
    null,
    { providerName: "acme" },
    { providerName: "acme", provider: { baseUrl: "https://acme.example/v1" } },
    { providerName: "acme", provider: { baseUrl: "https://acme.example/v1" }, model: { id: "" } },
    { providerName: "acme", provider: { baseUrl: "https://acme.example/v1", weird: 1 }, model: { id: "m" } },
  ]) {
    const response = await POST(testModel(body));
    assert.equal(response.status, 400, JSON.stringify(body));
    assert.deepEqual(await response.json(), { ok: false, error: "invalidBody" }, JSON.stringify(body));
  }
});

test("a non-JSON content type is refused before the body is read", async () => {
  const response = await POST(new Request("http://localhost/api/models-config/test", {
    method: "POST",
    headers: { "Content-Type": "text/plain", Host: "localhost" },
    body: "{}",
  }));
  assert.equal(response.status, 415);
});

test("a model-level baseUrl override does not smuggle a stored credential past the provider-level gate (review r1)", async () => {
  // provider.baseUrl is the configured URL, but the SDK's modelFromJson
  // gives model.baseUrl precedence — the effective destination is foreign.
  const response = await POST(testModel({
    providerName: "openai",
    provider: { baseUrl: "https://api.openai.com/v1" },
    model: { id: "gpt-x", baseUrl: "https://evil.example/v1" },
  }));
  assert.equal(response.status, 403);
  assert.deepEqual(await response.json(), { ok: false, error: "storedCredentialBaseUrlMismatch" });
});

function sseOkResponse() {
  const events = [
    'data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-x","choices":[{"index":0,"delta":{"role":"assistant","content":"OK"},"finish_reason":null}]}',
    'data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}',
    "data: [DONE]",
  ].join("\n\n") + "\n\n";
  return new Response(events, {
    status: 200,
    headers: { "Content-Type": "text/event-stream" },
  });
}

test("an omitted provider.api resolves from the model entry, the catalog, or the OpenAI default (review blocker)", async (t) => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (input, init) => {
    calls.push({ input: String(input), init });
    return sseOkResponse();
  };
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  // 1. model-level protocol: the model entry names the api, the provider
  //    omits it (the guard now passes "" instead of a default).
  const modelLevel = await POST(testModel({
    providerName: "acme",
    provider: { baseUrl: "https://acme.example/v1", apiKey: "own-key" },
    model: { id: "m", api: "openai-completions" },
  }));
  const modelPayload = await modelLevel.json();
  assert.equal(modelLevel.status, 200, JSON.stringify(modelPayload));
  assert.equal(modelPayload.ok, true, JSON.stringify(modelPayload));
  assert.equal(calls.at(-1).input.startsWith("https://acme.example/v1"), true, calls.at(-1).input);

  // 2. catalog default: a catalog provider with neither provider.api nor
  //    model.api — pi's catalog supplies the provider's native protocol
  //    (deepseek ships openai-completions).
  const catalog = await POST(testModel({
    providerName: "deepseek",
    provider: { baseUrl: "https://api.deepseek.com", apiKey: "own-key" },
    model: { id: "deepseek-chat" },
  }));
  const catalogPayload = await catalog.json();
  assert.equal(catalog.status, 200, JSON.stringify(catalogPayload));
  assert.equal(catalogPayload.ok, true, JSON.stringify(catalogPayload));
  assert.ok(calls.at(-1).input.startsWith("https://api.deepseek.com"), calls.at(-1).input);

  // 3. custom provider with no protocol anywhere: the temp load first fails
  //    with 'no "api" specified' and the route retries with the
  //    OpenAI-compatible default the guard used to pin.
  const custom = await POST(testModel({
    providerName: "brand-new",
    provider: { baseUrl: "https://custom.example/v1", apiKey: "own-key" },
    model: { id: "m" },
  }));
  const customPayload = await custom.json();
  assert.equal(custom.status, 200, JSON.stringify(customPayload));
  assert.equal(customPayload.ok, true, JSON.stringify(customPayload));
  assert.ok(calls.at(-1).input.startsWith("https://custom.example/v1"), calls.at(-1).input);
  assert.equal(new Headers(calls.at(-1).init.headers).get("authorization"), "Bearer own-key");
});

test("a request apiKey is sent as the exact LITERAL on an isolated credential path — the operator's stored credential is never swapped in (review r1)", async (t) => {
  // providerName "openai" carries a stored auth.json credential; the request
  // supplies its own key and a foreign URL. Under the old code, getAuth()
  // resolved the STORED credential (auth precedence) and attached it to the
  // request-chosen destination.
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (input, init) => {
    calls.push({ input: String(input), init });
    return sseOkResponse();
  };
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  const response = await POST(testModel({
    providerName: "openai",
    provider: { baseUrl: "https://evil.example/v1", api: "openai-completions", apiKey: "request-literal-key" },
    model: { id: "gpt-x" },
  }));
  const payload = await response.json();
  assert.equal(response.status, 200);
  assert.equal(payload.ok, true, JSON.stringify(payload));
  assert.equal(payload.responseText, "OK");

  assert.equal(calls.length, 1);
  assert.ok(calls[0].input.startsWith("https://evil.example/v1"), calls[0].input);
  // The completion transport refuses redirects (review r1: second-hop exfil).
  assert.equal(calls[0].init.redirect, "error");
  const authorization = new Headers(calls[0].init.headers).get("authorization");
  assert.equal(authorization, "Bearer request-literal-key");
  // The operator's stored credential appears nowhere in the outbound request.
  assert.equal(JSON.stringify(calls).includes("operator-stored-secret"), false);
});

test("a configured provider's own apiKey travels as the request literal to its configured URL (feature kept usable)", async (t) => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (input, init) => {
    calls.push({ input: String(input), init });
    return sseOkResponse();
  };
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  // "acme" is configured in models.json with its own apiKey — the panel's
  // normal test request carries it, and that literal goes to the configured
  // URL.
  const response = await POST(testModel({
    providerName: "acme",
    provider: { baseUrl: "https://acme.example/v1", api: "openai-completions", apiKey: "configured-key" },
    model: { id: "m" },
  }));
  const payload = await response.json();
  assert.equal(payload.ok, true, JSON.stringify(payload));
  assert.ok(calls[0].input.startsWith("https://acme.example/v1"));
  assert.equal(new Headers(calls[0].init.headers).get("authorization"), "Bearer configured-key");
});

test("a saved provider's own $ENV apiKey round-trips and resolves via the real config (review r2: the regression this fix restores)", async (t) => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (input, init) => {
    calls.push({ input: String(input), init });
    return sseOkResponse();
  };
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  // ModelsConfig.tsx sends the saved provider object back UNMODIFIED on a
  // "test" click, including its literal "$ACME_EXPR_KEY" apiKey string. That
  // must resolve through the operator's real environment, at the provider's
  // own configured URL — not be rejected outright, and not be sent as the
  // literal string "$ACME_EXPR_KEY".
  const response = await POST(testModel({
    providerName: "exprProvider",
    provider: { baseUrl: "https://expr.example/v1", api: "openai-completions", apiKey: "$ACME_EXPR_KEY" },
    model: { id: "m" },
  }));
  const payload = await response.json();
  assert.equal(payload.ok, true, JSON.stringify(payload));
  assert.equal(new Headers(calls[0].init.headers).get("authorization"), "Bearer resolved-expr-key");
});

test("a forged expression apiKey is refused even at the provider's own configured URL (review r2)", async () => {
  // Same provider/baseUrl as the real config, but a DIFFERENT expression —
  // an attacker who can reach this route must not be able to make the
  // server resolve an arbitrary env var merely by matching an existing
  // provider's destination.
  const response = await POST(testModel({
    providerName: "exprProvider",
    provider: { baseUrl: "https://expr.example/v1", api: "openai-completions", apiKey: "$SOME_OTHER_SECRET" },
    model: { id: "m" },
  }));
  assert.equal(response.status, 403);
  assert.deepEqual(await response.json(), { ok: false, error: "invalidConfigExpression" });
});

test("a verified expression apiKey is still refused at a foreign destination (review r2)", async () => {
  // The exact real expression, but pointed at a base URL that is not this
  // provider's configured one — the destination gate still applies.
  const response = await POST(testModel({
    providerName: "exprProvider",
    provider: { baseUrl: "https://evil.example/v1", api: "openai-completions", apiKey: "$ACME_EXPR_KEY" },
    model: { id: "m" },
  }));
  assert.equal(response.status, 403);
  assert.deepEqual(await response.json(), { ok: false, error: "storedCredentialBaseUrlMismatch" });
});

test("request-supplied credentials and headers with SDK expression syntax are refused unless they match the real config (review r1/r2)", async () => {
  // "acme" carries no matching apiKey/header in the configured fixture, so
  // every provider-level expression here is unverifiable and refused as a
  // credential-policy decision (403), not a schema error.
  for (const provider of [
    { baseUrl: "https://x.example/v1", api: "openai-completions", apiKey: "$OPENAI_API_KEY" },
    { baseUrl: "https://x.example/v1", api: "openai-completions", apiKey: "!echo pwned" },
    { baseUrl: "https://x.example/v1", api: "openai-completions", headers: { "x-probe": "${PI_WEB_PASSWORD}" } },
  ]) {
    const response = await POST(testModel({ providerName: "acme", provider, model: { id: "m" } }));
    assert.equal(response.status, 403, JSON.stringify(provider));
    assert.deepEqual(await response.json(), { ok: false, error: "invalidConfigExpression" }, JSON.stringify(provider));
  }
  // Model-level header location is covered too.
  const modelHeaders = await POST(testModel({
    providerName: "acme",
    provider: { baseUrl: "https://acme.example/v1", api: "openai-completions" },
    model: { id: "m", headers: { "x-probe": "$SECRET" } },
  }));
  assert.equal(modelHeaders.status, 400);
  assert.deepEqual(await modelHeaders.json(), { ok: false, error: "invalidConfigExpression" });
});

test("the completion transport refuses a redirect from the configured URL — no second hop occurs (review r1)", async (t) => {
  assert.equal(typeof redirectRefusingFetch, "function");

  // A real local server: the first response is a 302 to a second server that
  // would count as the exfil hop. redirect:"error" must make the fetch THROW
  // and leave the second server with zero requests.
  const first = createServer((_, res) => {
    res.statusCode = 302;
    res.setHeader("Location", "http://127.0.0.1:" + secondPort + "/leak");
    res.end();
  });
  const secondHits = [];
  const second = createServer((_, res) => {
    secondHits.push(1);
    res.end("leaked");
  });
  await new Promise((resolve) => first.listen(0, "127.0.0.1", resolve));
  await new Promise((resolve) => second.listen(0, "127.0.0.1", resolve));
  const firstPort = first.address().port;
  const secondPort = second.address().port;
  t.after(async () => {
    await Promise.all([
      new Promise((resolve) => first.close(resolve)),
      new Promise((resolve) => second.close(resolve)),
    ]);
  });

  await assert.rejects(
    () => redirectRefusingFetch(`http://127.0.0.1:${firstPort}/chat/completions`, { method: "POST" }),
  );
  assert.equal(secondHits.length, 0);
});
