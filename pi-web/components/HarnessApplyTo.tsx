"use client";

// The 「应用到：全部 / 指定」 shared component (polyforge#149, #85 85a1b,
// Q6 A): choosing all or specific harnesses changes only the target — it
// never triggers a save; an empty specific selection cannot submit; the
// parent owns the selection and answers per target, so a failure keeps the
// draft and is never reported as a total success. Presentational like
// SettingsUi.tsx.

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
  /** The user's submit click; the parent writes each chosen target. Omitted
   * when the parent renders its own actions beside the target choice (85d). */
  onSubmit?: () => void;
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

/**
 * The 「应用到」 component (AC3): all harnesses, or the specific selection.
 * A target change calls `onChange` alone — never a save (Q6 A: choosing all
 * or specific only changes the target); `onSubmit` is the user's one click,
 * offered only while the selection names at least one target — a parent that
 * renders its own actions beside the choice may omit it, and then no submit
 * button shows; the parent owns the selection and answers per target, whose
 * last round renders as rows — so a failure keeps the draft and is never
 * folded into a success.
 * Presentational like SettingsUi.tsx: every word a prop, no state of its own.
 */
export function HarnessApplyTo({
  texts,
  targets,
  value,
  busy = false,
  results = null,
  onChange,
  onSubmit,
}: HarnessApplyToProps) {
  const canSubmit = applyToCanSubmit(value, targets);
  const name = `harness-apply-to-${texts.title.replace(/\W+/g, "-")}`;
  return (
    <div className="harness-apply-to" data-mode={value.mode}>
      <h3 className="settings-general-heading">{texts.title}</h3>
      <div role="radiogroup" aria-label={texts.title} className="harness-apply-to-modes">
        <label className="harness-apply-to-mode">
          <input
            type="radio"
            name={name}
            checked={value.mode === "all"}
            onChange={() => onChange({ mode: "all", selected: [...value.selected] })}
          />
          <span>{texts.all}</span>
        </label>
        <label className="harness-apply-to-mode">
          <input
            type="radio"
            name={name}
            checked={value.mode === "specific"}
            onChange={() => onChange({ mode: "specific", selected: [...value.selected] })}
          />
          <span>{texts.specific}</span>
        </label>
      </div>
      {value.mode === "specific" && (
        <ul className="harness-apply-to-targets">
          {targets.map((target) => (
            <li key={target.harness} data-harness={target.harness}>
              <label className={target.disabled ? "harness-apply-to-target is-disabled" : "harness-apply-to-target"}>
                <input
                  type="checkbox"
                  checked={value.selected.includes(target.harness)}
                  disabled={target.disabled}
                  onChange={(event) => onChange({
                    mode: "specific",
                    selected: event.target.checked
                      ? [...value.selected, target.harness]
                      : value.selected.filter((harness) => harness !== target.harness),
                  })}
                />
                <span>{target.label}</span>
              </label>
              {target.disabled && target.disabledReason && (
                <span className="harness-apply-to-target-reason">{target.disabledReason}</span>
              )}
            </li>
          ))}
        </ul>
      )}
      {onSubmit && (
        <button type="button" className="config-button" onClick={onSubmit} disabled={busy || !canSubmit}>
          {texts.submit}
        </button>
      )}
      {results !== null && results.length > 0 && (
        <ul className="harness-apply-to-results">
          {results.map((result) => (
            <li key={result.harness} data-harness={result.harness} data-ok={result.ok}>{result.text}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
