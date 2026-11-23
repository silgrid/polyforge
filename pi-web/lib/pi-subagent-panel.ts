/**
 * Client-safe view model of one pi subagent run's progress panel
 * (polyforge#255): the status/elapsed projection, the transcript merge and
 * the launch-position lookup the panel and its controller share. No node
 * built-ins: the client bundle imports this module.
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

export interface PiSubagentEntry {
  id: string;
  message: AgentMessage;
}

export type PiSubagentPanelPhase = "loading" | "ready" | "missing" | "error";

export function piSubagentStatusLabelKey(_status: PiSubagentStatus): string {
  return "piSubagent.status.unknown";
}

export function piSubagentIsActive(_status: PiSubagentStatus): boolean {
  return false;
}

export function piSubagentCanSteer(_status: PiSubagentStatus): boolean {
  return false;
}

export function piSubagentInputReason(
  _status: PiSubagentStatus,
  _phase: PiSubagentPanelPhase,
): string | null {
  return "piSubagent.input.loading";
}

export function piSubagentElapsedMs(
  _run: Pick<PiSubagentRunView, "status" | "createdAt" | "completedAt">,
  _now: number,
): number | undefined {
  return undefined;
}

export function mergePiSubagentEntries(
  _existing: readonly PiSubagentEntry[],
  _incoming: readonly PiSubagentEntry[],
  _mode: "append" | "prepend" | "replace" = "append",
): PiSubagentEntry[] {
  return [];
}

export function findPiSubagentLaunchEntryId(
  _toolCallId: string,
  _messages: readonly AgentMessage[],
  _entryIds: readonly string[],
): string | null {
  return null;
}

export function piSubagentRunFromJson(_value: unknown): PiSubagentRunView | null {
  return null;
}
