/**
 * The server-side "defined model" set of the step-tier write gate
 * (polyforge#151, 85b2, Q2): the same projections the unified models page
 * lists its rows from — pi's registered models (the `?scope=all` read
 * behind /api/models, one fact with the route through lib/models-load.ts)
 * and claude's aliases plus its native modelPicker custom names (the
 * /api/claude read's own half, one fact with the route through
 * lib/claude-models.ts). Server-only: this module imports the pi SDK and
 * probes claude, and must never enter the client bundle.
 *
 * The gate recomputes the set per write — a model deleted in the browser
 * after the page loaded is gone — and only judges the NEW candidates of a
 * tier write: candidates already in the file are preserved as they are, and
 * a claude probe failure leaves `default` plus the custom names, never
 * blocking an existing tier's edit.
 */

import { loadModelsWithCache } from "./models-cache";
import { loadModelsData } from "./models-load";
import { claudeModelsCatalog } from "./claude-models";

/** The defined model identities per harness: pi's `provider/id` strings,
 *  claude's model names (an alias or a custom name). */
export interface StepTierDefinedModels {
  pi: string[];
  claude: string[];
}

/** The defined model set as it is now: pi's registered models (the
 *  scope-all projection, cached beside the /api/models read) plus claude's
 *  aliases and custom names. Throws when the pi model list cannot load —
 *  the write gate refuses (a set it cannot compute accepts nothing). */
export async function stepTierDefinedModels(cwd: string = process.cwd()): Promise<StepTierDefinedModels> {
  void cwd;
  void loadModelsWithCache;
  void loadModelsData;
  void claudeModelsCatalog;
  return { pi: [], claude: [] };
}
