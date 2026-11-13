"use client";

// A claude subagent run as a first-class Agent row: one header line
// (type badge, description, live status or `N tools · duration · tokens`) that expands
// in place into the prompt, an indented timeline rendered by the chat's own message
// view, and the Result. Collapsed, nothing of the body is mounted. Beside the header,
// where the chat provides ClaudeSubagentPanelContext, an Open panel button opens the
// run's progress panel, running or finished. Nested Agent calls
// in the timeline come back here through the same message view. Its model and rules
// live in lib/claude-subagent-view.ts; this file holds no claude format knowledge.
import { createContext, useContext, useState, type ReactNode } from "react";
import { MarkdownBody } from "./MarkdownBody";
import { useI18n } from "@/hooks/useI18n";
import {
  CLAUDE_SUBAGENT_PAGE_SIZE,
  claudeSubagentBody,
  claudeSubagentStatParts,
  formatClaudeSubagentTokens,
  isClaudeSubagentExpanded,
  setClaudeSubagentManualExpansion,
  type ClaudeSubagentRun,
  type ClaudeSubagentRuns,
} from "@/lib/claude-subagent-view";
import { formatTurnDuration } from "@/lib/turn-duration";
import type { AgentMessage, ToolResultMessage } from "@/lib/types";

/** The session's runs by tool call id; empty outside a claude session. */
export const ClaudeSubagentContext = createContext<ClaudeSubagentRuns>({});

/** Opens a run's progress panel by its tool call id; null where no panel exists. */
export const ClaudeSubagentPanelContext = createContext<((toolCallId: string) => void) | null>(null);

/** Renders one timeline entry with the chat's own message view (passed in: this row does not import it). */
export type ClaudeSubagentEntryRenderer = (message: AgentMessage, toolResults: Map<string, ToolResultMessage>, key: string) => ReactNode;

const SUBAGENT_COLOR = "#8b5cf6";
const SCROLL_BLOCK = { margin: 0, maxHeight: 320, overflow: "auto", whiteSpace: "pre-wrap", wordBreak: "break-word" } as const;

export function ClaudeSubagentRow({ run, toolName, renderEntry }: { run: ClaudeSubagentRun; toolName: string; renderEntry: ClaudeSubagentEntryRenderer }) {
  const { t, locale } = useI18n();
  // Opens this run's progress panel, running or finished; absent outside the chat.
  const openPanel = useContext(ClaudeSubagentPanelContext);
  // Re-rendered on a toggle; the choice itself is kept in page memory so it survives remounts.
  const [, setToggles] = useState(0);
  const expanded = isClaudeSubagentExpanded(run);
  const toggle = () => {
    setClaudeSubagentManualExpansion(run.toolCallId, !expanded);
    setToggles((count) => count + 1);
  };
  const stats = claudeSubagentStatParts(run.usage);
  const statText = [
    stats.toolUses === undefined ? null : stats.toolUses === 1 ? t("claudeSubagent.toolCountOne") : t("claudeSubagent.toolCount", { count: stats.toolUses }),
    stats.durationMs === undefined ? null : formatTurnDuration(stats.durationMs),
    stats.totalTokens === undefined ? null : t("claudeSubagent.tokens", { count: formatClaudeSubagentTokens(stats.totalTokens, locale) }),
  ].filter((part): part is string => part !== null).join(" · ");
  const failed = run.status === "failed";

  return (
    <div
      data-claude-subagent={run.toolCallId}
      style={{
        borderRadius: 7,
        fontSize: 12,
        border: failed ? "1px solid rgba(248,113,113,0.45)" : "1px solid rgba(139,92,246,0.3)",
        borderLeft: `3px solid ${failed ? "#f87171" : SUBAGENT_COLOR}`,
        background: "rgba(139,92,246,0.04)",
        minWidth: 0,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", minWidth: 0 }}>
        <button
          type="button"
          aria-expanded={expanded}
          onClick={toggle}
          // A toggle leaves the focus where it was (the composer keeps its caret).
          onMouseDown={(event) => event.preventDefault()}
          style={{ display: "flex", alignItems: "center", gap: 7, flex: 1, width: "100%", minWidth: 0, padding: "6px 10px", background: "none", border: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 12, textAlign: "left" }}
        >
          <span style={{ flexShrink: 0, padding: "0 6px", borderRadius: 4, background: "rgba(139,92,246,0.14)", color: SUBAGENT_COLOR, fontFamily: "var(--font-mono)", fontSize: 11, fontWeight: 600 }}>
            {run.agentType ?? toolName}
          </span>
          <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "var(--text)" }}>{run.description ?? toolName}</span>
          <span style={{ flex: 1 }} />
          {run.status === "running" ? (
            <>
              {run.progress && <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "var(--text-dim)" }}>{run.progress}</span>}
              <span className="animate-[pulse_1.5s_infinite]" style={{ flexShrink: 0, color: SUBAGENT_COLOR }}>{t("claudeSubagent.running")}</span>
            </>
          ) : (
            <>
              {run.status !== "completed" && <span style={{ flexShrink: 0, color: failed ? "#f87171" : "var(--text-dim)" }}>{t(`claudeSubagent.${run.status}`)}</span>}
              {statText && <span style={{ flexShrink: 0, color: "var(--text-dim)", fontVariantNumeric: "tabular-nums" }}>{statText}</span>}
            </>
          )}
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="var(--text-dim)" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, transform: expanded ? "rotate(180deg)" : "none", transition: "transform 0.15s" }}>
            <polyline points="2 3.5 5 6.5 8 3.5" />
          </svg>
        </button>
        {openPanel && (
          <button
            type="button"
            onClick={() => openPanel(run.toolCallId)}
            // Like the toggle, opening the panel leaves the focus where it was.
            onMouseDown={(event) => event.preventDefault()}
            style={{ flexShrink: 0, marginRight: 6, padding: "1px 7px", borderRadius: 5, border: "1px solid rgba(139,92,246,0.3)", background: "none", color: SUBAGENT_COLOR, cursor: "pointer", fontSize: 11, whiteSpace: "nowrap" }}
          >
            {t("claudeSubagent.openPanel")}
          </button>
        )}
      </div>
      {expanded && <ClaudeSubagentBody run={run} renderEntry={renderEntry} />}
    </div>
  );
}

/** A run's body: prompt → timeline → Result. The expanded row and the progress panel both render it. */
export function ClaudeSubagentBody({ run, renderEntry }: { run: ClaudeSubagentRun; renderEntry: ClaudeSubagentEntryRenderer }) {
  const { t } = useI18n();
  const [showAll, setShowAll] = useState(false);
  const body = claudeSubagentBody(run);
  const toolResults = new Map<string, ToolResultMessage>();
  for (const entry of run.entries) {
    const message = entry.message as unknown as AgentMessage;
    if (message.role === "toolResult") toolResults.set(message.toolCallId, message);
  }
  const shown = showAll ? body.steps : body.steps.slice(0, CLAUDE_SUBAGENT_PAGE_SIZE);
  const hidden = body.steps.length - shown.length;
  return (
    <div style={{ padding: "4px 10px 8px", borderTop: "1px solid rgba(139,92,246,0.2)", display: "flex", flexDirection: "column", gap: 6 }}>
      {run.prompt && (
        <div>
          <div style={{ color: "var(--text-dim)", fontSize: 11, marginBottom: 2 }}>{t("claudeSubagent.prompt")}</div>
          <pre style={{ ...SCROLL_BLOCK, maxHeight: 200, padding: "6px 8px", borderRadius: 5, background: "var(--bg-subtle)", color: "var(--text-muted)", fontFamily: "inherit", fontSize: "calc(12px + var(--chat-font-size-offset, 0px))" }}>{run.prompt}</pre>
        </div>
      )}
      {shown.length > 0 && (
        <div style={{ paddingLeft: 10, borderLeft: "2px solid rgba(139,92,246,0.25)", display: "flex", flexDirection: "column", gap: 4, minWidth: 0 }}>
          {shown.map((entry) => renderEntry(entry.message as unknown as AgentMessage, toolResults, entry.id))}
          {hidden > 0 && (
            <button type="button" onClick={() => setShowAll(true)} style={{ alignSelf: "flex-start", padding: "2px 0", background: "none", border: "none", color: SUBAGENT_COLOR, cursor: "pointer", fontSize: 12 }}>
              {t("claudeSubagent.showMore", { count: hidden })}
            </button>
          )}
        </div>
      )}
      {body.settled && (
        <div>
          <div style={{ color: "var(--text-dim)", fontSize: 11, marginBottom: 2 }}>{t("claudeSubagent.result")}</div>
          {body.result === null ? (
            <div style={{ color: "var(--text-dim)", fontStyle: "italic" }}>{t("claudeSubagent.noResult")}</div>
          ) : (
            <div style={{ ...SCROLL_BLOCK, whiteSpace: "normal" }}>
              <MarkdownBody>{body.result}</MarkdownBody>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
