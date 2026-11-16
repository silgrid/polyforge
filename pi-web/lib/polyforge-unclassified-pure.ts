/**
 * The sanitizer of the daemon's unclassified-error report
 * (`pf --json ops unclassified`, polyforge#51) — zero-value stub (r1#1
 * re-sequencing): the next commit restores the sanitizer.
 */

/** One deduplicated fallback text with its occurrences. */
export interface PolyforgeUnclassifiedItem {
  text: string;
  count: number;
  lastAt: string;
}

/** The Host-aggregated report (the window in days is the daemon's). */
export interface PolyforgeUnclassifiedReport {
  windowDays: number;
  items: PolyforgeUnclassifiedItem[];
}

export const UNCLASSIFIED_TEXT_MAX = 1000;
export const UNCLASSIFIED_ITEMS_MAX = 100;

/** Stub: no answer is a report yet. */
export function sanitizePolyforgeUnclassified(_value: unknown): PolyforgeUnclassifiedReport | null {
  return null;
}
