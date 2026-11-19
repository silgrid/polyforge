// GET /api/harness — the harness list (polyforge#85 85a1): every registered
// adapter with its probed version, whether the write gate knows that version
// (是否可写: a known version is what the gate admits; no concept write is
// implemented yet), and its capability declaration. Behind the API guard like
// every /api route; reads only.
import { NextResponse } from "next/server";
import { ensureDefaultHarnesses } from "@/lib/harness/adapters";
import { isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";

export async function GET(req: Request) {
  if (!isApiRequestAllowed(req)) {
    return NextResponse.json({ error: "request-denied", message: "Untrusted API request" }, { status: 403 });
  }
  // stub (85a1 u5): zero-value body, the red run's target
  void ensureDefaultHarnesses;
  return NextResponse.json({});
}
