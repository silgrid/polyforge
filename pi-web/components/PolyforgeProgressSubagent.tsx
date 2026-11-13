"use client";

import { useState, type CSSProperties, type ReactNode } from "react";
import { progressSubagentHeader, type PolyforgeProgressLine, type PolyforgeProgressSubagent } from "@/lib/polyforge-cards-pure";
import { formatToolDuration } from "./ToolCallCard";
import { useI18n } from "@/hooks/useI18n";

const STATE_COLORS: Record<string, string> = {
  running: "var(--accent)",
  done: "#16a34a",
  error: "#f87171",
  stopped: "var(--text-dim)",
};

const headerStyle: CSSProperties = {
  display: "flex", alignItems: "baseline", gap: 6, minWidth: 0, width: "100%",
  padding: 0, background: "none", border: "none", color: "var(--text-muted)",
  cursor: "pointer", font: "inherit", textAlign: "left",
};
const badgeStyle: CSSProperties = {
  flexShrink: 0, padding: "0 5px", borderRadius: 4, border: "1px solid var(--border)",
  color: "var(--text-dim)", fontSize: 10, lineHeight: "14px",
};
const statStyle: CSSProperties = { flexShrink: 0, whiteSpace: "nowrap", fontSize: 10 };

/** The clock at render — the panel's clock-at-render pattern: the 2 s
 *  progress poll re-renders a running subagent, so its age stays live
 *  without a timer of its own. */
function renderNow(): number {
  return Date.now();
}

/**
 * One subagent of a progress tool line: an entry under its
 * parent call — the agent badge, the task description on one ellipsized
 * line (the full text on its title), its state (running / done / failed,
 * or stopped when its spawn block ended with it unfinished) and its
 * duration (the recorded one once finished, its live age while it runs) and
 * tool-call count. Collapsed by default; expanded, it shows the subagent's
 * own conversation as progress lines (`renderLine`: narration, tool cards
 * with their on-demand detail, nested subagents) — the latest poll's, so a
 * running one grows in place — or says it is too large to show (`omitted`). Each toggle is a fold (`onToggle`). The root
 * carries `data-pf-progress-kind="subagent"`,
 * `data-pf-progress-subagent-id` and `data-pf-progress-subagent-status`.
 */
export function PolyforgeProgressSubagentEntry({ run, live, renderLine, onToggle }: {
  run: PolyforgeProgressSubagent;
  live: boolean;
  renderLine: (line: PolyforgeProgressLine) => ReactNode;
  onToggle: () => void;
}) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const header = progressSubagentHeader(run, renderNow(), live);
  const toggle = () => {
    setExpanded(!expanded);
    onToggle();
  };
  return (
    <div
      data-pf-progress-kind="subagent"
      data-pf-progress-subagent-id={run.id}
      data-pf-progress-subagent-status={header.state}
      style={{ marginLeft: 12, paddingLeft: 8, borderLeft: "2px solid var(--border)", display: "flex", flexDirection: "column", gap: 4, minWidth: 0 }}
    >
      <button type="button" data-pf-progress-subagent-toggle="" aria-expanded={expanded} title={run.description} onClick={toggle} style={headerStyle}>
        <span style={{ flexShrink: 0 }}>{expanded ? "▾" : "▸"}</span>
        <span style={badgeStyle}>{run.agent || t("polyforge.progress.subagent")}</span>
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "var(--text)" }}>{header.description}</span>
        <span data-pf-progress-subagent-state="" style={{ ...statStyle, color: STATE_COLORS[header.state] }}>{t(`polyforge.progress.subagentStatus.${header.state}`)}</span>
        {header.durationMs !== undefined && <span style={{ ...statStyle, color: "var(--text-dim)" }}>{formatToolDuration(header.durationMs)}</span>}
        <span style={{ ...statStyle, color: "var(--text-dim)" }}>{t("polyforge.progress.subagentTools", { count: header.tools })}</span>
      </button>
      {expanded && (
        <div data-pf-progress-subagent-body="" style={{ display: "flex", flexDirection: "column", gap: 4, minWidth: 0 }}>
          {run.lines.length === 0
            ? <span style={{ color: "var(--text-dim)", fontStyle: "italic" }}>{t(run.omitted ? "polyforge.progress.subagentOmitted" : "polyforge.progress.subagentEmpty")}</span>
            : run.lines.map((line) => renderLine(line))}
        </div>
      )}
    </div>
  );
}
