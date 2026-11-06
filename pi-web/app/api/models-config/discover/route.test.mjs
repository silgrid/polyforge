import "#test-env";
import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const originalAgentDir = process.env.PI_CODING_AGENT_DIR;
const originalOffline = process.env.PI_OFFLINE;
const testAgentDir = await mkdtemp(join(tmpdir(), "pi-web-discover-route-"));
process.env.PI_CODING_AGENT_DIR = testAgentDir;
process.env.PI_OFFLINE = "1";
// The audit S2 exfil scenario: a models.json entry exists for one provider,
// but the attacker names a BUILT-IN provider id so the temp runtime inherits
// its stored credential.
await writeFile(join(testAgentDir, "models.json"), JSON.stringify({
  providers: {
    acme: { baseUrl: "https://acme.example/v1", apiKey: "configured-key" },
  },
}));
// Review r1: an actual ISOLATED auth.json holding the operator's stored
// credential for the colliding built-in id — the exact precedence the SDK's
// auth resolution puts ahead of a temp models.json apiKey.
await writeFile(join(testAgentDir, "auth.json"), JSON.stringify({
  openai: { type: "api_key", key: "operator-stored-secret" },
}));;

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { POST } = await jiti.import("./route.ts");

test.after(async () => {
  if (originalAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = originalAgentDir;
  if (originalOffline === undefined) delete process.env.PI_OFFLINE;
  else process.env.PI_OFFLINE = originalOffline;
  await rm(testAgentDir, { recursive: true, force: true });
});

function discover(body) {
  // Node-constructed Requests do not materialize a Host header from the URL;
  // the trust gate reads it, so set it explicitly like a real client does.
  return new Request("http://localhost/api/models-config/discover", {
    method: "POST",
    headers: { "Content-Type": "application/json", Host: "localhost" },
    body: JSON.stringify(body),
  });
}

test("malformed bodies are refused with the typed code", async () => {
  for (const body of [null, {}, { providerName: "acme" }, { providerName: "acme", provider: { baseUrl: 1 } }]) {
    const response = await POST(discover(body));
    assert.equal(response.status, 400, JSON.stringify(body));
    assert.deepEqual(await response.json(), { error: "invalidBody" }, JSON.stringify(body));
  }
});

test("a stored credential is refused for a base URL the operator never configured", async () => {
  // providerName collides with a built-in; the request's baseUrl is foreign.
  const response = await POST(discover({
    providerName: "openai",
    provider: { baseUrl: "https://evil.example/v1" },
  }));
  assert.equal(response.status, 403);
  assert.deepEqual(await response.json(), { error: "storedCredentialBaseUrlMismatch" });

  // Also refused when a configured provider's key would be re-attached elsewhere.
  const redirected = await POST(discover({
    providerName: "acme",
    provider: { baseUrl: "https://acme.example/v1/../..//evil" },
  }));
  assert.equal(redirected.status, 403);
});

test("the configured base URL is honored (with trailing-slash tolerance) and the fetch refuses redirects", async (t) => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (endpoint, options) => {
    calls.push({ endpoint: String(endpoint), options });
    return new Response(JSON.stringify({ data: [{ id: "model-a" }] }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  };
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  const response = await POST(discover({
    providerName: "acme",
    provider: { baseUrl: "https://acme.example/v1/" },
  }));
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.deepEqual(payload.models, [{ id: "model-a" }]);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].endpoint, "https://acme.example/v1/models");
  // Audit S2: no redirect hop, and no credential attached (the request did
  // not supply one and models.json resolution finds none in the temp file).
  assert.equal(calls[0].options.redirect, "error");
  assert.equal(new Headers(calls[0].options.headers).get("authorization"), null);
});

test("a request-supplied apiKey travels to the request's own base URL with redirect refusal", async (t) => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (endpoint, options) => {
    calls.push({ endpoint: String(endpoint), options });
    return new Response(JSON.stringify({ data: [{ id: "model-a" }] }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  };
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  const response = await POST(discover({
    providerName: "brand-new",
    provider: { baseUrl: "https://custom.example/v1", apiKey: "request-own-key" },
  }));
  assert.equal(response.status, 200);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].options.redirect, "error");
  assert.equal(new Headers(calls[0].options.headers).get("authorization"), "Bearer request-own-key");
});

test("a request apiKey is sent as the exact LITERAL — the operator's stored credential for a colliding built-in id is never swapped in (review r1)", async (t) => {
  // providerName "openai" has a stored auth.json credential; the request
  // supplies its own key and a foreign URL. The SDK's auth precedence puts
  // auth.json AHEAD of a models.json apiKey, so resolving through getAuth()
  // here would leak "operator-stored-secret" to https://evil.example.
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (endpoint, options) => {
    calls.push({ endpoint: String(endpoint), options });
    return new Response(JSON.stringify({ data: [{ id: "model-a" }] }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  };
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  const response = await POST(discover({
    providerName: "openai",
    provider: { baseUrl: "https://evil.example/v1", apiKey: "request-literal-key" },
  }));
  assert.equal(response.status, 200);
  assert.equal(calls.length, 1);
  const authorization = new Headers(calls[0].options.headers).get("authorization");
  assert.equal(authorization, "Bearer request-literal-key");
  assert.notEqual(authorization, "Bearer operator-stored-secret");
  // The stored credential never appears anywhere in the outbound request.
  assert.equal(JSON.stringify(calls).includes("operator-stored-secret"), false);
});

test("an env/command expression in request-supplied credentials or headers is refused unless it matches the real config (review r1/r2)", async () => {
  // "acme" carries no matching apiKey/header in the configured fixture, so
  // every expression here is unverifiable and refused — a credential-policy
  // refusal (403), the same status the destination-mismatch gate uses, not
  // a schema error (400): the body is structurally valid.
  for (const provider of [
    { baseUrl: "https://x.example/v1", apiKey: "$OPENAI_API_KEY" },
    { baseUrl: "https://x.example/v1", apiKey: "!echo pwned" },
    { baseUrl: "https://x.example/v1", headers: { "x-probe": "${PI_WEB_PASSWORD}" } },
  ]) {
    const response = await POST(discover({ providerName: "acme", provider }));
    assert.equal(response.status, 403, JSON.stringify(provider));
    assert.deepEqual(await response.json(), { error: "invalidConfigExpression" }, JSON.stringify(provider));
  }
});
