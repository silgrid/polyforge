import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { isPolyforgeProfileName } from "@/lib/polyforge-cells-pure";
import { isPolyforgeWiRef, PolyforgeError } from "@/lib/polyforge-status";
import { getPolyforgeParams } from "@/lib/polyforge-params";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/params?wi=<slug|id> — one work item's composed
 * params view (polyforge#79): exactly one `pf [--profile p] --json params
 * <wi>` spawn, bounded by the one read timeout every pf read shares, the
 * ref and profile validated BEFORE any spawn (a malformed ref is a 400
 * and never spawns pf), the answer through the view's passport (one row
 * per declared step, every declared param's final value and source —
 * the per-ticket layer, the project's yaml, or the lane default; a
 * queued work item's per-ticket layer is the reserved step_params attrs
 * key). Read-only: GET alone, any other method is 405. Degradation-first:
 * pf absent, a refusal, a hang or an undecodable body answer 200 with
 * `params: null` (a garbage answer is no view, never a 5xx). The panel's
 * job detail reads the same view through the watch route's composed
 * answer (`status.paramsView`); this route is the 工单详情's one read per
 * open.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const searchParams = new URL(request.url).searchParams;
  const wi = searchParams.get("wi");
  // ?profile= addresses the row's Cell (validated before any spawn)
  const profile = searchParams.get("profile") ?? undefined;
  if (profile !== undefined && !isPolyforgeProfileName(profile)) {
    return NextResponse.json({ error: "profile must match [a-z0-9][a-z0-9-]{0,31}" }, { status: 400 });
  }
  if (!isPolyforgeWiRef(wi)) {
    return NextResponse.json(
      { error: "wi must be a slug (pi#107) or id (wi_…)" },
      { status: 400 },
    );
  }
  try {
    const params = await getPolyforgeParams(wi, profile);
    return NextResponse.json({ params });
  } catch (error) {
    if (!(error instanceof PolyforgeError)) return NextResponse.json({ params: null, error: "polyforge is unavailable" });
    return NextResponse.json({ params: null, error: error.message });
  }
}
