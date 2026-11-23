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
// (ComposerBox): the bordered row with Send inside it. Its top edge
// drags its height: see useClaudeSubagentPanelHeight.
//
// polyforge#255: the floating section, the top-edge drag and the Esc rule are
// the shared SubagentPanelFrame's, so a pi subagent's panel (PiSubagentPanel)
// is the same shell with its own header, transcript and input rules.
import { useRef, useState } from "react";
import { ComposerBox } from "./ComposerBox";
import { ClaudeSubagentBody, type ClaudeSubagentEntryRenderer } from "./ClaudeSubagentRow";
import { claudeSubagentLabel, useClaudeSubagentClock } from "./ClaudeSubagentRunningList";
import { SubagentPanelFrame } from "./SubagentPanelFrame";
import { useI18n } from "@/hooks/useI18n";
import { claudeSubagentElapsedMs, claudeSubagentStatParts, type ClaudeSubagentRun } from "@/lib/claude-subagent-view";
import { formatTurnDuration } from "@/lib/turn-duration";

export {
  isClaudeSubagentPanelEscape,
  CLAUDE_SUBAGENT_PANEL_HEIGHT_KEY,
  CLAUDE_SUBAGENT_PANEL_MIN_HEIGHT,
} from "./SubagentPanelFrame";
export type { ClaudeSubagentPanelKey } from "./SubagentPanelFrame";

const SUBAGENT_COLOR = "#8b5cf6";

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
    <SubagentPanelFrame
      panelAttrs={{ "data-claude-subagent-panel": run.toolCallId }}
      ariaLabel={label}
      accent={SUBAGENT_COLOR}
      borderColor="rgba(139,92,246,0.35)"
      failed={run.status === "failed"}
      resizeLabel={t("claudeSubagent.resizePanel")}
      header={(
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
      )}
      footer={(
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
      )}
    >
      <ClaudeSubagentBody run={run} renderEntry={renderEntry} />
    </SubagentPanelFrame>
  );
}
