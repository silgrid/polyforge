import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import {
  PolyforgeInstallError,
  polyforgeInstallRun,
  readPolyforgeInstallVersions,
  startPolyforgeInstall,
  type PolyforgeInstallRequest,
} from "@/lib/polyforge-install";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

/**
 * GET /api/polyforge/install[?force=1] — Settings › Polyforge's install
 * entry: the installed, latest (release channel) and running
 * daemon versions, the jobs an upgrade would be refused for, Pi Web's own
 * version, whether a first install needs the key and host, and the current
 * or last run (`run`, null before the first). `force` bypasses the presence
 * and latest-release caches. Degradation as fields; never 5xx.
 *
 * POST /api/polyforge/install {force?, refusedRun?, apiKey?, host?} — start one run of
 * the channel's tools/install.sh (one at a time: 409 while one runs). A
 * first install needs `host` and `apiKey`; the key reaches install.sh on
 * stdin only and is never echoed, logged or returned. `force` (install.sh's
 * PF_INSTALL_FORCE=1) is accepted only with `refusedRun` naming the run
 * install.sh refused, while that is the last run and at most 10 minutes old
 * (409 otherwise). The run outlives the request; its output streams
 * from GET /api/polyforge/install/events. Reach: pi-web's authenticated
 * surface (the proxy's login, plus the in-route gate); JSON-only.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const force = new URL(request.url).searchParams.get("force") === "1";
  try {
    return NextResponse.json({ versions: await readPolyforgeInstallVersions(force), run: polyforgeInstallRun() });
  } catch (error) {
    return NextResponse.json({ versions: null, run: polyforgeInstallRun(), error: error instanceof Error ? error.message : "polyforge is unavailable" });
  }
}

export async function POST(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ error: "Content-Type must be application/json" }, { status: 415 });
  }
  let body: PolyforgeInstallRequest;
  try {
    body = (await request.json()) as PolyforgeInstallRequest;
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  if (!body || typeof body !== "object") {
    return NextResponse.json({ error: "the body must be a JSON object" }, { status: 400 });
  }
  try {
    return NextResponse.json({ ok: true, run: await startPolyforgeInstall(body) }, { status: 202 });
  } catch (error) {
    if (error instanceof PolyforgeInstallError) {
      return NextResponse.json({ error: error.message }, { status: error.status });
    }
    return NextResponse.json({ error: "the installer could not start" });
  }
}
