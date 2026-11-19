"use client";

// The 统一设置 shared component (polyforge#149, #85 85a1b, Q6 A): the parent
// explicitly provides the current value, each harness's read state and
// capability, and the submit callback; only a user click submits, once, to
// the applicable and writable harnesses. Presentational like SettingsUi.tsx
// (every word a prop, no state of its own). Placeholder signature; the body
// arrives with its unit and replaces this stub.

/** One harness's read of the unified value. */
export interface UnifiedSettingHarnessValue {
  harness: string;
  status: "ready" | "unsupported" | "not-installed" | "unknown" | "error";
  /** The harness's value as display text; non-null only while `status` is ready. */
  value: string | null;
  /** The visible reason a non-ready harness reads so — never folded into a tooltip. */
  reason?: string | null;
}

/** Every word the component shows; the parent passes its `t()` strings. */
export interface UnifiedSettingTexts {
  title: string;
  currentValue: string;
  unify: string;
  consistent: string;
  unsupported: string;
  saving: string;
}

export interface UnifiedSettingProps {
  texts: UnifiedSettingTexts;
  /** The parent-provided current value the 「统一成当前值」 click would write. */
  current: string | null;
  values: UnifiedSettingHarnessValue[];
  busy?: boolean;
  /** The user's one unified write; absent or never called unless the user clicks. */
  onUnify?: () => void;
}

/**
 * The four situations of AC3, derived only from the harness values:
 * consistent — every harness is ready and they all agree; unsupported — no
 * harness is ready; inconsistent — otherwise (never counting a non-ready
 * harness as agreeing, Q6 A).
 */
export function unifiedStatus(values: readonly UnifiedSettingHarnessValue[]): "consistent" | "inconsistent" | "unsupported" {
  const ready = values.filter((value) => value.status === "ready" && typeof value.value === "string");
  if (ready.length === 0) return "unsupported";
  if (ready.length === values.length && new Set(ready.map((value) => value.value)).size === 1) return "consistent";
  return "inconsistent";
}

export function UnifiedSetting(_props: UnifiedSettingProps): null {
  return null;
}
