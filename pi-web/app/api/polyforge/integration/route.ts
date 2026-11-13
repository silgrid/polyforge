import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { readPolyforgeIntegration } from "@/lib/polyforge-integration";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/integration — the Work › 待集成 view's read:
 * `{pfPresent, projects: [{project, landed, batches, error?}], error?}` from
 * `readPolyforgeIntegration` (one `pf --json config list`; per mapped project
 * the landed items through `pf --json queue --attr integration.state=landed`
 * and the newest integrate batches through `--bundle integrate --limit 5`;
 * one `pf watch` per batch that is not terminal). Only the view reads it — the
 * default status poll never does.
 * Reach: pi-web's authenticated surface only (the in-route gate below); a step
 * agent holds no browser session and cannot reach it. Read-only: GET alone, so
 * any other method is 405. Degradation-first: pf absent answers
 * `{pfPresent: false, projects: []}`, a failed mapping read the read's
 * `error`, a failed project its own `error` beside the others — always 200,
 * never 5xx; the only non-2xx answer is the gate's 403. Nothing is logged.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  try {
    return NextResponse.json(await readPolyforgeIntegration());
  } catch {
    return NextResponse.json({ pfPresent: true, projects: [], error: "polyforge is unavailable" });
  }
}
