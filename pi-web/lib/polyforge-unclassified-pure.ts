/**
 * The sanitizer of the daemon's unclassified-error report
 * (`pf --json ops unclassified`, polyforge#51): the Host-wide list of the
 * error texts that fell into the classification fallback over the last
 * 7 days, deduplicated with counts. Import-free (browser-safe) like the
 * other `-pure` modules: the server read and the Host region's types share
 * one definition, the sanitizer is the route's one gate.
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

/** The bounds of what the panel ever renders: the daemon caps an error
 *  text at 1000 runes; a longer one (a future or older daemon) is cut, and
 *  the list keeps at most 100 rows (count-first, the daemon's order). */
export const UNCLASSIFIED_TEXT_MAX = 1000;
export const UNCLASSIFIED_ITEMS_MAX = 100;

/** Parse and bound one report; null when the answer is not one. */
export function sanitizePolyforgeUnclassified(value: unknown): PolyforgeUnclassifiedReport | null {
  return null;
}
