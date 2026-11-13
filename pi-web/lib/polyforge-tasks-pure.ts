/**
 * Client-safe pure model for the dock's read-only Tasks tab:
 * the list-row and detail wire types (the row type names exactly the nine
 * keys the server passport forwards, report_tail/reporter included —
 *), the task id validator, the
 * running-on-top section split, the client-side search, the project/state
 * filters and the block timing helpers, and the reporter
 * filter's options and term (全部发起人, human display names
 * ascending, then `machine` last — from the whole loaded list). The report's
 * collapse threshold and truncation test are the Memory tab's own
 * (`MEMORY_COLLAPSE_BYTES`, `isMemoryBodyTruncated` in
 * `lib/polyforge-memory-pure.ts`) — one rule, one place. No Node import
 * lives here, so both the server module and the client panel can use it.
 */

/** One `pf task list` row, exactly the field set the sanitizer forwards. */
export interface PolyforgeTaskRow {
  id: string;
  project: string;
  state: string;
  /** The prompt's bounded first line (pf ≥ 2.20.12); never the full prompt. */
  prompt_summary: string;
  /** timing facts, each absent when pf has none (pf's zero
   *  time reads as absent): a running block ticks from `started_at`, a
   *  finished one shows `wall_consumed_ns` and how long ago `ended_at` was. */
  started_at?: string;
  ended_at?: string;
  wall_consumed_ns?: number;
  /** pf's bounded last non-empty report line (≤200 code
   *  points, an over-long line already `…`-prefixed by pf; `""` while
   *  running) — shown as sent, never re-cut. Absent from a pf that predates it. */
  report_tail?: string;
  /** the spawn-time human display name, or `machine`.
   *  Absent from a pf that predates it. */
  reporter?: string;
}

/** One `pf task show` read: status, candidates, budget, times, prompt and
 *  the report (capped at the memory body forward cap; `report_len` is the
 *  uncut UTF-8 byte length, `truncated` pf's own budget-cut flag). */
export interface PolyforgeTaskDetail {
  id: string;
  project: string;
  state: string;
  candidates: string[];
  max_duration_ns: number;
  max_tokens: number;
  wall_consumed_ns: number;
  tokens_consumed: number;
  created_at: string;
  started_at?: string;
  ended_at?: string;
  prompt: string;
  report: string;
  report_len: number;
  truncated: boolean;
}

const TASK_ID = /^task_[0-9a-f]{16}$/;

/** A task id: `task_` + 16 lowercase hex. Anything else never reaches a spawn argv. */
export function isPolyforgeTaskId(value: unknown): value is string {
  return typeof value === "string" && TASK_ID.test(value);
}

/** The two sections of the one list: 正在运行 holds exactly the `running`
 *  tasks and 全部 the rest, both in pf's order — every task is listed exactly
 *  once, the running ones on top. Pure. */
export function splitTaskSections(rows: PolyforgeTaskRow[]): { running: PolyforgeTaskRow[]; all: PolyforgeTaskRow[] } {
  return {
    running: rows.filter((row) => row.state === "running"),
    all: rows.filter((row) => row.state !== "running"),
  };
}

/** The client-side search: a case-insensitive substring of the id, project,
 *  state or prompt summary; an empty needle matches every row. Pure. */
export function taskMatches(row: PolyforgeTaskRow, needle: string): boolean {
  const wanted = needle.trim().toLowerCase();
  if (!wanted) return true;
  return [row.id, row.project, row.state, row.prompt_summary].some((field) => field.toLowerCase().includes(wanted));
}

// ---- filters ----

/** The state filter's options: everything, then the five pf task states. */
export const TASK_STATE_FILTERS = ["all", "running", "completed", "capped", "failed", "interrupted"] as const;
export type PolyforgeTaskStateFilter = (typeof TASK_STATE_FILTERS)[number];

/** The project filter's options: the distinct non-empty projects of the
 *  loaded list, sorted. Pure. */
export function taskProjectOptions(rows: PolyforgeTaskRow[]): string[] {
  return [...new Set(rows.map((row) => row.project).filter(Boolean))].sort();
}

/** pf's `reporter` sentinel for a task no human spawned. */
export const TASK_REPORTER_MACHINE = "machine";

/** The reporter filter's options, from the whole loaded list
 *  (never the filtered rows, so they do not cascade): the distinct non-empty
 *  human display names sorted ascending, and whether any row is `machine`
 *  (offered last). A row without the field (a pre-field pf) adds nothing. Pure. */
export function taskReporterOptions(rows: PolyforgeTaskRow[]): { humans: string[]; machine: boolean } {
  const reporters = rows.map((row) => row.reporter ?? "");
  return {
    humans: [...new Set(reporters.filter((reporter) => reporter !== "" && reporter !== TASK_REPORTER_MACHINE))].sort(),
    machine: reporters.includes(TASK_REPORTER_MACHINE),
  };
}

/** The one client-side filter over the already-read list: the search
 *  (`taskMatches`), then the project (`""` = every project), the state
 *  (`"all"` = every state) and the reporter (`null` or absent
 *  = every reporter, else an exact `row.reporter` match). Pure: filtering
 *  never reads. */
export function filterTaskRows(
  rows: PolyforgeTaskRow[],
  filter: { search: string; project: string; state: PolyforgeTaskStateFilter; reporter?: string | null },
): PolyforgeTaskRow[] {
  const reporter = filter.reporter ?? null;
  return rows.filter((row) => taskMatches(row, filter.search)
    && (filter.project === "" || row.project === filter.project)
    && (filter.state === "all" || row.state === filter.state)
    && (reporter === null || row.reporter === reporter));
}

// ---- block timing ----

function wholeSecondsSince(at: string | undefined, now: number): number | undefined {
  const ms = at ? Date.parse(at) : Number.NaN;
  if (!Number.isFinite(ms) || !Number.isFinite(now)) return undefined;
  return Math.max(0, Math.floor((now - ms) / 1000));
}

/** A block's duration in whole seconds: a running task counts `now -
 *  started_at` (the client clock ticks it); any other task shows pf's
 *  `wall_consumed_ns`. `undefined` when the needed field is absent — never
 *  NaN or negative. Pure. */
export function taskDurationSeconds(row: PolyforgeTaskRow, now: number): number | undefined {
  if (row.state === "running") return wholeSecondsSince(row.started_at, now);
  if (row.wall_consumed_ns === undefined || !Number.isFinite(row.wall_consumed_ns)) return undefined;
  return Math.max(0, Math.round(row.wall_consumed_ns / 1e9));
}

/** Whole seconds since a finished task's `ended_at`; `undefined` when the
 *  task has not ended (no `ended_at`) or the time is unreadable. Pure. */
export function taskEndedAgo(row: PolyforgeTaskRow, now: number): number | undefined {
  return wholeSecondsSince(row.ended_at, now);
}

/** A coarse age for "ended … ago": `45s`, `12m`, `3h`, `2d`. Pure. */
export function formatTaskAge(seconds: number): string {
  const total = Number.isFinite(seconds) && seconds > 0 ? Math.floor(seconds) : 0;
  if (total < 60) return `${total}s`;
  if (total < 3600) return `${Math.floor(total / 60)}m`;
  if (total < 86400) return `${Math.floor(total / 3600)}h`;
  return `${Math.floor(total / 86400)}d`;
}
