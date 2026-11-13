import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import { ConfigWriteError, reloadPolyforgeConfig } from "@/lib/polyforge-config-write";
import { parsePolyforgeScope, type PolyforgeConfigScope } from "@/lib/polyforge-cells-pure";

export const dynamic = "force-dynamic";

/**
 * POST /api/polyforge/config/reload — apply a hand edit on demand through
 * `pf config reload`, then re-read `pf --json ops status` independently so
 * the panel shows the daemon's own newly loaded value (never the write's own
 * reload report).
 */
export async function POST(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ error: "Content-Type must be application/json" }, { status: 415 });
  }
  // an optional {profile} or {host:true} body scopes the reload;
  // without either it is the default Cell's, never the Cell
  // pf would resolve from this server's PF_PROFILE or cwd
  let scope: PolyforgeConfigScope;
  try {
    const body = (await request.json().catch(() => ({}))) as { host?: unknown; profile?: unknown };
    scope = parsePolyforgeScope(body ?? {});
  } catch (error) {
    return NextResponse.json({ error: error instanceof Error ? error.message : "bad scope" }, { status: 400 });
  }
  try {
    return NextResponse.json(await reloadPolyforgeConfig(scope));
  } catch (error) {
    if (error instanceof ConfigWriteError) {
      return NextResponse.json({ error: error.message }, { status: error.status });
    }
    return NextResponse.json({ error: error instanceof Error ? error.message : "polyforge is unavailable" }, { status: 400 });
  }
}
