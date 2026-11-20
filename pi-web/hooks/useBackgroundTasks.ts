"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import type { BackgroundTasksClientEvent } from "@/lib/types";
import type { BgTaskOutput } from "@/lib/bg-protocol";
import { isBackgroundTasksChangeSignal, nextBackgroundTasksListState } from "@/lib/background-tasks-client";

/**
 * Terminal-notification dedupe: one browser notification per task id, even
 * when several panes/clients replay the same terminal event. Module-level so
 * the mark survives unmounts within the page lifetime.
 */
const notifiedTerminalTaskIds = new Set<string>();

/** Returns true exactly once per task id (the caller should notify on true). */
export function markTerminalNotified(taskId: string): boolean {
  if (notifiedTerminalTaskIds.has(taskId)) return false;
  notifiedTerminalTaskIds.add(taskId);
  return true;
}

/**
 * The running list's and the task panels' client state for one pi session's
 * built-in background tasks (#173): the registry's own records, listed
 * through `GET /api/agent/[id]/bg-tasks` (every record the session owns,
 * settled ones and a restart's interrupted included) and re-read whenever the
 * session's SSE stream reports a change — the events keep the old bridge's
 * snapshot shape (#172's contract), so the hook treats them as signals and
 * reads the richer registry answer. A null session (a claude session, the
 * fresh composer) keeps its state untouched.
 */
export type BackgroundTasksFetchState = import("@/lib/background-tasks-client").BackgroundTasksListState;

export function useBackgroundTasks(sessionId: string | null) {
  const [state, setState] = useState<BackgroundTasksFetchState>({ kind: "loading" });
  const fetchSeq = useRef(0);

  const refresh = useCallback(() => {
    if (!sessionId) return;
    const seq = ++fetchSeq.current;
    // The pure machine owns the sequencing rules: a late answer of a
    // session the hook has already left never lands in the state.
    const settle = (status: number, body: unknown) => {
      setState((prev) => nextBackgroundTasksListState(prev, fetchSeq.current, { seq, status, body }));
    };
    fetch(`/api/agent/${encodeURIComponent(sessionId)}/bg-tasks`)
      .then(async (response) => settle(response.status, await response.json().catch(() => null)))
      .catch(() => settle(0, null));
  }, [sessionId]);

  useEffect(() => {
    if (!sessionId) {
      setState({ kind: "loading" });
      return;
    }
    refresh();
    return () => { fetchSeq.current += 1; };
  }, [sessionId, refresh]);

  /** A live event from this session's SSE stream: a change signal — re-read. */
  const applyEvent = useCallback((event: BackgroundTasksClientEvent) => {
    if (isBackgroundTasksChangeSignal(event)) refresh();
  }, [refresh]);

  /** One task's output tail, straight from the registry (≤ 8 KiB, `tail: true`). */
  const readOutput = useCallback(async (taskId: string): Promise<BgTaskOutput> => {
    if (!sessionId) throw new Error("no active session");
    const response = await fetch(`/api/agent/${encodeURIComponent(sessionId)}/bg-tasks/${encodeURIComponent(taskId)}/logs`);
    const body = await response.json() as (BgTaskOutput & { error?: string });
    if (!response.ok) throw new Error(body.error ?? `HTTP ${response.status}`);
    return body;
  }, [sessionId]);

  /** Stops the task through the executor's idempotent stop. */
  const killTask = useCallback(async (taskId: string) => {
    if (!sessionId) return;
    const response = await fetch(
      `/api/agent/${encodeURIComponent(sessionId)}/bg-tasks/${encodeURIComponent(taskId)}/kill`,
      { method: "POST" },
    );
    if (!response.ok) {
      const body = await response.json().catch(() => null) as { error?: string } | null;
      throw new Error(body?.error ?? `HTTP ${response.status}`);
    }
    refresh();
  }, [sessionId, refresh]);

  return { state, refresh, applyEvent, readOutput, killTask };
}
