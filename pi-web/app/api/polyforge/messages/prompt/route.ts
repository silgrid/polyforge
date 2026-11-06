import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import {
  isPolyforgeMessageId,
  PolyforgeMessagesError,
  messageErrorStatus,
  promptPolyforgeMessage,
  readPolyforgePromptCapability,
} from "@/lib/polyforge-messages";

export const dynamic = "force-dynamic";

/**
 * POST /api/polyforge/messages/prompt — the Messages 「@」 action:
 * pf turns one message into an execution prompt with one read-only task, and
 * the call blocks until that task ends.
 * Reach: pi-web's authenticated surface only (the in-route gate below).
 * JSON-only; the message id is validated
 * before any spawn, and pf below the prompt floor
 * (POLYFORGE_PROMPT_MINIMUM_PF_VERSION) answers 400 `{error, upgrade}` before
 * any spawn. Then exactly one `pf --json msg prompt <id>`; the answer is
 * `{ok, prompt}` with the sanitized four fields (prompt, task_id, model,
 * duration). The prompt exists only in this response: nothing is written,
 * persisted or logged, and the panel fills it into a composer without ever
 * sending it. pf's typed refusal keeps its 4xx (DAEMON-409 slot-full,
 * DAEMON-400 no task chain, DAEMON-412 unmapped, AUTH-403/CORE-404 an
 * unreadable message); DAEMON-500 (a failed/capped/interrupted task or a
 * blank report) and an unreachable pf answer 200 `{error}` — never 5xx.
 */
export async function POST(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ error: "Content-Type must be application/json" }, { status: 415 });
  }
  let body: { message_id?: unknown };
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  if (!isPolyforgeMessageId(body.message_id)) {
    return NextResponse.json({ error: "message_id must be a message id" }, { status: 400 });
  }
  const capability = await readPolyforgePromptCapability();
  if (!capability.capable) {
    return NextResponse.json(
      { error: capability.reason ?? "polyforge is unavailable", upgrade: capability.pfPresent },
      { status: 400 },
    );
  }
  try {
    return NextResponse.json({ ok: true, prompt: await promptPolyforgeMessage(body.message_id) });
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
