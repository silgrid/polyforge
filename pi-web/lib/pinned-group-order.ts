/**
 * Pinned-group drag order (wi B2): the sidebar's pinned (listed-directory)
 * groups render in the custom-directories storage order by default; this
 * module persists a user-chosen order as a JSON array of group keys under
 * "pi-web:pinned-group-order". The order is an OVERLAY on the rendered
 * sequence: `applyGroupOrder` maps a stored order onto the current group
 * list — known keys sort by their stored position, keys with no stored
 * entry keep their default (incoming) position, and stored keys that no
 * longer match any group are ignored, so an unpinned group leaves nothing
 * behind and a re-pin simply finds its old slot again.
 *
 * Reorder semantics follow the upstream adjacent-move model (upstream
 * SessionSidebar adjacentProjectMove): a move swaps the dragged group with
 * the neighbor it lands on, i.e. the persisted order is always the FULL
 * current key sequence written at move time (remove + reinsert at the
 * drop target's index). Free positioning beyond the pinned-group band
 * (into the unpinned project-groups section) is not expressible here.
 *
 * Same best-effort, storage-injectable, DOM-free pattern as
 * lib/pinned-expansion.ts: every accessor re-reads storage (no module
 * cache), unavailable or corrupt storage degrades to "no order" (the
 * default sequence), and writes never throw.
 */

export const PINNED_GROUP_ORDER_STORAGE_KEY = "pi-web:pinned-group-order";

interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

function getBrowserStorage(): StorageLike | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

/**
 * The persisted group-key order; absent, corrupt, or unavailable storage
 * reads as the empty array (caller keeps the default sequence). Duplicate
 * keys collapse to their first occurrence; non-string entries drop.
 */
export function listGroupOrder(
  storage: StorageLike | null = getBrowserStorage(),
): string[] {
  if (!storage) return [];
  try {
    const raw = storage.getItem(PINNED_GROUP_ORDER_STORAGE_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    const keys: string[] = [];
    for (const entry of parsed) {
      if (typeof entry !== "string") continue;
      if (!keys.includes(entry)) keys.push(entry);
    }
    return keys;
  } catch {
    return [];
  }
}

/**
 * Persist the group-key order (best-effort; never throws). Callers pass the
 * full current key sequence — a partial list still applies (unknown keys
 * keep their default positions), but a full list round-trips exactly.
 */
export function setGroupOrder(
  keys: readonly string[],
  storage: StorageLike | null = getBrowserStorage(),
): void {
  if (!storage) return;
  try {
    storage.setItem(PINNED_GROUP_ORDER_STORAGE_KEY, JSON.stringify([...keys]));
  } catch {
    // ignore storage quota / privacy-mode errors
  }
}

export interface GroupKeyed {
  key: string;
}

/**
 * Apply a stored order to the incoming group list (pure, no storage).
 * - empty order → the incoming sequence unchanged (fresh install, corrupt
 *   storage: today's behavior);
 * - keys present in the order sort by their stored position;
 * - keys absent from the order (a newly pinned group) keep their default
 *   position: each is re-inserted at its incoming index, clamped to the
 *   current result length, so it lands where the default list put it;
 * - stored keys matching no group are ignored.
 * Always returns a new array; the input is never mutated.
 */
export function applyGroupOrder<T extends GroupKeyed>(
  projects: readonly T[],
  order: readonly string[],
): T[] {
  if (order.length === 0 || projects.length <= 1) {
    return [...projects];
  }
  // First occurrence wins (listGroupOrder already dedupes, but a caller may
  // pass a raw array).
  const orderIndex = new Map<string, number>();
  for (let index = 0; index < order.length; index += 1) {
    const key = order[index];
    if (!orderIndex.has(key)) orderIndex.set(key, index);
  }
  const known: T[] = [];
  const unknown: T[] = [];
  for (const project of projects) {
    if (orderIndex.has(project.key)) known.push(project);
    else unknown.push(project);
  }
  // No key of the incoming list is ordered → nothing to apply.
  if (known.length === 0) return [...projects];
  known.sort((a, b) => orderIndex.get(a.key)! - orderIndex.get(b.key)!);
  const result = [...known];
  for (const project of unknown) {
    const defaultIndex = projects.indexOf(project);
    const insertAt = defaultIndex < 0 ? result.length : Math.min(defaultIndex, result.length);
    result.splice(insertAt, 0, project);
  }
  return result;
}
