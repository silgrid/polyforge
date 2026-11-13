import { NextResponse } from "next/server";
import { jsonResponse } from "@/lib/json-response";
import { isSessionFiltered } from "@/lib/session-filter";
import { listAllSessions } from "@/lib/session-reader";

export const dynamic = "force-dynamic";

/** Caps so the query string can never become a scan amplification lever. */
const MAX_PATTERNS = 50;
const MAX_PATTERN_LENGTH = 500;

/**
 * Server-side full-text matching for the worker-session filter (review r1 on
 * the /api/sessions payload slim-down): the LIST payload now carries only a
 * firstMessage PREVIEW, so a filter pattern whose only match sits beyond the
 * preview boundary can no longer be evaluated client-side. This endpoint
 * evaluates the same rules (case-insensitive substring hit on the stored
 * name OR the FULL first message) against the reader's cached full text and
 * returns the ids that match; the sidebar hides `local match ∪ server match`
 * and the list payload stays small.
 */
export async function GET(req: Request) {
  try {
    const raw = new URL(req.url).searchParams.get("patterns") ?? "[]";
    let patterns: unknown;
    try {
      patterns = JSON.parse(raw);
    } catch {
      return NextResponse.json({ error: "invalidPatterns" }, { status: 400 });
    }
    if (
      !Array.isArray(patterns)
      || patterns.length > MAX_PATTERNS
      || patterns.some((pattern) => typeof pattern !== "string" || pattern.length > MAX_PATTERN_LENGTH)
    ) {
      return NextResponse.json({ error: "invalidPatterns" }, { status: 400 });
    }

    // allowStale: matching is a read-only concern; the reader's cache keeps
    // the FULL first message and a stale scan is at worst one generation old.
    const sessions = await listAllSessions({ allowStale: true });
    const matchedIds = sessions
      .filter((session) => isSessionFiltered(session, patterns as string[]))
      .map((session) => session.id);

    return jsonResponse(req, { matchedIds }, { headers: { "Cache-Control": "no-store" } });
  } catch (error) {
    return NextResponse.json(
      { error: String(error) },
      { status: 500, headers: { "Cache-Control": "no-store" } },
    );
  }
}
