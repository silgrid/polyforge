import { execFileAsync } from "./exec-file";
import { readPolyforgeMappedProjects, PolyforgeMessagesError } from "./polyforge-messages";
import { polyforgePfBin } from "./polyforge-pf-bin";

/**
 * Server-side reads/writes for the Work › projects view (polyforge#30).
 * Every fact comes from a `pf` subprocess through `execFileAsync` (no
 * shell); pi-web holds no aihub credential and calls no aihub HTTP face.
 * The reads, all read-only:
 * - the mapped projects: `pf --json config list`
 *   (`readPolyforgeMappedProjects`, the same source the other Work tabs use);
 * - per project, its global state: `pf --json projects get <p>` — the
 *   sanitizer keeps exactly {project, status} (status defaulted to
 *   active for a pf below the v0.2.1 floor, whose answers carry no
 *   status field); description, repos, members and every other field
 *   never leave the server.
 * The one write:
 * - `pf --json projects restore <p>` — the reversible door; a failure
 *   throws pf's typed failure (capped), the route answers it as the
 *   action's error.
 * A failure stays where it happened: a failed mapping read is the read's
 * `error`, a failed project read that project's own `error` — each pf's
 * own stderr, credentials masked, at most 512 characters.
 */

/** Each read's own bound. */
const READ_TIMEOUT_MS = 15000;
/** pf's own stderr passes through under this cap; nothing else is echoed. */
const STDERR_CAP = 512;
/** A project name safe to pass as one argv value (no leading dash, no
 *  whitespace or control byte) — the same shape the members write uses. */
export const PROJECT_TOKEN = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/;

/** One mapped project of the list answer. */
export interface PolyforgeProjectRow {
  project: string;
  status: "active" | "deactivated";
  error?: string;
}

/** The GET /api/polyforge/projects answer. */
export interface PolyforgeProjectsRead {
  pfPresent: boolean;
  projects: PolyforgeProjectRow[];
  error?: string;
}

/** A project name is safe as one argv value, or nothing runs. */
export function isPolyforgeProjectToken(value: unknown): value is string {
  return typeof value === "string" && PROJECT_TOKEN.test(value);
}

/** The project's global state, defaulted: an answer without the status
 *  field (a pf below the v0.2.1 floor) is the active line, the only state
 *  such a server can hold. */
function sanitizeProjectStatus(raw: unknown): { status: "active" | "deactivated" } {
  const status = raw && typeof raw === "object" && typeof (raw as { status?: unknown }).status === "string"
    ? (raw as { status: string }).status
    : "active";
  return { status: status === "deactivated" ? "deactivated" : "active" };
}

async function runPfJson(argv: string[]): Promise<unknown> {
  try {
    const { stdout } = await execFileAsync(polyforgePfBin(), argv, { timeout: READ_TIMEOUT_MS });
    return JSON.parse(stdout);
  } catch (error) {
    throw PolyforgeMessagesError.fromSpawnError(error);
  }
}

/** The mapped projects with their global states. Never rejects: a failed
 *  mapping read is the answer's error with an empty list, a failed project
 *  read that project's own error row. */
export async function readPolyforgeProjects(pin: string[] = []): Promise<PolyforgeProjectsRead> {
  let projects: string[];
  try {
    projects = await readPolyforgeMappedProjects(pin);
  } catch (error) {
    const failure = error instanceof PolyforgeMessagesError ? error : PolyforgeMessagesError.fromSpawnError(error);
    return { pfPresent: true, projects: [], error: failure.message.slice(0, STDERR_CAP) };
  }
  const rows = await Promise.all(projects.map(async (project) => {
    try {
      const { status } = sanitizeProjectStatus(await runPfJson([...pin, "--json", "projects", "get", project]));
      return { project, status } satisfies PolyforgeProjectRow;
    } catch (error) {
      const failure = error instanceof PolyforgeMessagesError ? error : PolyforgeMessagesError.fromSpawnError(error);
      return { project, status: "active", error: failure.message.slice(0, STDERR_CAP) } satisfies PolyforgeProjectRow;
    }
  }));
  return { pfPresent: true, projects: rows };
}

/** Restore one deactivated project — exactly one `pf --json projects
 *  restore <p>`; a refusal throws pf's typed failure for the route to
 *  answer. The door is reversible and needs no confirmation. */
export async function restorePolyforgeProject(project: string, pin: string[] = []): Promise<void> {
  if (!isPolyforgeProjectToken(project)) {
    throw new PolyforgeMessagesError("project must match [A-Za-z0-9][A-Za-z0-9_.-]{0,127}");
  }
  await runPfJson([...pin, "--json", "projects", "restore", project]);
}
