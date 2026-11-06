"use client";

import { useLayoutEffect, type CSSProperties, type KeyboardEvent, type RefObject } from "react";
import { useIsMobile } from "@/hooks/useIsMobile";

/** The main composer's resting border colour. */
export const COMPOSER_BOX_BORDER = "color-mix(in srgb, var(--border) 70%, transparent)";

/**
 * The main session composer's bordered row: the one style
 * source of every Pi Web input box — ChatInput's row, the job card's composer
 * and the subagent panel's box. `borderColor` overrides the resting border
 * (ChatInput's bash / steer states, the job composer's awaiting amber).
 */
export function composerBoxStyle(isMobile: boolean, borderColor: string = COMPOSER_BOX_BORDER): CSSProperties {
  return {
    background: "var(--bg)",
    border: `1px solid ${borderColor}`,
    borderRadius: 14,
    padding: isMobile ? "6px 6px 6px 12px" : "10px 10px 10px 14px",
    boxShadow: "0 1px 2px rgba(15,23,42,0.04), 0 8px 24px -12px rgba(15,23,42,0.10)",
    transition: "border-color 0.15s, background 0.15s, box-shadow 0.15s",
  };
}

/** The main composer's send button: accent while there is something to send, icon-only on a phone. */
export function composerSendButtonStyle(active: boolean, iconOnly: boolean): CSSProperties {
  return {
    flexShrink: 0,
    alignSelf: "flex-end",
    display: "flex", alignItems: "center", justifyContent: "center", gap: 6,
    // Mobile: icon-only so the placeholder and draft keep the width.
    ...(iconOnly ? { width: 36, height: 36, padding: 0 } : { padding: "7px 14px" }),
    background: active ? "var(--accent)" : "var(--bg-panel)",
    border: "none",
    borderRadius: 8,
    color: active ? "var(--accent-contrast)" : "var(--text-dim)",
    cursor: active ? "pointer" : "not-allowed",
    fontSize: 13,
    fontWeight: 600,
    letterSpacing: "-0.01em",
    boxShadow: active ? "0 1px 3px color-mix(in srgb, var(--accent) 25%, transparent)" : "none",
    transition: "background 0.15s, box-shadow 0.15s",
  };
}

/**
 * The composer's auto-grow step: fit the text to 200 px, then let a scrollbar
 * appear only when the text is still taller than the box.
 *
 * The height comes from the value, not from scrollHeight: a wrapped
 * placeholder counts toward scrollHeight too, so an empty box would grow to
 * its own hint's lines. Only typed text scrolls, so a placeholder that
 * overflows the box stays cut off instead of showing a scrollbar.
 */
export function autoGrowTextarea(ta: HTMLTextAreaElement): void {
  ta.style.height = "auto";
  ta.style.height = `${ta.value ? Math.min(ta.scrollHeight, 200) : ta.clientHeight}px`;
  ta.style.overflowY = ta.value && ta.scrollHeight > ta.clientHeight ? "auto" : "hidden";
}

/** The send button's arrow. */
export function ComposerSendIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <line x1="2" y1="7" x2="11" y2="7" />
      <polyline points="7.5 3 12 7 7.5 11" />
    </svg>
  );
}

type DataAttributes = Record<`data-${string}`, string>;

/**
 * An input box in the main composer's look: the bordered row with a
 * borderless textarea that grows with its text (to 200px) and the send button
 * inside it at the bottom right. Presentational only — the caller owns the
 * text, the keys and the send.
 */
export function ComposerBox({
  textareaRef,
  value,
  onChange,
  onKeyDown,
  placeholder,
  disabled,
  borderColor,
  inputData,
  send,
}: {
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  value: string;
  onChange: (value: string) => void;
  onKeyDown: (event: KeyboardEvent<HTMLTextAreaElement>) => void;
  /** The placeholder, also the textarea's accessible name. */
  placeholder: string;
  disabled?: boolean;
  borderColor?: string;
  inputData?: DataAttributes;
  send: { label: string; active: boolean; disabled: boolean; onClick: () => void; data?: DataAttributes };
}) {
  const isMobile = useIsMobile();
  useLayoutEffect(() => {
    if (textareaRef.current) autoGrowTextarea(textareaRef.current);
  }, [textareaRef, value]);
  return (
    <div
      data-composer-box=""
      style={{ minWidth: 0, display: "flex", flexDirection: "row", gap: 8, alignItems: "center", ...composerBoxStyle(isMobile, borderColor) }}
    >
      <textarea
        ref={textareaRef}
        {...inputData}
        value={value}
        rows={1}
        disabled={disabled}
        placeholder={placeholder}
        aria-label={placeholder}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={onKeyDown}
        style={{
          flex: 1, minWidth: 0, width: "100%",
          background: "none", border: "none", outline: "none", resize: "none",
          color: "var(--text)", fontSize: "var(--chat-content-font-size, 14px)", lineHeight: 1.6, fontFamily: "inherit",
          minHeight: 24, maxHeight: 200, overflow: "auto",
        }}
      />
      <button
        type="button"
        {...send.data}
        disabled={send.disabled}
        onClick={send.onClick}
        title={send.label}
        aria-label={send.label}
        style={composerSendButtonStyle(send.active, isMobile)}
      >
        <ComposerSendIcon />
        {!isMobile && send.label}
      </button>
    </div>
  );
}
