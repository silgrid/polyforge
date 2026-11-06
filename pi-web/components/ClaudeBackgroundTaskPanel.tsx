"use client";

// A claude background task's panel, opened from its button in the running
// list into the subagent panel's floating slot (one panel open at a time). The header
// holds the kind badge, the whole command (a monitor's description below it when it
// differs), the status, the elapsed time, Stop (while running: claude's own stop_task)
// and Close. The body is the task's recent output as claude reads it (get_task_output,
// its last 8 KiB): read on open, every 2 s while the task runs and the panel is open, and
// once more after it ends; claude's refusal shows as its text. The panel reads the task
// it is given on every render, so a task that ends while open keeps its panel, showing
// the final status (as a subagent's panel does,).
import { useEffect, useRef, useState } from "react";
import { ClaudeTaskKindBadge, CLAUDE_TASK_COLOR, useClaudeSubagentClock } from "./ClaudeSubagentRunningList";
import { useI18n } from "@/hooks/useI18n";
import { claudeBackgroundTaskElapsedMs, claudeBackgroundTaskLabel, type ClaudeBackgroundTask, type ClaudeBackgroundTaskStatus } from "@/lib/claude-background-task-view";
import { formatTurnDuration } from "@/lib/turn-duration";

/** How often an open panel re-reads a running task's output. */
export const CLAUDE_TASK_OUTPUT_POLL_MS = 2_000;

export interface ClaudeBackgroundTaskPanelProps {
  task: ClaudeBackgroundTask;
  /** Stops the task; a rejection's message shows in the panel. */
  onStop: () => Promise<void>;
  /** Reads the task's recent output; a rejection's message shows in the panel. */
  onReadOutput: () => Promise<{ output: string; totalBytes: number; truncated: boolean }>;
  onClose: () => void;
  now?: number;
}

const HEADER_BUTTON = { flexShrink: 0, padding: "2px 8px", borderRadius: 5, border: "1px solid var(--border)", background: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 12 } as const;

function useStatusLabel(status: ClaudeBackgroundTaskStatus): string {
  const { t } = useI18n();
  if (status === "running") return t("claudeSubagent.running");
  if (status === "completed") return t("claudeSubagent.completed");
  if (status === "failed") return t("claudeSubagent.failed");
  return t("claudeSubagent.stopped");
}

type OutputState = { kind: "loading" } | { kind: "output"; output: string; truncated: boolean } | { kind: "error"; message: string };

/** The task's output, read on open, every 2 s while it runs, and once after it ends. */
function useTaskOutput(taskId: string, running: boolean, read: () => Promise<{ output: string; truncated: boolean }>): OutputState {
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
        const { output, truncated } = await reader.current();
        if (current) setState({ kind: "output", output, truncated });
      } catch (error) {
        if (current) setState({ kind: "error", message: error instanceof Error ? error.message : String(error) });
      } finally {
        inFlight = false;
      }
    };
    void load();
    const timer = running ? setInterval(() => void load(), CLAUDE_TASK_OUTPUT_POLL_MS) : null;
    return () => {
      current = false;
      if (timer) clearInterval(timer);
    };
  }, [taskId, running]);
  return state;
}

export function ClaudeBackgroundTaskPanel({ task, onStop, onReadOutput, onClose, now: fixedNow }: ClaudeBackgroundTaskPanelProps) {
  const { t } = useI18n();
  const running = task.status === "running";
  const now = useClaudeSubagentClock(running, fixedNow);
  const output = useTaskOutput(task.taskId, running, onReadOutput);
  const [stopping, setStopping] = useState(false);
  const [stopError, setStopError] = useState<string | null>(null);
  const status = useStatusLabel(task.status);
  const label = claudeBackgroundTaskLabel(task);
  const command = task.command || label;
  const description = task.description && task.description !== command ? task.description : null;

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
      data-claude-task-panel={task.taskId}
      data-claude-task-status={task.status}
      aria-label={label}
      style={{ display: "flex", flexDirection: "column", minWidth: 0, minHeight: 0, maxHeight: "min(60vh, 560px)", overflow: "hidden", fontSize: 12, borderRadius: 8, border: "1px solid rgba(13,148,136,0.35)", borderLeftColor: task.status === "failed" ? "#f87171" : CLAUDE_TASK_COLOR, background: "var(--bg-panel)", boxShadow: "0 6px 24px rgba(0,0,0,0.18)" }}
    >
      <div style={{ display: "flex", alignItems: "flex-start", gap: 7, flexShrink: 0, minWidth: 0, padding: "8px 8px 6px 10px" }}>
        <ClaudeTaskKindBadge task={task} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div data-claude-task-command="" style={{ color: "var(--text)", fontFamily: "var(--font-mono)", whiteSpace: "pre-wrap", wordBreak: "break-all", maxHeight: 72, overflowY: "auto" }}>{command}</div>
          {description && <div style={{ color: "var(--text-dim)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={description}>{description}</div>}
        </div>
        <span
          data-claude-task-status-label=""
          className={running ? "animate-[pulse_1.5s_infinite]" : undefined}
          style={{ flexShrink: 0, color: running ? CLAUDE_TASK_COLOR : task.status === "failed" ? "#f87171" : "var(--text-dim)" }}
        >
          {status}
        </span>
        <span style={{ flexShrink: 0, color: "var(--text-dim)", fontVariantNumeric: "tabular-nums" }}>{formatTurnDuration(claudeBackgroundTaskElapsedMs(task, now))}</span>
        {running && (
          <button type="button" onClick={() => void stop()} disabled={stopping} style={{ ...HEADER_BUTTON, cursor: stopping ? "default" : "pointer" }}>{t("claudeTask.stop")}</button>
        )}
        <button type="button" onClick={onClose} aria-label={t("claudeSubagent.closePanel")} title={t("claudeSubagent.closePanel")} style={{ ...HEADER_BUTTON, padding: "2px 6px", border: "none" }}>
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true">
            <line x1="2" y1="2" x2="8" y2="8" />
            <line x1="8" y1="2" x2="2" y2="8" />
          </svg>
        </button>
      </div>
      {stopError && <div data-claude-task-stop-error="" style={{ flexShrink: 0, padding: "0 10px 6px", color: "#f87171" }}>{t("claudeTask.stopFailed", { error: stopError })}</div>}
      <div data-claude-task-output="" style={{ flex: 1, minHeight: 0, overflow: "auto", borderTop: "1px solid rgba(13,148,136,0.2)", padding: "6px 10px 8px" }}>
        {output.kind === "loading" && <div style={{ color: "var(--text-dim)" }}>{t("claudeTask.loadingOutput")}</div>}
        {output.kind === "error" && <div style={{ color: "#f87171" }}>{t("claudeTask.outputFailed", { error: output.message })}</div>}
        {output.kind === "output" && (output.output === ""
          ? <div style={{ color: "var(--text-dim)" }}>{t("claudeTask.noOutput")}</div>
          : <>
            {output.truncated && <div style={{ color: "var(--text-dim)", marginBottom: 4 }}>{t("claudeTask.outputTruncated")}</div>}
            <pre style={{ margin: 0, fontFamily: "var(--font-mono)", fontSize: 12, whiteSpace: "pre-wrap", wordBreak: "break-all", color: "var(--text)" }}>{output.output}</pre>
          </>)}
      </div>
    </section>
  );
}
