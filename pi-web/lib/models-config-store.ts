import { existsSync, lstatSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { dirname, join } from "node:path";
import { getAgentDir } from "@earendil-works/pi-coding-agent";
import { z } from "zod";
import { writePrivateFileAtomicSync } from "./atomic-file";
import { stripJsonComments } from "./jsonc";
import { invalidateModelsCache } from "./models-cache";
import { isRecord } from "./type-guards";

const MODEL_COST_KEYS = ["input", "output", "cacheRead", "cacheWrite"] as const;

function normalizeModelCost(value: unknown): Record<string, unknown> | undefined {
  if (!isRecord(value)) return undefined;
  const providedKeys = MODEL_COST_KEYS.filter((key) => value[key] !== undefined);
  if (providedKeys.length === 0) return undefined;
  if (providedKeys.some((key) => (
    typeof value[key] !== "number" || !Number.isFinite(value[key])
  ))) return undefined;

  return Object.fromEntries([
    ...Object.entries(value),
    ...MODEL_COST_KEYS.map((key) => [key, value[key] ?? 0]),
  ]);
}

/** Complete partial cost groups with zero; omit a cost group only when it is empty. */
export function normalizeModelsConfigCosts(
  data: Record<string, unknown>,
): Record<string, unknown> {
  const normalized = structuredClone(data);
  if (!isRecord(normalized.providers)) return normalized;

  for (const provider of Object.values(normalized.providers)) {
    if (!isRecord(provider) || !Array.isArray(provider.models)) continue;
    for (const model of provider.models) {
      if (!isRecord(model) || !("cost" in model)) continue;
      const cost = normalizeModelCost(model.cost);
      if (cost) model.cost = cost;
      else delete model.cost;
    }
  }
  return normalized;
}

function sanitizeModelsConfig(data: Record<string, unknown>): Record<string, unknown> {
  if (!isRecord(data.providers)) return data;

  const providers = Object.fromEntries(Object.entries(data.providers).map(([providerId, provider]) => {
    if (!isRecord(provider) || !Array.isArray(provider.models)) return [providerId, provider];
    const models = provider.models.filter((model) => (
      !isRecord(model) || typeof model.id !== "string" || model.id.trim().length > 0
    ));
    return [providerId, { ...provider, models }];
  }));

  return { ...data, providers };
}

export function getModelsConfigPath(): string {
  return join(getAgentDir(), "models.json");
}

/** models.json exists but its contents cannot be used, so it must not be replaced. */
export class ModelsConfigReadError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ModelsConfigReadError";
  }
}

/**
 * Reads models.json with the same leniency as pi's loader (BOM, `//` comments,
 * trailing commas). `stripJsonComments` also drops block comments, which pi
 * rejects, and returns pi's own result for every file pi accepts. A file pi
 * accepts must never read as empty here: the panel saves its whole draft, so
 * an empty read would delete every provider on the next save. Unusable
 * contents throw instead.
 */
export function readModelsConfig(
  modelsPath = getModelsConfigPath(),
): Record<string, unknown> {
  if (!existsSync(modelsPath)) return { providers: {} };
  let parsed: unknown;
  try {
    const content = readFileSync(modelsPath, "utf8").replace(/^\uFEFF/, "");
    if (!content.trim()) return { providers: {} };
    parsed = JSON.parse(stripJsonComments(content));
  } catch (error) {
    throw new ModelsConfigReadError(
      `Failed to read ${modelsPath}: ${error instanceof Error ? error.message : String(error)}`,
    );
  }
  if (!isRecord(parsed)) {
    throw new ModelsConfigReadError(`Failed to read ${modelsPath}: expected a JSON object`);
  }
  return parsed;
}

/**
 * Schema validation for a models.json PUT body (audit S8). The route
 * previously wrote an unrestricted `Record<string, unknown>` straight to
 * disk; the SDK then rejects mistyped fields when loading models.json. The
 * schema below mirrors the SDK's own `ProviderConfigSchema` /
 * `ModelDefinitionSchema` (core/model-config.js) field-for-field, so a
 * payload this panel accepts is one pi can load, and a mistyped field
 * (`baseUrl: 17`, `models: [{ id: 0 }]`, …) is refused with the typed
 * `invalidBody` code before anything is written. Unknown keys are
 * PRESERVED, like the SDK's own loader (TypeBox without
 * `additionalProperties: false`): this file is the operator's trusted
 * configuration, and expressions such as `apiKey: "$MY_KEY"` are a
 * legitimate pi feature here — only REQUEST-supplied values on the
 * discovery/test routes are expression-gated (lib/model-discovery-guards).
 */
const jsonValueSchema: z.ZodType<unknown> = z.lazy(() => z.union([
  z.string(),
  z.number(),
  z.boolean(),
  z.null(),
  z.array(jsonValueSchema),
  z.record(z.string(), jsonValueSchema),
]));

/** `compat` is a per-API union the SDK re-validates at composition time; here
 *  it only needs to be structured JSON (not `17`, not `"nope"`). */
const compatSchema = z.record(z.string(), jsonValueSchema);

const thinkingLevelMapSchema = z.looseObject({
  off: z.string().nullable().optional(),
  minimal: z.string().nullable().optional(),
  low: z.string().nullable().optional(),
  medium: z.string().nullable().optional(),
  high: z.string().nullable().optional(),
  xhigh: z.string().nullable().optional(),
  max: z.string().nullable().optional(),
});

const costRatesSchema = z.object({
  input: z.number(),
  output: z.number(),
  cacheRead: z.number(),
  cacheWrite: z.number(),
});

/** Model-definition cost may arrive partial (the panel's draft); the write
 *  path completes missing rates with zero (normalizeModelsConfigCosts). */
const modelCostSchema = z.looseObject({
  input: z.number().optional(),
  output: z.number().optional(),
  cacheRead: z.number().optional(),
  cacheWrite: z.number().optional(),
  tiers: z.array(z.object({ inputTokensAbove: z.number(), ...costRatesSchema.shape })).optional(),
});

const promptCacheSchema = z.looseObject({
  short: z.number().positive().optional(),
  long: z.number().positive().optional(),
});

const imageResizeSchema = z.looseObject({
  maxWidth: z.number().int().positive().optional(),
  maxHeight: z.number().int().positive().optional(),
  maxBytes: z.number().int().positive().optional(),
  jpegQuality: z.number().int().min(1).max(100).optional(),
});

const inputLimitsSchema = z.looseObject({
  maxRequestBytes: z.number().int().positive().optional(),
  images: z.looseObject({
    resize: imageResizeSchema.optional(),
    maxPerMessage: z.number().int().positive().optional(),
    maxPerRequest: z.number().int().positive().optional(),
  }).optional(),
});

const modelInputSchema = z.array(z.union([z.literal("text"), z.literal("image")]));

const modelDefinitionSchema = z.looseObject({
  id: z.string().min(1),
  name: z.string().min(1).optional(),
  api: z.string().min(1).optional(),
  baseUrl: z.string().min(1).optional(),
  reasoning: z.boolean().optional(),
  thinkingLevelMap: thinkingLevelMapSchema.optional(),
  input: modelInputSchema.optional(),
  inputLimits: inputLimitsSchema.optional(),
  cost: modelCostSchema.optional(),
  promptCache: promptCacheSchema.optional(),
  contextWindow: z.number().optional(),
  maxTokens: z.number().optional(),
  samplingParams: z.record(z.string(), z.unknown()).optional(),
  headers: z.record(z.string(), z.string()).optional(),
  compat: compatSchema.optional(),
});

const modelOverrideSchema = z.looseObject({
  name: z.string().min(1).optional(),
  reasoning: z.boolean().optional(),
  thinkingLevelMap: thinkingLevelMapSchema.optional(),
  input: modelInputSchema.optional(),
  inputLimits: inputLimitsSchema.optional(),
  cost: z.looseObject({
    input: z.number().optional(),
    output: z.number().optional(),
    cacheRead: z.number().optional(),
    cacheWrite: z.number().optional(),
    tiers: z.array(z.object({ inputTokensAbove: z.number(), ...costRatesSchema.shape })).optional(),
  }).optional(),
  promptCache: promptCacheSchema.optional(),
  contextWindow: z.number().optional(),
  maxTokens: z.number().optional(),
  samplingParams: z.record(z.string(), z.unknown()).optional(),
  headers: z.record(z.string(), z.string()).optional(),
  compat: compatSchema.optional(),
});

const providerConfigSchema = z.looseObject({
  name: z.string().min(1).optional(),
  baseUrl: z.string().min(1).optional(),
  apiKey: z.string().min(1).optional(),
  api: z.string().min(1).optional(),
  oauth: z.literal("radius").optional(),
  headers: z.record(z.string(), z.string()).optional(),
  compat: compatSchema.optional(),
  authHeader: z.boolean().optional(),
  models: z.array(modelDefinitionSchema).optional(),
  modelOverrides: z.record(z.string(), modelOverrideSchema).optional(),
});

const modelsConfigSchema = z.looseObject({
  providers: z.record(z.string(), providerConfigSchema),
});

export type ModelsConfigPayloadValidation =
  | { ok: true; data: Record<string, unknown> }
  | { ok: false; reason: "invalidBody" };

export function validateModelsConfigPayload(body: unknown): ModelsConfigPayloadValidation {
  const parsed = modelsConfigSchema.safeParse(body);
  if (!parsed.success) return { ok: false, reason: "invalidBody" };
  return { ok: true, data: parsed.data as Record<string, unknown> };
}

export function writeModelsConfig(
  data: Record<string, unknown>,
  modelsPath = getModelsConfigPath(),
): void {
  // Refuse to replace a file this panel could not read: the draft being saved
  // was not built from it, so writing would silently discard its contents.
  readModelsConfig(modelsPath);
  const dir = dirname(modelsPath);
  if (!existsSync(dir)) mkdirSync(dir, { recursive: true });
  const normalized = normalizeModelsConfigCosts(sanitizeModelsConfig(data));
  // models.json may be a symbolic link (a dotfiles setup). The atomic write
  // renames a temp file over the destination path, which would replace the
  // link itself and leave its target holding the old contents, so write
  // through the resolved path instead. A dangling link is refused: the read
  // guard above reports it as missing, and replacing it would silently break
  // the user's link.
  let writePath = modelsPath;
  if (lstatSync(modelsPath, { throwIfNoEntry: false })?.isSymbolicLink()) {
    try {
      writePath = realpathSync(modelsPath);
    } catch {
      throw new ModelsConfigReadError(`Failed to write ${modelsPath}: dangling symbolic link`);
    }
  }
  writePrivateFileAtomicSync(writePath, JSON.stringify(normalized, null, 2));
  invalidateModelsCache();
}
