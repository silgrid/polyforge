"use client";

// The 「应用到：全部 / 指定」 shared component (polyforge#149, #85 85a1b,
// Q6 A): choosing all or specific harnesses changes only the target — it
// never triggers a save; an empty specific selection cannot submit; the
// parent owns the selection and answers per target, so a failure keeps the
// draft and is never reported as a total success. Presentational like
// SettingsUi.tsx. Placeholder signature; the body arrives with its unit and
// replaces this stub.

export type HarnessApplyToMode = "all" | "specific";

export interface HarnessApplyToTarget {
  harness: string;
  label: string;
  disabled?: boolean;
  disabledReason?: string | null;
}

export interface HarnessApplyToSelection {
  mode: HarnessApplyToMode;
  selected: string[];
}

/** One target's feedback from the parent's last submit round. */
export interface HarnessApplyToResult {
  harness: string;
  ok: boolean;
  text: string;
}

export interface HarnessApplyToTexts {
  title: string;
  all: string;
  specific: string;
  submit: string;
}

export interface HarnessApplyToProps {
  texts: HarnessApplyToTexts;
  targets: HarnessApplyToTarget[];
  value: HarnessApplyToSelection;
  busy?: boolean;
  results?: HarnessApplyToResult[] | null;
  /** A target change only; it never submits. */
  onChange: (next: HarnessApplyToSelection) => void;
  /** The user's submit click; the parent writes each chosen target. */
  onSubmit: () => void;
}

/** The harnesses a submission would reach: every target, or the specific selection. */
export function applyToTargets(value: HarnessApplyToSelection, targets: readonly HarnessApplyToTarget[]): string[] {
  if (value.mode === "all") return targets.map((target) => target.harness);
  return targets.map((target) => target.harness).filter((harness) => value.selected.includes(harness));
}

/** False while a specific selection names no target: an empty target cannot submit (Q6 A). */
export function applyToCanSubmit(value: HarnessApplyToSelection, targets: readonly HarnessApplyToTarget[]): boolean {
  return applyToTargets(value, targets).length > 0;
}

export function HarnessApplyTo(_props: HarnessApplyToProps): null {
  return null;
}
