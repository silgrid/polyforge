import { NextResponse } from "next/server";
import { codeFromError } from "@/lib/server-error";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import { readPolyforgeConfig } from "@/lib/polyforge-config";
import { parsePolyforgeScope, type PolyforgeConfigScope } from "@/lib/polyforge-cells-pure";
import { ConfigWriteError, writePolyforgeConfig, type ConfigWriteRequest } from "@/lib/polyforge-config-write";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/config?cwd=… — the panel's configuration read.
 * Reads only through pf; every degradation (pf absent, below
 * the capability floor, an old list shape, daemon down, an unmapped cwd) is
 * a 200 field, never 5xx. The in-route isApiRequestAllowed check is the
 * authenticated-surface gate (the proxy still enforces PI_WEB_PASSWORD).
 *
 * POST /api/polyforge/config — the write path. Exactly the panel's allowlist;
 * every mutation goes through `pf`, api_key on stdin only, and a refusal is
 * pf's own 4xx envelope with the key-level diff.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const params = new URL(request.url).searchParams;
  const cwd = params.get("cwd") ?? "";
  let scope: PolyforgeConfigScope;
  try {
    // ?host=1 reads the Host keys, ?profile=NAME one Cell's file;
    // with neither, the default Cell's (pinned --profile default)
    scope = parsePolyforgeScope({ host: params.get("host") === "1" ? true : undefined, profile: params.get("profile") ?? undefined });
  } catch (error) {
    return NextResponse.json({ error: error instanceof Error ? error.message : "bad scope" }, { status: 400 });
  }
  try {
    return NextResponse.json(await readPolyforgeConfig(cwd, scope));
  } catch {
    return NextResponse.json({
      pfPresent: false,
      pfVersion: null,
      capable: false,
      reason: "the configuration read failed; polyforge may be unavailable",
      items: [],
      mapped: false,
    });
  }
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
  const write = body as { key?: unknown; action?: unknown };
  try {
    return NextResponse.json(await writePolyforgeConfig(body as ConfigWriteRequest));
  } catch (error) {
    if (error instanceof ConfigWriteError) {
      return NextResponse.json({
        error: error.message,
        // polyforge#259 T4 (owner 2A): the shape keeps `error` and the status; a
        // code rides beside it so the client can say it in Chinese.
        code: codeFromError(error.message),
        params: { key: typeof write.key === "string" ? write.key : null, action: typeof write.action === "string" ? write.action : null },
        ...(error.diff !== undefined ? { diff: error.diff } : {}),
      }, { status: error.status });
    }
    return NextResponse.json({ error: error instanceof Error ? error.message : "polyforge is unavailable" }, { status: 400 });
  }
}
