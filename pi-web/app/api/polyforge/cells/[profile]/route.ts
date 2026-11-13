import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import { changePolyforgeCell } from "@/lib/polyforge-cells";
import { ConfigWriteError } from "@/lib/polyforge-config-write";

export const dynamic = "force-dynamic";

/**
 * POST /api/polyforge/cells/<profile> {action: "update"|"repair"} — re-apply
 * a Cell's file; {action: "remove"} (the page's path) or DELETE — `pf cell remove` (refused while it holds work;
 * the state dir is kept). Reach: pi-web's authenticated surface; validated
 * and capability-gated before any spawn. No permanent-cleanup path exists
 * here (CLI-only by design).
 */
type Ctx = { params: Promise<{ profile: string }> };

function answer(error: unknown) {
  const status = error instanceof ConfigWriteError && error.status < 500 ? error.status : 200;
  return NextResponse.json({ error: error instanceof Error ? error.message : "polyforge is unavailable" }, { status });
}

export async function POST(request: Request, ctx: Ctx) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ error: "Content-Type must be application/json" }, { status: 415 });
  }
  const { profile } = await ctx.params;
  let action: unknown;
  try {
    action = ((await request.json()) as { action?: unknown })?.action;
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  if (action !== "update" && action !== "repair" && action !== "remove") {
    return NextResponse.json({ error: "action must be update, repair or remove" }, { status: 400 });
  }
  try {
    return NextResponse.json({ ok: true, result: await changePolyforgeCell(profile, action) });
  } catch (error) {
    return answer(error);
  }
}

export async function DELETE(request: Request, ctx: Ctx) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const { profile } = await ctx.params;
  try {
    return NextResponse.json({ ok: true, result: await changePolyforgeCell(profile, "remove") });
  } catch (error) {
    return answer(error);
  }
}
