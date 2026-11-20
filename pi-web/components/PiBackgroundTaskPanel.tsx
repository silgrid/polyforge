"use client";

// A built-in background task's panel, and the session's finished-tasks
// history, opened from the running list above a pi session's composer into
// the floating slot one panel at a time (polyforge#173, AC1/AC3).
//
// The panel reads the registry record it is given on every render, so a task
// that ends while open keeps its panel, showing the final status. Its header
// holds the kind badge and the whole command; its right column is Close on
// its own top row, then — while the task runs — the stop icon button (the
// executor's idempotent stop) and the ticking elapsed time, so the running
// state shows without any status text (polyforge#69's rule); a task that
// ended swaps the stop button and the time for its final status and duration.
// The body is the task's output tail as the registry serves it (its last
// 8 KiB): read on open, every 2 s while the task runs and the panel is open,
// and once more after it ends. An `interrupted` record — what a restart
// turns a running task into — shows as its own status, never as running.
//
// The history panel lists the session's settled tasks, newest end first,
// each row a button that opens that task's panel; its entry is the detail
// panel's header button (polyforge#189: the running area no longer carries
// a settled entry, so the panel's own history button opens the list — the
// owner's B ruling on #173's settled entry).
import { useEffect, useRef, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import { formatTurnDuration } from "@/lib/turn-duration";
import { useClaudeSubagentClock } from "./ClaudeSubagentRunningList";
import type { BgTaskInfo, BgTaskOutput } from "@/lib/bg-protocol";

/** A built-in task's color, apart from the subagents' violet. */
export const PI_TASK_COLOR = "#0d9488";

/** How often an open panel re-reads a running task's output. */
export const PI_TASK_OUTPUT_POLL_MS = 2_000;

const HEADER_BUTTON = { flexShrink: 0, padding: "2px 8px", borderRadius: 5, border: "1px solid var(--border)", background: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 12 } as const;

/** The stop icon button's glyph: the filled square the chat's own Stop uses. */
const STOP_ICON = (
  <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
    <rect x="1.5" y="1.5" width="7" height="7" rx="1.5" fill="currentColor" />
  </svg>
);

/** The history entry's glyph: a counterclockwise arrow over a clock face. */
const HISTORY_ICON = (
  <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M1.8 5a3.2 3.2 0 1 1 1 2.3" />
    <path d="M1.4 4.3 1.8 5l.8-.2" />
    <path d="M5 3.4V5l1.2.8" />
  </svg>
);

const RUNNING = "running";

/** The record's own status word, the way the registry spells it (succeeded reads as the completed label). */
function statusLabel(t: ReturnType<typeof useI18n>["t"], status: BgTaskInfo["status"]): string {
  // The registry spells success `succeeded`; its label is the completed one.
  if (status === "succeeded") return t("bgTasks.status.completed");
  return t(`bgTasks.status.${status}`);
}

/** Whether the panel's task still runs (the only state that may offer Stop). */
function isRunning(task: BgTaskInfo): boolean {
  return task.status === RUNNING;
}

function elapsedMs(task: BgTaskInfo, now: number): number {
  const end = isRunning(task) ? now : task.endedAt ?? now;
  return Math.max(0, end - task.startedAt);
}

type OutputState = { kind: "loading" } | { kind: "output"; output: BgTaskOutput } | { kind: "error"; message: string };

/** The task's output, read on open, every 2 s while it runs, and once after it ends. */
function useTaskOutput(taskId: string, running: boolean, read: () => Promise<BgTaskOutput>): OutputState {
  const [state, setState] = useState<OutputState>({ kind: "loading" });
  // The latest reader, without restarting the polling on every render.
  const reader = useRef(read);
  useEffect(() => {
    reader.current = read;
  });
  useEffect(() => {
    let current = true;
    let inFlight = false;
    const load = async () => {
      if (inFlight) return;
      inFlight = true;
      try {
        const output = await reader.current();
        if (current) setState({ kind: "output", output });
      } catch (error) {
        if (current) setState({ kind: "error", message: error instanceof Error ? error.message : String(error) });
      } finally {
        inFlight = false;
      }
    };
    void load();
    const timer = running ? setInterval(() => void load(), PI_TASK_OUTPUT_POLL_MS) : null;
    return () => {
      current = false;
      if (timer) clearInterval(timer);
    };
  }, [taskId, running]);
  return state;
}

export function PiBackgroundTaskPanel({ task, onStop, onReadOutput, onOpenHistory, onClose, now: fixedNow }: {
  task: BgTaskInfo;
  /** Stops the task (the executor's idempotent stop); a rejection's message shows in the panel. */
  onStop: () => Promise<void>;
  /** Reads the task's output tail; a rejection's message shows in the panel. */
  onReadOutput: () => Promise<BgTaskOutput>;
  /** Opens the session's settled history — the history panel's entry (#189); absent, no button. */
  onOpenHistory?: () => void;
  onClose: () => void;
  now?: number;
}) {
  const { t } = useI18n();
  const running = isRunning(task);
  const now = useClaudeSubagentClock(running, fixedNow);
  const output = useTaskOutput(task.id, running, onReadOutput);
  const [stopping, setStopping] = useState(false);
  const [stopError, setStopError] = useState<string | null>(null);

  const stop = async () => {
    if (stopping) return;
    setStopping(true);
    setStopError(null);
    try {
      await onStop();
    } catch (error) {
      setStopError(error instanceof Error ? error.message : String(error));
    } finally {
      setStopping(false);
    }
  };

  return (
    <section
      data-pi-task-panel={task.id}
      data-pi-task-status={task.status}
      aria-label={task.command}
      style={{ display: "flex", flexDirection: "column", minWidth: 0, minHeight: 0, maxHeight: "min(60vh, 560px)", overflow: "hidden", fontSize: 12, borderRadius: 8, border: "1px solid rgba(13,148,136,0.35)", borderLeftColor: task.status === "failed" ? "#f87171" : PI_TASK_COLOR, background: "var(--bg-panel)", boxShadow: "0 6px 24px rgba(0,0,0,0.18)" }}
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 3, flexShrink: 0, minWidth: 0, padding: "8px 8px 6px 10px" }}>
        <div data-pi-task-header-row="" style={{ display: "flex", alignItems: "flex-start", gap: 7, minWidth: 0 }}>
          <span style={{ flexShrink: 0, padding: "0 5px", borderRadius: 4, background: "rgba(13,148,136,0.14)", color: PI_TASK_COLOR, fontFamily: "var(--font-mono)", fontSize: 11, fontWeight: 600 }}>{t("bgTasks.kindTask")}</span>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div data-pi-task-command="" style={{ color: "var(--text)", fontFamily: "var(--font-mono)", whiteSpace: "pre-wrap", wordBreak: "break-all", maxHeight: 72, overflowY: "auto" }}>{task.command}</div>
          </div>
          <div data-pi-task-side="" style={{ display: "flex", flexDirection: "column", alignItems: "flex-end", gap: 4, flexShrink: 0 }}>
            <button type="button" onClick={onClose} aria-label={t("bgTasks.close")} title={t("bgTasks.close")} style={{ ...HEADER_BUTTON, padding: "2px 6px", border: "none" }}>
              <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true">
                <line x1="2" y1="2" x2="8" y2="8" />
                <line x1="8" y1="2" x2="2" y2="8" />
              </svg>
            </button>
            {onOpenHistory && (
              <button type="button" onClick={onOpenHistory} data-pi-bg-history-open="" aria-label={t("bgTasks.historyTitle")} title={t("bgTasks.historyTitle")} style={{ ...HEADER_BUTTON, padding: "2px 6px", border: "none", cursor: "pointer" }}>
                {HISTORY_ICON}
              </button>
            )}
            {running ? (
              <>
                <button type="button" onClick={() => void stop()} disabled={stopping} aria-label={t("bgTasks.stop")} title={t("bgTasks.stop")} style={{ ...HEADER_BUTTON, padding: "2px 6px", border: "none", cursor: stopping ? "default" : "pointer" }}>{STOP_ICON}</button>
                <span data-pi-task-elapsed="" style={{ color: "var(--text-dim)", fontVariantNumeric: "tabular-nums" }}>{formatTurnDuration(elapsedMs(task, now))}</span>
              </>
            ) : (
              <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                <span data-pi-task-status-label="" style={{ color: task.status === "failed" ? "#f87171" : "var(--text-dim)" }}>{statusLabel(t, task.status)}</span>
                <span style={{ color: "var(--text-dim)", fontVariantNumeric: "tabular-nums" }}>{formatTurnDuration(elapsedMs(task, now))}</span>
              </div>
            )}
          </div>
        </div>
      </div>
      {stopError && <div data-pi-task-stop-error="" style={{ flexShrink: 0, padding: "0 10px 6px", color: "#f87171" }}>{t("bgTasks.stopFailed", { error: stopError })}</div>}
      <div data-pi-task-output="" style={{ flex: 1, minHeight: 0, overflow: "auto", borderTop: "1px solid rgba(13,148,136,0.2)", padding: "6px 10px 8px" }}>
        {output.kind === "loading" && <div style={{ color: "var(--text-dim)" }}>{t("bgTasks.loadingOutput")}</div>}
        {output.kind === "error" && <div style={{ color: "#f87171" }}>{t("bgTasks.outputFailed", { error: output.message })}</div>}
        {output.kind === "output" && (output.output.text === ""
          ? <div style={{ color: "var(--text-dim)" }}>{t("bgTasks.noOutput")}</div>
          : <>
            {output.output.truncated && <div style={{ color: "var(--text-dim)", marginBottom: 4 }}>{t("bgTasks.outputTruncated")}</div>}
            <pre style={{ margin: 0, fontFamily: "var(--font-mono)", fontSize: 12, whiteSpace: "pre-wrap", wordBreak: "break-all", color: "var(--text)" }}>{output.output.text}</pre>
          </>)}
      </div>
    </section>
  );
}

/** The session's settled tasks, newest end first: the 「已结束」 entry's rows. */
export function PiBackgroundTaskHistory({ tasks, onOpenTask, onClose, now: fixedNow }: {
  tasks: BgTaskInfo[];
  onOpenTask: (taskId: string) => void;
  onClose: () => void;
  now?: number;
}) {
  const { t } = useI18n();
  const now = useClaudeSubagentClock(false, fixedNow);
  const settled = tasks
    .filter((task) => !isRunning(task))
    .sort((a, b) => (b.endedAt ?? b.startedAt) - (a.endedAt ?? a.startedAt));
  return (
    <section
      data-pi-bg-history-panel=""
      aria-label={t("bgTasks.historyTitle")}
      style={{ display: "flex", flexDirection: "column", minWidth: 0, minHeight: 0, maxHeight: "min(60vh, 560px)", overflow: "hidden", fontSize: 12, borderRadius: 8, border: "1px solid rgba(13,148,136,0.35)", background: "var(--bg-panel)", boxShadow: "0 6px 24px rgba(0,0,0,0.18)" }}
    >
      <div style={{ display: "flex", alignItems: "flex-start", gap: 7, flexShrink: 0, minWidth: 0, padding: "8px 8px 6px 10px" }}>
        <div style={{ flex: 1, minWidth: 0, fontWeight: 600, color: "var(--text)" }}>{t("bgTasks.historyTitle")}</div>
        <button type="button" onClick={onClose} aria-label={t("bgTasks.close")} title={t("bgTasks.close")} style={{ ...HEADER_BUTTON, padding: "2px 6px", border: "none" }}>
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true">
            <line x1="2" y1="2" x2="8" y2="8" />
            <line x1="8" y1="2" x2="2" y2="8" />
          </svg>
        </button>
      </div>
      <div style={{ flex: 1, minHeight: 0, overflowY: "auto", borderTop: "1px solid rgba(13,148,136,0.2)", padding: "4px 6px" }}>
        {settled.length === 0 && <div style={{ color: "var(--text-dim)", padding: "4px 6px" }}>{t("bgTasks.emptyHistory")}</div>}
        {settled.map((task) => (
          <button
            key={task.id}
            type="button"
            data-pi-history-task={task.id}
            onClick={() => onOpenTask(task.id)}
            onMouseDown={(event) => event.preventDefault()}
            title={task.command}
            style={{ display: "flex", alignItems: "center", gap: 7, width: "100%", minWidth: 0, padding: "4px 8px", borderRadius: 6, border: "1px solid transparent", background: "none", cursor: "pointer", fontSize: 12, lineHeight: "18px", textAlign: "left" }}
          >
            <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "var(--text)", fontFamily: "var(--font-mono)" }}>{task.command}</span>
            <span style={{ flexShrink: 0, color: task.status === "failed" ? "#f87171" : "var(--text-dim)" }}>{statusLabel(t, task.status)}</span>
            <span style={{ flexShrink: 0, color: "var(--text-dim)", fontVariantNumeric: "tabular-nums" }}>{formatTurnDuration(elapsedMs(task, now))}</span>
          </button>
        ))}
      </div>
    </section>
  );
}
