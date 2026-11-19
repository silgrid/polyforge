// GET /api/harness/compare?cwd=&field= — the read-back comparison
// (polyforge#85 85a1): every compared field across the harnesses, or one
// field when ?field= names it. Behind the API guard; the cwd goes through
// the file-access check.
import { NextResponse } from "next/server";
import { compareAll, compareField, COMPARE_FIELDS, type CompareField } from "@/lib/harness/compare";
import { harnessRefusalBody, harnessRouteContext } from "@/lib/harness/route-context";
import { isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";

export async function GET(req: Request) {
  if (!isApiRequestAllowed(req)) {
    return NextResponse.json({ error: "request-denied", message: "Untrusted API request" }, { status: 403 });
  }
  const field = new URL(req.url).searchParams.get("field");
  if (field !== null && !(COMPARE_FIELDS as readonly string[]).includes(field)) {
    return NextResponse.json({ error: "unknown-field", message: `field must be one of ${COMPARE_FIELDS.join(", ")}` }, { status: 400 });
  }
  const context = await harnessRouteContext(req.url);
  if ("status" in context) {
    return NextResponse.json(harnessRefusalBody(context), { status: context.status });
  }
  try {
    const results = field === null
      ? await compareAll(context.ctx)
      : [await compareField(field as CompareField, context.ctx)];
    return NextResponse.json({ results });
  } catch (error) {
    return NextResponse.json(
      { error: "internal", message: error instanceof Error ? error.message : String(error) },
      { status: 500 },
    );
  }
}
