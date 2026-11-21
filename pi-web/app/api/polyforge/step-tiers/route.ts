import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { readStepTierCatalog } from "@/lib/polyforge-config";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/step-tiers — the shared candidate picker's one catalog
 * read (polyforge#151, 85b2): the Host's [step_tiers] tiers and the defined
 * model identities per harness, composed by the one server composer the
 * watch route's snapshot composition shares (lib/polyforge-config.ts's
 * readStepTierCatalog — the dock's controls fetch nothing on selection,
 * G11). Reads only, degradation-first: every failure — pf absent, below the
 * family's own 0.2.4 floor, a refused Host pin, a failed list, an
 * unloadable model list — answers 200 with `capable: false` and the reason,
 * and the picker shows the manual fill alone. No write face, never 5xx.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  return NextResponse.json(await readStepTierCatalog(), { headers: { "Cache-Control": "no-store" } });
}
