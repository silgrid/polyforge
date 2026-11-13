"use client";

import { useEffect, useState } from "react";
import { nextMachineLoadMetrics, type MachineLoadMetrics } from "@/lib/machine-load-view";

/** The fixed poll interval: no setting, no stored value. */
export const MACHINE_LOAD_POLL_MS = 2000;
/** One request's bound: a request that never settles is
 *  aborted after one poll interval (2000 ms), so it cannot hold the in-flight
 *  slot and freeze the last good values; a timeout keeps the last good values
 *  and frees the slot for the next tick. A cold `next dev` compile of the
 *  route can exceed this bound, so the first request after a dev start may be
 *  aborted once and the section shows `不可用` until the next tick lands. */
export const MACHINE_LOAD_REQUEST_TIMEOUT_MS = MACHINE_LOAD_POLL_MS;
const MACHINE_LOAD_URL = "/api/machine-load";

/**
 * Machine load for MachineLoadSection, which only the Debug panel
 * (DebugPanelView) renders, and only while open; the
 * Session-info popover no longer mounts it. Closing the panel drops the
 * timer and the values.
 *
 * - Polls only while `document.visibilityState === "visible"`: on mount and on
 *   becoming visible it fetches once immediately, then every 2 s; on hide it
 *   clears the interval and aborts the request in flight (zero requests while
 *   hidden).
 * - At most one request in flight: a tick that finds one still running skips.
 *   Each request is bounded at MACHINE_LOAD_REQUEST_TIMEOUT_MS (one interval);
 *   a timed-out request keeps the last good values and frees the slot.
 * - State changes only on a valid whitelisted success; a failed tick keeps the
 *   last good values, and with none yet the view shows the unavailable text.
 */
export function useMachineLoad(): MachineLoadMetrics | null {
  const [metrics, setMetrics] = useState<MachineLoadMetrics | null>(null);

  useEffect(() => {
    let active = true;
    let interval: ReturnType<typeof setInterval> | null = null;
    let inFlight: AbortController | null = null;

    const poll = async () => {
      if (!active || inFlight || document.visibilityState !== "visible") return;
      const controller = new AbortController();
      inFlight = controller;
      // AbortController + setTimeout: the timeout-signal helper is absent on
      // the Safari 16.2 target.
      const timer = setTimeout(() => controller.abort(), MACHINE_LOAD_REQUEST_TIMEOUT_MS);
      try {
        const response = await fetch(MACHINE_LOAD_URL, { cache: "no-store", signal: controller.signal });
        const body: unknown = response.ok ? await response.json() : null;
        if (active && !controller.signal.aborted) setMetrics((previous) => nextMachineLoadMetrics(previous, body));
      } catch {
        // Network error, abort or a non-JSON body: keep the last good values.
      } finally {
        clearTimeout(timer);
        if (inFlight === controller) inFlight = null;
      }
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
      void poll();
      interval = setInterval(() => void poll(), MACHINE_LOAD_POLL_MS);
    };
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") start();
      else stop();
    };

    document.addEventListener("visibilitychange", onVisibilityChange);
    // One microtask late: React Strict Mode's synchronous mount → cleanup →
    // mount in development then issues one first request, not an aborted
    // duplicate. Microtasks are not timers, so the fetch is still immediate.
    queueMicrotask(start);
    return () => {
      active = false;
      document.removeEventListener("visibilitychange", onVisibilityChange);
      stop();
    };
  }, []);

  return metrics;
}
