// The client-safe view model of General's 环境 and 生效值与来源 panes
// (polyforge#149, #85 85a1b): the pure mappers from the 85a1 adapter payload
// types to display rows — environment groups hold variable names only, and
// every effective row keeps its 推算 provenance. Placeholder signatures; the
// bodies arrive with their units and replace this stub.
import type { EffectiveConfigPayload, EnvPayload } from "./harness/concepts";

/** One harness's environment group: its variable names, sorted, never a value. */
export interface HarnessEnvGroup {
  harness: string;
  target: string;
  keys: string[];
}

export function envGroup(_harness: string, _payload: EnvPayload | null): HarnessEnvGroup | null {
  return null;
}

/** One effective value row: the field, its display value, and its source. */
export interface HarnessEffectiveRow {
  field: string;
  value: string;
  sourceScope: string;
  sourcePath: string;
}

export function effectiveRows(_payload: EffectiveConfigPayload | null): HarnessEffectiveRow[] {
  return [];
}
