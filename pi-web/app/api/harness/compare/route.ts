// GET /api/harness/compare?cwd=&field= — the read-back comparison
// (polyforge#85 85a1): every compared field across the harnesses, or one
// field when ?field= names it. Behind the API guard; the cwd goes through the
// file-access check.
import { NextResponse } from "next/server";
import { compareAll, compareField, COMPARE_FIELDS, type CompareField } from "@/lib/harness/compare";
import { harnessRefusalBody, harnessRouteContext } from "@/lib/harness/route-context";
import { isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";

export async function GET(req: Request) {
  if (!isApiRequestAllowed(req)) {
    return NextResponse.json({ error: "request-denied", message: "Untrusted API request" }, { status: 403 });
  }
  // stub (85a1 u5): zero-value body, the red run's target
  void compareAll;
  void compareField;
  void COMPARE_FIELDS;
  void harnessRefusalBody;
  void harnessRouteContext;
  return NextResponse.json({});
}
