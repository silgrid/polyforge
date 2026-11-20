"use client";

import { useEffect, useRef, useState } from "react";
import { nextSubagentRunsRows } from "@/lib/background-tasks-client";
import type { PiSubagentRunSummary } from "@/lib/subagent-runs-view";

/**
 * This pi session's RUNNING built-in subagent runs (#173): polled from the
 * parent-scoped route — the runs have no SSE events of their own, and a
 * queued run has no session file the session list could show. The poll lives
 * only while a session id is given and the page is visible; a session switch
 * clears the rows and invalidates the previous session's in-flight answer,
 * which the pure machine drops (lib/background-tasks-client.ts).
 */
export function useSubagentRuns(sessionId: string | null): { runs: PiSubagentRunSummary[] } {
  const [runs, setRuns] = useState<PiSubagentRunSummary[]>([]);
  const seqRef = useRef(0);
  useEffect(() => {
    if (!sessionId) return undefined;
    setRuns([]);
    let inFlight = false;
    const read = async () => {
      // A hidden page needs no rows; the interval keeps running, so the
      // list refreshes within one tick of the page becoming visible again.
      if (inFlight || (typeof document !== "undefined" && document.hidden)) return;
      inFlight = true;
      const seq = ++seqRef.current;
      try {
        const response = await fetch(`/api/agent/${encodeURIComponent(sessionId)}/subagent-runs`);
        const body = await response.json().catch(() => null);
        setRuns((prev) => nextSubagentRunsRows(prev, seqRef.current, { seq, status: response.status, body }));
      } catch {
        // A failed poll keeps the last rows; the next one retries.
      } finally {
        inFlight = false;
      }
    };
    void read();
    const timer = setInterval(() => void read(), 2_000);
    return () => {
      clearInterval(timer);
      // The switch invalidates the in-flight answer: the machine drops it.
      seqRef.current += 1;
    };
  }, [sessionId]);
  return { runs };
}
