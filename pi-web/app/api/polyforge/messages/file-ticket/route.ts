import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import {
  PolyforgeMessagesError,
  createPolyforgeFileTicket,
  messageErrorStatus,
  readPolyforgeMappedProjects,
  readPolyforgeMessagesCapability,
  validateFileTicketInput,
} from "@/lib/polyforge-messages";

export const dynamic = "force-dynamic";

/**
 * POST /api/polyforge/messages/file-ticket — the analysis card's 【建票】
 * confirm. Body `{goal, project, content}`.
 *
 * Reach: whoever can already reach pi-web's authenticated surface — the same
 * in-route gate as the existing messaging writes (403 otherwise). It holds no
 * aihub credential, proxies no aihub route, writes no config file and logs
 * no request or response body.
 *
 * Order: the gate (403), JSON-only (415), an unparsable body (400), pf below
 * the Messages floor (400 with its reason), then the body against the
 * current `cli.toml` mapping (400: a blank/multi-line goal or one over 500
 * characters, an unmapped project, a blank or over-64 KiB content, a content
 * pf would read as a file or stdin) — all before the one write spawn:
 * exactly one `pf --json file create --project <p> --goal <g> --content <c>`
 * without `--bundle` (a registration ticket). Success is `{ok: true, slug}`;
 * pf's typed refusal keeps its 4xx with pf's text; DAEMON-500 or an
 * unreachable pf answers 200 `{error}` — never 5xx, never retried.
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
    const validated = validateFileTicketInput(body, await readPolyforgeMappedProjects());
    if (!validated.ok) {
      return NextResponse.json({ error: validated.error }, { status: 400 });
    }
    const created = await createPolyforgeFileTicket(validated.value);
    return NextResponse.json({ ok: true, slug: created.slug });
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
