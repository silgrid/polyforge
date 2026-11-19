// GET /api/harness/[harness]/diagnose?cwd= — one harness's native
// diagnostics (polyforge#149, #85 85a1b, AC6): the adapter's fixed safe
// command executed with a deadline and through the credential filter, as a
// collapsible block of the 生效值与来源 pane. Behind the API guard; the cwd
// goes through the file-access check. Placeholder body; the route's unit
// replaces this stub.
import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";

export async function GET() {
  return NextResponse.json({ error: "not-implemented" }, { status: 501 });
}
