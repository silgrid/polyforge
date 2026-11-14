import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { getPolyforgeOpsStatus, probePolyforgePresent } from "@/lib/polyforge-status";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/status — the machine's Polyforge job queue, sanitized to
 * the fields the panel renders (pi#108). Degradation-first: pf absent, daemon
 * down, auth failure or a hang all answer 200 JSON with `status: null` (plus
 * `pfPresent`, so the client can distinguish "no pf" from "pf, daemon down";
 * on the former it only re-probes this route, every 30 s and on window
 * focus). No 5xx path: the e2e suite treats any 5xx as a
 * browser error.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!(await probePolyforgePresent())) {
    return NextResponse.json({ pfPresent: false, status: null });
  }
  try {
    const status = await getPolyforgeOpsStatus();
    return NextResponse.json({ pfPresent: true, status });
  } catch (error) {
    // Only pf's own bounded stderr surfaces (PolyforgeError); never argv,
    // never `process.env` values.
    return NextResponse.json({
      pfPresent: true,
      status: null,
      error: error instanceof Error ? error.message : "polyforge is unavailable",
    });
  }
}
