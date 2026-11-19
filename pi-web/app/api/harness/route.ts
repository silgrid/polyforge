// GET /api/harness — the harness list (polyforge#85 85a1): every registered
// adapter with its probed version, whether the write gate knows that version
// (是否可写: a known version is what the gate admits; no concept write is
// implemented yet), and its capability declaration. Behind the API guard like
// every /api route; reads only, never a credential value.
import { NextResponse } from "next/server";
import { ensureDefaultHarnesses } from "@/lib/harness/adapters";
import { isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";

export async function GET(req: Request) {
  if (!isApiRequestAllowed(req)) {
    return NextResponse.json({ error: "request-denied", message: "Untrusted API request" }, { status: 403 });
  }
  try {
    const harnesses = [];
    for (const adapter of ensureDefaultHarnesses()) {
      const probe = await adapter.probeVersion();
      harnesses.push({
        id: adapter.id,
        version: probe.version,
        versionKnown: probe.known,
        knownRange: probe.knownRange ?? null,
        versionReason: probe.reason ?? null,
        writable: probe.known,
        capabilities: adapter.capabilities,
      });
    }
    return NextResponse.json({ harnesses });
  } catch (error) {
    return NextResponse.json(
      { error: "internal", message: error instanceof Error ? error.message : String(error) },
      { status: 500 },
    );
  }
}
