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

/** Runs one identity's plan to an empty plan. Every step re-reads the catalog
 *  with the hash its write expects (the CAS guards exactly that state); a
 *  read or write refusal stops there, and a plan still non-empty when the
 *  limit is spent is a failure — never a success reported after a partial
 *  write. A legitimate catalog may hold more tiers than the limit; the caller
 *  keeps the draft and retries. */
export async function runStepTierWritePlan(
  read: StepTierRunRead,
  write: StepTierRunWrite,
  identity: { harness: "pi" | "claude"; model: string },
  target: string | null,
  limit = 32,
): Promise<StepTierRunResult> {
  for (let guard = 0; guard < limit; guard++) {
    const current = await read();
    if (!current.ok) return { ok: false, reason: "read", error: current.error };
    const plan = stepTierWritePlan(stepTierItems(current.items), identity, target);
    if (plan.length === 0) return { ok: true };
    const answer = await write(plan[0], current.hash);
    if (!answer.ok) return { ok: false, reason: "write", error: answer.error };
  }
  // The limit bounds writes, not the confirmation: the last allowed write may
  // have emptied the plan, so read once more (never write) before failing.
  const final = await read();
  if (!final.ok) return { ok: false, reason: "read", error: final.error };
  const remaining = stepTierWritePlan(stepTierItems(final.items), identity, target);
  return remaining.length === 0 ? { ok: true } : { ok: false, reason: "limit" };
}
