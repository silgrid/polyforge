import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { polyforgeCellRoot, readJobIndex, readProgressSnapshot } from "@/lib/polyforge-cards";
import { isPolyforgeWorkItemId } from "@/lib/polyforge-cards-pure";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/progress?wi=<wi_…>[&profile=<Cell>] — the job detail's execution
 * progress, whole-job and complete:
 * every spawn of every step, reruns included, as one
 * time-ordered stream of narration (assistant text and non-empty thinking,
 * its original whitespace kept and only masked), one masked
 * tool line per call and one composed line per retained lifecycle event —
 * `{ available, lines: [{ id, kind: "narration"|"tool"|"event", text,
 * variant?, tone?, event?, at?, tool? }], omitted }`: an event line's `at` is
 * its time (epoch ms); a tool line's `tool` is the light
 * `{ name, argument?, status: "ok"|"error"|"running", durationMs? }` only —
 * the call's full input and output never ride this poll, they are read on
 * demand from `GET /api/polyforge/progress/tool`. Every line
 * has no line bound and no per-line cap (omitted stays 0 in the wire shape) — read from
 * the daemon's own files (no pf spawn), for a running job and a finished one
 * still in the daemon's job index. A job the daemon has dropped from that
 * index (`dropJob`, moments after its terminal event) is served from its
 * event log alone (`events/wi-<wi>.jsonl`): its lifecycle
 * lines, the final `terminal` / `job_stopped` included, with no narration or
 * tool line.
 *
 * Reachable by whoever passes the existing /api gate (403 otherwise).
 * Read-only: the wi must have the daemon's id shape (400 before any file
 * read). A missing, unreadable or non-JSON `runtime-state.json` answers 200
 * `available: false` (N6). A readable index without that job, or a record
 * without a capsule, is answered from the event log; only a wi with neither
 * an index record nor a readable event log answers 400 `unknown work item`.
 * Every path comes from the daemon's records or the validated wi, and a
 * transcript must resolve inside the capsule or the data root; nothing
 * readable answers 200 `available: false`. Only the two 400s and the gate's
 * 403 are non-2xx; no response names a path. GET only (any other method is
 * 405).
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
  // the state dir of the work reference's own Cell
  // (`profile`, the job row's WorkRef.profile; absent = default) — never
  // looked up by the work-item literal
  const root = await polyforgeCellRoot(params.get("profile") ?? undefined);
  if (!root) {
    return NextResponse.json({ error: "profile must match [a-z0-9][a-z0-9-]{0,31}" }, { status: 400 });
  }
  // readJobIndex and readProgressSnapshot never throw: an unreadable index
  // is `readable: false`, an unreadable capsule or log is `available: false`.
  const index = await readJobIndex(root);
  if (!index.readable) return NextResponse.json({ available: false, lines: [], omitted: 0 });
  const record = index.records.get(wi);
  const snapshot = await readProgressSnapshot(root, record?.capsuleDir, wi);
  if (!record && !snapshot.available) {
    return NextResponse.json({ error: "unknown work item" }, { status: 400 });
  }
  return NextResponse.json(snapshot);
}
