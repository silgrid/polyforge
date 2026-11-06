/**
 * Collapsed project groups of the Polyforge Messages list,
 * the #212 disclosure paradigm with its own key: one namespaced key in this
 * browser's localStorage holds a JSON array of the collapsed project names —
 * never pf config, never a server file, never a request. Groups default to
 * expanded: an absent, blocked, unreadable, corrupt or wrong-typed value reads
 * as "none collapsed"; non-string or empty entries are dropped. Names of
 * projects no longer mapped stay stored (a write preserves them) and are
 * ignored by the list, which only asks about the groups it renders. A write
 * storage refuses still takes effect for this page's lifetime. Client-safe:
 * no Node import.
 */

export const POLYFORGE_MESSAGES_COLLAPSE_KEY = "pi-web:polyforge-messages-collapsed";
/** Dispatched on window after a write, so the open page re-reads it. */
export const POLYFORGE_MESSAGES_COLLAPSE_EVENT = "pi-web:polyforge-messages-collapsed";

export const POLYFORGE_MESSAGES_COLLAPSE_NONE: ReadonlySet<string> = Object.freeze(new Set<string>());

/** A stored string → the collapsed project names. Pure; never throws. */
export function parsePolyforgeMessagesCollapse(raw: unknown): ReadonlySet<string> {
  if (typeof raw !== "string" || raw === "") return new Set<string>();
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return new Set<string>();
  }
  if (!Array.isArray(parsed)) return new Set<string>();
  const names = new Set<string>();
  for (const entry of parsed) {
    if (typeof entry === "string" && entry.trim()) names.add(entry.trim());
  }
  return names;
}

/** The raw stored string (null when absent or storage is unavailable). */
export function readPolyforgeMessagesCollapseRaw(): string | null {
  try {
    return window.localStorage.getItem(POLYFORGE_MESSAGES_COLLAPSE_KEY);
  } catch {
    return null;
  }
}

// A toggle's result for this page's lifetime when storage refused to keep it
// (blocked, quota): the groups stay foldable with storage unavailable. Null
// while storage holds the truth, so another tab's write still shows.
let session: ReadonlySet<string> | null = null;
// One parsed set per stored string, so a snapshot read is stable.
let cachedRaw: string | null = null;
let cachedValue: ReadonlySet<string> = POLYFORGE_MESSAGES_COLLAPSE_NONE;

/** The collapsed projects in effect: the in-memory state after a refused
 *  write, else the stored set (none when storage is unavailable). The same
 *  object until the state changes, so it serves as a useSyncExternalStore
 *  snapshot. */
export function readCollapsedProjects(): ReadonlySet<string> {
  if (session) return session;
  const raw = readPolyforgeMessagesCollapseRaw();
  if (raw !== cachedRaw) {
    cachedRaw = raw;
    cachedValue = raw === null ? POLYFORGE_MESSAGES_COLLAPSE_NONE : parsePolyforgeMessagesCollapse(raw);
  }
  return cachedValue;
}

/** Collapse or expand one project, keeping every other stored name, store it
 *  when storage allows, and tell the open page either way. Never throws.
 *  Returns the set now in effect. */
export function writeProjectCollapsed(project: string, collapsed: boolean): ReadonlySet<string> {
  const next = new Set(readCollapsedProjects());
  if (collapsed) next.add(project);
  else next.delete(project);
  try {
    window.localStorage.setItem(POLYFORGE_MESSAGES_COLLAPSE_KEY, JSON.stringify([...next].sort()));
    session = null;
  } catch {
    session = next;
  }
  try {
    window.dispatchEvent(new Event(POLYFORGE_MESSAGES_COLLAPSE_EVENT));
  } catch {
    // no window event target: the next read still sees the new state
  }
  return next;
}
