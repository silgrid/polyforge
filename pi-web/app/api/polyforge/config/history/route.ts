import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import { readPolyforgeConfigHistory } from "@/lib/polyforge-config-history";
import { ConfigWriteError, rollbackPolyforgeConfig, type ConfigRollbackRequest } from "@/lib/polyforge-config-write";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/config/history — the bounded (50-entry) panel write
 * history, newest first. api_key entries carry set/unset only and are not
 * rollbackable.
 *
 * POST /api/polyforge/config/history — rollback: a fresh read, then the same
 * CAS write path, recorded as a new entry.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  return NextResponse.json({ version: 1, entries: readPolyforgeConfigHistory() });
}

export async function POST(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ error: "Content-Type must be application/json" }, { status: 415 });
  }
  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  try {
    return NextResponse.json(await rollbackPolyforgeConfig(body as ConfigRollbackRequest));
  } catch (error) {
    if (error instanceof ConfigWriteError) {
      return NextResponse.json({ error: error.message, ...(error.diff !== undefined ? { diff: error.diff } : {}) }, { status: error.status });
    }
    return NextResponse.json({ error: error instanceof Error ? error.message : "polyforge is unavailable" }, { status: 400 });
  }
}
