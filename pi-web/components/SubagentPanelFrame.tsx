"use client";

// The shared shell of a subagent progress panel: the floating section, its
// top-edge height drag and its Esc rule, with the header, body and footer
// supplied by the caller. ClaudeSubagentPanel and PiSubagentPanel both use it,
// so the two panels keep one layout, one drag and one keyboard rule.
import { useCallback, useRef, useState, type PointerEvent, type ReactNode, type RefObject } from "react";

/** What `isClaudeSubagentPanelEscape` reads of a keydown. */
export interface ClaudeSubagentPanelKey {
  key: string;
  isComposing: boolean;
  keyCode: number;
  defaultPrevented: boolean;
  target: EventTarget | null;
}

/**
 * Whether a keydown is the Escape that closes the open panel: one nothing
 * handled yet and no input method composes with (`isComposing`, or Safari's keyCode 229),
 * pressed inside the panel's chat pane — the composer keeps the focus when a list button
 * opens the panel — or, with the focus nowhere (the page itself), while that pane is the
 * active one. A key from elsewhere (another pane, a dialog above the page) is not the panel's.
 */
export function isClaudeSubagentPanelEscape(event: ClaudeSubagentPanelKey, pane: { contains(node: Node | null): boolean } | null, activePane: boolean): boolean {
  if (event.key !== "Escape" || event.defaultPrevented || event.isComposing || event.keyCode === 229) return false;
  const target = event.target as { nodeName?: unknown } | null;
  const nodeName = typeof target?.nodeName === "string" ? target.nodeName : undefined;
  if (nodeName === undefined || nodeName === "BODY" || nodeName === "HTML" || nodeName === "#document") return activePane;
  return pane !== null && pane.contains(target as Node);
}

/** Where a finished drag keeps the panel height (px), shared by every subagent's panel. */
export const CLAUDE_SUBAGENT_PANEL_HEIGHT_KEY = "pi-web:claude-subagent-panel-height";
export const CLAUDE_SUBAGENT_PANEL_MIN_HEIGHT = 160;
// The floating wrapper's vertical padding in ChatWindow (8px top + 6px bottom).
const PANEL_WRAPPER_PADDING_Y = 14;

function readStoredPanelHeight(): number | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(CLAUDE_SUBAGENT_PANEL_HEIGHT_KEY);
    const value = raw === null ? NaN : Number(raw);
    return Number.isFinite(value) && value >= CLAUDE_SUBAGENT_PANEL_MIN_HEIGHT ? value : null;
  } catch {
    return null;
  }
}

function writeStoredPanelHeight(height: number | null): void {
  try {
    if (height === null) window.localStorage.removeItem(CLAUDE_SUBAGENT_PANEL_HEIGHT_KEY);
    else window.localStorage.setItem(CLAUDE_SUBAGENT_PANEL_HEIGHT_KEY, String(height));
  } catch {
    // Resizing still works for this panel when storage is unavailable.
  }
}

interface PanelDrag {
  pointerId: number;
  startY: number;
  startHeight: number;
  max: number;
  height: number;
  moved: boolean;
}

/**
 * The panel's user height, dragged on its top edge as useResizableSheet's
 * sheets are (pointer events with capture, so mouse and touch alike), in pixels: null is the
 * default (content-sized, capped as before). The drag is bottom-anchored and clamped to
 * [160px, the message area (ChatWindow's `data-claude-subagent-panel-bounds` box) minus the
 * wrapper's padding], measured at its start; a finished drag that moved stores the height,
 * which every panel mount reads; a double-click clears it back to the default. A stored
 * height taller than a later, smaller area is shrunk by the wrapper's CSS (maxHeight 100%,
 * a shrinkable section), so the header with Close stays on screen.
 */
function useClaudeSubagentPanelHeight() {
  const [height, setHeight] = useState<number | null>(readStoredPanelHeight);
  const dragRef = useRef<PanelDrag | null>(null);

  const finish = useCallback((target: HTMLElement, pointerId: number) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== pointerId) return;
    dragRef.current = null;
    if (drag.moved) writeStoredPanelHeight(drag.height);
    try {
      if (target.hasPointerCapture(pointerId)) target.releasePointerCapture(pointerId);
    } catch {
      // Capture may already be gone after a cancelled pointer.
    }
  }, []);

  const onPointerDown = useCallback((event: PointerEvent<HTMLDivElement>) => {
    if (event.pointerType === "mouse" && event.button !== 0) return;
    const panel = event.currentTarget.closest("[data-subagent-panel]");
    if (!panel) return;
    const bounds = panel.closest("[data-claude-subagent-panel-bounds]");
    const area = bounds ? bounds.clientHeight : window.innerHeight;
    const startHeight = panel.getBoundingClientRect().height;
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    dragRef.current = {
      pointerId: event.pointerId,
      startY: event.clientY,
      startHeight,
      max: Math.max(CLAUDE_SUBAGENT_PANEL_MIN_HEIGHT, area - PANEL_WRAPPER_PADDING_Y),
      height: Math.round(startHeight),
      moved: false,
    };
  }, []);

  const onPointerMove = useCallback((event: PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    if (event.pointerType === "mouse" && event.buttons === 0) {
      finish(event.currentTarget, event.pointerId);
      return;
    }
    event.preventDefault();
    if (event.clientY === drag.startY && !drag.moved) return;
    // Up grows, down shrinks; past either bound it just clamps.
    const next = Math.round(Math.min(drag.max, Math.max(CLAUDE_SUBAGENT_PANEL_MIN_HEIGHT, drag.startHeight + drag.startY - event.clientY)));
    drag.moved = true;
    drag.height = next;
    setHeight(next);
  }, [finish]);

  const onPointerEnd = useCallback((event: PointerEvent<HTMLDivElement>) => {
    finish(event.currentTarget, event.pointerId);
  }, [finish]);

  const onDoubleClick = useCallback(() => {
    dragRef.current = null;
    writeStoredPanelHeight(null);
    setHeight(null);
  }, []);

  return {
    height,
    handleProps: {
      onPointerDown,
      onPointerMove,
      onPointerUp: onPointerEnd,
      onPointerCancel: onPointerEnd,
      onLostPointerCapture: onPointerEnd,
      onDoubleClick,
    },
  };
}

/**
 * The floating panel itself: the section every subagent panel shares, its
 * drag handle, then the caller's header, scrolling body and footer. A failed
 * run turns the left edge red; the accent is the caller's (Claude's violet,
 * the Pi subagent's violet).
 */
export function SubagentPanelFrame({
  panelAttrs,
  ariaLabel,
  accent,
  borderColor,
  status,
  failed = false,
  resizeLabel,
  bodyRef,
  header,
  children,
  footer,
}: {
  panelAttrs: Record<`data-${string}`, string | undefined>;
  ariaLabel: string;
  /** The run's colour: the left edge, unless the run failed. */
  accent: string;
  /** The section border's colour; each panel keeps its own. */
  borderColor: string;
  /** The run's status, so a failed run's left edge is red without a second flag. */
  status?: string;
  failed?: boolean;
  resizeLabel: string;
  /** The scrolling body, for a panel that keeps its newest entry in view. */
  bodyRef?: RefObject<HTMLDivElement | null>;
  header: ReactNode;
  children: ReactNode;
  footer: ReactNode;
}) {
  const { height, handleProps } = useClaudeSubagentPanelHeight();
  return (
    <section
      {...panelAttrs}
      data-subagent-panel=""
      aria-label={ariaLabel}
      style={{ display: "flex", flexDirection: "column", minWidth: 0, minHeight: 0, ...(height === null ? { maxHeight: "min(60vh, 560px)" } : { height }), overflow: "hidden", fontSize: 12, borderRadius: 8, border: `1px solid ${borderColor}`, borderLeftColor: failed || status === "failed" ? "#f87171" : accent, background: "var(--bg-panel)", boxShadow: "0 6px 24px rgba(0,0,0,0.18)" }}
    >
      <div
        data-claude-subagent-panel-resize=""
        role="separator"
        aria-orientation="horizontal"
        aria-label={resizeLabel}
        title={resizeLabel}
        {...handleProps}
        style={{ display: "flex", alignItems: "center", justifyContent: "center", flexShrink: 0, height: 10, cursor: "row-resize", touchAction: "none" }}
      >
        <span aria-hidden="true" style={{ width: 32, height: 3, borderRadius: 2, background: "rgba(139,92,246,0.35)" }} />
      </div>
      {header}
      <div ref={bodyRef} data-subagent-panel-body="" style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        {children}
      </div>
      {footer}
    </section>
  );
}
