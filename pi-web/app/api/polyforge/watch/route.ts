import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { getPolyforgeWatch, isPolyforgeWiRef, PolyforgeError } from "@/lib/polyforge-status";
import { isPolyforgeProfileName } from "@/lib/polyforge-cells-pure";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/watch?wi=<slug|id> — one work item's workflow header,
 * execution-ordered steps and the newest 40 events (pi#108). The ref is
 * validated BEFORE any subprocess argv (a malformed ref is a 400 and never
 * spawns pf); every other failure — pf absent, daemon down, unknown ref, a
 * hang — answers 200 JSON with `status: null`, so the client keeps its last
 * snapshot. No 5xx path.
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
    const status = await getPolyforgeWatch(wi, profile);
    return NextResponse.json({ status });
  } catch (error) {
    if (error instanceof PolyforgeError) {
      return NextResponse.json({ status: null, error: error.message });
    }
    return NextResponse.json({ status: null, error: "polyforge is unavailable" });
  }
}
