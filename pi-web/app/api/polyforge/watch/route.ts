import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { getPolyforgeWatch, isPolyforgeWiRef, PolyforgeError } from "@/lib/polyforge-status";
import { isPolyforgeProfileName } from "@/lib/polyforge-cells-pure";
import { getPolyforgeOverride } from "@/lib/polyforge-overrides";
import type { PolyforgeOverrideView } from "@/lib/polyforge-overrides";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/watch?wi=<slug|id> — one work item's workflow header,
 * execution-ordered steps and the newest 40 events (pi#108). The ref is
 * validated BEFORE any subprocess argv (a malformed ref is a 400 and never
 * spawns pf); every other failure — pf absent, daemon down, unknown ref, a
 * hang — answers 200 JSON with `status: null`, so the client keeps its last
 * snapshot. No 5xx path.
 *
 * polyforge#42 (G11): the same response also carries the per-step overlay
 * view (`status.overrideView`, `pf [--profile p] --json override <wi>`,
 * bounded by the one pf read timeout) — the
 * panel's snapshot is the one read surface, and the selected unstarted
 * step's controls fetch nothing on selection (a click-caused read broke
 * the graph suite's G11). The view is composed only when the watch read
 * itself answered a snapshot; a failed, refused or unparseable view read
 * (an older pf without the verb among them) degrades to a snapshot without
 * the field — the controls still render the switch (skippable, the
 * overlay's values) and only the effective-model source line falls back to
 * its none text. Writes keep their own POST face
 * (app/api/polyforge/override/route.ts).
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
    let overrideView: PolyforgeOverrideView | undefined;
    if (status) {
      overrideView = await getPolyforgeOverride(wi, profile).catch(() => undefined) ?? undefined;
    }
    return NextResponse.json({ status: status ? { ...status, ...(overrideView ? { overrideView } : {}) } : status });
  } catch (error) {
    if (error instanceof PolyforgeError) {
      return NextResponse.json({ status: null, error: error.message });
    }
    return NextResponse.json({ status: null, error: "polyforge is unavailable" });
  }
}
