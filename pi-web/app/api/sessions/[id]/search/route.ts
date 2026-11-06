import { NextResponse } from "next/server";
import { resolveSessionPath, openSessionManager, SM_CACHE_LIMITS } from "@/lib/session-reader";
import { getRpcSession } from "@/lib/rpc-manager";
import { searchActiveBranch } from "@/lib/session-branch-search";
import { statSync } from "node:fs";

export const dynamic = "force-dynamic";

/**
 * In-tab search (pi#80): active-branch match list for ONE session, used by
 * the chat tab's search bar to count and navigate matches in EARLIER
 * (unloaded) history. Mirrors the context route's resolution: a live RPC
 * wrapper wins, otherwise the parsed session file (cached manager).
 */
export async function GET(
  req: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params;
  const url = new URL(req.url);
  const query = (url.searchParams.get("q") ?? "").trim();
  const leafId = url.searchParams.get("leafId");
  const caseSensitive = url.searchParams.has("case");
  const regex = url.searchParams.has("regex");
  const headers = { "Cache-Control": "no-store" };

  if (query.length > 200) {
    return NextResponse.json({ error: "Search query exceeds 200 characters" }, { status: 400, headers });
  }
  if (!query) {
    return NextResponse.json({ matches: [], totalMatches: 0, truncated: false }, { headers });
  }

  try {
    const rpc = getRpcSession(id);
    const liveRpc = rpc?.isAlive() ? rpc : undefined;
    const filePath = liveRpc ? null : await resolveSessionPath(id);
    if (!liveRpc && !filePath) {
      return NextResponse.json({ error: "Session not found" }, { status: 404, headers });
    }
    if (filePath) {
      // Oversize sessions are served uncached by the manager; searching one
      // would pin its parsed heap for the cache lifetime, so bound it the
      // same way the cache does.
      try {
        if (statSync(filePath).size > SM_CACHE_LIMITS.maxFileBytes) {
          return NextResponse.json({ error: "Session file too large to search" }, { status: 413, headers });
        }
      } catch {
        return NextResponse.json({ error: "Session file not readable" }, { status: 404, headers });
      }
    }
    const sm = liveRpc?.inner.sessionManager ?? openSessionManager(filePath!);
    const response = searchActiveBranch(sm.getEntries() as never, query, leafId, { caseSensitive, regex });
    return NextResponse.json(response, { headers });
  } catch (error) {
    return NextResponse.json({ error: String(error) }, { status: 500, headers });
  }
}
