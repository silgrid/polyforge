"use client";

// A built-in background task's panel, and the session's finished-tasks
// history, opened from the running list above a pi session's composer into
// the floating slot (polyforge#173). Skeleton pending its unit's tests.

import type { BgTaskInfo, BgTaskOutput } from "@/lib/bg-protocol";

export interface PiBackgroundTaskPanelProps {
  task: BgTaskInfo;
  /** Stops the task (the executor's idempotent stop); a rejection's message shows in the panel. */
  onStop: () => Promise<void>;
  /** Reads the task's output tail; a rejection's message shows in the panel. */
  onReadOutput: () => Promise<BgTaskOutput>;
  onClose: () => void;
  now?: number;
}

export interface PiBackgroundTaskHistoryProps {
  /** The session's settled tasks (its history entry's rows). */
  tasks: BgTaskInfo[];
  onOpenTask: (taskId: string) => void;
  onClose: () => void;
  now?: number;
}

export function PiBackgroundTaskPanel(_props: PiBackgroundTaskPanelProps): null {
  return null;
}

export function PiBackgroundTaskHistory(_props: PiBackgroundTaskHistoryProps): null {
  return null;
}
