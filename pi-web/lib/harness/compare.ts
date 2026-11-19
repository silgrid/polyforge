// Read-back comparison of the unified fields across harnesses (polyforge#85
// 85a1, unit u4): each field's status is `consistent` when every harness
// supports it and the neutral values agree, `inconsistent` when they differ
// (each harness's current value listed), `unsupported` when a harness does
// not have the field (owner ruling Q3 A+B: reasoning effort proves
// consistent/inconsistent, the approval mode proves pi's 「不支持」).
import type { HarnessId, HarnessReadContext } from "./adapter";
import { ensureDefaultHarnesses } from "./adapters";

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
export async function compareField(_field: CompareField, _ctx?: HarnessReadContext): Promise<CompareFieldResult> {
  // stub (85a1 u4): zero-value body, the red run's target
  void ensureDefaultHarnesses;
  return { field: "reasoning-effort", status: "consistent", values: {} };
}

/** Every field's comparison, in COMPARE_FIELDS order. */
export async function compareAll(ctx?: HarnessReadContext): Promise<CompareFieldResult[]> {
  const results: CompareFieldResult[] = [];
  for (const field of COMPARE_FIELDS) {
    results.push(await compareField(field, ctx));
  }
  return results;
}
