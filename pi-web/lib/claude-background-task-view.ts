// The client-safe model of a claude background task: a `Bash` command
// run in the background, or a `Monitor`, both claude's `local_bash` tasks — never a
// subagent run. lib/claude-adapter.ts (the only module that knows claude's formats)
// builds each snapshot from claude's task events; no claude format is parsed here.
// The page folds the snapshots (mergeClaudeBackgroundTask) into the tasks the running
// list and the task panel show, and replaces them by the server's whole list of running
// tasks a new stream starts with (replaceClaudeBackgroundTasks); the force-stop event's shape lives here too, so the
// server that sends it and the page that reads it share one definition.

/** `shell` for a `Bash` command, `monitor` for a `Monitor` (claude's tool, both `local_bash`). */
export type ClaudeBackgroundTaskKind = "shell" | "monitor";

/** claude's own reported state of a task; the child's exit settles a running one as `stopped`. */
export type ClaudeBackgroundTaskStatus = "running" | "completed" | "failed" | "stopped";

export interface ClaudeBackgroundTask {
  /** claude's task id (`b` + 8 characters for a shell task). */
  taskId: string;
  /** The `Bash` / `Monitor` tool call that started it. */
  toolCallId: string;
  kind: ClaudeBackgroundTaskKind;
  /** The tool call's `command`; empty when unknown. */
  command: string;
  description?: string;
  status: ClaudeBackgroundTaskStatus;
  /** Epoch ms claude reported the task started (its `task_started`). */
  startedAt: number;
  /** Epoch ms it ended; absent while running. */
  endedAt?: number;
}

/** The pi event a task snapshot goes out as: `{ type, task }`. */
export const CLAUDE_BACKGROUND_TASK_EVENT = "claude_background_task";

/** The tasks a session holds, by task id. */
export type ClaudeBackgroundTasks = Readonly<Record<string, ClaudeBackgroundTask>>;

/**
 * One piece of background work a live claude child still runs: a background subagent
 * (`agent`: its `Agent` call id, its description) or a background task (`task`: claude's
 * task id, its command — a monitor's description).
 */
export interface ClaudeBackgroundWork {
  kind: "agent" | "task";
  id: string;
  label: string;
}

/**
 * The live wire event of a Stop that did not end the turn by its deadline while background
 * work is unfinished: `{ type, work: ClaudeBackgroundWork[] }`, the work a force stop
 * (`abort { force: true }`) would end. It stands until the turn ends — its
 * `agent_settled` (a compaction's `compaction_end`) clears it; no event of its own — and
 * a page subscribing meanwhile gets it at once (lib/claude-session.ts sends it).
 */
export const CLAUDE_FORCE_STOP_EVENT = "claude_force_stop_needed";

/**
 * A live task snapshot merged into the page's tasks: the snapshot replaces the task's
 * entry (claude's reports only move a task forward), except that a settled task never
 * goes back to running (a replayed snapshot racing its end). Unchanged: the same object.
 */
export function mergeClaudeBackgroundTask(tasks: ClaudeBackgroundTasks, task: ClaudeBackgroundTask): ClaudeBackgroundTasks {
  const existing = tasks[task.taskId];
  if (existing && existing.status !== "running" && task.status === "running") return tasks;
  if (existing && JSON.stringify(existing) === JSON.stringify(task)) return tasks;
  return { ...tasks, [task.taskId]: task };
}

/**
 * The pi event of the server's whole list of the tasks a live child runs: `{ type, tasks }`
 * (running snapshots, start order; empty when none runs). lib/claude-session.ts sends it
 * to each new subscriber — a reconnect, a session switched back to, a page after a pi-web
 * restart — so a task that ended while the page was not listening leaves its list.
 */
export const CLAUDE_BACKGROUND_TASKS_EVENT = "claude_background_tasks";

/**
 * The page's tasks after the server's whole list of running tasks: the list stands for
 * every running task — one the page holds as running but the list lacks is dropped, its
 * end unseen — while a task the page saw settle stays as it ended (an open panel keeps its
 * final state) and never goes back to running. Unchanged: the same object.
 */
export function replaceClaudeBackgroundTasks(tasks: ClaudeBackgroundTasks, running: readonly ClaudeBackgroundTask[]): ClaudeBackgroundTasks {
  const next: Record<string, ClaudeBackgroundTask> = {};
  for (const task of Object.values(tasks)) {
    if (task.status !== "running") next[task.taskId] = task;
  }
  for (const task of running) {
    if (!next[task.taskId]) next[task.taskId] = task;
  }
  return JSON.stringify(next) === JSON.stringify(tasks) ? tasks : next;
}

/** The running tasks, oldest start first. */
export function runningClaudeBackgroundTasks(tasks: ClaudeBackgroundTasks): ClaudeBackgroundTask[] {
  return Object.values(tasks).filter((task) => task.status === "running").sort((a, b) => a.startedAt - b.startedAt);
}

/**
 * A task's one-line label, as claude's own task list names it: a shell task by its
 * command, a monitor by its description (each falling back to the other).
 */
export function claudeBackgroundTaskLabel(task: ClaudeBackgroundTask): string {
  const description = task.description ?? "";
  return (task.kind === "monitor" ? description || task.command : task.command || description).trim();
}

/** How long the task has run (to `now`) or ran (to its end). */
export function claudeBackgroundTaskElapsedMs(task: ClaudeBackgroundTask, now: number): number {
  return Math.max(0, (task.endedAt ?? now) - task.startedAt);
}
