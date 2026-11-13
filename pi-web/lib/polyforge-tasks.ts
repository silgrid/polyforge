import { execFileAsync } from "./exec-file";
import { parsePfError } from "./polyforge-config-write";
import { isPolyforgeVersionBelowMinimum } from "./polyforge-detect";
import { polyforgePfBin } from "./polyforge-pf-bin";
import { MEMORY_BODY_FORWARD_CAP } from "./polyforge-memory";
import { capUtf8Bytes, memoryUtf8ByteLength } from "./polyforge-memory-pure";
import { probePolyforgeVersion } from "./polyforge-status";
import { isPolyforgeTaskId, type PolyforgeTaskDetail, type PolyforgeTaskRow } from "./polyforge-tasks-pure";

/**
 * Server-side reads for the dock's read-only Tasks tab.
 * Exactly two pf verbs, through `execFileAsync` (no shell): `pf --json task
 * list` and `pf --json task show <id>` (the id validated by
 * `isPolyforgeTaskId` before any spawn). There is no spawn and no cancel: the
 * tab is a read surface. The HTTP faces under `app/api/polyforge/tasks/**`
 * only degrade and answer.
 *
 * The sanitizers are the only passport off this module: a list row keeps
 * exactly nine keys — id/project/state/prompt_summary, the three timing facts
 * started_at?/ended_at?/wall_consumed_ns? and
 * report_tail?/reporter?: `report_tail` is pf's own bounded
 * last non-empty report line (never re-cut here), `reporter` the spawn-time
 * human display name or `machine`; each travels only when pf sends a string,
 * so a 2.20.12 pf without them still lists (the floor is unchanged). A detail keeps id/project/state/
 * candidates/max_duration_ns/max_tokens/wall_consumed_ns/tokens_consumed/
 * created_at/started_at?/ended_at?/prompt/report/report_len/truncated, the
 * report cut to MEMORY_BODY_FORWARD_CAP UTF-8 bytes on a code-point boundary
 * with `report_len` carrying the measured uncut length. pf nests a task's
 * spawn facts (project, prompt, candidates, budgets) under `spec`; they are
 * read only there. Every other field (error text, origin chat, delivery
 * marks) never leaves the server, no response carries an environment value
 * and nothing is logged.
 */

/** The first pf whose task list carries `prompt_summary`.
 *  Below it the tab shows its upgrade state and spawns no task verb. */
export const POLYFORGE_TASKS_MINIMUM_PF_VERSION = "2.20.12";

const READ_TIMEOUT_MS = 15000;
/** A 50-task list with summaries, or one report under its own cap, is far below this. */
const SPAWN_MAX_BUFFER = 8 * 1024 * 1024;
/** pf's own stderr passes through under this cap; nothing else is echoed. */
const STDERR_CAP = 512;

export { isPolyforgeTaskId } from "./polyforge-tasks-pure";
export type { PolyforgeTaskDetail, PolyforgeTaskRow } from "./polyforge-tasks-pure";

/** pf's typed/plain failure text, bounded: never our argv, never env values. */
export class PolyforgeTasksError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PolyforgeTasksError";
  }

  static fromSpawnError(error: unknown): PolyforgeTasksError {
    if (error instanceof Error) {
      const stderr = String((error as { stderr?: unknown }).stderr ?? "").trim();
      if (stderr) return new PolyforgeTasksError(parsePfError(stderr).message.slice(0, STDERR_CAP));
    }
    return new PolyforgeTasksError("polyforge is unavailable");
  }
}

// ---- pure sanitizers (the only passport) ----

type Row = Record<string, unknown>;

function asRow(value: unknown): Row | null {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Row : null;
}

function str(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function num(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? Math.max(0, Math.trunc(value)) : 0;
}

/** A spawn fact: pf nests them under the record's `spec` object. */
function specField(row: Row, name: string): unknown {
  return asRow(row.spec)?.[name];
}

/** A real instant, or absent: pf marshals an unset time as the zero time. */
function instant(value: unknown): string | undefined {
  const text = str(value);
  if (!text || text.startsWith("0001-01-01")) return undefined;
  return text;
}

/** One list row, cut to id/project/state/prompt_summary plus the timing
 *  facts (each only when pf carries it: a real instant, a numeric wall
 *  time) and pf's report_tail/reporter strings as sent;
 *  an invalid id drops it. */
export function sanitizeTaskRow(raw: unknown): PolyforgeTaskRow | null {
  const row = asRow(raw);
  if (!row) return null;
  const id = str(row.id);
  if (!isPolyforgeTaskId(id)) return null;
  const sanitized: PolyforgeTaskRow = {
    id,
    project: str(specField(row, "project")),
    state: str(row.state),
    prompt_summary: str(row.prompt_summary),
  };
  const started = instant(row.started_at);
  const ended = instant(row.ended_at);
  if (started) sanitized.started_at = started;
  if (ended) sanitized.ended_at = ended;
  if (typeof row.wall_consumed_ns === "number" && Number.isFinite(row.wall_consumed_ns)) sanitized.wall_consumed_ns = num(row.wall_consumed_ns);
  if (typeof row.report_tail === "string") sanitized.report_tail = row.report_tail;
  if (typeof row.reporter === "string") sanitized.reporter = row.reporter;
  return sanitized;
}

/** The `pf task list` answer: only its `tasks` list, each through the row passport. */
export function sanitizeTaskListAnswer(raw: unknown): PolyforgeTaskRow[] {
  const tasks = asRow(raw)?.tasks;
  if (!Array.isArray(tasks)) return [];
  return tasks.flatMap((entry) => {
    const row = sanitizeTaskRow(entry);
    return row ? [row] : [];
  });
}

/** One `pf task show` answer through the detail passport, the report capped. */
export function sanitizeTaskDetail(raw: unknown): PolyforgeTaskDetail | null {
  const row = asRow(raw);
  const listRow = sanitizeTaskRow(raw);
  if (!row || !listRow) return null;
  const candidates = specField(row, "candidates");
  const report = typeof row.report === "string" ? row.report : "";
  const detail: PolyforgeTaskDetail = {
    id: listRow.id,
    project: listRow.project,
    state: listRow.state,
    candidates: Array.isArray(candidates)
      ? candidates.filter((item): item is string => typeof item === "string" && item.trim() !== "").map((item) => item.trim())
      : [],
    max_duration_ns: num(specField(row, "max_duration_ns")),
    max_tokens: num(specField(row, "max_tokens")),
    wall_consumed_ns: num(row.wall_consumed_ns),
    tokens_consumed: num(row.tokens_consumed),
    created_at: str(row.created_at),
    prompt: typeof specField(row, "prompt") === "string" ? specField(row, "prompt") as string : "",
    report: capUtf8Bytes(report, MEMORY_BODY_FORWARD_CAP),
    report_len: memoryUtf8ByteLength(report),
    truncated: row.truncated === true,
  };
  const started = instant(row.started_at);
  const ended = instant(row.ended_at);
  if (started) detail.started_at = started;
  if (ended) detail.ended_at = ended;
  return detail;
}

// ---- transport ----

async function runPfJson(argv: string[]): Promise<unknown> {
  try {
    const { stdout } = await execFileAsync(polyforgePfBin(), argv, { timeout: READ_TIMEOUT_MS, maxBuffer: SPAWN_MAX_BUFFER });
    return JSON.parse(stdout);
  } catch (error) {
    throw PolyforgeTasksError.fromSpawnError(error);
  }
}

export interface PolyforgeTasksCapability {
  pfPresent: boolean;
  pfVersion: string | null;
  capable: boolean;
  reason?: string;
}

/** The Tasks floor gate: presence + the 2.20.12 floor, from the one existing
 *  version probe. Below it no task verb is spawned. */
export async function readPolyforgeTasksCapability(): Promise<PolyforgeTasksCapability> {
  const presence = await probePolyforgeVersion();
  if (!presence.present) return { pfPresent: false, pfVersion: null, capable: false };
  if (isPolyforgeVersionBelowMinimum(presence.version, POLYFORGE_TASKS_MINIMUM_PF_VERSION)) {
    return {
      pfPresent: true,
      pfVersion: presence.version,
      capable: false,
      reason: `the Tasks tab needs pf ${POLYFORGE_TASKS_MINIMUM_PF_VERSION} or newer (found ${presence.version ?? "an unparseable version"})`,
    };
  }
  return { pfPresent: true, pfVersion: presence.version, capable: true };
}

/** The list face: exactly one `pf --json task list` (every task of the local
 *  daemon, no project filter). */
export async function listPolyforgeTasks(): Promise<PolyforgeTaskRow[]> {
  return sanitizeTaskListAnswer(await runPfJson(["--json", "task", "list"]));
}

/** The detail face: exactly one `pf --json task show <id>`; the caller has
 *  validated the id. A malformed answer degrades to null, never a guess. */
export async function showPolyforgeTask(id: string): Promise<PolyforgeTaskDetail | null> {
  return sanitizeTaskDetail(await runPfJson(["--json", "task", "show", id]));
}
