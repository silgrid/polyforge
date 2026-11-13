import { spawn } from "node:child_process";
import { isPolyforgeProfileName } from "./polyforge-cells-pure";
import { isPolyforgeWorkItemId } from "./polyforge-cards-pure";
import { execFileAsync } from "./exec-file";
import { polyforgePfBin } from "./polyforge-pf-bin";

/**
 * Server-side pf reads for the Polyforge dock panel (pi#108). The transport
 * (execFile via lib/exec-file.ts, no shell) and the pure logic (ref
 * validation, sanitizers, step ordering, event tail) live here together; the
 * HTTP faces in `app/api/polyforge/**` only degrade and answer.
 *
 * Subprocess contract (pf CLI, verified live 2026-10-18):
 * - presence probe: `pf --version` (exit 0, no daemon needed)
 * - ops: `pf --json ops status` → machine `{ env_names, workspaces,
 *   workflows_dir, machine_id, jobs[], queue: { pending, running,
 *   max_concurrent }, … }`
 * - watch: `pf --json watch <ref> --once --demand --events 40` → machine `{ known,
 *   slug, execution: { steps: Record<name, { status, started_at }> }, …,
 *   events[] }` — `--once` is mandatory (the resident watcher would stream
 *   forever and outlive the 10s timeout); `--demand` (pi#119) arms the
 *   summarize lane on every read (the open panel's 3s poll is this route's
 *   only in-app caller, so every read is a human-side read).
 *
 * The sanitizers are the only passport off this file: machine passthrough
 * never reaches a response (env names, workspace paths, raw event bodies,
 * session fingerprints, non-chat origins). A `needs_input` event's question
 * text and id are the one event-body exception. The
 * owner-approved additions (pi#117) still leave only through them:
 * `progress.command` masked + truncated server-side, `command_age_sec`,
 * `tool_calls`, `recent_files`, per-step `startedAt`, and the chat-origin
 * `owner`. The summarize-lane fields (pi#119) — `summary`,
 * `summary_age_sec`, `summary_error`, `summary_capped` — leave only through
 * the same passport: flat on the wire, flat on the response, each absent
 * when missing or malformed.
 *
 * The job card's writes ride here too, never on the dock's poll: the
 * owner's message to the current step agent, or the wake of a parked work
 * item (workflow contract v2, change 14; workflow contract 4,),
 * `sendPolyforgeOwnerMessage` (`pf --json answer <wi> --text=…`, no
 * question id). The card has no buttons: a parked work
 * item is woken by the message, and a park no agent can start shows the
 * copyable `pf resume` / `pf cancel` commands.
 */

const PRESENCE_PROBE_TIMEOUT_MS = 3000;
const READ_TIMEOUT_MS = 10000;
const PRESENCE_TTL_MS = 60000;
/** A failed presence probe is cached only this long: one
 *  timeout must not hide pf for a minute. */
const ABSENCE_TTL_MS = 5000;
const EVENT_TAIL = 40;
/** pf's own stderr passes through under this cap; nothing else is echoed. */
const STDERR_CAP = 512;

/** Cap on the command text that can leave the server (pi#117): the value is
 *  the program+args shape, ~120 chars, never a full raw command line. */
const COMMAND_MAX = 120;

/** Cap on the summary error text that can leave the server (pi#119): pf's
 *  own raw wording for a failed summarize call, verbatim but bounded (the
 *  same bound as the pf-stderr cap; a separate fact, so a separate
 *  constant). */
const SUMMARY_ERROR_MAX = 512;

/** Cap on a queued verification's reason: the daemon's own
 *  short gate wording ("heavy-run slots full (2/2 running)"), bounded like
 *  the other pf-worded texts. */
const VERIFY_REASON_MAX = 512;

/** Credential-shaped substrings masked server-side before a command can
 *  reach a response (pi#117, owner-approved): token prefixes (`sk-`,
 *  `ghp_`, `npm_`), env assignments whose name ends in `API_KEY`,
 *  `token=`/`password=`/`passwd=` assignments (the `=` forms, `--flag=`
 *  included), and the `--password <value>` flag form. Over-masking only
 *  costs display fidelity; under-masking leaks, so the value patterns are
 *  greedy `\S+`. No RegExp lookbehind (repo rule). */
const COMMAND_REDACTIONS: Array<[RegExp, string]> = [
  [/\bsk-[A-Za-z0-9_-]+/g, "sk-***"],
  [/\bghp_[A-Za-z0-9]+/g, "ghp_***"],
  [/\bnpm_[A-Za-z0-9]+/g, "npm_***"],
  [/\b([A-Za-z0-9_]*API_KEY=)\S+/gi, "$1***"],
  [/\b(token=|password=|passwd=)\S+/gi, "$1***"],
  [/(--password\s)\S+/gi, "$1***"],
];

/** Mask credential-shaped substrings (COMMAND_REDACTIONS), no truncation.
 *  Shared by `progress.command` and the Jobs-tab card progress entries
 *, which cap at their own length afterwards. Pure, never
 *  throws. */
export function maskPolyforgeCredentials(text: string): string {
  let masked = text;
  for (const [pattern, replacement] of COMMAND_REDACTIONS) {
    masked = masked.replace(pattern, replacement);
  }
  return masked;
}

/** Mask credential-shaped substrings, then truncate to COMMAND_MAX — the
 *  only shape in which `progress.command` may leave the server. A clean
 *  command passes verbatim (the program+args shape is the point); one
 *  carrying credential-shaped substrings never leaves unmasked. Pure,
 *  never throws. */
export function redactPolyforgeCommand(command: string): string {
  return maskPolyforgeCredentials(command).slice(0, COMMAND_MAX);
}

export class PolyforgeError extends Error {
  /** The typed refusal's reason (pf's `--json` error envelope's
   *  `extra.reason`, e.g. WAKE_CANNOT_START), when pf named one. */
  readonly reason?: string;

  constructor(message: string, reason?: string) {
    super(message);
    this.name = "PolyforgeError";
    if (reason) this.reason = reason;
  }

  /** The only text that survives a spawn failure: pf's own stderr, bounded.
   *  Never `error.message` (it carries our argv) and never `process.env`
   *  (a stale POLYFORGE_V1_API_KEY must leak nothing). */
  static fromSpawnError(error: unknown): PolyforgeError {
    if (error instanceof Error) {
      const stderr = String((error as { stderr?: unknown }).stderr ?? "").trim();
      if (stderr) return new PolyforgeError(stderr.slice(0, STDERR_CAP));
    }
    return new PolyforgeError("polyforge is unavailable");
  }
}

// ---- ref validation (before any subprocess argv) ----

/**
 * A single closed positional: slug `proj#123` (project part `[A-Za-z0-9]`
 * then dash-safe), id `wi_…`, or a message anchor's `project-123` form
 * (a sent anchor is the work item's stable short reference).
 * Anything else (spaces, flags, `;`) is refused — the ref never reaches argv.
 */
const WI_REF_PATTERN = /^(wi_[A-Za-z0-9]+|[A-Za-z0-9][A-Za-z0-9_-]*#\d+|[A-Za-z0-9][A-Za-z0-9_-]*-\d+)$/;

export function isPolyforgeWiRef(value: unknown): value is string {
  return typeof value === "string" && WI_REF_PATTERN.test(value);
}

// ---- response shapes (assumption 4: key names are ours to choose) ----

export interface PolyforgeQueue {
  running: number;
  max: number;
}

export interface PolyforgeJob {
  wi: string;
  slug: string;
  status: string;
  stepAgeSec: number;
  idleAgeSec: number;
  candidate: string;
  spawnN: number;
  /** the Cell (profile) running the job on the Host
   *  aggregate; absent from a pre-Cell pf. Equal wi literals on two Cells
   *  stay distinguishable by it. */
  profile?: string;
  /** the job's heavy dispatch (a step declaring
   *  `admission: heavy`, or its run request) waits in the heavy-run
   *  admission queue (`/status` job row `verify_queue`,
   *) — its 1-based position, the gate holding it (masked)
   *  and since when (RFC3339, absent when malformed). Absent when the job is
   *  not queued; such a job holds no job slot, so `queue.running` leaves it
   *  out. */
  verifyQueue?: PolyforgeVerifyQueue;
  /** the running step session's context-window usage (the
   *  `/status` job row's `context`), through sanitizeContextUsage; a
   *  missing window filled server-side from the chat header's catalog
   *  (fillPolyforgeContextWindows). Absent outside a dispatch or when
   *  nothing about it is known. */
  context?: PolyforgeContextUsage;
}

/** a running step session's context-window usage — the same
 *  numbers the chat header's meter shows for an interactive session. Each
 *  field is absent when unknown, never a guessed 0. */
export interface PolyforgeContextUsage {
  /** Tokens the session's conversation fills. */
  tokens?: number;
  /** The model's context window (> 0). */
  window?: number;
  /** tokens / window × 100. */
  percent?: number;
  /** The model id (`<provider>/<id>` for pi, claude's model name). */
  model?: string;
  /** The transcript timestamp the count was taken from. */
  asOf?: string;
}

export interface PolyforgeVerifyQueue {
  position: number;
  reason: string;
  since?: string;
}

/** The machine's one-shot task counts: the Work › Tasks
 *  badge reads `running`, and the Tasks list re-reads once when
 *  running+completed changes. pf's other task counters are dropped. */
export interface PolyforgeTaskCounts {
  running: number;
  completed: number;
}

export interface PolyforgeOpsStatus {
  queue: PolyforgeQueue;
  jobs: PolyforgeJob[];
  /** Always set by sanitizeOpsStatus (zeros when pf reports none); optional
   *  only because the config page builds a queue-only snapshot. */
  tasks?: PolyforgeTaskCounts;
}

export interface PolyforgeStep {
  name: string;
  status: string;
  /** Machine `started_at` (pi#117): present only on started steps, so the
   *  client can show start times and compute durations. */
  startedAt?: string;
}

/** Sanitized workflow declaration entry (pi#114): id only — `on_fail` and
 *  every other declared field are dropped server-side and never reach a
 *  response. */
export interface PolyforgeDeclaredStep {
  id: string;
}

export interface PolyforgeEvent {
  at: string;
  kind: string;
  step?: string;
  model?: string;
  from?: string;
  to?: string;
  cause?: string;
  gate?: string;
  target?: string;
  status?: string;
  /** `needs_input` and `awaiting_owner` only (owner decision
   *  2026-11-09; workflow contract v2 change 14): the question as the step
   *  asked it, verbatim, and its id (an ask's id is its stamp). */
  question?: string;
  question_id?: string;
  /** `owner_message` only (workflow contract v2): who sent it. */
  by?: string;
  /** `delivered` only (workflow contract v2): how the delivery went. */
  disposition?: string;
  /** `intent` only (workflow contract v2): the intent's kind (the body's
   *  `kind`, renamed so the event kind stays `intent`). */
  intent?: string;
}

/** Sanitized live progress (pi#117): every field is absent when missing or
 *  malformed on the wire — the client renders nothing for an absent field. */
export interface PolyforgeProgress {
  /** Masked credential-shaped substrings + ~120-char truncation, both
   *  server-side (redactPolyforgeCommand): the program+args shape, never
   *  secrets. */
  command?: string;
  command_age_sec?: number;
  tool_calls?: number;
  recent_files?: string[];
}

/** Sanitized origin (pi#117): exposed only for chat origins (kind "chat"
 *  means an interactive submit) — every other origin field is dropped, and
 *  any other kind degrades the whole origin to absent. the
 *  origin carries no chat id; a wire `chat_id` never leaves. */
export interface PolyforgeOrigin {
  kind: "chat";
  owner?: string;
}

/** The summarize-lane fields of a watch snapshot (pi#119), flat like on
 *  the wire: each is absent when missing or malformed — the client renders
 *  nothing for an absent field. */
export interface PolyforgeSummary {
  /** The last finished summarize line — narration, passed verbatim (the
   *  lane's own bound is 200 chars; no server-side cap, no masking). */
  summary?: string;
  /** Present only together with `summary`. */
  summary_age_sec?: number;
  /** Raw error text of the latest failed summarize call, verbatim (no
   *  masking — the dispatch design carries no credential in the summarize
   *  argv, logs or artifacts), capped server-side at 512 chars. */
  summary_error?: string;
  /** Present and true only at the lane's 20-calls-per-attempt cap. */
  summary_capped?: true;
}

/** A blocked workflow's cause (the watch snapshot's `execution.blocked`,
 *), read-only: its kind (`incomplete`, `write_failed`, …),
 *  step, reason and the action it requires — masked and capped; `evidence`
 *  never leaves the server (nor would any field the panel does not know). */
export interface PolyforgeBlocked {
  kind: string;
  step?: string;
  reason: string;
  requiredAction?: string;
}

export interface PolyforgeWatchStatus extends PolyforgeSummary {
  wi: string;
  slug: string;
  workflowStatus: string;
  currentStep?: string;
  updatedAt?: string;
  steps: PolyforgeStep[];
  declared_steps: PolyforgeDeclaredStep[] | null;
  events: PolyforgeEvent[];
  progress?: PolyforgeProgress;
  origin?: PolyforgeOrigin;
  /** (additive; a daemon without it sends none): a blocked
   *  workflow's cause and what releases it (main's read-only face). */
  blocked?: PolyforgeBlocked;
  /** Workflow contract v2 (change 14): the question the current step waits
   *  on — the current dispatch's open `pf step ask` (its id the ask's own stamp)
   *  — the question verbatim. Absent while nothing is asked. */
  pendingQuestion?: PolyforgePendingQuestion;
}

/** The watch snapshot's `execution.pending_question`, the fields the job
 *  card shows: id, step, the question verbatim and when it was asked. The
 *  asker's session and transcript path never leave the server. */
export interface PolyforgePendingQuestion {
  id: string;
  step?: string;
  question: string;
  askedAt?: string;
}

// ---- transport ----

/** Cached presence probe: `pf` reachable on PATH plus the version it
 *  reported. Cached server-side for 60s when present and for 5s when absent
 *  (a CLI installed while the server runs, or one a single
 *  timeout misjudged, reads present again on the first probe after those 5s).
 *  Any probe failure (ENOENT, timeout, kill) reads as absent — the UI
 *  affordance is gated on presence, never on daemon reachability. */
let presenceCache: { present: boolean; version: string | null; expiresAt: number } | null = null;

export interface PolyforgePresence {
  present: boolean;
  /** The version `pf --version` reported, verbatim; null when absent or unparseable. */
  version: string | null;
}

/** `pf` reachable on PATH (pi#108's original probe, boolean contract kept for
 *  its callers — the status route and the decision feed route). Same single
 *  `pf --version` spawn and cache (60s present, 5s absent) as
 *  `probePolyforgeVersion`. */
export async function probePolyforgePresent(): Promise<boolean> {
  return (await probePolyforgeVersion()).present;
}

/** The one `pf --version` spawn (presence + the reported version), cached
 *  module-side for 60s when present and 5s when absent. `force` bypasses the
 *  cache read (the settings section's 「重新检测」, pi#122) but still
 *  refreshes it — one spawn per probe, never a second detection
 *  implementation. */
export async function probePolyforgeVersion(force = false): Promise<PolyforgePresence> {
  const now = Date.now();
  if (!force && presenceCache && now < presenceCache.expiresAt) {
    return { present: presenceCache.present, version: presenceCache.version };
  }
  let present = false;
  let version: string | null = null;
  try {
    const stdout = await runPfVersion(PRESENCE_PROBE_TIMEOUT_MS);
    present = true;
    version = parsePolyforgeVersion(stdout);
  } catch {
    present = false;
  }
  presenceCache = { present, version, expiresAt: now + (present ? PRESENCE_TTL_MS : ABSENCE_TTL_MS) };
  return { present, version };
}


/** The one `pf --version` spawn. stdin is ignored (a pf that reads stdin must
 *  not turn a version probe into a timeout); stdout is collected, stderr is
 *  discarded, and the timeout kills a child that is still running.
 *
 *  The bound's verdict waits one loop turn: after a blocked
 *  event loop Node runs the expired timer before it processes the child's
 *  exit, so a child that exited in time would still read as timed out. On the
 *  next turn the pending exit has landed: an exited child is not killed and its
 *  own `close` settles the probe (that wait is bounded by one more timeout);
 *  only a child genuinely still running is killed and judged absent. */
function runPfVersion(timeoutMs: number): Promise<string> {
  return new Promise((resolve, reject) => {
    const child = spawn(polyforgePfBin(), ["--version"], { shell: false, stdio: ["ignore", "pipe", "ignore"] });
    let stdout = "";
    let settled = false;
    const timedOut = () => new Error("pf --version timed out");
    const timer = setTimeout(() => {
      setImmediate(() => {
        if (settled) return;
        if (child.exitCode === null && child.signalCode === null) {
          child.kill("SIGKILL");
          reject(timedOut());
          return;
        }
        setTimeout(() => reject(timedOut()), timeoutMs).unref();
      });
    }, timeoutMs);
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => { stdout += chunk; });
    child.on("error", (error) => {
      settled = true;
      clearTimeout(timer);
      reject(error);
    });
    child.on("close", (code) => {
      settled = true;
      clearTimeout(timer);
      if (code === 0) resolve(stdout);
      else reject(new Error(`pf --version exited ${code ?? 1}`));
    });
  });
}

const VERSION_VALUE_PATTERN = /^v?\d+\.\d+\.\d+(?:[-+][^\s]+)?$/;

/** Version parse (pure): `pf --version` stdout verbatim when it is a
 *  semver-ish `v?X.Y.Z` line (a prerelease/build suffix tolerated — the
 *  reported string is the point); anything else → null (version absent,
 *  still present), never an error. */
export function parsePolyforgeVersion(stdout: string): string | null {
  const value = stdout.trim();
  return VERSION_VALUE_PATTERN.test(value) ? value : null;
}

async function runPfJson(argv: string[]): Promise<unknown> {
  try {
    const { stdout } = await execFileAsync(polyforgePfBin(), argv, { timeout: READ_TIMEOUT_MS });
    return JSON.parse(stdout);
  } catch (error) {
    throw PolyforgeError.fromSpawnError(error);
  }
}

/** How long a completed `pf ops status` read is reused. The
 *  status poll and the cards poll share one spawn and one snapshot (the queue
 *  line and the cards agree) only when both land inside this window — aligned
 *  when the dock opens on Work › Jobs at the default 3 s. Out of phase (after
 *  a tab switch, or at another card interval) each poll spawns its own read
 *  and may show a different snapshot (docs/agents/polyforge-panel.md). */
export const OPS_STATUS_REUSE_MS = 1000;

/**
 * A shared reader over one async read: concurrent calls share
 * the one in-flight promise, a completed read is reused for `reuseMs`, and a
 * failed read is never reused (the next call reads again). Calls further apart
 * than `reuseMs` each read: sharing is a timing window, not a merge.
 */
export function createOpsStatusReader<T>(
  read: () => Promise<T>,
  now: () => number = Date.now,
  reuseMs: number = OPS_STATUS_REUSE_MS,
): () => Promise<T> {
  let inFlight: Promise<T> | null = null;
  let last: { value: T; at: number } | null = null;
  return () => {
    if (last && now() - last.at < reuseMs) return Promise.resolve(last.value);
    if (inFlight) return inFlight;
    const pending = read()
      .then((value) => {
        last = { value, at: now() };
        return value;
      })
      .finally(() => {
        if (inFlight === pending) inFlight = null;
      });
    inFlight = pending;
    return pending;
  };
}

/** One independent `pf --json ops status` read, never shared: the detect
 *  reachability check uses it, so an explicit re-check sees the daemon now. */
export async function readPolyforgeOpsStatus(): Promise<PolyforgeOpsStatus | null> {
  return sanitizeOpsStatus(await runPfJson(["--json", "ops", "status"]));
}

/** How long the pi model registry may take to answer a window lookup; past
 *  it the read goes on without the window (the next poll joins the same
 *  in-flight build), so a slow extension never holds the job list. */
const CONTEXT_CATALOG_TIMEOUT_MS = 3000;

/** The chat header's window for a step session's model (
 *  owner ruling A): claude → `claudeContextWindow` (pi's built-in anthropic
 *  catalog, the fallback the header uses before claude reports one); pi →
 *  the pi model registry's `<provider>/<id>` contextWindow, the registry a
 *  pi session on that model reads (built-ins, models.json, extension
 *  providers). null when there is none: no meter rather than a guess. Both
 *  modules load only when a window is missing. */
async function catalogContextWindow(harness: string, model: string): Promise<number | null> {
  if (harness === "claude") {
    const { claudeContextWindow } = await import("./claude-session");
    return claudeContextWindow(model);
  }
  if (harness !== "pi") return null;
  const slash = model.indexOf("/");
  if (slash <= 0 || slash === model.length - 1) return null;
  const { getModelRuntimeWithExtensions } = await import("./model-runtime");
  let timer: ReturnType<typeof setTimeout> | undefined;
  const runtime = await Promise.race([
    getModelRuntimeWithExtensions(),
    new Promise<null>((resolve) => { timer = setTimeout(() => resolve(null), CONTEXT_CATALOG_TIMEOUT_MS); }),
  ]).finally(() => clearTimeout(timer));
  const contextWindow = runtime?.getModel(model.slice(0, slash), model.slice(slash + 1))?.contextWindow;
  return typeof contextWindow === "number" && Number.isFinite(contextWindow) && contextWindow > 0 ? contextWindow : null;
}

/**
 * (owner ruling A): the daemon carries a window only when the
 * harness reported one (claude's `result.modelUsage`); a job whose `context`
 * has a model but no window gets the chat header's catalog window for its
 * harness (the candidate's `<harness>:` prefix), and the percent from it
 * when the count is known. A failed lookup leaves the window absent. The
 * snapshot is not mutated.
 */
export async function fillPolyforgeContextWindows(status: PolyforgeOpsStatus | null): Promise<PolyforgeOpsStatus | null> {
  if (!status || !status.jobs.some((job) => job.context?.model && job.context.window === undefined)) return status;
  const jobs = await Promise.all(status.jobs.map(async (job): Promise<PolyforgeJob> => {
    const context = job.context;
    if (!context?.model || context.window !== undefined) return job;
    const harness = job.candidate.split(":", 1)[0];
    const window = await catalogContextWindow(harness, context.model).catch(() => null);
    if (window === null) return job;
    return {
      ...job,
      context: {
        ...(context.tokens !== undefined ? { tokens: context.tokens } : {}),
        window,
        ...(context.tokens !== undefined ? { percent: (context.tokens / window) * 100 } : {}),
        model: context.model,
        ...(context.asOf ? { asOf: context.asOf } : {}),
      },
    };
  }));
  return { ...status, jobs };
}

/** The panel's read: one `pf --json ops status`, sanitized, a missing
 *  context window filled from the catalog. */
async function readPolyforgePanelOpsStatus(): Promise<PolyforgeOpsStatus | null> {
  return fillPolyforgeContextWindows(await readPolyforgeOpsStatus());
}

/** The one module-level shared `pf --json ops status` reader: the status
 *  route and the cards route share it, so their polls spawn once when they
 *  land within `OPS_STATUS_REUSE_MS` of each other (the same-phase case);
 *  out of phase each poll spawns its own read. */
export const getPolyforgeOpsStatus: () => Promise<PolyforgeOpsStatus | null> =
  createOpsStatusReader(readPolyforgePanelOpsStatus);

export async function getPolyforgeWatch(wiRef: string, profile?: string): Promise<PolyforgeWatchStatus | null> {
  if (!isPolyforgeWiRef(wiRef)) {
    throw new PolyforgeError("wi must be a slug (pi#107) or id (wi_…)");
  }
  if (profile !== undefined && !isPolyforgeProfileName(profile)) {
    throw new PolyforgeError("profile must match [a-z0-9][a-z0-9-]{0,31}");
  }
  // `--demand` (pi#119) arms the summarize lane on EVERY read: the open
  // panel's 3s poll is this route's only in-app caller, so every read is a
  // human-side read. Always passed, no client parameter, no fallback — a
  // pf older than v2.18.1 (no --demand) fails the read and the route
  // degrades through the existing unavailable path.
  // a row's Cell is addressed explicitly (never the cwd)
  const machine = await runPfJson(profile
    ? ["--profile", profile, "--json", "watch", wiRef, "--once", "--demand", "--events", String(EVENT_TAIL)]
    : ["--json", "watch", wiRef, "--once", "--demand", "--events", String(EVENT_TAIL)]);
  return sanitizeWatch(machine);
}

// ---- pure helpers ----

function toNumber(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function toTrimmedString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function toOptionalTrimmedString(value: unknown): string | undefined {
  const trimmed = toTrimmedString(value);
  return trimmed ? trimmed : undefined;
}

/**
 * Execution order: started steps by `started_at` ascending, then steps that
 * never started, ties broken by step name. `pf watch` exposes only each
 * step's `started_at` (no workflow declaration), so this ordering is the
 * plan's definition of "execution order". Started steps keep their
 * `startedAt` (pi#117); unstarted steps carry none.
 */
export function orderPolyforgeSteps(
  machineSteps: Record<string, { status?: unknown; started_at?: unknown }> | undefined,
): PolyforgeStep[] {
  if (!machineSteps || typeof machineSteps !== "object") return [];
  const entries = Object.entries(machineSteps).map(([name, step]) => ({
    name,
    status: toTrimmedString((step ?? {}).status) || "pending",
    startedAt: toOptionalTrimmedString((step ?? {}).started_at),
  }));
  const started = entries
    .filter((entry) => entry.startedAt)
    .sort((a, b) => (a.startedAt! < b.startedAt! ? -1 : a.startedAt! > b.startedAt! ? 1 : a.name.localeCompare(b.name)));
  const unstarted = entries
    .filter((entry) => !entry.startedAt)
    .sort((a, b) => a.name.localeCompare(b.name));
  return [...started, ...unstarted].map(({ name, status, startedAt }) =>
    startedAt ? { name, status, startedAt } : { name, status });
}

/**
 * Sanitize `execution.declared_steps` (pi#114). Keeps each entry's trimmed
 * non-empty `id`, in declaration order; `on_fail` and every other field are
 * dropped server-side (raw shape is proposed to companion WI #213). Absent,
 * null, non-array, empty, or any entry without a valid id → null — the
 * client then falls back to started-at ordering. Pure, never throws, so the
 * route's 2xx degradation contract is untouched.
 */
export function sanitizeDeclaredSteps(value: unknown): PolyforgeDeclaredStep[] | null {
  if (!Array.isArray(value) || value.length === 0) return null;
  const steps: PolyforgeDeclaredStep[] = [];
  for (const entry of value) {
    const id = entry && typeof entry === "object" ? toTrimmedString((entry as { id?: unknown }).id) : "";
    if (!id) return null;
    steps.push({ id });
  }
  return steps;
}

/** Owner-approved per-kind event body whitelist (pi#112). Every kind not
 *  listed here passes through only `body.step` (when present); all other
 *  body keys — detail, evidence, error, answered_by, files, reason,
 *  from_model/to_model, spawn_n, … — never reach a response. The one
 *  relaxation (owner decision 2026-11-09): a `needs_input`
 *  event carries its question text and question id, so the panel can show
 *  the question verbatim; no other kind gains a field. */
/** The per-kind body whitelist (pi#112): a field name, or `[from, to]` when
 *  the body's field is written under another name (an intent's `kind` would
 *  otherwise overwrite the event's kind). Workflow contract v2 (change 14):
 *  `awaiting_owner` carries the question like `needs_input`; `owner_message`
 *  its sender; `delivered` its disposition; `intent` its kind and target;
 *  the words of an owner message and the reason of an intent ride the
 *  progress stream (the event log), never the watch tail. */
const EVENT_BODY_FIELDS: Record<string, readonly (string | readonly [string, string])[]> = {
  step_started: ["step", "model"],
  candidate_switched: ["step", "from", "to"],
  step_failed: ["step", "cause"],
  step_dead: ["step", "cause"],
  rewind: ["gate", "target"],
  terminal: ["status"],
  needs_input: ["step", "question", "question_id"],
  awaiting_owner: ["step", "question", "question_id"],
  owner_message: ["step", "by"],
  delivered: ["step", "disposition"],
  intent: ["step", ["kind", "intent"], "to"],
};

/** Fields passed as written (only a blank value is absent): the question is
 *  shown verbatim, never trimmed or summarized. */
const VERBATIM_EVENT_FIELDS = new Set(["question"]);

/** Clamp to the newest `maxEvents`, order preserved (newest last). */
export function tailPolyforgeEvents(events: unknown[], maxEvents: number = EVENT_TAIL): PolyforgeEvent[] {
  if (!Array.isArray(events)) return [];
  return events.slice(-maxEvents).map((event): PolyforgeEvent | null => {
    if (!event || typeof event !== "object") return null;
    const e = event as { at?: unknown; kind?: unknown; body?: unknown };
    const kind = toTrimmedString(e.kind);
    const at = toTrimmedString(e.at);
    if (!at || !kind) return null;
    const mapped: PolyforgeEvent = { at, kind };
    // Per-kind body whitelist (pi#112): only the owner-approved fields for
    // each kind pass through; every other body key is dropped before it can
    // reach a response. Unknown kinds pass through at most `body.step`.
    if (e.body && typeof e.body === "object") {
      const body = e.body as Record<string, unknown>;
      const fields = EVENT_BODY_FIELDS[kind] ?? ["step"];
      for (const entry of fields) {
        const [field, as] = typeof entry === "string" ? [entry, entry] : entry;
        const raw = body[field];
        const value = VERBATIM_EVENT_FIELDS.has(field)
          ? (typeof raw === "string" && raw.trim() ? raw : undefined)
          : toOptionalTrimmedString(raw);
        if (value !== undefined) {
          (mapped as unknown as Record<string, string | undefined>)[as] = value;
        }
      }
    }
    return mapped;
  }).filter((event): event is PolyforgeEvent => event !== null);
}

/** a job row's `verify_queue` → `verifyQueue`. Only a
 *  positive integer `position` with a non-empty `reason` marks the job as
 *  queued; the reason passes the credential mask and VERIFY_REASON_MAX,
 *  `since` only as a string. Anything else reads as not queued. Pure. */
function sanitizeVerifyQueue(value: unknown): PolyforgeVerifyQueue | undefined {
  if (!value || typeof value !== "object") return undefined;
  const q = value as { position?: unknown; reason?: unknown; since?: unknown };
  if (typeof q.position !== "number" || !Number.isInteger(q.position) || q.position < 1) return undefined;
  const reason = maskPolyforgeCredentials(toTrimmedString(q.reason)).slice(0, VERIFY_REASON_MAX);
  if (!reason) return undefined;
  const since = toTrimmedString(q.since);
  return { position: q.position, reason, ...(since ? { since } : {}) };
}

/** The daemon's own model-id bound: at most 128 bytes of an id charset. */
const CONTEXT_MODEL_PATTERN = /^[A-Za-z0-9._:/@+-]{1,128}$/;
const CONTEXT_AS_OF_MAX = 64;

function toNonNegativeNumber(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : undefined;
}

/** a job row's `context` → `context`. Only finite
 *  non-negative `tokens`/`percent`, a positive `window`, a `model` in the
 *  daemon's id charset and an `as_of` that parses as a time pass; every
 *  other wire field is dropped. Without a valid count, window or model the
 *  whole field is absent. Pure. */
function sanitizeContextUsage(value: unknown): PolyforgeContextUsage | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const c = value as { tokens?: unknown; window?: unknown; percent?: unknown; model?: unknown; as_of?: unknown };
  const tokens = toNonNegativeNumber(c.tokens);
  const window = toNonNegativeNumber(c.window);
  const percent = toNonNegativeNumber(c.percent);
  const model = typeof c.model === "string" && CONTEXT_MODEL_PATTERN.test(c.model) ? c.model : undefined;
  const asOf = typeof c.as_of === "string" && c.as_of.length <= CONTEXT_AS_OF_MAX && Number.isFinite(Date.parse(c.as_of)) ? c.as_of : undefined;
  const context: PolyforgeContextUsage = {
    ...(tokens !== undefined ? { tokens } : {}),
    ...(window !== undefined && window > 0 ? { window } : {}),
    ...(percent !== undefined ? { percent } : {}),
    ...(model ? { model } : {}),
    ...(asOf ? { asOf } : {}),
  };
  return context.tokens !== undefined || context.window !== undefined || context.model ? context : undefined;
}

/**
 * Machine ops status → response snapshot. Keeps `queue.running`,
 * `queue.max_concurrent`, per-job wi/slug/status/stepAgeSec/idleAgeSec/
 * candidate/spawnN/verifyQueue/context and `tasks.running`/`tasks.completed` (a
 * missing or malformed `tasks` reads as zeros). Drops the other task
 * counters (`capped`, `failed`, `interrupted`, `limit`), `env_names`,
 * `workspaces`, `workflows_dir`, `machine_id`, `shutting_down`,
 * `session_fingerprint`, `origin`.
 */
export function sanitizeOpsStatus(machine: unknown): PolyforgeOpsStatus | null {
  if (!machine || typeof machine !== "object") return null;
  const m = machine as { queue?: unknown; jobs?: unknown; tasks?: unknown };
  if (!m.queue || typeof m.queue !== "object" || m.queue === null) return null;
  if (!Array.isArray(m.jobs)) return null;
  const queue = m.queue as { running?: unknown; max_concurrent?: unknown };
  const jobs = m.jobs.flatMap((job): PolyforgeJob[] => {
    if (!job || typeof job !== "object") return [];
    const j = job as {
      work_item_id?: unknown;
      slug?: unknown;
      status?: unknown;
      spawn_n?: unknown;
      step_age_sec?: unknown;
      last_work_ago_sec?: unknown;
      candidate?: unknown;
      profile?: unknown;
      verify_queue?: unknown;
      context?: unknown;
    };
    const verifyQueue = sanitizeVerifyQueue(j.verify_queue);
    const context = sanitizeContextUsage(j.context);
    const slug = toTrimmedString(j.slug);
    if (!slug) return [];
    return [{
      wi: toTrimmedString(j.work_item_id),
      slug,
      status: toTrimmedString(j.status),
      stepAgeSec: toNumber(j.step_age_sec),
      idleAgeSec: toNumber(j.last_work_ago_sec),
      candidate: toTrimmedString(j.candidate),
      spawnN: toNumber(j.spawn_n),
      ...(isPolyforgeProfileName(j.profile) ? { profile: j.profile } : {}),
      ...(verifyQueue ? { verifyQueue } : {}),
      ...(context ? { context } : {}),
    }];
  });
  const tasks = (m.tasks && typeof m.tasks === "object" ? m.tasks : {}) as { running?: unknown; completed?: unknown };
  return {
    queue: {
      running: toNumber(queue.running),
      max: toNumber(queue.max_concurrent),
    },
    jobs,
    tasks: {
      running: toNumber(tasks.running),
      completed: toNumber(tasks.completed),
    },
  };
}

/**
 * Sanitize machine `progress` (pi#117): only `command` (masked + truncated
 *  server-side via redactPolyforgeCommand), `command_age_sec`, `tool_calls`
 *  and `recent_files` pass; every other machine field is dropped. Each
 *  field is absent when missing or malformed; a progress object with no
 *  valid field degrades the whole field to absent. Pure, never throws.
 */
function sanitizeProgress(value: unknown): PolyforgeProgress | undefined {
  if (!value || typeof value !== "object") return undefined;
  const p = value as { command?: unknown; command_age_sec?: unknown; tool_calls?: unknown; recent_files?: unknown };
  const sanitized: PolyforgeProgress = {};
  const command = toOptionalTrimmedString(p.command);
  if (command) sanitized.command = redactPolyforgeCommand(command);
  if (typeof p.command_age_sec === "number" && Number.isFinite(p.command_age_sec)) {
    sanitized.command_age_sec = p.command_age_sec;
  }
  if (typeof p.tool_calls === "number" && Number.isFinite(p.tool_calls)) {
    sanitized.tool_calls = p.tool_calls;
  }
  // recent_files passes the whitelist as path strings; anything that is not
  // an array of strings degrades to absent — never an error (the field's
  // exact pf wire shape is unobserved, owner-stated).
  if (Array.isArray(p.recent_files) && p.recent_files.every((file) => typeof file === "string")) {
    sanitized.recent_files = p.recent_files as string[];
  }
  return Object.keys(sanitized).length > 0 ? sanitized : undefined;
}

/**
 * Sanitize the machine job origin (pi#117): `kind` and `owner` are exposed
 * only when `kind === "chat"` (no `chat_id`) — every other origin field is
 *  dropped, and any other kind (or a malformed job/origin) degrades the
 * whole origin to absent. Pure, never throws.
 */
function sanitizeOrigin(job: unknown): PolyforgeOrigin | undefined {
  if (!job || typeof job !== "object") return undefined;
  const origin = (job as { origin?: unknown }).origin;
  if (!origin || typeof origin !== "object") return undefined;
  const o = origin as { kind?: unknown; owner?: unknown };
  if (toTrimmedString(o.kind) !== "chat") return undefined;
  const sanitized: PolyforgeOrigin = { kind: "chat" };
  const owner = toOptionalTrimmedString(o.owner);
  if (owner) sanitized.owner = owner;
  return sanitized;
}

/**
 * Sanitize the flat summarize-lane wire fields (pi#119): `summary` (a
 *  trimmed non-empty string, verbatim — the lane's own bound is 200 chars,
 *  no server-side cap), `summary_age_sec` (a finite number, kept only
 *  together with `summary`), `summary_error` (a trimmed non-empty string,
 *  verbatim and capped at SUMMARY_ERROR_MAX — no masking: the dispatch
 *  design carries no credential in the summarize argv, logs or artifacts,
 *  so raw-never-faked holds literally) and `summary_capped` (kept only
 *  when the wire value is exactly `true`). Each field is absent when
 *  missing or malformed on the wire, never an error, and no other machine
 *  field leaks. Pure, never throws.
 */
function sanitizeSummaryFields(machine: {
  summary?: unknown;
  summary_age_sec?: unknown;
  summary_error?: unknown;
  summary_capped?: unknown;
}): PolyforgeSummary {
  const summary = toOptionalTrimmedString(machine.summary);
  const summaryError = toOptionalTrimmedString(machine.summary_error);
  const fields: PolyforgeSummary = {};
  if (summary) {
    fields.summary = summary;
    if (typeof machine.summary_age_sec === "number" && Number.isFinite(machine.summary_age_sec)) {
      fields.summary_age_sec = machine.summary_age_sec;
    }
  }
  if (summaryError) fields.summary_error = summaryError.slice(0, SUMMARY_ERROR_MAX);
  if (machine.summary_capped === true) fields.summary_capped = true;
  return fields;
}

const BLOCKED_TEXT_MAX = 2000;

/** `execution.blocked` → its read-only face, or undefined (absent or without
 *  a kind and reason). Pure. */
export function sanitizeBlocked(value: unknown): PolyforgeBlocked | undefined {
  if (!value || typeof value !== "object") return undefined;
  const b = value as { kind?: unknown; step?: unknown; reason?: unknown; required_action?: unknown };
  const kind = toTrimmedString(b.kind);
  const reason = maskPolyforgeCredentials(toTrimmedString(b.reason)).slice(0, BLOCKED_TEXT_MAX);
  if (!kind || !reason) return undefined;
  const step = toOptionalTrimmedString(b.step);
  const action = maskPolyforgeCredentials(toTrimmedString(b.required_action)).slice(0, BLOCKED_TEXT_MAX);
  return { kind, ...(step ? { step } : {}), reason, ...(action ? { requiredAction: action } : {}) };
}

/**
 * Machine watch → response snapshot, or null when the ref is unknown to the
 * daemon (`known:false` shape) or the machine shape is unusable. Null keeps
 * the client's last snapshot (selection persistence, daemon blips).
 */
export function sanitizeWatch(machine: unknown): PolyforgeWatchStatus | null {
  if (!machine || typeof machine !== "object") return null;
  const m = machine as {
    known?: unknown;
    work_item?: unknown;
    slug?: unknown;
    job?: unknown;
    execution?: unknown;
    events?: unknown;
    progress?: unknown;
    summary?: unknown;
    summary_age_sec?: unknown;
    summary_error?: unknown;
    summary_capped?: unknown;
  };
  if (m.known !== true) return null;
  const execution = m.execution;
  if (!execution || typeof execution !== "object") return null;
  const ex = execution as {
    workflow_status?: unknown;
    current_step?: unknown;
    updated_at?: unknown;
    steps?: unknown;
    declared_steps?: unknown;
    blocked?: unknown;
  };
  if (!ex.steps || typeof ex.steps !== "object") return null;
  // `work_item` is the daemon's wire field (internal/common/api/machines_face.go);
  // a `wi`-only document is not a watch snapshot.
  const wi = toTrimmedString(m.work_item);
  const slug = toTrimmedString(m.slug);
  if (!wi || !slug) return null;
  return {
    wi,
    slug,
    workflowStatus: toTrimmedString(ex.workflow_status),
    currentStep: toOptionalTrimmedString(ex.current_step),
    updatedAt: toOptionalTrimmedString(ex.updated_at),
    steps: orderPolyforgeSteps(ex.steps as Record<string, { status?: unknown; started_at?: unknown }>),
    declared_steps: sanitizeDeclaredSteps(ex.declared_steps),
    events: tailPolyforgeEvents(Array.isArray(m.events) ? m.events : [], EVENT_TAIL),
    progress: sanitizeProgress(m.progress),
    origin: sanitizeOrigin(m.job),
    ...(sanitizeBlocked(ex.blocked) ? { blocked: sanitizeBlocked(ex.blocked) } : {}),
    ...sanitizeSummaryFields(m),
    ...sanitizePendingQuestion(execution),
  };
}

/**
 * `execution.pending_question` (workflow contract v2: the current
 * dispatch's open `pf step ask`, whose id and `asked_at` are the ask's stamp) →
 * `pendingQuestion` with the id, step, the question verbatim (never trimmed
 * or summarized, the relaxation) and `askedAt`. Absent
 * without an id and a question. (`execution.blocked` is
 * `sanitizeBlocked`'s.) Pure.
 */
function sanitizePendingQuestion(execution: object): { pendingQuestion?: PolyforgePendingQuestion } {
  const ex = execution as { pending_question?: unknown };
  const out: { pendingQuestion?: PolyforgePendingQuestion } = {};
  const pending = ex.pending_question;
  if (pending && typeof pending === "object") {
    const q = pending as { id?: unknown; step?: unknown; question?: unknown; asked_at?: unknown };
    const id = isPolyforgeQuestionId(q.id) ? q.id : undefined;
    const question = typeof q.question === "string" && q.question.trim() ? q.question : undefined;
    if (id && question) {
      const step = toOptionalTrimmedString(q.step);
      const askedAt = toOptionalTrimmedString(q.asked_at);
      out.pendingQuestion = { id, ...(step ? { step } : {}), question, ...(askedAt ? { askedAt } : {}) };
    }
  }
  return out;
}

// ---- the owner's messages and the no-agent menu's actions ----

function profileArgv(profile: string | undefined): string[] {
  if (profile === undefined) return [];
  if (!isPolyforgeProfileName(profile)) throw new PolyforgeError("profile must match [a-z0-9][a-z0-9-]{0,31}");
  return ["--profile", profile];
}

/** A question id as the snapshot's pending question carries it. */
const QUESTION_ID_PATTERN = /^[A-Za-z0-9._:#-]{1,80}$/;
export function isPolyforgeQuestionId(value: unknown): value is string {
  return typeof value === "string" && QUESTION_ID_PATTERN.test(value);
}

/** The relay's acknowledgement — or a parked work item's wake
 *  (`disposition: "wake"`, workflow contract 4) — as the answer route
 *  passes it to the job card (the client type is
 *  `lib/polyforge-conversation-pure.ts`'s PolyforgeOwnerMessageAck). */
export interface PolyforgeOwnerMessageAckWire {
  kind: "reply" | "message";
  at?: string;
  askAt?: string;
  step?: string;
  spawnN?: number;
  delivered?: boolean;
  disposition?: string;
  note?: string;
}

/** `pf answer` waits up to 3 s for the delivery proof after the daemon's
 *  own acknowledgement bound (the relay's 15 s + 20 s); a parked work
 *  item's wake (workflow contract 4) opens the aihub door and then claims
 *  it — the capsule clone and the agent start inside the request, as a
 *  `pf resume` — so the spawn gets pf's own bound on a daemon request
 *  (its client's 120 s). */
export const OWNER_MESSAGE_TIMEOUT_MS = 120_000;

/** Cap on a refusal of the owner's message the route passes on: the
 *  daemon's own words (a WAKE_CANNOT_START names the park's blocked reason,
 *  which can carry a model's error), masked, bounded. */
const OWNER_REFUSAL_MAX = 2000;

const REFUSAL_REASON_PATTERN = /^[A-Z][A-Z0-9_]{0,63}$/;

/**
 * pf's refusal of an owner message (`--json` mode prints the typed error
 * envelope `{"code","detail","extra":{"reason"}}` on stderr instead of its
 * human line, after any context line pf wrote first — "the door is open,
 * but the wake did not complete:") → the text the card shows (the context
 * lines, then `<code>: <detail>`), masked and bounded, and the envelope's
 * reason when it is a token (WAKE_CANNOT_START: the park's agent cannot
 * start, nothing was recorded). Null when stderr holds no envelope. Pure.
 */
export function parseOwnerMessageRefusal(stderr: string): { text: string; reason?: string } | null {
  const lines = stderr.split("\n").map((line) => line.trim()).filter(Boolean);
  for (let index = lines.length - 1; index >= 0; index -= 1) {
    if (!lines[index].startsWith("{")) continue;
    let envelope: unknown;
    try {
      envelope = JSON.parse(lines[index]);
    } catch {
      continue;
    }
    if (!envelope || typeof envelope !== "object" || Array.isArray(envelope)) continue;
    const e = envelope as { code?: unknown; detail?: unknown; extra?: unknown };
    if (typeof e.code !== "string" || !e.code) continue;
    const detail = typeof e.detail === "string" && e.detail.trim() ? e.detail.trim() : "";
    const extra = e.extra && typeof e.extra === "object" ? (e.extra as { reason?: unknown }) : undefined;
    const reason = typeof extra?.reason === "string" && REFUSAL_REASON_PATTERN.test(extra.reason) ? extra.reason : undefined;
    const text = [...lines.slice(0, index), detail ? `${e.code}: ${detail}` : e.code].join("\n");
    return { text: maskPolyforgeCredentials(text).slice(0, OWNER_REFUSAL_MAX), ...(reason ? { reason } : {}) };
  }
  return null;
}

/** The owner's message to the work item's current step agent (workflow
 *  contract v2 §5.2, change 14): `pf [--profile p] --json answer <wi>
 *  --text=<words>`, no question id — the daemon delivers the words to the
 *  step that waits on its `pf step ask` (a reply) or runs (an interjection). The
 *  words go verbatim. A parked work item (blocked or paused) is woken by
 *  the message (workflow contract 4, design #483 §7): pf opens the park's
 *  door with the caller's key, the daemon records the message and wakes
 *  the step. Answers the daemon's acknowledgement parsed from pf's
 *  `--json` output: the relay's `{status, at, kind, ask_at?, step, spawn_n,
 *  delivered, disposition?, note?}` (`disposition: "wake"` for a wake).
 *  Throws PolyforgeError on a refusal: pf's typed envelope as
 *  `<code>: <detail>` with its reason (parseOwnerMessageRefusal), else pf's
 *  bounded stderr; a spawn pf did not finish within the bound says the
 *  message may be recorded. */
export async function sendPolyforgeOwnerMessage(wi: string, text: string, profile?: string, timeoutMs = OWNER_MESSAGE_TIMEOUT_MS): Promise<PolyforgeOwnerMessageAckWire> {
  if (!isPolyforgeWorkItemId(wi)) throw new PolyforgeError("wi must be a work item id (wi_…)");
  if (typeof text !== "string" || !text.trim()) throw new PolyforgeError("a message needs text");
  let stdout: string;
  try {
    // `=` form: words that start with `-` stay the flag's value.
    ({ stdout } = await execFileAsync(polyforgePfBin(), [
      ...profileArgv(profile), "--json", "answer", wi, `--text=${text}`,
    ], { timeout: timeoutMs }));
  } catch (error) {
    if (error && typeof error === "object" && (error as { killed?: unknown }).killed === true) {
      // a wake's claim goes on in the daemon without its caller
      throw new PolyforgeError(`pf did not answer within ${Math.round(timeoutMs / 1000)} s: the message may still be recorded — check the flow before you send it again`);
    }
    const refusal = error instanceof Error ? parseOwnerMessageRefusal(String((error as { stderr?: unknown }).stderr ?? "")) : null;
    if (refusal) throw new PolyforgeError(refusal.text, refusal.reason);
    throw PolyforgeError.fromSpawnError(error);
  }
  return parseOwnerMessageAckWire(stdout);
}

/** pf's `--json answer` output → the route's ack: the relay's (`kind`
 *  reply / message). An unreadable or unknown body is reported as an
 *  undelivered message (the message went out — pf exited 0 — only the
 *  proof is missing). */
export function parseOwnerMessageAckWire(stdout: string): PolyforgeOwnerMessageAckWire {
  let raw: unknown;
  try {
    raw = JSON.parse(stdout);
  } catch {
    return { kind: "message", delivered: false };
  }
  if (!raw || typeof raw !== "object") return { kind: "message", delivered: false };
  const a = raw as Record<string, unknown>;
  const text = (key: string) => toOptionalTrimmedString(a[key]);
  if (a.kind !== "reply" && a.kind !== "message") return { kind: "message", delivered: false };
  const spawnN = typeof a.spawn_n === "number" && Number.isSafeInteger(a.spawn_n) ? a.spawn_n : undefined;
  return {
    kind: a.kind,
    ...(text("at") ? { at: text("at") } : {}),
    ...(text("ask_at") ? { askAt: text("ask_at") } : {}),
    ...(text("step") ? { step: text("step") } : {}),
    ...(spawnN !== undefined ? { spawnN } : {}),
    delivered: a.delivered === true,
    ...(text("disposition") ? { disposition: text("disposition") } : {}),
    ...(text("note") ? { note: maskPolyforgeCredentials(text("note")!).slice(0, STDERR_CAP) } : {}),
  };
}
