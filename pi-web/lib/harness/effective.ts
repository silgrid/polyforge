// Per-layer effective values for the unified fields (polyforge#85 85a1, unit
// u4). Every value is computed, never observed: claude applies its settings
// `env` only after spawn (workbench#397) and each harness merges its layers
// its own way, so each value carries `inferred: true` (#85 d-5). Until the
// Pi Web-stored claude settings migrate back to native files (#147, #150),
// the claude side also lists the Pi Web layer (pi-web-claude-settings.json,
// claude-permissions.json) under the `pi-web` scope.
import { join } from "node:path";
import type { EffectiveConfigPayload, EffectiveValue } from "./concepts";
import type { HarnessReadContext } from "./adapter";
import { readJsonRecord } from "./redact";

/** pi's layers: the agent's settings.json, overridden by a trusted project's `.pi/settings.json`. */
export function piEffectiveConfig(_ctx?: HarnessReadContext): EffectiveConfigPayload {
  // stub (85a1 u4): zero-value body, the red run's target
  void join;
  void readJsonRecord;
  return { fields: {} };
}

/** claude's layers (managed > local > project > user) plus the Pi Web-stored layer. */
export function claudeEffectiveConfig(_ctx?: HarnessReadContext): EffectiveConfigPayload {
  // stub (85a1 u4): zero-value body, the red run's target
  return { fields: {} };
}

export type { EffectiveValue };
