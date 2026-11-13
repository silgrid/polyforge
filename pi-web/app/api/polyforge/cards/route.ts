import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { getPolyforgeOpsStatus, PolyforgeError, probePolyforgePresent, type PolyforgeOpsStatus } from "@/lib/polyforge-status";
import { readPolyforgeCards } from "@/lib/polyforge-cards";
import { parseCardCursors } from "@/lib/polyforge-cards-pure";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/cards[?cursors=<JSON map wi → cursor>] — the Jobs tab's
 * whole card stream in one read: every `pf ops status` job
 * as a card with its goal, earliest start, per-step durations and the
 * progress increment after its cursor, read from the daemon's own files.
 *
 * Read-only and degradation-first: pf absent, daemon down, unreadable files
 * and malformed cursors all answer 200 (`cards: null` when there is no job
 * list to build from, `available: false` per card when its transcript is
 * not readable). The only non-2xx answer is the gate's 403; no client value
 * ever names a path — the work items come from pf's own job list and the
 * cursors are validated opaque offsets.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!(await probePolyforgePresent())) {
    return NextResponse.json({ pfPresent: false, cards: null });
  }
  let status: PolyforgeOpsStatus | null;
  try {
    status = await getPolyforgeOpsStatus();
  } catch (error) {
    // Only pf's own bounded stderr surfaces (PolyforgeError).
    const message = error instanceof PolyforgeError ? error.message : "polyforge is unavailable";
    return NextResponse.json({ pfPresent: true, cards: null, error: message });
  }
  if (!status) {
    return NextResponse.json({ pfPresent: true, cards: null, error: "polyforge is unavailable" });
  }
  const cursors = parseCardCursors(new URL(request.url).searchParams.get("cursors"));
  const cards = await readPolyforgeCards(status.jobs, cursors, Date.now());
  return NextResponse.json({ pfPresent: true, cards });
}
