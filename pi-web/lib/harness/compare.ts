// Read-back comparison of the unified fields across harnesses (polyforge#85
// 85a1, unit u4): each field's status is `consistent` when every harness
// supports it and the neutral values agree, `inconsistent` when they differ
// (each harness's current value listed), `unsupported` when a harness does
// not have the field (owner ruling Q3 A+B: reasoning effort proves
// consistent/inconsistent, the approval mode proves pi's 「不支持」).
import type { HarnessReadContext } from "./adapter";
import { ensureDefaultHarnesses } from "./adapters";
import { neutralApprovalMode, neutralReasoningEffort, type EffectiveConfigPayload, type HarnessId, type PermissionPayload } from "./concepts";

/** The fields the first version compares. */
export const COMPARE_FIELDS = ["reasoning-effort", "approval-mode"] as const;
export type CompareField = (typeof COMPARE_FIELDS)[number];

export type CompareStatus = "consistent" | "inconsistent" | "unsupported";

/** One harness's value for a compared field. */
export interface CompareFieldValue {
  supported: boolean;
  neutral: string | null;
  raw: string | null;
}

/** One field's comparison across every registered harness. */
export interface CompareFieldResult {
  field: CompareField;
  status: CompareStatus;
  values: Record<HarnessId, CompareFieldValue>;
}

/** One field's comparison (Q3 A+B). */
export async function compareField(field: CompareField, ctx?: HarnessReadContext): Promise<CompareFieldResult> {
  const adapters = ensureDefaultHarnesses();
  const values = {} as Record<HarnessId, CompareFieldValue>;
  for (const adapter of adapters) {
    if (field === "approval-mode") {
      const permission = (await adapter.read("permission", ctx)) as PermissionPayload | null;
      values[adapter.id] = {
        supported: permission?.approvalMode.supported ?? false,
        neutral: permission?.approvalMode.neutral ?? null,
        raw: permission?.approvalMode.raw ?? null,
      };
    } else {
      const effectivePayload = (await adapter.read("effective-config", ctx)) as EffectiveConfigPayload | null;
      const entry = effectivePayload?.fields[field];
      const raw = entry && typeof entry.value === "string" ? entry.value : null;
      values[adapter.id] = {
        supported: true,
        neutral: field === "reasoning-effort" ? neutralReasoningEffort(raw) : neutralApprovalMode(raw),
        raw,
      };
    }
  }
  const list = Object.values(values);
  const status: CompareStatus = list.some((value) => !value.supported)
    ? "unsupported"
    : new Set(list.map((value) => value.neutral)).size === 1
      ? "consistent"
      : "inconsistent";
  return { field, status, values };
}

/** Every field's comparison, in COMPARE_FIELDS order. */
export async function compareAll(ctx?: HarnessReadContext): Promise<CompareFieldResult[]> {
  const results: CompareFieldResult[] = [];
  for (const field of COMPARE_FIELDS) {
    results.push(await compareField(field, ctx));
  }
  return results;
}
