/**
 * Client-safe view model of one pi subagent run's progress panel
 * (polyforge#255): the status/elapsed projection the owner settled (Q2, Q8),
 * the transcript merge the snapshot poll uses (Q5) and the launch-position
 * lookup Jump uses (Q6). No node built-ins: the client bundle imports this
 * module.
 */
import type { AgentMessage } from "./types";

export type PiSubagentStatus =
  | "starting"
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "aborted"
  | "interrupted"
  | "unknown";

export interface PiSubagentRunView {
  sessionId: string;
  parentSessionId: string;
  parentToolCallId?: string;
  profile: string;
  description: string;
  task?: string;
  status: PiSubagentStatus;
  createdAt: string;
  completedAt?: string;
  result?: string;
  error?: string;
}

/** One transcript entry: the entry id the session reader gives the message. */
export interface PiSubagentEntry {
  id: string;
  message: AgentMessage;
}

export type PiSubagentPanelPhase = "loading" | "ready" | "missing" | "error";

const STATUSES: ReadonlySet<string> = new Set(["starting", "queued", "running", "completed", "failed", "aborted", "interrupted"]);
const ACTIVE: ReadonlySet<string> = new Set(["starting", "queued", "running"]);

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function text(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

/** The run's status word as the panel's label key. */
export function piSubagentStatusLabelKey(status: PiSubagentStatus): string {
  return `piSubagent.status.${STATUSES.has(status) ? status : "unknown"}`;
}

/** Whether the run has not settled yet (running, queued or starting). */
export function piSubagentIsActive(status: PiSubagentStatus): boolean {
  return ACTIVE.has(status);
}

/** Only a running run takes a steer: `steer_subagent` refuses every other state. */
export function piSubagentCanSteer(status: PiSubagentStatus): boolean {
  return status === "running";
}

/**
 * Why the input box is disabled, as its own label key, or null while the run
 * takes a message. The read's phase wins over the run's status: an unread,
 * missing or failed read cannot prove the run is running, so it never enables
 * sending (Q2).
 */
export function piSubagentInputReason(status: PiSubagentStatus, phase: PiSubagentPanelPhase): string | null {
  if (phase === "loading") return "piSubagent.input.loading";
  if (phase === "missing") return "piSubagent.input.missing";
  if (phase === "error") return "piSubagent.input.error";
  if (status === "running") return null;
  return `piSubagent.input.${status}`;
}

/**
 * The run's elapsed time in ms: from its first creation (`createdAt` — a
 * resumed run keeps it, so this is never a claim about this resume) while it
 * is active; once settled `completedAt − createdAt`, clamped at 0. A terminal
 * run without `completedAt` (an interrupted one) has no duration: it must not
 * keep growing (Q8).
 */
export function piSubagentElapsedMs(
  run: Pick<PiSubagentRunView, "status" | "createdAt" | "completedAt">,
  now: number,
): number | undefined {
  const created = Date.parse(run.createdAt);
  if (!Number.isFinite(created)) return undefined;
  if (piSubagentIsActive(run.status)) return Math.max(0, now - created);
  if (!run.completedAt) return undefined;
  const completed = Date.parse(run.completedAt);
  return Number.isFinite(completed) ? Math.max(0, completed - created) : undefined;
}

/**
 * Merge a fresh snapshot into the loaded transcript by entry id. `append` is
 * the poll (new entries only, already-loaded history kept), `prepend` one
 * earlier page (its overlap appears once, at the front) and `replace` the
 * first load. Entry order is the session's; a snapshot never reorders what is
 * already shown.
 */
export function mergePiSubagentEntries(
  existing: readonly PiSubagentEntry[],
  incoming: readonly PiSubagentEntry[],
  mode: "append" | "prepend" | "replace" = "append",
): PiSubagentEntry[] {
  if (mode === "replace") return dedupe(incoming);
  if (mode === "prepend") {
    const seen = new Set(incoming.map((entry) => entry.id));
    return dedupe([...incoming, ...existing.filter((entry) => !seen.has(entry.id))]);
  }
  const seen = new Set(existing.map((entry) => entry.id));
  return [...existing, ...dedupe(incoming.filter((entry) => !seen.has(entry.id)))];
}

function dedupe(entries: readonly PiSubagentEntry[]): PiSubagentEntry[] {
  const seen = new Set<string>();
  const out: PiSubagentEntry[] = [];
  for (const entry of entries) {
    if (seen.has(entry.id)) continue;
    seen.add(entry.id);
    out.push(entry);
  }
  return out;
}

/**
 * The entry id of the Agent tool call that launched a run, so Jump can scroll
 * to it: the first message block whose `toolCallId` matches. Null when this
 * transcript holds no such call (a sibling, an ancestor or a deleted branch).
 */
export function findPiSubagentLaunchEntryId(
  toolCallId: string,
  messages: readonly AgentMessage[],
  entryIds: readonly string[],
): string | null {
  for (let i = 0; i < messages.length; i += 1) {
    const content = (messages[i] as { content?: unknown }).content;
    if (!Array.isArray(content)) continue;
    for (const block of content) {
      const item = record(block);
      if (item?.type === "toolCall" && item.toolCallId === toolCallId && typeof entryIds[i] === "string") {
        return entryIds[i];
      }
    }
  }
  return null;
}

/** A `GET /api/subagents/[id]` answer's run, or null when it carries none. */
export function piSubagentRunFromJson(value: unknown): PiSubagentRunView | null {
  const body = record(value);
  const run = record(body?.run);
  if (!run) return null;
  const sessionId = text(run.sessionId);
  const parentSessionId = text(run.parentSessionId);
  const createdAt = text(run.createdAt);
  if (!sessionId || !parentSessionId || !createdAt) return null;
  const status = typeof run.status === "string" && STATUSES.has(run.status) ? run.status as PiSubagentStatus : "unknown";
  return {
    sessionId,
    parentSessionId,
    ...(text(run.parentToolCallId) ? { parentToolCallId: text(run.parentToolCallId) } : {}),
    profile: text(run.profile) ?? "",
    description: text(run.description) ?? "",
    ...(text(run.task) ? { task: text(run.task) } : {}),
    status,
    createdAt,
    ...(text(run.completedAt) ? { completedAt: text(run.completedAt) } : {}),
    ...(text(run.result) ? { result: text(run.result) } : {}),
    ...(text(run.error) ? { error: text(run.error) } : {}),
  };
}

export interface PiSubagentTranscript {
  entries: PiSubagentEntry[];
  hasEarlier: boolean;
  oldestEntryId: string | null;
  cwd: string | null;
}

/** A `GET /api/sessions/[id]` (or its context page) answer's transcript page. */
export function piSubagentTranscriptFromJson(value: unknown): PiSubagentTranscript | null {
  const body = record(value);
  const context = record(body?.context);
  if (!context || !Array.isArray(context.messages) || !Array.isArray(context.entryIds)) return null;
  const entries: PiSubagentEntry[] = [];
  const messages = context.messages;
  const entryIds = context.entryIds;
  for (let i = 0; i < messages.length; i += 1) {
    const id = entryIds[i];
    if (typeof id !== "string") continue;
    const message = record(messages[i]);
    if (!message || typeof message.role !== "string") continue;
    entries.push({ id, message: messages[i] as AgentMessage });
  }
  const info = record(body?.info);
  return {
    entries,
    hasEarlier: context.hasMore === true,
    oldestEntryId: typeof context.oldestEntryId === "string" ? context.oldestEntryId : entries[0]?.id ?? null,
    cwd: typeof info?.cwd === "string" ? info.cwd : null,
  };
}

/** The steer request: the one existing subagents route, never a composer prompt. */
export function piSubagentSteerRequest(childSessionId: string, message: string): { url: string; init: RequestInit } {
  return {
    url: `/api/subagents/${encodeURIComponent(childSessionId)}`,
    init: {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action: "steer", message }),
    },
  };
}