/**
 * Client-safe pure model for the Work panel's read-only 工单列表 (work item
 * list) sub-tab: the list-row and detail wire types (exactly
 * the key sets the server passport forwards), the work-item id validator,
 * the seven real statuses, the stable newest-first sort, the project filter
 * options, the exact-500-per-project cap hint, the one combined client-side
 * filter and the running count the sub-tab badge shows. No Node import lives
 * here, so both the server module and the client surface can use it.
 */

/** One merged `pf --json queue` row, exactly the eight keys the list
 *  passport forwards, plus the one conditional ninth key
 *  `acceptance_missing` (present only when pf sends `true`: a queued
 *  execution ticket with no acceptance section). Nullable
 *  fields stay null when pf sends null or a wrong-typed value (never
 *  coerced). */
export interface PolyforgeWorkItemRow {
  id: string;
  slug: string;
  project: string | null;
  status: string | null;
  priority: string | null;
  bundle_name: string | null;
  goal: string | null;
  created_at: string | null;
  acceptance_missing?: true;
}

/** One unfinished blocker of a queued work item, as pf derives it. */
export interface PolyforgeWorkItemBlocker {
  slug: string | null;
  status: string | null;
}

/** One `pf --json file show <id>` read, exactly the eighteen keys the detail
 *  passport forwards: the row fields, labels, milestone, people, times, the
 *  waiting facts (`waiting_for` pf's derived blockers, `waiting_on` the raw
 *  marker) and the content capped at the Memory forward cap, with
 *  `content_len` the measured uncut UTF-8 byte length. */
export interface PolyforgeWorkItemDetail extends PolyforgeWorkItemRow {
  labels: string[];
  milestone: string | null;
  reporter: string | null;
  assignee: string | null;
  updated_at: string | null;
  closed_at: string | null;
  waiting_for: PolyforgeWorkItemBlocker[] | null;
  waiting_on: string | null;
  content: string | null;
  content_len: number;
}

/** The server's per-project list cap (`defaultListLimit = 500`): a project
 *  contributing exactly this many rows may have older ones left unread. */
export const WORK_ITEMS_PROJECT_CAP = 500;

/** The seven real work-item statuses, in the status filter's order. */
export const POLYFORGE_WORK_ITEM_STATUSES = ["queued", "running", "paused", "blocked", "wrapped", "failed", "canceled"] as const;
export type PolyforgeWorkItemStatus = (typeof POLYFORGE_WORK_ITEM_STATUSES)[number];

const WORK_ITEM_ID = /^wi_[A-Za-z0-9]+$/;

/** A work-item id: `wi_` + alphanumerics. Anything else never reaches a spawn argv. */
export function isPolyforgeWorkItemId(value: unknown): value is string {
  return typeof value === "string" && WORK_ITEM_ID.test(value);
}

/** Globally newest first by `created_at`. The comparator is stable (equal
 *  instants keep pf's returned order) and rows whose instant is missing or
 *  unparseable go last, in pf's order. Pure, never throws. */
export function sortWorkItemsNewestFirst<T extends { created_at: string | null }>(rows: T[]): T[] {
  const instant = (row: T) => {
    const ms = typeof row.created_at === "string" ? Date.parse(row.created_at) : Number.NaN;
    return Number.isFinite(ms) ? ms : null;
  };
  return rows
    .map((row, index) => ({ row, index, at: instant(row) }))
    .sort((a, b) => {
      if (a.at !== null && b.at !== null && a.at !== b.at) return b.at - a.at;
      if (a.at === null && b.at !== null) return 1;
      if (a.at !== null && b.at === null) return -1;
      return a.index - b.index;
    })
    .map((entry) => entry.row);
}

/** The project filter's options: the distinct non-empty projects of the
 *  loaded list, sorted. Pure. */
export function workItemProjectOptions(rows: PolyforgeWorkItemRow[]): string[] {
  return [...new Set(rows.map((row) => row.project ?? "").filter(Boolean))].sort();
}

/** The projects that contributed exactly `WORK_ITEMS_PROJECT_CAP` rows —
 *  inferred from the rows already read (no extra read); 499 or 501 never
 *  trigger it. Pure. */
export function workItemsAtCap(rows: PolyforgeWorkItemRow[]): Set<string> {
  const counts = new Map<string, number>();
  for (const row of rows) {
    if (!row.project) continue;
    counts.set(row.project, (counts.get(row.project) ?? 0) + 1);
  }
  return new Set([...counts].filter(([, count]) => count === WORK_ITEMS_PROJECT_CAP).map(([project]) => project));
}

/** The one combined client-side predicate over the loaded rows: a
 *  case-insensitive substring of slug, goal or project (never content — the
 *  list carries none), then the project (`""` = every project) and the
 *  status (`"all"` = every status). Pure: filtering never reads. */
export function filterWorkItemRows(
  rows: PolyforgeWorkItemRow[],
  filter: { search: string; project: string; status: string },
): PolyforgeWorkItemRow[] {
  const wanted = filter.search.trim().toLowerCase();
  return rows.filter((row) => (!wanted || [row.slug, row.goal, row.project].some((field) => (field ?? "").toLowerCase().includes(wanted)))
    && (filter.project === "" || row.project === filter.project)
    && (filter.status === "all" || row.status === filter.status));
}

/** The sub-tab badge's value: the rows whose status is exactly `running`. Pure. */
export function workItemsRunningCount(rows: PolyforgeWorkItemRow[]): number {
  return rows.filter((row) => row.status === "running").length;
}
