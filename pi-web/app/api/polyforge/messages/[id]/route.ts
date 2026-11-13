import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import {
  isPolyforgeConversationId,
  PolyforgeMessagesError,
  readPolyforgeMessagesCapability,
  readPolyforgeMessagesPage,
  readPolyforgeNewestPage,
  readPolyforgeNewerMessages,
} from "@/lib/polyforge-messages";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/messages/[id][?before=<seq>] — one conversation's
 * history page. Without `before` the newest page is returned (positioned at
 * the bottom by the client); scroll-up passes the oldest loaded sequence.
 * Reads always answer 200 JSON: a bad id or a bad cursor is a 4xx field-level
 * refusal, pf's typed failure is its own 4xx, and unavailable pf answers
 * 200 `{messages: [], last_seq: 0, error}`. There is no 5xx path.
 */
export async function GET(
  request: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const { id } = await params;
  if (!isPolyforgeConversationId(id)) {
    return NextResponse.json({ error: "conversation must be a conversation id" }, { status: 400 });
  }
  const url = new URL(request.url);
  const rawBefore = url.searchParams.get("before");
  const rawAfter = url.searchParams.get("after");
  let before = 0;
  if (rawBefore !== null && rawBefore !== "") {
    before = Number(rawBefore);
    if (!Number.isSafeInteger(before) || before <= 0) {
      return NextResponse.json({ error: "before must be a positive sequence" }, { status: 400 });
    }
  }
  let after = 0;
  if (rawAfter !== null && rawAfter !== "") {
    after = Number(rawAfter);
    if (!Number.isSafeInteger(after) || after <= 0) {
      return NextResponse.json({ error: "after must be a positive sequence" }, { status: 400 });
    }
  }
  const capability = await readPolyforgeMessagesCapability();
  if (!capability.capable) {
    return NextResponse.json({
      pfPresent: capability.pfPresent,
      pfVersion: capability.pfVersion,
      capable: false,
      reason: capability.reason,
      messages: [],
      last_seq: 0,
    });
  }
  try {
    // `?after=` is the open conversation's 3 s refresh: one forward read,
    // never a second backward page (scroll-up owns `?before=`).
    const page = after > 0
      ? { messages: await readPolyforgeNewerMessages(id, after), last_seq: 0 }
      : before > 0
        ? await readPolyforgeMessagesPage(id, before)
        : await readPolyforgeNewestPage(id);
    return NextResponse.json({ capable: true, ...page });
  } catch (error) {
    const failure = error instanceof PolyforgeMessagesError
      ? error
      : new PolyforgeMessagesError("polyforge is unavailable");
    // Reads degrade to 200: pf's typed error is a field, never a non-2xx
    // read response (only the write routes map a code to a 4xx).
    return NextResponse.json(
      { error: failure.message, messages: [], last_seq: 0 },
    );
  }
}
