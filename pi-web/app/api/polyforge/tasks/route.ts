import { NextResponse } from "next/server";
import { readPolyforgeResolvedCell } from "@/lib/polyforge-cells";
import { isApiRequestAllowed } from "@/lib/request-security";
import { listPolyforgeTasks, PolyforgeTasksError, readPolyforgeTasksCapability } from "@/lib/polyforge-tasks";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/tasks — the Tasks tab's list read:
 * exactly one `pf --json task list`, every task of the local daemon, each row
 * id/project/state/prompt_summary plus started_at?/ended_at?/wall_consumed_ns?
 * plus report_tail?/reporter? nine keys, the
 * two new ones forwarded only as pf's own string, never re-cut; the full report
 * stays out. Read-only: this route exports GET alone,
 * so any other method is 405. Degradation-first: pf absent, below the 2.20.12
 * floor and a failed spawn answer 200 with fields, never 5xx; the only
 * non-2xx answer is the gate's 403.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const capability = await readPolyforgeTasksCapability();
  if (!capability.capable) {
    return NextResponse.json({ ...capability, tasks: [] });
  }
  try {
    const tasks = await listPolyforgeTasks();
    // the Cell this list is read from, named in the tab
    const cell = await readPolyforgeResolvedCell();
    return NextResponse.json({ ...capability, tasks, ...(cell ? { cell } : {}) });
  } catch (error) {
    const failure = error instanceof PolyforgeTasksError ? error : new PolyforgeTasksError("polyforge is unavailable");
    return NextResponse.json({ ...capability, tasks: [], error: failure.message });
  }
}
