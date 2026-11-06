import { mkdtempSync, rmSync, writeFileSync } from "fs";
import { tmpdir } from "os";
import { join } from "path";
import { InMemoryCredentialStore } from "@earendil-works/pi-ai";
import { ModelRuntime } from "@earendil-works/pi-coding-agent";
import { isRecord } from "./type-guards";

export interface ModelDiscoveryAuth {
  apiKey?: string;
  headers: Record<string, string>;
  /**
   * Effective upstream base URL: the models.json value when present, otherwise
   * pi's own provider catalog. This is what lets discovery work for a provider
   * entry that only lists models and relies on the built-in endpoint, and for
   * providers that pi ships without a models.json entry at all.
   */
  baseUrl?: string;
  /** Effective API protocol, needed to build the protocol-correct model list URL. */
  api?: string;
}

/**
 * The models-config/test route and the discovery-auth resolution used to
 * each keep a private copy of the same dance: temp dir, temp models.json,
 * ModelRuntime.create, load-error check, cleanup (audit P2). This is the
 * one shared helper now.
 */
export class TempModelsLoadError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "TempModelsLoadError";
  }
}

/** Options for {@link withTempModelsRuntime}. */
export interface TempModelsRuntimeOptions {
  /**
   * Load the runtime with an EMPTY in-memory credential store instead of the
   * operator's real auth.json (audit S2, review r1): a request-supplied
   * apiKey is a LITERAL the client chose, so the runtime that handles it
   * must not be able to inherit the operator's stored API keys, OAuth
   * tokens, or env-configured credentials for a colliding provider id.
   */
  isolatedCredentials?: boolean;
}

/**
 * Runs `fn` against a ModelRuntime loaded from a TEMP models.json containing
 * exactly `providerName` with the given provider entry and models. The temp
 * dir is always removed. A load error throws TempModelsLoadError so each
 * route can map it to its own response contract.
 */
export async function withTempModelsRuntime<T>(
  providerName: string,
  provider: Record<string, unknown>,
  models: Record<string, unknown>[],
  fn: (modelRuntime: ModelRuntime) => Promise<T>,
  options: TempModelsRuntimeOptions = {},
): Promise<T> {
  let tempDir: string | undefined;
  try {
    tempDir = mkdtempSync(join(tmpdir(), "pi-web-model-runtime-"));
    const modelsPath = join(tempDir, "models.json");
    writeFileSync(modelsPath, JSON.stringify({
      providers: {
        [providerName]: {
          ...provider,
          models,
        },
      },
    }, null, 2), "utf8");

    const modelRuntime = await ModelRuntime.create({
      modelsPath,
      // isolatedCredentials: no auth.json, no inherited stored credential —
      // the request-key path can only ever see the literal it supplied.
      ...(options.isolatedCredentials ? { credentials: new InMemoryCredentialStore() } : {}),
    });
    const loadError = modelRuntime.getError();
    if (loadError) throw new TempModelsLoadError(loadError);
    return await fn(modelRuntime);
  } finally {
    if (tempDir) rmSync(tempDir, { recursive: true, force: true });
  }
}

const MODEL_DISCOVERY_MODEL_ID = "__pi_web_model_discovery__";

export async function resolveModelDiscoveryAuth(
  providerName: string,
  provider: Record<string, unknown>,
): Promise<ModelDiscoveryAuth> {
  return withTempModelsRuntime(providerName, provider, [{ id: MODEL_DISCOVERY_MODEL_ID }], async (modelRuntime) => {
    const model = modelRuntime.getModel(providerName, MODEL_DISCOVERY_MODEL_ID);
    if (!model) throw new Error(`Unable to load provider "${providerName}"`);

    // Compose the provider the same way pi does: an entry without a baseUrl or
    // api (built-in provider, or a models-only override) still resolves to the
    // endpoint and protocol pi ships for that provider.
    const baseUrl = model.baseUrl?.trim()
      || modelRuntime.getProvider(providerName)?.baseUrl?.trim()
      || undefined;
    const api = model.api ?? modelRuntime.getModels(providerName)[0]?.api;

    const resolved = await modelRuntime.getAuth(model);
    if (resolved) {
      return {
        apiKey: resolved.auth.apiKey,
        headers: stringRecord(resolved.auth.headers),
        baseUrl,
        api,
      };
    }

    return {
      headers: stringRecord(modelRuntime.getCompatibilityRequestConfig(model).headers),
      baseUrl,
      api,
    };
  });
}

function stringRecord(value: unknown): Record<string, string> {
  if (!isRecord(value)) return {};
  return Object.fromEntries(Object.entries(value).filter((entry): entry is [string, string] => typeof entry[1] === "string"));
}
