import { execFileAsync } from "./exec-file";
import { readPolyforgeMappedProjects } from "./polyforge-messages";
import { polyforgePfBin } from "./polyforge-pf-bin";
import {
  getPolyforgeWatch,
  maskPolyforgeCredentials,
  PolyforgeError,
  probePolyforgePresent,
  type PolyforgeBlocked,
  type PolyforgeDeclaredStep,
  type PolyforgePendingQuestion,
  type PolyforgeStep,
} from "./polyforge-status";
import { isPolyforgeWorkItemId } from "./polyforge-work-items-pure";

/**
 * Server-side reads for the Work › 待集成 view. Every fact
 * comes from a `pf` subprocess through `execFileAsync` (no shell) on
 * `polyforgePfBin()`; pi-web holds no aihub credential and calls no aihub
 * HTTP face. The reads, all read-only:
 * - the mapped projects: `pf --json config list` (`readPolyforgeMappedProjects`);
 * - per project, the landed items through aihub's generic attrs list filter:
 *   `pf --json queue --project <p> --attr integration.state=landed`;
 * - per project, the newest integrate batches:
 *   `pf --json queue --project <p> --bundle integrate --limit 5`;
 * - per batch that is not terminal, its own job data:
 *   `pf --json watch <wi> --once --demand --events 40` (`getPolyforgeWatch`).
 * Nothing here reads the daemon's status snapshot or any removed integration
 * face. A failure stays where it happened: a failed mapping read is the read's
 * `error`, a failed project read that project's `error`, a failed watch that
 * batch's `error` — each pf's own stderr, credentials masked, at most 512
 * characters.
 *
 * The sanitizers are the only passport off this module. A landed row keeps
 * id, slug, repos (`repo`, `pr` only when http(s), `merge_commit`), and
 * `waived` (only `true`), `batch`, `red_rounds`, `fix_tickets` when present;
 * a batch row keeps id, slug, status and — while it is not terminal — the
 * watch's workflowStatus, currentStep, steps, declared_steps, pendingQuestion
 * and blocked. Content, goal, heads, red heads, every other attr and the
 * watch's events, progress and origin never leave the server; nothing is
 * logged.
 */

/** One repo of a landed item: the PR (http(s) only) and its merge commit. */
export interface PolyforgeIntegrationRepo {
  repo: string;
  pr: string | null;
  merge_commit: string | null;
}

/** One landed item through the passport. */
export interface PolyforgeIntegrationLanded {
  id: string;
  slug: string;
  repos: PolyforgeIntegrationRepo[];
  waived?: true;
  batch?: string;
  red_rounds?: number;
  fix_tickets?: string[];
}

/** One integrate batch: the hub row plus, while it is not terminal, its watch. */
export interface PolyforgeIntegrationBatch {
  id: string;
  slug: string;
  status: string | null;
  workflowStatus?: string;
  currentStep?: string;
  steps?: PolyforgeStep[];
  declared_steps?: PolyforgeDeclaredStep[] | null;
  pendingQuestion?: PolyforgePendingQuestion;
  blocked?: PolyforgeBlocked;
  error?: string;
}

export interface PolyforgeIntegrationProject {
  project: string;
  landed: PolyforgeIntegrationLanded[];
  batches: PolyforgeIntegrationBatch[];
  error?: string;
}

export interface PolyforgeIntegrationRead {
  pfPresent: boolean;
  projects: PolyforgeIntegrationProject[];
  error?: string;
}

const READ_TIMEOUT_MS = 15_000;
/** A landed row is a whole hub work item (content included). */
const SPAWN_MAX_BUFFER = 16 * 1024 * 1024;
const ERROR_CAP = 512;
const BATCH_LIMIT = "5";
/** Hub statuses after which a batch has no live job to watch. */
const TERMINAL_STATUSES = new Set(["wrapped", "failed", "canceled"]);

type Row = Record<string, unknown>;

function asRow(value: unknown): Row | null {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Row : null;
}

function text(value: unknown): string | null {
  return typeof value === "string" && value !== "" ? value : null;
}

function httpUrl(value: unknown): string | null {
  return typeof value === "string" && /^https?:\/\//i.test(value) ? value : null;
}

/** pf's own failure text, credentials masked, capped. */
function errorText(error: unknown): string {
  const message = error instanceof Error && error.message ? error.message : "polyforge is unavailable";
  return maskPolyforgeCredentials(message).slice(0, ERROR_CAP);
}

function listItems(raw: unknown): Row[] {
  const answer = asRow(raw);
  return (Array.isArray(answer?.items) ? answer.items : []).flatMap((entry) => {
    const row = asRow(entry);
    return row && isPolyforgeWorkItemId(row.id) && text(row.slug) ? [row] : [];
  });
}

/** One hub row with `attrs.integration` → the landed passport. */
export function sanitizeLandedRow(row: Row): PolyforgeIntegrationLanded {
  const integration = asRow(asRow(row.attrs)?.integration) ?? {};
  const repos = (Array.isArray(integration.repos) ? integration.repos : []).flatMap((entry) => {
    const repo = asRow(entry);
    const name = text(repo?.repo);
    return repo && name ? [{ repo: name, pr: httpUrl(repo.pr), merge_commit: text(repo.merge_commit) }] : [];
  });
  const batch = text(integration.batch);
  const redRounds = integration.red_rounds;
  return {
    id: row.id as string,
    slug: row.slug as string,
    repos,
    ...(integration.waived === true ? { waived: true as const } : {}),
    ...(batch ? { batch } : {}),
    ...(typeof redRounds === "number" && Number.isSafeInteger(redRounds) && redRounds >= 0 ? { red_rounds: redRounds } : {}),
    ...(Array.isArray(integration.fix_tickets)
      ? { fix_tickets: integration.fix_tickets.filter((ticket): ticket is string => typeof ticket === "string") }
      : {}),
  };
}

async function runPfJson(argv: string[]): Promise<unknown> {
  let stdout: string;
  try {
    ({ stdout } = await execFileAsync(polyforgePfBin(), argv, { timeout: READ_TIMEOUT_MS, maxBuffer: SPAWN_MAX_BUFFER }));
  } catch (error) {
    throw PolyforgeError.fromSpawnError(error);
  }
  try {
    return JSON.parse(stdout);
  } catch {
    throw new PolyforgeError("pf returned an undecodable answer");
  }
}

/** One batch row; its watch only while it is not terminal. */
async function readBatch(row: Row): Promise<PolyforgeIntegrationBatch> {
  const status = text(row.status);
  const batch: PolyforgeIntegrationBatch = { id: row.id as string, slug: row.slug as string, status };
  if (status !== null && TERMINAL_STATUSES.has(status)) return batch;
  try {
    const watch = await getPolyforgeWatch(batch.id);
    if (!watch) return batch;
    return {
      ...batch,
      workflowStatus: watch.workflowStatus,
      ...(watch.currentStep ? { currentStep: watch.currentStep } : {}),
      steps: watch.steps,
      declared_steps: watch.declared_steps,
      ...(watch.pendingQuestion ? { pendingQuestion: watch.pendingQuestion } : {}),
      ...(watch.blocked ? { blocked: watch.blocked } : {}),
    };
  } catch (error) {
    return { ...batch, error: errorText(error) };
  }
}

async function readProject(project: string): Promise<PolyforgeIntegrationProject> {
  const [landed, batches] = await Promise.allSettled([
    runPfJson(["--json", "queue", "--project", project, "--attr", "integration.state=landed"])
      .then((raw) => listItems(raw).map(sanitizeLandedRow)),
    runPfJson(["--json", "queue", "--project", project, "--bundle", "integrate", "--limit", BATCH_LIMIT])
      .then((raw) => Promise.all(listItems(raw).map(readBatch))),
  ]);
  const failure = [landed, batches].find((result) => result.status === "rejected");
  return {
    project,
    landed: landed.status === "fulfilled" ? landed.value : [],
    batches: batches.status === "fulfilled" ? batches.value : [],
    ...(failure ? { error: errorText(failure.reason) } : {}),
  };
}

/** The whole view's read: pf absent → no project read; a failed mapping read →
 *  the read's error; otherwise every mapped project, each with its own error. */
export async function readPolyforgeIntegration(): Promise<PolyforgeIntegrationRead> {
  if (!(await probePolyforgePresent())) return { pfPresent: false, projects: [] };
  let projects: string[];
  try {
    projects = await readPolyforgeMappedProjects();
  } catch (error) {
    return { pfPresent: true, projects: [], error: errorText(error) };
  }
  return { pfPresent: true, projects: await Promise.all(projects.map(readProject)) };
}
