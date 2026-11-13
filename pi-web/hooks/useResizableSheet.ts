"use client";

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type PointerEvent,
} from "react";
import {
  SHEET_DEFAULT_HEIGHT_PCT,
  SHEET_MAX_HEIGHT_PCT,
  SHEET_MIN_HEIGHT_PCT,
  clampSheetHeightPct,
  parseStoredSheetHeightPct,
} from "@/lib/panel-layout";

interface DragState {
  pointerId: number;
  // The sheet is bottom-anchored, so its bottom edge stays fixed during the
  // drag: the height under the pointer is sheetBottom - clientY.
  sheetBottom: number;
  // Percentage basis: the padding-box height of the sheet's containing block
  // (its offsetParent) — the same container a `62%` height resolves against.
  containerHeight: number;
  target: HTMLDivElement;
  previousCursor: string;
  previousUserSelect: string;
}

interface UseResizableSheetOptions {
  ariaLabel: string;
  storageKey: string;
}

function writeStoredSheetHeightPct(storageKey: string, pct: number): void {
  try {
    window.localStorage.setItem(storageKey, String(pct));
  } catch {
    // Resizing remains available when storage is unavailable.
  }
}

/** Drag-to-resize for the mobile bottom sheets (pi#120): one shared
 *  mechanism, two consumers (the Polyforge sheet and the bg-tasks sheet,
 *  each persisting its own height). The drag is continuous, clamped to
 *  [30%, 90%] of the sheet's container — dragging past either bound clamps
 *  and never dismisses (the backdrop tap stays the only dismiss gesture: the
 *  handle is a DOM child of the sheet, above the backdrop). Absent/invalid/
 *  out-of-range stored heights read as the 62% default. Pointer events cover
 *  touch and mouse alike (pi#31: a mouse-only drag is the trap); the
 *  handle's touch-action: none keeps the page from scrolling or
 *  pull-to-refresh while it is dragged. */
export function useResizableSheet(options: UseResizableSheetOptions) {
  const { ariaLabel, storageKey } = options;
  const sheetRef = useRef<HTMLDivElement>(null);
  const dragRef = useRef<DragState | null>(null);
  const restoredRef = useRef(false);
  const [heightPct, setHeightPct] = useState(SHEET_DEFAULT_HEIGHT_PCT);
  const [isResizing, setIsResizing] = useState(false);
  // Live height alongside the state: the drag writes the style directly
  // (no re-render per move); the state commits on finish.
  const heightPctRef = useRef(SHEET_DEFAULT_HEIGHT_PCT);

  const applyLiveHeightPct = useCallback((nextPct: number) => {
    heightPctRef.current = nextPct;
    sheetRef.current?.style.setProperty("height", `${nextPct}%`);
  }, []);

  const commitHeightPct = useCallback((candidate: number, forcePersist = false) => {
    const nextPct = clampSheetHeightPct(candidate);
    applyLiveHeightPct(nextPct);
    setHeightPct(nextPct);
    if (forcePersist) writeStoredSheetHeightPct(storageKey, nextPct);
    return nextPct;
  }, [applyLiveHeightPct, storageKey]);

  const restoreBodyState = useCallback((drag: DragState) => {
    document.body.style.cursor = drag.previousCursor;
    document.body.style.userSelect = drag.previousUserSelect;
  }, []);

  const finishResize = useCallback((pointerId: number) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== pointerId) return;
    dragRef.current = null;
    restoreBodyState(drag);
    setIsResizing(false);
    // The dragged height persists on every finished drag, clamped or not.
    commitHeightPct(heightPctRef.current, true);

    try {
      if (drag.target.hasPointerCapture(pointerId)) {
        drag.target.releasePointerCapture(pointerId);
      }
    } catch {
      // The browser may have already released capture after pointer cancellation.
    }
  }, [commitHeightPct, restoreBodyState]);

  const onPointerDown = useCallback((event: PointerEvent<HTMLDivElement>) => {
    if (event.pointerType === "mouse" && event.button !== 0) return;
    const sheet = sheetRef.current;
    if (!sheet) return;
    // The sheet must have a measurable containing block before the drag
    // starts; without one the percentage math has no basis.
    const container = sheet.offsetParent ?? sheet.parentElement;
    const containerHeight = container?.clientHeight ?? 0;
    if (containerHeight <= 0) return;
    event.preventDefault();
    event.stopPropagation();

    const activeDrag = dragRef.current;
    if (activeDrag) finishResize(activeDrag.pointerId);

    const target = event.currentTarget;
    target.focus({ preventScroll: true });
    target.setPointerCapture(event.pointerId);
    dragRef.current = {
      pointerId: event.pointerId,
      sheetBottom: sheet.getBoundingClientRect().bottom,
      containerHeight,
      target,
      previousCursor: document.body.style.cursor,
      previousUserSelect: document.body.style.userSelect,
    };
    document.body.style.cursor = "row-resize";
    document.body.style.userSelect = "none";
    setIsResizing(true);
  }, [finishResize]);

  const onPointerMove = useCallback((event: PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    if (event.pointerType === "mouse" && event.buttons === 0) {
      finishResize(event.pointerId);
      return;
    }
    event.preventDefault();
    // Up grows, down shrinks; past either bound just clamps (never dismisses).
    const nextPct = clampSheetHeightPct(((drag.sheetBottom - event.clientY) / drag.containerHeight) * 100);
    applyLiveHeightPct(nextPct);
    event.currentTarget.setAttribute("aria-valuenow", String(nextPct));
    event.currentTarget.setAttribute("aria-valuetext", `${nextPct}%`);
  }, [applyLiveHeightPct, finishResize]);

  const onPointerUp = useCallback((event: PointerEvent<HTMLDivElement>) => {
    finishResize(event.pointerId);
  }, [finishResize]);

  const onPointerCancel = useCallback((event: PointerEvent<HTMLDivElement>) => {
    finishResize(event.pointerId);
  }, [finishResize]);

  const onLostPointerCapture = useCallback((event: PointerEvent<HTMLDivElement>) => {
    finishResize(event.pointerId);
  }, [finishResize]);

  const resetHeight = useCallback(() => {
    commitHeightPct(SHEET_DEFAULT_HEIGHT_PCT, true);
  }, [commitHeightPct]);

  const onKeyDown = useCallback((event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.shiftKey ? 15 : 5;
    if (event.key === "ArrowUp") {
      event.preventDefault();
      commitHeightPct(heightPctRef.current + step, true);
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      commitHeightPct(heightPctRef.current - step, true);
    } else if (event.key === "Home") {
      event.preventDefault();
      commitHeightPct(SHEET_MIN_HEIGHT_PCT, true);
    } else if (event.key === "End") {
      event.preventDefault();
      commitHeightPct(SHEET_MAX_HEIGHT_PCT, true);
    } else if (event.key === "Enter") {
      event.preventDefault();
      resetHeight();
    }
  }, [commitHeightPct, resetHeight]);

  // Restore the persisted height once after mount (window is unavailable at
  // first render); absent/invalid/out-of-range reads as the 62% default and
  // is not rewritten — the next finished drag replaces the value wholesale.
  useEffect(() => {
    if (restoredRef.current) return;
    restoredRef.current = true;
    let stored: number | null = null;
    try {
      stored = parseStoredSheetHeightPct(window.localStorage.getItem(storageKey));
    } catch {
      // storage unavailable — keep the default
    }
    commitHeightPct(stored ?? SHEET_DEFAULT_HEIGHT_PCT, false);
  }, [commitHeightPct, storageKey]);

  // A drag abandoned by the window (blur / hidden tab) finishes cleanly.
  useEffect(() => {
    if (!isResizing) return;
    const cancelResize = () => {
      const drag = dragRef.current;
      if (drag) finishResize(drag.pointerId);
    };
    const onVisibilityChange = () => {
      if (document.visibilityState !== "visible") cancelResize();
    };
    window.addEventListener("blur", cancelResize);
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      window.removeEventListener("blur", cancelResize);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [finishResize, isResizing]);

  // A drag still active at unmount restores the body styles it took.
  useEffect(() => {
    return () => {
      const drag = dragRef.current;
      if (!drag) return;
      dragRef.current = null;
      restoreBodyState(drag);
    };
  }, [restoreBodyState]);

  return {
    heightPct,
    isResizing,
    sheetRef,
    separatorProps: {
      "aria-label": ariaLabel,
      "aria-orientation": "horizontal" as const,
      "aria-valuemax": SHEET_MAX_HEIGHT_PCT,
      "aria-valuemin": SHEET_MIN_HEIGHT_PCT,
      "aria-valuenow": heightPct,
      "aria-valuetext": `${heightPct}%`,
      onDoubleClick: resetHeight,
      onKeyDown,
      onLostPointerCapture,
      onPointerCancel,
      onPointerDown,
      onPointerMove,
      onPointerUp,
      role: "separator" as const,
      tabIndex: 0,
    },
  };
}