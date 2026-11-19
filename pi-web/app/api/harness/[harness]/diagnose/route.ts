// GET /api/harness/[harness]/diagnose?cwd= — one harness's native
// diagnostics (polyforge#149, #85 85a1b, AC6): the adapter's fixed safe
// command executed with a deadline and through the credential filter, as a
// collapsible block of the 生效值与来源 pane. Behind the API guard like
// every /api route; the cwd goes through the file-access check; reads only —
// the one command it can run is the harness's own declared diagnostic.
import { NextResponse } from "next/server";
import { ensureDefaultHarnesses } from "@/lib/harness/adapters";
import { harnessRefusalBody, harnessRouteContext } from "@/lib/harness/route-context";
import { isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";

export async function GET(
  req: Request,
  { params }: { params: Promise<{ harness: string }> },
) {
  if (!isApiRequestAllowed(req)) {
    return NextResponse.json({ error: "request-denied", message: "Untrusted API request" }, { status: 403 });
  }
  const { harness } = await params;
  const adapter = ensureDefaultHarnesses().find((candidate) => candidate.id === harness);
  if (!adapter) {
    return NextResponse.json({ error: "unknown-harness", message: `no adapter for ${harness}` }, { status: 404 });
  }
  const context = await harnessRouteContext(req.url);
  if ("status" in context) {
    return NextResponse.json(harnessRefusalBody(context), { status: context.status });
  }
  try {
    const diagnostics = await adapter.diagnose?.(context.ctx)
      ?? { available: false as const, reason: `${harness} declares no native diagnostics` };
    return NextResponse.json({ harness, diagnostics });
  } catch (error) {
    return NextResponse.json(
      { error: "internal", message: error instanceof Error ? error.message : String(error) },
      { status: 500 },
    );
  }
}
