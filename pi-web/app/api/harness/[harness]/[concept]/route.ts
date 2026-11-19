// GET /api/harness/[harness]/[concept]?cwd= — one concept's read
// (polyforge#85 85a1): the harness's per-concept payload, built through the
// redaction allow-list (no credential value ever reaches the response).
// Behind the API guard; the cwd goes through the file-access check.
import { NextResponse } from "next/server";
import { ensureDefaultHarnesses } from "@/lib/harness/adapters";
import type { ConceptId } from "@/lib/harness/concepts";
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
  const { harness, concept } = await params;
  const adapter = ensureDefaultHarnesses().find((candidate) => candidate.id === harness);
  if (!adapter) {
    return NextResponse.json({ error: "unknown-harness", message: `no adapter for ${harness}` }, { status: 404 });
  }
  if (!adapter.capabilities.concepts.includes(concept as ConceptId)) {
    return NextResponse.json({ error: "unknown-concept", message: `${harness} has no concept ${concept}` }, { status: 404 });
  }
  const context = await harnessRouteContext(req.url);
  if ("status" in context) {
    return NextResponse.json(harnessRefusalBody(context), { status: context.status });
  }
  try {
    const data = await adapter.read(concept as ConceptId, context.ctx);
    return NextResponse.json({ harness, concept, data });
  } catch (error) {
    return NextResponse.json(
      { error: "internal", message: error instanceof Error ? error.message : String(error) },
      { status: 500 },
    );
  }
}
