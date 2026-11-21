"use client";

// ModelCandidatePicker — the shared candidate picker of the three
// step-model surfaces (polyforge#151, 85b2, Q5): the workflow card's step
// candidate chain, CellSettings' summarize.models chips and the dock's
// single-model override. A select grouped by the step-tier catalog (default
// first, then the 85b alphabetical order, then the「未设置」group —
// lib/step-tiers.ts' pure grouping), with an invalid reference marked,
// BESIDE the manual fill of the full candidate string (kept whatever
// happens); a failed catalog read leaves the manual fill with its reason.
// Presentational: the parent owns the draft and the write; the picker never
// fetches (the dock's copy rides the watch snapshot — G11) and never saves.

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

export function ModelCandidatePicker({
  groups,
  reason = null,
  disabled = false,
  value,
  onChange,
  selectLabel,
  inputLabel,
  placeholder,
  inputProps,
}: ModelCandidatePickerProps) {
  const { t } = useI18n();
  return (
    <span className="polyforge-config-write model-candidate-picker">
      {/* the manual fill first — it is every row's value column (the layout
          suite pins the columns' left edges), the grouped select beside it */}
      <input
        type="text"
        disabled={disabled}
        aria-label={inputLabel}
        placeholder={placeholder}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        {...(inputProps ?? {})}
      />
      {groups !== null && (
        <select
          aria-label={selectLabel}
          disabled={disabled}
          value=""
          onChange={(event) => {
            const next = event.target.value;
            // the placeholder option re-selects nothing; a choice fills the
            // manual draft (the parent owns the write, never this select)
            if (next !== "") onChange(next);
            event.target.value = "";
          }}
        >
          <option value="">{t("polyforge.candidatePicker.choose")}</option>
          {groups.map((group) => (
            <optgroup key={group.tier ?? ""} label={group.tier ?? t("polyforge.candidatePicker.unsetGroup")}>
              {group.candidates.map((candidate) => (
                <option key={candidate.value} value={candidate.value}>
                  {candidate.invalid ? `${candidate.value}${t("polyforge.candidatePicker.invalid")}` : candidate.value}
                </option>
              ))}
            </optgroup>
          ))}
        </select>
      )}
      {groups === null && (
        <span className="polyforge-config-empty">
          {t("polyforge.candidatePicker.unavailable")}{reason ? `：${reason}` : ""}
        </span>
      )}
    </span>
  );
}
