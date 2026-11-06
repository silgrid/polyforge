import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import {
  PolyforgeMemoryError,
  isPolyforgeMemoryId,
  readPolyforgeMemoryCapability,
  showPolyforgeMemory,
} from "@/lib/polyforge-memory";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/memory/[id] — the single-memory read:
 * exactly one `pf memory show`, the id shape-validated before any spawn.
 * Reads always answer 200 JSON: pf absent and below the 2.20.12 floor answer
 * the capability fields with `memory: null`, and a failed or refused spawn
 * answers 200 with an `error` field — never 5xx. The only non-2xx answers
 * are the gate's 403 and the malformed-id 400.
 */
export async function GET(
  request: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const { id } = await params;
  if (!isPolyforgeMemoryId(id)) {
    return NextResponse.json({ error: "id must be a memory id" }, { status: 400 });
  }
  const capability = await readPolyforgeMemoryCapability();
  if (!capability.capable) {
    return NextResponse.json({ ...capability, memory: null });
  }
  try {
    const memory = await showPolyforgeMemory(id);
    return NextResponse.json({ ...capability, memory });
  } catch (error) {
    const failure = error instanceof PolyforgeMemoryError ? error : new PolyforgeMemoryError("polyforge is unavailable");
    return NextResponse.json({ ...capability, memory: null, error: failure.message });
  }
}
