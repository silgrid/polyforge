import type { AgentMessage } from "./types";
import { isScrollAtTail } from "./chat-lazy-load";

/**
 * The session page's manual "fetch latest": pure helpers the
 * hook (`useAgentSession`'s `manualFetch`) and the message list's pull-up
 * gesture (`ChatWindow`) share. Identity is the entry id, never message text: a
 * same-text follow-up is a distinct entry (#153's lesson).
 */

/** A manual fetch's reads (the forced session read and the run-state read) give up this long after the trigger. */
export const MANUAL_FETCH_TIMEOUT_MS = 15_000;
/** How far a touch must pull up past the bottom of an already bottomed-out list to start a fetch. */
export const PULL_FETCH_THRESHOLD_PX = 60;

/** A loaded window of the conversation: `messages` may run past `entryIds` with live (not yet saved) messages. */
export interface LoadedWindow {
  messages: AgentMessage[];
  entryIds: string[];
  oldestEntryId: string | null;
  hasMore: boolean;
}

/**
 * Whether a fresh read adds anything to the view: an entry id the view does not
 * hold, or a shared entry whose message changed. Messages are compared by their
 * serialized content under the same entry id, never matched by text.
 */
export function hasFreshContent(
  previous: Pick<LoadedWindow, "messages" | "entryIds">,
  next: Pick<LoadedWindow, "messages" | "entryIds">,
): boolean {
  const index = new Map<string, number>();
  previous.entryIds.forEach((id, i) => index.set(id, i));
  for (let i = 0; i < next.entryIds.length; i++) {
    const at = index.get(next.entryIds[i]);
    if (at === undefined) return true;
    if (JSON.stringify(previous.messages[at]) !== JSON.stringify(next.messages[i])) return true;
  }
  return false;
}

/**
 * The view after a forced read of the newest window, keeping the history the
 * user already paged in. The fresh window replaces everything from its first
 * entry on (live messages included: the read is the truth for them); the
 * loaded entries before it stay, with their older cursor and `hasMore`.
 *
 * - The fresh window covers the loaded one (it holds the loaded first entry, or
 *   it is the whole branch): the fresh window.
 * - No overlap while the server still has older entries (more than a window of
 *   new entries since the view's last read): the loaded saved entries stay and
 *   the fresh window follows them. Entries between the two windows are missing
 *   until the view is reloaded; a known, rare gap, not a reason to drop history.
 *
 * Never returns an entry id twice.
 */
export function mergeLoadedHistory(previous: LoadedWindow, next: LoadedWindow): LoadedWindow {
  const first = next.entryIds[0];
  if (first === undefined) return next;
  const at = previous.entryIds.indexOf(first);
  if (at === -1) {
    const loadedFirst = previous.entryIds[0];
    if (loadedFirst === undefined || next.entryIds.includes(loadedFirst) || !next.hasMore) return next;
    const fresh = new Set(next.entryIds);
    const keep: number[] = [];
    previous.entryIds.forEach((id, i) => { if (!fresh.has(id)) keep.push(i); });
    return {
      messages: [...keep.map((i) => previous.messages[i]), ...next.messages],
      entryIds: [...keep.map((i) => previous.entryIds[i]), ...next.entryIds],
      oldestEntryId: previous.oldestEntryId,
      hasMore: previous.hasMore,
    };
  }
  if (at === 0) return next;
  const fresh = new Set(next.entryIds);
  const keptIds = previous.entryIds.slice(0, at);
  if (keptIds.some((id) => fresh.has(id))) return next;
  return {
    messages: [...previous.messages.slice(0, at), ...next.messages],
    entryIds: [...keptIds, ...next.entryIds],
    oldestEntryId: previous.oldestEntryId,
    hasMore: previous.hasMore,
  };
}

/** A pull-up arms only for a touch that starts while the list is already at its bottom. */
export function shouldArmPullFetch(input: {
  touch: boolean;
  scrollTop: number;
  clientHeight: number;
  scrollHeight: number;
}): boolean {
  return input.touch && isScrollAtTail(input.scrollTop, input.clientHeight, input.scrollHeight);
}

/** Released past the threshold, upward (the finger moved up the screen). */
export function isPullFetchTriggered(startY: number, endY: number, threshold = PULL_FETCH_THRESHOLD_PX): boolean {
  return startY - endY >= threshold;
}

/** Local `hh:mm`, zero-padded: the completion time the updated notice carries. */
export function formatFetchTime(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
