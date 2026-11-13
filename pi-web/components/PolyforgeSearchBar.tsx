"use client";

import type { ButtonHTMLAttributes, ReactNode } from "react";
import { useI18n } from "@/hooks/useI18n";

/**
 * The one Polyforge search-bar layout, shared by Work ›
 * 工单列表, Memory and Tasks: an optional `<form>` root (Memory submits on
 * Enter), row 1 the search input filling the width with one square icon action
 * on its right, row 2 that surface's filter controls. The root carries
 * `data-pf-searchbar`; the component only lays it out — every surface keeps
 * its own search semantics, filters and reads.
 */
export interface PolyforgeSearchBarProps {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
  /** The accessible name (aria-label + title) of the input. */
  label: string;
  /** The square icon action rendered at the input's right. */
  action?: ReactNode;
  /** The filter row's controls. */
  children?: ReactNode;
  /** Memory renders a real form so Enter submits; other surfaces do not. */
  onSubmit?: (event: React.FormEvent<HTMLFormElement>) => void;
  /** The Memory form owns the surface padding/border; the Work sub-tabs
   *  already sit inside their padded header. */
  padded?: boolean;
}

export function PolyforgeSearchBar({
  value,
  onChange,
  placeholder,
  label,
  action,
  children,
  onSubmit,
  padded = false,
}: PolyforgeSearchBarProps) {
  const body = (
    <>
      <div className="pf-searchbar-row" data-pf-searchbar-row="">
        <input
          type="search"
          className="pf-searchbar-input"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          placeholder={placeholder}
          aria-label={label}
          title={label}
        />
        {action}
      </div>
      <div className="pf-searchbar-filters" data-pf-searchbar-filters="">{children}</div>
    </>
  );
  const className = `pf-searchbar${padded ? " pf-searchbar--padded" : ""}`;
  if (onSubmit) {
    return (
      <form data-pf-searchbar="" className={className} onSubmit={onSubmit}>
        {body}
      </form>
    );
  }
  return (
    <div data-pf-searchbar="" className={className}>
      {body}
    </div>
  );
}

/** One filter control of the shared bar: the desktop 26px geometry and the
 *  ≤640px 40px touch target live in the shared `app/globals.css` rules,
 *  never inline. */
export const POLYFORGE_SEARCH_CONTROL_CLASS = "pf-searchbar-control";

/** The any-word checkbox's whole clickable label cell. */
export const POLYFORGE_SEARCH_CHECK_CLASS = "pf-searchbar-check";

/**
 * The shared square icon action: Tasks' existing #204 ↻ and
 * the 工单列表's new manual re-read. Its busy state has one source — the
 * newest list read still in flight — and only that newest read clears it, so
 * a stale read never ends the newer one's spin.
 */
export function PolyforgeRefreshButton({
  refreshing,
  onClick,
  ...rest
}: { refreshing: boolean; onClick: () => void } & Omit<ButtonHTMLAttributes<HTMLButtonElement>, "onClick">) {
  const { t } = useI18n();
  return (
    <button
      type="button"
      className="pf-searchbar-action"
      data-pf-searchbar-action=""
      onClick={onClick}
      disabled={refreshing}
      aria-busy={refreshing}
      title={refreshing ? t("polyforge.refreshing") : t("polyforge.refresh")}
      aria-label={refreshing ? t("polyforge.refreshing") : t("polyforge.refresh")}
      {...rest}
    >
      <svg className={refreshing ? "animate-spin" : undefined} width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M21 12a9 9 0 1 1-2.64-6.36L21 8" />
        <path d="M21 3v5h-5" />
      </svg>
    </button>
  );
}

/** The Memory tab's 🔍 submit: the icon is the shared action slot, its
 *  accessible name the existing `polyforge.memory.search` text. */
export function PolyforgeSearchSubmitButton({ label }: { label: string }) {
  return (
    <button
      type="submit"
      className="pf-searchbar-action"
      data-pf-searchbar-action=""
      title={label}
      aria-label={label}
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <circle cx="11" cy="11" r="7" />
        <path d="m20 20-4.35-4.35" />
      </svg>
    </button>
  );
}
