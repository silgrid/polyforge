import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import {
  PolyforgeMessagesError,
  changePolyforgeMembership,
  messageErrorStatus,
  readPolyforgeMappedProjects,
  readPolyforgeMessagesCapability,
  validateMembershipInput,
} from "@/lib/polyforge-messages";

export const dynamic = "force-dynamic";

/**
 * POST /api/polyforge/projects/members — add or remove one human user in one
 * `cli.toml`-mapped project from the Messages panel. Body
 * `{action: "add" | "remove", project, user_id}`. Reachable only through the
 * authenticated surface (in-route gate, 403) and JSON-only (415); an invalid
 * body, a project outside the current mapping or an invalid user id is 400,
 * and pf below the 2.20.12 floor is 400 with its reason — all before any
 * membership spawn. Then exactly one `pf --json projects members add <p>
 * --user <id> --role writer` or `... remove <p> --user <id>`. Success is
 * `{ok: true}`; a typed pf refusal is its 4xx with pf's text; DAEMON-500 or
 * an unreachable pf is 200 `{error}` (no 5xx). Nothing is logged.
 */
export async function POST(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ error: "Content-Type must be application/json" }, { status: 415 });
  }
  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  const capability = await readPolyforgeMessagesCapability();
  if (!capability.capable) {
    return NextResponse.json(
      { error: capability.reason ?? "polyforge messages are unavailable" },
      { status: 400 },
    );
  }
  try {
    const validated = validateMembershipInput(body, await readPolyforgeMappedProjects());
    if (!validated.ok) {
      return NextResponse.json({ error: validated.error }, { status: 400 });
    }
    await changePolyforgeMembership(validated.value);
    return NextResponse.json({ ok: true });
  } catch (error) {
    const failure = error instanceof PolyforgeMessagesError
      ? error
      : new PolyforgeMessagesError("polyforge is unavailable");
    return NextResponse.json(
      { error: failure.message },
      { status: messageErrorStatus(failure.code) },
    );
  }
}
