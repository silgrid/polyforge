import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import {
  listPolyforgeWorkItems,
  PolyforgeWorkItemsError,
  readPolyforgeWorkItemsCapability,
} from "@/lib/polyforge-work-items";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/work-items — the Work › 工单列表 list read:
 * exactly one merged `pf --json queue` (every `[workspace]`-mapped project, no
 * `--project`/`--status`/`--limit`; pf's own 500-per-project cap), each row
 * through the eight-key passport (id, slug, project, status, priority,
 * bundle_name, goal, created_at; plus `acceptance_missing` only when pf
 * sends `true`,) in pf's order — the client sorts — with
 * `total` pf's total (else the sanitized row count). Read-only: this route
 * exports GET alone, so any other method is 405. Degradation-first and whole:
 * pf absent, below the 2.19.0 floor, a failed project (pf fails the whole
 * verb), no mapping, a timeout or an undecodable answer all answer 200 with
 * `items: []`, `total: 0` and the reason — never a partial table, never 5xx;
 * the only non-2xx answer is the gate's 403.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const capability = await readPolyforgeWorkItemsCapability();
  if (!capability.capable) {
    return NextResponse.json({ ...capability, items: [], total: 0 });
  }
  try {
    const { items, total } = await listPolyforgeWorkItems();
    return NextResponse.json({ ...capability, items, total });
  } catch (error) {
    const failure = error instanceof PolyforgeWorkItemsError ? error : new PolyforgeWorkItemsError("polyforge is unavailable");
    return NextResponse.json({ ...capability, items: [], total: 0, error: failure.message });
  }
}
