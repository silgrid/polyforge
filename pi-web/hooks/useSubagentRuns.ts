"use client";

import { useEffect, useState } from "react";
import type { PiSubagentRunSummary } from "@/lib/subagent-runs-view";

/**
 * This pi session's RUNNING built-in subagent runs (#173): polled from the
 * parent-scoped route — the runs have no SSE events of their own, and a
 * queued run has no session file the session list could show. The poll lives
 * only while a session id is given; a session switch drops the previous
 * session's rows before the first read of the next.
 */
export function useSubagentRuns(sessionId: string | null): { runs: PiSubagentRunSummary[] } {
  const [runs, setRuns] = useState<PiSubagentRunSummary[]>([]);
  useEffect(() => {
    if (!sessionId) return undefined;
    setRuns([]);
    let inFlight = false;
    const read = async () => {
      if (inFlight) return;
      inFlight = true;
      try {
        const response = await fetch(`/api/agent/${encodeURIComponent(sessionId)}/subagent-runs`);
        if (!response.ok) return;
        const body = await response.json() as { runs?: PiSubagentRunSummary[] };
        if (Array.isArray(body.runs)) setRuns(body.runs);
      } catch {
        // The next poll retries; a session's list is never an error surface.
      } finally {
        inFlight = false;
      }
    };
    void read();
    const timer = setInterval(() => void read(), 2_000);
    return () => {
      clearInterval(timer);
    };
  }, [sessionId]);
  return { runs };
}
