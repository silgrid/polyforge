import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { probePolyforgePresent, PolyforgeError } from "@/lib/polyforge-status";
import { readPolyforgeDecisions } from "@/lib/polyforge-decisions";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/decisions — the machine's decision feed:
 * every human decision of every Cell, from the daemon through
 * `pf --json watch decisions`, sanitized (`{decisions:[…], attention}`).
 * Degradation-first like the status route: pf absent → 200
 * `{pfPresent:false, feed:null}`; a failed or unreadable read → 200
 * `{pfPresent:true, feed:null, error}` with pf's bounded stderr only. No 5xx.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!(await probePolyforgePresent())) {
    return NextResponse.json({ pfPresent: false, feed: null });
  }
  try {
    return NextResponse.json({ pfPresent: true, feed: await readPolyforgeDecisions() });
  } catch (error) {
    return NextResponse.json({
      pfPresent: true,
      feed: null,
      error: error instanceof PolyforgeError ? error.message : "polyforge is unavailable",
    });
  }
}
