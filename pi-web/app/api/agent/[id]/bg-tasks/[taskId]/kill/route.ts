import { NextResponse } from "next/server";
import { getBgRuntime } from "@/lib/rpc-manager";

export const dynamic = "force-dynamic";

// POST /api/agent/[id]/bg-tasks/[taskId]/kill — stop one of this session's
// built-in background tasks (polyforge#173, AC2): the executor's idempotent
// stop. A task already settled answers with its record unchanged; a task id of
// another session (or none at all) reads as not found, as JSON.
export async function POST(
  _req: Request,
  { params }: { params: Promise<{ id: string; taskId: string }> },
) {
  const { id, taskId } = await params;
  try {
    const task = getBgRuntime().stop(id, taskId);
    return NextResponse.json({ task, message: `Stopped background task ${task.id}` });
  } catch (error) {
    return NextResponse.json(
      { error: error instanceof Error ? error.message : String(error) },
      { status: 404 },
    );
  }
}
