import { NextResponse } from "next/server";
import {
  ModelsConfigReadError,
  readModelsConfig,
  validateModelsConfigPayload,
  writeModelsConfig,
} from "@/lib/models-config-store";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import { ContextWindowError, normalizeContextWindowOverrides } from "@/lib/model-context";
import { checkProviderEntries, ProviderEntryError } from "@/lib/model-provider-check";
import { checkImageMaxBytes, ImageMaxBytesError } from "@/lib/model-image-limits";

export const dynamic = "force-dynamic";

export async function GET() {
  try {
    return NextResponse.json(readModelsConfig());
  } catch (error) {
    if (error instanceof ModelsConfigReadError) {
      return NextResponse.json({ error: error.message }, { status: 422 });
    }
    return NextResponse.json({ error: String(error) }, { status: 500 });
  }
}

export async function PUT(req: Request) {
  if (!isApiRequestAllowed(req)) {
    return NextResponse.json({ error: "Untrusted API request" }, { status: 403 });
  }
  if (!hasJsonContentType(req)) {
    return NextResponse.json({ error: "Content-Type must be application/json" }, { status: 415 });
  }
  try {
    const body: unknown = await req.json().catch(() => null);
    // Context-window overrides are normalized first (a numeric string becomes a
    // number; an invalid one throws ContextWindowError), then the payload is
    // schema-gated (audit S8) instead of writing an unrestricted record.
    const normalized = typeof body === "object" && body !== null && !Array.isArray(body)
      ? normalizeContextWindowOverrides(body as Record<string, unknown>)
      : body;
    const validated = validateModelsConfigPayload(normalized);
    if (!validated.ok) {
      return NextResponse.json({ error: validated.reason }, { status: 400 });
    }
    checkProviderEntries(validated.data);
    checkImageMaxBytes(validated.data);
    writeModelsConfig(validated.data);
    return NextResponse.json({ success: true });
  } catch (error) {
    if (error instanceof ContextWindowError || error instanceof ProviderEntryError || error instanceof ImageMaxBytesError) {
      return NextResponse.json({ error: error.message }, { status: 400 });
    }
    if (error instanceof ModelsConfigReadError) {
      return NextResponse.json({ error: error.message }, { status: 409 });
    }
    return NextResponse.json({ error: String(error) }, { status: 500 });
  }
}
