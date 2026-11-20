"use client";

// The running list above a pi session's composer (polyforge#173, AC1): one
// compact button per running built-in subagent and per running background
// task of THIS session, merged in one order — the owner's ruling: no
// grouping, oldest start/queue first, the type told apart by its badge and
// color, a queued run named and not offered a stop it cannot take (the
// controller's stop semantics; a running subagent stops in its own chat).
// Settled tasks never list (AC3): the 「已结束」 entry beside the running
// items opens the history panel instead, and is the one thing that renders
// once everything settled. Nothing renders at all while no session of this
// page runs nothing and has nothing settled — the composer keeps its place.
// The looks are the Claude list's (the reference this list aligns with):
// the same grid, the same one-line buttons, a 1 s clock only while non-empty.
import { useI18n } from "@/hooks/useI18n";
import { formatTurnDuration } from "@/lib/turn-duration";
import { useClaudeSubagentClock } from "./ClaudeSubagentRunningList";
import { PI_TASK_COLOR } from "./PiBackgroundTaskPanel";
import type { BgTaskInfo } from "@/lib/bg-protocol";
import type { PiSubagentRunSummary } from "@/lib/subagent-runs-view";

/** A running subagent's color: Claude's violet, the list's reference. */
const SUBAGENT_COLOR = "#8b5cf6";
// One button is 24px tall (an 18px line, 2px padding and a 1px border, top and
// bottom), with 4px between rows.
const ITEM_HEIGHT_PX = 24;
const ITEM_GAP_PX = 4;
// The most rows of buttons the list shows at once; more scroll inside it.
const MAX_ROWS = 3;
const ITEMS_MAX_HEIGHT_PX = MAX_ROWS * ITEM_HEIGHT_PX + (MAX_ROWS - 1) * ITEM_GAP_PX;
// Equal-width columns, as many as fit, none narrower than this.
const ITEM_MIN_WIDTH_PX = 260;

export function PiRunningList({ subagents, tasks, onOpenSubagent, onOpenTask, onOpenHistory, now: fixedNow }: {
  /** This session's running and queued built-in subagent runs. */
  subagents: PiSubagentRunSummary[];
  /** This session's background-task records; the running ones list, the settled ones open the history entry. */
  tasks: BgTaskInfo[];
  onOpenSubagent: (sessionId: string, label: string) => void;
  onOpenTask: (taskId: string) => void;
  onOpenHistory: () => void;
  now?: number;
}) {
  const { t } = useI18n();
  const runningTasks = tasks.filter((task) => task.status === "running");
  const settledCount = tasks.length - runningTasks.length;
  const ticking = subagents.length + runningTasks.length > 0;
  const now = useClaudeSubagentClock(ticking, fixedNow);
  if (subagents.length === 0 && runningTasks.length === 0 && settledCount === 0) return null;

  const subagentLabel = (run: PiSubagentRunSummary): string => run.description || run.profile || t("bgTasks.agent");
  // One merged order (the owner's ruling): a subagent's dispatch time, a
  // task's start time, oldest first.
  const merged: Array<{ at: number; node: React.ReactNode }> = [
    ...subagents.map((run) => ({ at: Date.parse(run.createdAt), node: (() => {
      const label = subagentLabel(run);
      const running = run.status === "running";
      return (
        <button
          key={`sub:${run.sessionId}`}
          type="button"
          data-pi-subagent-item={run.sessionId}
          data-pi-subagent-status={run.status}
          title={label}
          onClick={() => { if (running) onOpenSubagent(run.sessionId, label); }}
          // Opening the subagent's chat leaves the focus where it was.
          onMouseDown={(event) => event.preventDefault()}
          style={{ display: "flex", alignItems: "center", gap: 6, width: "100%", minWidth: 0, padding: "2px 8px", borderRadius: 6, border: `1px solid rgba(139,92,246,0.3)`, background: "rgba(139,92,246,0.06)", color: "var(--text-muted)", cursor: "pointer", fontSize: 12, lineHeight: "18px" }}
        >
          <span style={{ flex: 1, minWidth: 0, textAlign: "left", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "var(--text)" }}>{label}</span>
          {running
            ? <span className="animate-[pulse_1.5s_infinite]" style={{ flexShrink: 0, color: SUBAGENT_COLOR }}>{t("bgTasks.status.running")}</span>
            : <span style={{ flexShrink: 0, color: "var(--text-dim)" }}>{t("bgTasks.queued")}</span>}
          <span style={{ flexShrink: 0, color: "var(--text-dim)", fontVariantNumeric: "tabular-nums" }}>{formatTurnDuration(Math.max(0, now - Date.parse(run.createdAt)))}</span>
        </button>
      );
    })() })),
    ...runningTasks.map((task) => ({ at: task.startedAt, node: (() => {
      return (
        <button
          key={`task:${task.id}`}
          type="button"
          data-pi-task-item={task.id}
          data-pi-task-status={task.status}
          title={task.command}
          onClick={() => onOpenTask(task.id)}
          onMouseDown={(event) => event.preventDefault()}
          style={{ display: "flex", alignItems: "center", gap: 6, width: "100%", minWidth: 0, padding: "2px 8px", borderRadius: 6, border: "1px solid rgba(13,148,136,0.3)", background: "rgba(13,148,136,0.06)", color: "var(--text-muted)", cursor: "pointer", fontSize: 12, lineHeight: "18px" }}
        >
          <span style={{ flexShrink: 0, padding: "0 5px", borderRadius: 4, background: "rgba(13,148,136,0.14)", color: PI_TASK_COLOR, fontFamily: "var(--font-mono)", fontSize: 11, fontWeight: 600 }}>{t("bgTasks.kindShort")}</span>
          <span style={{ flex: 1, minWidth: 0, textAlign: "left", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "var(--text)", fontFamily: "var(--font-mono)" }}>{task.command}</span>
          <span className="animate-[pulse_1.5s_infinite]" style={{ flexShrink: 0, color: PI_TASK_COLOR }}>{t("bgTasks.status.running")}</span>
          <span style={{ flexShrink: 0, color: "var(--text-dim)", fontVariantNumeric: "tabular-nums" }}>{formatTurnDuration(Math.max(0, now - task.startedAt))}</span>
        </button>
      );
    })() })),
  ].sort((a, b) => a.at - b.at);

  return (
    <div
      data-pi-running-list
      role="group"
      aria-label={t("bgTasks.runningList")}
      style={{ display: "flex", alignItems: "flex-start", gap: 6, padding: "4px 0 2px", minWidth: 0 }}
    >
      <div
        data-pi-running-items
        style={{ display: "grid", gridTemplateColumns: `repeat(auto-fill, minmax(${ITEM_MIN_WIDTH_PX}px, 1fr))`, gap: ITEM_GAP_PX, flex: 1, minWidth: 0, maxHeight: ITEMS_MAX_HEIGHT_PX, overflowY: "auto", overscrollBehavior: "contain" }}
      >
        {merged.map((item) => item.node)}
        {settledCount > 0 && (
          <button
            type="button"
            data-pi-bg-history={String(settledCount)}
            title={t("bgTasks.historyTitle")}
            onClick={onOpenHistory}
            onMouseDown={(event) => event.preventDefault()}
            style={{ display: "flex", alignItems: "center", gap: 6, width: "100%", minWidth: 0, padding: "2px 8px", borderRadius: 6, border: "1px solid var(--border)", background: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 12, lineHeight: "18px" }}
          >
            <span style={{ flex: 1, minWidth: 0, textAlign: "left", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "var(--text-muted)" }}>{t("bgTasks.history")}</span>
            <span style={{ flexShrink: 0, color: "var(--text-dim)", fontVariantNumeric: "tabular-nums" }}>{settledCount}</span>
          </button>
        )}
      </div>
    </div>
  );
}
