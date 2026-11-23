import { NextResponse } from "next/server";
import { isPolyforgeProfileName, polyforgeScopeArgv } from "@/lib/polyforge-cells-pure";
import { isApiRequestAllowed } from "@/lib/request-security";
import { probePolyforgeVersion } from "@/lib/polyforge-status";
import {
  fillTaskContextWindow,
  isPolyforgeTaskId,
  PolyforgeTasksError,
  readPolyforgeTasksCapability,
  showPolyforgeTask,
} from "@/lib/polyforge-tasks";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/tasks/[id] — one task's detail: exactly
 * one `pf --json task show <id>`, the id (`task_` + 16 hex) validated before
 * any spawn. `?profile=<cell>` names the row's Cell (polyforge#73): a valid
 * profile pins the read (`pf --profile <cell> --json task show <id>`, the
 * global flags before the verb); a malformed one is a 400 before any spawn.
 * A pf without the multi-Cell protocol cannot address a Cell — it has one —
 * so the read stays unflagged, exactly like the list face's fallback.
 * Read-only: GET alone, any other method is 405. pf absent and below the
 * 2.20.12 floor answer the capability fields with `task: null`; a failed or
 * refused spawn answers 200 with an `error` field — never 5xx. The only
 * non-2xx answers are the gate's 403 and the malformed-id 400.
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
  const profile = new URL(request.url).searchParams.get("profile");
  if (profile !== null && !isPolyforgeProfileName(profile)) {
    return NextResponse.json({ error: "profile must match [a-z0-9][a-z0-9-]{0,31}" }, { status: 400 });
  }
  const capability = await readPolyforgeTasksCapability();
  if (!capability.capable) {
    return NextResponse.json({ ...capability, task: null });
  }
  // the pin: the profile the row names, only on a pf that can address it
  // (an unaddressable scope falls back to the unflagged single-Cell read —
  // a pre-Cell pf has no --profile flag and one Cell, so the read is right)
  const { capabilities } = await probePolyforgeVersion();
  const pin = profile ? (polyforgeScopeArgv({ profile }, capabilities)?.prefix ?? []) : [];
  try {
    const task = await showPolyforgeTask(id, pin);
    const filled = task === null ? null : await fillTaskContextWindow(task);
    return NextResponse.json({ ...capability, task: filled });
  } catch (error) {
    const failure = error instanceof PolyforgeTasksError ? error : new PolyforgeTasksError("polyforge is unavailable");
    return NextResponse.json({ ...capability, task: null, error: failure.message });
  }
}
