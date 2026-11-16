/**
 * Server-side pf calls of the Host region's unclassified-error list
 * (polyforge#51) — zero-value stub (r1#1 re-sequencing): the next commit
 * restores the pf read.
 */

import type { PolyforgeUnclassifiedReport } from "./polyforge-unclassified-pure";

/** The Host's unclassified-error report, sanitized (stub: empty). */
export async function readPolyforgeUnclassified(): Promise<PolyforgeUnclassifiedReport> {
  return { windowDays: 0, items: [] };
}
