import { execFileAsync } from "./exec-file";
import { parsePfError } from "./polyforge-config-write";
import { isPolyforgeVersionBelowMinimum } from "./polyforge-detect";
import { MEMORY_BODY_FORWARD_CAP } from "./polyforge-memory";
import { capUtf8Bytes, memoryUtf8ByteLength } from "./polyforge-memory-pure";
import { polyforgePfBin } from "./polyforge-pf-bin";
import { probePolyforgeVersion } from "./polyforge-status";
import {
  isPolyforgeWorkItemId,
  type PolyforgeWorkItemBlocker,
  type PolyforgeWorkItemDetail,
  type PolyforgeWorkItemRow,
} from "./polyforge-work-items-pure";

/**
 * Server-side reads for the Work panel's read-only 工单列表 sub-tab.
 * Exactly two pf verbs, through `execFileAsync` (no shell)
 * on `polyforgePfBin()`: `pf --json queue` — ONE merged `{items, total}` over
 * every `[workspace]`-mapped project, with no `--project`, `--status` or
 * `--limit` (pf's server default is 500 rows per project, `created_at DESC`)
 * — and `pf --json file show <id>` (the id validated by
 * `isPolyforgeWorkItemId` before any spawn). No write verb exists here; the
 * HTTP faces under `app/api/polyforge/work-items/**` only degrade and answer.
 *
 * The sanitizers are the only passport off this module, and they forward pf
 * data verbatim (no trim, no coercion). A list row keeps exactly id, slug,
 * project, status, priority, bundle_name, goal, created_at: a nullable field
 * is null when pf sends null or a wrong type; a row without a `wi_` id or a
 * non-empty string slug is omitted. One conditional ninth key,
 * `acceptance_missing`, is forwarded only when pf sends `true` (pf queue's
 * mark on a queued execution ticket without acceptance) and
 * is otherwise absent; it is never on a detail. A detail keeps those
 * eight plus labels, milestone, reporter, assignee, updated_at, closed_at,
 * waiting_for (pf's derived blockers as `{slug, status}`), waiting_on (the
 * raw marker), content (cut to MEMORY_BODY_FORWARD_CAP UTF-8 bytes on a
 * code-point boundary) and content_len (the measured uncut length). Everything else — content and
 * waiting facts on list rows, attrs, project_id, parent_id, seq, clean_pause,
 * repo_inventory, session_* — never leaves the server; no response carries an
 * environment value and nothing is logged.
 */

/** The earliest tagged pf whose `queue` merges every mapped project without
 *  `--project` and that carries `file show`. Below it no queue/file verb is
 *  spawned. */
export const POLYFORGE_WORK_ITEMS_MINIMUM_PF_VERSION = "2.19.0";

/** The real merged queue answer is ≈5.3 MB of JSON; both bounds leave room. */
const READ_TIMEOUT_MS = 30_000;
const SPAWN_MAX_BUFFER = 16 * 1024 * 1024;
/** pf's own stderr passes through under this cap; nothing else is echoed. */
const STDERR_CAP = 512;

export { isPolyforgeWorkItemId } from "./polyforge-work-items-pure";
export type { PolyforgeWorkItemBlocker, PolyforgeWorkItemDetail, PolyforgeWorkItemRow } from "./polyforge-work-items-pure";

/** A bounded, typed read failure: pf's own stderr, or a fixed reason —
 *  never our argv, never an environment value. */
export class PolyforgeWorkItemsError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PolyforgeWorkItemsError";
  }

  static fromSpawnError(error: unknown): PolyforgeWorkItemsError {
    if (error instanceof Error) {
      const failure = error as { code?: unknown; killed?: unknown; signal?: unknown; stderr?: unknown };
      if (failure.code === "ERR_CHILD_PROCESS_STDIO_MAXBUFFER") {
        return new PolyforgeWorkItemsError("pf's answer exceeded the read buffer");
      }
      if (failure.killed === true || failure.signal === "SIGTERM") {
        return new PolyforgeWorkItemsError(`pf did not answer within ${READ_TIMEOUT_MS / 1000}s`);
      }
      const stderr = String(failure.stderr ?? "").trim();
      if (stderr) return new PolyforgeWorkItemsError(parsePfError(stderr).message.slice(0, STDERR_CAP));
    }
    return new PolyforgeWorkItemsError("polyforge is unavailable");
  }
}

// ---- pure sanitizers (the only passport) ----

type Row = Record<string, unknown>;

function asRow(value: unknown): Row | null {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Row : null;
}

/** pf's string as sent, or null (null, absent and wrong types alike). */
function text(value: unknown): string | null {
  return typeof value === "string" ? value : null;
}

/** One list row through the eight-key passport (plus `acceptance_missing`
 *  only when pf sends `true`); null when its id is not a `wi_` id or its
 *  slug is not a non-empty string. */
export function sanitizeWorkItemRow(raw: unknown): PolyforgeWorkItemRow | null {
  const row = asRow(raw);
  if (!row || !isPolyforgeWorkItemId(row.id) || typeof row.slug !== "string" || row.slug === "") return null;
  return {
    id: row.id,
    slug: row.slug,
    project: text(row.project),
    status: text(row.status),
    priority: text(row.priority),
    bundle_name: text(row.bundle_name),
    goal: text(row.goal),
    created_at: text(row.created_at),
    ...(row.acceptance_missing === true ? { acceptance_missing: true as const } : {}),
  };
}

/** The merged `pf queue` answer: the sanitized rows in pf's order, and pf's
 *  total when it is a finite non-negative integer, else the sanitized row
 *  count. */
export function sanitizeWorkItemListAnswer(raw: unknown): { items: PolyforgeWorkItemRow[]; total: number } {
  const answer = asRow(raw);
  const items = (Array.isArray(answer?.items) ? answer.items : []).flatMap((entry) => {
    const row = sanitizeWorkItemRow(entry);
    return row ? [row] : [];
  });
  const total = answer?.total;
  return {
    items,
    total: typeof total === "number" && Number.isSafeInteger(total) && total >= 0 ? total : items.length,
  };
}

function blockers(value: unknown): PolyforgeWorkItemBlocker[] | null {
  if (!Array.isArray(value)) return null;
  return value.flatMap((entry) => {
    const blocker = asRow(entry);
    return blocker ? [{ slug: text(blocker.slug), status: text(blocker.status) }] : [];
  });
}

/** One `pf file show` body through the eighteen-key passport, the content
 *  capped; null when the body is not a work item. */
export function sanitizeWorkItemDetail(raw: unknown): PolyforgeWorkItemDetail | null {
  const row = asRow(raw);
  const listRow = sanitizeWorkItemRow(raw);
  if (!row || !listRow) return null;
  // The list-only mark never reaches the detail's eighteen keys.
  delete listRow.acceptance_missing;
  const content = text(row.content);
  return {
    ...listRow,
    labels: Array.isArray(row.labels) ? row.labels.filter((label): label is string => typeof label === "string") : [],
    milestone: text(row.milestone),
    reporter: text(row.reporter),
    assignee: text(row.assignee),
    updated_at: text(row.updated_at),
    closed_at: text(row.closed_at),
    waiting_for: blockers(row.waiting_for),
    waiting_on: text(row.waiting_on),
    content: content === null ? null : capUtf8Bytes(content, MEMORY_BODY_FORWARD_CAP),
    content_len: content === null ? 0 : memoryUtf8ByteLength(content),
  };
}

// ---- transport ----

async function runPfJson(argv: string[]): Promise<unknown> {
  let stdout: string;
  try {
    ({ stdout } = await execFileAsync(polyforgePfBin(), argv, { timeout: READ_TIMEOUT_MS, maxBuffer: SPAWN_MAX_BUFFER }));
  } catch (error) {
    throw PolyforgeWorkItemsError.fromSpawnError(error);
  }
  try {
    return JSON.parse(stdout);
  } catch {
    throw new PolyforgeWorkItemsError("pf returned an undecodable answer");
  }
}

export interface PolyforgeWorkItemsCapability {
  pfPresent: boolean;
  pfVersion: string | null;
  capable: boolean;
  reason?: string;
}

/** The floor gate: presence + the 2.19.0 floor, from the one existing
 *  (60 s-cached) version probe. Below it no queue/file verb is spawned. */
export async function readPolyforgeWorkItemsCapability(): Promise<PolyforgeWorkItemsCapability> {
  const presence = await probePolyforgeVersion();
  if (!presence.present) return { pfPresent: false, pfVersion: null, capable: false };
  if (isPolyforgeVersionBelowMinimum(presence.version, POLYFORGE_WORK_ITEMS_MINIMUM_PF_VERSION)) {
    return {
      pfPresent: true,
      pfVersion: presence.version,
      capable: false,
      reason: `the work item list needs pf ${POLYFORGE_WORK_ITEMS_MINIMUM_PF_VERSION} or newer (found ${presence.version ?? "an unparseable version"})`,
    };
  }
  return { pfPresent: true, pfVersion: presence.version, capable: true };
}

/** The argv of the one merged list read — no project, status or limit. */
export const WORK_ITEMS_LIST_ARGV: readonly string[] = ["--json", "queue"];

/** The list face: exactly one merged `pf --json queue`. Any failure (one
 *  project failing fails pf's whole verb) throws; the caller answers an
 *  empty table, never a partial one. */
export async function listPolyforgeWorkItems(): Promise<{ items: PolyforgeWorkItemRow[]; total: number }> {
  return sanitizeWorkItemListAnswer(await runPfJson([...WORK_ITEMS_LIST_ARGV]));
}

/** The detail face: exactly one `pf --json file show <id>`; the caller has
 *  validated the id. A body that is not a work item degrades to null. */
export async function showPolyforgeWorkItem(id: string): Promise<PolyforgeWorkItemDetail | null> {
  return sanitizeWorkItemDetail(await runPfJson(["--json", "file", "show", id]));
}
