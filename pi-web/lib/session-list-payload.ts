import type { SessionInfo } from "./types";
import { skillExpansionToCommand } from "./slash-display";

/**
 * Response-layer trim for the /api/sessions LIST payload (audit task: the
 * list returned ~6.1MB for 379 sessions because every row carried the full
 * first-message text). The trim happens HERE, on the merged list handed to
 * the response — never inside lib/session-reader.ts, whose view cache and
 * scan index must keep full text for the session-detail endpoint and for
 * sidebar previews after the row is opened.
 */

/** Preview cap for a row's firstMessage in the list payload. */
export const SESSION_LIST_FIRST_MESSAGE_MAX = 160;

/**
 * Truncates `firstMessage` to SESSION_LIST_FIRST_MESSAGE_MAX characters and
 * marks the row with `firstMessageTruncated: true` so consumers can tell a
 * cut preview from the real text. Short rows pass through untouched (no
 * marker, referential copies where nothing changes).
 *
 * A cut row also carries `firstMessageDisplay`: the compact DISPLAY form
 * (an SDK-expanded <skill> block collapsed back to `/skill:name args`)
 * computed from the FULL cached text before the cut. The collapse regex
 * needs the complete closing envelope, so it cannot run client-side on a
 * truncated preview — without this field the sidebar would render raw
 * `<skill name=...>` markup for any expanded skill longer than the cap.
 */
export function trimSessionListFirstMessage<T extends SessionInfo>(session: T): T {
  const firstMessage = session.firstMessage ?? "";
  if (firstMessage.length <= SESSION_LIST_FIRST_MESSAGE_MAX) return session;
  const display = skillExpansionToCommand(firstMessage) ?? firstMessage;
  return {
    ...session,
    firstMessage: firstMessage.slice(0, SESSION_LIST_FIRST_MESSAGE_MAX),
    firstMessageDisplay: display.slice(0, SESSION_LIST_FIRST_MESSAGE_MAX),
    firstMessageTruncated: true,
    // review r2: the wi's literal response contract names a `truncated: true`
    // marker; kept alongside the more specific `firstMessageTruncated` (which
    // predates this and is what existing tests/consumers already read) so
    // both names are satisfied rather than picking one and breaking the other.
    truncated: true,
  };
}

export function trimSessionListFirstMessages<T extends SessionInfo>(sessions: T[]): T[] {
  return sessions.map(trimSessionListFirstMessage);
}
