import { closeSync, mkdirSync, openSync, readdirSync, readFileSync, readSync, renameSync, rmSync, statSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { randomUUID } from "node:crypto";
import { sanitizeProjectCommandEnvironment } from "./project-command-env";
import { spawnBgSupervisedTask, type BgSupervisorSpawnOptions, type BgSupervisorTask } from "./bg-supervisor";
import { registerSessionLivenessProvider } from "./session-liveness";
import {
  BG_LOG_MAX_BYTES,
  BG_MAX_CONCURRENT_PER_SESSION,
  BG_TASK_NOTIFICATION_TYPE,
  bgTaskNotificationText,
  type BgTaskInfo,
  type BgTaskOutput,
  type BgTaskStatus,
} from "./bg-protocol";

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
      message: { customType: string; content: string | unknown[]; display: boolean; details?: unknown },
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
  /** The wake's idle-wait poll interval (default 500 ms). */
  wakeIdlePollMs?: number;
  /** The wake's idle-wait deadline (default 10 min): past it the notification is sent anyway, queued as a follow-up. */
  wakeIdleTimeoutMs?: number;
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

interface InternalTask {
  info: BgTaskInfo;
  supervisor: BgSupervisorTask | null;
  /** Set when this process saw the task end live and still owes the wake. */
  wakePending: boolean;
}

const TERMINAL_STATUSES: ReadonlySet<BgTaskStatus> = new Set(["succeeded", "failed", "stopped", "interrupted"]);

const WAKE_IDLE_POLL_MS = 500;
const WAKE_IDLE_TIMEOUT_MS = 10 * 60_000;

const sleep = (ms: number): Promise<void> => new Promise((resolve) => { setTimeout(resolve, ms); });

function taskDir(root: string, sessionId: string, taskId: string): string {
  return join(root, sessionId, taskId);
}

function taskJsonPath(root: string, sessionId: string, taskId: string): string {
  return join(taskDir(root, sessionId, taskId), "task.json");
}

/** Atomic per-task record write: a temp file beside the target, renamed into place. */
function persist(root: string, info: BgTaskInfo): void {
  const dir = taskDir(root, info.sessionId, info.id);
  mkdirSync(dir, { recursive: true });
  const target = taskJsonPath(root, info.sessionId, info.id);
  const temp = `${target}.tmp`;
  writeFileSync(temp, JSON.stringify(info), "utf8");
  renameSync(temp, target);
}

function isTaskInfo(value: unknown): value is BgTaskInfo {
  if (typeof value !== "object" || value === null) return false;
  const info = value as Partial<BgTaskInfo>;
  return typeof info.id === "string" && info.id.length > 0
    && typeof info.sessionId === "string" && info.sessionId.length > 0
    && typeof info.command === "string"
    && typeof info.cwd === "string"
    && typeof info.status === "string"
    && typeof info.startedAt === "number"
    && (info.endedAt === null || typeof info.endedAt === "number")
    && (info.exitCode === null || typeof info.exitCode === "number")
    && (info.signal === null || typeof info.signal === "string")
    && (info.reason === null || info.reason === "log-limit" || info.reason === "stopped")
    && (info.bytesWritten === null || typeof info.bytesWritten === "number")
    && typeof info.logPath === "string"
    && typeof info.notified === "boolean"
    && (info.pgid === undefined || info.pgid === null || typeof info.pgid === "number");
}

/**
 * What a previous pi-web process left behind: every persisted record still
 * `running` belongs to a process group nobody owns any more — it reads as
 * `interrupted`, is persisted as such, and is never signalled or woken (AC4).
 */
function recoverInterrupted(root: string, now: () => number): Map<string, InternalTask> {
  const tasks = new Map<string, InternalTask>();
  let sessions: string[];
  try {
    sessions = readdirSync(root);
  } catch {
    return tasks;
  }
  for (const sessionId of sessions) {
    let taskIds: string[];
    try {
      taskIds = readdirSync(join(root, sessionId));
    } catch {
      continue;
    }
    for (const taskId of taskIds) {
      let info: unknown;
      try {
        info = JSON.parse(readFileSync(taskJsonPath(root, sessionId, taskId), "utf8"));
      } catch {
        continue;
      }
      if (!isTaskInfo(info) || info.status !== "running") continue;
      const interrupted: BgTaskInfo = { ...info, status: "interrupted", endedAt: now(), notified: true };
      try {
        persist(root, interrupted);
      } catch {
        // A registry entry that cannot be rewritten still reads as interrupted in memory.
      }
      tasks.set(interrupted.id, { info: interrupted, supervisor: null, wakePending: false });
    }
  }
  return tasks;
}

export function createBgTaskController(deps: BgRuntimeDeps): BgTaskController {
  const root = deps.getLogRoot();
  const now = deps.now ?? (() => Date.now());
  const logMaxBytes = deps.logMaxBytes ?? BG_LOG_MAX_BYTES;
  const tasks = recoverInterrupted(root, now);
  const deletedSessions = new Set<string>();
  const livenessDisposers = new Map<string, () => void>();
  let sink: BgRuntimeSink | null = null;

  const infosOf = (sessionId: string): BgTaskInfo[] =>
    [...tasks.values()].filter((task) => task.info.sessionId === sessionId).map((task) => ({ ...task.info }));

  const find = (sessionId: string, taskId: string): InternalTask => {
    const task = tasks.get(taskId);
    if (!task || task.info.sessionId !== sessionId) {
      throw new Error(`Background task not found: ${taskId}`);
    }
    return task;
  };

  function emitUpdate(sessionId: string): void {
    sink?.update(sessionId, infosOf(sessionId));
  }

  /** The session counts as alive to the idle timer while work or an owed wake exists (AC3). */
  function refreshLiveness(sessionId: string): void {
    const active = [...tasks.values()].some(
      (task) => task.info.sessionId === sessionId
        && (task.info.status === "running" || task.wakePending),
    );
    if (active && !livenessDisposers.has(sessionId)) {
      const dispose = registerSessionLivenessProvider({
        name: "pi-web-bg-tasks",
        sessionId,
        isActive: () => [...tasks.values()].some(
          (task) => task.info.sessionId === sessionId
            && (task.info.status === "running" || task.wakePending),
        ),
      });
      livenessDisposers.set(sessionId, dispose);
    } else if (!active && livenessDisposers.has(sessionId)) {
      livenessDisposers.get(sessionId)!();
      livenessDisposers.delete(sessionId);
    }
  }

  /**
   * The completion wake (AC1 / Q5 A / Q8): one custom message with
   * `triggerTurn`, queued as a follow-up when the session is mid-turn. The
   * `notified` flag is persisted before anything is sent, so a restart or a
   * crash mid-delivery can never deliver a second copy; a session under the
   * deletion gate counts as notified with nothing sent.
   */
  async function deliverCompletion(task: InternalTask): Promise<void> {
    const info = task.info;
    try {
      if (deletedSessions.has(info.sessionId)) return;
      let wrapper = deps.getSession(info.sessionId);
      if (!wrapper?.isAlive()) {
        const sessionFile = await deps.resolveSessionPath(info.sessionId);
        if (!sessionFile) return; // the session is gone: nothing to wake
        wrapper = await deps.reopenSession(info.sessionId, sessionFile);
      }
      await wrapper.waitUntilReady?.();
      // A session mid-turn (a reply streaming, a compaction running) gets the
      // notification after its turn ends, never inside it (AC1); a turn that
      // outlasts the deadline still gets it, queued as a follow-up.
      const pollMs = deps.wakeIdlePollMs ?? WAKE_IDLE_POLL_MS;
      const deadline = Date.now() + (deps.wakeIdleTimeoutMs ?? WAKE_IDLE_TIMEOUT_MS);
      while (wrapper.isAlive() && wrapper.isRunning() && Date.now() < deadline) {
        await sleep(pollMs);
      }
      await wrapper.inner.sendCustomMessage({
        customType: BG_TASK_NOTIFICATION_TYPE,
        content: bgTaskNotificationText(info),
        display: true,
      }, { deliverAs: "followUp", triggerTurn: true });
    } catch (error) {
      console.error(
        "[pi-web] failed to deliver background task completion:",
        error instanceof Error ? error.message : error,
      );
    } finally {
      task.wakePending = false;
      refreshLiveness(info.sessionId);
    }
  }

  function settleTerminal(task: InternalTask, end: {
    exitCode: number | null; signal: string | null; bytesWritten: number | null; reason: "log-limit" | "stopped" | null;
  }): void {
    if (TERMINAL_STATUSES.has(task.info.status)) return; // terminal once
    if (tasks.get(task.info.id) !== task) return; // deleted meanwhile: no registry resurrection
    const succeeded = end.reason === null && end.signal === null && end.exitCode === 0;
    task.info = {
      ...task.info,
      status: end.reason === "stopped" ? "stopped" : succeeded ? "succeeded" : "failed",
      endedAt: now(),
      exitCode: end.exitCode,
      signal: end.signal,
      reason: end.reason,
      bytesWritten: end.bytesWritten,
    };
    task.wakePending = true;
    // The once flag goes to disk before anything is delivered (AC1).
    persist(root, { ...task.info, notified: true });
    task.info = { ...task.info, notified: true };
    sink?.terminal(task.info.sessionId, { ...task.info });
    emitUpdate(task.info.sessionId);
    void deliverCompletion(task);
  }

  return {
    run(sessionId, cwd, command) {
      if (deletedSessions.has(sessionId)) {
        throw new Error(`Session ${sessionId} is being deleted; no background task can start`);
      }
      const running = [...tasks.values()].filter(
        (task) => task.info.sessionId === sessionId && task.info.status === "running",
      );
      if (running.length >= BG_MAX_CONCURRENT_PER_SESSION) {
        throw new Error(
          `This session already runs ${running.length} background tasks (limit ${BG_MAX_CONCURRENT_PER_SESSION}); wait for one to end or stop it first`,
        );
      }
      const id = randomUUID();
      const logPath = join(taskDir(root, sessionId, id), "log");
      const supervisor = deps.spawnTask({
        command,
        cwd,
        env: sanitizeProjectCommandEnvironment(deps.baseEnv ?? process.env),
        logPath,
        logMaxBytes,
      });
      const info: BgTaskInfo = {
        id,
        sessionId,
        command,
        cwd,
        status: "running",
        startedAt: now(),
        endedAt: null,
        exitCode: null,
        signal: null,
        reason: null,
        bytesWritten: null,
        logPath,
        notified: false,
        pgid: supervisor.pgid,
      };
      const task: InternalTask = { info, supervisor, wakePending: false };
      try {
        persist(root, info);
      } catch (error) {
        // A registry that cannot be written must not leak a running group.
        void supervisor.stop().catch(() => undefined);
        throw error;
      }
      tasks.set(id, task);
      void supervisor.done.then((end) => settleTerminal(task, end));
      emitUpdate(sessionId);
      refreshLiveness(sessionId);
      return { ...task.info };
    },

    status(sessionId, taskId) {
      if (taskId === undefined) return infosOf(sessionId);
      return [{ ...find(sessionId, taskId).info }];
    },

    readOutput(sessionId, taskId, maxBytes) {
      const task = find(sessionId, taskId);
      let totalBytes = 0;
      try {
        totalBytes = statSync(task.info.logPath).size;
      } catch {
        totalBytes = 0;
      }
      const budget = typeof maxBytes === "number" && Number.isFinite(maxBytes) && maxBytes > 0
        ? Math.floor(maxBytes)
        : 8192;
      let startByte = Math.max(0, totalBytes - budget);
      let text = "";
      if (startByte < totalBytes) {
        const fd = openSync(task.info.logPath, "r");
        try {
          const buffer = Buffer.alloc(totalBytes - startByte);
          readSync(fd, buffer, 0, buffer.length, startByte);
          // A tail may begin mid-character: skip a leading partial UTF-8 sequence.
          let skip = 0;
          while (skip < buffer.length && skip < 3) {
            const byte = buffer[skip];
            if (byte < 0x80 || byte >= 0xc0) break;
            skip += 1;
          }
          startByte += skip;
          text = buffer.subarray(skip).toString("utf8");
        } finally {
          closeSync(fd);
        }
      }
      return {
        text,
        startByte,
        endByte: startByte + Buffer.byteLength(text),
        totalBytes,
        logPath: task.info.logPath,
        truncated: startByte > 0,
      };
    },

    stop(sessionId, taskId) {
      const task = find(sessionId, taskId);
      if (TERMINAL_STATUSES.has(task.info.status)) return { ...task.info };
      const stopped: BgTaskInfo = { ...task.info, status: "stopped", endedAt: now(), reason: "stopped" };
      task.info = stopped;
      task.wakePending = true;
      persist(root, { ...stopped, notified: true });
      task.info = { ...task.info, notified: true };
      sink?.terminal(sessionId, { ...task.info });
      emitUpdate(sessionId);
      void task.supervisor?.stop().catch(() => undefined);
      void deliverCompletion(task);
      return { ...task.info };
    },

    beginSessionDeletion(sessionIds) {
      for (const sessionId of sessionIds) deletedSessions.add(sessionId);
    },

    async deleteSessionTasks(sessionIds) {
      for (const sessionId of sessionIds) {
        for (const task of [...tasks.values()]) {
          if (task.info.sessionId !== sessionId) continue;
          if (task.info.status === "running") {
            try {
              await task.supervisor?.stop();
            } catch {
              // The group is gone; the registry entry goes regardless.
            }
            if (TERMINAL_STATUSES.has(task.info.status) === false) {
              task.info = { ...task.info, status: "stopped", endedAt: now(), reason: "stopped", notified: true };
            }
          }
          tasks.delete(task.info.id);
        }
        try {
          rmSync(join(root, sessionId), { recursive: true, force: true });
        } catch {
          // A log directory already gone needs no work.
        }
        emitUpdate(sessionId);
        refreshLiveness(sessionId);
      }
    },

    hasActiveWork(sessionId) {
      return [...tasks.values()].some(
        (task) => task.info.sessionId === sessionId
          && (task.info.status === "running" || task.wakePending),
      );
    },

    setSink(next) {
      sink = next;
    },
  };
}

/** The default process-layer spawn (tests inject their own). */
export function defaultSpawnTask(options: BgSupervisorSpawnOptions): BgSupervisorTask {
  return spawnBgSupervisedTask(options);
}
