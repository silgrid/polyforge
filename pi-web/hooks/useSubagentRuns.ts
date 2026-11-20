"use client";

import { useEffect, useState } from "react";
import type { PiSubagentRunSummary } from "@/lib/subagent-runs-view";

/**
 * This pi session's RUNNING built-in subagent runs (#173): polled from the
 * parent-scoped route — the runs have no SSE events of their own, and a
 * queued run has no session file the session list could show. Skeleton
 * pending its unit's tests.
 */
export function useSubagentRuns(_sessionId: string | null): { runs: PiSubagentRunSummary[] } {
  return { runs: [] };
}
