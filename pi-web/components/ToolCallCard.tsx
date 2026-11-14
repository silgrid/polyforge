"use client";

import type { HTMLAttributes, ReactNode } from "react";

/** Below this a tool card shows no time: it would read 0.0s on every quick read or search. */
export const TOOL_DURATION_MIN_MS = 100;

/** A tool's run time as the pi CLI's "Took" writes it: 2.4s, then 3m 5s, then 1h 2m 5s. */
export function formatToolDuration(ms: number): string {
  const seconds = ms / 1000;
  if (seconds < 60) return `${seconds.toFixed(1)}s`;
  const totalSeconds = Math.floor(seconds);
  const minutes = Math.floor(totalSeconds / 60);
  const remainder = totalSeconds % 60;
  if (minutes < 60) return `${minutes}m ${remainder}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m ${remainder}s`;
}

/** The card's border tone: green for a call that has not failed, red for one that has. */
export function toolCallCardTone(isError: boolean): { border: string; background: string; divider: string } {
  return isError
    ? { border: "1px solid rgba(248,113,113,0.45)", background: "rgba(248,113,113,0.05)", divider: "1px solid rgba(248,113,113,0.25)" }
    : { border: "1px solid rgba(34,197,94,0.25)", background: "rgba(34,197,94,0.04)", divider: "1px solid rgba(34,197,94,0.2)" };
}

/** A header stat beside the preview (a count, a status, a duration): small, dim, never shrinking. */
export function ToolCallCardStat({ children, color }: { children: ReactNode; color?: string }) {
  return <span style={{ fontSize: 11, color: color ?? "var(--text-dim)", flexShrink: 0, fontVariantNumeric: "tabular-nums" }}>{children}</span>;
}

export interface ToolCallCardProps extends Omit<HTMLAttributes<HTMLDivElement>, "children" | "title"> {
  isError: boolean;
  expanded: boolean;
  onToggle: () => void;
  /** The tool's name (mono, bold, in the card's tone). */
  name: ReactNode;
  nameTitle?: string;
  /** One line after the name, ellipsized by CSS. */
  preview: ReactNode;
  /** The header button's hover text (the full header when the preview is cut). */
  headerTitle?: string;
  /** Stats between the preview and the chevron, in order. */
  stats?: ReactNode;
  /** A control beside the header button (the subagent tab opener). */
  aside?: ReactNode;
  /** The expanded body, under the header. */
  children?: ReactNode;
}

/**
 * The tool card shell of the session transcript (extracted it
 * from MessageView's ToolCallBlock, which renders its rich body inside it):
 * a rounded bordered card in the call's tone, one header button
 * (`aria-expanded`) — name, a single ellipsized preview line, the stats and
 * the chevron. The Polyforge job detail's progress tool cards use the same
 * shell, so both surfaces draw one card language.
 */
export function ToolCallCard({ isError, expanded, onToggle, name, nameTitle, preview, headerTitle, stats, aside, children, style, ...rest }: ToolCallCardProps) {
  const tone = toolCallCardTone(isError);
  return (
    <div {...rest} style={{ borderRadius: 7, overflow: "hidden", fontSize: 12, border: tone.border, background: tone.background, ...style }}>
      <div style={{ display: "flex", alignItems: "stretch", minWidth: 0 }}>
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={expanded}
          title={headerTitle}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 7,
            flex: 1,
            minWidth: 0,
            padding: "6px 10px",
            background: "none",
            border: "none",
            color: "var(--text-muted)",
            cursor: "pointer",
            fontSize: 12,
            textAlign: "left",
          }}
        >
          <span
            title={nameTitle}
            style={{ color: isError ? "#f87171" : "#16a34a", fontFamily: "var(--font-mono)", fontWeight: 600, fontSize: 11, flexShrink: 0 }}
          >
            {name}
          </span>
          <span style={{ color: "var(--text-dim)", fontFamily: "var(--font-mono)", fontSize: 11, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", flex: 1, minWidth: 0 }}>
            {preview}
          </span>
          {stats}
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="var(--text-dim)" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, transform: expanded ? "rotate(180deg)" : "none", transition: "transform 0.15s" }}>
            <polyline points="2 3.5 5 6.5 8 3.5" />
          </svg>
        </button>
        {aside}
      </div>
      {children}
    </div>
  );
}
