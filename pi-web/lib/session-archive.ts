/**
 * Session archive set (wi pi#103, sidebar A1: Archive).
 *
 * The archive is pure client-side state: a set of session ids persisted in
 * localStorage under "pi-web:archived-session-ids" as a JSON string array —
 * the exact shape of the unread-ids precedent (pi-web:unread-session-ids):
 * the key is REMOVED when the set is empty, and nothing here ever talks to
 * the server. Archiving/unarchiving is a local set mutation only.
 *
 * Everything is pure and best-effort by contract (mirrors
 * lib/session-filter.ts / lib/pinned-expansion.ts): storage is injected so
 * `node --test` drives the real logic without a DOM, an absent/corrupt/
 * non-array/non-string payload reads as the empty set, and persistence
 * failures are swallowed (unavailable storage degrades to in-memory state).
 */

import type { SessionInfo } from "./types";

/** localStorage key of the persisted archive set (JSON string array). */
export const ARCHIVED_SESSIONS_STORAGE_KEY = "pi-web:archived-session-ids";

/** Minimal storage surface the archive helpers need (localStorage-shaped). */
export interface SessionArchiveStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

/** Acquires the browser's localStorage; null during server rendering or
 *  when the browser denies storage access. Never throws. */
export function sessionArchiveStorage(): SessionArchiveStorage | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch {
    return null; // storage-policy denial degrades to the empty set
  }
}

/** Reads the persisted archive set. Absent key, non-JSON, non-array or
 *  non-string entries read as the empty set; a throwing storage read never
 *  propagates. A raw scalar (non-JSON) is tolerated as a single id so the
 *  read stays useful against hand-edited values. */
export function loadArchivedSessionIds(
  storage: SessionArchiveStorage | null,
): Set<string> {
  if (!storage) return new Set();
  let raw: string | null;
  try {
    raw = storage.getItem(ARCHIVED_SESSIONS_STORAGE_KEY);
  } catch {
    return new Set();
  }
  if (raw == null) return new Set();
  try {
    const parsed: unknown = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      return new Set(parsed.filter((id): id is string => typeof id === "string"));
    }
    if (typeof parsed === "string" && parsed.length > 0) {
      return new Set([parsed]);
    }
    return new Set();
  } catch {
    return new Set();
  }
}

/** Persists the archive set (best-effort; never throws). The key is removed
 *  when the set is empty — the unread-ids precedent, so a clean tree leaves
 *  no stale `[]` behind in localStorage. */
export function saveArchivedSessionIds(
  ids: ReadonlySet<string>,
  storage: SessionArchiveStorage | null,
): void {
  if (!storage) return;
  try {
    if (ids.size === 0) storage.removeItem(ARCHIVED_SESSIONS_STORAGE_KEY);
    else storage.setItem(ARCHIVED_SESSIONS_STORAGE_KEY, JSON.stringify([...ids]));
  } catch {
    // ignore storage quota / privacy-mode errors
  }
}

/** Pure set difference: `ids` minus `removableIds`. Returns the SAME set
 *  reference when nothing is removable so React state updates (which rely
 *  on reference identity) stay no-ops. Reused for both external-deletion
 *  pruning (the caller passes ids absent from the freshly loaded session
 *  list) and the running-force-unarchive effect (the caller passes the
 *  currently running ids). */
export function pruneArchivedSessionIds(
  ids: ReadonlySet<string>,
  removableIds: Iterable<string>,
): ReadonlySet<string> {
  let next: Set<string> | null = null;
  for (const id of removableIds) {
    if (!ids.has(id)) continue;
    if (!next) next = new Set(ids);
    next.delete(id);
  }
  return next ?? ids;
}

/** One project group of the archive view: the shared project root (the
 *  path-derived label source) and its archived sessions. */
export interface ArchivedSessionGroup {
  root: string;
  sessions: SessionInfo[];
}

/** Groups archived sessions by `projectRoot ?? cwd` for the archive view.
 *  Sessions within a group are ordered most-recently-modified first, and
 *  the groups themselves are ordered by their most-recent session, so the
 *  view leads with the liveliest project. Pure — no React, no storage. */
export function groupArchivedSessionsByProject(
  sessions: readonly SessionInfo[],
): ArchivedSessionGroup[] {
  const byRoot = new Map<string, SessionInfo[]>();
  for (const session of sessions) {
    const root = session.projectRoot ?? session.cwd;
    const list = byRoot.get(root);
    if (list) list.push(session);
    else byRoot.set(root, [session]);
  }
  const groups: ArchivedSessionGroup[] = [];
  for (const [root, list] of byRoot) {
    const sorted = [...list].sort((a, b) => compareModifiedDesc(a, b));
    groups.push({ root, sessions: sorted });
  }
  groups.sort((a, b) => {
    const aModified = a.sessions[0]?.modified ?? "";
    const bModified = b.sessions[0]?.modified ?? "";
    if (aModified !== bModified) return aModified < bModified ? 1 : -1;
    return a.root < b.root ? -1 : a.root > b.root ? 1 : 0;
  });
  return groups;
}

function compareModifiedDesc(a: SessionInfo, b: SessionInfo): number {
  if (a.modified !== b.modified) return a.modified < b.modified ? 1 : -1;
  // Stable tie-break so the order never flickers between renders.
  if (a.id !== b.id) return a.id < b.id ? -1 : 1;
  return 0;
}
