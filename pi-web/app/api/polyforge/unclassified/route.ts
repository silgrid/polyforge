import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/unclassified — zero-value stub (polyforge#51 r1#1
 * re-sequencing): a fixed empty answer until the next commit restores the
 * pf read.
 */
export async function GET(_request: Request) {
  return NextResponse.json({ pfPresent: false, report: null });
}
