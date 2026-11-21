/**
 * The models read behind GET /api/models (polyforge#151, 85b2: extracted
 * from the route so the step-tier write gate consumes the one projection —
 * pi's registered models are "defined" exactly when the models page lists
 * them, and both readers share the same cache key). Server-only: the pi SDK
 * must never enter the client bundle.
 */

import { getSupportedThinkingLevels } from "@earendil-works/pi-ai";
import { createAgentSessionServices, getAgentDir, type SettingsManager } from "@earendil-works/pi-coding-agent";
import {
  withModelRuntimeError,
  type ModelsData,
} from "./models-cache";
import { orderSelectorModels, resolveVisibleModels, selectInitialModelScope } from "./model-scope";
import { rememberProviderModels, withDeferredProviderModels } from "./deferred-provider-models";
import { projectTrustReloadOptions } from "./project-trust";

/** The route's model-list load: pi's registered models (the scope-all
 *  projection), its default model and its thinking levels. Behavior is
 *  byte-for-byte the function the route carried before the extraction; the
 *  route keeps the cwd checks, the cache key and the safe-failure shape. */
export async function loadModelsData(cwd: string, scopeAll = false): Promise<ModelsData> {
  const nameMap = new Map<string, string>();
  let modelList: { id: string; name: string; provider: string }[] = [];
  let defaultModel: { provider: string; modelId: string } | null = null;
  const thinkingLevels: Record<string, string[]> = {};
  const thinkingLevelMaps: Record<string, Record<string, string | null>> = {};

  const agentDir = getAgentDir();
  // Gate untrusted project extensions: enumerating models still imports and
  // runs a repository's .pi/extensions factories, so honor project trust here
  // too (see lib/project-trust.ts, #236).
  const trustReloadOptions = projectTrustReloadOptions(cwd, agentDir);
  const services = await createAgentSessionServices({
    cwd,
    agentDir,
    ...(trustReloadOptions ? { resourceLoaderReloadOptions: trustReloadOptions } : {}),
  });
  const modelError = services.modelRuntime.getError();
  const settings: SettingsManager = services.settingsManager;
  await rememberProviderModels(services.modelRuntime);
  // `enabledModels` supports globs and fuzzy patterns, so resolve it the same
  // way the CLI does instead of comparing pattern strings literally (#307).
  const scope = await resolveVisibleModels(
    withDeferredProviderModels(services.modelRuntime),
    scopeAll ? undefined : settings.getEnabledModels(),
  );
  const { visible, thinkingLevelPins, warnings } = scope;
  modelList = orderSelectorModels(scope).map((m) => ({
    id: m.id,
    name: m.name,
    provider: m.provider,
    input: m.input,
  }));
  for (const m of visible) {
    const key = `${m.provider}:${m.id}`;
    nameMap.set(key, m.name);
    thinkingLevels[key] = getSupportedThinkingLevels(m);
    if (m.thinkingLevelMap) thinkingLevelMaps[key] = m.thinkingLevelMap;
  }

  const defaultProvider = settings.getDefaultProvider();
  const defaultModelId = settings.getDefaultModel();
  const initial = selectInitialModelScope(scope, {
    ...(defaultProvider && defaultModelId
      ? { defaultModel: { provider: defaultProvider, modelId: defaultModelId } }
      : {}),
  });
  if (initial.model) {
    defaultModel = { provider: initial.model.provider, modelId: initial.model.id };
  }
  const defaultThinkingLevel = initial.thinkingLevel
    ?? (initial.model
      ? settings.getModelThinkingLevel(initial.model.provider, initial.model.id)
      : undefined)
    ?? settings.getDefaultThinkingLevel()
    ?? null;

  return withModelRuntimeError(
    {
      models: Object.fromEntries(nameMap),
      modelList,
      defaultModel,
      defaultThinkingLevel,
      savedDefaultThinkingLevel: settings.getDefaultThinkingLevel() ?? null,
      thinkingLevels,
      thinkingLevelMaps,
      thinkingLevelPins,
      ...(warnings.length > 0 ? { modelScopeWarnings: warnings } : {}),
      isScoped: scopeAll ? false : scope.scopedModels.length > 0,
    },
    modelError,
  );
}
