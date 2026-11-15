import { NextResponse } from "next/server";
import { isApiRequestAllowed, hasJsonContentType } from "@/lib/request-security";
import { isPolyforgeProfileName } from "@/lib/polyforge-cells-pure";
import { isPolyforgeWiRef, PolyforgeError } from "@/lib/polyforge-status";
import { getPolyforgeOverride, overrideActionArgv, sendPolyforgeOverrideAction, type PolyforgeOverrideAction } from "@/lib/polyforge-overrides";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/override?wi=<slug|id>&profile=<cell> — polyforge#25's
 * per-step overlay view: `pf [--profile p] --json override <wi>`, one row
 * per declared step (its status, the lane's skippable declaration, the
 * overlay aspects and the effective model chain's source). The ref is
 * validated BEFORE any subprocess argv; every failure answers 200 JSON
 * with `status: null`, so the client keeps its last view. No 5xx path.
 * Reach: pi-web's authenticated surface only — a step agent has no
 * browser session and cannot reach it (and the daemon refuses its
 * process tree besides).
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) return NextResponse.json({ status: null, error: "Forbidden" }, { status: 403 });
  const searchParams = new URL(request.url).searchParams;
  const wi = searchParams.get("wi");
  const profile = searchParams.get("profile") ?? undefined;
  if (profile !== undefined && !isPolyforgeProfileName(profile)) {
    return NextResponse.json({ status: null, error: "profile must match [a-z0-9][a-z0-9-]{0,31}" }, { status: 400 });
  }
  if (!isPolyforgeWiRef(wi)) {
    return NextResponse.json({ status: null, error: "wi must be a slug (pi#107) or id (wi_…)" }, { status: 400 });
  }
  try {
    const status = await getPolyforgeOverride(wi, profile);
    return NextResponse.json({ status });
  } catch (error) {
    if (error instanceof PolyforgeError) return NextResponse.json({ status: null, error: error.message }, { status: 200 });
    return NextResponse.json({ status: null, error: "polyforge is unavailable" }, { status: 200 });
  }
}

/**
 * POST /api/polyforge/override { wi, profile?, action } — one overlay
 * change through `pf [--profile p] --json override <wi> --step …`:
 * `action` is exactly one of {kind:"models", step, models:[…]},
 * {kind:"clearModels", step} or {kind:"skip", step, value} (validated
 * before any spawn). `{ ok: true, result }` carries the write's outcome
 * (the applied aspect, the posture, whether the capsule chain carries
 * the change); pf's refusal is 409 `{ok:false, error, reason?}` with its
 * typed envelope as `<code>: <detail>`. Never 5xx.
 */
export async function POST(request: Request) {
  if (!isApiRequestAllowed(request)) return NextResponse.json({ ok: false, error: "Forbidden" }, { status: 403 });
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ ok: false, error: "Content-Type must be application/json" }, { status: 415 });
  }
  let body: { wi?: unknown; profile?: unknown; action?: unknown };
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ ok: false, error: "invalid JSON body" }, { status: 400 });
  }
  if (typeof body.wi !== "string" || !isPolyforgeWiRef(body.wi)) {
    return NextResponse.json({ ok: false, error: "wi must be a slug (pi#107) or id (wi_…)" }, { status: 400 });
  }
  const profile = body.profile === undefined || body.profile === "" ? undefined : body.profile;
  if (profile !== undefined && !isPolyforgeProfileName(profile)) {
    return NextResponse.json({ ok: false, error: "profile must match [a-z0-9][a-z0-9-]{0,31}" }, { status: 400 });
  }
  const action = body.action as PolyforgeOverrideAction | null | undefined;
  if (!overrideActionArgv(body.wi, action ?? null)) {
    return NextResponse.json({ ok: false, error: "the override action is invalid (a step, and one action with its whole value)" }, { status: 400 });
  }
  try {
    const result = await sendPolyforgeOverrideAction(body.wi, action as PolyforgeOverrideAction, profile);
    return NextResponse.json({ ok: true, result });
  } catch (error) {
    if (!(error instanceof PolyforgeError)) return NextResponse.json({ ok: false, error: "polyforge is unavailable" }, { status: 409 });
    return NextResponse.json({ ok: false, error: error.message, ...(error.reason ? { reason: error.reason } : {}) }, { status: 409 });
  }
}
