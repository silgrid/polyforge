"use client";

import { memo, useState, type CSSProperties } from "react";
import type { PolyforgeProgressLine, PolyforgeProgressToolStatus } from "@/lib/polyforge-cards-pure";
import { TOOL_DURATION_MIN_MS, ToolCallCard, ToolCallCardStat, formatToolDuration, toolCallCardTone } from "./ToolCallCard";
import { useI18n } from "@/hooks/useI18n";

/** The collapsed card header's visible length in code points;
 *  the full header text is on the header's title and in the expanded body. */
export const PROGRESS_TOOL_HEADER_MAX = 120;

/** One call's on-demand detail as the card holds it. */
export type PolyforgeProgressToolDetailState =
  | { status: "loading" }
  | { status: "unavailable" }
  | { status: "available"; input: string; output: string | null; isError: boolean };

/** `GET /api/polyforge/progress/tool`'s body → the card's detail state: only
 *  a well-formed `available: true` answer is a detail; anything else — a
 *  degraded answer, a refusal, a malformed body — is unavailable. Pure. */
export function parseProgressToolDetail(body: unknown): PolyforgeProgressToolDetailState {
  if (!body || typeof body !== "object") return { status: "unavailable" };
  const answer = body as Record<string, unknown>;
  const tool = answer.tool as Record<string, unknown> | undefined;
  if (answer.available !== true || !tool || typeof tool !== "object") return { status: "unavailable" };
  if (typeof tool.input !== "string" || (tool.output !== null && typeof tool.output !== "string")) return { status: "unavailable" };
  return { status: "available", input: tool.input, output: tool.output as string | null, isError: tool.isError === true };
}

/** The on-demand tool detail route. */
export const PROGRESS_TOOL_URL = "/api/polyforge/progress/tool";

/** One call's detail read: `GET PROGRESS_TOOL_URL?wi=&id=[&profile=]` once
 *  (`profile` = the job row's Cell,), settled as the card's
 *  state — an HTTP refusal, a network error or a malformed body is
 *  unavailable. Never rejects. */
export async function fetchProgressToolDetail(wi: string, id: string, profile?: string): Promise<PolyforgeProgressToolDetailState> {
  try {
    const cell = profile ? `&profile=${encodeURIComponent(profile)}` : "";
    const response = await fetch(`${PROGRESS_TOOL_URL}?wi=${encodeURIComponent(wi)}&id=${encodeURIComponent(id)}${cell}`, { cache: "no-store" });
    return parseProgressToolDetail(response.ok ? await response.json() : null);
  } catch {
    return { status: "unavailable" };
  }
}

/** A tool line's header: its name, its one-line preview (the argument with
 *  whitespace collapsed, cut with `…` so name + `: ` + preview stay within
 *  PROGRESS_TOOL_HEADER_MAX code points) and the full header text. A line
 *  without the light fields (an older server) shows its text as the name. Pure. */
export function progressToolHeader(line: PolyforgeProgressLine): { name: string; preview: string; full: string; status?: PolyforgeProgressToolStatus; durationMs?: number } {
  const tool = line.tool;
  if (!tool) return { name: line.text, preview: "", full: line.text };
  const name = tool.name;
  const argument = (tool.argument ?? "").replace(/\s+/g, " ").trim();
  const budget = Math.max(0, PROGRESS_TOOL_HEADER_MAX - Array.from(name).length - 2);
  const points = Array.from(argument);
  const preview = points.length <= budget ? argument : `${points.slice(0, Math.max(0, budget - 1)).join("")}…`;
  return { name, preview, full: tool.argument ? `${name}: ${tool.argument}` : name, status: tool.status, ...(tool.durationMs !== undefined ? { durationMs: tool.durationMs } : {}) };
}

const STATUS_COLORS: Record<PolyforgeProgressToolStatus, string> = {
  ok: "#16a34a",
  error: "#f87171",
  running: "var(--accent)",
};

const bodyText: CSSProperties = {
  margin: 0,
  padding: "6px 10px",
  color: "var(--text-muted)",
  fontFamily: "var(--font-mono)",
  fontSize: 11,
  lineHeight: 1.5,
  whiteSpace: "pre-wrap",
  overflowWrap: "anywhere",
};
const sectionLabel: CSSProperties = { padding: "4px 10px 0", color: "var(--text-dim)", fontSize: 10, textTransform: "uppercase", letterSpacing: 0.4 };

/**
 * One tool call of the job detail's execution progress: the
 * transcript's ToolCallCard shell — name, the 120-code-point preview with the
 * full header on hover, `ok` / `error` / `running` and the duration (only
 * when the poll carries one, with the transcript's 100 ms floor). Collapsed
 * by default; every toggle is reported to the panel (`onToggle`, which
 * re-measures the follow state and, on an expansion, asks for that one call's
 * detail at most once) and the body shows it inline —
 * the complete masked input and output, wrapping, no scroller of its own —
 * or states that there is no result yet, or that the detail is unavailable
 * (that card only; the stream is untouched). The root carries
 * `data-pf-progress-kind="tool"`.
 */
export const PolyforgeProgressToolCard = memo(function PolyforgeProgressToolCard({ line, detail, onToggle }: {
  line: PolyforgeProgressLine;
  detail: PolyforgeProgressToolDetailState | undefined;
  onToggle: (id: string, open: boolean) => void;
}) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const header = progressToolHeader(line);
  const isError = header.status === "error";
  const divider = toolCallCardTone(isError).divider;
  const toggle = () => {
    setExpanded(!expanded);
    onToggle(line.id, !expanded);
  };
  return (
    <ToolCallCard
      data-pf-progress-kind="tool"
      isError={isError}
      expanded={expanded}
      onToggle={toggle}
      name={header.name}
      preview={header.preview}
      headerTitle={header.full}
      stats={<>
        {header.status && <ToolCallCardStat color={STATUS_COLORS[header.status]}>{t(`polyforge.progress.toolStatus.${header.status}`)}</ToolCallCardStat>}
        {header.durationMs !== undefined && header.durationMs >= TOOL_DURATION_MIN_MS && <ToolCallCardStat>{formatToolDuration(header.durationMs)}</ToolCallCardStat>}
      </>}
      style={{ minWidth: 0, flexShrink: 0 }}
    >
      {expanded && (
        <div style={{ borderTop: divider, background: "var(--bg-subtle)" }}>
          {!detail || detail.status === "loading" ? (
            <div style={{ ...bodyText, color: "var(--text-dim)" }}>{t("polyforge.loading")}</div>
          ) : detail.status === "unavailable" ? (
            <div style={{ ...bodyText, color: "var(--text-dim)", fontStyle: "italic" }}>{t("polyforge.progress.toolUnavailable")}</div>
          ) : (
            <>
              <div style={sectionLabel}>{t("polyforge.progress.toolInput")}</div>
              <pre style={bodyText}>{detail.input}</pre>
              <div style={{ ...sectionLabel, borderTop: divider }}>{t("polyforge.progress.toolOutput")}</div>
              {detail.output === null ? (
                <div style={{ ...bodyText, color: "var(--text-dim)", fontStyle: "italic" }}>{t("polyforge.progress.toolNoResult")}</div>
              ) : (
                <pre style={{ ...bodyText, background: "var(--bg)", color: detail.isError ? "#f87171" : "var(--text-muted)" }}>{detail.output}</pre>
              )}
            </>
          )}
        </div>
      )}
    </ToolCallCard>
  );
});
