"use client";

// The running list above a pi session's composer (polyforge#173, AC1): one
// compact button per running built-in subagent and per running background
// task of THIS session, merged oldest first — the owner's ruling: no
// grouping, the type told apart by its badge, a queued run named — plus the
// 「已结束」 entry to the session's settled tasks. Nothing renders while
// nothing runs and nothing settled. Skeleton pending its unit's tests.

import type { BgTaskInfo } from "@/lib/bg-protocol";
import type { PiSubagentRunSummary } from "@/lib/subagent-runs-view";

export function PiRunningList(_props: {
  subagents: PiSubagentRunSummary[];
  tasks: BgTaskInfo[];
  onOpenSubagent: (sessionId: string, label: string) => void;
  onOpenTask: (taskId: string) => void;
  onOpenHistory: () => void;
  now?: number;
}): null {
  return null;
}
