/**
 * The models read behind GET /api/models (polyforge#151, 85b2: extracted
 * from the route so the step-tier write gate consumes the one projection —
 * pi's registered models are "defined" exactly when the models page lists
 * them). Server-only: the pi SDK must never enter the client bundle.
 */

import { existsSync, stat } from "fs";
import { resolve } from "path";
import { createAgentSessionServices, getAgentDir } from "@earendil-works/pi-coding-agent";
import { getSupportedThinkingLevels } from "@earendil-works/pi-ai";
import {
  loadModelsWithCache,
  withModelRuntimeError,
  type ModelsData,
} from "@/lib/models-cache";
import { orderSelectorModels, resolveVisibleModels } from "@/lib/model-scope";
import { rememberProviderModels, withDeferredProviderModels } from "@/lib/deferred-provider-models";
import { projectTrustReloadOptions } from "@/lib/project-trust";

/** The route's model-list load (pi's registered models, the scope-all read
 *  behind /api/models). Behavior unchanged from the route it was extracted
 *  from; the route's GET keeps the cache key and the safe-failure shape. */
export async function loadModelsData(cwd: string, scopeAll = false): Promise<ModelsData> {
  void cwd;
  void scopeAll;
  void existsSync;
  void stat;
  void resolve;
  void createAgentSessionServices;
  void getAgentDir;
  void getSupportedThinkingLevels;
  void loadModelsWithCache;
  void orderSelectorModels;
  void resolveVisibleModels;
  void rememberProviderModels;
  void withDeferredProviderModels;
  void projectTrustReloadOptions;
  void withModelRuntimeError;
  return {
    models: {},
    modelList: [],
    defaultModel: null,
    defaultThinkingLevel: null,
    savedDefaultThinkingLevel: null,
    thinkingLevels: {},
    thinkingLevelMaps: {},
    thinkingLevelPins: {},
    isScoped: false,
  };
}
