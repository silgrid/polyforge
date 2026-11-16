import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { probePolyforgePresent, PolyforgeError } from "@/lib/polyforge-status";
import { readPolyforgeUnclassified } from "@/lib/polyforge-unclassified";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/unclassified — the Host region's list of the error
 * texts that fell into the classification fallback over the last 7 days
 * (polyforge#51): from the daemon through `pf --json ops unclassified`,
 * sanitized. Degradation-first like the decisions route: pf absent → 200
 * `{pfPresent:false, report:null}`; a failed or unreadable read → 200
 * `{pfPresent:true, report:null, error}` with pf's bounded stderr only.
 * No 5xx. The daemon itself refuses a step or task agent's process tree on
 * the route; this panel's pf call runs as the operator session.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!(await probePolyforgePresent())) {
    return NextResponse.json({ pfPresent: false, report: null });
  }
  try {
    return NextResponse.json({ pfPresent: true, report: await readPolyforgeUnclassified() });
  } catch (error) {
    return NextResponse.json({
      pfPresent: true,
      report: null,
      error: error instanceof PolyforgeError ? error.message : "polyforge is unavailable",
    });
  }
}
