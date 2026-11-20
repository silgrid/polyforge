import { NextResponse } from "next/server";
import { listSubagentRunsOf } from "@/lib/rpc-manager";
import { projectRunningSubagentRuns } from "@/lib/subagent-runs-view";

export const dynamic = "force-dynamic";

// GET /api/agent/[id]/subagent-runs — the parent session's RUNNING built-in
// subagent runs (polyforge#173): the live map's rows above this session's
// composer, running and queued alike. The map is this process's own; a
// settled run never appears (its record left the map when it settled), and a
// restart's interrupted runs surface through the session tree, not here.
export async function GET(
  _req: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params;
  return NextResponse.json({ runs: projectRunningSubagentRuns(listSubagentRunsOf(id)) });
}
