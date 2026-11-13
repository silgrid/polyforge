/**
 * Pure typed guards for the models-config discovery/test routes (audit S2).
 *
 * Both routes resolve a provider credential through a TEMP models.json built
 * from the request body and then send it to a request-supplied baseUrl. The
 * stored-credential exfil path: a request naming a provider id that a
 * built-in provider also answers to inherits the built-in's auth resolution,
 * so `ModelRuntime.getAuth()` hands out the STORED key (auth.json /
 * AuthStorage) and the route would attach it to any URL the client chose.
 *
 * The policy (audit S2):
 * - A request-supplied apiKey may go where the request says (it is the
 *   client's own key) — but only when it is a LITERAL: expression syntax
 *   (`$VAR`, `${VAR}`, leading `!`) that the SDK's config resolver would
 *   evaluate against the SERVER's environment (or run as a shell command)
 *   is never treated as a portable, request-chosen literal.
 * - review r2: an expression-syntax apiKey/header is not always attacker
 *   input — ModelsConfig.tsx round-trips the operator's OWN saved provider
 *   (including a legitimately `$ENV`/`!command`-configured apiKey) back to
 *   these routes on every "test" click, unmodified. Rejecting the syntax
 *   outright therefore broke testing for every provider configured that
 *   way. The fix: an expression value is accepted ONLY when it is BYTE-FOR-
 *   BYTE identical to what is already persisted for that provider name in
 *   the operator's REAL models.json (`verifyConfigExpressionsAreConfigured`)
 *   — an attacker cannot forge a new expression, only replay the exact one
 *   already authorized for that provider — and even then it is never
 *   treated as a portable `requestApiKey`: it is resolved exclusively
 *   through the ordinary "configured" path below, so it still requires the
 *   request's EFFECTIVE destination to match that provider's configured
 *   base URL.
 * - Every other resolved credential — the stored one, env-inherited ones,
 *   and a verified-configured expression — may only be attached when the
 *   request's EFFECTIVE destination (the model's baseUrl when the model
 *   entry carries one, else the provider's) is the provider's OPERATOR-
 *   CONFIGURED base URL (the entry in the real ~/.pi/agent/models.json). A
 *   provider absent from models.json has no configured URL, so stored
 *   credentials are never attached for it.
 *
 * review r2 (review blocker): the pinned S2/S8 contract asked for the body
 * to be validated by zod. The structural validation below is a SHARED zod
 * schema/safeParse path — the discover and test routes parse through the
 * exact same schemas, so the accepted request shape cannot drift between
 * the siblings — while the credential and effective-destination policy
 * stays in the pure guard functions the routes call afterwards.
 *
 * Typed codes only — never English prose (repo convention, see
 * lib/fs-manage-guards.ts).
 */

import { z } from "zod";
import { isRecord } from "./type-guards";

export type ModelDiscoveryRefusalReason =
  | "invalidBody"
  | "invalidConfigExpression"
  | "storedCredentialBaseUrlMismatch";

/**
 * Whether a string would be interpreted by the SDK's config resolver
 * (`resolve-config-value.js`) as an environment reference or a shell
 * command: a leading `!` runs the rest as a command, `$NAME` / `${NAME}`
 * interpolate server environment variables. Request-supplied credential
 * and header values are LITERALS on these routes, so any such shape is
 * refused before it can reach the resolver.
 */
export function isConfigExpression(value: string): boolean {
  return value.startsWith("!") || /\$[A-Za-z_{]/.test(value);
}

function hasConfigExpressionValue(headers: Record<string, string>): boolean {
  return Object.values(headers).some((value) => isConfigExpression(value));
}

/**
 * Header values that must be LITERALS: model- and modelOverrides-level
 * headers flow into the temp runtime the request drives, so expression
 * syntax there is refused structurally. The refine carries the typed
 * refusal code as its message; `refusalFromZodError` maps it back.
 */
const literalHeaderValuesSchema = z
  .record(z.string(), z.string())
  .refine((headers) => !hasConfigExpressionValue(headers), "invalidConfigExpression");

/**
 * Provider-level headers ACCEPT expression syntax at the schema layer
 * (review r2): a saved provider legitimately round-trips its own
 * `$ENV`/`!command` headers, and `verifyConfigExpressionsAreConfigured`
 * checks those for authenticity once the operator's real config is known.
 */
const providerHeaderValuesSchema = z.record(z.string(), z.string());

const recordValueSchema = z.record(z.string(), z.unknown());

/** Keys the SDK's `ModelOverrideSchema` accepts per modelOverrides entry. */
const modelOverrideSchema = z.strictObject({
  name: z.string().optional(),
  reasoning: z.boolean().optional(),
  thinkingLevelMap: recordValueSchema.optional(),
  inputLimits: recordValueSchema.optional(),
  cost: recordValueSchema.optional(),
  promptCache: recordValueSchema.optional(),
  contextWindow: z.number().optional(),
  maxTokens: z.number().optional(),
  samplingParams: recordValueSchema.optional(),
  headers: literalHeaderValuesSchema.optional(),
  compat: recordValueSchema.optional(),
});

/** Mirrors the field set the SDK's `ModelDefinitionSchema` accepts on a models.json model. */
const modelDiscoveryModelSchema = z.strictObject({
  id: z.string().trim().min(1),
  name: z.string().trim().min(1).optional(),
  api: z.string().trim().min(1).optional(),
  baseUrl: z.string().trim().min(1).optional(),
  reasoning: z.boolean().optional(),
  thinkingLevelMap: recordValueSchema.optional(),
  input: z.array(z.enum(["text", "image"])).optional(),
  inputLimits: recordValueSchema.optional(),
  cost: recordValueSchema.optional(),
  promptCache: recordValueSchema.optional(),
  contextWindow: z.number().optional(),
  maxTokens: z.number().optional(),
  samplingParams: recordValueSchema.optional(),
  headers: literalHeaderValuesSchema.optional(),
  compat: recordValueSchema.optional(),
});

/**
 * Mirrors the field set the SDK's `ProviderConfigSchema` supports — a saved
 * provider carrying `name`, `authHeader`, or `oauth: "radius"` is a
 * legitimate configuration and must pass, not read as an unknown key
 * (strict object: unrecognized keys are refused). `apiKey` and provider-
 * level `headers` accept expression syntax here; their authenticity policy
 * is `verifyConfigExpressionsAreConfigured`, which the routes run once the
 * operator's real config is known.
 */
const modelDiscoveryProviderSchema = z.strictObject({
  // Optional (upstream #1006): a request naming a built-in provider id, or
  // an entry that only lists models, relies on pi's own provider catalog to
  // resolve the endpoint — see model-discovery-auth.ts's fallback and the
  // discover route's "Base URL is required" handling for the unresolvable
  // case. An EXPLICITLY empty string is still refused (`min(1)` only runs
  // when the key is present) so a caller cannot send `baseUrl: ""` to mean
  // the same as omitting it.
  baseUrl: z.string().trim().min(1).optional(),
  api: z.string().trim().min(1).optional(),
  apiKey: z.string().trim().min(1).optional(),
  name: z.string().trim().min(1).optional(),
  authHeader: z.boolean().optional(),
  oauth: z.literal("radius").optional(),
  headers: providerHeaderValuesSchema.optional(),
  compat: recordValueSchema.optional(),
  modelOverrides: z.record(z.string(), modelOverrideSchema).optional(),
  // The panel sends the loaded provider entry, which legitimately carries its
  // configured models; also tolerates the draft-spread `models: undefined`.
  models: z.array(modelDiscoveryModelSchema).optional(),
});

const modelDiscoveryBodySchema = z.strictObject({
  providerName: z.string().trim().min(1),
  provider: modelDiscoveryProviderSchema,
  // The test route reads the `model` entry from the same body and validates
  // it separately through `validateModelDiscoveryModel` (the shared zod
  // model schema); it is intentionally not validated here.
  model: z.unknown().optional(),
});

export interface ModelDiscoveryProvider {
  /** "" when the request named none at all — the caller falls back to pi's provider catalog. */
  baseUrl: string;
  api: string;
  apiKey?: string;
  headers: Record<string, string>;
  extra: Record<string, unknown>;
}

export interface ModelDiscoveryModel {
  id: string;
  /** The model's own protocol, when the request supplies one. */
  api?: string;
  /** The model's own baseUrl override, when the request supplies one. */
  baseUrl?: string;
  /** The validated model entry, ready for the temp models.json. */
  entry: Record<string, unknown>;
}

export type ModelDiscoveryBodyValidation =
  | { ok: true; providerName: string; provider: ModelDiscoveryProvider }
  | { ok: false; reason: ModelDiscoveryRefusalReason };

export type ModelDiscoveryModelValidation =
  | { ok: true; model: ModelDiscoveryModel }
  | { ok: false; reason: ModelDiscoveryRefusalReason };

/**
 * Maps a zod failure to the route's typed refusal code. The only non-
 * structural refusal the schemas can produce is the literal-header
 * expression refine, which carries the typed code as its message; every
 * other issue is a shape problem and reads as `invalidBody`.
 */
function refusalFromZodError(error: z.ZodError): ModelDiscoveryRefusalReason {
  return error.issues.some((issue) => issue.message === "invalidConfigExpression")
    ? "invalidConfigExpression"
    : "invalidBody";
}

/**
 * Shared zod validation of `{ providerName, provider }` (audit S2, review
 * r2): both the discover and the test route parse through
 * `modelDiscoveryBodySchema`. On success the validated provider is
 * assembled for the temp models.json — `extra` carries exactly the
 * SDK-supported optional fields the request supplied.
 */
export function validateModelDiscoveryProvider(body: unknown): ModelDiscoveryBodyValidation {
  const parsed = modelDiscoveryBodySchema.safeParse(body);
  if (!parsed.success) return { ok: false, reason: refusalFromZodError(parsed.error) };
  const { providerName, provider } = parsed.data;
  const extra: Record<string, unknown> = {};
  for (const key of ["name", "authHeader", "oauth", "compat", "modelOverrides", "models"] as const) {
    if (provider[key] !== undefined) extra[key] = provider[key];
  }
  return {
    ok: true,
    providerName,
    provider: {
      // "" rather than undefined: callers treat a falsy baseUrl as "fall back
      // to pi's provider catalog" without needing to special-case undefined.
      baseUrl: provider.baseUrl ?? "",
      // "" rather than undefined: callers treat a falsy api as "fall back
      // to pi's provider catalog" without needing to special-case undefined.
      // Deliberately NOT defaulted to "openai-completions" here: the catalog
      // resolves the protocol for built-in providers (upstream #1006), and a
      // default at this layer would make the route's `provider.api ||
      // resolved.api` fallback unreachable for a non-OpenAI provider whose
      // request omits the protocol (review blocker 3).
      api: provider.api ?? "",
      ...(provider.apiKey ? { apiKey: provider.apiKey } : {}),
      headers: provider.headers ?? {},
      extra,
    },
  };
}

/**
 * Shared zod validation of a discovery/test request's `model` entry,
 * mirroring the SDK's `ModelDefinitionSchema`. The model's own `baseUrl`
 * (which the SDK gives precedence over the provider's, see `modelFromJson`)
 * is surfaced so callers can gate the credential on the EFFECTIVE
 * destination, not just the provider's URL.
 */
export function validateModelDiscoveryModel(raw: unknown): ModelDiscoveryModelValidation {
  const parsed = modelDiscoveryModelSchema.safeParse(raw);
  if (!parsed.success) return { ok: false, reason: refusalFromZodError(parsed.error) };
  const model = parsed.data;
  return {
    ok: true,
    model: {
      id: model.id,
      ...(model.api ? { api: model.api } : {}),
      ...(model.baseUrl ? { baseUrl: model.baseUrl } : {}),
      entry: { ...model, id: model.id },
    },
  };
}

/** Normalize a base URL for comparison: trim, drop trailing slashes. */
export function normalizeBaseUrlForCompare(url: string): string {
  return url.trim().replace(/\/+$/, "");
}

export interface ModelDiscoveryCredentialRequest {
  /** The request's chosen provider base URL (trimmed). */
  baseUrl: string;
  /** Plain-string apiKey supplied by the request, when present. */
  requestApiKey?: string;
  /** The model entry's own baseUrl override, when the request supplies one. */
  modelBaseUrl?: string | null;
  /** The provider's operator-configured base URL (real models.json), if any. */
  configuredBaseUrl?: string | null;
}

export type ModelDiscoveryCredentialDecision =
  | { attach: true; source: "requestApiKey" | "configuredBaseUrl" }
  | { attach: false; reason: "storedCredentialBaseUrlMismatch" };

/**
 * The destination the completion request actually hits: the SDK's
 * `modelFromJson` gives a model's own `baseUrl` precedence over the
 * provider's, so an entry carrying both resolves to the model's URL.
 */
export function resolveEffectiveRequestBaseUrl(
  providerBaseUrl: string,
  modelBaseUrl?: string | null,
): string {
  return typeof modelBaseUrl === "string" && modelBaseUrl.trim() ? modelBaseUrl : providerBaseUrl;
}

/**
 * Whether a credential resolved from the temp runtime may be attached to
 * the request's destination (audit S2 policy, see module doc). The
 * destination checked is the EFFECTIVE one — a model-level baseUrl
 * override must not smuggle a server-resolved credential past a
 * provider-level check.
 */
export function resolveModelDiscoveryCredentialDecision(
  request: ModelDiscoveryCredentialRequest,
): ModelDiscoveryCredentialDecision {
  if (request.requestApiKey) return { attach: true, source: "requestApiKey" };
  const configured = request.configuredBaseUrl ? normalizeBaseUrlForCompare(request.configuredBaseUrl) : "";
  const effective = resolveEffectiveRequestBaseUrl(request.baseUrl, request.modelBaseUrl);
  if (configured && normalizeBaseUrlForCompare(effective) === configured) {
    return { attach: true, source: "configuredBaseUrl" };
  }
  return { attach: false, reason: "storedCredentialBaseUrlMismatch" };
}

export interface ModelDiscoveryModelsConfigSource {
  providers?: unknown;
}

/**
 * The provider's operator-configured base URL: the entry in the REAL
 * models.json. A provider absent from models.json has none — its stored
 * credentials (if a built-in answers to the same id) are never attachable.
 */
export function getConfiguredProviderBaseUrl(
  providerName: string,
  modelsConfig: ModelDiscoveryModelsConfigSource,
): string | null {
  if (!isRecord(modelsConfig.providers)) return null;
  const provider = modelsConfig.providers[providerName];
  if (!isRecord(provider) || typeof provider.baseUrl !== "string") return null;
  const baseUrl = provider.baseUrl.trim();
  return baseUrl || null;
}

export interface ModelDiscoveryConfiguredCredentials {
  apiKey: string | null;
  headers: Record<string, string>;
}

/** The provider's REAL persisted apiKey/headers (review r2), for authenticity checks below. */
export function getConfiguredProviderCredentials(
  providerName: string,
  modelsConfig: ModelDiscoveryModelsConfigSource,
): ModelDiscoveryConfiguredCredentials {
  if (!isRecord(modelsConfig.providers)) return { apiKey: null, headers: {} };
  const provider = modelsConfig.providers[providerName];
  if (!isRecord(provider)) return { apiKey: null, headers: {} };
  const apiKey = typeof provider.apiKey === "string" && provider.apiKey ? provider.apiKey : null;
  const headers: Record<string, string> = {};
  if (isRecord(provider.headers)) {
    for (const [name, value] of Object.entries(provider.headers)) {
      if (typeof value === "string") headers[name] = value;
    }
  }
  return { apiKey, headers };
}

/**
 * A request-supplied apiKey/header value using config-expression syntax is
 * refused UNLESS it is byte-for-byte identical to what the operator already
 * persisted for that exact provider name (review r2, see module doc): an
 * attacker can replay an already-authorized expression, never forge a new
 * one. This is a pure authenticity check — it says nothing about which
 * destination the value may then be used against; the ordinary configured-
 * base-URL gate in `resolveModelDiscoveryCredentialDecision` still applies.
 */
export function verifyConfigExpressionsAreConfigured(
  provider: { apiKey?: string; headers: Record<string, string> },
  configured: ModelDiscoveryConfiguredCredentials,
): { ok: true } | { ok: false; reason: "invalidConfigExpression" } {
  if (typeof provider.apiKey === "string" && isConfigExpression(provider.apiKey)) {
    if (provider.apiKey !== configured.apiKey) return { ok: false, reason: "invalidConfigExpression" };
  }
  for (const [name, value] of Object.entries(provider.headers)) {
    if (isConfigExpression(value) && configured.headers[name] !== value) {
      return { ok: false, reason: "invalidConfigExpression" };
    }
  }
  return { ok: true };
}

/**
 * Audit S2 (review r1): every outbound completion request goes through this
 * fetch seam so a 3xx from the destination is REFUSED instead of followed —
 * a followed redirect is a second exfil hop for whatever credential the
 * headers carry, and the first URL's check says nothing about the hop's
 * destination. The seam is passed to completeSimple; provider adapters that
 * cannot inject a custom fetch fail the request rather than silently
 * falling back to a redirect-following transport. Lives here rather than in
 * the route file because Next.js route modules may only export route
 * handlers/config — the build's generated route types reject any other
 * export (pi#62 ship gate).
 */
export const redirectRefusingFetch: typeof globalThis.fetch = (input, init) =>
  globalThis.fetch(input, { ...init, redirect: "error" });
