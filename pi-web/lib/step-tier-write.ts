/** A bounded runner for one harness's step-tier plan (polyforge#258 T3):
 *  read, plan and write until the plan is empty, or fail without claiming
 *  success when the read, a write or the write limit stops it. */

import { stepTierItems, stepTierWritePlan, type StepTierWrite } from "./step-tiers";

export type StepTierRunReason = "limit" | "read" | "write";

export interface StepTierRunResult {
  ok: boolean;
  reason?: StepTierRunReason;
  error?: string;
}

export type StepTierRunRead = () => Promise<
  | { ok: true; items: Array<{ key: string; value?: unknown }>; hash: string | null }
  | { ok: false; error: string }
>;

export type StepTierRunWrite = (write: StepTierWrite, hash: string | null) => Promise<{ ok: true } | { ok: false; error?: string }>;

/** Stub (polyforge#258 u7): filled in by the implementation commit. */
export async function runStepTierWritePlan(
  _read: StepTierRunRead,
  _write: StepTierRunWrite,
  _identity: { harness: "pi" | "claude"; model: string },
  _target: string | null,
  _limit = 32,
): Promise<StepTierRunResult> {
  return { ok: true };
}
