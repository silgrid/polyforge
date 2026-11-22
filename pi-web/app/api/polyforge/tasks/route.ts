import { NextResponse } from "next/server";
import { hasPolyforgeCellCapabilities, polyforgeScopePrefix } from "@/lib/polyforge-cells-pure";
import { isApiRequestAllowed } from "@/lib/request-security";
import { getPolyforgeOpsStatus, probePolyforgeVersion, servingPolyforgeCells } from "@/lib/polyforge-status";
import { fillTaskContextWindows, listPolyforgeTasks, PolyforgeTasksError, readPolyforgeTasksCapability, type PolyforgeTaskRow } from "@/lib/polyforge-tasks";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/tasks — the Tasks tab's list read: on a multi-Cell pf
 * one `pf --profile <cell> --json task list` per valid Cell of the Host
 * aggregate (a degraded one too, r2#1: its daemon still runs and answers
 * its local reads, the same set the Host's task sum and the Jobs list
 * count) — the same `pf --json ops status` read the Jobs tab
 * polls (its shared cached reader; no new pf probe, polyforge#73) — each row
 * named with its Cell by this outer layer (`cell`, the tenth field; the
 * nine-key row passport itself is unchanged). A Cell whose read fails
 * degrades to one `cellErrors` entry while the rest list. A pf without the
 * multi-Cell protocol — and an aggregate that cannot be read, or one with no
 * valid Cell — keeps today's single unflagged `pf --json task list`.
 * Read-only: this route exports GET alone, so any other method is 405.
 * Degradation-first: pf absent, below the 2.20.12 floor and a failed spawn
 * answer 200 with fields, never 5xx; the only non-2xx answer is the gate's
 * 403.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const capability = await readPolyforgeTasksCapability();
  if (!capability.capable) {
    return NextResponse.json({ ...capability, tasks: [] });
  }
  const { capabilities } = await probePolyforgeVersion();
  let cells: string[] = [];
  if (hasPolyforgeCellCapabilities(capabilities)) {
    // an aggregate that cannot be read (daemon down) is the pre-Cell
    // degradation: no valid Cells known, the one unflagged read below
    cells = servingPolyforgeCells((await getPolyforgeOpsStatus().catch(() => null))?.cells);
  }
  try {
    // The pre-Cell fallback is also the daemon-down degradation: one config
    // file, one unflagged read — exactly the pre-polyforge#73 behaviour. It
    // is deleted with polyforgeScopeArgv's own pre-Cell branch
    // (lib/polyforge-cells-pure.ts), once the oldest pf pi-web supports
    // reports the multi-Cell capabilities.
    if (cells.length === 0) {
      const filled = await fillTaskContextWindows(await listPolyforgeTasks());
      return NextResponse.json({ ...capability, tasks: filled });
    }
    const reads = await Promise.all(cells.map(async (cell) => {
      try {
        return { cell, tasks: (await listPolyforgeTasks(polyforgeScopePrefix({ profile: cell }))).map((row) => ({ ...row, cell })) };
      } catch (error) {
        const failure = error instanceof PolyforgeTasksError ? error : new PolyforgeTasksError("polyforge is unavailable");
        return { cell, error: failure.message };
      }
    }));
    const tasks: (PolyforgeTaskRow & { cell: string })[] = reads.flatMap((read) => read.tasks ?? []);
    const filled = await fillTaskContextWindows(tasks);
    const cellErrors = reads.flatMap((read) => (read.error === undefined ? [] : [{ cell: read.cell, error: read.error }]));
    return NextResponse.json({ ...capability, tasks: filled, ...(cellErrors.length > 0 ? { cellErrors } : {}) });
  } catch (error) {
    const failure = error instanceof PolyforgeTasksError ? error : new PolyforgeTasksError("polyforge is unavailable");
    return NextResponse.json({ ...capability, tasks: [], error: failure.message });
  }
}
