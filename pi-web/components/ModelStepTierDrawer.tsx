"use client";

// Stub (polyforge#258 u5): the step-tier drawer, filled in by the
// implementation commit.

import type { ModelViewRow } from "@/lib/harness/model-view";
import type { StepTierItem, StepTierWrite } from "@/lib/step-tiers";

export interface StepTierColumn {
  capable: boolean;
  reason: string | null;
  tiers: StepTierItem[];
}

export type StepTierRead = () => Promise<
  { ok: true; items: Array<{ key: string; value?: unknown }>; hash: string | null }
  | { ok: false; error: string }
>;

export type StepTierWriteFn = (write: StepTierWrite, hash: string | null) => Promise<{ ok: true } | { ok: false; error?: string }>;

export function stepTierIdentity(row: Pick<ModelViewRow, "provider" | "id">, harness: string): { harness: "pi" | "claude"; model: string } | null {
  return null;
}

export function ModelStepTierDrawer(_props: {
  row: ModelViewRow;
  tier: StepTierColumn;
  tierRead: StepTierRead;
  tierWrite: StepTierWriteFn;
  onClose: () => void;
  onSaved?: () => void;
}) {
  return null;
}
