import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/params?wi=<slug|id> — polyforge#79's params read
 * face.
 *
 * THIS COMMIT IS THE SIGNATURE STUB ahead of u6's red replay (review
 * r1#3): the GET the route tests import, no gate, no spawn, a fixed
 * degradation answer — so the unit's recorded run fails on its
 * assertions, never on a missing module. The real route lands in the
 * next commits.
 */
export async function GET() {
  return NextResponse.json({ params: null });
}
