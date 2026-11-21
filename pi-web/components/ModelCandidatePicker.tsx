"use client";

// ModelCandidatePicker — the shared candidate picker of the three
// step-model surfaces (polyforge#151, 85b2, Q5): the workflow card's step
// candidate chain, CellSettings' summarize.models chips and the dock's
// single-model override. A select grouped by the step-tier catalog (default
// first, then the 85b alphabetical order, then the「未设置」group —
// lib/step-tiers.ts' pure grouping), with an invalid reference marked,
// BESIDE the manual fill of the full candidate string (kept whatever
// happens); a failed catalog read leaves the manual fill with its reason.
// Presentational: the parent owns the draft and the write.

import { useI18n } from "@/hooks/useI18n";
import type { StepTierPickerGroup } from "@/lib/step-tiers";

export interface ModelCandidatePickerProps {
  /** The picker groups (lib/step-tiers.ts), or null when the catalog read
   *  failed — then only the manual fill shows, with the reason. */
  groups: StepTierPickerGroup[] | null;
  /** Why the catalog is unavailable (shown beside the manual fill). */
  reason?: string | null;
  disabled?: boolean;
  /** The manual draft — the parent owns it; a select choice fills it. */
  value: string;
  onChange: (next: string) => void;
  selectLabel: string;
  inputLabel: string;
  placeholder?: string;
  /** Extra props onto the manual input (data-* hooks, onKeyDown). */
  inputProps?: Record<string, unknown>;
}

export function ModelCandidatePicker(_props: ModelCandidatePickerProps) {
  void useI18n;
  return null;
}
