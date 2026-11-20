"use client";

import { useEffect, useRef, useState } from "react";
import { nextSubagentRunsRows } from "@/lib/background-tasks-client";
import type { PiSubagentRunSummary } from "@/lib/subagent-runs-view";

/**
 * This pi session's RUNNING built-in subagent runs (#173): polled from the
 * parent-scoped route — the runs have no SSE events of their own, and a
 * queued run has no session file the session list could show. A session
 * switch clears the rows and invalidates the previous session's in-flight
 * answer, which the pure machine drops (lib/background-tasks-client.ts).
 *
 * The tick reads only while it can learn something (G11,
 * e2e/polyforge-graph.mjs: the graph dock's click window admits no session
 * read, and a 2 s poll that reads while the session sits idle with nothing
 * live lands inside it): a run is dispatched by a reply of this session
 * (`sessionRunning`) or already recorded in the last answer's rows, so an
 * idle session with nothing live falls silent after the discovery reads —
 * the mount read, and one when the page becomes visible again (a hidden
 * page's mount read is skipped, so its return re-reads once; a reload that
 * lands on a live run still finds it).
 */
export function useSubagentRuns(sessionId: string | null, sessionRunning: boolean): { runs: PiSubagentRunSummary[] } {
  const [runs, setRuns] = useState<PiSubagentRunSummary[]>([]);
  const seqRef = useRef(0);
  // Both flags ride refs the tick guard reads: neither may re-run the effect
  // (a re-run clears the rows the way a session switch does), and the
  // interval outlives the renders that change them.
  const sessionRunningRef = useRef(sessionRunning);
  sessionRunningRef.current = sessionRunning;
  const liveRef = useRef(false);
  liveRef.current = runs.length > 0;
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
    const timer = setInterval(() => {
      // An idle session with no live run cannot change: no read (G11).
      if (!sessionRunningRef.current && !liveRef.current) return;
      void read();
    }, 2_000);
    // A hidden page's return re-reads once: the mount discovery read was
    // skipped while hidden, and an idle session's ticks stay silent.
    const onVisible = () => {
      if (typeof document !== "undefined" && !document.hidden) void read();
    };
    if (typeof document !== "undefined") document.addEventListener("visibilitychange", onVisible);
    return () => {
      clearInterval(timer);
      if (typeof document !== "undefined") document.removeEventListener("visibilitychange", onVisible);
      // The switch invalidates the in-flight answer: the machine drops it.
      seqRef.current += 1;
    };
  }, [sessionId]);
  return { runs };
}
