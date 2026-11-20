/**
 * Client-safe view model of a pi session's RUNNING built-in subagent runs —
 * the rows the running list above the composer shows (polyforge#173), with
 * the pure projection the route and the tests share. No node built-ins: the
 * client bundle imports this module.
 */

import type { SubagentRunInfo } from "./subagents";

/** One live subagent row of the running list (the fields the list needs). */
export interface PiSubagentRunSummary {
  sessionId: string;
  parentSessionId: string;
  profile: string;
  description: string;
  status: "running" | "queued";
  /** ISO timestamp of the run's dispatch — the list's ordering key. */
  createdAt: string;
}

/**
 * The live map's runs of ONE parent session, projected to the running list's
 * rows: only `running` and `queued` survive (a settled run left the list the
 * moment its record settled), oldest first.
 */
export function projectRunningSubagentRuns(runs: readonly SubagentRunInfo[]): PiSubagentRunSummary[] {
  return runs
    .filter((run): run is SubagentRunInfo & { status: "running" | "queued" } => run.status === "running" || run.status === "queued")
    .map((run) => ({
      sessionId: run.sessionId,
      parentSessionId: run.parentSessionId,
      profile: run.profile,
      description: run.description,
      status: run.status,
      createdAt: run.createdAt,
    }))
    .sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
}
