/**
 * Generic drag-to-reorder core (pi#70), shared by every tab strip in the
 * app: the right-panel file/terminal strip (`TabBar`/`file-tab-state.ts`)
 * and the session pane strip (`PaneHeader`/`SplitPaneLayout`/
 * `lib/pane-state.ts`). Every helper here is pure and keyed by id, so a
 * reorder only ever changes POSITION — the moved item is spliced out and
 * reinserted verbatim, never rebuilt, so its bound session/pane mapping is
 * untouched by construction (task requirement: moving a tab must not change
 * what it points at).
 */

/**
 * Move the item identified by `draggedId` to sit immediately before or
 * after the item identified by `targetId`.
 *
 * No-ops (returns the same array reference) when the two ids are equal, or
 * when either id is not present in `items` — a stale drag (e.g. the target
 * closed mid-drag) leaves the array untouched rather than throwing.
 */
export function reorderById<T>(
  items: T[],
  getId: (item: T) => string,
  draggedId: string,
  targetId: string,
  after: boolean,
): T[] {
  if (draggedId === targetId) return items;
  const fromIndex = items.findIndex((item) => getId(item) === draggedId);
  const toIndex = items.findIndex((item) => getId(item) === targetId);
  if (fromIndex === -1 || toIndex === -1) return items;

  const next = items.slice();
  const [moved] = next.splice(fromIndex, 1);
  let insertAt = toIndex;
  // Removing `fromIndex` shifts every later index left by one before the
  // reinsertion point is computed against the post-removal array.
  if (fromIndex < toIndex) insertAt -= 1;
  if (after) insertAt += 1;
  next.splice(insertAt, 0, moved);
  return next;
}

/**
 * Reconcile a tracked display order against the live id set: ids no longer
 * live are dropped, and ids not yet tracked (newly opened tabs) are
 * appended in their live-array order. Order among already-tracked ids is
 * preserved exactly, so a reorder survives other tabs opening or closing
 * around it.
 */
export function syncTabOrder(order: readonly string[], liveIds: readonly string[]): string[] {
  const live = new Set(liveIds);
  const kept = order.filter((id) => live.has(id));
  const keptSet = new Set(kept);
  const appended = liveIds.filter((id) => !keptSet.has(id));
  return [...kept, ...appended];
}

/**
 * Render a live `items` array in the tracked `order`. Any id in `order`
 * that no longer matches a live item is skipped; any live item whose id is
 * not (yet) in `order` is appended at the end in its natural array
 * position, so an order that has not been synced yet (e.g. the very first
 * render before the sync effect runs) still renders every item exactly
 * once, in the caller's own order.
 */
export function applyTabOrder<T>(
  order: readonly string[],
  items: T[],
  getId: (item: T) => string,
): T[] {
  const byId = new Map(items.map((item) => [getId(item), item]));
  const seen = new Set<string>();
  const ordered: T[] = [];
  for (const id of order) {
    const item = byId.get(id);
    if (!item || seen.has(id)) continue;
    seen.add(id);
    ordered.push(item);
  }
  for (const item of items) {
    const id = getId(item);
    if (seen.has(id)) continue;
    seen.add(id);
    ordered.push(item);
  }
  return ordered;
}
