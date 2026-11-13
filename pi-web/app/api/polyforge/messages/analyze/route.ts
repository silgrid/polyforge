import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import {
  analyzePolyforgeMessage,
  isPolyforgeMessageId,
  PolyforgeMessagesError,
  messageErrorStatus,
  readPolyforgeAnalysisCapability,
} from "@/lib/polyforge-messages";

export const dynamic = "force-dynamic";

/**
 * POST /api/polyforge/messages/analyze — the structured analysis of one
 * received message: pf runs it as one read-only task and the
 * call blocks until that task ends. JSON-only; the message id is validated
 * before any spawn, and pf below the analysis floor (2.20.18) answers 400
 * `{error, upgrade: true}` before any spawn — the Messages tab's own floor is
 * unaffected. Then exactly one `pf --json msg analyze <id>`; the answer is
 * the sanitized structured analysis `{ok, analysis}` (the task id, the
 * closed-set type and actions, the summary, model, duration, the project and
 * an optional reply draft — nothing else, the retired `conclusion` never).
 * It exists only in this response: nothing is written, persisted or logged.
 * pf's typed refusal keeps its 4xx (DAEMON-409 slot-full, DAEMON-400 no task
 * chain, DAEMON-412 unmapped); DAEMON-500 (a failed/capped/interrupted task)
 * and an unreachable pf answer 200 `{error}` — never 5xx.
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
  const capability = await readPolyforgeAnalysisCapability();
  if (!capability.capable) {
    return NextResponse.json(
      { error: capability.reason ?? "polyforge is unavailable", upgrade: capability.pfPresent },
      { status: 400 },
    );
  }
  try {
    return NextResponse.json({ ok: true, analysis: await analyzePolyforgeMessage(body.message_id) });
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
