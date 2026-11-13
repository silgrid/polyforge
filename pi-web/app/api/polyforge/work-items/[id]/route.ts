import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import {
  isPolyforgeWorkItemId,
  PolyforgeWorkItemsError,
  readPolyforgeWorkItemsCapability,
  showPolyforgeWorkItem,
} from "@/lib/polyforge-work-items";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/work-items/[id] — one work item's read-only detail
 *: exactly one `pf --json file show <id>`, the id (`wi_` +
 * alphanumerics) validated before any probe or spawn, the body through the
 * eighteen-key passport with the content capped at 262144 UTF-8 bytes and
 * `content_len` uncut. Read-only: GET alone, any other method is 405. pf
 * absent and below the 2.19.0 floor answer the capability fields with
 * `item: null`; a failed spawn answers 200 with `item: null` and pf's bounded
 * reason — never 5xx. The only non-2xx answers are the gate's 403 and the
 * malformed-id 400.
 */
export async function GET(
  request: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const { id } = await params;
  if (!isPolyforgeWorkItemId(id)) {
    return NextResponse.json({ error: "id must be a work item id" }, { status: 400 });
  }
  const capability = await readPolyforgeWorkItemsCapability();
  if (!capability.capable) {
    return NextResponse.json({ ...capability, item: null });
  }
  try {
    const item = await showPolyforgeWorkItem(id);
    return NextResponse.json({ ...capability, item });
  } catch (error) {
    const failure = error instanceof PolyforgeWorkItemsError ? error : new PolyforgeWorkItemsError("polyforge is unavailable");
    return NextResponse.json({ ...capability, item: null, error: failure.message });
  }
}
