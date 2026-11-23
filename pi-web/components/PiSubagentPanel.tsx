"use client";

// A pi subagent run's progress panel (polyforge#255): the shared
// SubagentPanelFrame with this run's header (profile, description, status,
// elapsed), its child transcript rendered read-only through the chat's own
// MessageView — so new entries refresh the body while it is open — and an
// input box that steers the run through the existing subagents route while it
// runs and is disabled with the settled reason once it ended.
import { useEffect, useMemo, useRef, useState } from "react";
import { ComposerBox } from "./ComposerBox";
import { MarkdownBody } from "./MarkdownBody";
import { MessageView } from "./MessageView";
import { SubagentPanelFrame } from "./SubagentPanelFrame";
import { useClaudeSubagentClock } from "./ClaudeSubagentRunningList";
import { useI18n } from "@/hooks/useI18n";
import {
  piSubagentCanSteer,
  piSubagentElapsedMs,
  piSubagentInputReason,
  piSubagentIsActive,
  piSubagentStatusLabelKey,
  type PiSubagentEntry,
  type PiSubagentPanelPhase,
  type PiSubagentRunView,
} from "@/lib/pi-subagent-panel";
import { formatTurnDuration } from "@/lib/turn-duration";
import type { AgentMessage, ToolResultMessage } from "@/lib/types";

/** The pi subagent's colour: the same violet the running list and its Agent card use. */
const SUBAGENT_COLOR = "#8b5cf6";

const HEADER_BUTTON = { flexShrink: 0, padding: "2px 8px", borderRadius: 5, border: "1px solid var(--border)", background: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 12 } as const;

/** Every tool result in the transcript, by its call id: what MessageView pairs a call with. */
function toolResultsOf(entries: readonly PiSubagentEntry[]): Map<string, ToolResultMessage> {
  const results = new Map<string, ToolResultMessage>();
  for (const entry of entries) {
    const message = entry.message as unknown as AgentMessage;
    if (message.role === "toolResult") results.set((message as unknown as ToolResultMessage).toolCallId, message as unknown as ToolResultMessage);
  }
  return results;
}

/**
 * The panel. `run` is the latest read's run (Q8): the header's status,
 * description and elapsed come from it, never from a history card. The body
 * is the transcript as loaded so far; a new entry re-renders it in place, so
 * the open panel follows the run without a reload.
 */
export function PiSubagentPanel({
  sessionId,
  run,
  phase,
  error,
  entries,
  hasEarlier,
  loadingEarlier,
  steerError,
  launchToolCallId,
  cwd,
  onLoadEarlier,
  onJump,
  onClose,
  onSteer,
  now: fixedNow,
}: {
  sessionId: string;
  run: PiSubagentRunView | null;
  phase: PiSubagentPanelPhase;
  error: string | null;
  entries: readonly PiSubagentEntry[];
  hasEarlier: boolean;
  loadingEarlier: boolean;
  steerError: string | null;
  /** The Agent call in this chat that leads to the child; null disables Jump (Q6). */
  launchToolCallId: string | null;
  cwd: string | null;
  onLoadEarlier: () => void;
  onJump: () => void;
  onClose: () => void;
  /** Sends the text through the subagents steer route; false keeps the draft. */
  onSteer: (text: string) => Promise<boolean>;
  now?: number;
}) {
  const { t } = useI18n();
  const status = run?.status ?? "unknown";
  const active = piSubagentIsActive(status);
  const now = useClaudeSubagentClock(status === "running", fixedNow);
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const lastEntryId = entries.at(-1)?.id ?? null;
  const seenLastRef = useRef<string | null>(null);
  const toolResults = useMemo(() => toolResultsOf(entries), [entries]);
  const elapsed = run ? piSubagentElapsedMs(run, now) : undefined;
  const reason = piSubagentInputReason(status, phase);
  const canRelay = phase === "ready" && piSubagentCanSteer(status);
  const canSend = canRelay && !sending && text.trim() !== "";
  const title = run?.description || run?.profile || t("piSubagent.status.unknown");
  const jumpEnabled = launchToolCallId !== null;

  // A new entry scrolls the body to it; an earlier page prepended keeps the
  // reader where the loaded history began.
  useEffect(() => {
    const body = bodyRef.current;
    if (!body) return;
    // The first loaded snapshot opens at its newest entry (the run's latest
    // step); later entries scroll along, an earlier page prepended keeps the
    // reader where the loaded history began.
    if (lastEntryId !== null && lastEntryId !== seenLastRef.current) {
      body.scrollTop = body.scrollHeight;
    }
    seenLastRef.current = lastEntryId;
  }, [lastEntryId]);

  const send = async () => {
    if (!canSend) return;
    setSending(true);
    try {
      const sent = await onSteer(text);
      if (sent) setText("");
    } finally {
      setSending(false);
    }
  };

  return (
    <SubagentPanelFrame
      panelAttrs={{ "data-pi-subagent-panel": sessionId, "data-pi-subagent-status": status }}
      ariaLabel={title}
      accent={SUBAGENT_COLOR}
      borderColor="rgba(139,92,246,0.35)"
      status={status}
      resizeLabel={t("piSubagent.resizePanel")}
      header={(
        <div style={{ display: "flex", alignItems: "center", gap: 7, flexShrink: 0, minWidth: 0, padding: "0 8px 6px 10px" }}>
          {run?.profile && (
            <span style={{ flexShrink: 0, padding: "0 6px", borderRadius: 4, background: "rgba(139,92,246,0.14)", color: SUBAGENT_COLOR, fontFamily: "var(--font-mono)", fontSize: 11, fontWeight: 600 }}>{run.profile}</span>
          )}
          <span title={title} style={{ minWidth: 0, flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "var(--text)" }}>{title}</span>
          <span
            className={status === "running" ? "animate-[pulse_1.5s_infinite]" : undefined}
            style={{ flexShrink: 0, color: status === "running" ? SUBAGENT_COLOR : status === "failed" ? "#f87171" : "var(--text-dim)" }}
          >
            {t(piSubagentStatusLabelKey(status))}
          </span>
          {elapsed !== undefined && <span style={{ flexShrink: 0, color: "var(--text-dim)", fontVariantNumeric: "tabular-nums" }}>{formatTurnDuration(elapsed)}</span>}
          <button
            type="button"
            onClick={onJump}
            disabled={!jumpEnabled}
            title={jumpEnabled ? t("piSubagent.jumpToLaunch") : t("piSubagent.jumpUnavailable")}
            style={{ ...HEADER_BUTTON, cursor: jumpEnabled ? "pointer" : "default", opacity: jumpEnabled ? 1 : 0.55 }}
          >
            {t("piSubagent.jumpToLaunch")}
          </button>
          <button type="button" onClick={onClose} aria-label={t("piSubagent.closePanel")} title={t("piSubagent.closePanel")} style={{ ...HEADER_BUTTON, padding: "2px 6px", border: "none" }}>
            <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true">
              <line x1="2" y1="2" x2="8" y2="8" />
              <line x1="8" y1="2" x2="2" y2="8" />
            </svg>
          </button>
        </div>
      )}
      footer={(
        <div style={{ display: "flex", flexDirection: "column", gap: 4, flexShrink: 0, padding: "6px 8px 8px", borderTop: "1px solid rgba(139,92,246,0.2)" }}>
          {reason && <div data-pi-subagent-input-reason="" style={{ color: "var(--text-dim)" }}>{t(reason)}</div>}
          {steerError && <div data-pi-subagent-steer-error="" style={{ color: "#f87171" }}>{t("piSubagent.steerFailed", { error: steerError })}</div>}
          <ComposerBox
            textareaRef={inputRef}
            value={text}
            onChange={setText}
            onKeyDown={(event) => {
              if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing) return;
              event.preventDefault();
              void send();
            }}
            placeholder={t("piSubagent.placeholder")}
            disabled={!canRelay}
            send={{ label: t("piSubagent.send"), active: canSend, disabled: !canSend, onClick: () => void send() }}
          />
        </div>
      )}
    >
      <div ref={bodyRef} style={{ padding: "4px 10px 8px", borderTop: "1px solid rgba(139,92,246,0.2)", display: "flex", flexDirection: "column", gap: 6 }}>
        {entries.length === 0 && (
          <div data-pi-subagent-empty="" style={{ color: "var(--text-dim)" }}>{phase === "loading" ? t("piSubagent.input.loading") : t("piSubagent.noEntries")}</div>
        )}
        {hasEarlier && (
          <button
            type="button"
            onClick={onLoadEarlier}
            disabled={loadingEarlier}
            data-pi-subagent-load-earlier=""
            style={{ alignSelf: "flex-start", padding: "2px 0", background: "none", border: "none", color: SUBAGENT_COLOR, cursor: loadingEarlier ? "default" : "pointer", fontSize: 12 }}
          >
            {t("piSubagent.loadEarlier")}
          </button>
        )}
        {entries.map((entry) => (
          <MessageView
            key={entry.id}
            message={entry.message}
            toolResults={toolResults}
            cwd={cwd ?? undefined}
            sessionId={sessionId}
          />
        ))}
        {error && phase !== "ready" && <div style={{ color: "#f87171" }}>{error}</div>}
        {phase === "missing" && <div style={{ color: "var(--text-dim)" }}>{t("piSubagent.input.missing")}</div>}
        {run?.result && (
          <div>
            <div style={{ color: "var(--text-dim)", fontSize: 11, marginBottom: 2 }}>{t("piSubagent.result")}</div>
            <div style={{ margin: 0, maxHeight: 320, overflow: "auto", whiteSpace: "normal" }}>
              <MarkdownBody>{run.result}</MarkdownBody>
            </div>
          </div>
        )}
      </div>
    </SubagentPanelFrame>
  );
}