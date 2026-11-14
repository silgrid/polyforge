import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import { resolvePolyforgeCellScope } from "@/lib/polyforge-cells";
import { isPolyforgeProjectToken, readPolyforgeProjects, restorePolyforgeProject } from "@/lib/polyforge-projects";
import { PolyforgeMessagesError } from "@/lib/polyforge-messages";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/projects — the Work › projects view's data: the
 * mapped projects with their global states, `{pfPresent, projects:
 * [{project, status, error?}], error?}`. The mapped projects come from one
 * `pf --json config list`; each project's state from one bounded
 * `pf --json projects get <p>` (status only — no other field reaches the
 * browser). The view hides the deactivated ones by default; the route
 * answers every mapped row's state and lets the client decide. Reachable
 * only through the authenticated surface: the in-route gate answers 403;
 * a failed read degrades to the read's own error, never a 5xx. Reach:
 * pi-web's authenticated surface only — a step agent has no browser
 * session and cannot reach it, and the route itself answers no
 * credential.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  try {
    return NextResponse.json(await readPolyforgeProjects((await resolvePolyforgeCellScope()).pin.prefix));
  } catch {
    return NextResponse.json({ pfPresent: true, projects: [], error: "the projects read failed; polyforge may be unavailable" });
  }
}

/**
 * POST /api/polyforge/projects { project, action } — the view's one write:
 * `action: "restore"` moves a deactivated project back to active through
 * exactly one `pf --json projects restore <p>` (the reversible door; only
 * the hard delete removes data, and it stays on the CLI). The project is
 * validated before any spawn (400); the JSON content type (415) and the
 * in-route gate (403) like the other polyforge writes; pf's typed refusal
 * is the action's 409 error. Never 5xx.
 */
export async function POST(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ ok: false, error: "Forbidden" }, { status: 403 });
  }
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ ok: false, error: "Content-Type must be application/json" }, { status: 415 });
  }
  let body: { project?: unknown; action?: unknown };
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ ok: false, error: "invalid JSON body" }, { status: 400 });
  }
  if (!isPolyforgeProjectToken(body.project)) {
    return NextResponse.json({ ok: false, error: "project must match [A-Za-z0-9][A-Za-z0-9_.-]{0,127}" }, { status: 400 });
  }
  if (body.action !== "restore") {
    return NextResponse.json({ ok: false, error: "action must be \"restore\"" }, { status: 400 });
  }
  try {
    await restorePolyforgeProject(body.project, (await resolvePolyforgeCellScope()).pin.prefix);
    return NextResponse.json({ ok: true, project: body.project, status: "active" });
  } catch (error) {
    const failure = error instanceof PolyforgeMessagesError ? error : null;
    return NextResponse.json(
      { ok: false, error: failure ? failure.message : "polyforge is unavailable" },
      { status: 409 },
    );
  }
}
