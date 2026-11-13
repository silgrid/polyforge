"use client";

// A claude subagent's progress panel: a header with the run's name,
// status and elapsed time, Jump (to its launch position) and Close; the Agent row's
// own body (ClaudeSubagentBody: prompt → timeline → Result), read from the run it is
// given on every render, so it refreshes as entries arrive; and an input box whose
// text goes to `onRelay`. The relay itself (wording, background_task, steer or prompt)
// is the caller's; the box needs claude's agent id, since only that names the agent.
// The panel fits whatever height its caller gives it: the header and
// the input box keep their size and the body between them scrolls, so a short space
// clips the body, never the header with Close. The input box is the main session's
// (ComposerBox,): the bordered row with Send inside it. Its top edge
// drags its height: see useClaudeSubagentPanelHeight.
import { useCallback, useRef, useState, type PointerEvent } from "react";
import { ComposerBox } from "./ComposerBox";
import { ClaudeSubagentBody, type ClaudeSubagentEntryRenderer } from "./ClaudeSubagentRow";
import { claudeSubagentLabel, useClaudeSubagentClock } from "./ClaudeSubagentRunningList";
import { useI18n } from "@/hooks/useI18n";
import { claudeSubagentElapsedMs, claudeSubagentStatParts, type ClaudeSubagentRun } from "@/lib/claude-subagent-view";
import { formatTurnDuration } from "@/lib/turn-duration";

const SUBAGENT_COLOR = "#8b5cf6";
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
    const panel = event.currentTarget.closest("[data-claude-subagent-panel]");
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

const HEADER_BUTTON = { flexShrink: 0, padding: "2px 8px", borderRadius: 5, border: "1px solid var(--border)", background: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 12 } as const;

export function ClaudeSubagentPanel({ run, renderEntry, onJump, onClose, onRelay, now: fixedNow }: {
  run: ClaudeSubagentRun;
  renderEntry: ClaudeSubagentEntryRenderer;
  onJump: () => void;
  onClose: () => void;
  /** Sends the text to the subagent; a rejection keeps the text in the box. */
  onRelay: (text: string) => Promise<void> | void;
  now?: number;
}) {
  const { t } = useI18n();
  const running = run.status === "running";
  const now = useClaudeSubagentClock(running, fixedNow);
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const label = claudeSubagentLabel(run);
  const elapsed = running ? claudeSubagentElapsedMs(run, now) : claudeSubagentStatParts(run.usage).durationMs;
  const canRelay = run.agentId !== undefined;
  const canSend = canRelay && !sending && text.trim() !== "";
  const { height, handleProps } = useClaudeSubagentPanelHeight();

  const send = async () => {
    if (!canSend) return;
    setSending(true);
    try {
      await onRelay(text);
      setText("");
    } catch {
      // The caller reports the failure; the text stays for another try.
    } finally {
      setSending(false);
    }
  };

  return (
    <section
      data-claude-subagent-panel={run.toolCallId}
      aria-label={label}
      style={{ display: "flex", flexDirection: "column", minWidth: 0, minHeight: 0, ...(height === null ? { maxHeight: "min(60vh, 560px)" } : { height }), overflow: "hidden", fontSize: 12, borderRadius: 8, border: "1px solid rgba(139,92,246,0.35)", borderLeftColor: run.status === "failed" ? "#f87171" : SUBAGENT_COLOR, background: "var(--bg-panel)", boxShadow: "0 6px 24px rgba(0,0,0,0.18)" }}
    >
      <div
        data-claude-subagent-panel-resize=""
        role="separator"
        aria-orientation="horizontal"
        aria-label={t("claudeSubagent.resizePanel")}
        title={t("claudeSubagent.resizePanel")}
        {...handleProps}
        style={{ display: "flex", alignItems: "center", justifyContent: "center", flexShrink: 0, height: 10, cursor: "row-resize", touchAction: "none" }}
      >
        <span aria-hidden="true" style={{ width: 32, height: 3, borderRadius: 2, background: "rgba(139,92,246,0.35)" }} />
      </div>
      <div style={{ display: "flex", alignItems: "center", gap: 7, flexShrink: 0, minWidth: 0, padding: "0 8px 6px 10px" }}>
        {run.agentType && (
          <span style={{ flexShrink: 0, padding: "0 6px", borderRadius: 4, background: "rgba(139,92,246,0.14)", color: SUBAGENT_COLOR, fontFamily: "var(--font-mono)", fontSize: 11, fontWeight: 600 }}>{run.agentType}</span>
        )}
        <span title={label} style={{ minWidth: 0, flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "var(--text)" }}>{label}</span>
        <span
          className={running ? "animate-[pulse_1.5s_infinite]" : undefined}
          style={{ flexShrink: 0, color: running ? SUBAGENT_COLOR : run.status === "failed" ? "#f87171" : "var(--text-dim)" }}
        >
          {t(`claudeSubagent.${run.status}`)}
        </span>
        {elapsed !== undefined && <span style={{ flexShrink: 0, color: "var(--text-dim)", fontVariantNumeric: "tabular-nums" }}>{formatTurnDuration(elapsed)}</span>}
        <button type="button" onClick={onJump} style={HEADER_BUTTON}>{t("claudeSubagent.jumpToLaunch")}</button>
        <button type="button" onClick={onClose} aria-label={t("claudeSubagent.closePanel")} title={t("claudeSubagent.closePanel")} style={{ ...HEADER_BUTTON, padding: "2px 6px", border: "none" }}>
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true">
            <line x1="2" y1="2" x2="8" y2="8" />
            <line x1="8" y1="2" x2="2" y2="8" />
          </svg>
        </button>
      </div>
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        <ClaudeSubagentBody run={run} renderEntry={renderEntry} />
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 4, flexShrink: 0, padding: "6px 8px 8px", borderTop: "1px solid rgba(139,92,246,0.2)" }}>
        {!canRelay && <div style={{ color: "var(--text-dim)" }}>{t("claudeSubagent.relayNeedsAgentId")}</div>}
        <ComposerBox
          textareaRef={inputRef}
          value={text}
          onChange={setText}
          onKeyDown={(event) => {
            if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing) return;
            event.preventDefault();
            void send();
          }}
          placeholder={t("claudeSubagent.relayPlaceholder")}
          disabled={!canRelay}
          send={{ label: t("claudeSubagent.relaySend"), active: canSend, disabled: !canSend, onClick: () => void send() }}
        />
      </div>
    </section>
  );
}
