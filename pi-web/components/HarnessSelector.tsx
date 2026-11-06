"use client";

import { useEffect, useRef, useState, type CSSProperties } from "react";
import { useIsMobile } from "@/hooks/useIsMobile";
import { CLAUDE_HARNESS, PI_HARNESS, type SessionHarness } from "@/lib/claude-shared";
import { SelectorRow } from "./SelectorRow";

interface HarnessSelectorProps {
  value: SessionHarness;
  onChange: (harness: SessionHarness) => void;
  /** Whether claude is on PATH; when not, its row is disabled and says why. */
  claudeAvailable: boolean;
  /** Visible reason under the disabled claude row. */
  unavailableReason: string;
  ariaLabel: string;
  disabled?: boolean;
}

/**
 * A fresh composer's harness picker: pi or claude, as a popover with the
 * same toolbar button and menu rows as the model selector beside it.
 */
export function HarnessSelector({ value, onChange, claudeAvailable, unavailableReason, ariaLabel, disabled = false }: HarnessSelectorProps) {
  const isMobile = useIsMobile();
  const rootRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [anchor, setAnchor] = useState<{ top: number; left: number; width: number } | null>(null);

  useEffect(() => {
    if (!open) return;
    const handleOutsideClick = (event: MouseEvent) => {
      const target = event.target as Node;
      if (rootRef.current?.contains(target) || panelRef.current?.contains(target)) return;
      setOpen(false);
    };
    document.addEventListener("mousedown", handleOutsideClick);
    return () => document.removeEventListener("mousedown", handleOutsideClick);
  }, [open]);

  useEffect(() => {
    if (disabled) setOpen(false);
  }, [disabled]);

  const choose = (harness: SessionHarness) => {
    setOpen(false);
    if (harness !== value) onChange(harness);
  };

  const buttonStyle: CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: 6,
    height: 32,
    padding: isMobile ? "8px 10px" : "8px 12px",
    border: "none",
    borderRadius: 9,
    background: open ? "var(--bg-hover)" : "none",
    color: "var(--text-muted)",
    cursor: disabled ? "not-allowed" : "pointer",
    fontSize: 12,
    opacity: disabled ? 0.5 : 1,
    flexShrink: 0,
    transition: "background 0.12s, color 0.12s",
  };

  return (
    <div
      ref={rootRef}
      className="harness-selector"
      style={{ position: "relative", flexShrink: 0 }}
      onKeyDown={(event) => {
        if (event.key !== "Escape" || !open) return;
        event.preventDefault();
        event.stopPropagation();
        setOpen(false);
      }}
    >
      <button
        type="button"
        aria-label={ariaLabel}
        aria-haspopup="listbox"
        aria-expanded={open}
        disabled={disabled}
        title={ariaLabel}
        style={buttonStyle}
        onClick={(event) => {
          const rect = event.currentTarget.getBoundingClientRect();
          setAnchor({ top: rect.top, left: rect.left, width: rect.width });
          setOpen((current) => !current);
        }}
        onMouseEnter={(event) => {
          if (disabled) return;
          event.currentTarget.style.background = "var(--bg-hover)";
          event.currentTarget.style.color = "var(--text)";
        }}
        onMouseLeave={(event) => {
          event.currentTarget.style.background = open ? "var(--bg-hover)" : "none";
          event.currentTarget.style.color = "var(--text-muted)";
        }}
      >
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={{ flexShrink: 0 }}>
          <polyline points="4 17 10 11 4 5" />
          <line x1="12" y1="19" x2="20" y2="19" />
        </svg>
        <span style={{ whiteSpace: "nowrap" }}>{value}</span>
      </button>

      {open && anchor && (
        <div
          ref={panelRef}
          role="listbox"
          aria-label={ariaLabel}
          style={{
            position: "fixed",
            top: anchor.top - 6,
            left: isMobile ? 8 : anchor.left,
            transform: "translateY(-100%)",
            zIndex: 500,
            minWidth: Math.max(anchor.width, 140),
            maxWidth: "calc(100vw - 16px)",
            display: "flex",
            flexDirection: "column",
            overflow: "hidden",
            border: "1px solid var(--border)",
            borderRadius: 8,
            background: "var(--bg)",
            boxShadow: "0 -4px 16px rgba(0,0,0,0.10)",
          }}
        >
          <HarnessOptions value={value} claudeAvailable={claudeAvailable} unavailableReason={unavailableReason} onSelect={choose} />
        </div>
      )}
    </div>
  );
}

/** The menu's rows: pi, and claude — disabled with its reason as visible text when not on PATH. */
export function HarnessOptions({ value, claudeAvailable, unavailableReason, onSelect }: {
  value: SessionHarness;
  claudeAvailable: boolean;
  unavailableReason: string;
  onSelect: (harness: SessionHarness) => void;
}) {
  return (
    <>
      <SelectorRow active={value === PI_HARNESS} onSelect={() => onSelect(PI_HARNESS)}>
        <span>{PI_HARNESS}</span>
      </SelectorRow>
      {claudeAvailable ? (
        <SelectorRow active={value === CLAUDE_HARNESS} onSelect={() => onSelect(CLAUDE_HARNESS)}>
          <span>{CLAUDE_HARNESS}</span>
        </SelectorRow>
      ) : (
        <>
          <button
            type="button"
            role="option"
            aria-selected={false}
            aria-disabled="true"
            disabled
            style={{
              display: "flex", alignItems: "center", gap: 8,
              width: "100%", padding: "7px 12px",
              border: "none", background: "none",
              color: "var(--text-dim)", cursor: "not-allowed",
              fontSize: 12, textAlign: "left", whiteSpace: "nowrap",
            }}
          >
            <span style={{ width: 10, flexShrink: 0 }} />
            <span>{CLAUDE_HARNESS}</span>
          </button>
          <div className="harness-unavailable" style={{ padding: "0 12px 7px 30px", color: "var(--text-dim)", fontSize: 11, whiteSpace: "normal", maxWidth: 260 }}>
            {unavailableReason}
          </div>
        </>
      )}
    </>
  );
}
