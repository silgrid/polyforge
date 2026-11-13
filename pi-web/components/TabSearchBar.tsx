"use client";

import { useId } from "react";
import { useI18n } from "@/hooks/useI18n";

export interface TabSearchBarProps {
  value: string;
  onChange: (value: string) => void;
  caseSensitive: boolean;
  regexEnabled: boolean;
  onToggleCase: () => void;
  onToggleRegex: () => void;
  /**
   * Merged navigation state. `total` counts local matches plus earlier
   * (unloaded) matches; `current` is the 1-based position, or 0 when no
   * match is selected yet.
   */
  current: number;
  total: number;
  /** Local (loaded-content) matches; 0-when-absent lets callers omit it. */
  localCount?: number;
  earlierCount: number;
  loadingEarlier: boolean;
  hasEarlierHistory: boolean;
  onPrevious: () => void;
  onNext: () => void;
  onClose: () => void;
  /** Optional extra affordance (file viewer: search the rest of the file). */
  extraAction?: { label: string; onClick: () => void; busy?: boolean };
}

/**
 * Floating in-tab search bar (pi#80). Presentational and fully controlled:
 * matching, highlight painting and navigation live in the owning hook
 * (useTabSearch) so the chat pane and the file viewer share one bar.
 */
export function TabSearchBar({
  value,
  onChange,
  caseSensitive,
  regexEnabled,
  onToggleCase,
  onToggleRegex,
  current,
  total,
  earlierCount,
  loadingEarlier,
  hasEarlierHistory,
  onPrevious,
  onNext,
  onClose,
  extraAction,
}: TabSearchBarProps) {
  const { t } = useI18n();
  const inputId = useId();

  const counter = total === 0
    ? value.trim() ? t("tabSearch.noMatches") : ""
    : `${current || 1}/${total}`;
  const earlierNote = earlierCount > 0 ? t("tabSearch.earlierMatches", { count: earlierCount }) : null;

  return (
    <div className="tab-search-bar" role="search" aria-label={t("tabSearch.ariaLabel")}>
      <input
        id={inputId}
        type="text"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            event.preventDefault();
            onClose();
          } else if (event.key === "Enter") {
            event.preventDefault();
            if (event.shiftKey) onPrevious();
            else onNext();
          }
        }}
        placeholder={t("tabSearch.placeholder")}
        aria-keyshortcuts="Control+F Meta+F"
        spellCheck={false}
        autoComplete="off"
        className="tab-search-input"
        data-testid="tab-search-input"
      />
      <span
        className="tab-search-counter"
        aria-live="polite"
        data-testid="tab-search-counter"
        title={loadingEarlier ? t("tabSearch.searchingHistory") : undefined}
      >
        {loadingEarlier ? "…" : counter}
      </span>
      <button
        type="button"
        onClick={onToggleCase}
        className={`tab-search-toggle ${caseSensitive ? "tab-search-toggle-active" : ""}`}
        title={t("tabSearch.caseSensitive")}
        aria-label={t("tabSearch.caseSensitive")}
        aria-pressed={caseSensitive}
      >
        Aa
      </button>
      <button
        type="button"
        onClick={onToggleRegex}
        className={`tab-search-toggle ${regexEnabled ? "tab-search-toggle-active" : ""}`}
        title={t("tabSearch.regex")}
        aria-label={t("tabSearch.regex")}
        aria-pressed={regexEnabled}
      >
        .*
      </button>
      <button
        type="button"
        onClick={onPrevious}
        className="tab-search-nav"
        title={t("tabSearch.previousMatch")}
        aria-label={t("tabSearch.previousMatch")}
        disabled={total === 0}
      >
        ↑
      </button>
      <button
        type="button"
        onClick={onNext}
        className="tab-search-nav"
        title={t("tabSearch.nextMatch")}
        aria-label={t("tabSearch.nextMatch")}
        disabled={total === 0}
      >
        ↓
      </button>
      {extraAction && (
        <button
          type="button"
          onClick={extraAction.onClick}
          className="tab-search-extra"
          title={extraAction.label}
          disabled={extraAction.busy}
        >
          {extraAction.busy ? "…" : extraAction.label}
        </button>
      )}
      <button
        type="button"
        onClick={onClose}
        className="tab-search-close"
        title={t("tabSearch.close")}
        aria-label={t("tabSearch.close")}
      >
        ×
      </button>
      <span className="tab-search-earlier-note" title={earlierNote ?? undefined}>
        {earlierNote}
        {loadingEarlier && hasEarlierHistory ? ` · ${t("tabSearch.searchingHistory")}` : ""}
      </span>
    </div>
  );
}
