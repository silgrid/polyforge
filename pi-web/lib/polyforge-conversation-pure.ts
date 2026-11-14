import type { PolyforgeBlocked, PolyforgeEvent, PolyforgeJob, PolyforgeOpsStatus, PolyforgePendingQuestion, PolyforgeStep, PolyforgeWatchStatus } from "./polyforge-status";
import type { PolyforgeDecision } from "./polyforge-decisions-pure";
import { TERMINAL_BLOCKED_KINDS } from "./polyforge-decisions-pure";

/**
 * Client-safe vocabulary of the job card's conversation (workflow contract
 * v2, design §5.2, §5.4; workflow contract 4, design #483 §7):
 * which jobs wait for the owner, the question the selected one waits on,
 * whether the card's one input box takes a message right now — a step agent
 * runs or waits, or the work item is parked and the message wakes it — else
 * why it is closed and, when no agent can start, the copyable `pf resume` /
 * `pf cancel` commands; and the owner's own messages echoed into the flow
 * until the daemon's record shows them. No I/O here.
 */

/** The jobs of a status snapshot waiting for the owner, in list order: the
 *  status face says `needs_input` for a step parked on its `pf step ask` (the
 *  daemon's `execFaces` rule). */
export function needsInputJobs(status: PolyforgeOpsStatus | null): PolyforgeJob[] {
  return status ? status.jobs.filter((job) => job.status === "needs_input") : [];
}

/** The question the selected job waits on: the watch snapshot's
 *  `pendingQuestion` (the daemon's own truth — the current dispatch's open
 *  `pf step ask`, whose id is its stamp). Null when the snapshot names none. */
export function currentPendingQuestion(watch: PolyforgeWatchStatus | null): PolyforgePendingQuestion | null {
  return watch?.pendingQuestion ?? null;
}

/** Why the input box is closed: nothing a message could reach — no agent
 *  yet, no agent any more, the daemon unreachable — or a park whose agent
 *  cannot start (design #483 §7: no message wakes it; the card shows why and
 *  the copyable commands). */
export type PolyforgeConversationClosed =
  | "finished"   // the work item ended (completed / failed / cancelled / wrapped)
  | "queued"     // not dispatched yet: no agent exists
  | "claiming"   // the daemon is claiming it (cloning the capsule): no agent yet
  | "recovering" // the daemon is recovering it after a restart: the step comes back on its own
  | "sealing"    // the run is being closed and saved
  | "candidates" // cannot start: the parked step ran out of model candidates — fix the configuration, then resume
  | "runtime"    // cannot start: a block the runtime made with no workflow park (the capsule clone failed at the claim, I3; a wrap whose push failed) — resume retries
  | "contract"   // cannot start: the contract-mismatch park — cancel and re-file
  | "offline"    // no daemon status: the machine or the daemon is not reachable
  | "unknown";   // the daemon holds no job and the snapshot says nothing final

/** The parks a message wakes (design #483 §7, the daemon's park names):
 *  `blocked` — the step handed off blocked, a supervision rule parked it
 *  (timeouts, exits without a hand-off), its hub writes failed — and
 *  `paused` (the owner's pause). */
export type PolyforgePark = "blocked" | "paused";

/** The owner's commands a park no message wakes leaves (design #483 §7):
 *  `pf resume` and `pf cancel`, shown copyable. */
export type PolyforgeOwnerCommand = "resume" | "cancel";

export type PolyforgeConversationState =
  /** A step agent runs, or waits on its `pf step ask` (`awaiting`): the message goes to it. */
  | { input: "open"; awaiting: boolean; park?: undefined }
  /** Parked: the message wakes the step the park belongs to (the daemon
   *  decides which and how; it never reads the words). */
  | { input: "open"; awaiting: false; park: PolyforgePark }
  | { input: "closed"; reason: PolyforgeConversationClosed; commands: readonly PolyforgeOwnerCommand[] };

/** The status face's terminal job states (`pf ops status` jobs[].status
 *  after the daemon sealed the run) and the hub's final statuses the watch
 *  snapshot reports once no job exists. */
const FINISHED_STATUSES = new Set(["completed", "failed", "cancelled", "canceled", "wrapped", "failed_gate", "done"]);

/** The runtime's transient job states (the status face's jobs[].status):
 *  no agent to talk to, no park, nothing to do but wait. */
const TRANSIENT_STATUSES = new Set(["claiming", "recovering", "sealing"]);

/**
 * Whether the parked step ran out of model candidates in its current run:
 * only the driver's park (`driver`, the kind `watchBlocked` gives a
 * supervision block) can be one, and only when the watch tail holds a
 * `candidates_exhausted` event of the parked step at or after that step's
 * current start (the daemon announces the exhaustion with the block whose
 * reason opens `no usable model:` — a park no message wakes). A feed entry
 * cannot prove it: it names no step (review r2 B1). No proof reads as a
 * park the message wakes: the daemon's WAKE_CANNOT_START refusal then says
 * so (the composer shows it with the commands). Pure.
 */
function candidatesExhausted(blocked: PolyforgeBlocked | undefined, currentStep: string | undefined, steps: readonly PolyforgeStep[], events: readonly PolyforgeEvent[]): boolean {
  if (blocked && blocked.kind !== "driver") return false;
  const step = blocked?.step ?? currentStep;
  if (!step) return false;
  const started = Date.parse(steps.find((s) => s.name === step)?.startedAt ?? "");
  if (!Number.isFinite(started)) return false;
  return events.some((e) => e.kind === "candidates_exhausted" && e.step === step && Date.parse(e.at) >= started);
}

const BOTH: readonly PolyforgeOwnerCommand[] = ["resume", "cancel"];
const CANCEL_ONLY: readonly PolyforgeOwnerCommand[] = ["cancel"];
const NONE: readonly PolyforgeOwnerCommand[] = [];

/**
 * Whether the card's input box takes a message now, and how (design §5.4;
 * workflow contract 4, design #483 §7 "owner 往停住的票发消息"):
 *
 * | state | input | shown |
 * |---|---|---|
 * | the job runs or waits (`running`, `needs_input`) | open | — |
 * | a park the message wakes: any workflow park but the two below (the step's own `blocked`, a supervision block, `write_failed`, a kind still to come) | open, `park: blocked` | the hint |
 * | the owner paused it (`paused`) | open, `park: paused` | the hint |
 * | the driver's park whose step ran out of model candidates in its current run, per the watch tail | closed `candidates` | why; `pf resume` · `pf cancel` |
 * | the `contract` park | closed `contract` | why; `pf cancel` |
 * | no live job, no workflow park, an open `blocked` feed entry (a block the runtime made: the capsule clone failed at the claim, I3; a run that completed but whose final push failed) | closed `runtime` | why; `pf resume` · `pf cancel` |
 * | queued (no dispatch yet); claiming, recovering, sealing (the runtime at work) | closed | why |
 * | the work item ended | closed `finished` | why |
 * | no status snapshot at all | closed `offline` | why (the restart reconciliation recovers) |
 *
 * The daemon is the authority on whether a park wakes: a park this table
 * reads as wakeable whose agent cannot start (no candidate list at all, a
 * failed composition, …) is refused with WAKE_CANNOT_START, and the
 * composer shows that refusal with the same commands.
 *
 * The watch snapshot's park decides a parked item; a feed entry decides only
 * where the snapshot names no park (a block the runtime made, a pause of a
 * dropped item). An open runtime `blocked` entry outranks a `completed`
 * workflow status: the final push failed after the workflow completed, and
 * only a resume saves the run (review r2 B2). `blocked` outranks the job
 * status (a blocked job's row may still read `running` for a moment). Pure.
 */
export function conversationState(input: {
  jobStatus: string | undefined;
  workflowStatus?: string;
  currentStep?: string;
  blocked?: PolyforgeBlocked;
  /** The watch snapshot's steps and event tail (the candidates proof). */
  steps?: readonly PolyforgeStep[];
  events?: readonly PolyforgeEvent[];
  /** The work item's decision-feed entries (itemDecisions). */
  entries?: readonly PolyforgeDecision[];
  /** Whether the status read answered at all (`opsStatus !== null`). */
  daemonPresent: boolean;
}): PolyforgeConversationState {
  const closed = (reason: PolyforgeConversationClosed, commands = NONE): PolyforgeConversationState => ({ input: "closed", reason, commands });
  if (!input.daemonPresent) return closed("offline");
  const open = (cls: string) => (input.entries ?? []).some((e) => e.kind === "work_item" && e.class === cls && !e.resolved && !e.dismissed);
  const blockedPark = (): PolyforgeConversationState => (candidatesExhausted(input.blocked, input.currentStep, input.steps ?? [], input.events ?? [])
    ? closed("candidates", BOTH)
    : { input: "open", awaiting: false, park: "blocked" });
  const pausedPark: PolyforgeConversationState = { input: "open", awaiting: false, park: "paused" };
  if (input.blocked) {
    if (TERMINAL_BLOCKED_KINDS.includes(input.blocked.kind)) return closed("contract", ["cancel"]);
    return blockedPark();
  }
  switch (input.jobStatus) {
    case "running": return { input: "open", awaiting: false };
    case "needs_input": return { input: "open", awaiting: true };
    case "paused": return pausedPark;
    case "queued": return closed("queued");
    case undefined: break;
    default:
      if (TRANSIENT_STATUSES.has(input.jobStatus)) return closed(input.jobStatus as PolyforgeConversationClosed);
      if (FINISHED_STATUSES.has(input.jobStatus)) return closed("finished");
      return closed("unknown");
  }
  // no live job: the watch snapshot's workflow status decides, else the feed
  if (input.workflowStatus === "blocked") return blockedPark();
  if (input.workflowStatus === "paused") return pausedPark;
  if (open("blocked")) return closed("runtime", BOTH);
  if (input.workflowStatus && FINISHED_STATUSES.has(input.workflowStatus)) return closed("finished");
  if (open("paused")) return pausedPark;
  return closed("unknown");
}

/** The answer route's typed refusal of a message to a park whose agent
 *  cannot start (the daemon's DAEMON-409 reason): nothing was recorded. */
export const WAKE_CANNOT_START = "WAKE_CANNOT_START";

/** The same refusal for a park whose lane snapshot targets another
 *  workflow contract (a `contract` park, or a park of the previous contract
 *  made before the upgrade): a resume parks it again. */
export const WAKE_CANNOT_START_CONTRACT = "WAKE_CANNOT_START_CONTRACT";

/** The commands the composer offers beside a cannot-start refusal of a
 *  message (its typed reason): `pf cancel` alone for another contract's
 *  snapshot, `pf resume` and `pf cancel` otherwise; null for any other
 *  refusal (it shows as an error). Pure. */
export function cannotStartCommands(reason: string | undefined): readonly PolyforgeOwnerCommand[] | null {
  if (reason === WAKE_CANNOT_START_CONTRACT) return CANCEL_ONLY;
  if (reason === WAKE_CANNOT_START) return BOTH;
  return null;
}

/** The owner's message as the answer route acknowledges it (`pf --json
 *  answer`): the relay's ack (`kind` reply or message, the recorded `at`,
 *  delivered or the disposition the delivery took — a parked work item's
 *  wake included). */
export interface PolyforgeOwnerMessageAck {
  kind: "reply" | "message";
  /** The recorded line's stamp: the message's identity in the capsule
   *  record and the `owner_message` event (relay kinds only). */
  at?: string;
  askAt?: string;
  step?: string;
  spawnN?: number;
  delivered?: boolean;
  /** `started` / `queued` (a live process took it), `resume` (the step's
   *  next dispatch carries it) or `wake` (the message woke the parked work
   *  item, workflow contract 4); absent once delivered. */
  disposition?: string;
  /** Why a live process did not take the message, verbatim from the daemon. */
  note?: string;
}

/** The owner's own message, echoed into the flow the moment the route
 *  acknowledges it (the record exists; the event log's `owner_message` line
 *  follows on the next progress read) with how far the delivery got. */
export interface PolyforgeOwnerEcho {
  /** The ack's `at` (an ack without one: the client's own stamp). */
  at: string;
  text: string;
  ack: PolyforgeOwnerMessageAck;
}

/** The echoes the flow still shows: those whose `at` (the relay ack's —
 *  the record line's own stamp) no `owner` line of the snapshot names as
 *  its `msgAt` yet. Never the line's `at`: the event log stamps its
 *  envelope later, after the record's fsync (review r1). An echo whose ack
 *  named no `at` has no line to wait for and stays until the selection
 *  changes. */
export function pendingOwnerEchoes(echoes: readonly PolyforgeOwnerEcho[], lines: readonly { kind: string; msgAt?: string }[]): PolyforgeOwnerEcho[] {
  const shown = new Set<string>();
  for (const line of lines) if (line.kind === "owner" && line.msgAt) shown.add(line.msgAt);
  return echoes.filter((echo) => !shown.has(echo.at));
}

/** The answer route's `ack` body → the client ack, or null when malformed. */
export function parseOwnerMessageAck(value: unknown): PolyforgeOwnerMessageAck | null {
  if (!value || typeof value !== "object") return null;
  const v = value as Record<string, unknown>;
  const text = (key: string) => (typeof v[key] === "string" && (v[key] as string).trim() ? (v[key] as string) : undefined);
  if (v.kind !== "reply" && v.kind !== "message") return null;
  const spawnN = typeof v.spawnN === "number" && Number.isSafeInteger(v.spawnN) ? v.spawnN : undefined;
  return {
    kind: v.kind,
    ...(text("at") ? { at: text("at") } : {}),
    ...(text("askAt") ? { askAt: text("askAt") } : {}),
    ...(text("step") ? { step: text("step") } : {}),
    ...(spawnN !== undefined ? { spawnN } : {}),
    ...(typeof v.delivered === "boolean" ? { delivered: v.delivered } : {}),
    ...(text("disposition") ? { disposition: text("disposition") } : {}),
    ...(text("note") ? { note: text("note") } : {}),
  };
}

/** Upper bound of one owner message the route accepts: the daemon's
 *  `capsule.MaxOwnerMessageBytes` is 64 KiB; the client bound is in
 *  characters and stays well inside it. */
export const OWNER_MESSAGE_TEXT_MAX = 16_000;
