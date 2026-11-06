"use client";

// The running list above a claude session's composer: one compact
// button per subagent claude reports running (foreground and background, at any
// depth), oldest start first, each with its description, status and elapsed time.
// Nothing renders while none runs; a settled run leaves the list and keeps its Agent
// row in the conversation. The list is derived from the run map alone
// (runningClaudeSubagents), so a reload rebuilds it. However many run, it is at most
// three rows of buttons tall and scrolls inside itself: the composer
// below and an open panel above keep their room. It shows no count label.
// The buttons sit in an equal-width grid, as many columns as fit at 260px or more, each
// filling its cell: the name on the left, cut with an ellipsis, the status and elapsed
// time on the right, so a column's elapsed times line up.
// claude's background tasks (a background Bash or a Monitor) are buttons of the same grid,
// after the subagents, oldest start first, in their own color: a kind badge, the command
// (a monitor's description), Running and the elapsed time; a click opens the task's
// panel. ClaudeForceStopNotice is the confirmation a Stop asks for when
// claude did not end the turn by its deadline while background work runs.
// It has no side padding of its own: its edges are the composer box's, from the outer
// gutter ChatWindow shares between the two.
import { useEffect, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import { claudeBackgroundTaskElapsedMs, claudeBackgroundTaskLabel, runningClaudeBackgroundTasks, type ClaudeBackgroundTask, type ClaudeBackgroundTasks, type ClaudeBackgroundWork } from "@/lib/claude-background-task-view";
import { claudeSubagentElapsedMs, runningClaudeSubagents, type ClaudeSubagentRun, type ClaudeSubagentRuns } from "@/lib/claude-subagent-view";
import { formatTurnDuration } from "@/lib/turn-duration";

const SUBAGENT_COLOR = "#8b5cf6";
/** A background task's color: apart from the subagents' violet. */
export const CLAUDE_TASK_COLOR = "#0d9488";
const NO_TASKS: ClaudeBackgroundTasks = {};
// One button is 24px tall (an 18px line, 2px padding and a 1px border, top and bottom),
// with 4px between rows.
const ITEM_HEIGHT_PX = 24;
const ITEM_GAP_PX = 4;
// The most rows of buttons the list shows at once; more scroll inside it.
const MAX_ROWS = 3;
const ITEMS_MAX_HEIGHT_PX = MAX_ROWS * ITEM_HEIGHT_PX + (MAX_ROWS - 1) * ITEM_GAP_PX;
// Equal-width columns, as many as fit, none narrower than this.
const ITEM_MIN_WIDTH_PX = 260;

/** The name a run shows: its description, else its agent type, else the tool's name. */
export function claudeSubagentLabel(run: ClaudeSubagentRun): string {
  return run.description ?? run.agentType ?? "Agent";
}

/**
 * The clock elapsed times are read against: `fixed` when given, else the current time,
 * re-read once a second only while `ticking`. No I/O inside.
 */
export function useClaudeSubagentClock(ticking: boolean, fixed?: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!ticking || fixed !== undefined) return;
    setNow(Date.now());
    const clock = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(clock);
  }, [ticking, fixed]);
  return fixed ?? now;
}

/** A background task's kind badge: claude's tool name. */
export function ClaudeTaskKindBadge({ task }: { task: ClaudeBackgroundTask }) {
  const { t } = useI18n();
  return (
    <span style={{ flexShrink: 0, padding: "0 5px", borderRadius: 4, background: "rgba(13,148,136,0.14)", color: CLAUDE_TASK_COLOR, fontFamily: "var(--font-mono)", fontSize: 11, fontWeight: 600 }}>
      {task.kind === "monitor" ? t("claudeTask.kindMonitor") : t("claudeTask.kindShell")}
    </span>
  );
}

export function ClaudeSubagentRunningList({ runs, onOpen, tasks = NO_TASKS, onOpenTask, now: fixedNow }: {
  runs: ClaudeSubagentRuns;
  onOpen: (toolCallId: string) => void;
  /** claude's background tasks by task id; the running ones are listed. */
  tasks?: ClaudeBackgroundTasks;
  onOpenTask?: (taskId: string) => void;
  now?: number;
}) {
  const { t } = useI18n();
  const running = runningClaudeSubagents(runs);
  const runningTasks = runningClaudeBackgroundTasks(tasks);
  const now = useClaudeSubagentClock(running.length + runningTasks.length > 0, fixedNow);
  if (running.length === 0 && runningTasks.length === 0) return null;
  return (
    <div
      data-claude-subagent-list
      role="group"
      aria-label={t("claudeSubagent.runningList")}
      style={{ display: "flex", alignItems: "flex-start", gap: 6, padding: "4px 0 2px", minWidth: 0 }}
    >
      <div
        data-claude-subagent-items
        style={{ display: "grid", gridTemplateColumns: `repeat(auto-fill, minmax(${ITEM_MIN_WIDTH_PX}px, 1fr))`, gap: ITEM_GAP_PX, flex: 1, minWidth: 0, maxHeight: ITEMS_MAX_HEIGHT_PX, overflowY: "auto", overscrollBehavior: "contain" }}
      >
        {running.map((run) => {
          const label = claudeSubagentLabel(run);
          const elapsed = claudeSubagentElapsedMs(run, now);
          return (
            <button
              key={run.toolCallId}
              type="button"
              data-claude-subagent-item={run.toolCallId}
              title={label}
              onClick={() => onOpen(run.toolCallId)}
              // Opening the panel leaves the focus where it was (the composer keeps its caret).
              onMouseDown={(event) => event.preventDefault()}
              style={{ display: "flex", alignItems: "center", gap: 6, width: "100%", minWidth: 0, padding: "2px 8px", borderRadius: 6, border: "1px solid rgba(139,92,246,0.3)", background: "rgba(139,92,246,0.06)", color: "var(--text-muted)", cursor: "pointer", fontSize: 12, lineHeight: "18px" }}
            >
              <span style={{ flex: 1, minWidth: 0, textAlign: "left", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "var(--text)" }}>{label}</span>
              <span className="animate-[pulse_1.5s_infinite]" style={{ flexShrink: 0, color: SUBAGENT_COLOR }}>{t("claudeSubagent.running")}</span>
              {elapsed !== undefined && <span style={{ flexShrink: 0, color: "var(--text-dim)", fontVariantNumeric: "tabular-nums" }}>{formatTurnDuration(elapsed)}</span>}
            </button>
          );
        })}
        {runningTasks.map((task) => {
          const label = claudeBackgroundTaskLabel(task);
          return (
            <button
              key={task.taskId}
              type="button"
              data-claude-task-item={task.taskId}
              data-claude-task-kind={task.kind}
              title={label}
              onClick={() => onOpenTask?.(task.taskId)}
              onMouseDown={(event) => event.preventDefault()}
              style={{ display: "flex", alignItems: "center", gap: 6, width: "100%", minWidth: 0, padding: "2px 8px", borderRadius: 6, border: "1px solid rgba(13,148,136,0.3)", background: "rgba(13,148,136,0.06)", color: "var(--text-muted)", cursor: "pointer", fontSize: 12, lineHeight: "18px" }}
            >
              <ClaudeTaskKindBadge task={task} />
              <span style={{ flex: 1, minWidth: 0, textAlign: "left", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "var(--text)", fontFamily: "var(--font-mono)" }}>{label}</span>
              <span className="animate-[pulse_1.5s_infinite]" style={{ flexShrink: 0, color: CLAUDE_TASK_COLOR }}>{t("claudeSubagent.running")}</span>
              <span style={{ flexShrink: 0, color: "var(--text-dim)", fontVariantNumeric: "tabular-nums" }}>{formatTurnDuration(claudeBackgroundTaskElapsedMs(task, now))}</span>
            </button>
          );
        })}
      </div>
    </div>
  );
}

/**
 * The force-stop confirmation, above the running list: the turn did not
 * stop by its deadline, and ending it now ends the background work listed. Force stop
 * signals claude; Keep waiting dismisses the notice. Like the list it sits in the outer
 * gutter ChatWindow shares with the composer: no side margin of its own.
 */
export function ClaudeForceStopNotice({ work, onForceStop, onKeepWaiting }: { work: readonly ClaudeBackgroundWork[]; onForceStop: () => void; onKeepWaiting: () => void }) {
  const { t } = useI18n();
  return (
    <div
      data-claude-force-stop=""
      role="alert"
      style={{ display: "flex", flexDirection: "column", gap: 4, margin: "4px 0 2px", padding: "6px 10px", borderRadius: 6, border: "1px solid rgba(217,119,6,0.45)", background: "rgba(217,119,6,0.08)", color: "var(--text)", fontSize: 12, lineHeight: "18px" }}
    >
      <div>{t("claudeStop.forceStopNotice")}</div>
      {work.length > 0 && (
        <ul style={{ margin: 0, paddingLeft: 16, color: "var(--text-muted)" }}>
          {work.map((piece) => (
            <li key={`${piece.kind}:${piece.id}`} data-claude-force-stop-work={piece.id} style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {piece.kind === "agent" ? t("claudeStop.workAgent") : t("claudeStop.workTask")}: {piece.label}
            </li>
          ))}
        </ul>
      )}
      <div style={{ display: "flex", gap: 6, justifyContent: "flex-end" }}>
        <button type="button" onClick={onKeepWaiting} style={{ padding: "2px 10px", borderRadius: 5, border: "1px solid var(--border)", background: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 12 }}>{t("claudeStop.keepWaiting")}</button>
        <button type="button" onClick={onForceStop} style={{ padding: "2px 10px", borderRadius: 5, border: "1px solid #d97706", background: "#d97706", color: "#fff", cursor: "pointer", fontSize: 12 }}>{t("claudeStop.forceStop")}</button>
      </div>
    </div>
  );
}
