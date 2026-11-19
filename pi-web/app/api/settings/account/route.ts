// The account route (polyforge#142, 85a2 AC1/AC6) — placeholder: the
// test-first commit carries only the route's shape so the suite's
// assertions run red; the implementation lands with the unit.
import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function GET(req: Request) {
  if (!isApiRequestAllowed(req)) {
    return NextResponse.json({ error: "request-denied", message: "Untrusted API request" }, { status: 403 });
  }
  return NextResponse.json({ claude: null, cells: [] });
}

export async function POST(req: Request) {
  if (!isApiRequestAllowed(req)) {
    return NextResponse.json({ error: "request-denied", message: "Untrusted API request" }, { status: 403 });
  }
  return NextResponse.json({ error: "not-implemented" }, { status: 501 });
}
