"use client";

// The 「应用到」 chip (polyforge#149, #85 85a1b Q6 A; polyforge#259 T4 AC2):
// one compact control shared by the skills page, MCP's rows and the models
// page's step-tier drawer. It is a native <details> chip — 「应用到：全部 ▾」 —
// whose menu holds the all/specific choice and the harness checkboxes, an
// unsupported harness greyed with its reason as visible text. Choosing a
// target changes only the target and **never submits**: the page that shows it
// owns the save (Q6 A; the ticket's "控件不负责提交"), so a failure keeps the
// draft and is never reported as a total success. Presentational like
// SettingsUi.tsx: every word a prop, no state of its own, so it renders outside
// the i18n provider and without a browser.

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
  /** The reader's line — translated, never the server's own English. */
  text: string;
  /** The server's own words, folded under the line (AC2: the fallback lives in a fold). */
  details?: string | null;
}

export interface HarnessApplyToTexts {
  /** The chip's own name (「应用到」). */
  title: string;
  all: string;
  specific: string;
  /** The fold's name for a result's server原文 (「详情」). */
  detailsLabel?: string;
}

export interface HarnessApplyToProps {
  texts: HarnessApplyToTexts;
  targets: HarnessApplyToTarget[];
  value: HarnessApplyToSelection;
  /** A target change only; it never submits. */
  onChange: (next: HarnessApplyToSelection) => void;
  /** Renders the menu open — a page whose whole point is this choice (the step-tier drawer). */
  open?: boolean;
  /** The parent's last round, one row per target. */
  results?: HarnessApplyToResult[] | null;
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
 * The 「应用到」 chip. The summary reads the current target (「应用到：全部」);
 * the menu holds the all/specific radios and the harness checkboxes; the last
 * round's per-target results sit under the chip, visible while it is closed. It
 * renders no button: the page's own save model
 * submits, and only on the user's click there.
 */
export function HarnessApplyTo({ texts, targets, value, onChange, open = false, results = null }: HarnessApplyToProps) {
  const name = `harness-apply-to-${texts.title.replace(/\W+/g, "-")}`;
  return (
    <>
    <details className="harness-apply-to" data-mode={value.mode} open={open}>
      <summary className="harness-apply-to-chip">
        <span className="harness-apply-to-chip-label">{texts.title}</span>
        <span className="harness-apply-to-chip-mode">{value.mode === "all" ? texts.all : texts.specific}</span>
        <svg className="harness-apply-to-chip-chevron" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="m6 9 6 6 6-6" />
        </svg>
      </summary>
      <div className="harness-apply-to-menu">
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
      </div>
    </details>
    {results !== null && results.length > 0 && (
        // Outside the <details> itself: a refusal must be readable without
        // opening the chip (polyforge#259 T4, review #6).
        <ul className="harness-apply-to-results">
          {results.map((result) => (
            <li key={result.harness} data-harness={result.harness} data-ok={result.ok}>
              {result.text}
              {result.details ? (
                <details className="harness-apply-to-result-details">
                  <summary>{texts.detailsLabel}</summary>
                  <span className="harness-apply-to-result-original">{result.details}</span>
                </details>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </>
  );
}