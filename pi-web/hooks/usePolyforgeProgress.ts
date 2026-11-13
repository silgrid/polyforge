"use client";

import { useEffect, useState } from "react";
import { isPolyforgeWorkItemId, nextProgressSnapshot, type PolyforgeProgressSnapshot } from "@/lib/polyforge-cards-pure";

/** The fixed progress poll interval: no setting, no stored value, never the
 *  card poll interval. */
export const PROGRESS_POLL_MS = 2000;
/** One request's bound: a read that never settles is aborted, keeps the last
 *  good snapshot and frees the in-flight slot (the frozen-poll lesson). */
export const PROGRESS_REQUEST_TIMEOUT_MS = 5000;
const PROGRESS_URL = "/api/polyforge/progress";
/** What a read that fails outright counts as (N5): with
 *  nothing held it is the unavailable state, never a perpetual Loading;
 *  with a snapshot held it is kept, like any degraded read. */
const FAILED_READ: PolyforgeProgressSnapshot = { available: false, lines: [], omitted: 0 };

/**
 * The selected job's whole-job execution progress (the
 * snapshot payload), for the Jobs
 * detail only: the panel's detail mounts the component that calls this hook
 * while a job is selected, so closing the dock or deselecting unmounts it
 * and stops everything. Modeled on useMachineLoad.
 *
 * - `wi` is the selected job's work-item id (a card click stores the slug,
 *   so the caller passes the selected job's `wi`); null or a non-id reads
 *   nothing. `profile` is the job row's Cell (its WorkRef.profile; absent =
 *   default) and addresses the read: equal work-item
 *   literals of two Cells are two jobs. A different `wi` or `profile` never
 *   shows the previous job's snapshot.
 * - `running`: an immediate read, then one every PROGRESS_POLL_MS while the
 *   page is visible. Not running: exactly one read, no interval.
 * - Hidden page: the interval is cleared and the request in flight aborted
 *   at once (zero requests while hidden); becoming visible reads at once.
 * - At most one request in flight, each bounded by PROGRESS_REQUEST_TIMEOUT_MS.
 * - A failed or degraded read keeps the last good snapshot
 *   (nextProgressSnapshot); a usable read replaces it wholesale, except the
 *   route's event-log answer after the daemon drops a finished job
 * (lifecycle lines only): the open detail keeps the
 *   narration and tool lines it held and gains the event lines it lacked
 *   (the final `terminal` / `job_stopped`). Null until
 *   the first read settles; a first read that fails outright (HTTP error,
 *   network error, timeout, malformed body) is the unavailable state (N5).
 */
export function usePolyforgeProgress(wi: string | null, running: boolean, profile?: string): PolyforgeProgressSnapshot | null {
  const [state, setState] = useState<{ wi: string; snapshot: PolyforgeProgressSnapshot | null } | null>(null);
  const ref = `${profile ?? ""}/${wi ?? ""}`;

  useEffect(() => {
    if (!isPolyforgeWorkItemId(wi)) return;
    const cell = profile ? `&profile=${encodeURIComponent(profile)}` : "";
    let active = true;
    let interval: ReturnType<typeof setInterval> | null = null;
    let inFlight: AbortController | null = null;
    let readOnce = false;

    const poll = async () => {
      if (!active || inFlight || document.visibilityState !== "visible") return;
      const controller = new AbortController();
      inFlight = controller;
      readOnce = true;
      let timedOut = false;
      const timer = setTimeout(() => { timedOut = true; controller.abort(); }, PROGRESS_REQUEST_TIMEOUT_MS);
      let body: unknown = null;
      try {
        const response = await fetch(`${PROGRESS_URL}?wi=${encodeURIComponent(wi)}${cell}`, { cache: "no-store", signal: controller.signal });
        if (response.ok) body = await response.json();
      } catch {
        // Network error, timeout or a non-JSON body: a failed read.
      } finally {
        clearTimeout(timer);
        if (inFlight === controller) inFlight = null;
      }
      // Hiding, deselecting or unmounting aborts a read: it writes nothing.
      if (!active || (controller.signal.aborted && !timedOut)) return;
      setState((previous) => {
        const held = previous?.wi === ref ? previous.snapshot : null;
        // A failed or malformed read counts as unavailable: kept over a held
        // snapshot, the unavailable state with nothing held (N5).
        const read = nextProgressSnapshot(null, body) === null ? FAILED_READ : body;
        const snapshot = nextProgressSnapshot(held, read);
        return snapshot === held && previous?.wi === ref ? previous : { wi: ref, snapshot };
      });
    };
    const stop = () => {
      if (interval !== null) clearInterval(interval);
      interval = null;
      inFlight?.abort();
      inFlight = null;
    };
    const start = () => {
      stop();
      if (!active || document.visibilityState !== "visible") return;
      if (running) {
        void poll();
        interval = setInterval(() => void poll(), PROGRESS_POLL_MS);
      } else if (!readOnce) {
        void poll();
      }
    };
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") start();
      else stop();
    };

    document.addEventListener("visibilitychange", onVisibilityChange);
    // One microtask late, as useMachineLoad: Strict Mode's mount → cleanup →
    // mount issues one first request, not an aborted duplicate.
    queueMicrotask(start);
    return () => {
      active = false;
      document.removeEventListener("visibilitychange", onVisibilityChange);
      stop();
    };
  }, [wi, running, profile, ref]);

  return state && state.wi === ref ? state.snapshot : null;
}
