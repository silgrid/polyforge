import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import {
  acknowledgePolyforgeMessage,
  isPolyforgeConversationId,
  isPositiveSeq,
  PolyforgeMessagesError,
  messageErrorStatus,
  readPolyforgeMessagesCapability,
} from "@/lib/polyforge-messages";

export const dynamic = "force-dynamic";

/**
 * POST /api/polyforge/messages/ack — advance the caller's read cursor to
 * `{conversation_id, seq}`. JSON-only; the conversation id and a positive
 * sequence are validated before any spawn, and an invalid value is a 400.
 * pf's typed refusal (past-the-newest, unknown conversation, non-member) is
 * its own 4xx; an unavailable pf answers 200 `{error}`. Never 5xx.
 */
export async function POST(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ error: "Content-Type must be application/json" }, { status: 415 });
  }
  let body: { conversation_id?: unknown; seq?: unknown };
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  if (!isPolyforgeConversationId(body.conversation_id)) {
    return NextResponse.json({ error: "conversation_id must be a conversation id" }, { status: 400 });
  }
  if (!isPositiveSeq(body.seq)) {
    return NextResponse.json({ error: "seq must be a positive sequence" }, { status: 400 });
  }
  const capability = await readPolyforgeMessagesCapability();
  if (!capability.capable) {
    return NextResponse.json(
      { error: capability.reason ?? "polyforge messages are unavailable" },
      { status: 400 },
    );
  }
  try {
    await acknowledgePolyforgeMessage(body.conversation_id, body.seq);
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
