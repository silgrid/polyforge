/**
 * Client-safe vocabulary of pi-web's built-in background tasks
 * (polyforge#172, unit bg-runtime): the wire shape of a task snapshot, the
 * structured refusal code of an operation refused over unfinished background
 * work, the notification's custom message type, and the pure helpers that
 * assemble tool output and notification text. No node built-ins here: the
 * client bundle imports this module too.
 */

import type { BgTaskSnapshot } from "./types";

/** The per-task log cap pi-web enforces (64 MiB, AC2 / Q7 A). */
export const BG_LOG_MAX_BYTES = 64 * 1024 * 1024;

/** How many tasks one session may run at once (AC2). */
export const BG_MAX_CONCURRENT_PER_SESSION = 4;

/** `bg_output`'s whole answer, text and prompt lines together, stays under this many UTF-8 bytes (AC2). */
export const BG_OUTPUT_MAX_BYTES = 8192;

/** The custom message type of a task's completion notification (Q5 A). */
export const BG_TASK_NOTIFICATION_TYPE = "pi-web:bg-task-notification";

/**
 * The structured refusal code of a command refused because the session has
 * unfinished background work (AC3; #128's `claude_restart_refused` is the
 * shape this follows).
 */
export const PI_RESTART_REFUSED_CODE = "pi_restart_refused";

export type BgTaskStatus = "running" | "succeeded" | "failed" | "stopped" | "interrupted";

/** A task snapshot as the tools, the SSE events and the notification carry it. */
export interface BgTaskInfo {
  id: string;
  sessionId: string;
  command: string;
  cwd: string;
  status: BgTaskStatus;
  startedAt: number;
  endedAt: number | null;
  exitCode: number | null;
  signal: string | null;
  /** `log-limit` when the byte cap stopped the task, `stopped` when bg_stop did. */
  reason: "log-limit" | "stopped" | null;
  bytesWritten: number | null;
  logPath: string;
  /** Whether the completion notification was already delivered (AC1's once rule). */
  notified: boolean;
  /** The task's process group id while it ran. Recorded for display only: after a restart nobody signals it (AC4). */
  pgid?: number | null;
}

/** A `bg_output` read: the excerpt, its byte range in the full log, and the log's size. */
export interface BgTaskOutput {
  text: string;
  startByte: number;
  endByte: number;
  totalBytes: number;
  logPath: string;
  truncated: boolean;
}

/**
 * The SSE event payload of a built-in task, in the old bridge's `BgTaskSnapshot`
 * contract: the dock keeps opening on `background_task_terminal` unchanged and
 * `bg-ui` later swaps only the HTTP interface, never the event names or shape.
 */
export function bgTaskClientSnapshot(info: BgTaskInfo): BgTaskSnapshot {
  return {
    id: info.id,
    name: info.command.length > 0 ? info.command.slice(0, 60) : undefined,
    command: info.command,
    description: "pi-web built-in background task",
    status: info.status === "running"
      ? "running"
      : info.status === "failed"
        ? "failed"
        : info.status === "succeeded" ? "completed" : "killed",
    outputPath: info.logPath,
    cwd: info.cwd,
    startTime: info.startedAt,
    ...(info.endedAt !== null ? { endTime: info.endedAt } : {}),
    ...(info.exitCode !== null ? { exitCode: info.exitCode } : { exitCode: null }),
    ...(info.signal !== null ? { signal: info.signal } : { signal: null }),
    bytesWritten: info.bytesWritten ?? 0,
    isAgent: true,
    ...(info.reason === "log-limit" ? { error: `log reached the ${BG_LOG_MAX_BYTES / (1024 * 1024)} MiB cap` } : {}),
    notified: info.notified,
    notifyOnCompletion: true,
    triggerOnCompletion: true,
  };
}

const utf8Encoder = new TextEncoder();
const utf8Decoder = new TextDecoder("utf-8", { fatal: false });

/** Cuts `text` to at most `maxBytes` UTF-8 bytes without splitting a character. */
export function utf8Truncate(text: string, maxBytes: number): string {
  if (!Number.isFinite(maxBytes) || maxBytes <= 0) return "";
  const bytes = utf8Encoder.encode(text);
  if (bytes.length <= maxBytes) return text;
  // A cut mid-character decodes to one trailing replacement character: drop it.
  let cut = utf8Decoder.decode(bytes.subarray(0, maxBytes));
  if (cut.endsWith("\uFFFD")) cut = cut.slice(0, -1);
  return cut;
}

/**
 * Drops up to `bytes` leading UTF-8 bytes of `text` without splitting a
 * character, keeping the tail; returns how many bytes actually went.
 */
export function utf8SkipLeading(text: string, bytes: number): { text: string; skipped: number } {
  if (!Number.isFinite(bytes) || bytes <= 0 || text.length === 0) return { text, skipped: 0 };
  const encoded = utf8Encoder.encode(text);
  if (encoded.length <= bytes) return { text: "", skipped: encoded.length };
  let skip = bytes;
  // A skip landing mid-character moves up to the character's start.
  while (skip > 0 && (encoded[skip] & 0xc0) === 0x80) skip -= 1;
  const kept = utf8Decoder.decode(encoded.subarray(skip));
  // A cut mid-character decodes to one leading replacement character: drop it.
  const clean = kept.startsWith("\uFFFD") ? kept.slice(1) : kept;
  return { text: clean, skipped: skip + (kept.length - clean.length) };
}

/**
 * The one-line completion notification a session receives (AC1: exit code or
 * signal, duration, log path). Derived from the record alone, never from the
 * implementation's output.
 */
export function bgTaskNotificationText(info: BgTaskInfo): string {
  const id = info.id.slice(0, 8);
  const head = info.status === "stopped"
    ? `Background task \`${id}\` was stopped`
    : info.status === "failed"
      ? info.reason === "log-limit"
        ? `Background task \`${id}\` failed: its log reached the ${BG_LOG_MAX_BYTES / (1024 * 1024)} MiB cap and the task was stopped`
        : `Background task \`${id}\` failed`
      : info.status === "interrupted"
        ? `Background task \`${id}\` was interrupted by a restart`
        : `Background task \`${id}\` succeeded`;
  const outcome = info.exitCode !== null
    ? `exit code ${info.exitCode}`
    : info.signal !== null
      ? `signal ${info.signal}`
      : "";
  const duration = info.endedAt !== null
    ? `duration ${((info.endedAt - info.startedAt) / 1000).toFixed(1)}s`
    : "";
  const details = [
    `command: ${info.command}`,
    outcome,
    duration,
    `log: ${info.logPath}`,
  ].filter(Boolean).join(" · ");
  return `${head}\n${details}`;
}
