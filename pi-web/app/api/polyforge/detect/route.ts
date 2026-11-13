import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { detectPolyforge } from "@/lib/polyforge-detect";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/detect[?force=1] — the Settings Polyforge section's
 * machine-level detection (pi#122): flat booleans/labels only
 * (`lib/polyforge-detect.ts`), the presence probe shared with the dock —
 * `probePolyforgeVersion` in `lib/polyforge-status.ts`, the one
 * `pf --version` spawn site — so there is one detection implementation,
 * never a second. Degradation-first like the panel's other routes:
 * every absent/degraded state answers 200 decodable JSON — pf absent included,
 * with no error field, so the pf-less path trips no browser console error —
 * and a top-level catch answers 200 with the absent states. No 5xx path
 * exists; the only non-2xx is the in-route `isApiRequestAllowed` 403 JSON.
 * `?force=1` bypasses the 60s presence cache read (the 「重新检测」 control).
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const force = new URL(request.url).searchParams.get("force") === "1";
  try {
    return NextResponse.json(await detectPolyforge(force));
  } catch {
    // Every helper degrades on its own; this catch only guards the assembly
    // itself — absent states, never an error surface, never 5xx.
    return NextResponse.json({
      pfPresent: false,
      pfVersion: null,
      pfVersionBelowMinimum: false,
      line: "unknown",
      piSkillInstalled: false,
      piSkillEnabled: false,
      claudeSkillsSymlink: false,
      claudeSessionStartHook: false,
      daemon: null,
    });
  }
}