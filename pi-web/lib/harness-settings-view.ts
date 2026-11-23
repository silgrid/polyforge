// The client-safe view model of General's 环境 and 生效值与来源 panes
// (polyforge#149, #85 85a1b): the pure mappers from the 85a1 adapter payload
// types to display rows — environment groups hold variable names only, and
// every effective row keeps its 推算 provenance.
import type { EffectiveConfigPayload, EnvPayload } from "./harness/concepts";

/** One harness's environment group: its variable names, sorted, never a value. */
export interface HarnessEnvGroup {
  harness: string;
  target: string;
  keys: string[];
}

export function envGroup(harness: string, payload: EnvPayload | null): HarnessEnvGroup | null {
  if (!payload) return null;
  // Names only, deduped and sorted — the payload already carries no value;
  // the view keeps it that way by construction.
  const keys = [...new Set(payload.setKeys.filter((key): key is string => typeof key === "string" && key !== ""))].sort();
  return { harness, target: payload.target, keys };
}

/** One effective value row: the field, its display value, and its source. */
export interface HarnessEffectiveRow {
  field: string;
  value: string;
  sourceScope: string;
  sourcePath: string;
}

export function effectiveRows(payload: EffectiveConfigPayload | null): HarnessEffectiveRow[] {
  if (!payload) return [];
  // A stable order (by field name); every row keeps its winning source, and
  // the 推算 mark is the pane's, from the payload's own inferred contract.
  return Object.entries(payload.fields)
    .map(([field, entry]) => ({
      field,
      value: typeof entry.value === "string" ? entry.value : JSON.stringify(entry.value),
      sourceScope: entry.sourceScope,
      sourcePath: entry.sourcePath,
    }))
    .sort((a, b) => (a.field < b.field ? -1 : a.field > b.field ? 1 : 0));
}
