"use client";

import { useCallback, useState } from "react";

export interface TabDropTarget {
  id: string;
  after: boolean;
}

export interface TabDragHandlers {
  draggable: true;
  onDragStart: (event: React.DragEvent<HTMLElement>) => void;
  onDragOver: (event: React.DragEvent<HTMLElement>) => void;
  onDragLeave: (event: React.DragEvent<HTMLElement>) => void;
  onDrop: (event: React.DragEvent<HTMLElement>) => void;
  onDragEnd: () => void;
}

export interface UseTabDragReorderResult {
  /** id of the tab currently being dragged, or null when nothing is. */
  draggedId: string | null;
  /** Where a drop would land right now: which tab, and before/after it. */
  dropTarget: TabDropTarget | null;
  /** Spread onto each draggable tab element, keyed by that tab's own id. */
  getDragHandlers: (id: string) => TabDragHandlers;
}

/**
 * Native HTML5 drag-to-reorder for a single tab strip (pi#70), shared by
 * `TabBar` (right-panel file/terminal tabs) and `SplitPaneLayout`'s embedded
 * `PaneHeader`s (session panes). Chosen over pointer events: every tab here
 * is already a plain DOM element with no custom gesture surface, the
 * browser supplies the drag image/cursor for free, and each strip is a
 * single row, so HTML5 DnD's per-element dragover/drop granularity is all
 * the "insertion position" feedback needs.
 *
 * `getDragHandlers(id)` is spread onto each tab. `draggedId` drives the
 * dragged tab's dim state; `dropTarget` drives the insertion-position
 * indicator on the hovered tab (before/after, split at its horizontal
 * midpoint). `onReorder` fires once per drop with the final
 * (draggedId, targetId, after) triple; the caller owns how that maps onto
 * its own state, so this hook never touches tab data itself — only which
 * tab is being dragged and where it would land.
 */
export function useTabDragReorder(
  onReorder: (draggedId: string, targetId: string, after: boolean) => void,
): UseTabDragReorderResult {
  const [draggedId, setDraggedId] = useState<string | null>(null);
  const [dropTarget, setDropTarget] = useState<TabDropTarget | null>(null);

  const reset = useCallback(() => {
    setDraggedId(null);
    setDropTarget(null);
  }, []);

  const getDragHandlers = useCallback(
    (id: string): TabDragHandlers => ({
      draggable: true,
      onDragStart: (event) => {
        setDraggedId(id);
        event.dataTransfer.effectAllowed = "move";
        try {
          // Firefox refuses to start a drag at all without data set here;
          // the value itself is never read back (state carries the id).
          event.dataTransfer.setData("text/plain", id);
        } catch {
          // Some environments (e.g. jsdom in tests) throw on setData; the
          // drag still works off draggedId/dropTarget state alone.
        }
      },
      onDragOver: (event) => {
        if (!draggedId || draggedId === id) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
        const rect = event.currentTarget.getBoundingClientRect();
        const after = event.clientX - rect.left > rect.width / 2;
        setDropTarget((prev) => (prev && prev.id === id && prev.after === after ? prev : { id, after }));
      },
      onDragLeave: (event) => {
        // A leave into a child element is not a real leave of the tab;
        // only clear the indicator when the pointer left the tab itself.
        const related = event.relatedTarget as Node | null;
        if (related && event.currentTarget.contains(related)) return;
        setDropTarget((prev) => (prev?.id === id ? null : prev));
      },
      onDrop: (event) => {
        event.preventDefault();
        // Recomputed fresh rather than trusting dropTarget state, so a drop
        // always reflects the cursor position at drop time.
        const rect = event.currentTarget.getBoundingClientRect();
        const after = event.clientX - rect.left > rect.width / 2;
        if (draggedId && draggedId !== id) onReorder(draggedId, id, after);
        reset();
      },
      onDragEnd: reset,
    }),
    [draggedId, onReorder, reset],
  );

  return { draggedId, dropTarget, getDragHandlers };
}
