import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { isProgressToolId, polyforgeCellRoot, readJobIndex, readProgressEventLog, readProgressToolDetail } from "@/lib/polyforge-cards";
import { isPolyforgeWorkItemId } from "@/lib/polyforge-cards-pure";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/progress/tool?wi=<wi_…>&id=<tool line id>[&profile=<Cell>] — one tool
 * call's full detail for the job detail's expanded progress card,
 * fetched only when a reader expands that card; the 2 s
 * progress poll carries only the light tool fields.
 *
 * Reachable by whoever passes the existing /api gate, the same in-route
 * `isApiRequestAllowed` check as `GET /api/polyforge/progress` (403
 * otherwise). Read-only: it logs nothing, writes nothing and reads no env of
 * its own. `wi` must have the daemon's id shape and `id` the progress line
 * id shape (`<16-hex>.<offset>.<block>`, no path), both checked before any
 * file read (400); a wi with neither an index record nor a readable event
 * log is 400 `unknown work item`. Otherwise 200: `{ available: true,
 * hasResult, tool: { name, argument?, status, durationMs?, input, output,
 * isError } }` — input and output complete and credential-masked, `output`
 * null while no result is paired — or `{ available: false, error }` when the
 * job index, the capsule, the transcript or the call is no longer readable.
 * Never 5xx; no response names a path. GET only (any other method is 405).
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const params = new URL(request.url).searchParams;
  const wi = params.get("wi");
  if (!isPolyforgeWorkItemId(wi)) {
    return NextResponse.json({ error: "wi must be a work-item id (wi_…)" }, { status: 400 });
  }
  const id = params.get("id");
  if (!isProgressToolId(id)) {
    return NextResponse.json({ error: "id must be a progress tool line id" }, { status: 400 });
  }
  // the state dir of the work reference's own Cell
  // (`profile`, the job row's WorkRef.profile; absent = default) — never
  // looked up by the work-item literal
  const root = await polyforgeCellRoot(params.get("profile") ?? undefined);
  if (!root) {
    return NextResponse.json({ error: "profile must match [a-z0-9][a-z0-9-]{0,31}" }, { status: 400 });
  }
  // None of these throw: an unreadable index is `readable: false`, an
  // unreadable log undefined, an unreadable call `available: false`.
  const index = await readJobIndex(root);
  if (!index.readable) return NextResponse.json({ available: false, error: "the job index is not readable" });
  const record = index.records.get(wi);
  if (!record && !(await readProgressEventLog(root, wi))) {
    return NextResponse.json({ error: "unknown work item" }, { status: 400 });
  }
  return NextResponse.json(await readProgressToolDetail(root, record?.capsuleDir, id));
}
