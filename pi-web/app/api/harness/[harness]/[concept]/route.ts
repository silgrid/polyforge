// GET /api/harness/[harness]/[concept]?cwd= — one concept's read
// (polyforge#85 85a1): the harness's per-concept payload, built through the
// redaction allow-list (no credential value ever reaches the response).
// PUT /api/harness/[harness]/[concept]?cwd= (polyforge#150 85a4) — the one
// write path of the harness layer: the change goes to the adapter's write(),
// which passes the shared version gate first. Behind the API guard; a ?cwd=
// goes through the file-access check; the write only ever touches what the
// adapter declares writable (the permission concept: claude's native
// user-level settings.json / state file, pi's trust.json).
import { NextResponse } from "next/server";
import type { WriteChange } from "@/lib/harness/adapter";
import type { ConceptId } from "@/lib/harness/concepts";
import { ensureDefaultHarnesses } from "@/lib/harness/adapters";
import { harnessRefusalBody, harnessRouteContext } from "@/lib/harness/route-context";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";

function failure(status: number, error: string, message: string) {
  return NextResponse.json({ error, message }, { status });
}

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

export async function PUT(
  req: Request,
  { params }: { params: Promise<{ harness: string; concept: string }> },
) {
  if (!isApiRequestAllowed(req)) {
    return NextResponse.json({ error: "request-denied", message: "Untrusted API request" }, { status: 403 });
  }
  const { harness, concept } = await params;
  const adapter = ensureDefaultHarnesses().find((candidate) => candidate.id === harness);
  if (!adapter) {
    return failure(404, "unknown-harness", `no adapter for ${harness}`);
  }
  if (!adapter.capabilities.concepts.includes(concept as ConceptId)) {
    return failure(404, "unknown-concept", `${harness} has no concept ${concept}`);
  }
  if (!adapter.capabilities.writableConcepts.includes(concept as ConceptId)) {
    return failure(400, "not-writable", `${harness} declares ${concept} read-only`);
  }
  if (!hasJsonContentType(req)) {
    return failure(415, "not-json", "Content-Type must be application/json");
  }
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return failure(400, "invalid-json", "Invalid JSON body");
  }
  if (typeof body !== "object" || body === null || Array.isArray(body)) {
    return failure(400, "invalid-body", "Expected a JSON object");
  }
  const context = await harnessRouteContext(req.url);
  if ("status" in context) {
    return NextResponse.json(harnessRefusalBody(context), { status: context.status });
  }
  try {
    const result = await adapter.write(concept as ConceptId, body as WriteChange, context.ctx);
    if (result.status === "written") {
      return NextResponse.json({ harness, concept, result });
    }
    // A refusal (the version gate, the change's shape, a native file the
    // adapter will not touch) is the client's to see; a concept known but
    // still without a write stays behind the writableConcepts check above.
    return failure(400, result.status === "refused" ? "write-refused" : "not-implemented", result.reason);
  } catch (error) {
    return NextResponse.json(
      { error: "internal", message: error instanceof Error ? error.message : String(error) },
      { status: 500 },
    );
  }
}
