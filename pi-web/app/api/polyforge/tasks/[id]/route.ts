import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import {
  isPolyforgeTaskId,
  PolyforgeTasksError,
  readPolyforgeTasksCapability,
  showPolyforgeTask,
} from "@/lib/polyforge-tasks";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/tasks/[id] — one task's detail: exactly
 * one `pf --json task show <id>`, the id (`task_` + 16 hex) validated before
 * any spawn. Read-only: GET alone, any other method is 405. pf absent and
 * below the 2.20.12 floor answer the capability fields with `task: null`; a
 * failed or refused spawn answers 200 with an `error` field — never 5xx. The
 * only non-2xx answers are the gate's 403 and the malformed-id 400.
 */
export async function GET(
  request: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const { id } = await params;
  if (!isPolyforgeTaskId(id)) {
    return NextResponse.json({ error: "id must be a task id" }, { status: 400 });
  }
  const capability = await readPolyforgeTasksCapability();
  if (!capability.capable) {
    return NextResponse.json({ ...capability, task: null });
  }
  try {
    const task = await showPolyforgeTask(id);
    return NextResponse.json({ ...capability, task });
  } catch (error) {
    const failure = error instanceof PolyforgeTasksError ? error : new PolyforgeTasksError("polyforge is unavailable");
    return NextResponse.json({ ...capability, task: null, error: failure.message });
  }
}
