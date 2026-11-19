import type { BgSupervisorSpawnOptions, BgSupervisorTask } from "./bg-supervisor";
import type { BgTaskInfo, BgTaskOutput } from "./bg-protocol";

/**
 * The module-level executor of pi-web's built-in background tasks
 * (polyforge#172): one controller per process, independent of any session
 * wrapper (Q11 A — Stop's forced idle reap ends a stuck wrapper, the tasks
 * live on), owning the task registry, the per-session limits, the persistence
 * under pi-web's own data dir (Q3 A), the deletion gate (Q8) and the
 * completion wake (Q5 A). rpc-manager.ts assembles the singleton with its
 * session dependencies, exactly as SUBAGENT_CONTROLLER is assembled there.
 */

/** A session wrapper as the wake path needs it (structurally: AgentSessionWrapper). */
export interface BgWrapperLike {
  isAlive(): boolean;
  isRunning(): boolean;
  waitUntilReady?(): Promise<void>;
  readonly sessionFile?: string;
  inner: {
    sendCustomMessage(
      message: { customType: string; content: string; display?: boolean; details?: unknown },
      options?: { deliverAs?: string; triggerTurn?: boolean },
    ): Promise<unknown>;
  };
}

export interface BgRuntimeDeps {
  /** The registry root: `<agent dir>/pi-web-bg-tasks`, layered `<sessionId>/<taskId>/` (Q3 A). */
  getLogRoot(): string;
  /** The process-layer spawn; the controller's tests inject a fake. */
  spawnTask(options: BgSupervisorSpawnOptions): BgSupervisorTask;
  /** The environment a task inherits; sanitized before the spawn (Q4 A). */
  baseEnv?: NodeJS.ProcessEnv;
  now?(): number;
  getSession(sessionId: string): BgWrapperLike | undefined;
  resolveSessionPath(sessionId: string): Promise<string | null>;
  reopenSession(sessionId: string, sessionFile: string): Promise<BgWrapperLike>;
  /** The per-task log cap; defaults to the ticket's 64 MiB. */
  logMaxBytes?: number;
}

export interface BgRuntimeSink {
  /** One task of this session reached a terminal state. */
  terminal: (sessionId: string, task: BgTaskInfo) => void;
  /** The session's task list changed. */
  update: (sessionId: string, tasks: BgTaskInfo[]) => void;
}

export interface BgTaskController {
  /** Starts a task; returns its id at once. Refused past the per-session cap or over the deletion gate. */
  run(sessionId: string, cwd: string, command: string): BgTaskInfo;
  /** The session's tasks, one task when `taskId` names it; a task of another session reads as not found. */
  status(sessionId: string, taskId?: string): BgTaskInfo[];
  /** The tail of a task's log with its byte range (AC2). */
  readOutput(sessionId: string, taskId: string, maxBytes?: number): BgTaskOutput;
  /** Stops the task's process group; idempotent (AC2). */
  stop(sessionId: string, taskId: string): BgTaskInfo;
  /** Marks sessions as being deleted: no new task starts, no completion wake (Q8). */
  beginSessionDeletion(sessionIds: readonly string[]): void;
  /** Stops every task of the sessions and clears their log directories. */
  deleteSessionTasks(sessionIds: readonly string[]): Promise<void>;
  /** True while the session runs a task or owes a completion notification (AC3's idle hold). */
  hasActiveWork(sessionId: string): boolean;
  setSink(sink: BgRuntimeSink | null): void;
}

export function createBgTaskController(deps: BgRuntimeDeps): BgTaskController {
  void deps;
  const zero: BgTaskInfo = {
    id: "", sessionId: "", command: "", cwd: "", status: "interrupted",
    startedAt: 0, endedAt: null, exitCode: null, signal: null, reason: null,
    bytesWritten: null, logPath: "", notified: true,
  };
  return {
    run: () => zero,
    status: () => [],
    readOutput: () => ({ text: "", startByte: 0, endByte: 0, totalBytes: 0, logPath: "", truncated: false }),
    stop: () => zero,
    beginSessionDeletion: () => {},
    deleteSessionTasks: async () => {},
    hasActiveWork: () => false,
    setSink: () => {},
  };
}
