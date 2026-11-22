/**
 * Client-safe pure logic of the built-in background tasks' client state
 * (polyforge#173): the answer parser, the sequenced state machines both
 * hooks run on, and the SSE change-signal test. The hooks wire these; the
 * tests exercise the machines, so a session switch dropping the previous
 * session's late answer is pinned as behaviour, not as source text. No node
 * built-ins: the client bundle imports this module.
 */

import type { BackgroundTasksClientEvent } from "./types";
import type { BgTaskInfo } from "./bg-protocol";
import type { PiSubagentRunSummary } from "./subagent-runs-view";

/** One in-flight read's answer: which request it answers, and what came back. */
export interface SequencedAnswer {
  /** The read's own sequence number, taken when it started. */
  seq: number;
  /** The HTTP status; 0 for a transport failure. */
  status: number;
  /** The parsed JSON body, or null when none arrived. */
  body: unknown;
}

/** The running list's state for one session (`useBackgroundTasks`). */
export type BackgroundTasksListState =
  | { kind: "loading" }
  | { kind: "ready"; tasks: BgTaskInfo[] }
  | { kind: "error"; error: string };

/** Parses a list answer's body into the registry's records; null when it holds no valid task array. */
export function parseTaskListBody(body: unknown): BgTaskInfo[] | null {
  if (typeof body !== "object" || body === null) return null;
  const tasks = (body as { tasks?: unknown }).tasks;
  if (!Array.isArray(tasks)) return null;
  const clean = tasks.filter((task): task is BgTaskInfo => typeof (task as { id?: unknown })?.id === "string");
  return clean.length === tasks.length ? clean : null;
}

/** A refusal's error text: the body's own `error` field, else the status. */
export function answerErrorText(status: number, body: unknown): string {
  if (typeof body === "object" && body !== null && typeof (body as { error?: unknown }).error === "string") {
    return (body as { error: string }).error;
  }
  return `HTTP ${status}`;
}

/**
 * The list state after one read's answer: an answer whose sequence the
 * session has already left behind (a switch, an unmount) changes nothing —
 * the previous session's late answer never lands in the new one's state
 * (AC1: 切换会话不串项); a refused or malformed answer surfaces as an error;
 * a valid one is the session's records.
 */
export function nextBackgroundTasksListState(
  prev: BackgroundTasksListState,
  currentSeq: number,
  answer: SequencedAnswer,
): BackgroundTasksListState {
  if (answer.seq !== currentSeq) return prev;
  const tasks = answer.status === 200 ? parseTaskListBody(answer.body) : null;
  if (!tasks) return { kind: "error", error: answerErrorText(answer.status, answer.body) };
  return { kind: "ready", tasks };
}

/**
 * The SSE events that mean the registry changed: both kinds are change
 * signals — the hook re-reads the HTTP interface, the richer truth (#172's
 * contract keeps the events in the old bridge's snapshot shape).
 */
export function isBackgroundTasksChangeSignal(event: BackgroundTasksClientEvent): boolean {
  return event.type === "background_task_terminal" || event.type === "background_tasks_update";
}

/** Parses the subagent-runs answer into the running list's rows; null when it holds no valid rows array. */
export function parseSubagentRunsBody(body: unknown): PiSubagentRunSummary[] | null {
  if (typeof body !== "object" || body === null) return null;
  const runs = (body as { runs?: unknown }).runs;
  if (!Array.isArray(runs)) return null;
  const valid = runs.every((row): row is PiSubagentRunSummary => {
    if (typeof row !== "object" || row === null) return false;
    const r = row as { sessionId?: unknown; status?: unknown; createdAt?: unknown };
    return typeof r.sessionId === "string"
      && (r.status === "running" || r.status === "queued")
      && typeof r.createdAt === "string";
  });
  return valid ? runs : null;
}

/**
 * The running subagent rows after one poll's answer: a stale answer (the
 * session has switched or unmounted) changes nothing — its rows stay out;
 * a failed or malformed poll keeps the last rows (a session's rows are never
 * an error surface).
 */
export function nextSubagentRunsRows(
  prev: PiSubagentRunSummary[],
  currentSeq: number,
  answer: SequencedAnswer,
): PiSubagentRunSummary[] {
  if (answer.seq !== currentSeq) return prev;
  const runs = answer.status === 200 ? parseSubagentRunsBody(answer.body) : null;
  return runs ?? prev;
}
