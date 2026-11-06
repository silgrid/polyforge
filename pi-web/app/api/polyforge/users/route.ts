import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { readPolyforgeUsers } from "@/lib/polyforge-messages";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/users — the Messages panel's admin probe and membership
 * picker data: `{capable, pfPresent, pfVersion, admin,
 * users: [{id, display_name}], reason?, error?}`. Only human users and only
 * their id + display name reach the browser (no email, role, user_type or
 * api_tokens). `admin` is true only when `pf users list` (admin-only)
 * answered; any refusal, an unreachable pf, pf absent or below the 2.20.12
 * floor answers 200 with `admin: false` (below the floor nothing is
 * spawned). Reachable only through the authenticated surface: the in-route
 * gate answers 403; nothing else is a non-200.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  try {
    return NextResponse.json(await readPolyforgeUsers());
  } catch {
    return NextResponse.json({
      pfPresent: true,
      pfVersion: null,
      capable: true,
      admin: false,
      users: [],
      error: "the users read failed; polyforge may be unavailable",
    });
  }
}
