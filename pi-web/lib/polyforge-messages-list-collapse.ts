/**
 * The Messages contact column's whole-column collapse (the
 * ≤640px sheet folds through the same key), the paradigm
 * with its own key: one namespaced key in this browser's localStorage holds
 * "1" while the column is folded to its narrow strip — on desktop 28px, on
 * the ≤640px sheet 40px — never pf config, never a server file, never a
 * request. It is a different fact from #222's width (`pi-web:polyforge-messages-list-width`, which a
 * collapse never writes) and from #223's folded project groups
 * (`pi-web:polyforge-messages-collapsed`). The column defaults to expanded:
 * an absent, blocked, unreadable or any other value than "1"/"true" reads
 * as expanded. A write storage refuses still takes effect for this page's
 * lifetime. Client-safe: no Node import.
 */

export const POLYFORGE_MESSAGES_LIST_COLLAPSE_KEY = "pi-web:polyforge-messages-list-collapsed";
/** Dispatched on window after a write, so the open page re-reads it. */
export const POLYFORGE_MESSAGES_LIST_COLLAPSE_EVENT = "pi-web:polyforge-messages-list-collapsed";

/** A stored string → collapsed? Pure; never throws. */
export function parsePolyforgeMessagesListCollapse(raw: unknown): boolean {
  return raw === "1" || raw === "true";
}

/** The raw stored string (null when absent or storage is unavailable). */
export function readPolyforgeMessagesListCollapseRaw(): string | null {
  try {
    return window.localStorage.getItem(POLYFORGE_MESSAGES_LIST_COLLAPSE_KEY);
  } catch {
    return null;
  }
}

// A toggle's result for this page's lifetime when storage refused to keep it
// (blocked, quota). Null while storage holds the truth, so another tab's
// write still shows.
let session: boolean | null = null;

/** Whether the column is collapsed: the in-memory state after a refused
 *  write, else the stored value (expanded when storage is unavailable). A
 *  boolean, so it serves as a useSyncExternalStore snapshot as is. */
export function readMessagesListCollapsed(): boolean {
  if (session !== null) return session;
  return parsePolyforgeMessagesListCollapse(readPolyforgeMessagesListCollapseRaw());
}

/** Collapse or expand the column, store it when storage allows, and tell the
 *  open page either way. Never throws. Returns the state now in effect. */
export function writeMessagesListCollapsed(collapsed: boolean): boolean {
  try {
    window.localStorage.setItem(POLYFORGE_MESSAGES_LIST_COLLAPSE_KEY, collapsed ? "1" : "0");
    session = null;
  } catch {
    session = collapsed;
  }
  try {
    window.dispatchEvent(new Event(POLYFORGE_MESSAGES_LIST_COLLAPSE_EVENT));
  } catch {
    // no window event target: the next read still sees the new state
  }
  return collapsed;
}
