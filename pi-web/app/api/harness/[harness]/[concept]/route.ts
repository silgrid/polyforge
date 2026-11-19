// GET /api/harness/[harness]/[concept]?cwd= — one concept's read (polyforge#85
// 85a1): the harness's per-concept payload, built through the redaction
// allow-list (no credential value ever reaches the response). Behind the API
// guard; the cwd goes through the file-access check.
import { NextResponse } from "next/server";
import { ensureDefaultHarnesses } from "@/lib/harness/adapters";
import { harnessRefusalBody, harnessRouteContext } from "@/lib/harness/route-context";
import { isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";

export async function GET(
  req: Request,
  { params }: { params: Promise<{ harness: string; concept: string }> },
) {
  if (!isApiRequestAllowed(req)) {
    return NextResponse.json({ error: "request-denied", message: "Untrusted API request" }, { status: 403 });
  }
  // stub (85a1 u5): zero-value body, the red run's target
  void params;
  void ensureDefaultHarnesses;
  void harnessRefusalBody;
  void harnessRouteContext;
  return NextResponse.json({});
}
