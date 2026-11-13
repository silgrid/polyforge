import { NextResponse } from "next/server";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import { isPolyforgeProfileName } from "@/lib/polyforge-cells-pure";
import { isPolyforgeWorkItemId } from "@/lib/polyforge-cards-pure";
import { PolyforgeError, sendPolyforgeOwnerMessage } from "@/lib/polyforge-status";
import { OWNER_MESSAGE_TEXT_MAX } from "@/lib/polyforge-conversation-pure";

export const dynamic = "force-dynamic";

/**
 * POST /api/polyforge/answer { wi, profile?, text } — the job card's one
 * input box (workflow contract v2 §5.2 and §5.4, change 14; workflow
 * contract 4, design #483 §7,): the owner's words to the work
 * item's current step agent through the owner message relay, or — the work
 * item parked (blocked or paused) — the wake of the step the park belongs
 * to: `pf [--profile p] --json answer <wi> --text=<words>`, the same command
 * a human types, execFile, no shell, no question id (the daemon tells a
 * reply to an open `pf step ask` from an interjection into a running turn, and
 * classifies a park; pf opens the park's door with the caller's own key).
 * The wi (`wi_…`), the profile and the text bound are validated before any
 * spawn (400); the in-route host / origin gate (403) and the JSON content
 * type (415) as the other polyforge writes. `{ ok: true, ack }` carries the
 * daemon's acknowledgement (the recorded `at`, reply or message, delivered
 * or the delivery's disposition — `wake` for a woken park — see
 * `parseOwnerMessageAckWire`); pf's refusal is 409 `{ ok: false, error,
 * reason? }`: its typed envelope as `<code>: <detail>` (a terminal or
 * not-yet-dispatched work item, a park whose door the caller may not open,
 * a disabled Cell, the daemon down) with the envelope's `reason` when it
 * names one — `WAKE_CANNOT_START`: the park's agent cannot start, nothing
 * was recorded, the card shows why with `pf resume` / `pf cancel`. Never
 * 5xx. Reach: pi-web's authenticated surface only — a step agent has no
 * browser session and cannot reach it.
 */
export async function POST(request: Request) {
  if (!isApiRequestAllowed(request)) return NextResponse.json({ ok: false, error: "Forbidden" }, { status: 403 });
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ ok: false, error: "Content-Type must be application/json" }, { status: 415 });
  }
  let body: { wi?: unknown; profile?: unknown; text?: unknown };
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ ok: false, error: "invalid JSON body" }, { status: 400 });
  }
  if (!isPolyforgeWorkItemId(body.wi)) return NextResponse.json({ ok: false, error: "wi must be a work item id (wi_…)" }, { status: 400 });
  const profile = body.profile === undefined || body.profile === "" ? undefined : body.profile;
  if (profile !== undefined && !isPolyforgeProfileName(profile)) {
    return NextResponse.json({ ok: false, error: "profile must match [a-z0-9][a-z0-9-]{0,31}" }, { status: 400 });
  }
  if (body.text !== undefined && typeof body.text !== "string") return NextResponse.json({ ok: false, error: "text must be a string" }, { status: 400 });
  const text = typeof body.text === "string" && body.text.trim() ? body.text : undefined;
  if (text === undefined) return NextResponse.json({ ok: false, error: "text is required" }, { status: 400 });
  if (text.length > OWNER_MESSAGE_TEXT_MAX) return NextResponse.json({ ok: false, error: `text is longer than ${OWNER_MESSAGE_TEXT_MAX} characters` }, { status: 400 });
  try {
    const ack = await sendPolyforgeOwnerMessage(body.wi, text, profile);
    return NextResponse.json({ ok: true, ack });
  } catch (error) {
    if (!(error instanceof PolyforgeError)) return NextResponse.json({ ok: false, error: "polyforge is unavailable" }, { status: 409 });
    return NextResponse.json({ ok: false, error: error.message, ...(error.reason ? { reason: error.reason } : {}) }, { status: 409 });
  }
}
