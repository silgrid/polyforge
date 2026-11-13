import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import { addPolyforgeCell, readPolyforgeCells, type PolyforgeCellAddRequest } from "@/lib/polyforge-cells";
import { ConfigWriteError } from "@/lib/polyforge-config-write";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/cells — the Host's Cells (`pf --json cell list`) plus
 * the multi-Cell capability gate. Degradation-first: 200
 * with `cells: null` and an `error`/`upgradeHint` field, never 5xx.
 *
 * POST /api/polyforge/cells {profile, server, apiKey, modelSource?} — add a
 * Cell through `pf cell add` (the key on stdin only, never echoed). Reach:
 * pi-web's authenticated surface (in-route gate); JSON-only; validated and
 * capability-gated before any spawn; pf's refusal keeps its 4xx.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  return NextResponse.json(await readPolyforgeCells());
}

export async function POST(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ error: "Content-Type must be application/json" }, { status: 415 });
  }
  let body: PolyforgeCellAddRequest;
  try {
    body = (await request.json()) as PolyforgeCellAddRequest;
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  try {
    return NextResponse.json({ ok: true, result: await addPolyforgeCell(body ?? {}) });
  } catch (error) {
    const status = error instanceof ConfigWriteError && error.status < 500 ? error.status : 200;
    return NextResponse.json({ error: error instanceof Error ? error.message : "polyforge is unavailable" }, { status });
  }
}
