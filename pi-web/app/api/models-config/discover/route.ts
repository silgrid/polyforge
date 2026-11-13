import { NextResponse } from "next/server";
import { resolveModelDiscoveryAuth } from "@/lib/model-discovery-auth";
import { buildModelsListUrl, parseDiscoveredModels } from "@/lib/model-discovery";
import {
  getConfiguredProviderBaseUrl,
  getConfiguredProviderCredentials,
  isConfigExpression,
  resolveModelDiscoveryCredentialDecision,
  validateModelDiscoveryProvider,
  verifyConfigExpressionsAreConfigured,
} from "@/lib/model-discovery-guards";
import { readModelsConfig } from "@/lib/models-config-store";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";

const DISCOVERY_TIMEOUT_MS = 20_000;

function hasHeader(headers: Headers, name: string): boolean {
  return headers.has(name);
}

function buildHeaders(api: string, apiKey: string | undefined, configured: Record<string, string>): Headers {
  const headers = new Headers(configured);
  if (!hasHeader(headers, "accept")) headers.set("Accept", "application/json");
  if (!apiKey) return headers;

  if (api === "anthropic-messages") {
    if (!hasHeader(headers, "x-api-key")) headers.set("x-api-key", apiKey);
    if (!hasHeader(headers, "anthropic-version")) headers.set("anthropic-version", "2023-06-01");
  } else if (api === "google-generative-ai") {
    if (!hasHeader(headers, "x-goog-api-key")) headers.set("x-goog-api-key", apiKey);
  } else if (!hasHeader(headers, "authorization")) {
    headers.set("Authorization", `Bearer ${apiKey}`);
  }
  return headers;
}

export async function POST(req: Request) {
  if (!isApiRequestAllowed(req)) {
    return NextResponse.json({ error: "Untrusted API request" }, { status: 403 });
  }
  if (!hasJsonContentType(req)) {
    return NextResponse.json({ error: "Content-Type must be application/json" }, { status: 415 });
  }

  try {
    const body = await req.json().catch(() => null);
    const validated = validateModelDiscoveryProvider(body);
    if (!validated.ok) {
      return NextResponse.json({ error: validated.reason }, { status: 400 });
    }
    const { providerName, provider } = validated;
    // "" means the request named no base URL at all; it is never a value the
    // caller can forge their way into otherwise (lib/model-discovery-guards.ts
    // refuses an explicit empty string at the schema layer).
    const requestedBaseUrl = provider.baseUrl;

    const modelsConfig = readModelsConfig();

    // review r2: an expression apiKey/header ("$VAR", "!command") is refused
    // unless it exactly matches what the operator already persisted for this
    // provider name — an attacker can replay an authorized expression, never
    // forge a new one (see lib/model-discovery-guards.ts module doc).
    const configExpressionCheck = verifyConfigExpressionsAreConfigured(
      provider,
      getConfiguredProviderCredentials(providerName, modelsConfig),
    );
    if (!configExpressionCheck.ok) {
      return NextResponse.json({ error: configExpressionCheck.reason }, { status: 403 });
    }
    // An expression apiKey is never a portable "request-chosen" literal, even
    // once verified authentic — it is resolved exclusively through the
    // configured path below, so it still requires the configured-base-URL
    // gate to pass.
    const literalApiKey = provider.apiKey && !isConfigExpression(provider.apiKey) ? provider.apiKey : undefined;

    // Audit S2: a credential resolved from the operator's stored config may
    // only be attached to the provider's configured base URL; a key the
    // request itself supplies may go where the request says — as a LITERAL:
    // the request-key path never runs SDK auth resolution, so the operator's
    // stored credential for a colliding provider id cannot be swapped in.
    // This gate only has a destination to check when the REQUEST itself names
    // one: when it names none at all, the discovery endpoint can only ever
    // resolve to pi's own built-in provider catalog entry for this exact
    // providerName (upstream #1006, lib/model-discovery-auth.ts's fallback) —
    // a hardcoded value the request has no way to steer — so there is nothing
    // here for a destination gate to guard against.
    if (requestedBaseUrl) {
      const decision = resolveModelDiscoveryCredentialDecision({
        baseUrl: requestedBaseUrl,
        ...(literalApiKey ? { requestApiKey: literalApiKey } : {}),
        configuredBaseUrl: getConfiguredProviderBaseUrl(providerName, modelsConfig),
      });
      if (!decision.attach) {
        return NextResponse.json({ error: decision.reason }, { status: 403 });
      }
    }

    const providerEntry: Record<string, unknown> = {
      ...(requestedBaseUrl ? { baseUrl: requestedBaseUrl } : {}),
      // An explicitly named protocol pins the resolution; an omitted one must
      // reach the SDK unresolved so pi's catalog can supply the provider's
      // native protocol (upstream #1006). The guard passes an omitted api
      // through as "" rather than defaulting (lib/model-discovery-guards.ts).
      ...(provider.api ? { api: provider.api } : {}),
      ...(provider.apiKey ? { apiKey: provider.apiKey } : {}),
      ...(Object.keys(provider.headers).length > 0 ? { headers: provider.headers } : {}),
      ...provider.extra,
    };

    let auth: { apiKey?: string; headers: Record<string, string> };
    let effectiveBaseUrl = requestedBaseUrl;
    // The guard no longer defaults the protocol: provider.api is "" when the
    // request omitted it, so the catalog's own protocol (`resolved.api` below)
    // is consulted before the OpenAI fallback applies (review blocker 3).
    let effectiveApi = provider.api;

    if (literalApiKey && requestedBaseUrl) {
      // Request-key path with a known destination: the literal goes out
      // untouched — resolveModelDiscoveryAuth (SDK auth resolution) is skipped
      // entirely so it never gets a chance to substitute the operator's stored
      // credential for a colliding provider id. With no catalog consult, an
      // omitted protocol falls straight to the default.
      effectiveApi = provider.api || "openai-completions";
      auth = { apiKey: literalApiKey, headers: provider.headers };
    } else {
      // Stored/verified-expression path (destination gate above already
      // pinned the operator's configured URL when one was requested), or a
      // literal key with no requested base URL at all — pi's catalog resolves
      // the endpoint either way, and a literal key simply overrides whatever
      // credential that lookup would otherwise have resolved.
      let resolved: Awaited<ReturnType<typeof resolveModelDiscoveryAuth>>;
      try {
        resolved = await resolveModelDiscoveryAuth(providerName, providerEntry);
      } catch (firstError) {
        const message = firstError instanceof Error ? firstError.message : String(firstError);
        if (!/no .api. specified/.test(message)) {
          // Without a configured/requested Base URL, pi's catalog was the only
          // other source of one; for a custom/unknown provider that fails
          // validating the internal placeholder model (upstream #1006).
          if (!requestedBaseUrl) return NextResponse.json({ error: "Base URL is required" }, { status: 400 });
          throw firstError;
        }
        // A provider the catalog does not know has no SDK-level protocol
        // default: retry with the OpenAI-compatible protocol the guard used
        // to pin (review blocker 3). A catalog provider never lands here —
        // its native protocol resolved on the first attempt.
        try {
          resolved = await resolveModelDiscoveryAuth(providerName, {
            ...providerEntry,
            api: "openai-completions",
          });
        } catch (retryError) {
          if (!requestedBaseUrl) return NextResponse.json({ error: "Base URL is required" }, { status: 400 });
          throw retryError;
        }
      }
      // Fall back to pi's provider catalog so built-in providers, and entries
      // that only list models, do not have to repeat the upstream base URL
      // (upstream #1006).
      effectiveBaseUrl = requestedBaseUrl || resolved.baseUrl || "";
      effectiveApi = provider.api || resolved.api || "openai-completions";
      auth = literalApiKey
        ? { apiKey: literalApiKey, headers: provider.headers }
        : { apiKey: resolved.apiKey, headers: resolved.headers };
    }

    if (!effectiveBaseUrl) {
      return NextResponse.json({ error: "Base URL is required" }, { status: 400 });
    }

    let endpoint: URL;
    try {
      endpoint = buildModelsListUrl(effectiveBaseUrl, effectiveApi);
    } catch {
      return NextResponse.json({ error: "invalidBaseUrl" }, { status: 400 });
    }

    const response = await fetch(endpoint, {
      cache: "no-store",
      // Audit S2: a redirect would be a second exfil hop for whatever
      // credential the headers carry.
      redirect: "error",
      headers: buildHeaders(effectiveApi, auth.apiKey, auth.headers),
      signal: AbortSignal.timeout(DISCOVERY_TIMEOUT_MS),
    });
    const responseText = await response.text();
    if (!response.ok) {
      return NextResponse.json({
        error: responseText.slice(0, 500) || `Upstream returned HTTP ${response.status}`,
        status: response.status,
      }, { status: 502 });
    }

    let payload: unknown;
    try {
      payload = JSON.parse(responseText);
    } catch {
      return NextResponse.json({ error: "Upstream model list was not valid JSON" }, { status: 502 });
    }
    const models = parseDiscoveredModels(payload);
    if (models.length === 0) {
      return NextResponse.json({ error: "No models found in the upstream response" }, { status: 502 });
    }

    return NextResponse.json({ models, endpoint: endpoint.toString() });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    const status = error instanceof DOMException && error.name === "TimeoutError" ? 504 : 500;
    return NextResponse.json({ error: message }, { status });
  }
}
