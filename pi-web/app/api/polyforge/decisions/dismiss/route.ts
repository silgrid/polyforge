import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import { probePolyforgePresent, PolyforgeError } from "@/lib/polyforge-status";
import { dismissPolyforgeDecisions } from "@/lib/polyforge-decisions";
import { parseDismissRequest } from "@/lib/polyforge-decisions-pure";

export const dynamic = "force-dynamic";

/**
 * POST /api/polyforge/decisions/dismiss { ids: number[] } | { all: true } —
 * the human has seen these feed entries: `pf --json watch
 * dismiss <id>...` or `… --all`. JSON-only; the ids are validated as positive
 * integers before any spawn (400 `{ok:false, error}` otherwise). Success is
 * `{ok:true, dismissed}` (the daemon's count); pf absent, a refusal (a step or
 * task process tree is DAEMON-403) or an answer without the count answers 200
 * `{ok:false, error}` (pf's bounded stderr, or the decode failure). Never 5xx.
 */
export async function POST(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ ok: false, error: "Content-Type must be application/json" }, { status: 415 });
  }
  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ ok: false, error: "invalid JSON body" }, { status: 400 });
  }
  const dismiss = parseDismissRequest(body);
  if (!dismiss) {
    return NextResponse.json({ ok: false, error: "body must be {ids: positive integers} or {all: true}" }, { status: 400 });
  }
  if (!(await probePolyforgePresent())) {
    return NextResponse.json({ ok: false, error: "polyforge is not installed" });
  }
  try {
    return NextResponse.json({ ok: true, dismissed: await dismissPolyforgeDecisions(dismiss) });
  } catch (error) {
    return NextResponse.json({ ok: false, error: error instanceof PolyforgeError ? error.message : "polyforge is unavailable" });
  }
}
